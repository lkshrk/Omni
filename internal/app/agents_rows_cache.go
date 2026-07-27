package app

import (
	"context"
	"encoding/json"
	"fmt"
)

const (
	agentsRowsCacheSkillsKey       = "agents_rows_cache.skills"
	agentsRowsCacheMcpKey          = "agents_rows_cache.mcp"
	agentsRowsCachePluginsKey      = "agents_rows_cache.plugins"
	agentsRowsCacheMarketplacesKey = "agents_rows_cache.marketplaces"
)

type cachedSkillRows struct {
	Rows      []SkillPackageRow
	Unmanaged []SkillPackageRow
}

type cachedMcpRows struct {
	Rows      []McpServerRow
	Unmanaged map[string][]InstalledMcpServer
}

type cachedPluginRows struct {
	Rows      []PluginRow
	Unmanaged map[string][]InstalledPlugin
}

type cachedMarketplaceRows struct {
	Rows      []MarketplaceRow
	Unmanaged map[string][]InstalledMarketplace
}

// CachedAgentsRows — Loaded false means no cache row exists yet; zero rows with Loaded true was a genuinely empty load.
type CachedAgentsRows struct {
	Skills                []SkillPackageRow
	SkillsUnmanaged       []SkillPackageRow
	SkillsLoaded          bool
	Mcp                   []McpServerRow
	McpUnmanaged          map[string][]InstalledMcpServer
	McpLoaded             bool
	Plugins               []PluginRow
	PluginsUnmanaged      map[string][]InstalledPlugin
	PluginsLoaded         bool
	Marketplaces          []MarketplaceRow
	MarketplacesUnmanaged map[string][]InstalledMarketplace
	MarketplacesLoaded    bool
}

// Best-effort: a failed cache write must not discard the live listing it snapshots.
func (a *App) cacheAgentsRowsSectionBestEffort(ctx context.Context, key string, payload any) {
	// Documented no-op on error, per the rationale above.
	_ = a.cacheAgentsRowsSection(ctx, key, payload)
}

func (a *App) cacheAgentsRowsSection(ctx context.Context, key string, payload any) error {
	db := a.readDB()
	if db == nil {
		return nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode agents rows cache %q: %w", key, err)
	}
	if err := db.SetState(ctx, key, string(data)); err != nil {
		return fmt.Errorf("cache agents rows %q: %w", key, err)
	}
	return nil
}

// A missing or undecodable payload counts as cache-absent: a render cache must not fail startup.
func loadAgentsRowsSection(ctx context.Context, a *App, key string, out any) bool {
	db := a.readDB()
	if db == nil {
		return false
	}
	value, err := db.GetState(ctx, key)
	if err != nil {
		return false
	}
	return json.Unmarshal([]byte(value), out) == nil
}

// CachedAgentsRows — Local DB only; never shells out to agent CLIs.
func (a *App) CachedAgentsRows(ctx context.Context) (*CachedAgentsRows, error) {
	out := &CachedAgentsRows{}
	var skills cachedSkillRows
	if loadAgentsRowsSection(ctx, a, agentsRowsCacheSkillsKey, &skills) {
		out.Skills = skills.Rows
		out.SkillsUnmanaged = skills.Unmanaged
		out.SkillsLoaded = true
	}
	var mcp cachedMcpRows
	if loadAgentsRowsSection(ctx, a, agentsRowsCacheMcpKey, &mcp) {
		out.Mcp = mcp.Rows
		out.McpUnmanaged = mcp.Unmanaged
		out.McpLoaded = true
	}
	var plugins cachedPluginRows
	if loadAgentsRowsSection(ctx, a, agentsRowsCachePluginsKey, &plugins) {
		out.Plugins = plugins.Rows
		out.PluginsUnmanaged = plugins.Unmanaged
		out.PluginsLoaded = true
	}
	var marketplaces cachedMarketplaceRows
	if loadAgentsRowsSection(ctx, a, agentsRowsCacheMarketplacesKey, &marketplaces) {
		out.Marketplaces = marketplaces.Rows
		out.MarketplacesUnmanaged = marketplaces.Unmanaged
		out.MarketplacesLoaded = true
	}
	return out, nil
}

// SkillPackageRowState — The cache write is best-effort and never fails the returned rows.
func (a *App) SkillPackageRowState(ctx context.Context) (rows, unmanaged []SkillPackageRow, err error) {
	rows, err = a.SkillPackageRows(ctx)
	if err != nil {
		return nil, nil, err
	}
	unmanaged, err = a.UnmanagedSkillPackages(ctx)
	if err != nil {
		return rows, nil, err
	}
	a.cacheAgentsRowsSectionBestEffort(ctx, agentsRowsCacheSkillsKey, cachedSkillRows{Rows: rows, Unmanaged: unmanaged})
	return rows, unmanaged, nil
}
