package app_test

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

// ─── ListDiscovered ───────────────────────────────────────────────────────────

func TestListDiscovered_EmptyInitially(t *testing.T) {
	a, _ := newImportApp(t)

	discovered, err := a.ListDiscovered(context.Background())
	if err != nil {
		t.Fatalf("ListDiscovered: %v", err)
	}
	if len(discovered) != 0 {
		t.Errorf("expected 0 discovered tools on fresh app, got %d", len(discovered))
	}
}

func TestListDiscovered_HidesRowsOutsideTrackedProviders(t *testing.T) {
	brew := &stubProvider{name: "brew", available: true}
	pip := &stubProvider{name: "pip", available: true}
	a, cfgPath := newImportApp(t, brew, pip)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.DB().UpsertDiscoveredBatch(context.Background(), []database.DiscoveredUpsert{
		{Name: "jq", Provider: "brew", InstalledWith: "brew", Version: "1.7.0"},
		{Name: "black", Provider: "pip", InstalledWith: "pip", Version: "24.4.0"},
	}); err != nil {
		t.Fatalf("UpsertDiscoveredBatch: %v", err)
	}

	discovered, err := a.ListDiscovered(context.Background())
	if err != nil {
		t.Fatalf("ListDiscovered: %v", err)
	}
	if len(discovered) != 1 || discovered[0].Name != "jq" {
		t.Fatalf("ListDiscovered = %+v, want only brew jq", discovered)
	}

	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "jq" {
		t.Fatalf("ListTools = %+v, want only scoped untracked jq", tools)
	}
}

func TestListDiscovered_HidesUnattributedLegacyRows(t *testing.T) {
	brew := &stubProvider{name: "brew", available: true}
	system := &lifecycleProvider{stubProvider: stubProvider{name: "system", available: true}, resolvedName: "brew"}
	a, cfgPath := newImportApp(t, system, brew)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "system")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.DB().UpsertDiscoveredBatch(context.Background(), []database.DiscoveredUpsert{
		{Name: "jq", Provider: "system", InstalledWith: "brew", Version: "1.7.0"},
		{Name: "utm", Provider: "system", Version: "4.5.0"},
	}); err != nil {
		t.Fatalf("UpsertDiscoveredBatch: %v", err)
	}

	discovered, err := a.ListDiscovered(context.Background())
	if err != nil {
		t.Fatalf("ListDiscovered: %v", err)
	}
	if len(discovered) != 1 || discovered[0].Name != "jq" {
		t.Fatalf("ListDiscovered = %+v, want only attributed brew-backed jq", discovered)
	}

	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "jq" {
		t.Fatalf("ListTools = %+v, want only attributed brew-backed jq", tools)
	}
}

func TestToolDisplaySnapshotReturnsToolsDiscoveredAndManager(t *testing.T) {
	ctx := context.Background()
	brew := &stubProvider{name: "brew", available: true}
	system := &lifecycleProvider{stubProvider: stubProvider{name: "system", available: true}, resolvedName: "brew"}
	a, cfgPath := newImportApp(t, system, brew)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "system")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.DB().UpsertDiscoveredBatch(ctx, []database.DiscoveredUpsert{
		{Name: "jq", Provider: "system", InstalledWith: "brew", Version: "1.7.0"},
	}); err != nil {
		t.Fatalf("UpsertDiscoveredBatch: %v", err)
	}

	snapshot, err := a.ToolDisplaySnapshot(ctx)
	if err != nil {
		t.Fatalf("ToolDisplaySnapshot: %v", err)
	}
	if len(snapshot.Tools) != 1 || snapshot.Tools[0].Name != "jq" {
		t.Fatalf("Tools = %+v, want jq", snapshot.Tools)
	}
	if len(snapshot.Discovered) != 1 || snapshot.Discovered[0].Name != "jq" {
		t.Fatalf("Discovered = %+v, want jq", snapshot.Discovered)
	}
	if snapshot.EffectiveSystemManager != "brew" {
		t.Fatalf("EffectiveSystemManager = %q, want brew", snapshot.EffectiveSystemManager)
	}
}
