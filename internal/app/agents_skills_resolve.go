package app

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/agent"
	"github.com/lkshrk/omni/internal/config"
)

type SkillDriftStrategy string

const (
	SkillDriftUseManaged SkillDriftStrategy = "use_managed"
	SkillDriftUseLocal   SkillDriftStrategy = "use_local"
)

// ResolveSkillDriftOptions — Source may carry an @skill selector; Agents defaults to every drifted agent.
type ResolveSkillDriftOptions struct {
	Source   string
	Agents   []string
	Strategy SkillDriftStrategy
	DryRun   bool
}

type SkillDriftResolution struct {
	Source   string
	Skill    string
	Agents   []string
	Actions  []string
	Warnings []string
}

// ResolveSkillDrift — UseLocal only narrows the manifest: upstream owns a package's content, so omni cannot adopt a hand-edited copy.
func (a *App) ResolveSkillDrift(ctx context.Context, opts ResolveSkillDriftOptions) (SkillDriftResolution, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return SkillDriftResolution{}, err
	}
	if err := a.requireSkillsEnabled(cfg); err != nil {
		return SkillDriftResolution{}, err
	}
	parsed, err := a.parseSkillPackage(strings.TrimSpace(opts.Source))
	if err != nil {
		return SkillDriftResolution{}, err
	}
	skill := ""
	if len(parsed.Skills) > 0 {
		skill = parsed.Skills[0]
	}
	pkg, ok := a.resolvedSkillPackage(cfg, parsed.Source)
	if !ok {
		return SkillDriftResolution{}, fmt.Errorf("skill package %q is not in this host's manifest", parsed.Source)
	}
	targets := a.restoreTargetsFor(cfg, pkg)
	if len(targets) == 0 {
		return SkillDriftResolution{}, fmt.Errorf("skill package %q has no target agents on this host", pkg.Source)
	}
	service, err := a.skillService()
	if err != nil {
		return SkillDriftResolution{}, err
	}
	inventory, err := service.Inventory(ctx, pkg.Source, targets)
	if err != nil {
		return SkillDriftResolution{}, err
	}
	if skill != "" && len(inventory.Skills) > 0 && !slices.Contains(inventory.Skills, skill) {
		return SkillDriftResolution{}, fmt.Errorf("skill %q is not part of package %s", skill, pkg.Source)
	}
	selected, err := driftedResolveTargets(pkg.Source, targets, inventory, opts.Agents)
	if err != nil {
		return SkillDriftResolution{}, err
	}
	res := SkillDriftResolution{Source: pkg.Source, Skill: skill, Agents: selected}
	if opts.Strategy == SkillDriftUseLocal {
		return a.resolveSkillDriftUseLocal(res, pkg, skill, selected, targets, inventory.Skills, opts.DryRun)
	}
	return a.resolveSkillDriftUseManaged(ctx, res, service, pkg, skill, selected, targets, opts.DryRun)
}

func (a *App) resolvedSkillPackage(cfg *config.RootConfig, source string) (resolvedPackage, bool) {
	identity := a.skillSourceIdentity(source)
	for _, p := range a.resolveSkillPackages(cfg, currentMachineGroupName()) {
		if a.skillSourceIdentity(p.Source) == identity {
			return p, true
		}
	}
	return resolvedPackage{}, false
}

// Same target set as a restore: narrowing to the drifted agents would unlink the package everywhere else.
func (a *App) restoreTargetsFor(cfg *config.RootConfig, pkg resolvedPackage) []string {
	use := a.effectiveSettings(cfg).AgentsUse
	var detected []string
	if use == nil {
		detected = a.EnabledAgentIDs(cfg)
	}
	resolved := resolveRestoreTargets([]resolvedPackage{pkg}, use, detected)
	return effectiveSkillAgents(nil, resolved[0].SkillPackage)
}

// A package with no canonical content has no inventory, so its whole target set is eligible — but only
// once it is installed; a never-installed one is a sync away, not a manifest entry to narrow.
func driftedResolveTargets(source string, targets []string, inventory agent.SkillInventory, requested []string) ([]string, error) {
	known := make(map[string]bool, len(targets))
	for _, id := range targets {
		known[id] = true
	}
	uninstalled := fmt.Errorf(
		"skill package %s is not installed on any target agent, so nothing has drifted; run \"omni agents skills sync\"", source)
	eligible := len(inventory.Skills) == 0 && skillInstalledSomewhere(inventory)
	if len(requested) == 0 {
		var drifted []string
		for _, id := range targets {
			if inventory.PerTargetDrifted[id] || inventory.PerTargetShadowed[id] {
				drifted = append(drifted, id)
			}
		}
		if len(drifted) > 0 {
			return drifted, nil
		}
		if eligible {
			return append([]string(nil), targets...), nil
		}
		if len(inventory.Skills) == 0 {
			return nil, uninstalled
		}
		return nil, fmt.Errorf("skill package %s is not drifted on any target agent", source)
	}
	var out []string
	for _, id := range requested {
		if !known[id] {
			return nil, fmt.Errorf("skill package %s does not target agent %q", source, id)
		}
		if !eligible && !inventory.PerTargetDrifted[id] && !inventory.PerTargetShadowed[id] {
			if len(inventory.Skills) == 0 {
				return nil, uninstalled
			}
			return nil, fmt.Errorf("skill package %s is not drifted on %s", source, id)
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out, nil
}

func skillInstalledSomewhere(inventory agent.SkillInventory) bool {
	if inventory.Installed {
		return true
	}
	for _, installed := range inventory.PerTarget {
		if installed {
			return true
		}
	}
	for _, drifted := range inventory.PerTargetDrifted {
		if drifted {
			return true
		}
	}
	for _, shadowed := range inventory.PerTargetShadowed {
		if shadowed {
			return true
		}
	}
	return false
}

func (a *App) resolveSkillDriftUseManaged(
	ctx context.Context,
	res SkillDriftResolution,
	service *agent.Skills,
	pkg resolvedPackage,
	skill string,
	selected, targets []string,
	dryRun bool,
) (SkillDriftResolution, error) {
	var skills []string
	if skill != "" {
		skills = []string{skill}
	}
	entries, err := service.DriftedEntries(pkg.Source, selected, skills)
	if err != nil {
		return res, err
	}
	for _, id := range selected {
		if len(entries[id]) == 0 {
			res.Actions = append(res.Actions, fmt.Sprintf("install %s for %s", pkg.Source, id))
			continue
		}
		for _, path := range entries[id] {
			res.Actions = append(res.Actions, fmt.Sprintf("replace %s with omni's managed link for %s", path, id))
		}
	}
	if dryRun {
		return res, nil
	}
	warnings, err := service.ResolveDrift(ctx, pkg.SkillPackage, targets, agent.SkillDriftSelection{
		Targets: selected,
		Skills:  skills,
	})
	res.Warnings = append(res.Warnings, warnings...)
	if err != nil {
		return res, fmt.Errorf("resolving %s: %w", pkg.Source, err)
	}
	return res, nil
}

// A named skill narrows selectors (package-wide; there is no per-target selector), an unnamed one narrows targets.
func (a *App) resolveSkillDriftUseLocal(
	res SkillDriftResolution,
	pkg resolvedPackage,
	skill string,
	selected, targets, installed []string,
	dryRun bool,
) (SkillDriftResolution, error) {
	identity := a.skillSourceIdentity(pkg.Source)
	var apply func(entry config.SkillPackage) config.SkillPackage
	if skill != "" {
		selectors := pkg.Skills
		pinned := false
		if len(selectors) == 0 {
			if len(installed) == 0 {
				return res, fmt.Errorf(
					"cannot drop skill %q from %s: the package selects every skill and none is installed, so the remaining set is unknown",
					skill, pkg.Source)
			}
			selectors = installed
			pinned = true
		}
		remaining := slices.DeleteFunc(append([]string(nil), selectors...), func(name string) bool { return name == skill })
		if len(remaining) == 0 {
			return res, fmt.Errorf(
				"%s only provides skill %q; dropping it would leave the package empty — run \"omni agents skills remove %s\" instead",
				pkg.Source, skill, pkg.Source)
		}
		sort.Strings(remaining)
		res.Actions = append(res.Actions, fmt.Sprintf(
			"stop managing skill %q of %s (selectors: %s)", skill, pkg.Source, strings.Join(remaining, ", ")))
		if pinned {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"%s now pins its skill list; a skill added upstream will not install until you add it", pkg.Source))
		}
		apply = func(entry config.SkillPackage) config.SkillPackage {
			entry.Skills = remaining
			return entry
		}
	} else {
		configured := pkg.Agents
		pinned := false
		if len(configured) == 0 {
			configured = targets
			pinned = true
		}
		remaining := slices.DeleteFunc(append([]string(nil), configured...), func(id string) bool {
			return slices.Contains(selected, id)
		})
		if len(remaining) == 0 {
			return res, fmt.Errorf(
				"%s targets only %s; dropping them would leave the package with no agents — run \"omni agents skills remove %s\" instead",
				pkg.Source, strings.Join(selected, ", "), pkg.Source)
		}
		res.Actions = append(res.Actions, fmt.Sprintf(
			"stop managing %s on %s (agents: %s)", pkg.Source, strings.Join(selected, ", "), strings.Join(remaining, ", ")))
		if pinned {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"%s now pins its agent list; an agent enabled later will not receive it until you add it", pkg.Source))
		}
		apply = func(entry config.SkillPackage) config.SkillPackage {
			entry.Agents = remaining
			return entry
		}
	}
	if dryRun {
		return res, nil
	}
	err := a.withConfig(func(cfg *config.RootConfig) error {
		for i, entry := range cfg.Agents.Packages {
			if a.skillSourceIdentity(entry.Source) != identity {
				continue
			}
			cfg.Agents.Packages[i] = apply(normalizeConfiguredSkillPackage(entry))
			return nil
		}
		return fmt.Errorf("skill package %q not found", pkg.Source)
	})
	return res, err
}
