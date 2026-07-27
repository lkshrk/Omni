package app

import (
	"context"
	"fmt"
)

// A failing ListPlugins is a warning, not an error: a silently skipped adapter makes shadowed packages read as unshadowed.
func installedPluginNames(ctx context.Context, a *App) (map[string]map[string]bool, []string) {
	out := make(map[string]map[string]bool)
	var warnings []string
	for _, adapter := range a.pluginAdapters() {
		if !adapter.Available() {
			continue
		}
		listed, err := adapter.ListPlugins(ctx)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("plugin-shadow check skipped for %s: listing plugins failed: %v", adapter.ID(), err))
			continue
		}
		names := make(map[string]bool, len(listed))
		for _, p := range listed {
			names[p.Name] = true
		}
		out[adapter.ID()] = names
	}
	return out, warnings
}

func pluginShadowsName(byAgent map[string]map[string]bool, agentID, name string) bool {
	return byAgent[agentID][name]
}

// UnmanagedSkillPackages and ImportSkills must apply the identical rule, or a consent prompt counts a different set than the import ingests.
func pluginProvidingLockPackage(home, source string, names []string, agents []AgentInfo, pluginNames map[string]map[string]bool) (string, bool) {
	repoName := skillPackageRepoName(source)
	for _, ag := range agents {
		if agentHasAnySkill(home, ag, names) && pluginShadowsName(pluginNames, ag.ID, repoName) {
			return ag.ID, true
		}
	}
	return "", false
}
