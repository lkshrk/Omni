package tui

import (
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
)

func TestSeedAgentsRowsFromCache_SeedsOnlyLoadedSectionsAndSetsKnown(t *testing.T) {
	m := baseModel(nil)
	cache := &app.CachedAgentsRows{
		SkillsLoaded:  true,
		Skills:        []app.SkillPackageRow{{Name: "cached-skill", Source: "o/cached-skill", Installed: true}},
		PluginsLoaded: true,
		Plugins:       []app.PluginRow{{Name: "cached-plugin", Marketplace: "acme"}},
		Mcp:           []app.McpServerRow{{Name: "not-loaded-mcp"}},
	}

	(&m).seedAgentsRowsFromCache(cache)

	if len(m.skillsRows) != 1 || m.skillsRows[0].Name != "cached-skill" {
		t.Errorf("skillsRows = %#v, want the cached skill seeded", m.skillsRows)
	}
	if !m.skillsRowsKnown {
		t.Error("skillsRowsKnown should be true after seeding a Loaded skills section")
	}
	if len(m.pluginRows) != 1 || m.pluginRows[0].Name != "cached-plugin" {
		t.Errorf("pluginRows = %#v, want the cached plugin seeded", m.pluginRows)
	}
	if !m.pluginRowsKnown {
		t.Error("pluginRowsKnown should be true after seeding a Loaded plugins section")
	}
	if len(m.mcpRows) != 0 {
		t.Errorf("mcpRows = %#v, want empty: McpLoaded=false means no valid cache exists", m.mcpRows)
	}
	if m.mcpRowsKnown {
		t.Error("mcpRowsKnown should stay false for a section without a Loaded cache")
	}
	if m.marketplaceRowsKnown {
		t.Error("marketplaceRowsKnown should stay false for a section without a Loaded cache")
	}
}

func TestSeedAgentsRowsFromCache_NeverOverwritesNonEmptyRows(t *testing.T) {
	m := baseModel(nil)
	m.pluginRows = []app.PluginRow{{Name: "live-plugin", Marketplace: "acme"}}
	cache := &app.CachedAgentsRows{
		PluginsLoaded: true,
		Plugins:       []app.PluginRow{{Name: "stale-plugin", Marketplace: "acme"}},
	}

	(&m).seedAgentsRowsFromCache(cache)

	if len(m.pluginRows) != 1 || m.pluginRows[0].Name != "live-plugin" {
		t.Errorf("pluginRows = %#v, want live rows kept over stale cache", m.pluginRows)
	}
	if m.pluginRowsKnown {
		t.Error("pluginRowsKnown should stay untouched when the seed is skipped (the live path owns it)")
	}
}

func TestSeedAgentsRowsFromCache_NilCache_NoOp(t *testing.T) {
	m := baseModel(nil)

	(&m).seedAgentsRowsFromCache(nil)

	if m.skillsRowsKnown || m.mcpRowsKnown || m.pluginRowsKnown || m.marketplaceRowsKnown {
		t.Error("no Known flag should be set for a nil cache")
	}
}

// TestToolsLoadedMsg_AgentsRowsCache_RendersRowsImmediately drives the
// startup snapshot msg through Update and pins that the agents tab renders
// the cache-seeded rows right away — not the loading line or the onboarding
// empty state — while the live section loads are still in flight.
func TestToolsLoadedMsg_AgentsRowsCache_RendersRowsImmediately(t *testing.T) {
	m := agentsAllProgressModel(t, nil, nil, nil)
	m.skillTypeIdx = agentsChipAll
	cache := &app.CachedAgentsRows{
		SkillsLoaded:       true,
		Skills:             []app.SkillPackageRow{{Name: "cached-skill", Source: "o/cached-skill", Installed: true}},
		McpLoaded:          true,
		Mcp:                []app.McpServerRow{{Name: "cached-mcp", PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusInstalled}}},
		PluginsLoaded:      true,
		Plugins:            []app.PluginRow{{Name: "cached-plugin", Marketplace: "acme", PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}}},
		MarketplacesLoaded: true,
	}

	got := drive(m, toolsLoadedMsg{
		agentsEnabled:  true,
		skillsEnabled:  true,
		mcpEnabled:     true,
		pluginsEnabled: true,
		enabledAgents:  []string{"claude"},
		agentsRows:     cache,
	})

	if !got.skillsRowsKnown || !got.mcpRowsKnown || !got.pluginRowsKnown || !got.marketplaceRowsKnown {
		t.Fatalf("all Known flags should be set from the cache, got skills=%v mcp=%v plugin=%v marketplace=%v",
			got.skillsRowsKnown, got.mcpRowsKnown, got.pluginRowsKnown, got.marketplaceRowsKnown)
	}

	got.height = 40
	out := stripANSIEscapeSequences(got.viewSkillsBody())
	for _, want := range []string{"cached-skill", "cached-mcp", "cached-plugin"} {
		if !strings.Contains(out, want) {
			t.Errorf("agents tab should render seeded row %q immediately, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "loading agents…") {
		t.Errorf("agents tab should not show the loading line once rows are seeded, got:\n%s", out)
	}
	if strings.Contains(out, "No agent items tracked yet.") {
		t.Errorf("agents tab should not show the onboarding empty state with seeded rows, got:\n%s", out)
	}
}
