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

// SkillInstaller materializes one manifest skill onto the host for the given
// agents. The npx/bunx wrapper is the only implementation today; a Go-native
// installer can replace it behind this interface later.
type SkillInstaller interface {
	Install(ctx context.Context, skill config.ManifestSkill, agents []string) error
}

func skillRunner(nodeManager string) string {
	if nodeManager == "bun" {
		return "bunx"
	}
	return "npx"
}

func skillSource(skill config.ManifestSkill) string {
	if skill.Ref != "" {
		return skill.Source + "#" + skill.Ref
	}
	return skill.Source
}

// skillAddArgs builds the argument vector for `<runner> skills add ...`.
func skillAddArgs(skill config.ManifestSkill, agents []string) []string {
	args := []string{"skills", "add", skillSource(skill), "-s", skill.Name, "-g"}
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

func (n npxInstaller) Install(ctx context.Context, skill config.ManifestSkill, agents []string) error {
	args := skillAddArgs(skill, agents)
	_, stderr, err := n.exec(ctx, n.runner, args...)
	if err != nil {
		return fmt.Errorf("skills add %s: %w: %s", skill.Name, err, stderr)
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
}

func restoreSkills(ctx context.Context, skills []config.ManifestSkill, use []string, inst SkillInstaller) RestoreSkillsResult {
	var res RestoreSkillsResult
	for _, skill := range skills {
		if err := inst.Install(ctx, skill, effectiveSkillAgents(use, skill)); err != nil {
			res.Failed = append(res.Failed, SkillFailure{Name: skill.Name, Message: err.Error()})
			continue
		}
		res.Installed = append(res.Installed, skill.Name)
	}
	return res
}

// effectiveSkillAgents resolves which agents a skill installs to: the host's
// enabled agents (use), narrowed to the skill's own agents when it declares
// any. A nil use list means "not configured" — restore omits -a and lets the
// CLI auto-detect installed agents.
func effectiveSkillAgents(use []string, skill config.ManifestSkill) []string {
	if len(skill.Agents) == 0 {
		return use
	}
	if use == nil {
		return skill.Agents
	}
	enabled := make(map[string]bool, len(use))
	for _, a := range use {
		enabled[a] = true
	}
	out := make([]string, 0, len(skill.Agents))
	for _, a := range skill.Agents {
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

func dryRunLines(runner string, skills []config.ManifestSkill, use []string) []string {
	lines := make([]string, 0, len(skills))
	for _, skill := range skills {
		args := skillAddArgs(skill, effectiveSkillAgents(use, skill))
		lines = append(lines, runner+" "+strings.Join(args, " "))
	}
	return lines
}

func nodeManager(cfg *config.RootConfig) string {
	return cfg.Settings.Ecosystems[provider.EcosystemNode].Manager
}

// RestoreSkills restores the manifest skill set onto this host.
func (a *App) RestoreSkills(ctx context.Context, opts RestoreSkillsOptions) (RestoreSkillsResult, []string, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return RestoreSkillsResult{}, nil, err
	}
	if err := a.requireAgentsEnabled(cfg); err != nil {
		return RestoreSkillsResult{}, nil, err
	}
	runner := skillRunner(nodeManager(cfg))
	skills := cfg.Agents.Skills
	use := a.effectiveSettings(cfg).AgentsUse
	if opts.DryRun {
		return RestoreSkillsResult{}, dryRunLines(runner, skills, use), nil
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
	res := restoreSkills(ctx, skills, use, inst)
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

// importSkills upserts lockfile entries into the manifest, dropping runtime
// fields. Existing entries change only when source/ref/skillPath differ.
func importSkills(existing []config.ManifestSkill, lock *config.SkillLockFile) ([]config.ManifestSkill, ImportDiff) {
	byName := make(map[string]config.ManifestSkill, len(existing))
	order := make([]string, 0, len(existing))
	for _, s := range existing {
		byName[s.Name] = s
		order = append(order, s.Name)
	}

	var diff ImportDiff
	names := make([]string, 0, len(lock.Skills))
	for name := range lock.Skills {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		e := lock.Skills[name]
		next := config.ManifestSkill{Name: name, Source: e.Source, Ref: e.Ref, SkillPath: e.SkillPath}
		prev, ok := byName[name]
		switch {
		case !ok:
			next.Agents = nil
			byName[name] = next
			order = append(order, name)
			diff.Added = append(diff.Added, name)
		case prev.Source != next.Source || prev.Ref != next.Ref || prev.SkillPath != next.SkillPath:
			next.Agents = prev.Agents
			byName[name] = next
			diff.Updated = append(diff.Updated, name)
		default:
			diff.Unchanged = append(diff.Unchanged, name)
		}
	}

	merged := make([]config.ManifestSkill, 0, len(order))
	for _, name := range order {
		merged = append(merged, byName[name])
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
	if err := a.requireAgentsEnabled(cfg); err != nil {
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
		_, diff = importSkills(cfg.Agents.Skills, lock)
		return diff, nil
	}

	err = a.withConfig(func(cfg *config.RootConfig) error {
		merged, d := importSkills(cfg.Agents.Skills, lock)
		cfg.Agents.Skills = merged
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

// ListSkills returns the declared manifest skills.
func (a *App) ListSkills() ([]config.ManifestSkill, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	return cfg.Agents.Skills, nil
}

// skillUpdateArgs builds `<runner> skills update -g -y [names...]`. With manifest
// names it targets exactly those; with none it updates all global skills. The
// upstream CLI checks each skill's folder hash against the latest upstream tree
// SHA and only refreshes the ones that changed.
func skillUpdateArgs(skills []config.ManifestSkill) []string {
	args := []string{"skills", "update", "-g", "-y"}
	for _, s := range skills {
		args = append(args, s.Name)
	}
	return args
}

// UpdateSkillsOptions controls an update run.
type UpdateSkillsOptions struct {
	DryRun bool
}

// UpdateSkills refreshes the manifest skills to their latest upstream versions
// via the skills CLI (which performs the outdated check internally). DryRun
// returns the command that would run instead of executing it.
func (a *App) UpdateSkills(ctx context.Context, opts UpdateSkillsOptions) (output string, dryRun string, err error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return "", "", err
	}
	if err := a.requireAgentsEnabled(cfg); err != nil {
		return "", "", err
	}
	runner := skillRunner(nodeManager(cfg))
	args := skillUpdateArgs(cfg.Agents.Skills)
	if opts.DryRun {
		return "", runner + " " + strings.Join(args, " "), nil
	}
	stdout, stderr, err := a.fallbackExecutor().Run(ctx, runner, args...)
	if err != nil {
		return stdout, "", fmt.Errorf("skills update: %w: %s", err, stderr)
	}
	return stdout, "", nil
}
