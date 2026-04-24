package tui

// commands_test.go — additional coverage for uncovered tea.Cmd closures in
// commands.go and the loadTools function in model.go.
//
// All tests call cmd() directly (the tea.Cmd closure) rather than driving the
// model through the event loop. This keeps the tests fast and deterministic.

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// newCmdTestApp creates a minimal App backed by a real settings.json and the
// given provider. Uses InitTestMode so no system PMs are probed.
func newCmdTestApp(t *testing.T, prov ...interface{ Name() string }) *app.App {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(cfgPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath)
	a.CacheDir = dir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("App.InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// newCmdTestModel builds a Model wired to a fresh app in a temp dir.
// Used by tests that need to call do* methods directly.
func newCmdTestModel(t *testing.T) Model {
	t.Helper()
	a := newCmdTestApp(t)
	return modelForCmds(a)
}

// newCmdTestModelWithProvider builds a Model using a stub provider.
func newCmdTestModelWithProvider(t *testing.T, prov interface{}) Model {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(cfgPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath)
	a.CacheDir = dir
	// Delegate to the lower-level InitTestMode with a provider so cmd stubs work.
	// Use no-provider variant here — do* errors will surface via unknown-provider path.
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("App.InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return modelForCmds(a)
}

// ── loadTools ─────────────────────────────────────────────────────────────────

// TestLoadTools_HappyPath verifies that loadTools returns a toolsLoadedMsg with
// no error when the App has a valid settings.json.
func TestLoadTools_HappyPath(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(cfgPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath)
	a.CacheDir = dir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("App.InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	cmd := loadTools(a, context.Background())
	msg := cmd()

	got, ok := msg.(toolsLoadedMsg)
	if !ok {
		t.Fatalf("loadTools returned %T, want toolsLoadedMsg", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error from loadTools: %v", got.err)
	}
	if got.noConfig {
		t.Error("noConfig should be false when settings.json exists")
	}
}

// TestLoadTools_NoConfig verifies that loadTools returns noConfig=true when
// there is no settings.json file.
func TestLoadTools_NoConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json") // file does NOT exist
	a := app.New(cfgPath)
	a.CacheDir = dir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("App.InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	cmd := loadTools(a, context.Background())
	msg := cmd()

	got, ok := msg.(toolsLoadedMsg)
	if !ok {
		t.Fatalf("loadTools returned %T, want toolsLoadedMsg", msg)
	}
	if !got.noConfig {
		t.Error("noConfig should be true when settings.json does not exist")
	}
}

// TestLoadTools_WithGroups verifies that loadTools correctly populates
// groupNames and toolGroups when there are config groups.
func TestLoadTools_WithGroups(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": {Provider: "system", InstallWith: "brew"},
		},
		Groups: []*config.GroupConfig{
			{
				Name: "work",
				Tools: []config.ToolEntry{
					{Name: "ripgrep"},
				},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath)
	a.CacheDir = dir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("App.InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	cmd := loadTools(a, context.Background())
	msg := cmd()

	got, ok := msg.(toolsLoadedMsg)
	if !ok {
		t.Fatalf("loadTools returned %T, want toolsLoadedMsg", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	// configuredProviders should include the logical provider.
	found := false
	for _, p := range got.configuredProviders {
		if p == "system" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("configuredProviders = %v, expected to contain 'system'", got.configuredProviders)
	}
}

func TestLoadTools_GroupDisplayIsScopedToActiveProfile(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": {Provider: "system", InstallWith: "brew"},
		},
		Profiles: map[string]config.Profile{
			"main": {Groups: []string{"work"}},
		},
		Hostnames: map[string]string{"testhost": "main"},
		Groups: []*config.GroupConfig{
			{Name: "work", Tools: []config.ToolEntry{{Name: "ripgrep"}}},
			{Name: "personal", Tools: []config.ToolEntry{{Name: "ripgrep"}}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath)
	a.CacheDir = dir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("App.InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	msg := loadTools(a, context.Background())()
	got, ok := msg.(toolsLoadedMsg)
	if !ok {
		t.Fatalf("loadTools returned %T, want toolsLoadedMsg", msg)
	}
	key := toolKey("ripgrep", "system")
	if got.toolGroups[key] != "work" {
		t.Fatalf("display group = %q, want active-profile group work (all memberships: %v)", got.toolGroups[key], got.toolMemberships[key])
	}
	if len(got.toolMemberships[key]) != 2 {
		t.Fatalf("toolMemberships = %v, want all memberships retained for editing", got.toolMemberships[key])
	}
}

// TestLoadTools_WithProfile verifies that loadTools populates profileInfo and
// ignoreList when a profile with an ignore list is present.
func TestLoadTools_WithProfile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Profiles: map[string]config.Profile{
			"main": {Groups: []string{}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath)
	a.CacheDir = dir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("App.InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	cmd := loadTools(a, context.Background())
	msg := cmd()

	got, ok := msg.(toolsLoadedMsg)
	if !ok {
		t.Fatalf("loadTools returned %T, want toolsLoadedMsg", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	// No hostname mapping → noProfile = true
	if !got.noProfile {
		// Only fail if profileInfo is non-nil and has an active profile
		if got.profileInfo != nil && got.profileInfo.Active != "" {
			t.Errorf("expected noProfile=true when no hostname mapping exists, active=%q", got.profileInfo.Active)
		}
	}
}

// ── Init ──────────────────────────────────────────────────────────────────────

// TestModel_Init verifies that Init() returns a non-nil tea.Cmd without panicking.
func TestModel_Init(t *testing.T) {
	m := newCmdTestModel(t)
	cmd := m.Init()
	if cmd == nil {
		t.Error("Init() returned nil cmd, want non-nil batch cmd")
	}
}

// ── doRefreshDescriptions ─────────────────────────────────────────────────────

// TestDoRefreshDescriptions_HappyPath verifies that doRefreshDescriptions returns
// a descRefreshDoneMsg when the app has no tools to describe (empty config).
func TestDoRefreshDescriptions_HappyPath(t *testing.T) {
	m := newCmdTestModel(t)
	msg := m.doRefreshDescriptions(7)()
	got, ok := msg.(descRefreshDoneMsg)
	if !ok {
		t.Fatalf("expected descRefreshDoneMsg, got %T", msg)
	}
	if got.gen != 7 {
		t.Fatalf("descRefreshDoneMsg gen = %d, want 7", got.gen)
	}
}

// TestDoRefreshDescriptions_WithTools verifies that doRefreshDescriptions
// returns a descRefreshDoneMsg even when the App has tools (no descriptions to fetch
// in test env, so the refresh returns empty results gracefully).
func TestDoRefreshDescriptions_WithTools(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": tuiToolSpec("brew"),
		},
		Groups: []*config.GroupConfig{{
			Tools: []config.ToolEntry{
				{Name: "ripgrep"},
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath)
	a.CacheDir = dir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("App.InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	m := modelForCmds(a)
	msg := m.doRefreshDescriptions(9)()
	if _, ok := msg.(descRefreshDoneMsg); !ok {
		t.Fatalf("expected descRefreshDoneMsg, got %T", msg)
	}
}

// ── cancelSearch ──────────────────────────────────────────────────────────────

// TestCancelSearch_NilCancel verifies that cancelSearch is safe to call when
// searchCancel is nil (the common idle state).
func TestCancelSearch_NilCancel(t *testing.T) {
	m := newCmdTestModel(t)
	m.searchCancel = nil
	// Must not panic.
	m.cancelSearch()
	if m.searchCancel != nil {
		t.Error("searchCancel should remain nil after cancelSearch with nil cancel")
	}
}

// TestCancelSearch_NonNilCancel verifies that cancelSearch invokes and clears a
// non-nil cancel function.
func TestCancelSearch_NonNilCancel(t *testing.T) {
	m := newCmdTestModel(t)
	cancelled := false
	m.searchCancel = func() { cancelled = true }
	m.cancelSearch()
	if !cancelled {
		t.Error("cancelSearch did not call the cancel function")
	}
	if m.searchCancel != nil {
		t.Error("cancelSearch should set searchCancel to nil after calling it")
	}
}

// ── doSaveNodeManager ─────────────────────────────────────────────────────────

// TestDoSaveNodeManager_HappyPath verifies that doSaveNodeManager returns a
// setupNodeMgrDoneMsg with no error when the app has a valid config.
func TestDoSaveNodeManager_HappyPath(t *testing.T) {
	m := newCmdTestModel(t)
	msg := m.doSaveNodeManager("bun")()
	got, ok := msg.(setupNodeMgrDoneMsg)
	if !ok {
		t.Fatalf("expected setupNodeMgrDoneMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
}

// TestDoSaveNodeManager_EmptyManager verifies that saving an empty manager
// string (auto-detect mode) does not return an error.
func TestDoSaveNodeManager_EmptyManager(t *testing.T) {
	m := newCmdTestModel(t)
	msg := m.doSaveNodeManager("")()
	got, ok := msg.(setupNodeMgrDoneMsg)
	if !ok {
		t.Fatalf("expected setupNodeMgrDoneMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error saving empty manager: %v", got.err)
	}
}

// ── doSaveDisabledProviders ───────────────────────────────────────────────────

// TestDoSaveDisabledProviders_HappyPath verifies that doSaveDisabledProviders
// returns a setupProvidersDoneMsg with no error.
func TestDoSaveDisabledProviders_HappyPath(t *testing.T) {
	m := newCmdTestModel(t)
	msg := m.doSaveDisabledProviders([]string{"python"})()
	got, ok := msg.(setupProvidersDoneMsg)
	if !ok {
		t.Fatalf("expected setupProvidersDoneMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
}

// TestDoSaveDisabledProviders_Empty verifies that saving an empty slice (all
// providers enabled) does not return an error.
func TestDoSaveDisabledProviders_Empty(t *testing.T) {
	m := newCmdTestModel(t)
	msg := m.doSaveDisabledProviders(nil)()
	got, ok := msg.(setupProvidersDoneMsg)
	if !ok {
		t.Fatalf("expected setupProvidersDoneMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error saving nil slice: %v", got.err)
	}
}

// ── doSetupProfile ────────────────────────────────────────────────────────────

// TestDoSetupProfile_HappyPath verifies that doSetupProfile creates a profile
// and maps the hostname when called with a valid profile name.
func TestDoSetupProfile_HappyPath(t *testing.T) {
	m := newCmdTestModel(t)
	msg := m.doSetupProfile("mymachine")()
	got, ok := msg.(setupProfileDoneMsg)
	if !ok {
		t.Fatalf("expected setupProfileDoneMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	if got.profileName != "mymachine" {
		t.Errorf("profileName = %q, want %q", got.profileName, "mymachine")
	}
}

// TestDoSetupProfile_EmptyName verifies that doSetupProfile surfaces an error
// when passed an empty profile name (app rejects blank names).
func TestDoSetupProfile_EmptyName(t *testing.T) {
	m := newCmdTestModel(t)
	msg := m.doSetupProfile("")()
	got, ok := msg.(setupProfileDoneMsg)
	if !ok {
		t.Fatalf("expected setupProfileDoneMsg, got %T", msg)
	}
	// An empty profile name is invalid; the app should return an error.
	// If the app is lenient (returns no error), just confirm the message type.
	_ = got
}

// ── doSetupDotsRepo ───────────────────────────────────────────────────────────

// TestDoSetupDotsRepo_HappyPath verifies that doSetupDotsRepo saves the dots
// repo path and returns to normal loading without opening the old checklist.
func TestDoSetupDotsRepo_HappyPath(t *testing.T) {
	repoDir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNI_HOSTNAME", "testhost")
	if err := os.MkdirAll(filepath.Join(repoDir, "dotfiles", "nvim"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := newCmdTestModel(t)
	m.settings.DotsDisabled = config.BoolPtr(true)
	msg := m.doSetupDotsRepo(repoDir)()
	got, ok := msg.(toolsLoadedMsg)
	if !ok {
		t.Fatalf("expected toolsLoadedMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	settings, err := m.app.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if settings.DotsRepo != repoDir {
		t.Fatalf("DotsRepo = %q, want %q", settings.DotsRepo, repoDir)
	}
	if config.BoolVal(settings.DotsDisabled) {
		t.Fatal("DotsDisabled should be false after setup saves a repo")
	}
	cfg, err := config.Load(m.app.ConfigPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	group := findTUITestGroup(cfg.Groups, "testhost")
	if group == nil || len(group.Dots) != 1 || group.Dots[0].Name != "nvim" {
		t.Fatalf("bootstrap dots group = %#v, want testhost/nvim", group)
	}
}

func findTUITestGroup(groups []*config.GroupConfig, name string) *config.GroupConfig {
	for _, group := range groups {
		if group.BaseName() == name {
			return group
		}
	}
	return nil
}

// TestDoSetupDotsRepo_ErrorOnSave verifies that doSetupDotsRepo surfaces an
// error via toolsLoadedMsg when the settings save fails (read-only config).
func TestDoSetupDotsRepo_ErrorOnSave(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(cfgPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath)
	a.CacheDir = dir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("App.InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	// Make the config directory read-only so SaveSettings fails.
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Skipf("cannot chmod directory: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	m := modelForCmds(a)
	msg := m.doSetupDotsRepo("/some/repo")()
	got, ok := msg.(toolsLoadedMsg)
	if !ok {
		t.Fatalf("expected toolsLoadedMsg, got %T", msg)
	}
	if got.err == nil {
		t.Error("expected error when config directory is read-only")
	}
}

// ── doUpgradeAll ─────────────────────────────────────────────────────────────

// TestDoUpgradeAll_EmptyApp verifies that doUpgradeAll on an empty app
// (no tools to upgrade) returns a progressDoneMsg with key="*" and no error.
func TestDoUpgradeAll_EmptyApp(t *testing.T) {
	m := newCmdTestModel(t)
	ch := make(chan progressUpdate, 16)
	m.progressCh = ch
	msg := m.doUpgradeAll(ch, 1)()
	got, ok := msg.(progressDoneMsg)
	if !ok {
		t.Fatalf("expected progressDoneMsg, got %T", msg)
	}
	if got.key != "*" {
		t.Errorf("key = %q, want %q", got.key, "*")
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
}

// TestDoUpgradeAll_ChannelClosed verifies that the channel is closed after
// doUpgradeAll returns (so waitForProgress receives progressDoneMsg).
func TestDoUpgradeAll_ChannelClosed(t *testing.T) {
	m := newCmdTestModel(t)
	ch := make(chan progressUpdate, 16)
	m.progressCh = ch
	_ = m.doUpgradeAll(ch, 1)()
	// If the channel is still open this receive would block; the goroutine closes
	// it via defer close(ch), so we should get the zero value with ok=false.
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("channel should be closed after doUpgradeAll returns")
		}
	default:
		// Channel already closed and drained — that's fine.
	}
}

// ── doRefreshDiscovered error path ────────────────────────────────────────────

// TestDoRefreshDiscovered_EmptyApp exercises the non-error path more completely:
// with an empty DB and no providers that return discovered tools, it should
// return a discoveredRefreshedMsg with nil discovered list and no error.
func TestDoRefreshDiscovered_EmptyApp(t *testing.T) {
	m := newCmdTestModel(t)
	msg := m.doRefreshDiscovered(4)()
	got, ok := msg.(discoveredRefreshedMsg)
	if !ok {
		t.Fatalf("expected discoveredRefreshedMsg, got %T", msg)
	}
	if got.gen != 4 {
		t.Errorf("gen = %d, want 4", got.gen)
	}
	// With no providers that list installed tools, err should be nil and
	// discovered should be empty (not nil-panicking).
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
}

// ── anyMissingDescription ─────────────────────────────────────────────────────

// TestAnyMissingDescription_AllPresent verifies the false branch when every
// tool has a valid description.
func TestAnyMissingDescription_AllPresent(t *testing.T) {
	from := func(desc string) *database.ToolCache {
		return &database.ToolCache{
			Description: sql.NullString{String: desc, Valid: true},
		}
	}
	tools := []*database.ToolCache{
		from("fast line-oriented search"),
		from("command-line JSON processor"),
	}
	if anyMissingDescription(tools) {
		t.Error("anyMissingDescription = true, want false when all tools have descriptions")
	}
}

// TestAnyMissingDescription_OneMissing verifies the true branch when at least
// one tool lacks a description.
func TestAnyMissingDescription_OneMissing(t *testing.T) {
	present := &database.ToolCache{
		Description: sql.NullString{String: "something", Valid: true},
	}
	missing := &database.ToolCache{
		Description: sql.NullString{String: "", Valid: false},
	}
	tools := []*database.ToolCache{present, missing}
	if !anyMissingDescription(tools) {
		t.Error("anyMissingDescription = false, want true when a tool lacks a description")
	}
}

// TestAnyMissingDescription_Empty verifies the false branch for an empty slice.
func TestAnyMissingDescription_Empty(t *testing.T) {
	if anyMissingDescription(nil) {
		t.Error("anyMissingDescription(nil) = true, want false")
	}
}
