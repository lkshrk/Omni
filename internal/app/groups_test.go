package app_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
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

func TestGroupSummaries_ReturnsDisplayFields(t *testing.T) {
	stub := &stubProvider{name: "brew", available: true}
	a, cfgPath := newImportApp(t, stub)

	rootCfg := &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "brew"),
			logicalTool("fd", "brew"),
			logicalTool("slack", "brew"),
		),
		Groups: []*config.GroupConfig{
			{Name: "work", Description: "Work tools", Tools: groupTools("ripgrep", "fd")},
			{Name: "apps", Tools: groupTools("slack")},
		},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatal(err)
	}

	summaries, err := a.GroupSummaries(context.Background())
	if err != nil {
		t.Fatalf("GroupSummaries: %v", err)
	}

	want := []app.GroupSummary{
		{Name: "apps", ToolCount: 1},
		{Name: "work", Description: "Work tools", ToolCount: 2},
	}
	if len(summaries) != len(want) {
		t.Fatalf("got %d summaries, want %d", len(summaries), len(want))
	}
	for i := range want {
		if summaries[i] != want[i] {
			t.Fatalf("summaries[%d] = %+v, want %+v", i, summaries[i], want[i])
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

func TestAdd_RecordsTapQualifiedPackageForBrewInstallWith(t *testing.T) {
	stub := &stubProvider{name: "brew", available: true}
	a, cfgPath := newImportApp(t, stub)

	if err := a.Add(context.Background(), "system", "hashicorp/tap/terraform", "terraform", "work", "brew"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	spec := updated.Tools["terraform"]
	if len(spec.Taps) != 1 || spec.Taps[0] != "hashicorp/tap" {
		t.Fatalf("terraform taps = %v, want [hashicorp/tap]", spec.Taps)
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

func TestAddWithStateAssignsGroupHostsAndReturnsState(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost.example")
	ctx := context.Background()
	stub := &stubProvider{name: "brew", available: true}
	a, cfgPath := newImportApp(t, stub)

	if err := a.DB().UpsertDiscovered(ctx, "ripgrep", "brew", "brew", "14.1.0"); err != nil {
		t.Fatalf("UpsertDiscovered: %v", err)
	}

	result, err := a.AddWithState(ctx, app.AddToolOptions{
		ProviderName: "brew",
		Package:      "ripgrep",
		Name:         "ripgrep",
		GroupName:    "work",
		AssignHosts:  []string{"other.example"},
	})
	if err != nil {
		t.Fatalf("AddWithState: %v", err)
	}

	key := "ripgrep\x00brew"
	if result.State.ToolGroups[key] != "work" {
		t.Fatalf("ToolGroups[%q] = %q, want work", key, result.State.ToolGroups[key])
	}
	if !slices.Contains(result.State.ToolMemberships[key], "work") {
		t.Fatalf("ToolMemberships[%q] = %v, want work", key, result.State.ToolMemberships[key])
	}
	assertHostAssignedToGroup(t, result.State.HostInfo, "testhost", "work")
	assertHostAssignedToGroup(t, result.State.HostInfo, "other", "work")

	found := false
	for _, tool := range result.Tools {
		if tool.Name == "ripgrep" && tool.Provider == "brew" {
			found = true
			if !tool.Tracked {
				t.Fatalf("claimed tool row Tracked = false, want true")
			}
		}
	}
	if !found {
		t.Fatalf("returned tools missing ripgrep/brew: %+v", result.Tools)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if !slices.Contains(cfg.Hosts["testhost"], "work") {
		t.Fatalf("config host testhost groups = %v, want work", cfg.Hosts["testhost"])
	}
	if !slices.Contains(cfg.Hosts["other"], "work") {
		t.Fatalf("config host other groups = %v, want work", cfg.Hosts["other"])
	}
}

func TestInstallAndAddWithStateReturnsUpdatedState(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost.example")
	ctx := context.Background()
	brew := &installTracker{stubProvider: stubProvider{name: "brew", available: true}}
	a, _ := newImportApp(t, brew)

	result, err := a.InstallAndAddWithState(ctx, app.AddToolOptions{
		ProviderName: "brew",
		Package:      "ripgrep",
		Name:         "ripgrep",
		GroupName:    "work",
	})
	if err != nil {
		t.Fatalf("InstallAndAddWithState: %v", err)
	}
	if len(brew.installCalled) != 1 || brew.installCalled[0] != "ripgrep" {
		t.Fatalf("installCalled = %v, want [ripgrep]", brew.installCalled)
	}

	key := "ripgrep\x00brew"
	if result.State.ToolGroups[key] != "work" {
		t.Fatalf("ToolGroups[%q] = %q, want work", key, result.State.ToolGroups[key])
	}
	if !slices.Contains(result.State.ToolMemberships[key], "work") {
		t.Fatalf("ToolMemberships[%q] = %v, want work", key, result.State.ToolMemberships[key])
	}
	assertHostAssignedToGroup(t, result.State.HostInfo, "testhost", "work")

	for _, tool := range result.Tools {
		if tool.Name == "ripgrep" && tool.Provider == "brew" {
			if !tool.Installed || !tool.Tracked {
				t.Fatalf("installed tool row = %+v, want installed tracked", tool)
			}
			return
		}
	}
	t.Fatalf("returned tools missing ripgrep/brew: %+v", result.Tools)
}

func TestInstallAndAddWithStatePersistsAndPassesOptions(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost.example")
	ctx := context.Background()
	brew := &installCaptureStub{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, brew)

	_, err := a.InstallAndAddWithState(ctx, app.AddToolOptions{
		ProviderName: "brew",
		Package:      "visual-studio-code",
		Name:         "visual-studio-code",
		GroupName:    "work",
		Options:      map[string]string{"brew_kind": "cask"},
	})
	if err != nil {
		t.Fatalf("InstallAndAddWithState: %v", err)
	}
	if len(brew.installed) != 1 {
		t.Fatalf("brew installs = %d, want 1", len(brew.installed))
	}
	installed := brew.installed[0]
	if installed.Provider != "brew" {
		t.Fatalf("installed.Provider = %q, want brew", installed.Provider)
	}
	if installed.Options["brew_kind"] != "cask" {
		t.Fatalf("installed.Options[brew_kind] = %q, want cask", installed.Options["brew_kind"])
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	spec := cfg.Tools["visual-studio-code"]
	if len(spec.Providers) != 1 || spec.Providers[0].Provider != "brew" {
		t.Fatalf("spec providers = %+v, want brew", spec.Providers)
	}
	if spec.Providers[0].Options["brew_kind"] != "cask" {
		t.Fatalf("spec provider options[brew_kind] = %q, want cask", spec.Providers[0].Options["brew_kind"])
	}
}

func TestInstallAndAddWithStateRejectsInvalidInstallWith(t *testing.T) {
	for _, tt := range []struct {
		name        string
		installWith string
		want        string
	}{
		{name: "unknown manager", installWith: "missing", want: `unknown concrete provider/manager "missing"`},
		{name: "ecosystem as manager", installWith: "node", want: `install_with "node" must be a concrete provider or manager`},
		{name: "wrong ecosystem", installWith: "uv", want: `install_with "uv" belongs to ecosystem "python", not "node"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			npm := &installTracker{stubProvider: stubProvider{name: "npm", available: true}}
			a, cfgPath := newImportApp(t, npm)
			if err := saveAppConfig(t, cfgPath, &config.RootConfig{}); err != nil {
				t.Fatalf("save config: %v", err)
			}

			_, err := a.InstallAndAddWithState(ctx, app.AddToolOptions{
				ProviderName: "node",
				Package:      "prettier",
				Name:         "prettier",
				GroupName:    "work",
				InstallWith:  tt.installWith,
			})
			if err == nil || err.Error() != tt.want {
				t.Fatalf("InstallAndAddWithState err = %v, want %q", err, tt.want)
			}
			if len(npm.installCalled) != 0 {
				t.Fatalf("installCalled = %v, want no install after rejected install_with", npm.installCalled)
			}
			cfg, err := config.Load(cfgPath)
			if err != nil {
				t.Fatalf("config.Load: %v", err)
			}
			if len(cfg.Tools) != 0 {
				t.Fatalf("tools = %+v, want no config writes after rejected install_with", cfg.Tools)
			}
		})
	}
}

func assertHostAssignedToGroup(t *testing.T, info *app.HostInfo, host, group string) {
	t.Helper()
	if info == nil {
		t.Fatalf("HostInfo is nil")
	}
	assignment, ok := info.Hosts[host]
	if !ok {
		t.Fatalf("HostInfo missing host %q: %+v", host, info.Hosts)
	}
	if !slices.Contains(assignment.Groups, group) {
		t.Fatalf("HostInfo host %q groups = %v, want %s", host, assignment.Groups, group)
	}
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

// ─── Cross-group duplicate membership ────────────────────────────────────────

func TestSync_DuplicateToolAcrossGroups_IsValid(t *testing.T) {
	brew := &installTracker{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, brew)

	// Put ripgrep in TWO reusable groups. A tool may belong to any number of
	// reusable groups, so this is valid and Sync should succeed and install it.
	host := testShortHostname()
	rootCfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Hosts: map[string][]string{host: {"base", "work"}},
		Groups: []*config.GroupConfig{
			{Name: host, Special: "host"},
			{Name: "base", Tools: groupTools("ripgrep")},
			{Name: "work", Tools: groupTools("ripgrep")},
		},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatal(err)
	}

	_, err := a.Sync(context.Background(), gosync.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync error = %v, want nil for tool in multiple reusable groups", err)
	}
	if len(brew.installCalled) != 1 || brew.installCalled[0] != "ripgrep" {
		t.Fatalf("installCalled = %v, want [ripgrep]", brew.installCalled)
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
