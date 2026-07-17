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

// CachedAgentsRows is the last successfully loaded agents-tab row state,
// persisted per section so the TUI can render it at launch while the live
// adapter CLI refresh runs. A section's Loaded flag is false only when no
// cache row exists yet (first run) — zero rows with Loaded true means the
// last real load was genuinely empty.
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

// cacheAgentsRowsSectionBestEffort persists one section without ever failing
// the caller: the cache is a render-only launch optimization, and a failed
// write must not discard the authoritative live listing it snapshots — the
// worst outcome of a lost write is one slower launch.
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

// loadAgentsRowsSection unmarshals one cached section into out. A missing row
// or undecodable payload (stale schema after an upgrade) is treated as
// cache-absent rather than an error: this is a render cache, and failing the
// whole startup snapshot over it would block the launch it exists to speed up.
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

// CachedAgentsRows loads every cached agents-tab section. Sections without a
// valid cache come back empty with Loaded false; the call itself only touches
// the local DB and never shells out to agent CLIs.
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

// SkillPackageRowState returns managed and unmanaged skill package rows in
// one call and refreshes their cache section, so TUI loaders get caching for
// free. The cache write is best-effort and never fails the returned rows.
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
