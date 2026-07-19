package app_test

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

type ignoredProcessingStub struct {
	stubProvider
	installed         map[string]string
	outdated          map[string]string
	listInstalled     []provider.InstalledTool
	installCalls      int
	installedMapCalls int
	outdatedMapCalls  int
	upgradeCalls      int
}

func (s *ignoredProcessingStub) Install(ctx context.Context, tool provider.Tool) error {
	s.installCalls++
	return s.stubProvider.Install(ctx, tool)
}

func (s *ignoredProcessingStub) InstalledMap(_ context.Context) (map[string]string, error) {
	s.installedMapCalls++
	return s.installed, nil
}

func (s *ignoredProcessingStub) OutdatedMap(_ context.Context) (map[string]string, error) {
	s.outdatedMapCalls++
	return s.outdated, nil
}

func (s *ignoredProcessingStub) Upgrade(_ context.Context, _ provider.Tool) error {
	s.upgradeCalls++
	return nil
}

func (s *ignoredProcessingStub) IsInstalled(_ context.Context, tool provider.Tool) (bool, string, error) {
	ver, ok := s.installed[tool.EffectivePackage()]
	return ok, ver, nil
}

func (s *ignoredProcessingStub) ListInstalled(_ context.Context) ([]provider.InstalledTool, error) {
	return s.listInstalled, nil
}

func TestIgnoredToolsHiddenFromListAndQuery(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	a, cfgPath := newImportApp(t)
	tools := logicalToolSpecs(
		logicalTool("ripgrep", "brew"),
		logicalTool("bat", "brew"),
		logicalTool("fd", "brew"),
	)
	bat := tools["bat"]
	bat.Ignore = true
	tools["bat"] = bat
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: tools,
		Groups: []*config.GroupConfig{{
			Name:    "testhost",
			Special: "host",
			Tools:   groupTools("ripgrep", "bat", "fd"),
		}},
		Hosts:  map[string][]string{"testhost": {}},
		Ignore: config.GlobalIgnore{Tools: []string{"ripgrep"}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	ctx := context.Background()
	for _, name := range []string{"ripgrep", "bat", "fd"} {
		if err := a.DB().Upsert(ctx, &database.ToolCache{
			Name:          name,
			Provider:      "brew",
			Package:       name,
			Installed:     true,
			InstalledWith: "brew",
			Tracked:       true,
		}); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	if err := a.DB().UpdateOutdated(ctx, "ripgrep", "brew", "ripgrep", true, "15.0.0"); err != nil {
		t.Fatalf("mark ripgrep outdated: %v", err)
	}

	listed, err := a.ListTools(ctx, "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(listed) != 1 || listed[0].Name != "fd" {
		t.Fatalf("ListTools = %v, want only fd", toolNames(listed))
	}
	items, err := a.QueryTools(ctx, app.ToolListOptions{})
	if err != nil {
		t.Fatalf("QueryTools: %v", err)
	}
	if len(items) != 1 || items[0].Tool.Name != "fd" {
		t.Fatalf("QueryTools = %v, want only fd", queryToolNames(items))
	}
	ignored, err := a.QueryTools(ctx, app.ToolListOptions{State: "ignored"})
	if err != nil {
		t.Fatalf("QueryTools ignored: %v", err)
	}
	if len(ignored) != 0 {
		t.Fatalf("ignored query returned %v, want no ignored tools exposed", queryToolNames(ignored))
	}
}

func TestListToolsForView_IncludesIgnoredTools(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("curl", "brew")),
		Groups: []*config.GroupConfig{testHostToolGroup("curl")},
		Hosts:  map[string][]string{"testhost": {}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	ctx := context.Background()
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "curl",
		Provider:      "brew",
		Package:       "curl",
		Installed:     true,
		InstalledWith: "brew",
		Tracked:       true,
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	if err := a.SetToolIgnore("curl", true); err != nil {
		t.Fatalf("SetToolIgnore: %v", err)
	}

	hidden, err := a.ListTools(ctx, "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(hidden) != 0 {
		t.Fatalf("ListTools = %v, want ignored tools hidden", toolNames(hidden))
	}
	visible, err := a.ListToolsForView(ctx, "")
	if err != nil {
		t.Fatalf("ListToolsForView: %v", err)
	}
	if len(visible) != 1 || visible[0].Name != "curl" {
		t.Fatalf("ListToolsForView = %v, want curl retained", viewNames(visible))
	}
}

func TestSetToolIgnoreScopesWithStateReturnsToolsAndScopeDisplay(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("curl", "brew")),
		Groups: []*config.GroupConfig{testHostToolGroup("curl")},
		Hosts:  map[string][]string{"testhost": {}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	ctx := context.Background()
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "curl",
		Provider:      "brew",
		Package:       "curl",
		Installed:     true,
		InstalledWith: "brew",
		Tracked:       true,
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	result, err := a.SetToolIgnoreScopesWithState(ctx, "curl", []app.ToolIgnoreScopeChange{{
		Kind:    app.ToolIgnoreScopeTool,
		Ignored: true,
	}})
	if err != nil {
		t.Fatalf("SetToolIgnoreScopesWithState: %v", err)
	}

	if !result.Ignored {
		t.Fatal("Ignored = false, want true")
	}
	if result.HostScopeChanged {
		t.Fatal("HostScopeChanged = true, want false")
	}
	if result.ScopeDisplay.IgnoreLabels["curl"] != "tool" {
		t.Fatalf("IgnoreLabels[curl] = %q, want tool", result.ScopeDisplay.IgnoreLabels["curl"])
	}
	if !result.ScopeDisplay.ToolIgnores["curl"] {
		t.Fatalf("ToolIgnores[curl] = false, want true")
	}
	if len(result.Tools) != 1 || result.Tools[0].Name != "curl" {
		t.Fatalf("Tools = %v, want curl retained for TUI ignored section", viewNames(result.Tools))
	}
	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if !updated.Tools["curl"].Ignore {
		t.Fatalf("config tool ignore = false, want true")
	}
}

func TestSetToolIgnoreScopesWithStateSuppressesDiscoveredOrphan(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Groups: []*config.GroupConfig{testHostToolGroup()},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	ctx := context.Background()
	if err := a.DB().UpsertDiscoveredBatch(ctx, []database.DiscoveredUpsert{
		{Name: "asyncpg", Provider: "python", InstalledWith: "pip3", Version: "0.31.0"},
	}); err != nil {
		t.Fatalf("UpsertDiscovered: %v", err)
	}

	result, err := a.SetToolIgnoreScopesWithState(ctx, "asyncpg", []app.ToolIgnoreScopeChange{{
		Kind:    app.ToolIgnoreScopeHost,
		Ignored: true,
	}})
	if err != nil {
		t.Fatalf("SetToolIgnoreScopesWithState: %v", err)
	}
	if !result.HostScopeChanged {
		t.Fatal("HostScopeChanged = false, want true")
	}

	discovered, err := a.ListDiscovered(ctx)
	if err != nil {
		t.Fatalf("ListDiscovered: %v", err)
	}
	if len(discovered) != 0 {
		t.Fatalf("discovered = %v, want asyncpg suppressed after global ignore", viewNames(discovered))
	}
}

func TestSetToolIgnoreScopesWithStateReportsHostScope(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("curl", "brew")),
		Groups: []*config.GroupConfig{testHostToolGroup("curl")},
		Hosts:  map[string][]string{"testhost": {}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	result, err := a.SetToolIgnoreScopesWithState(context.Background(), "curl", []app.ToolIgnoreScopeChange{{
		Kind:    app.ToolIgnoreScopeHost,
		Ignored: true,
	}})
	if err != nil {
		t.Fatalf("SetToolIgnoreScopesWithState: %v", err)
	}

	if !result.Ignored {
		t.Fatal("Ignored = false, want true")
	}
	if !result.HostScopeChanged {
		t.Fatal("HostScopeChanged = false, want true")
	}
	if result.ScopeDisplay.IgnoreLabels["curl"] != "global" {
		t.Fatalf("IgnoreLabels[curl] = %q, want global", result.ScopeDisplay.IgnoreLabels["curl"])
	}
	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if !slices.Contains(updated.Ignore.Tools, "curl") {
		t.Fatalf("global ignore = %v, want curl", updated.Ignore.Tools)
	}
}

func TestRefreshInstalledSkipsIgnoredToolsAndUntracksStaleCache(t *testing.T) {
	t.Parallel()
	stub := &ignoredProcessingStub{
		stubProvider: stubProvider{name: "brew", available: true},
		installed:    map[string]string{"ripgrep": "14.1.0"},
	}
	a, cfgPath := newImportApp(t, stub)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{Tools: groupTools("ripgrep")}},
		Ignore: config.GlobalIgnore{Tools: []string{"ripgrep"}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	ctx := context.Background()
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:        "ripgrep",
		Provider:    "brew",
		Package:     "ripgrep",
		Installed:   true,
		Version:     sql.NullString{String: "13.0.0", Valid: true},
		Tracked:     true,
		LastChecked: time.Now(),
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	if err := a.RefreshInstalled(ctx, nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}
	if stub.installedMapCalls != 0 {
		t.Fatalf("InstalledMap calls = %d, want ignored tool skipped", stub.installedMapCalls)
	}
	raw, err := a.DB().Get(ctx, "ripgrep", "brew", "ripgrep")
	if err != nil {
		t.Fatalf("get raw cache: %v", err)
	}
	if raw.Tracked {
		t.Fatal("ignored stale cache row stayed tracked")
	}
	listed, err := a.ListTools(ctx, "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("ListTools = %v, want ignored tool hidden", toolNames(listed))
	}
}

func TestOutdatedRefreshSkipsIgnoredTools(t *testing.T) {
	t.Parallel()
	stub := &ignoredProcessingStub{
		stubProvider: stubProvider{name: "brew", available: true},
		outdated:     map[string]string{"ripgrep": "15.0.0"},
	}
	a, cfgPath := newImportApp(t, stub)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{Tools: groupTools("ripgrep")}},
		Ignore: config.GlobalIgnore{Tools: []string{"ripgrep"}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	ctx := context.Background()
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "ripgrep",
		Provider:      "brew",
		Package:       "ripgrep",
		Installed:     true,
		InstalledWith: "brew",
		Tracked:       true,
		LastChecked:   time.Now(),
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	if err := a.RefreshOutdated(ctx, false, nil); err != nil {
		t.Fatalf("RefreshOutdated: %v", err)
	}
	if err := a.RefreshProviderOutdated(ctx, "brew", false); err != nil {
		t.Fatalf("RefreshProviderOutdated: %v", err)
	}
	if stub.outdatedMapCalls != 0 {
		t.Fatalf("OutdatedMap calls = %d, want ignored tool skipped", stub.outdatedMapCalls)
	}
	raw, err := a.DB().Get(ctx, "ripgrep", "brew", "ripgrep")
	if err != nil {
		t.Fatalf("get raw cache: %v", err)
	}
	if raw.Outdated || raw.LatestVersion.Valid {
		t.Fatalf("outdated/latest = %v/%q, want unchanged", raw.Outdated, raw.LatestVersion.String)
	}
}

func TestRefreshDiscoveredSkipsGloballyIgnoredName(t *testing.T) {
	t.Parallel()
	stub := &ignoredProcessingStub{
		stubProvider: stubProvider{name: "brew", available: true},
		listInstalled: []provider.InstalledTool{
			{Tool: provider.Tool{Name: "ripgrep", Provider: "brew"}, Version: "14.1.0"},
			{Tool: provider.Tool{Name: "fd", Provider: "brew"}, Version: "9.0.0"},
		},
	}
	a, cfgPath := newImportApp(t, stub)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{testHostToolGroup("ripgrep")},
		Ignore: config.GlobalIgnore{Tools: []string{"ripgrep"}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.RefreshDiscovered(context.Background()); err != nil {
		t.Fatalf("RefreshDiscovered: %v", err)
	}
	discovered, err := a.ListDiscovered(context.Background())
	if err != nil {
		t.Fatalf("ListDiscovered: %v", err)
	}
	if len(discovered) != 1 || discovered[0].Name != "fd" {
		t.Fatalf("discovered = %v, want only fd", viewNames(discovered))
	}
}

func TestRefreshDiscoveredSkipsDisabledProvider(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	stub := &ignoredProcessingStub{
		stubProvider: stubProvider{name: "brew", available: true},
		listInstalled: []provider.InstalledTool{
			{Tool: provider.Tool{Name: "ripgrep", Provider: "brew"}, Version: "14.1.0"},
			{Tool: provider.Tool{Name: "fd", Provider: "brew"}, Version: "9.0.0"},
		},
	}
	a, cfgPath := newImportApp(t, stub)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools:        logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups:       []*config.GroupConfig{testHostToolGroup("ripgrep")},
		HostSettings: map[string]config.Settings{"testhost": {DisabledProviders: []string{"brew"}}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.RefreshDiscovered(context.Background()); err != nil {
		t.Fatalf("RefreshDiscovered: %v", err)
	}
	discovered, err := a.ListDiscovered(context.Background())
	if err != nil {
		t.Fatalf("ListDiscovered: %v", err)
	}
	if len(discovered) != 0 {
		t.Fatalf("discovered = %v, want none (brew disabled)", viewNames(discovered))
	}
}

func TestSyncAllSkipsIgnoredDiscoveredClaims(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	brew := &installTracker{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, brew)
	tools := logicalToolSpecs(logicalTool("fzf", "brew"))
	ignored := tools["fzf"]
	ignored.Ignore = true
	tools["fzf"] = ignored
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools:  tools,
		Groups: []*config.GroupConfig{testHostToolGroup()},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	discovered := []*app.ToolView{
		{Name: "fzf", Provider: "brew", Installed: true, Tracked: false},
	}

	result, err := a.SyncAll(context.Background(), app.SyncAllOptions{Discovered: discovered})
	if err != nil {
		t.Fatalf("SyncAll: %v", err)
	}
	if len(result.ClaimedNames) != 0 {
		t.Fatalf("ClaimedNames = %v, want ignored discovered tool skipped", result.ClaimedNames)
	}
	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	hostGroup := findTestGroup(updated, "testhost")
	if hostGroup != nil && testGroupHasTool(hostGroup, "fzf") {
		t.Fatalf("ignored discovered fzf was claimed into host group: %+v", hostGroup.Tools)
	}
}

func TestImportSkipsGloballyIgnoredName(t *testing.T) {
	t.Parallel()
	stub := &ignoredProcessingStub{
		stubProvider: stubProvider{name: "brew", available: true},
		listInstalled: []provider.InstalledTool{
			{Tool: provider.Tool{Name: "ripgrep", Provider: "brew"}, Version: "14.1.0"},
			{Tool: provider.Tool{Name: "fd", Provider: "brew"}, Version: "9.0.0"},
		},
	}
	a, cfgPath := newImportApp(t, stub)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Ignore: config.GlobalIgnore{Tools: []string{"ripgrep"}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	result, err := a.Import(context.Background(), app.ImportOptions{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(result.Added) != 1 || result.Added[0].Name != "fd" {
		t.Fatalf("Import added = %+v, want only fd", result.Added)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	for _, group := range cfg.Groups {
		if logicalTestGroupHasTool(group, "ripgrep") {
			t.Fatalf("globally ignored ripgrep was imported into group %q", group.BaseName())
		}
	}
}

func TestUpgradePathsSkipIgnoredTools(t *testing.T) {
	t.Parallel()
	stub := &ignoredProcessingStub{
		stubProvider: stubProvider{name: "brew", available: true},
		installed:    map[string]string{"ripgrep": "14.1.0"},
		outdated:     map[string]string{"ripgrep": "15.0.0"},
	}
	a, cfgPath := newImportApp(t, stub)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{Tools: groupTools("ripgrep")}},
		Ignore: config.GlobalIgnore{Tools: []string{"ripgrep"}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	ctx := context.Background()
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "ripgrep",
		Provider:      "brew",
		Package:       "ripgrep",
		Installed:     true,
		InstalledWith: "brew",
		Outdated:      true,
		Tracked:       true,
		LastChecked:   time.Now(),
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	result, err := a.UpgradeAllDetailed(ctx, nil, nil)
	if err != nil {
		t.Fatalf("UpgradeAllDetailed: %v", err)
	}
	if len(result.Upgraded) != 0 || len(result.Failures) != 0 || stub.upgradeCalls != 0 {
		t.Fatalf("upgrade-all result=%+v calls=%d, want no ignored upgrades", result, stub.upgradeCalls)
	}
	if err := a.Upgrade(ctx, "ripgrep", "brew"); err == nil || !strings.Contains(err.Error(), "ignored") {
		t.Fatalf("Upgrade ignored error = %v, want ignored rejection", err)
	}
	if err := a.Install(ctx, "ripgrep", "brew"); err == nil || !strings.Contains(err.Error(), "ignored") {
		t.Fatalf("Install ignored error = %v, want ignored rejection", err)
	}
	if stub.upgradeCalls != 0 || stub.installCalls != 0 {
		t.Fatalf("provider calls upgrade/install = %d/%d, want none", stub.upgradeCalls, stub.installCalls)
	}
}

func toolNames(tools []*database.ToolCache) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		if t != nil {
			names = append(names, t.Name)
		}
	}
	return names
}

func viewFromCache(t *database.ToolCache) *app.ToolView {
	if t == nil {
		return nil
	}
	return &app.ToolView{
		ID:                 t.ID,
		Name:               t.Name,
		Provider:           t.Provider,
		Package:            t.Package,
		Installed:          t.Installed,
		InstalledWith:      t.InstalledWith,
		Version:            t.Version.String,
		Outdated:           t.Outdated,
		LatestVersion:      t.LatestVersion.String,
		Description:        t.Description.String,
		LastChecked:        t.LastChecked,
		FailedAt:           t.FailedAt,
		FailureCount:       t.FailureCount,
		LastError:          t.LastError.String,
		Tracked:            t.Tracked,
		Privilege:          t.Privilege,
		PrivilegeReason:    t.PrivilegeReason.String,
		PrivilegeAt:        t.PrivilegeAt,
		Options:            t.Options,
		UpdateBlocked:      t.UpdateBlocked,
		UpdateBlockedUntil: t.UpdateBlockedUntil,
		UpdateAvailableAt:  t.UpdateAvailableAt,
		UpdateDateSource:   t.UpdateDateSource,
	}
}

func viewNames(tools []*app.ToolView) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		if t != nil {
			names = append(names, t.Name)
		}
	}
	return names
}

func queryToolNames(items []app.ToolListItem) []string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		if item.Tool != nil {
			names = append(names, item.Tool.Name)
		}
	}
	return names
}
