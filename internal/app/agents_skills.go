package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/agent"
	"github.com/lkshrk/omni/internal/config"
)

type SkillInstaller interface {
	Sync(ctx context.Context, pkg config.SkillPackage, agents []string) ([]string, error)
}

type SkillFailure struct {
	Name    string
	Message string
}

type RestoreSkillsResult struct {
	Installed []string
	Failed    []SkillFailure
	Drift     []string
	Warnings  []string
	// A user-scope duplicate of a plugin-provided skill is harm, not repair.
	ShadowedByPlugin []string
}

// use mirrors effectiveSkillAgents so the shadow check sees the agent set restore would install to.
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

// Package-level targets stay authoritative; only unrestricted packages fall back to detected agents.
func resolveRestoreTargets(pkgs []resolvedPackage, use, detected []string) []resolvedPackage {
	out := make([]resolvedPackage, len(pkgs))
	copy(out, pkgs)
	for i := range out {
		pkg := &out[i].SkillPackage
		if use == nil {
			if len(pkg.Agents) == 0 {
				pkg.Agents = append([]string(nil), detected...)
			}
			continue
		}
		pkg.Agents = effectiveSkillAgents(use, *pkg)
	}
	return out
}

func restoreSkills(ctx context.Context, pkgs []resolvedPackage, use []string, inst SkillInstaller) RestoreSkillsResult {
	var res RestoreSkillsResult
	for _, p := range pkgs {
		agents := effectiveSkillAgents(use, p.SkillPackage)
		if len(agents) == 0 {
			continue
		}
		warnings, err := inst.Sync(ctx, p.SkillPackage, agents)
		res.Warnings = append(res.Warnings, warnings...)
		if err != nil {
			res.Failed = append(res.Failed, SkillFailure{Name: p.Source, Message: err.Error()})
			continue
		}
		res.Installed = append(res.Installed, p.Source)
	}
	return res
}

// A non-nil empty use list means no agents enabled, so the package installs to nothing.
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

type RestoreSkillsOptions struct {
	DryRun bool
}

// Sync leaves a drifted target untouched, so a preview that promised an install there would be the opposite of what applying it does.
func dryRunLines(ctx context.Context, pkgs []resolvedPackage, use []string, inventories skillInventoryReader) []string {
	lines := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		agents := effectiveSkillAgents(use, p.SkillPackage)
		if len(agents) == 0 {
			continue
		}
		drifted := driftedSkillTargets(ctx, inventories, p.Source, agents)
		installable := make([]string, 0, len(agents))
		for _, id := range agents {
			if !slices.Contains(drifted, id) {
				installable = append(installable, id)
			}
		}
		for _, id := range drifted {
			lines = append(lines, fmt.Sprintf(
				"skip %s on %s (drifted; another tool owns the skill entry); resolve with %s",
				p.Source, id, skillDriftRemedy))
		}
		if len(installable) == 0 {
			continue
		}
		agents = installable
		line := "install " + p.Source
		if p.Ref != "" {
			line += " at ref " + p.Ref
		}
		if len(p.Skills) > 0 {
			line += " skills " + strings.Join(p.Skills, ",")
		} else {
			line += " all discovered skills"
		}
		line += " for targets " + strings.Join(agents, ",")
		lines = append(lines, line)
	}
	return lines
}

// Without it, no-active-groups and host-never-bootstrapped are indistinguishable.
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

func (a *App) RestoreSkills(ctx context.Context, opts RestoreSkillsOptions) (RestoreSkillsResult, []string, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return RestoreSkillsResult{}, nil, err
	}
	if err := a.requireAgentsEnabled(cfg); err != nil {
		return RestoreSkillsResult{}, nil, err
	}
	if config.BoolVal(a.effectiveSettings(cfg).SkillsDisabled) {
		return RestoreSkillsResult{Warnings: []string{"skills are disabled for this host, skipping sync"}}, nil, nil
	}
	pkgs := a.resolveSkillPackages(cfg, currentMachineGroupName())
	use := a.effectiveSettings(cfg).AgentsUse
	var detected []string
	if use == nil {
		detected = a.EnabledAgentIDs(cfg)
	}
	pkgs = resolveRestoreTargets(pkgs, use, detected)
	pluginNames, warnings := installedPluginNames(ctx, a)
	if w := unconfiguredHostSkillsWarning(cfg); w != "" {
		warnings = append(warnings, w)
	}
	pkgs, shadowedSources := filterShadowedSkillPackages(pkgs, nil, pluginNames)
	service, err := a.skillService()
	if err != nil {
		return RestoreSkillsResult{}, nil, err
	}
	if opts.DryRun {
		return RestoreSkillsResult{ShadowedByPlugin: shadowedSources, Warnings: warnings},
			dryRunLines(ctx, pkgs, nil, service), nil
	}
	res := restoreSkills(ctx, pkgs, nil, service)
	res.ShadowedByPlugin = shadowedSources
	res.Warnings = append(res.Warnings, warnings...)
	res.Drift = skillDriftLines(ctx, pkgs, service)
	a.refreshSkillOutdatedBestEffort(ctx)
	return res, nil, nil
}

type skillInventoryReader interface {
	Inventory(ctx context.Context, source string, targetIDs []string) (agent.SkillInventory, error)
}

// Carried by every drift report so the fix travels with the finding.
const skillDriftRemedy = `"omni agents skills resolve" (--use-managed / --use-local)`

// An unreadable inventory yields no drifted targets: a preview or report must not invent one.
func driftedSkillTargets(ctx context.Context, inventories skillInventoryReader, source string, agents []string) []string {
	inventory, err := inventories.Inventory(ctx, source, agents)
	if err != nil {
		return nil
	}
	var drifted []string
	for _, id := range agents {
		if inventory.PerTargetDrifted[id] {
			drifted = append(drifted, id)
		}
	}
	sort.Strings(drifted)
	return drifted
}

// Converge already adopted the byte-identical copies, so whatever is still drifted needs a human.
func skillDriftLines(ctx context.Context, pkgs []resolvedPackage, inventories skillInventoryReader) []string {
	var lines []string
	for _, p := range pkgs {
		agents := effectiveSkillAgents(nil, p.SkillPackage)
		if len(agents) == 0 {
			continue
		}
		for _, id := range driftedSkillTargets(ctx, inventories, p.Source, agents) {
			lines = append(lines, fmt.Sprintf(
				"%s: drifted on %s (another tool owns the skill entry; left untouched); resolve with %s",
				p.Source, id, skillDriftRemedy))
		}
	}
	return lines
}

type ImportDiff struct {
	Added     []string
	Updated   []string
	Unchanged []string
	Warnings  []string
}

// New sources are appended ungrouped; an already managed source only takes the lockfile's Ref, because
// re-adding its skills would silently undo a selector the user narrowed with "skills resolve --use-local".
func importPackages(existing []config.SkillPackage, lock *config.SkillLockFile) ([]config.SkillPackage, ImportDiff) {
	bySource := make(map[string]config.SkillPackage, len(existing))
	order := make([]string, 0, len(existing))
	var normalized []config.SkillPackage
	for _, p := range existing {
		p = normalizeConfiguredSkillPackage(p)
		normalized, _ = upsertPackage(normalized, p)
	}
	for _, p := range normalized {
		bySource[p.Source] = p
		order = append(order, p.Source)
	}

	lockBySource := make(map[string]string)
	skillsBySource := make(map[string][]string)
	sources := make([]string, 0)
	for name, e := range lock.Skills {
		if e.Source == "" {
			continue
		}
		entry := normalizeConfiguredSkillPackage(config.SkillPackage{
			Source: e.Source,
			Ref:    e.Ref,
		})
		skillsBySource[entry.Source] = append(skillsBySource[entry.Source], name)
		if _, ok := lockBySource[entry.Source]; !ok {
			lockBySource[entry.Source] = entry.Ref
			sources = append(sources, entry.Source)
		}
	}
	sort.Strings(sources)

	var diff ImportDiff
	for _, src := range sources {
		ref := lockBySource[src]
		skills := skillsBySource[src]
		sort.Strings(skills)
		prev, ok := bySource[src]
		switch {
		case !ok:
			bySource[src] = config.SkillPackage{Source: src, Ref: ref, Skills: skills}
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

// ImportSkillsOptions — An empty Source claims every eligible package; naming one turns skip reasons into errors.
type ImportSkillsOptions struct {
	DryRun bool
	Source string
}

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
	full, err := config.LoadSkillLock(config.SkillLockPath(home))
	if err != nil {
		return ImportDiff{}, err
	}
	lock, warnings := a.filterPluginShadowedLock(ctx, cfg, home, full)
	if source := strings.TrimSpace(opts.Source); source != "" {
		lock, err = a.selectImportSource(cfg, full, lock, source)
		if err != nil {
			return ImportDiff{}, err
		}
		// Warnings about deliberately untouched packages are noise once the user named one package.
		warnings = nil
	}

	var diff ImportDiff
	if opts.DryRun {
		_, diff = importPackages(cfg.Agents.Packages, lock)
		diff.Warnings = append(diff.Warnings, warnings...)
		return diff, nil
	}

	var imported []config.SkillPackage
	err = a.withConfig(func(cfg *config.RootConfig) error {
		merged, d := importPackages(cfg.Agents.Packages, lock)
		cfg.Agents.Packages = merged
		diff = d
		imported = lockfilePackages(merged, lock)
		return nil
	})
	if err != nil {
		diff.Warnings = append(diff.Warnings, warnings...)
		return diff, err
	}
	diff.Warnings = append(warnings, a.adoptLegacySkills(ctx, cfg, imported)...)
	return diff, nil
}

// Names the specific reason: nothing-happened is otherwise indistinguishable from a typo.
func (a *App) selectImportSource(
	cfg *config.RootConfig,
	full, filtered *config.SkillLockFile,
	source string,
) (*config.SkillLockFile, error) {
	parsed, err := a.parseSkillPackage(source)
	if err != nil {
		return nil, err
	}
	identity := a.skillSourceIdentity(parsed.Source)
	inLock := false
	for _, entry := range full.Skills {
		if entry.Source != "" && a.skillSourceIdentity(entry.Source) == identity {
			inLock = true
			break
		}
	}
	if !inLock {
		return nil, fmt.Errorf(
			"skill package %q is not in the legacy lockfile; \"omni agents skills import\" only claims packages a CLI already installed",
			parsed.Source)
	}
	for _, p := range cfg.Agents.Packages {
		if a.skillSourceIdentity(p.Source) == identity {
			return nil, fmt.Errorf("skill package %q is already in the manifest", parsed.Source)
		}
	}
	narrowed := *filtered
	narrowed.Skills = make(map[string]config.SkillLockEntry)
	for name, entry := range filtered.Skills {
		if entry.Source != "" && a.skillSourceIdentity(entry.Source) == identity {
			narrowed.Skills[name] = entry
		}
	}
	if len(narrowed.Skills) == 0 {
		return nil, fmt.Errorf(
			"skill package %q is provided by an installed plugin of the same name; importing it would duplicate plugin-managed content",
			parsed.Source)
	}
	return &narrowed, nil
}

// Adopting a plugin-managed directory would hand omni content the plugin keeps rewriting.
func (a *App) filterPluginShadowedLock(ctx context.Context, cfg *config.RootConfig, home string, lock *config.SkillLockFile) (*config.SkillLockFile, []string) {
	candidates := a.unmanagedLockSources(cfg, lock)
	if len(candidates) == 0 {
		return lock, nil
	}
	pluginNames, warnings := installedPluginNames(ctx, a)
	installedAgents := a.installedAgents(home)
	shadowed := make(map[string]bool)
	for _, src := range candidates {
		agentID, ok := pluginProvidingLockPackage(home, src, a.packageSkills(lock, src), installedAgents, pluginNames)
		if !ok {
			continue
		}
		shadowed[src] = true
		warnings = append(warnings, fmt.Sprintf("skipped %s: plugin %q on %s already provides it", src, skillPackageRepoName(src), agentID))
	}
	if len(shadowed) == 0 {
		return lock, warnings
	}
	filtered := *lock
	filtered.Skills = make(map[string]config.SkillLockEntry, len(lock.Skills))
	for name, entry := range lock.Skills {
		if entry.Source != "" && shadowed[a.skillSourceIdentity(entry.Source)] {
			continue
		}
		filtered.Skills[name] = entry
	}
	return &filtered, warnings
}

func lockfilePackages(merged []config.SkillPackage, lock *config.SkillLockFile) []config.SkillPackage {
	sources := make(map[string]bool, len(lock.Skills))
	for _, entry := range lock.Skills {
		if entry.Source == "" {
			continue
		}
		sources[normalizeConfiguredSkillPackage(config.SkillPackage{Source: entry.Source}).Source] = true
	}
	out := make([]config.SkillPackage, 0, len(sources))
	for _, pkg := range merged {
		if sources[pkg.Source] {
			out = append(out, pkg)
		}
	}
	return out
}

// UpdateSkillsOptions — DryRun prints what an update would do; Check probes each source for what is actually behind.
type UpdateSkillsOptions struct {
	DryRun bool
	Check  bool
}

func (a *App) UpdateSkills(ctx context.Context, opts UpdateSkillsOptions) (output string, dryRun string, err error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return "", "", err
	}
	if err := a.requireSkillsEnabled(cfg); err != nil {
		return "", "", err
	}
	if opts.Check {
		out, err := a.checkSkillsOutdated(ctx)
		return out, "", err
	}
	pkgs := a.resolveSkillPackages(cfg, currentMachineGroupName())
	use := a.effectiveSettings(cfg).AgentsUse
	if use == nil {
		pkgs = resolveRestoreTargets(pkgs, nil, a.EnabledAgentIDs(cfg))
	} else {
		pkgs = resolveRestoreTargets(pkgs, use, nil)
	}
	pluginNames, warnings := installedPluginNames(ctx, a)
	pkgs, shadowed := filterShadowedSkillPackages(pkgs, nil, pluginNames)
	prelude := make([]string, 0, len(warnings)+len(shadowed))
	for _, w := range warnings {
		prelude = append(prelude, "warning: "+w)
	}
	for _, source := range shadowed {
		prelude = append(prelude, "skipped "+source+" (provided by plugin)")
	}
	service, err := a.skillService()
	if err != nil {
		return "", "", err
	}
	if opts.DryRun {
		return "", strings.Join(append(prelude, dryRunLines(ctx, pkgs, nil, service)...), "\n"), nil
	}
	output, err = updateSkills(ctx, pkgs, prelude, service)
	// An upgrade converges declared intent, so a contested entry is skipped rather than fatal.
	lines := make([]string, 0, 1)
	if output != "" {
		lines = append(lines, output)
	}
	for _, line := range skillDriftLines(ctx, pkgs, service) {
		lines = append(lines, "drift: "+line)
	}
	return strings.Join(lines, "\n"), "", err
}

// Forces a probe so an explicit check never answers from a stale recorded verdict.
func (a *App) checkSkillsOutdated(ctx context.Context) (string, error) {
	checks, err := a.RefreshSkillOutdated(ctx, true)
	if err != nil {
		return "", err
	}
	lines := make([]string, 0, len(checks))
	for _, c := range checks {
		switch c.Outdated {
		case SkillOutdatedBehind:
			lines = append(lines, "outdated "+c.Source+" (\"omni agents skills upgrade\" refreshes it)")
		case SkillOutdatedCurrent:
			lines = append(lines, "up to date "+c.Source)
		default:
			lines = append(lines, "unknown "+c.Source+" (source has no comparable version)")
		}
	}
	return strings.Join(lines, "\n"), nil
}

type skillUpdater interface {
	Upgrade(ctx context.Context, pkg config.SkillPackage, agents []string) ([]string, error)
	PackageStore(source string) (string, string, error)
}

// Derived from the content the refresh landed, not from a second network probe.
func updateSkillReason(before, after string, err error) string {
	if err != nil || before == "" || after == "" {
		return ""
	}
	if before != after {
		return " (outdated: source content changed)"
	}
	return " (forced: already up to date)"
}

func updateSkills(ctx context.Context, pkgs []resolvedPackage, prelude []string, inst skillUpdater) (string, error) {
	lines := make([]string, 0, len(prelude)+len(pkgs))
	lines = append(lines, prelude...)
	var failures []error
	for _, pkg := range pkgs {
		agents := effectiveSkillAgents(nil, pkg.SkillPackage)
		if len(agents) == 0 {
			continue
		}
		_, before, beforeErr := inst.PackageStore(pkg.Source)
		warnings, err := inst.Upgrade(ctx, pkg.SkillPackage, agents)
		for _, w := range warnings {
			lines = append(lines, "warning: "+w)
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", pkg.Source, err))
			continue
		}
		_, after, afterErr := inst.PackageStore(pkg.Source)
		lines = append(lines, "updated "+pkg.Source+updateSkillReason(before, after, errors.Join(beforeErr, afterErr)))
	}
	return strings.Join(lines, "\n"), errors.Join(failures...)
}
