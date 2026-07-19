package app_test

import (
	"context"
	"slices"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

func containsToolNamed(tools []*database.ToolCache, name string) bool {
	return slices.ContainsFunc(tools, func(t *database.ToolCache) bool {
		return t != nil && t.Name == name
	})
}

func anyToolInstalled(tools []*database.ToolCache) bool {
	return slices.ContainsFunc(tools, func(t *database.ToolCache) bool {
		return t != nil && t.Installed
	})
}

// ─── ListDiscovered ───────────────────────────────────────────────────────────

func TestListDiscovered_EmptyInitially(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	if !containsToolNamed(tools, "jq") || containsToolNamed(tools, "black") {
		t.Fatalf("ListTools = %+v, want scoped untracked jq present and out-of-scope black hidden", tools)
	}
}

func TestListDiscovered_HidesUnattributedLegacyRows(t *testing.T) {
	t.Parallel()
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
		{Name: "jq", Provider: "brew", InstalledWith: "brew", Version: "1.7.0"},
		{Name: "utm", Provider: "brew", Version: "4.5.0"},
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
	if !containsToolNamed(tools, "jq") || containsToolNamed(tools, "utm") {
		t.Fatalf("ListTools = %+v, want attributed brew-backed jq present and unattributed utm hidden", tools)
	}
}

func TestListTools_ShowsConfiguredToolWithoutCacheRow(t *testing.T) {
	t.Parallel()
	brew := &stubProvider{name: "brew", available: true}
	a, cfgPath := newImportApp(t, brew)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("fd", "brew")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("fd"),
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	// No refresh has run, so there is no cache row. The tool is config-led and
	// must still appear as a not-installed row rather than vanishing.
	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "fd" {
		t.Fatalf("ListTools = %+v, want one synthesized fd row", tools)
	}
	if tools[0].Installed {
		t.Fatalf("fd Installed = true, want false (no cache row)")
	}
	if tools[0].Provider != "brew" {
		t.Fatalf("fd Provider = %q, want brew", tools[0].Provider)
	}
}

func TestListTools_ShowsEmptyProviderToolAsUnresolved(t *testing.T) {
	t.Parallel()
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{"fd": {}},
		Groups: []*config.GroupConfig{{
			Tools: groupTools("fd"),
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "fd" || tools[0].Installed {
		t.Fatalf("ListTools = %+v, want one not-installed unresolved fd row", tools)
	}
}

func TestListTools_HidesTrackedRowsRemovedFromConfig(t *testing.T) {
	t.Parallel()
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
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "docker",
		Provider:      "brew",
		Package:       "docker-desktop",
		Installed:     true,
		InstalledWith: "brew",
	}); err != nil {
		t.Fatalf("Upsert stale docker: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "ripgrep",
		Provider:      "brew",
		Package:       "ripgrep",
		Installed:     true,
		InstalledWith: "brew",
	}); err != nil {
		t.Fatalf("Upsert configured ripgrep: %v", err)
	}

	tools, err := a.ListTools(ctx, "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "ripgrep" {
		t.Fatalf("ListTools = %+v, want only configured ripgrep", tools)
	}
}

func TestListTools_HidesTrackedRowsWhenConfigHasNoEffectiveTools(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	brew := &stubProvider{name: "brew", available: true}
	a, cfgPath := newImportApp(t, brew)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{Name: "system", Tools: nil}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "docker",
		Provider:      "brew",
		Package:       "docker-desktop",
		Installed:     true,
		InstalledWith: "brew",
		Tracked:       true,
	}); err != nil {
		t.Fatalf("Upsert stale docker: %v", err)
	}

	tools, err := a.ListTools(ctx, "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 0 {
		t.Fatalf("ListTools = %+v, want no stale tracked rows", tools)
	}
}

func TestListToolsAndRefreshUseActiveHostGroups(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "Topaz.local")
	ctx := context.Background()
	brew := &stubProvider{
		name:      "brew",
		available: true,
		installed: []provider.InstalledTool{
			installedTool("ripgrep", "14.1.0", "brew"),
		},
	}
	a, cfgPath := newImportApp(t, brew)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("docker", "brew"),
			logicalTool("ripgrep", "brew"),
		),
		Hosts: map[string][]string{
			"Topaz": {"dev"},
			"coder": {"coder-workspace"},
		},
		Groups: []*config.GroupConfig{
			{Name: "Topaz", Special: "host"},
			{Name: "coder", Special: "host"},
			{Name: "dev", Tools: groupTools("ripgrep")},
			{Name: "coder-workspace", Tools: groupTools("docker")},
		},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:      "docker",
		Provider:  "brew",
		Package:   "docker",
		Installed: false,
	}); err != nil {
		t.Fatalf("Upsert stale docker: %v", err)
	}
	if current := app.CurrentMachineGroupName(); current != "topaz" {
		t.Fatalf("CurrentMachineGroupName = %q, want topaz", current)
	}
	info, err := a.HostStatus()
	if err != nil {
		t.Fatalf("HostStatus: %v", err)
	}
	if info.Active != "topaz" || !slices.Contains(info.Hosts["topaz"].Groups, "dev") {
		t.Fatalf("HostStatus = %#v, want active topaz with dev", info)
	}

	tools, err := a.ListTools(ctx, "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if !containsToolNamed(tools, "ripgrep") || containsToolNamed(tools, "docker") {
		t.Fatalf("ListTools before refresh = %+v, want config-led Topaz ripgrep present and no stale coder docker", tools)
	}

	if err := a.RefreshInstalled(ctx, nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}
	docker, err := a.DB().Get(ctx, "docker", "brew", "docker")
	if err != nil {
		t.Fatalf("Get docker cache row: %v", err)
	}
	if docker.Tracked {
		t.Fatal("docker should be untracked after Topaz refresh")
	}
	tools, err = a.ListTools(ctx, "")
	if err != nil {
		t.Fatalf("ListTools after refresh: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "ripgrep" {
		t.Fatalf("ListTools after refresh = %+v, want only Topaz ripgrep", tools)
	}
}

func TestToolDisplaySnapshotReturnsToolsDiscoveredAndManager(t *testing.T) {
	t.Parallel()
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
		{Name: "jq", Provider: "brew", InstalledWith: "brew", Version: "1.7.0"},
	}); err != nil {
		t.Fatalf("UpsertDiscoveredBatch: %v", err)
	}

	snapshot, err := a.ToolDisplaySnapshot(ctx)
	if err != nil {
		t.Fatalf("ToolDisplaySnapshot: %v", err)
	}
	if !slices.ContainsFunc(snapshot.Tools, func(t *app.ToolView) bool { return t != nil && t.Name == "jq" }) {
		t.Fatalf("Tools = %+v, want jq present", snapshot.Tools)
	}
	if len(snapshot.Discovered) != 1 || snapshot.Discovered[0].Name != "jq" {
		t.Fatalf("Discovered = %+v, want jq", snapshot.Discovered)
	}
	if snapshot.EffectiveSystemManager != "brew" {
		t.Fatalf("EffectiveSystemManager = %q, want brew", snapshot.EffectiveSystemManager)
	}
}
