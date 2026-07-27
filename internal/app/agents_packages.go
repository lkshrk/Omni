package app

import (
	"slices"
	"sort"

	"github.com/lkshrk/omni/internal/agent"
	"github.com/lkshrk/omni/internal/config"
)

type resolvedPackage struct {
	config.SkillPackage
	Groups []string
}

func normalizeConfiguredSkillPackage(pkg config.SkillPackage) config.SkillPackage {
	parsed, err := agent.ParseSkillSource(pkg.Source)
	if err != nil {
		return pkg
	}
	pkg.Source = parsed.Source
	if pkg.Ref == "" {
		pkg.Ref = parsed.Ref
	}
	if len(parsed.Skills) > 0 {
		for _, skill := range parsed.Skills {
			if !slices.Contains(pkg.Skills, skill) {
				pkg.Skills = append(pkg.Skills, skill)
			}
		}
	}
	return pkg
}

// Every ungrouped package plus every package a host-active group references, deduped by source.
func (a *App) resolveSkillPackages(cfg *config.RootConfig, hostname string) []resolvedPackage {
	groupedSources := make(map[string]struct{})
	for _, g := range cfg.Groups {
		if g == nil {
			continue
		}
		for _, src := range g.Skills {
			groupedSources[a.skillSourceIdentity(src)] = struct{}{}
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
			source := a.skillSourceIdentity(src)
			activeRefs[source] = append(activeRefs[source], g.BaseName())
		}
	}

	normalized := make([]config.SkillPackage, 0, len(cfg.Agents.Packages))
	for _, p := range cfg.Agents.Packages {
		p = normalizeConfiguredSkillPackage(p)
		normalized, _ = upsertPackage(normalized, p)
	}

	out := make([]resolvedPackage, 0, len(normalized))
	for _, p := range normalized {
		identity := a.skillSourceIdentity(p.Source)
		_, grouped := groupedSources[identity]
		groups, active := activeRefs[identity]
		if grouped && !active {
			continue
		}
		sort.Strings(groups)
		out = append(out, resolvedPackage{SkillPackage: p, Groups: groups})
	}
	return out
}
