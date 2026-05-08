package app_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
	gosync "github.com/lkshrk/omni/internal/sync"
)

// ─── Groups ──────────────────────────────────────────────────────────────────

func TestGroups_EmptyDir(t *testing.T) {
	a, _ := newImportApp(t)
	groups, err := a.Groups(context.Background())
	if err != nil {
		t.Fatalf("Groups: %v", err)
	}
	if len(groups) != 0 {
		t.Errorf("got %d groups, want 0", len(groups))
	}
}

func TestGroups_ReturnsAllDiscovered(t *testing.T) {
	stub := &stubProvider{name: "brew", available: true}
	a, cfgPath := newImportApp(t, stub)

	rootCfg := &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "brew"),
			logicalTool("slack", "brew"),
			logicalTool("zoom", "brew"),
		),
		Groups: []*config.GroupConfig{
			{Tools: groupTools("ripgrep")},
			{Name: "work", Tools: groupTools("slack")},
			{Name: "apps", Tools: groupTools("zoom")},
		},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatal(err)
	}

	groups, err := a.Groups(context.Background())
	if err != nil {
		t.Fatalf("Groups: %v", err)
	}
	if len(groups) != 3 {
		t.Errorf("got %d groups, want 3 (apps + host + work)", len(groups))
	}
	want := []string{"apps", "work", testShortHostname()}
	for i, name := range want {
		if i >= len(groups) {
			break
		}
		if got := groups[i].BaseName(); got != name {
			t.Fatalf("groups[%d] = %q, want %q", i, got, name)
		}
	}
}

func TestInitTestMode_NormalizesConfigGroupOrderOnDisk(t *testing.T) {
	stub := &stubProvider{name: "brew", available: true}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	a := app.New(cfgPath)

	raw := `{
  "tools": {
    "slack": {"provider": "brew"},
    "ripgrep": {"provider": "brew"},
    "zoom": {"provider": "brew"}
  },
  "groups": [
    {"name": "work", "tools": ["slack"]},
    {"name": "testhost", "special": "host", "tools": ["ripgrep"]},
    {"name": "apps", "tools": ["zoom"]}
  ]
}`
	if err := os.WriteFile(cfgPath, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := a.InitTestMode(context.Background(), stub); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"apps", "work", "testhost"}
	for i, name := range want {
		if got := updated.Groups[i].BaseName(); got != name {
			t.Fatalf("groups[%d] = %q, want %q", i, got, name)
		}
	}
}

// ─── Add to group ─────────────────────────────────────────────────────────────

func TestAdd_ToBaseGroup(t *testing.T) {
	stub := &stubProvider{name: "brew", available: true}
	a, cfgPath := newImportApp(t, stub)

	if err := a.Add(context.Background(), "system", "ripgrep", "", "", "brew"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	tools := toolsFromConfig(updated)
	if len(tools) != 1 || tools[0].Name != "ripgrep" {
		t.Errorf("tools = %v, want [ripgrep]", tools)
	}
}

func TestAdd_ToNamedGroup(t *testing.T) {
	stub := &stubProvider{name: "brew", available: true}
	a, cfgPath := newImportApp(t, stub)

	if err := a.Add(context.Background(), "system", "slack", "", "work", "brew"); err != nil {
		t.Fatalf("Add to work group: %v", err)
	}

	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	var workGroup *config.GroupConfig
	for _, g := range updated.Groups {
		if g.Name == "work" {
			workGroup = g
			break
		}
	}
	if workGroup == nil {
		t.Fatal("work group not found in config")
	}
	if len(workGroup.Tools) != 1 || workGroup.Tools[0].Name != "slack" {
		t.Errorf("work tools = %v, want [slack]", workGroup.Tools)
	}
	if workGroup.GroupName() != "work" {
		t.Errorf("GroupName = %q, want work", workGroup.GroupName())
	}
}

func TestAdd_ToNamedGroup_AppendsIfExists(t *testing.T) {
	stub := &stubProvider{name: "brew", available: true}
	a, cfgPath := newImportApp(t, stub)

	// Add first tool.
	if err := a.Add(context.Background(), "system", "slack", "", "work", "brew"); err != nil {
		t.Fatal(err)
	}
	// Add second tool to same group.
	if err := a.Add(context.Background(), "system", "zoom", "", "work", "brew"); err != nil {
		t.Fatal(err)
	}

	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range updated.Groups {
		if g.Name == "work" {
			if len(g.Tools) != 2 {
				t.Errorf("got %d tools, want 2", len(g.Tools))
			}
			return
		}
	}
	t.Error("work group not found")
}

// ─── Sync group filter ────────────────────────────────────────────────────────

func TestSync_GroupFilter_UnknownGroup(t *testing.T) {
	stub := &stubProvider{name: "brew", available: true}
	a, cfgPath := newImportApp(t, stub)

	rootCfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatal(err)
	}

	_, err := a.Sync(context.Background(), gosync.SyncOptions{Group: "nonexistent"})
	if err == nil {
		t.Error("expected error for unknown group, got nil")
	}
}

func TestSync_GroupFilter_OnlySyncsGroup(t *testing.T) {
	brew := &installTracker{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, brew)

	rootCfg := &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "brew"),
			logicalTool("slack", "brew"),
		),
		Groups: []*config.GroupConfig{
			{Tools: groupTools("ripgrep")},
			{Name: "work", Tools: groupTools("slack")},
		},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatal(err)
	}

	// Sync only the "work" group — only slack should be installed.
	_, err := a.Sync(context.Background(), gosync.SyncOptions{Group: "work"})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(brew.installCalled) != 1 || brew.installCalled[0] != "slack" {
		t.Errorf("installCalled = %v, want [slack]", brew.installCalled)
	}
}

// ─── Import group ─────────────────────────────────────────────────────────────

func TestImport_ToNamedGroup(t *testing.T) {
	stub := &stubProvider{
		name:      "brew",
		available: true,
		installed: []provider.InstalledTool{
			installedTool("ripgrep", "14.0", "brew"),
		},
	}
	a, cfgPath := newImportApp(t, stub)

	result, err := a.Import(context.Background(), app.ImportOptions{Group: "work"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(result.Added) != 1 || result.Added[0].Name != "ripgrep" {
		t.Errorf("Added = %v, want [ripgrep]", result.Added)
	}

	// ripgrep must be in the requested "work" group.
	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	for _, g := range updated.Groups {
		if g.Name == "work" {
			if len(g.Tools) != 1 || g.Tools[0].Name != "ripgrep" {
				t.Errorf("work tools = %v, want [ripgrep]", g.Tools)
			}
			return
		}
	}
	t.Error("work group not found in config")
}

// ─── Cross-group duplicate rejection ─────────────────────────────────────────

func TestSync_DuplicateToolAcrossGroups_IsInvalid(t *testing.T) {
	brew := &installTracker{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, brew)

	// Put ripgrep in BOTH base and work group.
	rootCfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{
			{Tools: groupTools("ripgrep")},
			{Name: "work", Tools: groupTools("ripgrep")},
		},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatal(err)
	}

	_, err := a.Sync(context.Background(), gosync.SyncOptions{})
	if err == nil || !strings.Contains(err.Error(), `tool "ripgrep" already belongs to group`) {
		t.Fatalf("Sync error = %v, want duplicate ownership validation error", err)
	}
	if len(brew.installCalled) != 0 {
		t.Fatalf("install calls = %v, want none for invalid config", brew.installCalled)
	}
}

// ─── CreateGroup ──────────────────────────────────────────────────────────────

func TestCreateGroup_Success(t *testing.T) {
	stub := &stubProvider{name: "brew", available: true}
	a, cfgPath := newImportApp(t, stub)

	if err := a.CreateGroup("work"); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	found := false
	for _, g := range updated.Groups {
		if g.Name == "work" {
			found = true
		}
	}
	if !found {
		t.Error("group 'work' not found in config after CreateGroup")
	}
}

func TestCreateGroup_EmptyName_Error(t *testing.T) {
	a, _ := newImportApp(t)
	if err := a.CreateGroup(""); err == nil {
		t.Error("expected error for empty group name, got nil")
	}
	if err := a.CreateGroup("   "); err == nil {
		t.Error("expected error for whitespace-only group name, got nil")
	}
}

func TestCreateGroup_BaseAllowed(t *testing.T) {
	a, _ := newImportApp(t)
	if err := a.CreateGroup("base"); err != nil {
		t.Fatalf("CreateGroup(base): %v", err)
	}
}

func TestCreateGroup_Duplicate_Error(t *testing.T) {
	stub := &stubProvider{name: "brew", available: true}
	a, cfgPath := newImportApp(t, stub)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{
			{Tools: groupTools("ripgrep")},
			{Name: "work"},
		},
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if err := a.CreateGroup("work"); err == nil {
		t.Error("expected error for duplicate group name, got nil")
	}
}
