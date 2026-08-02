package app_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
	gosync "github.com/lkshrk/omni/internal/sync"
)

type listErrProvider struct {
	stubProvider
	err error
}

func (p *listErrProvider) ListInstalled(_ context.Context) ([]provider.InstalledTool, error) {
	return nil, p.err
}

func upsertInstalled(t *testing.T, db *database.DB, name, prov string) {
	t.Helper()
	cacheProvider := prov
	installedWith := ""
	if ecosystem := testEcosystemForConcrete(prov); ecosystem != "" {
		cacheProvider = ecosystem
		installedWith = prov
	}
	err := db.Upsert(context.Background(), &database.ToolCache{
		Name:          name,
		Provider:      cacheProvider,
		Package:       name,
		Installed:     true,
		InstalledWith: installedWith,
		Version:       sql.NullString{String: "1.0", Valid: true},
		LastChecked:   time.Now(),
	})
	if err != nil {
		t.Fatalf("upsert %s/%s: %v", prov, name, err)
	}
}

func testEcosystemForConcrete(providerName string) string {
	return ""
}

func TestCheckSatisfiedGroups_FullySatisfied(t *testing.T) {
	t.Parallel()
	brew := &stubProvider{name: "brew", available: true}
	a, cfgPath := newImportApp(t, brew)

	rootCfg := &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "brew"),
			logicalTool("slack", "brew"),
		),
		Groups: []*config.GroupConfig{
			testHostToolGroup("ripgrep"),
			{Name: "work", Tools: groupTools("slack")},
		},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatal(err)
	}
	upsertInstalled(t, a.DB(), "slack", "brew")

	satisfied, err := a.CheckSatisfiedGroups(context.Background(), []string{testShortHostname()})
	if err != nil {
		t.Fatalf("CheckSatisfiedGroups: %v", err)
	}
	if len(satisfied) != 1 || satisfied[0] != "work" {
		t.Errorf("satisfied = %v, want [work]", satisfied)
	}
}

func TestCheckSatisfiedGroups_PartiallyInstalled(t *testing.T) {
	t.Parallel()
	brew := &stubProvider{name: "brew", available: true}
	a, cfgPath := newImportApp(t, brew)

	rootCfg := &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "brew"),
			logicalTool("slack", "brew"),
			logicalTool("zoom", "brew"),
		),
		Groups: []*config.GroupConfig{
			testHostToolGroup("ripgrep"),
			{Name: "work", Tools: groupTools("slack", "zoom")},
		},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatal(err)
	}
	upsertInstalled(t, a.DB(), "slack", "brew") // zoom NOT installed

	satisfied, err := a.CheckSatisfiedGroups(context.Background(), []string{testShortHostname()})
	if err != nil {
		t.Fatalf("CheckSatisfiedGroups: %v", err)
	}
	if len(satisfied) != 0 {
		t.Errorf("expected no satisfied groups (zoom missing), got %v", satisfied)
	}
}

func TestCheckSatisfiedGroups_EmptyGroupSkipped(t *testing.T) {
	t.Parallel()
	brew := &stubProvider{name: "brew", available: true}
	a, cfgPath := newImportApp(t, brew)

	rootCfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{
			testHostToolGroup("ripgrep"),
			{Name: "empty", Tools: nil},
		},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatal(err)
	}

	satisfied, err := a.CheckSatisfiedGroups(context.Background(), []string{testShortHostname()})
	if err != nil {
		t.Fatalf("CheckSatisfiedGroups: %v", err)
	}
	if len(satisfied) != 0 {
		t.Errorf("empty group should be skipped, got %v", satisfied)
	}
}

func TestCheckSatisfiedGroups_ActiveGroupExcluded(t *testing.T) {
	t.Parallel()
	brew := &stubProvider{name: "brew", available: true}
	a, cfgPath := newImportApp(t, brew)

	rootCfg := &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "brew"),
			logicalTool("slack", "brew"),
		),
		Groups: []*config.GroupConfig{
			testHostToolGroup("ripgrep"),
			{Name: "work", Tools: groupTools("slack")},
		},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatal(err)
	}
	upsertInstalled(t, a.DB(), "slack", "brew")

	satisfied, err := a.CheckSatisfiedGroups(context.Background(), []string{testShortHostname(), "work"})
	if err != nil {
		t.Fatalf("CheckSatisfiedGroups: %v", err)
	}
	if len(satisfied) != 0 {
		t.Errorf("active group should be excluded, got %v", satisfied)
	}
}

func TestRefreshDiscovered_BaselinesSystemPackagesOnFirstObservation(t *testing.T) {
	t.Parallel()
	apt := &stubProvider{
		name:      "apt",
		available: true,
		installed: []provider.InstalledTool{
			installedTool("ripgrep", "1.0", "apt"),
			installedTool("libxcomposite1", "1.0", "apt"),
		},
	}
	brew := &stubProvider{
		name:      "brew",
		available: true,
		installed: []provider.InstalledTool{
			installedTool("bat", "1.0", "brew"),
			installedTool("fzf", "1.0", "brew"),
		},
	}
	a, cfgPath := newImportApp(t, apt, brew)
	short := testShortHostname()
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "apt"),
			logicalTool("bat", "brew"),
		),
		Groups: []*config.GroupConfig{
			{Name: short, Special: "host", Tools: groupTools("ripgrep", "bat")},
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := a.RefreshDiscovered(context.Background()); err != nil {
		t.Fatalf("RefreshDiscovered: %v", err)
	}

	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, ok := updated.Tools["libxcomposite1"]; ok {
		t.Fatalf("config tools = %+v, want system apt package left untracked", updated.Tools)
	}
	if logicalTestGroupByName(updated, config.SystemInventoryGroup) != nil {
		t.Fatalf("provider inventory group should not be auto-created")
	}
	discovered, err := a.ListDiscovered(context.Background())
	if err != nil {
		t.Fatalf("ListDiscovered: %v", err)
	}
	if len(discovered) != 1 || discovered[0].Name != "fzf" {
		t.Fatalf("discovered = %+v, want only non-system fzf visible Out of Sync", discovered)
	}
}

func TestRefreshDiscovered_SurfacesSystemPackageInstalledAfterBaseline(t *testing.T) {
	t.Parallel()
	apt := &stubProvider{
		name:      "apt",
		available: true,
		installed: []provider.InstalledTool{
			installedTool("ripgrep", "1.0", "apt"),        // configured tool, in scope
			installedTool("libxcomposite1", "1.0", "apt"), // pre-existing image package
		},
	}
	a, cfgPath := newImportApp(t, apt)
	short := testShortHostname()
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("ripgrep", "apt")),
		Groups: []*config.GroupConfig{{Name: short, Special: "host", Tools: groupTools("ripgrep")}},
	}); err != nil {
		t.Fatal(err)
	}

	if err := a.RefreshDiscovered(context.Background()); err != nil {
		t.Fatalf("RefreshDiscovered (baseline): %v", err)
	}
	discovered, err := a.ListDiscovered(context.Background())
	if err != nil {
		t.Fatalf("ListDiscovered: %v", err)
	}
	if len(discovered) != 0 {
		t.Fatalf("discovered after baseline = %+v, want none", discovered)
	}

	apt.installed = append(apt.installed, installedTool("htop", "1.0", "apt"))
	if err := a.RefreshDiscovered(context.Background()); err != nil {
		t.Fatalf("RefreshDiscovered (post-baseline): %v", err)
	}
	discovered, err = a.ListDiscovered(context.Background())
	if err != nil {
		t.Fatalf("ListDiscovered: %v", err)
	}
	if len(discovered) != 1 || discovered[0].Name != "htop" {
		t.Fatalf("discovered = %+v, want only post-baseline htop visible", discovered)
	}
}

func TestRefreshDiscovered_BaselinesEverySystemPackageProviderOnFirstObservation(t *testing.T) {
	t.Parallel()
	for _, providerName := range []string{"apt", "dnf", "pacman", "apk", "zypper"} {
		t.Run(providerName, func(t *testing.T) {
			t.Parallel()
			p := &stubProvider{
				name:      providerName,
				available: true,
				installed: []provider.InstalledTool{
					installedTool("tracked", "1.0", providerName),
					installedTool("system-package", "1.0", providerName),
				},
			}
			a, cfgPath := newImportApp(t, p)
			if err := saveAppConfig(t, cfgPath, &config.RootConfig{
				Tools:  logicalToolSpecs(logicalTool("tracked", providerName)),
				Groups: []*config.GroupConfig{{Name: testShortHostname(), Special: "host", Tools: groupTools("tracked")}},
			}); err != nil {
				t.Fatal(err)
			}

			if err := a.RefreshDiscovered(context.Background()); err != nil {
				t.Fatalf("RefreshDiscovered: %v", err)
			}
			updated, err := config.Load(cfgPath)
			if err != nil {
				t.Fatalf("config.Load: %v", err)
			}
			if _, ok := updated.Tools["system-package"]; ok {
				t.Fatalf("config tools = %+v, want %s system package left untracked", updated.Tools, providerName)
			}
			if logicalTestGroupByName(updated, config.SystemInventoryGroup) != nil {
				t.Fatalf("provider inventory group should not be auto-created")
			}
			discovered, err := a.ListDiscovered(context.Background())
			if err != nil {
				t.Fatalf("ListDiscovered: %v", err)
			}
			if len(discovered) != 0 {
				t.Fatalf("discovered = %+v, want no system package rows", discovered)
			}
		})
	}
}

func TestSync_HostActive_SystemPackageOrphansUseBaseline(t *testing.T) {
	t.Parallel()
	apt := &stubProvider{
		name:      "apt",
		available: true,
		installed: []provider.InstalledTool{
			installedTool("ripgrep", "1.0", "apt"),        // configured
			installedTool("libxcomposite1", "1.0", "apt"), // pre-existing image package
		},
	}
	a, cfgPath := newImportApp(t, apt)
	short := testShortHostname()
	rootCfg := &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("ripgrep", "apt")),
		Groups: []*config.GroupConfig{{Name: short, Special: "host", Tools: groupTools("ripgrep")}},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatal(err)
	}

	if _, err := a.Sync(context.Background(), gosync.SyncOptions{}); err != nil {
		t.Fatalf("Sync (baseline): %v", err)
	}
	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	hostGroup := findTestGroup(updated, short)
	if hostGroup == nil {
		t.Fatalf("hostname group not found, groups=%+v", updated.Groups)
	}
	if testGroupHasTool(hostGroup, "libxcomposite1") {
		t.Fatalf("hostname group tools = %+v, want baseline package left unclaimed", hostGroup.Tools)
	}

	apt.installed = append(apt.installed, installedTool("htop", "1.0", "apt"))
	if _, err := a.Sync(context.Background(), gosync.SyncOptions{}); err != nil {
		t.Fatalf("Sync (post-baseline): %v", err)
	}
	updated, err = config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	hostGroup = findTestGroup(updated, short)
	if hostGroup == nil {
		t.Fatalf("hostname group not found, groups=%+v", updated.Groups)
	}
	if !testGroupHasTool(hostGroup, "htop") {
		t.Fatalf("hostname group tools = %+v, want post-baseline htop claimed", hostGroup.Tools)
	}
	if testGroupHasTool(hostGroup, "libxcomposite1") {
		t.Fatalf("hostname group tools = %+v, want baseline package still unclaimed", hostGroup.Tools)
	}
}

func TestSync_HostActive_OrphansAddedToHostnameGroup(t *testing.T) {
	t.Parallel()
	brew := &stubProvider{
		name:      "brew",
		available: true,
		installed: []provider.InstalledTool{
			installedTool("slack", "1.0", "brew"), // in reusable host-assigned group
			installedTool("fd", "8.0", "brew"),    // orphan
		},
	}
	a, cfgPath := newImportApp(t, brew)
	short := testShortHostname()

	rootCfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("slack", "brew")),
		Groups: []*config.GroupConfig{
			{Name: short, Special: "host"},
			{Name: "work", Tools: groupTools("slack")},
		},
		Hosts: map[string][]string{short: {"work"}},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatal(err)
	}

	_, err := a.Sync(context.Background(), gosync.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	var hostGroup *config.GroupConfig
	for _, g := range updated.Groups {
		if g.Name == short {
			hostGroup = g
			break
		}
	}
	if hostGroup == nil {
		t.Fatalf("hostname group %q not found in config", short)
	}
	found := false
	for _, tool := range hostGroup.Tools {
		if tool.Name == "fd" {
			found = true
		}
		if tool.Name == "slack" {
			t.Error("slack is in a host-assigned reusable group and should not be in the hostname group")
		}
	}
	if !found {
		t.Errorf("fd (orphan) not found in hostname group, tools = %v", hostGroup.Tools)
	}
}

func TestSyncAll_ClaimsDiscoveredToHostnameGroupAndSyncs(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	brew := &installTracker{
		stubProvider: stubProvider{name: "brew", available: true},
		installed:    []provider.InstalledTool{installedTool("fzf", "1.0", "brew")},
	}
	a, cfgPath := newImportApp(t, brew)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{testHostToolGroup("ripgrep")},
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
	if len(result.ClaimedNames) != 1 || result.ClaimedNames[0] != "fzf" {
		t.Fatalf("ClaimedNames = %v, want [fzf]", result.ClaimedNames)
	}
	if len(brew.installCalled) != 1 || brew.installCalled[0] != "ripgrep" {
		t.Fatalf("installCalled = %v, want [ripgrep]", brew.installCalled)
	}

	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	hostGroup := findTestGroup(updated, "testhost")
	if hostGroup == nil {
		t.Fatalf("hostname group not found, groups=%+v", updated.Groups)
	}
	if !testGroupHasTool(hostGroup, "fzf") {
		t.Fatalf("hostname group tools = %+v, want claimed fzf", hostGroup.Tools)
	}
}

func TestSyncAllWithStateReturnsUpdatedToolsAndGroups(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	brew := &lifecycleProvider{stubProvider: stubProvider{name: "brew", available: true}, installed: true}
	system := &lifecycleProvider{
		stubProvider: stubProvider{name: "brew", available: true},
		resolvedName: "brew",
		installed:    true,
	}
	a, cfgPath := newImportApp(t, brew, system)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{Groups: []*config.GroupConfig{testHostGroup()}}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	discovered := []*app.ToolView{
		{Name: "fzf", Provider: "brew", Package: "fzf", InstalledWith: "brew", Installed: true, Tracked: false},
	}

	result, err := a.SyncAllWithState(context.Background(), app.SyncAllOptions{Discovered: discovered})
	if err != nil {
		t.Fatalf("SyncAllWithState: %v", err)
	}
	if result.Result == nil || !slices.Equal(result.Result.ClaimedNames, []string{"fzf"}) {
		t.Fatalf("SyncAll result = %+v, want claimed fzf", result.Result)
	}
	toolKey := "fzf\x00brew"
	if got := result.State.ToolMemberships[toolKey]; !slices.Equal(got, []string{"testhost"}) {
		t.Fatalf("ToolMemberships[%q] = %v, want [testhost]", toolKey, got)
	}
	for _, tool := range result.Tools {
		if tool.Name == "fzf" && tool.Provider == "brew" && tool.Installed {
			return
		}
	}
	t.Fatalf("Tools = %+v, want installed fzf/brew", result.Tools)
}

func TestSyncAll_ClaimsMultipleDiscoveredTools(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	brew := &lifecycleProvider{stubProvider: stubProvider{name: "brew", available: true}, installed: true}
	system := &lifecycleProvider{
		stubProvider: stubProvider{name: "brew", available: true},
		resolvedName: "brew",
		installed:    true,
	}
	a, cfgPath := newImportApp(t, brew, system)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{Groups: []*config.GroupConfig{testHostGroup()}}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	discovered := []*app.ToolView{
		{Name: "fzf", Provider: "brew", Package: "fzf", InstalledWith: "brew", Installed: true, Tracked: false},
		{Name: "fd", Provider: "brew", Package: "fd", InstalledWith: "brew", Installed: true, Tracked: false},
	}
	if err := a.DB().UpsertDiscoveredBatch(context.Background(), []database.DiscoveredUpsert{
		{Name: "fzf", Provider: "brew", InstalledWith: "brew", Version: "1.0.0"},
		{Name: "fd", Provider: "brew", InstalledWith: "brew", Version: "2.0.0"},
	}); err != nil {
		t.Fatalf("UpsertDiscoveredBatch: %v", err)
	}

	var progress []string
	result, err := a.SyncAll(context.Background(), app.SyncAllOptions{
		Discovered: discovered,
		ToolProgress: func(event gosync.ProgressEvent) {
			progress = append(progress, event.Message)
		},
	})
	if err != nil {
		t.Fatalf("SyncAll: %v", err)
	}
	if !slices.Equal(result.ClaimedNames, []string{"fzf", "fd"}) {
		t.Fatalf("ClaimedNames = %v, want [fzf fd]", result.ClaimedNames)
	}
	if !slices.Contains(progress, "Added fzf to config") || !slices.Contains(progress, "Added fd to config") {
		t.Fatalf("progress = %v, want both discovered tools marked added", progress)
	}
	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	hostGroup := findTestGroup(updated, "testhost")
	if hostGroup == nil || !testGroupHasTool(hostGroup, "fzf") || !testGroupHasTool(hostGroup, "fd") {
		t.Fatalf("hostname group tools = %+v, want fzf and fd", hostGroup)
	}
	left, err := a.ListDiscovered(context.Background())
	if err != nil {
		t.Fatalf("ListDiscovered: %v", err)
	}
	if len(left) != 0 {
		t.Fatalf("ListDiscovered after claims = %+v, want none", left)
	}
}

func TestSyncAll_ClaimResolvedDefaultConcreteDoesNotWriteInstallWith(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	brew := &lifecycleProvider{stubProvider: stubProvider{name: "brew", available: true}, installed: true}
	system := &lifecycleProvider{
		stubProvider: stubProvider{name: "brew", available: true},
		resolvedName: "brew",
		installed:    true,
	}
	a, cfgPath := newImportApp(t, brew, system)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{Groups: []*config.GroupConfig{testHostGroup()}}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	discovered := []*app.ToolView{
		{Name: "fzf", Provider: "brew", InstalledWith: "brew", Installed: true, Tracked: false},
	}

	result, err := a.SyncAll(context.Background(), app.SyncAllOptions{Discovered: discovered})
	if err != nil {
		t.Fatalf("SyncAll: %v", err)
	}
	if len(result.ClaimedNames) != 1 || result.ClaimedNames[0] != "fzf" {
		t.Fatalf("ClaimedNames = %v, want [fzf]", result.ClaimedNames)
	}
	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	spec := updated.Tools["fzf"]
	if len(spec.Providers) != 1 || spec.Providers[0].Provider != "brew" {
		t.Fatalf("fzf spec = %+v, want brew provider entry", spec)
	}
}

func TestSyncAll_ClaimNodeToolWritesConcreteNotFamily(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	bun := &lifecycleProvider{stubProvider: stubProvider{name: "bun", available: true}, installed: true}
	a, cfgPath := newImportApp(t, bun)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{Groups: []*config.GroupConfig{testHostGroup()}}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	discovered := []*app.ToolView{
		{Name: "tsx", Provider: "bun", InstalledWith: "bun", Installed: true, Tracked: false},
	}

	if _, err := a.SyncAll(context.Background(), app.SyncAllOptions{Discovered: discovered}); err != nil {
		t.Fatalf("SyncAll: %v", err)
	}
	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	spec := updated.Tools["tsx"]
	if len(spec.Providers) != 1 || spec.Providers[0].Provider != "bun" {
		t.Fatalf("tsx spec = %+v, want concrete bun provider (not node family)", spec)
	}
	if errs := config.ValidateRoot(updated, config.ProviderValidation{}); len(errs) > 0 {
		t.Fatalf("config did not validate clean after claim: %v", errs)
	}
}

func TestSyncAll_DryRunDoesNotWriteClaims(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	brew := &installTracker{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, brew)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{Groups: []*config.GroupConfig{testHostGroup()}}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	discovered := []*app.ToolView{
		{Name: "fzf", Provider: "brew", Installed: true, Tracked: false},
	}

	result, err := a.SyncAll(context.Background(), app.SyncAllOptions{Discovered: discovered, DryRun: true})
	if err != nil {
		t.Fatalf("SyncAll dry-run: %v", err)
	}
	if len(result.ClaimedNames) != 1 || result.ClaimedNames[0] != "fzf" {
		t.Fatalf("ClaimedNames = %v, want dry-run planned claim [fzf]", result.ClaimedNames)
	}

	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if hostGroup := findTestGroup(updated, "testhost"); hostGroup != nil && len(hostGroup.Tools) > 0 {
		t.Fatalf("dry-run wrote hostname group claims: %+v", hostGroup.Tools)
	}
}

func TestSyncAll_ReportsInvalidDiscoveredClaimWithoutWritingConfig(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	brew := &installTracker{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, brew)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{Groups: []*config.GroupConfig{testHostGroup()}}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	discovered := []*app.ToolView{
		{Name: "prettier", Provider: "node", Package: "prettier", Installed: true, Tracked: false},
	}

	result, err := a.SyncAll(context.Background(), app.SyncAllOptions{Discovered: discovered})
	if err == nil {
		t.Fatal("SyncAll err = nil, want invalid discovered provider error")
	}
	if !strings.Contains(err.Error(), `provider "node" must be concrete`) {
		t.Fatalf("SyncAll err = %v, want concrete-provider validation", err)
	}
	if len(result.ClaimedNames) != 0 {
		t.Fatalf("ClaimedNames = %v, want none", result.ClaimedNames)
	}
	if len(result.Failures) != 1 || result.Failures[0].Name != "prettier" || result.Failures[0].Provider != "node" {
		t.Fatalf("Failures = %+v, want prettier/node claim failure", result.Failures)
	}

	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, ok := updated.Tools["prettier"]; ok {
		t.Fatalf("prettier was written despite failed discovered claim: %+v", updated.Tools["prettier"])
	}
}

func TestSyncAll_DryRunDiscoversWithoutWritingDB(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	brew := &installTracker{
		stubProvider: stubProvider{
			name:      "brew",
			available: true,
			installed: []provider.InstalledTool{
				installedTool("fzf", "0.60.0", "brew"),
			},
		},
	}
	a, cfgPath := newImportApp(t, brew)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{testHostToolGroup("ripgrep")},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	result, err := a.SyncAll(context.Background(), app.SyncAllOptions{DryRun: true})
	if err != nil {
		t.Fatalf("SyncAll dry-run: %v", err)
	}
	if len(result.ClaimedNames) != 1 || result.ClaimedNames[0] != "fzf" {
		t.Fatalf("ClaimedNames = %v, want dry-run planned claim [fzf]", result.ClaimedNames)
	}
	if len(brew.installCalled) != 0 {
		t.Fatalf("dry-run should not install, got %v", brew.installCalled)
	}

	cached, err := a.DB().List(context.Background())
	if err != nil {
		t.Fatalf("DB.List: %v", err)
	}
	if len(cached) != 0 {
		t.Fatalf("dry-run wrote DB rows: %+v", cached)
	}
}

func TestSync_HostActive_NoOrphansSkipsHostnameGroup(t *testing.T) {
	t.Parallel()
	brew := &stubProvider{
		name:      "brew",
		available: true,
		installed: []provider.InstalledTool{
			installedTool("slack", "1.0", "brew"), // in reusable host-assigned group; no orphans
		},
	}
	a, cfgPath := newImportApp(t, brew)
	short := testShortHostname()

	rootCfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("slack", "brew")),
		Groups: []*config.GroupConfig{
			{Name: short, Special: "host"},
			{Name: "work", Tools: groupTools("slack")},
		},
		Hosts: map[string][]string{short: {"work"}},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatal(err)
	}

	_, err := a.Sync(context.Background(), gosync.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	for _, g := range updated.Groups {
		if g.Name == short && len(g.Tools) > 0 {
			t.Errorf("hostname group %q should be empty or absent when there are no orphans, got tools: %v", short, g.Tools)
		}
	}
}

func findTestGroup(cfg *config.RootConfig, name string) *config.GroupConfig {
	for _, g := range cfg.Groups {
		if g.Name == name {
			return g
		}
	}
	return nil
}

func testGroupHasTool(group *config.GroupConfig, name string) bool {
	if group == nil {
		return false
	}
	for _, tool := range group.Tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func TestSync_HostActive_ReturnsSatisfiedGroups(t *testing.T) {
	t.Parallel()
	brew := &stubProvider{
		name:      "brew",
		available: true,
		installed: []provider.InstalledTool{{Tool: provider.Tool{Name: "slack", Provider: "brew"}}},
	}
	a, cfgPath := newImportApp(t, brew)
	short := testShortHostname()

	rootCfg := &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "brew"),
			logicalTool("slack", "brew"),
		),
		Groups: []*config.GroupConfig{
			{Name: short, Special: "host", Tools: groupTools("ripgrep")},
			{Name: "work", Tools: groupTools("slack")},
		},
		Hosts: map[string][]string{short: {}},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatal(err)
	}
	upsertInstalled(t, a.DB(), "slack", "brew")

	result, err := a.Sync(context.Background(), gosync.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	found := false
	for _, g := range result.SatisfiedGroups {
		if g == "work" {
			found = true
		}
	}
	if !found {
		t.Errorf("SatisfiedGroups = %v, expected work to be satisfied", result.SatisfiedGroups)
	}
}

func TestSync_HostActive_HostnameGroupInjected(t *testing.T) {
	t.Parallel()
	brew := &installTracker{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, brew)
	short := testShortHostname()

	rootCfg := &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("slack", "brew"),
			logicalTool("fd", "brew"),
		),
		Groups: []*config.GroupConfig{
			{Name: "work", Tools: groupTools("slack")},
			{Name: short, Special: "host", Tools: groupTools("fd")},
		},
		Hosts: map[string][]string{short: {"work"}},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatal(err)
	}

	_, err := a.Sync(context.Background(), gosync.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	installed := make(map[string]bool, len(brew.installCalled))
	for _, n := range brew.installCalled {
		installed[n] = true
	}
	if !installed["slack"] {
		t.Error("slack not installed; expected via work group")
	}
	if !installed["fd"] {
		t.Error("fd not installed; expected via hostname group injection")
	}
}

func TestClaimFromMachineGroup_PrunesMachineGroup(t *testing.T) {
	t.Parallel()
	a, cfgPath := newImportApp(t)
	short := testShortHostname()

	rootCfg := &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("fd", "brew"),
			logicalTool("ripgrep", "brew"),
		),
		Groups: []*config.GroupConfig{
			{Name: short, Special: "host", Tools: groupTools("ripgrep")},
			{Name: "tools", Tools: groupTools("fd")},
		},
		Hosts: map[string][]string{short: {}},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatal(err)
	}

	if err := a.ClaimFromMachineGroup("tools"); err != nil {
		t.Fatalf("ClaimFromMachineGroup: %v", err)
	}

	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got := updated.Hosts[short]; !slices.Contains(got, "tools") {
		t.Errorf("host groups = %v, want 'tools' to be present", got)
	}

	var hg *config.GroupConfig
	for _, g := range updated.Groups {
		if g.Name == short {
			hg = g
			break
		}
	}
	if hg == nil {
		t.Fatalf("hostname group %q not found", short)
	}
	for _, tool := range hg.Tools {
		if tool.Name == "fd" {
			t.Error("fd should have been pruned from hostname group after claim")
		}
	}
	foundRipgrep := false
	for _, tool := range hg.Tools {
		if tool.Name == "ripgrep" {
			foundRipgrep = true
		}
	}
	if !foundRipgrep {
		t.Error("ripgrep should remain in hostname group")
	}
}

func TestSync_OrphanScanProviderError_SurfacedAsWarning(t *testing.T) {
	t.Parallel()
	scanErr := errors.New("boom: list installed failed")
	bad := &listErrProvider{
		stubProvider: stubProvider{name: "brew", available: true},
		err:          scanErr,
	}
	a, cfgPath := newImportApp(t, bad)
	short := testShortHostname()

	rootCfg := &config.RootConfig{
		Groups: []*config.GroupConfig{
			{Name: short, Special: "host"},
		},
		Hosts: map[string][]string{short: {}},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatal(err)
	}

	result, err := a.Sync(context.Background(), gosync.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "brew") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a warning mentioning provider %q, got warnings: %v", "brew", result.Warnings)
	}
}
