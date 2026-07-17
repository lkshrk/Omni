package app

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
)

// SkillInstaller materializes one skill package onto the host for the given
// agents. The npx/bunx wrapper is the only implementation today; a Go-native
// installer can replace it behind this interface later.
type SkillInstaller interface {
	Install(ctx context.Context, pkg config.SkillPackage, agents []string) error
}

func skillRunner(nodeManager string) string {
	if nodeManager == "bun" {
		return "bunx"
	}
	return "npx"
}

func skillPackageSource(pkg config.SkillPackage) string {
	if pkg.Ref != "" {
		return pkg.Source + "#" + pkg.Ref
	}
	return pkg.Source
}

// skillPackageAddArgs builds `skills add <source>[#ref] -g [-a agents...] -y`.
func skillPackageAddArgs(pkg config.SkillPackage, agents []string) []string {
	args := []string{"skills", "add", skillPackageSource(pkg), "-g"}
	if len(agents) > 0 {
		args = append(args, "-a")
		args = append(args, agents...)
	}
	return append(args, "-y")
}

// npxInstaller runs the upstream skills CLI via npx/bunx.
type npxInstaller struct {
	runner string
	exec   func(ctx context.Context, name string, args ...string) (string, string, error)
}

func (n npxInstaller) Install(ctx context.Context, pkg config.SkillPackage, agents []string) error {
	args := skillPackageAddArgs(pkg, agents)
	stdout, stderr, err := n.exec(ctx, n.runner, args...)
	if err != nil {
		return fmt.Errorf("skills add %s: %w: %s", pkg.Source, err, stderr)
	}
	if err := skillsCLIFailure("skills add "+pkg.Source, stdout, stderr); err != nil {
		return err
	}
	return verifySkillInstalled(pkg.Source)
}

// skillsCLIFailureMarkers are substrings the skills CLI prints when an
// operation fails despite a zero exit. Verified against vercel-labs/skills
// (main, 2026-07): per-skill add failures print "Failed to install ${N}" plus
// "✗ skill → agent: err" lines (src/add.ts), remove prints "Failed to remove
// ${N} skill(s)" (src/remove.ts), update prints "✗ Failed to update ${name}"
// (src/update.ts) — and none of these set process.exitCode, so the process
// exits 0 (src/cli.ts: `process.exit(process.exitCode ?? 0)`). Only a
// top-level clone/parse error exits 1. picocolors drops ANSI codes on
// non-TTY output, so plain substring matching holds through the executor.
// Marker checking catches the case verifySkillInstalled cannot: a failed
// REinstall whose stale lock entries from an earlier install still satisfy
// the effect check, plus update/remove operations that have no effect check
// at all. "✘" is kept defensively for glyph drift.
var skillsCLIFailureMarkers = []string{"Failed to", "✗", "✘"}

func skillsCLIFailure(op, stdout, stderr string) error {
	for _, m := range skillsCLIFailureMarkers {
		if strings.Contains(stdout, m) || strings.Contains(stderr, m) {
			if detail := skillsFailureDetail(stdout, stderr); detail != "" {
				return fmt.Errorf("%s exited 0 but reported failure: %s", op, detail)
			}
			return fmt.Errorf("%s exited 0 but reported failure (%q); treating as failed", op, m)
		}
	}
	return nil
}

// skillsFailureDetail extracts the CLI's own failure lines so the surfaced
// error explains why the operation failed, not just that a marker matched.
func skillsFailureDetail(stdout, stderr string) string {
	var lines []string
	for _, line := range strings.Split(stdout+"\n"+stderr, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		for _, m := range skillsCLIFailureMarkers {
			if strings.Contains(trimmed, m) {
				lines = append(lines, trimmed)
				break
			}
		}
	}
	const maxLines = 4
	if len(lines) > maxLines {
		lines = append(lines[:maxLines], "…")
	}
	return strings.Join(lines, "; ")
}

// SkillsCLIOutputIndicatesFailure reports whether output from the skills CLI
// contains a known failure marker. Exported for the real-CLI canary
// integration test, which guards the markers against upstream wording drift
// (the CLI is invoked via npx unpinned, so releases change under us).
func SkillsCLIOutputIndicatesFailure(stdout, stderr string) bool {
	return skillsCLIFailure("skills", stdout, stderr) != nil
}

// verifySkillInstalled guards against the skills CLI exiting 0 on a failed
// install — the same upstream-CLI contract already hit with `claude plugin`
// (see plugin_claude_adapter.go's failure markers). Rather than guessing at
// output markers for a CLI whose failure text is unverified, check the
// effect: a successful `skills add` writes the package's entries to the
// global lockfile, so their absence after a zero exit means the install did
// not happen. Entries left by an earlier install satisfy this check, so a
// failed exit-0 REinstall printing a failure marker is caught by
// skillsAddFailure instead; a marker-less silent one would need version/hash
// comparison against the resolved ref to detect.
func verifySkillInstalled(source string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolving home dir: %w", err)
	}
	lock, err := config.LoadSkillLock(config.SkillLockPath(home))
	if err != nil {
		return err
	}
	if len(packageSkills(lock, source)) == 0 {
		return fmt.Errorf("skills add %s exited 0 but wrote no lockfile entries; treating as failed install", source)
	}
	return nil
}

// SkillFailure records one skill that failed to install.
type SkillFailure struct {
	Name    string
	Message string
}

// RestoreSkillsResult summarises a restore run.
type RestoreSkillsResult struct {
	Installed []string
	Failed    []SkillFailure
	Drift     []string
	Warnings  []string
	// ShadowedByPlugin lists package sources skipped because an installed
	// plugin's name matches the package's repo-segment name on one of its
	// target agents — a user-scope duplicate of a plugin-provided skill is
	// harm, not repair (see SkillPackageRow.ShadowedByPlugin).
	ShadowedByPlugin []string
}

// filterShadowedSkillPackages splits pkgs into those to install and those
// skipped because an installed plugin already provides them on a target
// agent (see skillPackageRepoName/installedPluginNames). use is the host's
// enabled-agents fallback, mirroring effectiveSkillAgents' resolution so the
// shadow check considers the same agent set restore would actually install
// to.
func filterShadowedSkillPackages(pkgs []resolvedPackage, use []string, pluginNames map[string]map[string]bool) (keep []resolvedPackage, shadowed []string) {
	for _, p := range pkgs {
		repoName := skillPackageRepoName(p.Source)
		agents := effectiveSkillAgents(use, p.SkillPackage)
		isShadowed := false
		for _, id := range agents {
			if pluginShadowsName(pluginNames, id, repoName) {
				isShadowed = true
				break
			}
		}
		if isShadowed {
			shadowed = append(shadowed, p.Source)
			continue
		}
		keep = append(keep, p)
	}
	return keep, shadowed
}

// shadowCheckAgents resolves the agent set the shadow filter must consider.
// A nil use list makes the install omit -a and lets the CLI auto-detect
// agents, but handed straight to filterShadowedSkillPackages it would iterate
// nothing and shadow nothing — so fall back to the detected agents, the same
// set the auto-detecting install would target (mirrors SkillPackageRows).
func (a *App) shadowCheckAgents(cfg *config.RootConfig, use []string) []string {
	if use != nil {
		return use
	}
	return a.EnabledAgentIDs(cfg)
}

func restoreSkills(ctx context.Context, pkgs []resolvedPackage, use []string, inst SkillInstaller) RestoreSkillsResult {
	var res RestoreSkillsResult
	for _, p := range pkgs {
		agents := effectiveSkillAgents(use, p.SkillPackage)
		if err := inst.Install(ctx, p.SkillPackage, agents); err != nil {
			res.Failed = append(res.Failed, SkillFailure{Name: p.Source, Message: err.Error()})
			continue
		}
		res.Installed = append(res.Installed, p.Source)
	}
	return res
}

// effectiveSkillAgents resolves which agents a package installs to: the host's
// enabled agents (use), narrowed to the package's own agents when it declares
// any. A nil use list means "not configured" — restore omits -a and lets the
// CLI auto-detect installed agents.
// A non-nil empty use list means "no agents enabled" — the intersection is empty and the package installs to nothing.
func effectiveSkillAgents(use []string, pkg config.SkillPackage) []string {
	if len(pkg.Agents) == 0 {
		return use
	}
	if use == nil {
		return pkg.Agents
	}
	enabled := make(map[string]bool, len(use))
	for _, a := range use {
		enabled[a] = true
	}
	out := make([]string, 0, len(pkg.Agents))
	for _, a := range pkg.Agents {
		if enabled[a] {
			out = append(out, a)
		}
	}
	return out
}

// RestoreSkillsOptions controls a restore run.
type RestoreSkillsOptions struct {
	DryRun bool
}

func dryRunLines(runner string, pkgs []resolvedPackage, use []string) []string {
	lines := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		args := skillPackageAddArgs(p.SkillPackage, effectiveSkillAgents(use, p.SkillPackage))
		lines = append(lines, runner+" "+strings.Join(args, " "))
	}
	return lines
}

func nodeManager(cfg *config.RootConfig) string {
	return cfg.Settings.Ecosystems[provider.EcosystemNode].Manager
}

// unconfiguredHostSkillsWarning reports when group-assigned skill packages are
// silently excluded from a restore because the current host is not registered
// in cfg.Hosts (doctorHost surfaces the same state) — without it, "no active
// groups" and "host never bootstrapped" are indistinguishable to the user.
func unconfiguredHostSkillsWarning(cfg *config.RootConfig) string {
	if _, ok := activeHostGroupNames(cfg, currentMachineGroupName()); ok {
		return ""
	}
	for _, g := range cfg.Groups {
		if g != nil && len(g.Skills) > 0 {
			return "this host is not configured (run setup); skill packages assigned to groups are excluded from restore"
		}
	}
	return ""
}

// RestoreSkills restores the resolved package set onto this host.
func (a *App) RestoreSkills(ctx context.Context, opts RestoreSkillsOptions) (RestoreSkillsResult, []string, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return RestoreSkillsResult{}, nil, err
	}
	if err := a.requireAgentsEnabled(cfg); err != nil {
		return RestoreSkillsResult{}, nil, err
	}
	if config.BoolVal(a.effectiveSettings(cfg).SkillsDisabled) {
		return RestoreSkillsResult{Warnings: []string{"skills are disabled for this host, skipping restore"}}, nil, nil
	}
	runner := skillRunner(nodeManager(cfg))
	pkgs := resolveSkillPackages(cfg, currentMachineGroupName())
	use := a.effectiveSettings(cfg).AgentsUse
	pluginNames, warnings := installedPluginNames(ctx, a)
	if w := unconfiguredHostSkillsWarning(cfg); w != "" {
		warnings = append(warnings, w)
	}
	pkgs, shadowedSources := filterShadowedSkillPackages(pkgs, a.shadowCheckAgents(cfg, use), pluginNames)
	if opts.DryRun {
		return RestoreSkillsResult{ShadowedByPlugin: shadowedSources, Warnings: warnings}, dryRunLines(runner, pkgs, use), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return RestoreSkillsResult{}, nil, fmt.Errorf("resolving home dir: %w", err)
	}
	before, err := lockHashes(home)
	if err != nil {
		return RestoreSkillsResult{}, nil, err
	}
	inst := npxInstaller{runner: runner, exec: a.fallbackExecutor().Run}
	res := restoreSkills(ctx, pkgs, use, inst)
	res.ShadowedByPlugin = shadowedSources
	res.Warnings = append(res.Warnings, warnings...)
	after, err := lockHashes(home)
	if err != nil {
		return RestoreSkillsResult{}, nil, err
	}
	res.Drift = skillDrift(before, after)
	return res, nil, nil
}

// ImportDiff reports what an import changed.
type ImportDiff struct {
	Added     []string
	Updated   []string
	Unchanged []string
}

// importPackages folds lockfile entries into the flat package manifest, deduped
// by source. New sources are appended ungrouped; existing sources update Ref
// when the lockfile differs.
func importPackages(existing []config.SkillPackage, lock *config.SkillLockFile) ([]config.SkillPackage, ImportDiff) {
	bySource := make(map[string]config.SkillPackage, len(existing))
	order := make([]string, 0, len(existing))
	for _, p := range existing {
		bySource[p.Source] = p
		order = append(order, p.Source)
	}

	lockBySource := make(map[string]string)
	sources := make([]string, 0)
	for _, e := range lock.Skills {
		if e.Source == "" {
			continue
		}
		if _, ok := lockBySource[e.Source]; !ok {
			lockBySource[e.Source] = e.Ref
			sources = append(sources, e.Source)
		}
	}
	sort.Strings(sources)

	var diff ImportDiff
	for _, src := range sources {
		ref := lockBySource[src]
		prev, ok := bySource[src]
		switch {
		case !ok:
			bySource[src] = config.SkillPackage{Source: src, Ref: ref}
			order = append(order, src)
			diff.Added = append(diff.Added, src)
		case prev.Ref != ref:
			prev.Ref = ref
			bySource[src] = prev
			diff.Updated = append(diff.Updated, src)
		default:
			diff.Unchanged = append(diff.Unchanged, src)
		}
	}

	merged := make([]config.SkillPackage, 0, len(order))
	for _, src := range order {
		merged = append(merged, bySource[src])
	}
	return merged, diff
}

// ImportSkillsOptions controls an import run.
type ImportSkillsOptions struct {
	DryRun bool
}

// ImportSkills ingests CLI/UI-added skills from the live lockfile into the
// manifest. With DryRun it computes the diff but does not write.
func (a *App) ImportSkills(ctx context.Context, opts ImportSkillsOptions) (ImportDiff, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return ImportDiff{}, err
	}
	if err := a.requireSkillsEnabled(cfg); err != nil {
		return ImportDiff{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ImportDiff{}, fmt.Errorf("resolving home dir: %w", err)
	}
	lock, err := config.LoadSkillLock(config.SkillLockPath(home))
	if err != nil {
		return ImportDiff{}, err
	}

	var diff ImportDiff
	if opts.DryRun {
		_, diff = importPackages(cfg.Agents.Packages, lock)
		return diff, nil
	}

	err = a.withConfig(func(cfg *config.RootConfig) error {
		merged, d := importPackages(cfg.Agents.Packages, lock)
		cfg.Agents.Packages = merged
		diff = d
		return nil
	})
	return diff, err
}

// skillDrift returns the names whose folder hash changed (or newly appeared)
// between two lockfile snapshots. Names absent from `after` are ignored.
func skillDrift(before, after map[string]string) []string {
	var changed []string
	for name, h := range after {
		prev, ok := before[name]
		if ok && prev != "" && prev != h {
			changed = append(changed, name)
		}
	}
	sort.Strings(changed)
	return changed
}

func lockHashes(home string) (map[string]string, error) {
	lock, err := config.LoadSkillLock(config.SkillLockPath(home))
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(lock.Skills))
	for name, e := range lock.Skills {
		out[name] = e.SkillFolderHash
	}
	return out, nil
}

// skillUpdateArgs builds `skills update -g -y [names...]`. With names it targets
// exactly those skills; with none it updates all global skills.
func skillUpdateArgs(names []string) []string {
	args := []string{"skills", "update", "-g", "-y"}
	return append(args, names...)
}

// skillPackageRemoveArgs builds `skills remove -g [-a agents...] -y <names...>`.
// Mirrors skillPackageAddArgs' global scope and agent targeting.
func skillPackageRemoveArgs(skillNames, agents []string) []string {
	args := []string{"skills", "remove", "-g"}
	if len(agents) > 0 {
		args = append(args, "-a")
		args = append(args, agents...)
	}
	args = append(args, "-y")
	return append(args, skillNames...)
}

// agentsWithPackageSkills returns sorted agent IDs where any of the package's
// lockfile skill names are present in that agent's skills dirs.
func agentsWithPackageSkills(home string, lock *config.SkillLockFile, source string) []string {
	names := packageSkills(lock, source)
	if len(names) == 0 {
		return nil
	}
	var agents []string
	for _, ag := range InstalledAgents(home) {
		if agentHasAnySkill(home, ag, names) {
			agents = append(agents, ag.ID)
		}
	}
	sort.Strings(agents)
	return agents
}

// packageSkillNames returns the lockfile skill names whose source matches one of
// the resolved packages, sorted and unique.
func packageSkillNames(lock *config.SkillLockFile, pkgs []resolvedPackage) []string {
	want := make(map[string]struct{}, len(pkgs))
	for _, p := range pkgs {
		want[p.Source] = struct{}{}
	}
	var names []string
	for name, e := range lock.Skills {
		if _, ok := want[e.Source]; ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// UpdateSkillsOptions controls an update run.
type UpdateSkillsOptions struct {
	DryRun bool
}

// UpdateSkills refreshes the resolved package skills to their latest upstream
// versions via the skills CLI. DryRun returns the command that would run.
func (a *App) UpdateSkills(ctx context.Context, opts UpdateSkillsOptions) (output string, dryRun string, err error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return "", "", err
	}
	if err := a.requireSkillsEnabled(cfg); err != nil {
		return "", "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("resolving home dir: %w", err)
	}
	lock, err := config.LoadSkillLock(config.SkillLockPath(home))
	if err != nil {
		return "", "", err
	}
	pkgs := resolveSkillPackages(cfg, currentMachineGroupName())
	runner := skillRunner(nodeManager(cfg))
	args := skillUpdateArgs(packageSkillNames(lock, pkgs))
	if opts.DryRun {
		return "", runner + " " + strings.Join(args, " "), nil
	}
	stdout, stderr, err := a.fallbackExecutor().Run(ctx, runner, args...)
	if err != nil {
		return stdout, "", fmt.Errorf("skills update: %w: %s", err, stderr)
	}
	if err := skillsCLIFailure("skills update", stdout, stderr); err != nil {
		return stdout, "", err
	}
	return stdout, "", nil
}
