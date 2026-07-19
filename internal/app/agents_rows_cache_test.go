package app_test

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

// TestCachedAgentsRows_RoundTripAndCorruptSection pins the agents rows render
// cache: row loads persist their result into local_state, CachedAgentsRows
// returns it with the section's Loaded flag set, and a corrupt payload is
// treated as cache-absent (Loaded false) instead of failing the call.
func TestCachedAgentsRows_RoundTripAndCorruptSection(t *testing.T) {
	t.Parallel()
	stub := &stubPluginAdapter{
		id:        "claude-code",
		available: true,
		listedPlugins: []app.InstalledPlugin{
			{Name: "rt-plugin", Marketplace: "rt-market", Version: "1.0.0"},
		},
		listedMarkets: []app.InstalledMarketplace{
			{Name: "rt-market", Source: "acme/rt"},
		},
	}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "rt-market", Source: "acme/rt"}},
		Plugins:      []config.Plugin{{Name: "rt-plugin", Marketplace: "rt-market"}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	ctx := context.Background()

	before, err := a.CachedAgentsRows(ctx)
	if err != nil {
		t.Fatalf("CachedAgentsRows before any load: %v", err)
	}
	if before.PluginsLoaded || before.MarketplacesLoaded || before.SkillsLoaded || before.McpLoaded {
		t.Fatalf("no section should be Loaded before any rows call, got %+v", before)
	}

	if _, _, err := a.PluginRows(ctx); err != nil {
		t.Fatalf("PluginRows: %v", err)
	}
	if _, _, err := a.MarketplaceRows(ctx); err != nil {
		t.Fatalf("MarketplaceRows: %v", err)
	}
	if _, _, err := a.SkillPackageRowState(ctx); err != nil {
		t.Fatalf("SkillPackageRowState: %v", err)
	}

	cached, err := a.CachedAgentsRows(ctx)
	if err != nil {
		t.Fatalf("CachedAgentsRows after loads: %v", err)
	}
	if !cached.PluginsLoaded {
		t.Fatal("PluginsLoaded should be true after a successful PluginRows call")
	}
	if len(cached.Plugins) != 1 || cached.Plugins[0].Name != "rt-plugin" {
		t.Fatalf("cached.Plugins = %#v, want the rt-plugin row round-tripped", cached.Plugins)
	}
	if !cached.MarketplacesLoaded {
		t.Fatal("MarketplacesLoaded should be true after a successful MarketplaceRows call")
	}
	if len(cached.Marketplaces) != 1 || cached.Marketplaces[0].Name != "rt-market" {
		t.Fatalf("cached.Marketplaces = %#v, want the rt-market row round-tripped", cached.Marketplaces)
	}
	if !cached.SkillsLoaded {
		t.Fatal("SkillsLoaded should be true after a successful SkillPackageRowState call")
	}
	if cached.McpLoaded {
		t.Fatal("McpLoaded should stay false: no mcp rows call ran")
	}

	if err := a.DB().SetState(ctx, "agents_rows_cache.plugins", "{corrupt"); err != nil {
		t.Fatalf("corrupting plugins cache section: %v", err)
	}
	after, err := a.CachedAgentsRows(ctx)
	if err != nil {
		t.Fatalf("CachedAgentsRows with a corrupt section must not error, got: %v", err)
	}
	if after.PluginsLoaded {
		t.Fatal("PluginsLoaded should be false when the stored payload is corrupt")
	}
	if len(after.Plugins) != 0 {
		t.Fatalf("after.Plugins = %#v, want empty for a corrupt section", after.Plugins)
	}
	if !after.MarketplacesLoaded {
		t.Fatal("MarketplacesLoaded should survive another section's corruption")
	}
}

// TestAgentsRowsCache_WriteFailureKeepsLiveRows pins the best-effort cache
// write: with the DB closed underneath the app, SetState fails on every rows
// load, yet the live listings must come back intact with a nil error — a
// render-cache write may never discard the authoritative adapter data.
func TestAgentsRowsCache_WriteFailureKeepsLiveRows(t *testing.T) {
	t.Parallel()
	stub := &stubPluginAdapter{
		id:        "claude-code",
		available: true,
		listedPlugins: []app.InstalledPlugin{
			{Name: "wf-plugin", Marketplace: "wf-market", Version: "1.0.0"},
		},
		listedMarkets: []app.InstalledMarketplace{
			{Name: "wf-market", Source: "acme/wf"},
		},
	}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "wf-market", Source: "acme/wf"}},
		Plugins:      []config.Plugin{{Name: "wf-plugin", Marketplace: "wf-market"}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	ctx := context.Background()

	// Close the DB but keep the handle: SetState now errors on every write.
	if err := a.Close(); err != nil {
		t.Fatalf("closing DB: %v", err)
	}

	rows, _, err := a.PluginRows(ctx)
	if err != nil {
		t.Fatalf("PluginRows must not fail on a cache-write error, got: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "wf-plugin" {
		t.Fatalf("PluginRows = %#v, want the live wf-plugin row despite the failed cache write", rows)
	}

	markets, _, err := a.MarketplaceRows(ctx)
	if err != nil {
		t.Fatalf("MarketplaceRows must not fail on a cache-write error, got: %v", err)
	}
	if len(markets) != 1 || markets[0].Name != "wf-market" {
		t.Fatalf("MarketplaceRows = %#v, want the live wf-market row despite the failed cache write", markets)
	}

	if _, _, err := a.SkillPackageRowState(ctx); err != nil {
		t.Fatalf("SkillPackageRowState must not fail on a cache-write error, got: %v", err)
	}

	cached, err := a.CachedAgentsRows(ctx)
	if err != nil {
		t.Fatalf("CachedAgentsRows on a closed DB must not error, got: %v", err)
	}
	if cached.PluginsLoaded || cached.MarketplacesLoaded || cached.SkillsLoaded {
		t.Fatalf("no section should be Loaded when every cache write failed, got %+v", cached)
	}
}
