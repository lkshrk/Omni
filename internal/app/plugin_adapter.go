package app

import "github.com/lkshrk/omni/internal/agent"

// PluginAdapter keeps app callers stable while plugin ownership lives in agent.
type PluginAdapter = agent.PluginAdapter

// InstalledPlugin keeps app callers stable while plugin ownership lives in agent.
type InstalledPlugin = agent.InstalledPlugin

// InstalledMarketplace keeps app callers stable while plugin ownership lives in agent.
type InstalledMarketplace = agent.InstalledMarketplace

// InstalledPluginUpdate projects an agent-owned plugin DTO into the app's
// display model.
func InstalledPluginUpdate(p InstalledPlugin) PluginUpdate {
	return pluginUpdateDisplay(p.Version, p.LatestVersion, p.Sha, p.LatestSha, p.PathOutdated)
}
