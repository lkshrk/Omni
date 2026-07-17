package app

import (
	"context"
	"fmt"
	"os"
	"slices"

	"github.com/lkshrk/omni/internal/config"
)

// upsertPackage adds source to the manifest (ungrouped) or updates its ref when
// already present. Returns the new slice and whether a new package was added.
func upsertPackage(pkgs []config.SkillPackage, source, ref string) ([]config.SkillPackage, bool) {
	for i := range pkgs {
		if pkgs[i].Source == source {
			pkgs[i].Ref = ref
			return pkgs, false
		}
	}
	return append(pkgs, config.SkillPackage{Source: source, Ref: ref}), true
}

// AddSkillPackage registers a package in the manifest (ungrouped) and installs
// it via the skills CLI. input may be owner/repo, owner/repo@skill, owner/repo#ref,
// or a github URL; the @skill component is stripped (repo-level identity).
func (a *App) AddSkillPackage(ctx context.Context, input string) (config.SkillPackage, error) {
	source, ref, err := normalizeSkillSource(input)
	if err != nil {
		return config.SkillPackage{}, err
	}
	cfg, err := a.loadConfig()
	if err != nil {
		return config.SkillPackage{}, err
	}
	if err := a.requireSkillsEnabled(cfg); err != nil {
		return config.SkillPackage{}, err
	}
	pkg := config.SkillPackage{Source: source, Ref: ref}
	agents := effectiveSkillAgents(a.effectiveSettings(cfg).AgentsUse, pkg)
	runner := skillRunner(nodeManager(cfg))

	args := skillPackageAddArgs(pkg, agents)
	stdout, stderr, err := a.fallbackExecutor().Run(ctx, runner, args...)
	if err != nil {
		return config.SkillPackage{}, fmt.Errorf("skills add %s: %w: %s", source, err, stderr)
	}
	if err := skillsCLIFailure("skills add "+source, stdout, stderr); err != nil {
		return config.SkillPackage{}, err
	}
	if err := verifySkillInstalled(source); err != nil {
		return config.SkillPackage{}, err
	}

	if err := a.withConfig(func(c *config.RootConfig) error {
		merged, _ := upsertPackage(c.Agents.Packages, source, ref)
		c.Agents.Packages = merged
		return nil
	}); err != nil {
		return config.SkillPackage{}, fmt.Errorf("installed %s but failed to save to manifest (re-run to persist): %w", source, err)
	}
	return pkg, nil
}

// UninstallSkillPackage removes a lockfile package from disk via the skills
// CLI. It does not modify the manifest; use RemoveSkillPackage for that.
func (a *App) UninstallSkillPackage(ctx context.Context, source string) error {
	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	if err := a.requireSkillsEnabled(cfg); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolving home dir: %w", err)
	}
	lock, err := config.LoadSkillLock(config.SkillLockPath(home))
	if err != nil {
		return err
	}
	names := packageSkills(lock, source)
	if len(names) == 0 {
		return fmt.Errorf("skill package %q has no lockfile entries to uninstall", source)
	}
	agents := agentsWithPackageSkills(home, lock, source)
	if len(agents) == 0 {
		return fmt.Errorf("skill package %q is not installed on any detected agent", source)
	}
	runner := skillRunner(nodeManager(cfg))
	args := skillPackageRemoveArgs(names, agents)
	rmStdout, rmStderr, err := a.fallbackExecutor().Run(ctx, runner, args...)
	if err != nil {
		return fmt.Errorf("skills remove %s: %w: %s", source, err, rmStderr)
	}
	if err := skillsCLIFailure("skills remove "+source, rmStdout, rmStderr); err != nil {
		return err
	}
	return a.withConfig(func(c *config.RootConfig) error {
		c.Agents.Ignore.Skills = slices.DeleteFunc(c.Agents.Ignore.Skills, func(s string) bool {
			return s == source
		})
		return nil
	})
}

// RemoveSkillPackage unregisters a manifest package. Manifest-only — use
// UninstallSkillPackage to remove the package's files from agent skill dirs.
func (a *App) RemoveSkillPackage(source string) error {
	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	if err := a.requireSkillsEnabled(cfg); err != nil {
		return err
	}
	found := false
	for _, p := range cfg.Agents.Packages {
		if p.Source == source {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("skill package %q not found in manifest", source)
	}
	return a.withConfig(func(c *config.RootConfig) error {
		c.Agents.Packages = slices.DeleteFunc(c.Agents.Packages, func(p config.SkillPackage) bool {
			return p.Source == source
		})
		setSkillGroupsInConfig(c, source, map[string]struct{}{})
		c.Agents.Ignore.Skills = slices.DeleteFunc(c.Agents.Ignore.Skills, func(s string) bool {
			return s == source
		})
		return nil
	})
}

// AdoptSkillPackage records a lockfile-only package (installed on disk but
// absent from the manifest) into the manifest. Manifest upsert only — no
// reinstall — the skills CLI already installed it, so re-running it would be
// redundant.
func (a *App) AdoptSkillPackage(source string) (config.SkillPackage, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return config.SkillPackage{}, err
	}
	if err := a.requireSkillsEnabled(cfg); err != nil {
		return config.SkillPackage{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return config.SkillPackage{}, err
	}
	lock, err := config.LoadSkillLock(config.SkillLockPath(home))
	if err != nil {
		return config.SkillPackage{}, err
	}
	ref := ""
	for _, e := range lock.Skills {
		if e.Source == source && e.Ref != "" {
			ref = e.Ref
			break
		}
	}
	pkg := config.SkillPackage{Source: source, Ref: ref}
	if err := a.withConfig(func(c *config.RootConfig) error {
		merged, _ := upsertPackage(c.Agents.Packages, source, ref)
		c.Agents.Packages = merged
		return nil
	}); err != nil {
		return config.SkillPackage{}, fmt.Errorf("failed to save %s to manifest: %w", source, err)
	}
	return pkg, nil
}
