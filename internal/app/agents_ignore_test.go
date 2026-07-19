package app

import (
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestAgentsIgnoreSet(t *testing.T) {
	t.Parallel()
	a := &App{}
	cfg := &config.RootConfig{
		Agents: config.AgentsConfig{Ignore: config.AgentsIgnore{
			Skills:       []string{"o/r"},
			McpServers:   []string{"srv"},
			Plugins:      []string{"plg"},
			Marketplaces: []string{"mkt"},
		}},
	}
	skills, mcp, plugins, marketplaces := a.AgentsIgnoreSet(cfg)
	if !skills["o/r"] || len(skills) != 1 {
		t.Errorf("skills = %v, want {o/r}", skills)
	}
	if !mcp["srv"] || len(mcp) != 1 {
		t.Errorf("mcp = %v, want {srv}", mcp)
	}
	if !plugins["plg"] || len(plugins) != 1 {
		t.Errorf("plugins = %v, want {plg}", plugins)
	}
	if !marketplaces["mkt"] || len(marketplaces) != 1 {
		t.Errorf("marketplaces = %v, want {mkt}", marketplaces)
	}
}

func TestAgentsIgnoreSet_NeverNil(t *testing.T) {
	t.Parallel()
	a := &App{}
	skills, mcp, plugins, marketplaces := a.AgentsIgnoreSet(&config.RootConfig{})
	if skills == nil || mcp == nil || plugins == nil || marketplaces == nil {
		t.Fatalf("expected empty non-nil maps, got skills=%v mcp=%v plugins=%v marketplaces=%v", skills, mcp, plugins, marketplaces)
	}
	if len(skills) != 0 || len(mcp) != 0 || len(plugins) != 0 || len(marketplaces) != 0 {
		t.Fatalf("expected empty maps, got skills=%v mcp=%v plugins=%v marketplaces=%v", skills, mcp, plugins, marketplaces)
	}
}

func newAgentsIgnoreTestApp(t *testing.T) *App {
	t.Helper()
	brew := &availabilityCountingProvider{name: "brew", available: true}
	path := filepath.Join(t.TempDir(), "settings.json")
	a := New(path)
	if err := a.InitTestMode(t.Context(), brew); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func TestToggleAgentsIgnore_AddThenRemoveRoundTrip(t *testing.T) {
	t.Parallel()
	a := newAgentsIgnoreTestApp(t)

	nowIgnored, err := a.ToggleAgentsIgnore(t.Context(), "skills", "o/r")
	if err != nil {
		t.Fatal(err)
	}
	if !nowIgnored {
		t.Fatal("expected first toggle to add to ignore list")
	}
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.Ignore.Skills) != 1 || cfg.Agents.Ignore.Skills[0] != "o/r" {
		t.Fatalf("Ignore.Skills = %v, want [o/r]", cfg.Agents.Ignore.Skills)
	}

	nowIgnored, err = a.ToggleAgentsIgnore(t.Context(), "skills", "o/r")
	if err != nil {
		t.Fatal(err)
	}
	if nowIgnored {
		t.Fatal("expected second toggle to remove from ignore list")
	}
	cfg, err = a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.Ignore.Skills) != 0 {
		t.Fatalf("Ignore.Skills = %v, want empty after removal", cfg.Agents.Ignore.Skills)
	}
}

func TestToggleAgentsIgnore_McpAndPlugins(t *testing.T) {
	t.Parallel()
	a := newAgentsIgnoreTestApp(t)

	if _, err := a.ToggleAgentsIgnore(t.Context(), "mcp", "srv"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ToggleAgentsIgnore(t.Context(), "plugins", "plg"); err != nil {
		t.Fatal(err)
	}
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.Ignore.McpServers) != 1 || cfg.Agents.Ignore.McpServers[0] != "srv" {
		t.Fatalf("Ignore.McpServers = %v, want [srv]", cfg.Agents.Ignore.McpServers)
	}
	if len(cfg.Agents.Ignore.Plugins) != 1 || cfg.Agents.Ignore.Plugins[0] != "plg" {
		t.Fatalf("Ignore.Plugins = %v, want [plg]", cfg.Agents.Ignore.Plugins)
	}
}

func TestToggleAgentsIgnore_InvalidFeature(t *testing.T) {
	t.Parallel()
	a := newAgentsIgnoreTestApp(t)
	if _, err := a.ToggleAgentsIgnore(t.Context(), "bogus", "name"); err == nil {
		t.Fatal("expected error for invalid feature")
	}
}

func TestToggleAgentsIgnore_EmptyName(t *testing.T) {
	t.Parallel()
	a := newAgentsIgnoreTestApp(t)
	if _, err := a.ToggleAgentsIgnore(t.Context(), "skills", "  "); err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestToggleAgentsIgnore_PersistsAcrossReload(t *testing.T) {
	t.Parallel()
	a := newAgentsIgnoreTestApp(t)
	if _, err := a.ToggleAgentsIgnore(t.Context(), "skills", "o/r"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	skills, _, _, _ := a.AgentsIgnoreSet(reloaded)
	if !skills["o/r"] {
		t.Fatalf("expected o/r to still be ignored after reload, got %v", skills)
	}
}

func TestToggleAgentsIgnore_Marketplaces(t *testing.T) {
	t.Parallel()
	a := newAgentsIgnoreTestApp(t)

	nowIgnored, err := a.ToggleAgentsIgnore(t.Context(), "marketplaces", "mkt")
	if err != nil {
		t.Fatal(err)
	}
	if !nowIgnored {
		t.Fatal("expected first toggle to add to ignore list")
	}
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.Ignore.Marketplaces) != 1 || cfg.Agents.Ignore.Marketplaces[0] != "mkt" {
		t.Fatalf("Ignore.Marketplaces = %v, want [mkt]", cfg.Agents.Ignore.Marketplaces)
	}

	nowIgnored, err = a.ToggleAgentsIgnore(t.Context(), "marketplaces", "mkt")
	if err != nil {
		t.Fatal(err)
	}
	if nowIgnored {
		t.Fatal("expected second toggle to remove from ignore list")
	}
	cfg, err = a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.Ignore.Marketplaces) != 0 {
		t.Fatalf("Ignore.Marketplaces = %v, want empty after removal", cfg.Agents.Ignore.Marketplaces)
	}
}
