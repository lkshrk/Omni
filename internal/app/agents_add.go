package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/lkshrk/omni/internal/agent"
	"github.com/lkshrk/omni/internal/config"
)

// A selectorless re-add resets the package to all discovered skills.
func upsertPackage(pkgs []config.SkillPackage, pkg config.SkillPackage) ([]config.SkillPackage, bool) {
	for i := range pkgs {
		if pkgs[i].Source == pkg.Source {
			pkgs[i].Ref = pkg.Ref
			if len(pkg.Skills) == 0 {
				pkgs[i].Skills = nil
			} else {
				for _, skill := range pkg.Skills {
					if !slices.Contains(pkgs[i].Skills, skill) {
						pkgs[i].Skills = append(pkgs[i].Skills, skill)
					}
				}
			}
			return pkgs, false
		}
	}
	return append(pkgs, pkg), true
}

func upsertNormalizedPackages(pkgs []config.SkillPackage, pkg config.SkillPackage) ([]config.SkillPackage, bool) {
	normalized := make([]config.SkillPackage, 0, len(pkgs)+1)
	for _, existing := range pkgs {
		normalized, _ = upsertPackage(normalized, normalizeConfiguredSkillPackage(existing))
	}
	pkg = normalizeConfiguredSkillPackage(pkg)
	return upsertPackage(normalized, pkg)
}

func (a *App) AddSkillPackage(ctx context.Context, input string) (config.SkillPackage, []string, error) {
	pkg, err := a.parseSkillPackage(input)
	if err != nil {
		return config.SkillPackage{}, nil, err
	}
	cfg, err := a.loadConfig()
	if err != nil {
		return config.SkillPackage{}, nil, err
	}
	if err := a.requireSkillsEnabled(cfg); err != nil {
		return config.SkillPackage{}, nil, err
	}
	desired := pkg
	for _, existing := range cfg.Agents.Packages {
		existing = normalizeConfiguredSkillPackage(existing)
		if existing.Source != pkg.Source {
			continue
		}
		merged, _ := upsertPackage([]config.SkillPackage{existing}, pkg)
		desired = merged[0]
		break
	}
	use := a.effectiveSettings(cfg).AgentsUse
	var agents []string
	if use == nil && len(desired.Agents) > 0 {
		agents = append([]string(nil), desired.Agents...)
	} else {
		if use == nil {
			use = a.EnabledAgentIDs(cfg)
		}
		agents = effectiveSkillAgents(use, desired)
	}
	if len(agents) == 0 {
		return config.SkillPackage{}, nil, fmt.Errorf("no enabled agent targets selected")
	}
	pluginNames, warnings := installedPluginNames(ctx, a)
	repoName := skillPackageRepoName(desired.Source)
	for _, id := range agents {
		if pluginShadowsName(pluginNames, id, repoName) {
			return config.SkillPackage{}, warnings, fmt.Errorf(
				"skill package %q is provided by an installed plugin of the same name on %s; installing it would duplicate plugin-managed content",
				pkg.Source, id)
		}
	}
	service, err := a.skillService()
	if err != nil {
		return config.SkillPackage{}, warnings, err
	}
	refreshWarnings, err := service.Refresh(ctx, desired, agents)
	warnings = append(warnings, refreshWarnings...)
	if err != nil {
		return config.SkillPackage{}, warnings, fmt.Errorf("installing skill package %s: %w", pkg.Source, err)
	}

	if err := a.withConfig(func(c *config.RootConfig) error {
		merged, _ := upsertNormalizedPackages(c.Agents.Packages, pkg)
		c.Agents.Packages = merged
		return nil
	}); err != nil {
		return config.SkillPackage{}, warnings, fmt.Errorf("installed %s but failed to save to manifest (re-run to persist): %w", pkg.Source, err)
	}
	return pkg, warnings, nil
}

// IsSkillPackageNotInstalled — Composed callers treat a not-installed uninstall as already done, not a failure.
func IsSkillPackageNotInstalled(err error) bool {
	var notInstalled *agent.NotInstalledError
	return errors.As(err, &notInstalled)
}

// UninstallSkillPackage — Removes installed content only; RemoveSkillPackage unregisters from the manifest.
func (a *App) UninstallSkillPackage(ctx context.Context, source string) error {
	parsed, err := a.parseSkillPackage(source)
	if err != nil {
		return err
	}
	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	if err := a.requireSkillsEnabled(cfg); err != nil {
		return err
	}
	identity := a.skillSourceIdentity(parsed.Source)
	storedSource := parsed.Source
	for _, pkg := range cfg.Agents.Packages {
		if a.skillSourceIdentity(pkg.Source) == identity {
			storedSource = normalizeConfiguredSkillPackage(pkg).Source
			break
		}
	}
	service, err := a.skillService()
	if err != nil {
		return err
	}
	if _, err := service.Remove(ctx, storedSource, a.allAgentTargetIDs()); err != nil {
		return err
	}
	return a.withConfig(func(c *config.RootConfig) error {
		c.Agents.Ignore.Skills = slices.DeleteFunc(c.Agents.Ignore.Skills, func(s string) bool {
			return a.skillSourceIdentity(s) == identity
		})
		return nil
	})
}

// RemoveSkillPackage — Manifest-only; UninstallSkillPackage removes the files.
func (a *App) RemoveSkillPackage(source string) error {
	parsed, err := a.parseSkillPackage(source)
	if err != nil {
		return err
	}
	source = parsed.Source
	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	if err := a.requireSkillsEnabled(cfg); err != nil {
		return err
	}
	identity := a.skillSourceIdentity(source)
	found := false
	storedSource := source
	for _, p := range cfg.Agents.Packages {
		if a.skillSourceIdentity(p.Source) == identity {
			found = true
			storedSource = normalizeConfiguredSkillPackage(p).Source
			break
		}
	}
	if !found {
		return fmt.Errorf("skill package %q not found in manifest", source)
	}
	return a.withConfig(func(c *config.RootConfig) error {
		c.Agents.Packages = slices.DeleteFunc(c.Agents.Packages, func(p config.SkillPackage) bool {
			return a.skillSourceIdentity(p.Source) == identity
		})
		a.setSkillGroupsInConfig(c, storedSource, map[string]struct{}{})
		c.Agents.Ignore.Skills = slices.DeleteFunc(c.Agents.Ignore.Skills, func(s string) bool {
			return a.skillSourceIdentity(s) == identity
		})
		return nil
	})
}

// AdoptSkillPackage — Manifest upsert only: the next restore converges or installs the recorded intent.
func (a *App) AdoptSkillPackage(source string) (config.SkillPackage, error) {
	parsed, err := a.parseSkillPackage(source)
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
	home, err := os.UserHomeDir()
	if err != nil {
		return config.SkillPackage{}, err
	}
	lock, err := config.LoadSkillLock(config.SkillLockPath(home))
	if err != nil {
		return config.SkillPackage{}, err
	}
	source = parsed.Source
	identity := a.skillSourceIdentity(source)
	ref := ""
	var skills []string
	for _, e := range lock.Skills {
		entry := normalizeConfiguredSkillPackage(config.SkillPackage{Source: e.Source, Ref: e.Ref})
		if a.skillSourceIdentity(entry.Source) != identity {
			continue
		}
		if entry.Ref != "" && ref == "" {
			ref = entry.Ref
		}
	}
	for name, e := range lock.Skills {
		if a.skillSourceIdentity(e.Source) == identity && !slices.Contains(skills, name) {
			skills = append(skills, name)
		}
	}
	slices.Sort(skills)
	pkg := config.SkillPackage{Source: source, Ref: ref, Skills: skills}
	if err := a.withConfig(func(c *config.RootConfig) error {
		merged, _ := upsertNormalizedPackages(c.Agents.Packages, pkg)
		c.Agents.Packages = merged
		return nil
	}); err != nil {
		return config.SkillPackage{}, fmt.Errorf("failed to save %s to manifest: %w", source, err)
	}
	return pkg, nil
}
