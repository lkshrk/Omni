package app

import (
	"context"
	"fmt"
	"strings"
)

type projectedPluginNamesKey struct{}

type projectedPluginNames struct {
	names    map[string]map[string]bool
	warnings []string
}

func withProjectedPluginNames(ctx context.Context, names map[string]map[string]bool, warnings []string) context.Context {
	if names == nil {
		return ctx
	}
	return context.WithValue(ctx, projectedPluginNamesKey{}, projectedPluginNames{names: names, warnings: warnings})
}

func projectPluginInstalls(ctx context.Context, names map[string]map[string]bool, warnings []string, result RestorePluginResult) context.Context {
	for agentID, observed := range result.observedNames {
		names[agentID] = observed
	}
	for _, pair := range append(result.Installed, result.WouldInstall...) {
		agentID, name, ok := strings.Cut(pair, "/")
		if !ok {
			continue
		}
		if names[agentID] == nil {
			names[agentID] = make(map[string]bool)
		}
		names[agentID][name] = true
	}
	return withProjectedPluginNames(ctx, names, warnings)
}

// A failing ListPlugins is a warning, not an error: a silently skipped adapter makes shadowed packages read as unshadowed.
func installedPluginNames(ctx context.Context, a *App) (map[string]map[string]bool, []string) {
	if projected, ok := ctx.Value(projectedPluginNamesKey{}).(projectedPluginNames); ok {
		return projected.names, projected.warnings
	}
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
