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

// resolveDesiredAgentPackages maps the legacy host-scoped resolution (ungrouped packages plus packages
// referenced by a host-active group) onto APM install targets. Warnings cover entries excluded for this host.
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
	out := make([]desiredAgentPackage, 0, len(resolved))
	for _, pkg := range resolved {
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

func (p desiredAgentPackage) installSpec() string {
	if strings.TrimSpace(p.Ref) != "" {
		return p.Source + "#" + p.Ref
	}
	return p.Source
}

// groupDesiredByTargets batches packages sharing a target set into one apm install invocation, preserving order.
func groupDesiredByTargets(desired []desiredAgentPackage) [][]desiredAgentPackage {
	byKey := make(map[string]int)
	batches := make([][]desiredAgentPackage, 0, len(desired))
	for _, pkg := range desired {
		key := strings.Join(pkg.Targets, ",")
		if i, ok := byKey[key]; ok {
			batches[i] = append(batches[i], pkg)
			continue
		}
		byKey[key] = len(batches)
		batches = append(batches, []desiredAgentPackage{pkg})
	}
	return batches
}
