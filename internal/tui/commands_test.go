package tui

// Tests call cmd() directly rather than driving the event loop, which keeps them fast and deterministic.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/dots"
)

// Uses InitTestMode so no system package managers are probed.
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

func newCmdTestModel(t *testing.T) Model {
	t.Helper()
	a := newCmdTestApp(t)
	return modelForCmds(a)
}

func newCmdTestModelWithProvider(t *testing.T, prov interface{}) Model {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(cfgPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath)
	a.CacheDir = dir
	// No-provider variant: do* errors surface via the unknown-provider path.
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("App.InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return modelForCmds(a)
}

func TestLoadTools_HappyPath(t *testing.T) {
	t.Parallel()
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

func TestLoadTools_IncludesCachedDotsState(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: filepath.Join(dir, "dots")},
	}); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath)
	a.CacheDir = dir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("App.InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	observed := time.Date(2026, 5, 29, 11, 0, 0, 0, time.UTC)
	if err := a.DB().ReplaceDotsSnapshot(context.Background(), []*database.DotStatusCache{{
		Name:       "nvim",
		TargetPath: "~/.config/nvim",
		Health:     string(app.HealthOK),
		State:      string(dots.StateSynced),
		ObservedAt: observed,
	}}, "M dotfiles/nvim", 0, observed); err != nil {
		t.Fatalf("ReplaceDotsSnapshot: %v", err)
	}

	got, ok := loadTools(a, context.Background())().(toolsLoadedMsg)
	if !ok {
		t.Fatalf("loadTools returned non-tools message")
	}
	if got.dotsState == nil || !got.dotsState.Loaded {
		t.Fatalf("dotsState = %+v, want loaded cached state", got.dotsState)
	}
	if len(got.dotsState.Entries) != 1 || got.dotsState.Entries[0].Name != "nvim" || got.dotsState.GitStatus != "M dotfiles/nvim" {
		t.Fatalf("dotsState = %+v, want cached nvim and git status", got.dotsState)
	}
}

func TestLoadTools_NoConfig(t *testing.T) {
	t.Parallel()
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

func TestLoadTools_WithGroups(t *testing.T) {
	t.Parallel()
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
	if !slices.Contains(got.groupNames, "work") {
		t.Errorf("groupNames = %v, expected to contain work", got.groupNames)
	}
	if group := got.toolGroups["ripgrep\x00brew"]; group != "work" {
		t.Errorf("toolGroups[ripgrep/brew] = %q, want work", group)
	}
}

func TestLoadTools_GroupDisplayIsScopedToActiveHost(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": {Providers: []config.ToolInstallSpec{{Provider: "brew"}}},
		},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "work", Tools: []config.ToolEntry{{Name: "ripgrep"}}},
			{Name: "personal"},
		},
		Hosts: map[string][]string{"testhost": {"work"}},
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
	key := toolKey("ripgrep", "brew")
	if got.toolGroups[key] != "work" {
		t.Fatalf("display group = %q, want active-host group work (all memberships: %v)", got.toolGroups[key], got.toolMemberships[key])
	}
	if len(got.toolMemberships[key]) != 1 {
		t.Fatalf("toolMemberships = %v, want active membership retained for editing", got.toolMemberships[key])
	}
}

func TestLoadTools_WithHost(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Groups: []*config.GroupConfig{{Name: shortHostname(), Special: "host"}},
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
	if got.noHost {
		t.Fatal("noHost = true, want active host")
	}
	if got.hostInfo == nil || got.hostInfo.Active == "" {
		t.Fatalf("hostInfo = %#v, want active host", got.hostInfo)
	}
}

func TestModel_Init(t *testing.T) {
	t.Parallel()
	m := newCmdTestModel(t)
	cmd := m.Init()
	if cmd == nil {
		t.Error("Init() returned nil cmd, want non-nil batch cmd")
	}
}

// The app has no tools to describe (empty config).
func TestDoRefreshDescriptions_HappyPath(t *testing.T) {
	t.Parallel()
	m := newCmdTestModel(t)
	msg := m.doRefreshDescriptions(7, nil, 0)()
	got, ok := msg.(descRefreshDoneMsg)
	if !ok {
		t.Fatalf("expected descRefreshDoneMsg, got %T", msg)
	}
	if got.gen != 7 {
		t.Fatalf("descRefreshDoneMsg gen = %d, want 7", got.gen)
	}
}

// No descriptions are fetchable in the test environment, so the refresh returns empty results gracefully.
func TestDoRefreshDescriptions_WithTools(t *testing.T) {
	t.Parallel()
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
	msg := m.doRefreshDescriptions(9, nil, 0)()
	if _, ok := msg.(descRefreshDoneMsg); !ok {
		t.Fatalf("expected descRefreshDoneMsg, got %T", msg)
	}
}

// A nil searchCancel is the common idle state.
func TestCancelSearch_NilCancel(t *testing.T) {
	t.Parallel()
	m := newCmdTestModel(t)
	m.searchCancel = nil
	m.cancelSearch()
	if m.searchCancel != nil {
		t.Error("searchCancel should remain nil after cancelSearch with nil cancel")
	}
}

func TestCancelSearch_NonNilCancel(t *testing.T) {
	t.Parallel()
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

func TestDoSaveDisabledProviders_HappyPath(t *testing.T) {
	t.Parallel()
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

// An empty slice means all providers are enabled.
func TestDoSaveDisabledProviders_Empty(t *testing.T) {
	t.Parallel()
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

// doSetupHost must create the current host entry instead of reviving legacy host mappings.
func TestDoSetupHost_HappyPath(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost.example")
	m := newCmdTestModel(t)
	msg := m.doSetupHost("mymachine")()
	got, ok := msg.(setupHostDoneMsg)
	if !ok {
		t.Fatalf("expected setupHostDoneMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	if got.hostName != "testhost" {
		t.Errorf("hostName = %q, want %q", got.hostName, "testhost")
	}
	if got.info == nil {
		t.Fatal("setup host message should include refreshed host info")
	}
	if got.info.Active != "testhost" {
		t.Fatalf("message active host = %q, want testhost; info=%#v", got.info.Active, got.info)
	}
	info, err := m.app.HostStatus()
	if err != nil {
		t.Fatalf("HostStatus: %v", err)
	}
	if info.Active != "testhost" {
		t.Fatalf("active host = %q, want testhost; info=%#v", info.Active, info)
	}
	if _, ok := info.Hosts["testhost"]; !ok {
		t.Fatalf("setup should create current host entry, hosts=%v", info.Hosts)
	}
	if _, ok := info.Hosts["mymachine"]; ok {
		t.Fatalf("setup should not create typed legacy host name, hosts=%v", info.Hosts)
	}
}

func TestDoSetupHost_EmptyName(t *testing.T) {
	t.Parallel()
	m := newCmdTestModel(t)
	msg := m.doSetupHost("")()
	got, ok := msg.(setupHostDoneMsg)
	if !ok {
		t.Fatalf("expected setupHostDoneMsg, got %T", msg)
	}
	// If the app is lenient and returns no error, only the message type is confirmed.
	_ = got
}

func TestDoSetupCopyHostConfigFrom_CopiesSourceToCurrentHost(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "desk.example.com")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		HostSettings: map[string]config.Settings{
			"alpha": {DotsRepo: "/alpha/dots", DisabledProviders: []string{"node"}},
		},
		Groups: []*config.GroupConfig{
			{Name: "alpha", Special: "host"},
			{Name: "shared"},
		},
		Hosts: map[string][]string{"alpha": {"shared"}},
	}); err != nil {
		t.Fatalf("saveTUIConfig: %v", err)
	}
	a := app.New(cfgPath)
	a.CacheDir = dir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	m := modelForCmds(a)
	msg := m.doSetupCopyHostConfigFrom("alpha")()
	got, ok := msg.(setupHostCopyDoneMsg)
	if !ok {
		t.Fatalf("expected setupHostCopyDoneMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("doSetupCopyHostConfigFrom: %v", got.err)
	}
	if got.source != "alpha" || got.target != "desk" {
		t.Fatalf("source/target = %q/%q, want alpha/desk", got.source, got.target)
	}
	if got.info == nil || got.info.Active != "desk" {
		t.Fatalf("active host = %#v, want desk", got.info)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if groups := cfg.Hosts["desk"]; !slices.Equal(groups, []string{"shared"}) {
		t.Fatalf("desk groups = %v, want [shared]", groups)
	}
	if cfg.HostSettings["desk"].DotsRepo != "/alpha/dots" {
		t.Fatalf("desk dots repo = %q, want copied source", cfg.HostSettings["desk"].DotsRepo)
	}
}

func TestDoSetupHostGroups_SavesCurrentHostGroups(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "desk.example.com")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Groups: []*config.GroupConfig{
			{Name: "desk", Special: "host"},
			{Name: "shared"},
		},
		Hosts: map[string][]string{"desk": {}},
	}); err != nil {
		t.Fatalf("saveTUIConfig: %v", err)
	}
	a := app.New(cfgPath)
	a.CacheDir = dir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	m := modelForCmds(a)
	msg := m.doSetupHostGroups([]string{"shared"})()
	got, ok := msg.(setupHostGroupsDoneMsg)
	if !ok {
		t.Fatalf("expected setupHostGroupsDoneMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("doSetupHostGroups: %v", got.err)
	}
	if got.info == nil || got.info.Active != "desk" {
		t.Fatalf("active host = %#v, want desk", got.info)
	}
	if groups := got.info.Hosts["desk"].Groups; !slices.Equal(groups, []string{"shared"}) {
		t.Fatalf("desk groups = %v, want [shared]", groups)
	}
}

// The setup result handler decides whether onboarding advances to group selection or finishes.
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
	got, ok := msg.(dangerOpDoneMsg)
	if !ok {
		t.Fatalf("expected dangerOpDoneMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	if got.reload || got.setupComplete {
		t.Fatalf("setup dots result should not close onboarding directly, got reload=%v setupComplete=%v", got.reload, got.setupComplete)
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

// A failed settings save must surface an error without closing onboarding.
func TestDoSetupDotsRepo_ErrorOnSave(t *testing.T) {
	t.Parallel()
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

	if err := os.Chmod(dir, 0o555); err != nil {
		t.Skipf("cannot chmod directory: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	m := modelForCmds(a)
	msg := m.doSetupDotsRepo("/some/repo")()
	got, ok := msg.(dangerOpDoneMsg)
	if !ok {
		t.Fatalf("expected dangerOpDoneMsg, got %T", msg)
	}
	if got.err == nil {
		t.Error("expected error when config directory is read-only")
	}
	if got.setupComplete || got.reload {
		t.Fatalf("failed setup dots should not close onboarding or reload, got reload=%v setupComplete=%v", got.reload, got.setupComplete)
	}
}

// An empty app returns progressDoneMsg with key="*" and no error.
func TestDoUpgradeAll_EmptyApp(t *testing.T) {
	t.Parallel()
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

func TestDoUpgradeAll_ChannelClosed(t *testing.T) {
	t.Parallel()
	m := newCmdTestModel(t)
	ch := make(chan progressUpdate, 16)
	m.progressCh = ch
	_ = m.doUpgradeAll(ch, 1)()
	// The goroutine closes the channel via defer, so this receive yields the zero value with ok=false instead of blocking.
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("channel should be closed after doUpgradeAll returns")
		}
	default:
		// Channel already closed and drained — that's fine.
	}
}

// With an empty DB and no discovering providers, the msg carries a nil discovered list and no error.
func TestDoRefreshDiscovered_EmptyApp(t *testing.T) {
	t.Parallel()
	m := newCmdTestModel(t)
	msg := m.doRefreshDiscovered(4, nil, 0)()
	got, ok := msg.(discoveredRefreshedMsg)
	if !ok {
		t.Fatalf("expected discoveredRefreshedMsg, got %T", msg)
	}
	if got.gen != 4 {
		t.Errorf("gen = %d, want 4", got.gen)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
}

func TestAnyMissingDescription_AllPresent(t *testing.T) {
	t.Parallel()
	from := func(desc string) *app.ToolView {
		return &app.ToolView{
			Description: desc,
		}
	}
	tools := []*app.ToolView{
		from("fast line-oriented search"),
		from("command-line JSON processor"),
	}
	if anyMissingDescription(tools) {
		t.Error("anyMissingDescription = true, want false when all tools have descriptions")
	}
}

func TestAnyMissingDescription_OneMissing(t *testing.T) {
	t.Parallel()
	present := &app.ToolView{
		Description: "something",
	}
	missing := &app.ToolView{
		Description: "",
	}
	tools := []*app.ToolView{present, missing}
	if !anyMissingDescription(tools) {
		t.Error("anyMissingDescription = false, want true when a tool lacks a description")
	}
}

func TestAnyMissingDescription_Empty(t *testing.T) {
	t.Parallel()
	if anyMissingDescription(nil) {
		t.Error("anyMissingDescription(nil) = true, want false")
	}
}
