package app

import (
	"sort"

	"github.com/lkshrk/omni/internal/config"
)

// resolvedPackage is a package selected for the active host with the active
// group memberships that selected it (for display badges).
type resolvedPackage struct {
	config.SkillPackage
	Groups []string
}

// resolveSkillPackages returns the packages to restore on hostname: every
// ungrouped package (referenced by no group) plus every package referenced by
// a group active on the host. Deduped by source, first-seen order preserved.
func resolveSkillPackages(cfg *config.RootConfig, hostname string) []resolvedPackage {
	groupedSources := make(map[string]struct{})
	for _, g := range cfg.Groups {
		if g == nil {
			continue
		}
		for _, src := range g.Skills {
			groupedSources[src] = struct{}{}
		}
	}

	activeNames, _ := activeHostGroupNames(cfg, hostname)
	activeSet := make(map[string]struct{}, len(activeNames))
	for _, n := range activeNames {
		activeSet[n] = struct{}{}
	}
	activeRefs := make(map[string][]string)
	for _, g := range cfg.Groups {
		if g == nil {
			continue
		}
		if _, ok := activeSet[g.BaseName()]; !ok {
			continue
		}
		for _, src := range g.Skills {
			activeRefs[src] = append(activeRefs[src], g.BaseName())
		}
	}

	out := make([]resolvedPackage, 0, len(cfg.Agents.Packages))
	for _, p := range cfg.Agents.Packages {
		_, grouped := groupedSources[p.Source]
		groups, active := activeRefs[p.Source]
		if grouped && !active {
			continue
		}
		sort.Strings(groups)
		out = append(out, resolvedPackage{SkillPackage: p, Groups: groups})
	}
	return out
}
