package app

import (
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func newMembershipTestApp(t *testing.T) *App {
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

func TestSetMcpGroups_RoundTrip(t *testing.T) {
	a := newMembershipTestApp(t)
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Agents.McpServers = []config.McpServer{{Name: "srv", Transport: "stdio", Command: "echo"}}
		cfg.Groups = []*config.GroupConfig{{Name: "work"}, {Name: "home"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := a.SetMcpGroups(t.Context(), "srv", []string{"work", "home"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	work := findGroupInConfig(cfg, "work")
	home := findGroupInConfig(cfg, "home")
	if len(work.McpServers) != 1 || work.McpServers[0] != "srv" {
		t.Errorf("work.McpServers = %v, want [srv]", work.McpServers)
	}
	if len(home.McpServers) != 1 || home.McpServers[0] != "srv" {
		t.Errorf("home.McpServers = %v, want [srv]", home.McpServers)
	}

	if err := a.SetMcpGroups(t.Context(), "srv", []string{"work"}); err != nil {
		t.Fatal(err)
	}
	cfg, err = a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	work = findGroupInConfig(cfg, "work")
	home = findGroupInConfig(cfg, "home")
	if len(work.McpServers) != 1 || work.McpServers[0] != "srv" {
		t.Errorf("work.McpServers = %v, want [srv] after reassign", work.McpServers)
	}
	if len(home.McpServers) != 0 {
		t.Errorf("home.McpServers = %v, want empty after reassign", home.McpServers)
	}
}

func TestSetMcpGroups_GuardedByMcpEnabled(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	root := config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{McpDisabled: config.BoolPtr(true)},
		Agents: config.AgentsConfig{
			McpServers: []config.McpServer{{Name: "srv", Transport: "stdio", Command: "echo"}},
		},
		Groups: []*config.GroupConfig{{Name: "work"}},
	}
	if err := config.Save(cfgPath, &root); err != nil {
		t.Fatal(err)
	}
	brew := &availabilityCountingProvider{name: "brew", available: true}
	a := New(cfgPath)
	if err := a.InitTestMode(t.Context(), brew); err != nil {
		t.Fatal(err)
	}
	defer a.Close() //nolint:errcheck

	if err := a.SetMcpGroups(t.Context(), "srv", []string{"work"}); err == nil {
		t.Fatal("expected mcp-disabled error")
	}
}

// TestSetMcpGroups_NotFoundReturnsError pins the fix for the "mcp group
// assignment silently fails" report: a nonexistent server name used to
// silently no-op (setMcpGroupsInConfig writes into cfg.Groups regardless of
// whether name is a real manifest server) instead of surfacing an error.
func TestSetMcpGroups_NotFoundReturnsError(t *testing.T) {
	a := newMembershipTestApp(t)
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Groups = []*config.GroupConfig{{Name: "work"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	err := a.SetMcpGroups(t.Context(), "does-not-exist", []string{"work"})
	if err == nil {
		t.Fatal("expected a not-found error for a nonexistent mcp server name")
	}

	cfg, loadErr := a.loadConfig()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	work := findGroupInConfig(cfg, "work")
	if len(work.McpServers) != 0 {
		t.Errorf("work.McpServers = %v, want empty: a not-found name should not be written", work.McpServers)
	}
}

func TestSetPluginGroups_RoundTrip(t *testing.T) {
	a := newMembershipTestApp(t)
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Agents.Marketplaces = []config.Marketplace{{Name: "mkt", Source: "a/b"}}
		cfg.Agents.Plugins = []config.Plugin{{Name: "plg", Marketplace: "mkt"}}
		cfg.Groups = []*config.GroupConfig{{Name: "work"}, {Name: "home"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := a.SetPluginGroups(t.Context(), "plg", []string{"work", "home"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	work := findGroupInConfig(cfg, "work")
	home := findGroupInConfig(cfg, "home")
	if len(work.Plugins) != 1 || work.Plugins[0] != "plg" {
		t.Errorf("work.Plugins = %v, want [plg]", work.Plugins)
	}
	if len(home.Plugins) != 1 || home.Plugins[0] != "plg" {
		t.Errorf("home.Plugins = %v, want [plg]", home.Plugins)
	}

	if err := a.SetPluginGroups(t.Context(), "plg", []string{"home"}); err != nil {
		t.Fatal(err)
	}
	cfg, err = a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	work = findGroupInConfig(cfg, "work")
	home = findGroupInConfig(cfg, "home")
	if len(work.Plugins) != 0 {
		t.Errorf("work.Plugins = %v, want empty after reassign", work.Plugins)
	}
	if len(home.Plugins) != 1 || home.Plugins[0] != "plg" {
		t.Errorf("home.Plugins = %v, want [plg] after reassign", home.Plugins)
	}
}

// TestSetPluginGroups_NotFoundReturnsError is the plugin twin of
// TestSetMcpGroups_NotFoundReturnsError: same not-found-name gap, fixed the
// same mechanical way for consistency (plugin group-assign wasn't reported
// broken, but had the identical silent-no-op hole).
func TestSetPluginGroups_NotFoundReturnsError(t *testing.T) {
	a := newMembershipTestApp(t)
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Groups = []*config.GroupConfig{{Name: "work"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	err := a.SetPluginGroups(t.Context(), "does-not-exist", []string{"work"})
	if err == nil {
		t.Fatal("expected a not-found error for a nonexistent plugin name")
	}

	cfg, loadErr := a.loadConfig()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	work := findGroupInConfig(cfg, "work")
	if len(work.Plugins) != 0 {
		t.Errorf("work.Plugins = %v, want empty: a not-found name should not be written", work.Plugins)
	}
}

func TestSetPluginGroups_GuardedByPluginsEnabled(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	root := config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{PluginsDisabled: config.BoolPtr(true)},
		Agents: config.AgentsConfig{
			Marketplaces: []config.Marketplace{{Name: "mkt", Source: "a/b"}},
			Plugins:      []config.Plugin{{Name: "plg", Marketplace: "mkt"}},
		},
		Groups: []*config.GroupConfig{{Name: "work"}},
	}
	if err := config.Save(cfgPath, &root); err != nil {
		t.Fatal(err)
	}
	brew := &availabilityCountingProvider{name: "brew", available: true}
	a := New(cfgPath)
	if err := a.InitTestMode(t.Context(), brew); err != nil {
		t.Fatal(err)
	}
	defer a.Close() //nolint:errcheck

	if err := a.SetPluginGroups(t.Context(), "plg", []string{"work"}); err == nil {
		t.Fatal("expected plugins-disabled error")
	}
}

func TestSetMarketplaceGroups_RoundTrip(t *testing.T) {
	a := newMembershipTestApp(t)
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Agents.Marketplaces = []config.Marketplace{{Name: "mkt", Source: "a/b"}}
		cfg.Groups = []*config.GroupConfig{{Name: "work"}, {Name: "home"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := a.SetMarketplaceGroups(t.Context(), "mkt", []string{"work", "home"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	work := findGroupInConfig(cfg, "work")
	home := findGroupInConfig(cfg, "home")
	if len(work.Marketplaces) != 1 || work.Marketplaces[0] != "mkt" {
		t.Errorf("work.Marketplaces = %v, want [mkt]", work.Marketplaces)
	}
	if len(home.Marketplaces) != 1 || home.Marketplaces[0] != "mkt" {
		t.Errorf("home.Marketplaces = %v, want [mkt]", home.Marketplaces)
	}

	if err := a.SetMarketplaceGroups(t.Context(), "mkt", []string{"home"}); err != nil {
		t.Fatal(err)
	}
	cfg, err = a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	work = findGroupInConfig(cfg, "work")
	home = findGroupInConfig(cfg, "home")
	if len(work.Marketplaces) != 0 {
		t.Errorf("work.Marketplaces = %v, want empty after reassign", work.Marketplaces)
	}
	if len(home.Marketplaces) != 1 || home.Marketplaces[0] != "mkt" {
		t.Errorf("home.Marketplaces = %v, want [mkt] after reassign", home.Marketplaces)
	}
}

// TestSetMarketplaceGroups_NotFoundReturnsError is the marketplace twin of
// TestSetPluginGroups_NotFoundReturnsError.
func TestSetMarketplaceGroups_NotFoundReturnsError(t *testing.T) {
	a := newMembershipTestApp(t)
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Groups = []*config.GroupConfig{{Name: "work"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	err := a.SetMarketplaceGroups(t.Context(), "does-not-exist", []string{"work"})
	if err == nil {
		t.Fatal("expected a not-found error for a nonexistent marketplace name")
	}

	cfg, loadErr := a.loadConfig()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	work := findGroupInConfig(cfg, "work")
	if len(work.Marketplaces) != 0 {
		t.Errorf("work.Marketplaces = %v, want empty: a not-found name should not be written", work.Marketplaces)
	}
}

func TestSetMarketplaceGroups_GuardedByPluginsEnabled(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	root := config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{PluginsDisabled: config.BoolPtr(true)},
		Agents: config.AgentsConfig{
			Marketplaces: []config.Marketplace{{Name: "mkt", Source: "a/b"}},
		},
		Groups: []*config.GroupConfig{{Name: "work"}},
	}
	if err := config.Save(cfgPath, &root); err != nil {
		t.Fatal(err)
	}
	brew := &availabilityCountingProvider{name: "brew", available: true}
	a := New(cfgPath)
	if err := a.InitTestMode(t.Context(), brew); err != nil {
		t.Fatal(err)
	}
	defer a.Close() //nolint:errcheck

	if err := a.SetMarketplaceGroups(t.Context(), "mkt", []string{"work"}); err == nil {
		t.Fatal("expected plugins-disabled error")
	}
}
