package app_test

import (
	"context"
	"database/sql"
	"os"
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
			{Tools: groupTools("ripgrep")},
			{Name: "work", Tools: groupTools("slack")},
		},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatal(err)
	}
	upsertInstalled(t, a.DB(), "slack", "brew")

	satisfied, err := a.CheckSatisfiedGroups(context.Background(), []string{"base"})
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
			{Tools: groupTools("ripgrep")},
			{Name: "work", Tools: groupTools("slack", "zoom")},
		},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatal(err)
	}
	upsertInstalled(t, a.DB(), "slack", "brew") // zoom NOT installed

	satisfied, err := a.CheckSatisfiedGroups(context.Background(), []string{"base"})
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
			{Tools: groupTools("ripgrep")},
			{Name: "empty", Tools: nil},
		},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatal(err)
	}

	satisfied, err := a.CheckSatisfiedGroups(context.Background(), []string{"base"})
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
			{Tools: groupTools("ripgrep")},
			{Name: "work", Tools: groupTools("slack")},
		},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatal(err)
	}
	upsertInstalled(t, a.DB(), "slack", "brew")

	// work is in the active set → should not be returned.
	satisfied, err := a.CheckSatisfiedGroups(context.Background(), []string{"base", "work"})
	if err != nil {
		t.Fatalf("CheckSatisfiedGroups: %v", err)
	}
	if len(satisfied) != 0 {
		t.Errorf("active group should be excluded, got %v", satisfied)
	}
}

// ─── syncOrphansToMachineGroup (via Sync) ────────────────────────────────────

func TestSync_ProfileActive_OrphansAddedToHostnameGroup(t *testing.T) {
	brew := &stubProvider{
		name:      "brew",
		available: true,
		installed: []provider.InstalledTool{
			installedTool("slack", "1.0", "brew"), // in profile group
			installedTool("fd", "8.0", "brew"),    // orphan
		},
	}
	a, cfgPath := newImportApp(t, brew)
	short := testShortHostname()

	rootCfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("slack", "brew")),
		Groups: []*config.GroupConfig{
			{Name: "work", Tools: groupTools("slack")},
		},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatal(err)
	}
	if err := a.AddProfile("work-profile", []string{"work"}); err != nil {
		t.Fatal(err)
	}
	hostname, _ := os.Hostname()
	if err := a.SetHostname(hostname, "work-profile"); err != nil {
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
			t.Error("slack is in the profile group and should not be in the hostname group")
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
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{
			{},
			{Name: "testhost", Tools: groupTools("ripgrep")},
		},
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

func TestSyncAll_DryRunDoesNotWriteClaims(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	brew := &installTracker{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, brew)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{Groups: []*config.GroupConfig{{}}}); err != nil {
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
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{Groups: []*config.GroupConfig{{}}}); err != nil {
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

func TestSync_ProfileActive_NoOrphansSkipsHostnameGroup(t *testing.T) {
	brew := &stubProvider{
		name:      "brew",
		available: true,
		installed: []provider.InstalledTool{
			installedTool("slack", "1.0", "brew"), // in profile — no orphans
		},
	}
	a, cfgPath := newImportApp(t, brew)
	short := testShortHostname()

	rootCfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("slack", "brew")),
		Groups: []*config.GroupConfig{
			{Name: "work", Tools: groupTools("slack")},
		},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatal(err)
	}
	if err := a.AddProfile("work-profile", []string{"work"}); err != nil {
		t.Fatal(err)
	}
	hostname, _ := os.Hostname()
	if err := a.SetHostname(hostname, "work-profile"); err != nil {
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

func TestSync_ProfileActive_ReturnsSatisfiedGroups(t *testing.T) {
	brew := &stubProvider{
		name:      "brew",
		available: true,
		installed: []provider.InstalledTool{},
	}
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
	upsertInstalled(t, a.DB(), "slack", "brew")

	if err := a.AddProfile("base-profile", []string{"base"}); err != nil {
		t.Fatal(err)
	}

	result, err := a.Sync(context.Background(), gosync.SyncOptions{Profile: "base-profile"})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if result.ActiveProfile != "base-profile" {
		t.Errorf("ActiveProfile = %q, want base-profile", result.ActiveProfile)
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

func TestSync_ProfileActive_HostnameGroupInjected(t *testing.T) {
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
			{Name: short, Tools: groupTools("fd")},
		},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatal(err)
	}
	if err := a.AddProfile("work-profile", []string{"work"}); err != nil {
		t.Fatal(err)
	}

	_, err := a.Sync(context.Background(), gosync.SyncOptions{Profile: "work-profile"})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	// Both slack (from profile) and fd (from hostname group injection) must be installed.
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
			{Name: short, Tools: groupTools("fd", "ripgrep")},
			{Name: "tools", Tools: groupTools("fd")},
		},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatal(err)
	}
	if err := a.AddProfile("work", []string{"base"}); err != nil {
		t.Fatal(err)
	}

	if err := a.ClaimFromMachineGroup("work", "tools"); err != nil {
		t.Fatalf("ClaimFromMachineGroup: %v", err)
	}

	// "tools" must be in the profile.
	info, _ := a.ProfileStatus()
	found := false
	for _, g := range info.Profiles["work"].Groups {
		if g == "tools" {
			found = true
		}
	}
	if !found {
		t.Errorf("profile groups = %v, want 'tools' to be present", info.Profiles["work"].Groups)
	}

	// fd must be removed from the hostname group; ripgrep must remain.
	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
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
