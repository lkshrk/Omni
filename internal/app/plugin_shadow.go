package app

import "context"

// installedPluginNames returns, per agent ID, the set of plugin names
// installed on that agent — used to detect unmanaged skills/mcp servers that
// are actually provided by an installed plugin of the same name (plugin
// servers/skills are plugin-scoped and never appear in an agent's user-scope
// config, so they otherwise look permanently "missing"/unmanaged).
//
// This calls ListPlugins on every available plugin adapter, duplicating the
// exec calls PluginRows already makes. The TUI loads McpServerRows/
// SkillPackageRows and PluginRows via separate tea.Cmd (see doLoadMcpRows,
// doLoadPluginRows, loadSkillsManifestCmd), so they already run as
// independent subprocess calls; this adds one more `list plugins` exec per
// adapter per mcp/skills row build. Accepted as the cost of shadow detection
// without plumbing a cache across independently-scheduled row builders.
func installedPluginNames(ctx context.Context, a *App) map[string]map[string]bool {
	out := make(map[string]map[string]bool)
	for _, adapter := range a.pluginAdapters() {
		if !adapter.Available() {
			continue
		}
		listed, err := adapter.ListPlugins(ctx)
		if err != nil {
			continue
		}
		names := make(map[string]bool, len(listed))
		for _, p := range listed {
			names[p.Name] = true
		}
		out[adapter.ID()] = names
	}
	return out
}

// pluginShadowsName reports whether name is provided by an installed plugin
// on agentID.
func pluginShadowsName(byAgent map[string]map[string]bool, agentID, name string) bool {
	return byAgent[agentID][name]
}
