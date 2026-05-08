//go:build integration

package integration_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider/brew"
	"github.com/lkshrk/omni/internal/provider/pip"
	gosync "github.com/lkshrk/omni/internal/sync"
)

// ─── brew JSON helpers ─────────────────────────────────────────────────────

func brewInfoJSON(pkgs map[string]string) string {
	out := `{"formulae":[`
	first := true
	for name, ver := range pkgs {
		if !first {
			out += ","
		}
		out += `{"full_name":"` + name + `","installed":[{"version":"` + ver + `","installed_on_request":true}]}`
		first = false
	}
	return out + `],"casks":[]}`
}

const brewInfoEmpty = `{"formulae":[],"casks":[]}`
const brewOutdatedEmpty = `{"formulae":[],"casks":[]}`

// ─── pip list helper ───────────────────────────────────────────────────────

func pipListOutput(pkgs map[string]string) string {
	out := "["
	first := true
	for name, ver := range pkgs {
		if !first {
			out += ","
		}
		out += `{"name":"` + name + `","version":"` + ver + `"}`
		first = false
	}
	return out + "]"
}

// ─── Syncer-level integration tests ───────────────────────────────────────

func TestSync_InstallsMissingBrewTool(t *testing.T) {
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "brew --version", Response: executor.MockCall{Stdout: "Homebrew 4.0.0"}},
		executor.MatchRule{Pattern: "brew info --json=v2 --installed", Response: executor.MockCall{Stdout: brewInfoEmpty}},
		executor.MatchRule{Pattern: "brew outdated --json=v2", Response: executor.MockCall{Stdout: brewOutdatedEmpty}},
		executor.MatchRule{Pattern: "brew install ripgrep", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "brew list --versions ripgrep", Response: executor.MockCall{Stdout: "ripgrep 14.1.0\n"}},
	)
	ta := newTestStackWithMock(t, mock)
	cfg := simpleConfig("ripgrep", "brew")

	result, err := ta.Syncer.Sync(context.Background(), cfg, gosync.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(result.Installed()) != 1 {
		t.Errorf("Installed = %d, want 1", len(result.Installed()))
	}
	mock.AssertCalled(t, "brew install ripgrep")
}

func TestSync_SkipsAlreadyInstalledTool(t *testing.T) {
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "brew --version", Response: executor.MockCall{Stdout: "Homebrew 4.0.0"}},
		executor.MatchRule{Pattern: "brew info --json=v2 --installed", Response: executor.MockCall{
			Stdout: brewInfoJSON(map[string]string{"ripgrep": "14.1.0"}),
		}},
		executor.MatchRule{Pattern: "brew outdated --json=v2", Response: executor.MockCall{Stdout: brewOutdatedEmpty}},
	)
	ta := newTestStackWithMock(t, mock)
	cfg := simpleConfig("ripgrep", "brew")

	result, err := ta.Syncer.Sync(context.Background(), cfg, gosync.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(result.Skipped()) != 1 {
		t.Errorf("Skipped = %d, want 1", len(result.Skipped()))
	}
	mock.MustHaveCalledN(t, "brew install", 0)
}

func TestSync_ProviderUnavailable(t *testing.T) {
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "brew --version", Response: executor.MockCall{Err: errNotExist}},
	)
	ta := newTestStackWithMock(t, mock)
	cfg := simpleConfig("ripgrep", "brew")

	result, err := ta.Syncer.Sync(context.Background(), cfg, gosync.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(result.Ops) != 1 || result.Ops[0].Kind != gosync.OpProviderUnavailable {
		t.Errorf("expected OpProviderUnavailable, got %+v", result.Ops)
	}
	mock.MustHaveCalledN(t, "brew install", 0)
}

func TestSync_MultipleBrewTools(t *testing.T) {
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "brew --version", Response: executor.MockCall{Stdout: "Homebrew 4.0.0"}},
		executor.MatchRule{Pattern: "brew info --json=v2 --installed", Response: executor.MockCall{Stdout: brewInfoEmpty}},
		executor.MatchRule{Pattern: "brew outdated --json=v2", Response: executor.MockCall{Stdout: brewOutdatedEmpty}},
		executor.MatchRule{Pattern: "brew install ripgrep", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "brew install git", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "brew list --versions ripgrep", Response: executor.MockCall{Stdout: "ripgrep 14.1.0\n"}},
		executor.MatchRule{Pattern: "brew list --versions git", Response: executor.MockCall{Stdout: "git 2.43.0\n"}},
	)
	ta := newTestStackWithMock(t, mock)
	cfg := simpleConfig("ripgrep", "brew", "git", "brew")

	result, err := ta.Syncer.Sync(context.Background(), cfg, gosync.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(result.Installed()) != 2 {
		t.Errorf("Installed = %d, want 2", len(result.Installed()))
	}
}

func TestSync_DryRun_DoesNotInstall(t *testing.T) {
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "brew --version", Response: executor.MockCall{Stdout: "Homebrew 4.0.0"}},
		executor.MatchRule{Pattern: "brew info --json=v2 --installed", Response: executor.MockCall{Stdout: brewInfoEmpty}},
		executor.MatchRule{Pattern: "brew outdated --json=v2", Response: executor.MockCall{Stdout: brewOutdatedEmpty}},
	)
	ta := newTestStackWithMock(t, mock)
	cfg := simpleConfig("ripgrep", "brew")

	if _, err := ta.Syncer.Sync(context.Background(), cfg, gosync.SyncOptions{DryRun: true}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	mock.MustHaveCalledN(t, "brew install", 0)
}

func TestSync_Progress_ReceivesMessages(t *testing.T) {
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "brew --version", Response: executor.MockCall{Stdout: "Homebrew 4.0.0"}},
		executor.MatchRule{Pattern: "brew info --json=v2 --installed", Response: executor.MockCall{Stdout: brewInfoEmpty}},
		executor.MatchRule{Pattern: "brew outdated --json=v2", Response: executor.MockCall{Stdout: brewOutdatedEmpty}},
		executor.MatchRule{Pattern: "brew install ripgrep", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "brew list --versions ripgrep", Response: executor.MockCall{Stdout: "ripgrep 14.1.0\n"}},
	)
	ta := newTestStackWithMock(t, mock)
	cfg := simpleConfig("ripgrep", "brew")

	var messages []string
	if _, err := ta.Syncer.Sync(context.Background(), cfg, gosync.SyncOptions{
		Progress: func(s string) { messages = append(messages, s) },
	}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(messages) == 0 {
		t.Error("expected progress messages, got none")
	}
}

func TestSync_MarksToolInstalledInDB(t *testing.T) {
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "brew --version", Response: executor.MockCall{Stdout: "Homebrew 4.0.0"}},
		executor.MatchRule{Pattern: "brew info --json=v2 --installed", Response: executor.MockCall{Stdout: brewInfoEmpty}},
		executor.MatchRule{Pattern: "brew outdated --json=v2", Response: executor.MockCall{Stdout: brewOutdatedEmpty}},
		executor.MatchRule{Pattern: "brew install ripgrep", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "brew list --versions ripgrep", Response: executor.MockCall{Stdout: "ripgrep 14.1.0\n"}},
	)
	ta := newTestStackWithMock(t, mock)
	cfg := simpleConfig("ripgrep", "brew")

	if _, err := ta.Syncer.Sync(context.Background(), cfg, gosync.SyncOptions{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	entry, err := ta.DB.Get(context.Background(), "ripgrep", "brew", "ripgrep")
	if err != nil {
		t.Fatalf("DB.Get: %v", err)
	}
	if !entry.Installed {
		t.Error("expected Installed = true after sync")
	}
	if entry.Version.String != "14.1.0" {
		t.Errorf("Version = %q, want 14.1.0", entry.Version.String)
	}
}

func TestSync_InstallFailure_RecordedAsError(t *testing.T) {
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "brew --version", Response: executor.MockCall{Stdout: "Homebrew 4.0.0"}},
		executor.MatchRule{Pattern: "brew info --json=v2 --installed", Response: executor.MockCall{Stdout: brewInfoEmpty}},
		executor.MatchRule{Pattern: "brew outdated --json=v2", Response: executor.MockCall{Stdout: brewOutdatedEmpty}},
		executor.MatchRule{Pattern: "brew install ripgrep", Response: executor.MockCall{Err: errNotExist, Stderr: "formula not found"}},
	)
	ta := newTestStackWithMock(t, mock)
	cfg := simpleConfig("ripgrep", "brew")

	result, err := ta.Syncer.Sync(context.Background(), cfg, gosync.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(result.Errors()) != 1 {
		t.Errorf("Errors = %d, want 1", len(result.Errors()))
	}
}

func TestSync_PipTool(t *testing.T) {
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "pip3 --version", Response: executor.MockCall{Stdout: "pip 23.0"}},
		executor.MatchRule{Pattern: "pip3 list --outdated --format=json", Response: executor.MockCall{Stdout: "[]"}},
		executor.MatchRule{Pattern: "pip3 list", Response: executor.MockCall{Stdout: pipListOutput(nil)}},
		executor.MatchRule{Pattern: "pip3 install black", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "pip3 show black", Response: executor.MockCall{Stdout: "Name: black\nVersion: 24.3.0\n"}},
	)
	ta := newTestStackWithMock(t, mock)
	cfg := simpleConfig("black", "pip")

	result, err := ta.Syncer.Sync(context.Background(), cfg, gosync.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(result.Installed()) != 1 {
		t.Errorf("Installed = %d, want 1", len(result.Installed()))
	}
	mock.AssertCalled(t, "pip3 install black")
}

func TestSync_DoesNotRefreshOutdatedState(t *testing.T) {
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "brew --version", Response: executor.MockCall{Stdout: "Homebrew 4.0.0"}},
		executor.MatchRule{Pattern: "brew info --json=v2 --installed", Response: executor.MockCall{
			Stdout: brewInfoJSON(map[string]string{"ripgrep": "14.0.0"}),
		}},
	)
	ta := newTestStackWithMock(t, mock)
	cfg := simpleConfig("ripgrep", "brew")

	if err := ta.DB.Upsert(context.Background(), &database.ToolCache{
		Name:      "ripgrep",
		Provider:  "brew",
		Package:   "ripgrep",
		Installed: true,
	}); err != nil {
		t.Fatalf("prepopulate DB: %v", err)
	}
	if err := ta.DB.UpdateOutdated(context.Background(), "ripgrep", "brew", "ripgrep", true, "14.1.0"); err != nil {
		t.Fatalf("UpdateOutdated: %v", err)
	}

	if _, err := ta.Syncer.Sync(context.Background(), cfg, gosync.SyncOptions{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	entry, err := ta.DB.Get(context.Background(), "ripgrep", "brew", "ripgrep")
	if err != nil {
		t.Fatalf("DB.Get: %v", err)
	}
	if !entry.Outdated || entry.LatestVersion.String != "14.1.0" {
		t.Fatalf("outdated/latest = %v/%q, want preserved true/14.1.0", entry.Outdated, entry.LatestVersion.String)
	}
	mock.MustHaveCalledN(t, "brew outdated", 0)
}

// ─── App-level integration tests ──────────────────────────────────────────

func newAppWithBrew(t *testing.T, mock executor.Executor) (*app.App, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	a := app.New(cfgPath)
	if err := a.InitTestMode(context.Background(), brew.New(mock)); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a, cfgPath
}

func newAppWithPip(t *testing.T, mock executor.Executor) (*app.App, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	a := app.New(cfgPath)
	if err := a.InitTestMode(context.Background(), pip.New(mock)); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a, cfgPath
}

func rootWithTestHostGroup(tools map[string]config.ToolSpec, entries ...config.ToolEntry) *config.RootConfig {
	return &config.RootConfig{
		Tools: tools,
		Hosts: map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{{
			Name:    "testhost",
			Special: "host",
			Tools:   entries,
		}},
	}
}

func TestApp_Install_CallsProviderAndUpdatesDB(t *testing.T) {
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "brew --version", Response: executor.MockCall{Stdout: "Homebrew 4.0.0"}},
		executor.MatchRule{Pattern: "brew install ripgrep", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "brew list --versions ripgrep", Response: executor.MockCall{Stdout: "ripgrep 14.1.0\n"}},
	)
	a, _ := newAppWithBrew(t, mock)

	if err := a.Install(context.Background(), "ripgrep", "brew"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	mock.AssertCalled(t, "brew install ripgrep")

	entry, err := a.DB().Get(context.Background(), "ripgrep", "brew", "ripgrep")
	if err != nil {
		t.Fatalf("DB.Get: %v", err)
	}
	if !entry.Installed {
		t.Error("expected Installed = true after Install")
	}
	if entry.Version.String != "14.1.0" {
		t.Errorf("Version = %q, want 14.1.0", entry.Version.String)
	}
}

func TestApp_Install_UnknownProvider(t *testing.T) {
	a, _ := newAppWithBrew(t, executor.NewMatchMock())

	if err := a.Install(context.Background(), "ripgrep", "cargo"); err == nil {
		t.Error("expected error for unknown provider")
	}
}

func TestApp_Install_UnavailableProvider(t *testing.T) {
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "brew --version", Response: executor.MockCall{Err: errNotExist}},
	)
	a, _ := newAppWithBrew(t, mock)

	if err := a.Install(context.Background(), "ripgrep", "brew"); err == nil {
		t.Error("expected error when provider is unavailable")
	}
}

func TestApp_Uninstall_CallsProviderAndUpdatesDB(t *testing.T) {
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "brew --version", Response: executor.MockCall{Stdout: "Homebrew 4.0.0"}},
		executor.MatchRule{Pattern: "brew install ripgrep", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "brew list --versions ripgrep", Response: executor.MockCall{Stdout: "ripgrep 14.1.0\n"}},
		executor.MatchRule{Pattern: "brew uninstall ripgrep", Response: executor.MockCall{}},
	)
	a, _ := newAppWithBrew(t, mock)
	ctx := context.Background()

	if err := a.Install(ctx, "ripgrep", "brew"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := a.Uninstall(ctx, "ripgrep", "brew"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	mock.AssertCalled(t, "brew uninstall ripgrep")

	if _, err := a.DB().Get(ctx, "ripgrep", "brew", "ripgrep"); err == nil {
		t.Fatal("expected DB row to be removed after Uninstall")
	}
}

func TestApp_Upgrade_CallsProviderAndUpdatesVersion(t *testing.T) {
	// MockExecutor (sequential) — install then upgrade call different IsInstalled responses.
	installMock := &executor.MockExecutor{Responses: []executor.MockCall{
		{Stdout: "Homebrew 4.0.0"},   // brew --version (Available)
		{},                           // brew install ripgrep
		{Stdout: "ripgrep 14.0.0\n"}, // brew list --versions ripgrep (after install)
		{},                           // brew upgrade ripgrep
		{Stdout: "ripgrep 14.1.0\n"}, // brew list --versions ripgrep (after upgrade)
	}}
	a, _ := newAppWithBrew(t, installMock)
	ctx := context.Background()

	if err := a.Install(ctx, "ripgrep", "brew"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := a.Upgrade(ctx, "ripgrep", "brew"); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	entry, err := a.DB().Get(ctx, "ripgrep", "brew", "ripgrep")
	if err != nil {
		t.Fatalf("DB.Get: %v", err)
	}
	if entry.Version.String != "14.1.0" {
		t.Errorf("Version = %q, want 14.1.0 after upgrade", entry.Version.String)
	}
}

func TestApp_UpgradeAll_OnlyUpgradesOutdatedTools(t *testing.T) {
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "brew --version", Response: executor.MockCall{Stdout: "Homebrew 4.0.0"}},
		executor.MatchRule{Pattern: "brew install ripgrep", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "brew install git", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "brew list --versions ripgrep", Response: executor.MockCall{Stdout: "ripgrep 14.0.0\n"}},
		executor.MatchRule{Pattern: "brew list --versions git", Response: executor.MockCall{Stdout: "git 2.43.0\n"}},
		executor.MatchRule{Pattern: "brew upgrade ripgrep", Response: executor.MockCall{}},
	)
	a, _ := newAppWithBrew(t, mock)
	ctx := context.Background()

	if err := a.Install(ctx, "ripgrep", "brew"); err != nil {
		t.Fatalf("Install ripgrep: %v", err)
	}
	if err := a.Install(ctx, "git", "brew"); err != nil {
		t.Fatalf("Install git: %v", err)
	}
	if err := a.DB().UpdateOutdated(ctx, "ripgrep", "brew", "ripgrep", true, "14.1.0"); err != nil {
		t.Fatalf("UpdateOutdated: %v", err)
	}

	if err := a.UpgradeAll(ctx, nil); err != nil {
		t.Fatalf("UpgradeAll: %v", err)
	}

	mock.AssertCalled(t, "brew upgrade ripgrep")
	mock.MustHaveCalledN(t, "brew upgrade git", 0)
}

func TestApp_Sync_FullRoundTrip(t *testing.T) {
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "brew --version", Response: executor.MockCall{Stdout: "Homebrew 4.0.0"}},
		executor.MatchRule{Pattern: "brew info --json=v2 --installed", Response: executor.MockCall{Stdout: brewInfoEmpty}},
		executor.MatchRule{Pattern: "brew outdated --json=v2", Response: executor.MockCall{Stdout: brewOutdatedEmpty}},
		executor.MatchRule{Pattern: "brew install ripgrep", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "brew list --versions ripgrep", Response: executor.MockCall{Stdout: "ripgrep 14.1.0\n"}},
	)
	a, cfgPath := newAppWithBrew(t, mock)

	root := rootWithTestHostGroup(
		map[string]config.ToolSpec{
			"ripgrep": {Provider: "system", Package: "ripgrep", InstallWith: "brew"},
		},
		config.ToolEntry{Name: "ripgrep"},
	)
	if err := config.Save(cfgPath, root); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	result, err := a.Sync(context.Background(), gosync.SyncOptions{Group: "testhost"})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(result.Installed()) != 1 {
		t.Errorf("Installed = %d, want 1", len(result.Installed()))
	}

	tools, err := a.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || !tools[0].Installed {
		t.Errorf("expected 1 installed tool in DB, got %+v", tools)
	}
}

func TestApp_Sync_MixedStates(t *testing.T) {
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "brew --version", Response: executor.MockCall{Stdout: "Homebrew 4.0.0"}},
		executor.MatchRule{Pattern: "brew info --json=v2 --installed", Response: executor.MockCall{
			Stdout: brewInfoJSON(map[string]string{"ripgrep": "14.1.0"}),
		}},
		executor.MatchRule{Pattern: "brew outdated --json=v2", Response: executor.MockCall{Stdout: brewOutdatedEmpty}},
		executor.MatchRule{Pattern: "brew install git", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "brew list --versions git", Response: executor.MockCall{Stdout: "git 2.43.0\n"}},
	)
	a, cfgPath := newAppWithBrew(t, mock)

	root := rootWithTestHostGroup(
		map[string]config.ToolSpec{
			"ripgrep": {Provider: "system", Package: "ripgrep", InstallWith: "brew"},
			"git":     {Provider: "system", Package: "git", InstallWith: "brew"},
		},
		config.ToolEntry{Name: "ripgrep"},
		config.ToolEntry{Name: "git"},
	)
	if err := config.Save(cfgPath, root); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	result, err := a.Sync(context.Background(), gosync.SyncOptions{Group: "testhost"})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(result.Installed()) != 1 {
		t.Errorf("Installed = %d, want 1 (only git)", len(result.Installed()))
	}
	if len(result.Skipped()) != 1 {
		t.Errorf("Skipped = %d, want 1 (ripgrep)", len(result.Skipped()))
	}
}

func TestApp_ListTools_ReflectsDBState(t *testing.T) {
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "brew --version", Response: executor.MockCall{Stdout: "Homebrew 4.0.0"}},
		executor.MatchRule{Pattern: "brew install ripgrep", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "brew list --versions ripgrep", Response: executor.MockCall{Stdout: "ripgrep 14.1.0\n"}},
	)
	a, _ := newAppWithBrew(t, mock)
	ctx := context.Background()

	tools, err := a.ListTools(ctx, "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 0 {
		t.Errorf("expected empty list before install, got %d", len(tools))
	}

	if err := a.Install(ctx, "ripgrep", "brew"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	tools, err = a.ListTools(ctx, "")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || !tools[0].Installed {
		t.Errorf("expected 1 installed tool, got %+v", tools)
	}
}

func TestApp_Import_DiscoversInstalledTools(t *testing.T) {
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "brew --version", Response: executor.MockCall{Stdout: "Homebrew 4.0.0"}},
		executor.MatchRule{Pattern: "brew info --json=v2 --installed", Response: executor.MockCall{
			Stdout: `{"formulae":[{"full_name":"ripgrep","installed":[{"version":"14.1.0","installed_on_request":true}]}],"casks":[]}`,
		}},
	)
	a, cfgPath := newAppWithBrew(t, mock)

	root := rootWithTestHostGroup(
		map[string]config.ToolSpec{
			"ripgrep": {Provider: "system", Package: "ripgrep", InstallWith: "brew"},
		},
		config.ToolEntry{Name: "ripgrep"},
	)
	if err := config.Save(cfgPath, root); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	result, err := a.Import(context.Background(), app.ImportOptions{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if len(result.Added)+len(result.Skipped) == 0 {
		t.Error("expected import to find at least one tool")
	}
}

func TestApp_Install_PipTool(t *testing.T) {
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "pip3 --version", Response: executor.MockCall{Stdout: "pip 23.0"}},
		executor.MatchRule{Pattern: "pip3 install black", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "pip3 show black", Response: executor.MockCall{Stdout: "Name: black\nVersion: 24.3.0\n"}},
	)
	a, _ := newAppWithPip(t, mock)

	if err := a.Install(context.Background(), "black", "pip"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	entry, err := a.DB().Get(context.Background(), "black", "pip", "black")
	if err != nil {
		t.Fatalf("DB.Get: %v", err)
	}
	if entry.Version.String != "24.3.0" {
		t.Errorf("Version = %q, want 24.3.0", entry.Version.String)
	}
}

func TestApp_Uninstall_PipTool(t *testing.T) {
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "pip3 --version", Response: executor.MockCall{Stdout: "pip 23.0"}},
		executor.MatchRule{Pattern: "pip3 install black", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "pip3 show black", Response: executor.MockCall{Stdout: "Name: black\nVersion: 24.3.0\n"}},
		executor.MatchRule{Pattern: "pip3 uninstall -y black", Response: executor.MockCall{}},
	)
	a, _ := newAppWithPip(t, mock)

	if err := a.Install(context.Background(), "black", "pip"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := a.Uninstall(context.Background(), "black", "pip"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	mock.AssertCalled(t, "pip3 uninstall -y black")
}

func TestApp_Sync_BrewCask(t *testing.T) {
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "brew --version", Response: executor.MockCall{Stdout: "Homebrew 4.0.0"}},
		executor.MatchRule{Pattern: "brew info --json=v2 --installed", Response: executor.MockCall{Stdout: brewInfoEmpty}},
		executor.MatchRule{Pattern: "brew outdated --json=v2", Response: executor.MockCall{Stdout: brewOutdatedEmpty}},
		executor.MatchRule{Pattern: "brew install warp", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "brew list --versions --cask warp", Response: executor.MockCall{Stdout: "warp 0.2025.04\n"}},
	)
	a, cfgPath := newAppWithBrew(t, mock)

	root := rootWithTestHostGroup(
		map[string]config.ToolSpec{
			"warp": {Provider: "system", Package: "warp", InstallWith: "brew"},
		},
		config.ToolEntry{Name: "warp"},
	)
	if err := config.Save(cfgPath, root); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	result, err := a.Sync(context.Background(), gosync.SyncOptions{Group: "testhost"})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(result.Installed()) != 1 {
		t.Errorf("Installed = %d, want 1", len(result.Installed()))
	}
	mock.AssertCalled(t, "brew install warp")
}
