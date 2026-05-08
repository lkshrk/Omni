package app_test

import (
	"context"
	"database/sql"
	"slices"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
	gosync "github.com/lkshrk/omni/internal/sync"
)

// ─── CheckSatisfiedGroups ─────────────────────────────────────────────────────

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
	switch providerName {
	case "brew", "apt", "apk", "dnf", "pacman", "zypper":
		return "system"
	case "pip", "pip3", "uv":
		return "python"
	case "npm", "pnpm", "bun":
		return "node"
	default:
		return ""
	}
}

func TestCheckSatisfiedGroups_FullySatisfied(t *testing.T) {
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

	// work is in the active set → should not be returned.
	satisfied, err := a.CheckSatisfiedGroups(context.Background(), []string{testShortHostname(), "work"})
	if err != nil {
		t.Fatalf("CheckSatisfiedGroups: %v", err)
	}
	if len(satisfied) != 0 {
		t.Errorf("active group should be excluded, got %v", satisfied)
	}
}

// ─── syncOrphansToMachineGroup (via Sync) ────────────────────────────────────

func TestSync_HostActive_OrphansAddedToHostnameGroup(t *testing.T) {
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

	// fd (orphan) must land in the hostname group within settings.json.
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
	discovered := []*database.ToolCache{
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

func TestSyncAll_ClaimResolvedDefaultConcreteDoesNotWriteInstallWith(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	brew := &lifecycleProvider{stubProvider: stubProvider{name: "brew", available: true}, installed: true}
	system := &lifecycleProvider{
		stubProvider: stubProvider{name: "system", available: true},
		resolvedName: "brew",
		installed:    true,
	}
	a, cfgPath := newImportApp(t, brew, system)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{Groups: []*config.GroupConfig{testHostGroup()}}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	discovered := []*database.ToolCache{
		{Name: "fzf", Provider: "system", InstalledWith: "brew", Installed: true, Tracked: false},
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
	if spec.Provider != "system" || spec.InstallWith != "" {
		t.Fatalf("fzf spec = %+v, want provider system without install_with", spec)
	}
}

func TestSyncAll_NormalizesHostDefaultInstallOverrides(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost.example.com")
	brew := &lifecycleProvider{stubProvider: stubProvider{name: "brew", available: true}, installed: true}
	system := &lifecycleProvider{
		stubProvider: stubProvider{name: "system", available: true},
		resolvedName: "brew",
		installed:    true,
	}
	a, cfgPath := newImportApp(t, brew, system)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": {
				Provider: "system",
				Hosts: map[string]config.ToolInstallSpec{
					"testhost": {Provider: "system", InstallWith: "brew"},
				},
			},
			"jq": {
				Provider:    "system",
				InstallWith: "brew",
				Hosts: map[string]config.ToolInstallSpec{
					"testhost": {Provider: "system", InstallWith: "brew"},
				},
			},
		},
		Groups: []*config.GroupConfig{testHostToolGroup("ripgrep", "jq")},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	result, err := a.SyncAll(context.Background(), app.SyncAllOptions{Discovered: []*database.ToolCache{}})
	if err != nil {
		t.Fatalf("SyncAll: %v", err)
	}
	if len(result.NormalizedProviderOverrides) != 2 {
		t.Fatalf("NormalizedProviderOverrides = %+v, want 2 entries", result.NormalizedProviderOverrides)
	}
	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, ok := updated.Tools["ripgrep"].Hosts["testhost"]; ok {
		t.Fatalf("ripgrep host override still present: %+v", updated.Tools["ripgrep"].Hosts)
	}
	jq := updated.Tools["jq"]
	if jq.InstallWith != "brew" {
		t.Fatalf("jq global install_with = %q, want preserved brew", jq.InstallWith)
	}
	if jq.Hosts["testhost"].InstallWith != "" {
		t.Fatalf("jq host install_with = %q, want cleared", jq.Hosts["testhost"].InstallWith)
	}
}

func TestSyncAll_DryRunDoesNotWriteClaims(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	brew := &installTracker{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, brew)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{Groups: []*config.GroupConfig{testHostGroup()}}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	discovered := []*database.ToolCache{
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
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{Groups: []*config.GroupConfig{testHostGroup()}}); err != nil {
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

	// No orphans → hostname group must NOT be created with tools.
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
	brew := &stubProvider{
		name:      "brew",
		available: true,
		installed: []provider.InstalledTool{},
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

// ─── hostname group injection ─────────────────────────────────────────────────

func TestSync_HostActive_HostnameGroupInjected(t *testing.T) {
	brew := &installTracker{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, brew)
	short := testShortHostname()

	// fd lives in the hostname group (machine-local); slack is in the work group.
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

	// Both slack (from the host-assigned reusable group) and fd (from hostname group injection) must be installed.
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

// ─── ClaimFromMachineGroup ────────────────────────────────────────────────────

func TestClaimFromMachineGroup_PrunesMachineGroup(t *testing.T) {
	a, cfgPath := newImportApp(t)
	short := testShortHostname()

	// Hostname group has fd and ripgrep; the "tools" group also claims fd.
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

	// fd must not be copied into the hostname group; ripgrep must remain.
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
