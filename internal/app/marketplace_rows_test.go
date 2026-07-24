package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func TestMarketplaceRows_ListErrorIsSurfaced(t *testing.T) {
	t.Parallel()
	adapter := &stubPluginAdapter{
		id:            "claude-code",
		available:     true,
		listMarketErr: errors.New("parse json: expected array, got null"),
	}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "market", Source: "owner/repo"}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{adapter}))
	rows, _, err := a.MarketplaceRows(t.Context())
	if err == nil || !strings.Contains(err.Error(), "expected array, got null") {
		t.Fatalf("MarketplaceRows error = %v, want adapter parse error", err)
	}
	if rows != nil {
		t.Fatalf("MarketplaceRows returned misleading rows after adapter parse error: %+v", rows)
	}
}

func TestMarketplaceRows_ManagedRowReportsPerAgentStatus(t *testing.T) {
	t.Parallel()
	claude := &stubPluginAdapter{id: "claude-code", available: true}
	codex := &stubPluginAdapter{id: "codex", available: false}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{claude, codex}))
	rows, unmanaged, err := a.MarketplaceRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "caveman" || rows[0].Source != "a/b" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
	if rows[0].PerAgentStatus["claude-code"] != app.PluginStatusMissing {
		t.Fatalf("expected missing (not installed on claude), got %v", rows[0].PerAgentStatus)
	}
	if rows[0].PerAgentStatus["codex"] != app.PluginStatusAgentUnavailable {
		t.Fatalf("expected agent-unavailable for codex, got %v", rows[0].PerAgentStatus)
	}
	if len(unmanaged) != 0 {
		t.Fatalf("expected no unmanaged entries, got %v", unmanaged)
	}
}

func TestMarketplaceRows_InstalledStatus(t *testing.T) {
	t.Parallel()
	claude := &stubPluginAdapter{
		id:        "claude-code",
		available: true,
		listedMarkets: []app.InstalledMarketplace{
			{Name: "caveman", Source: "a/b"},
		},
	}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{claude}))
	rows, _, err := a.MarketplaceRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %+v", rows)
	}
	if rows[0].PerAgentStatus["claude-code"] != app.PluginStatusInstalled {
		t.Fatalf("expected installed, got %v", rows[0].PerAgentStatus)
	}
}

func TestMarketplaceRows_UnmanagedEntriesReported(t *testing.T) {
	t.Parallel()
	claude := &stubPluginAdapter{
		id:        "claude-code",
		available: true,
		listedMarkets: []app.InstalledMarketplace{
			{Name: "orphan", Source: "c/d"},
		},
	}
	agents := config.AgentsConfig{}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{claude}))
	rows, unmanaged, err := a.MarketplaceRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected no managed rows, got %+v", rows)
	}
	entries, ok := unmanaged["claude-code"]
	if !ok || len(entries) != 1 || entries[0].Name != "orphan" || entries[0].Source != "c/d" {
		t.Fatalf("unexpected unmanaged: %+v", unmanaged)
	}
}

// TestMarketplaceRows_GroupsPopulatedFromConfig is marketplace_rows' twin of
// plugin_rows_test.go's group-population coverage for PluginRow.Groups.
func TestMarketplaceRows_GroupsPopulatedFromConfig(t *testing.T) {
	t.Parallel()
	claude := &stubPluginAdapter{id: "claude-code", available: true}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{claude}))
	cfg := loadPluginTestConfig(t, a)
	cfg.Groups = []*config.GroupConfig{{Name: "work", Marketplaces: []string{"caveman"}}}
	if err := config.Save(a.ConfigPath, cfg); err != nil {
		t.Fatal(err)
	}
	rows, _, err := a.MarketplaceRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || len(rows[0].Groups) != 1 || rows[0].Groups[0] != "work" {
		t.Fatalf("expected rows[0].Groups = [work], got %+v", rows)
	}
}

// TestMarketplaceRows_UpdatedAtFromInstalledMarketplace verifies UpdatedAt
// flows from the adapter's InstalledMarketplace through to the row, mirroring
// how PluginRow.Version is joined from InstalledPlugin.
func TestMarketplaceRows_UpdatedAtFromInstalledMarketplace(t *testing.T) {
	t.Parallel()
	updated := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	claude := &stubPluginAdapter{
		id:        "claude-code",
		available: true,
		listedMarkets: []app.InstalledMarketplace{
			{Name: "caveman", Source: "a/b", UpdatedAt: updated},
		},
	}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{claude}))
	rows, _, err := a.MarketplaceRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !rows[0].UpdatedAt.Equal(updated) {
		t.Fatalf("expected rows[0].UpdatedAt = %v, got %+v", updated, rows)
	}
}

// TestMarketplaceRows_UpdatedAtZeroWhenUnknown verifies UpdatedAt stays the
// zero value when no adapter reports the marketplace as installed.
func TestMarketplaceRows_UpdatedAtZeroWhenUnknown(t *testing.T) {
	t.Parallel()
	claude := &stubPluginAdapter{id: "claude-code", available: true}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{claude}))
	rows, _, err := a.MarketplaceRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !rows[0].UpdatedAt.IsZero() {
		t.Fatalf("expected rows[0].UpdatedAt to be zero, got %+v", rows)
	}
}
