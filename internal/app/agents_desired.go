package app

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/config"
)

type desiredAgentPackage struct {
	config.SkillPackage
	Targets []string
}

// resolveDesiredAgentPackages maps the legacy host-scoped resolution (ungrouped packages plus packages referenced by a host-active group) onto APM install targets. Warnings cover entries excluded for this host.
func (a *App) resolveDesiredAgentPackages(cfg *config.RootConfig) ([]desiredAgentPackage, []string) {
	warnings := make([]string, 0)
	if w := unconfiguredHostSkillsWarning(cfg); w != "" {
		warnings = append(warnings, w)
	}
	use := a.effectiveSettings(cfg).AgentsUse
	defaultTargets, unsupportedDefaults := migrateAPMTargets(a.effectiveAgentTargets(cfg))
	if len(unsupportedDefaults) > 0 {
		warnings = append(warnings, fmt.Sprintf("agent targets without an APM equivalent are skipped: %s", strings.Join(unsupportedDefaults, ", ")))
	}

	resolved := a.resolveSkillPackages(cfg, currentMachineGroupName())
	ignored, _, _, _ := a.AgentsIgnoreSet(cfg)
	out := make([]desiredAgentPackage, 0, len(resolved))
	for _, pkg := range resolved {
		if ignored[skillPackageRepoName(pkg.Source)] {
			continue
		}
		agents := pkg.Agents
		if use != nil {
			agents = effectiveSkillAgents(use, pkg.SkillPackage)
			if len(agents) == 0 {
				warnings = append(warnings, fmt.Sprintf("package %q skipped: its targets are disabled by agents_use", pkg.Source))
				continue
			}
		}
		targets := slices.Clone(defaultTargets)
		if len(agents) > 0 {
			var unsupported []string
			targets, unsupported = migrateAPMTargets(agents)
			if len(unsupported) > 0 {
				warnings = append(warnings, fmt.Sprintf("package %q: agent targets without an APM equivalent are skipped: %s", pkg.Source, strings.Join(unsupported, ", ")))
			}
			if len(targets) == 0 {
				warnings = append(warnings, fmt.Sprintf("package %q skipped: none of its agent targets exist in APM", pkg.Source))
				continue
			}
		}
		if len(pkg.Skills) > 0 {
			warnings = append(warnings, fmt.Sprintf("package %q: APM installs the whole package; the skills subset selection is ignored", pkg.Source))
		}
		sort.Strings(targets)
		out = append(out, desiredAgentPackage{SkillPackage: pkg.SkillPackage, Targets: targets})
	}
	return out, warnings
}

func (p desiredAgentPackage) manifestEntry() apmPackageDependency {
	return apmPackageDependency{
		Git:     strings.TrimSpace(p.Source),
		Ref:     strings.TrimSpace(p.Ref),
		Targets: p.Targets,
	}
}

func desiredPackageEntries(desired []desiredAgentPackage) []apmPackageDependency {
	entries := make([]apmPackageDependency, 0, len(desired))
	for _, pkg := range desired {
		entries = append(entries, pkg.manifestEntry())
	}
	return entries
}

// unionAPMTargets — one invocation reconciles every declared package, so every target it should reach must be selected at once; per-dependency targets narrow the reach from there.
func unionAPMTargets(desired []desiredAgentPackage) []string {
	union := make([]string, 0)
	for _, pkg := range desired {
		for _, target := range pkg.Targets {
			if !slices.Contains(union, target) {
				union = append(union, target)
			}
		}
	}
	sort.Strings(union)
	return union
}

// Identities normalize exactly as the generator writes them, and a group-inactive package stays omni's to retire rather than foreign.
func managedPackageIdentities(cfg *config.RootConfig) map[string]bool {
	managed := make(map[string]bool, len(cfg.Agents.Packages))
	for _, pkg := range cfg.Agents.Packages {
		managed[apmPackageIdentityOf(pkg.Source)] = true
	}
	return managed
}

func apmPackageIdentityOf(source string) string {
	return normalizeAPMPackageIdentity(normalizeConfiguredSkillPackage(config.SkillPackage{Source: source}).Source)
}
