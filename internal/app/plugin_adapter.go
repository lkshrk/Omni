package app

import "github.com/lkshrk/omni/internal/agent"

type PluginAdapter = agent.PluginAdapter

type InstalledPlugin = agent.InstalledPlugin

type InstalledMarketplace = agent.InstalledMarketplace

func InstalledPluginUpdate(p InstalledPlugin) PluginUpdate {
	return pluginUpdateDisplay(p.Version, p.LatestVersion, p.Sha, p.LatestSha, p.PathOutdated)
}
