package tui

import "github.com/lkshrk/omni/internal/app"

// seedAgentsRowsFromCache pre-populates the agents-tab sections from the
// persisted last-known rows so the tab renders instantly at launch while the
// live adapter CLI loads run. Loaded flags stay untouched: the live loads
// still fire and their results replace the seed. A section already holding
// rows (a live load raced ahead) is never overwritten with stale cache.
func (m *Model) seedAgentsRowsFromCache(cache *app.CachedAgentsRows) {
	if cache == nil {
		return
	}
	if cache.SkillsLoaded && len(m.skillsRows) == 0 && len(m.skillsUnmanagedRows) == 0 {
		m.skillsRows = cache.Skills
		m.skillsUnmanagedRows = cache.SkillsUnmanaged
		m.skillsRowsKnown = true
	}
	if cache.McpLoaded && len(m.mcpRows) == 0 && len(m.mcpUnmanaged) == 0 {
		m.mcpRows = cache.Mcp
		m.mcpUnmanaged = cache.McpUnmanaged
		m.mcpRowsKnown = true
	}
	if cache.PluginsLoaded && len(m.pluginRows) == 0 && len(m.pluginUnmanaged) == 0 {
		m.pluginRows = cache.Plugins
		m.pluginUnmanaged = cache.PluginsUnmanaged
		m.pluginRowsKnown = true
	}
	if cache.MarketplacesLoaded && len(m.marketplaceRows) == 0 && len(m.marketplaceUnmanaged) == 0 {
		m.marketplaceRows = cache.Marketplaces
		m.marketplaceUnmanaged = cache.MarketplacesUnmanaged
		m.marketplaceRowsKnown = true
	}
}
