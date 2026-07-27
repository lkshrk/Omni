package tui

import "github.com/lkshrk/omni/internal/app"

// Loaded flags stay untouched so the live loads still fire and replace the seed; a section already holding rows is never overwritten with stale cache.
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
