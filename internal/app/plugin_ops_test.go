package app_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

type stubPluginAdapter struct {
	id                      string
	available               bool
	installErr              error
	removeErr               error
	updateErr               error
	addMarketErr            error
	listErr                 error
	updateMarketplacesErr   error
	listedPlugins           []app.InstalledPlugin
	listedMarkets           []app.InstalledMarketplace
	installedPlugin         []config.Plugin
	removedNames            []string
	updatedNames            []string
	updatedIdentities       []string
	addedMarkets            []config.Marketplace
	listCalls               int
	updateMarketplacesCalls int
}

func (s *stubPluginAdapter) ID() string      { return s.id }
func (s *stubPluginAdapter) Available() bool { return s.available }
func (s *stubPluginAdapter) ListPlugins(_ context.Context) ([]app.InstalledPlugin, error) {
	s.listCalls++
	if s.listErr != nil {
		return nil, s.listErr
	}
	return append([]app.InstalledPlugin(nil), s.listedPlugins...), nil
}
func (s *stubPluginAdapter) InstallPlugin(_ context.Context, p config.Plugin) error {
	s.installedPlugin = append(s.installedPlugin, p)
	return s.installErr
}
func (s *stubPluginAdapter) RemovePlugin(_ context.Context, p config.Plugin) error {
	s.removedNames = append(s.removedNames, p.Name)
	return s.removeErr
}
func (s *stubPluginAdapter) UpdatePlugin(_ context.Context, name, marketplace string) error {
	s.updatedNames = append(s.updatedNames, name)
	s.updatedIdentities = append(s.updatedIdentities, name+"@"+marketplace)
	return s.updateErr
}
func (s *stubPluginAdapter) ListMarketplaces(_ context.Context) ([]app.InstalledMarketplace, error) {
	return append([]app.InstalledMarketplace(nil), s.listedMarkets...), nil
}
func (s *stubPluginAdapter) AddMarketplace(_ context.Context, m config.Marketplace) error {
	s.addedMarkets = append(s.addedMarkets, m)
	return s.addMarketErr
}
func (s *stubPluginAdapter) UpdateMarketplaces(_ context.Context) error {
	s.updateMarketplacesCalls++
	return s.updateMarketplacesErr
}

func newPluginTestApp(t *testing.T, agents config.AgentsConfig, opts ...func(*app.App)) *app.App {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	root := config.RootConfig{Version: config.CurrentVersion, Agents: agents}
	if err := config.Save(cfgPath, &root); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath, opts...)
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func loadPluginTestConfig(t *testing.T, a *app.App) *config.RootConfig {
	t.Helper()
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestRestorePlugins_AddsMarketplaceBeforePlugin(t *testing.T) {
	stub := &stubPluginAdapter{id: "claude-code", available: true}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "lkshrk/agent-marketplace", Agents: []string{"claude-code"}}},
		Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman", Agents: []string{"claude-code"}}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	res, err := a.RestorePlugins(context.Background(), app.RestorePluginOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.addedMarkets) != 1 || stub.addedMarkets[0].Name != "caveman" {
		t.Fatalf("expected marketplace to be added before plugin, got %v", stub.addedMarkets)
	}
	if len(stub.installedPlugin) != 1 || stub.installedPlugin[0].Name != "caveman" {
		t.Fatalf("expected plugin install, got %v", stub.installedPlugin)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}
}

// TestRestorePlugins_RefreshesMarketplacesOnEveryAvailableAdapter pins the
// marketplace-before-plugin ordering fix for the restore/sync path: every
// available adapter's marketplace snapshots are refreshed once per restore,
// before that adapter's plugin list/install loop runs, so a stale clone never
// precedes an install that reads from it.
func TestRestorePlugins_RefreshesMarketplacesOnEveryAvailableAdapter(t *testing.T) {
	stub := &stubPluginAdapter{id: "claude-code", available: true}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "lkshrk/agent-marketplace", Agents: []string{"claude-code"}}},
		Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman", Agents: []string{"claude-code"}}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	res, err := a.RestorePlugins(context.Background(), app.RestorePluginOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if stub.updateMarketplacesCalls != 1 {
		t.Fatalf("expected UpdateMarketplaces to be called once, got %d", stub.updateMarketplacesCalls)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}
}

// TestRestorePlugins_MarketplaceRefreshFailureIsWarningNotFatal verifies a
// marketplace refresh failure surfaces as a warning and does not block the
// plugin install loop that follows.
func TestRestorePlugins_MarketplaceRefreshFailureIsWarningNotFatal(t *testing.T) {
	stub := &stubPluginAdapter{id: "claude-code", available: true, updateMarketplacesErr: errBoom}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "lkshrk/agent-marketplace", Agents: []string{"claude-code"}}},
		Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman", Agents: []string{"claude-code"}}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	res, err := a.RestorePlugins(context.Background(), app.RestorePluginOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 {
		t.Fatalf("expected 1 warning for the refresh failure, got %v", res.Warnings)
	}
	if len(stub.installedPlugin) != 1 {
		t.Fatalf("expected plugin install to still proceed despite the refresh failure, got %v", stub.installedPlugin)
	}
}

// TestRestorePlugins_DryRun_DoesNotRefreshMarketplaces verifies a dry-run
// restore (which never mutates adapter state) also skips the marketplace
// refresh call, mirroring how it skips ensureMarketplace/InstallPlugin.
func TestRestorePlugins_DryRun_DoesNotRefreshMarketplaces(t *testing.T) {
	stub := &stubPluginAdapter{id: "claude-code", available: true}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "lkshrk/agent-marketplace", Agents: []string{"claude-code"}}},
		Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman", Agents: []string{"claude-code"}}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	_, err := a.RestorePlugins(context.Background(), app.RestorePluginOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if stub.updateMarketplacesCalls != 0 {
		t.Fatalf("expected no UpdateMarketplaces call on a dry run, got %d", stub.updateMarketplacesCalls)
	}
}

func TestRestorePlugins_SkipsNonTargetedAgent(t *testing.T) {
	stub := &stubPluginAdapter{id: "codex", available: true}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
		Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman", Agents: []string{"claude-code"}}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	if _, err := a.RestorePlugins(context.Background(), app.RestorePluginOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(stub.installedPlugin) != 0 {
		t.Fatal("codex should be skipped when not in plugin's Agents list")
	}
}

func TestRestorePlugins_DryRun_ReportsWouldInstall(t *testing.T) {
	stub := &stubPluginAdapter{id: "claude-code", available: true}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
		Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman"}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	res, err := a.RestorePlugins(context.Background(), app.RestorePluginOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.installedPlugin) != 0 || len(stub.addedMarkets) != 0 {
		t.Fatal("dry run must not call the adapter")
	}
	if len(res.WouldInstall) != 1 || res.WouldInstall[0] != "claude-code/caveman" {
		t.Fatalf("expected would-install entry, got %v", res.WouldInstall)
	}
}

func TestRestorePlugins_PerPluginErrorIsNonFatal(t *testing.T) {
	stub := &stubPluginAdapter{id: "claude-code", available: true, installErr: errBoom}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
		Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman"}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	res, err := a.RestorePlugins(context.Background(), app.RestorePluginOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("expected 1 collected error, got %v", res.Errors)
	}
}

func TestRestorePlugins_SkipsAlreadyInstalled(t *testing.T) {
	stub := &stubPluginAdapter{
		id:            "claude-code",
		available:     true,
		listedPlugins: []app.InstalledPlugin{{Name: "caveman", Marketplace: "caveman"}},
	}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
		Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman", Agents: []string{"claude-code"}}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	res, err := a.RestorePlugins(context.Background(), app.RestorePluginOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.installedPlugin) != 0 {
		t.Fatalf("expected no install call for already-present plugin, got %v", stub.installedPlugin)
	}
	if len(stub.addedMarkets) != 0 {
		t.Fatalf("expected no marketplace add for already-present plugin, got %v", stub.addedMarkets)
	}
	if len(res.AlreadyInstalled) != 1 || res.AlreadyInstalled[0] != "claude-code/caveman" {
		t.Fatalf("expected already-installed entry, got %v", res.AlreadyInstalled)
	}
	if len(res.Installed) != 0 {
		t.Fatalf("expected no installed entries, got %v", res.Installed)
	}
}

func TestRestorePlugins_ListPluginsErrorWarnsAndAttemptsInstall(t *testing.T) {
	stub := &stubPluginAdapter{id: "claude-code", available: true, listErr: errBoom}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
		Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman", Agents: []string{"claude-code"}}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	res, err := a.RestorePlugins(context.Background(), app.RestorePluginOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 {
		t.Fatalf("expected a warning for the list failure, got %v", res.Warnings)
	}
	if len(stub.installedPlugin) != 1 || stub.installedPlugin[0].Name != "caveman" {
		t.Fatalf("expected install attempted despite list failure, got %v", stub.installedPlugin)
	}
	if len(res.Installed) != 1 {
		t.Fatalf("expected 1 installed entry, got %v", res.Installed)
	}
}

func TestAddPlugin_PersistsToManifestRegardlessOfAdapterOutcome(t *testing.T) {
	stub := &stubPluginAdapter{id: "claude-code", available: true, installErr: errBoom}
	agents := config.AgentsConfig{Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}}}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	res, err := a.AddPlugin(context.Background(), config.Plugin{Name: "caveman", Marketplace: "caveman", Agents: []string{"claude-code"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("expected adapter error surfaced, got %v", res.Errors)
	}
	cfg := loadPluginTestConfig(t, a)
	if len(cfg.Agents.Plugins) != 1 || cfg.Agents.Plugins[0].Name != "caveman" {
		t.Fatalf("manifest write must persist despite adapter failure, got %+v", cfg.Agents.Plugins)
	}
}

func TestAddPlugin_RejectsUnknownMarketplace(t *testing.T) {
	a := newPluginTestApp(t, config.AgentsConfig{}, app.WithPluginAdapters(nil))
	if _, err := a.AddPlugin(context.Background(), config.Plugin{Name: "caveman", Marketplace: "ghost"}); err == nil {
		t.Fatal("expected error for unknown marketplace ref")
	}
}

func TestPluginByName_ReturnsFullEntry(t *testing.T) {
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "m", Source: "a/b"}},
		Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "m", Agents: []string{"claude-code"}}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters(nil))
	got, ok, err := a.PluginByName("caveman")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected ok=true for a manifest entry")
	}
	if got.Marketplace != "m" || len(got.Agents) != 1 {
		t.Fatalf("expected full entry, got %+v", got)
	}
}

func TestPluginByName_NotFound(t *testing.T) {
	a := newPluginTestApp(t, config.AgentsConfig{}, app.WithPluginAdapters(nil))
	_, ok, err := a.PluginByName("missing")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected ok=false for an absent manifest entry")
	}
}

func TestRemovePlugin_RemovesFromAdapterAndManifestButKeepsMarketplace(t *testing.T) {
	stub := &stubPluginAdapter{id: "claude-code", available: true, listedPlugins: []app.InstalledPlugin{{Name: "caveman", Marketplace: "caveman"}}}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
		Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman", Agents: []string{"claude-code"}}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	if _, err := a.RemovePlugin(context.Background(), "caveman"); err != nil {
		t.Fatal(err)
	}
	if len(stub.removedNames) != 1 {
		t.Fatalf("expected adapter RemovePlugin call, got %v", stub.removedNames)
	}
	cfg := loadPluginTestConfig(t, a)
	if len(cfg.Agents.Plugins) != 0 {
		t.Fatal("expected plugin removed from manifest")
	}
	if len(cfg.Agents.Marketplaces) != 1 {
		t.Fatal("marketplace must remain in manifest after plugin removal")
	}
}

func TestRemovePlugin_ListErrorStillUninstallsAndDeletesManifest(t *testing.T) {
	stub := &stubPluginAdapter{id: "codex", available: true, listErr: errors.New("codex plugin list: boom")}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "ponytail", Source: "a/b"}},
		Plugins:      []config.Plugin{{Name: "ponytail", Marketplace: "ponytail", Agents: []string{"codex"}}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	res, err := a.RemovePlugin(context.Background(), "ponytail")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("expected no adapter errors, got %v", res.Errors)
	}
	if len(stub.removedNames) != 1 {
		t.Fatalf("expected attempted adapter uninstall, got %v", stub.removedNames)
	}
	if len(res.SkippedUnavailable) != 0 {
		t.Fatalf("expected no skip when uninstall succeeds, got %v", res.SkippedUnavailable)
	}
	cfg := loadPluginTestConfig(t, a)
	if len(cfg.Agents.Plugins) != 0 {
		t.Fatal("expected plugin removed from manifest")
	}
}

func TestRemovePlugin_ListErrorDowngradesUninstallFailureToWarning(t *testing.T) {
	stub := &stubPluginAdapter{
		id:        "codex",
		available: true,
		listErr:   errors.New("codex plugin list: boom"),
		removeErr: errors.New("codex plugin remove: not installed"),
	}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "ponytail", Source: "a/b"}},
		Plugins:      []config.Plugin{{Name: "ponytail", Marketplace: "ponytail", Agents: []string{"codex"}}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	res, err := a.RemovePlugin(context.Background(), "ponytail")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("expected uninstall failure downgraded to warning, got errors %v", res.Errors)
	}
	if len(res.Warnings) != 1 || res.Warnings[0].AgentID != "codex" {
		t.Fatalf("expected warning recorded, got %v", res.Warnings)
	}
	if !strings.Contains(res.Warnings[0].Err.Error(), "not installed") {
		t.Fatalf("warning must carry the uninstall error, got %v", res.Warnings[0].Err)
	}
	if len(res.SkippedUnavailable) != 0 {
		t.Fatalf("expected no unavailable skip, got %v", res.SkippedUnavailable)
	}
	cfg := loadPluginTestConfig(t, a)
	if len(cfg.Agents.Plugins) != 0 {
		t.Fatal("expected plugin removed from manifest")
	}
}

func TestRemovePlugin_RejectsUnmanaged(t *testing.T) {
	a := newPluginTestApp(t, config.AgentsConfig{}, app.WithPluginAdapters(nil))
	if _, err := a.RemovePlugin(context.Background(), "ghost"); err == nil {
		t.Fatal("expected error removing unmanaged plugin")
	}
}

func TestRemoveMarketplace_DeletesManifestEntryOnly(t *testing.T) {
	stub := &stubPluginAdapter{id: "claude-code", available: true}
	agents := config.AgentsConfig{Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}}}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	if err := a.RemoveMarketplace("caveman"); err != nil {
		t.Fatal(err)
	}
	if len(stub.removedNames) != 0 {
		t.Fatal("marketplace removal must never call any adapter")
	}
	cfg := loadPluginTestConfig(t, a)
	if len(cfg.Agents.Marketplaces) != 0 {
		t.Fatal("expected marketplace removed from manifest")
	}
}

func TestSetPluginAgents_WideningInstallsOnNewlySelectedAdapter(t *testing.T) {
	claude := &stubPluginAdapter{id: "claude-code", available: true, listedPlugins: []app.InstalledPlugin{{Name: "caveman", Marketplace: "caveman"}}}
	codex := &stubPluginAdapter{id: "codex", available: true}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
		Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman", Agents: []string{"claude-code"}}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{claude, codex}))
	if _, err := a.SetPluginAgents(context.Background(), "caveman", []string{"claude-code", "codex"}); err != nil {
		t.Fatal(err)
	}
	if len(codex.installedPlugin) != 1 {
		t.Fatalf("expected codex to install newly-selected plugin, got %v", codex.installedPlugin)
	}
	if len(claude.installedPlugin) != 0 {
		t.Fatal("claude-code was already targeted; must not reinstall")
	}
}

func TestSetPluginAgents_NarrowingRemovesFromDeselectedAdapterOnly(t *testing.T) {
	claude := &stubPluginAdapter{id: "claude-code", available: true}
	codex := &stubPluginAdapter{id: "codex", available: true}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
		Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman", Agents: []string{"claude-code", "codex"}}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{claude, codex}))
	if _, err := a.SetPluginAgents(context.Background(), "caveman", []string{"claude-code"}); err != nil {
		t.Fatal(err)
	}
	if len(codex.removedNames) != 1 || codex.removedNames[0] != "caveman" {
		t.Fatalf("expected codex to remove deselected plugin, got %v", codex.removedNames)
	}
	if len(claude.removedNames) != 0 {
		t.Fatal("claude-code is still targeted; must not be removed")
	}
	cfg := loadPluginTestConfig(t, a)
	if len(cfg.Agents.Plugins) != 1 || len(cfg.Agents.Plugins[0].Agents) != 1 || cfg.Agents.Plugins[0].Agents[0] != "claude-code" {
		t.Fatalf("expected manifest Agents narrowed to [claude-code], got %+v", cfg.Agents.Plugins)
	}
}

func TestSetPluginAgents_ReconcilesMissingSelectedAdapter(t *testing.T) {
	claude := &stubPluginAdapter{id: "claude-code", available: true}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
		Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman", Agents: []string{"claude-code"}}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{claude}))
	if _, err := a.SetPluginAgents(context.Background(), "caveman", []string{"claude-code"}); err != nil {
		t.Fatal(err)
	}
	if len(claude.installedPlugin) != 1 {
		t.Fatalf("expected missing selected plugin to be installed, got %d calls", len(claude.installedPlugin))
	}
}

func TestSetPluginAgents_TargetedUnavailableAdapterSkips(t *testing.T) {
	claude := &stubPluginAdapter{id: "claude-code", available: false}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
		Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman", Agents: []string{}}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{claude}))
	res, err := a.SetPluginAgents(context.Background(), "caveman", []string{"claude-code"})
	if err != nil {
		t.Fatalf("SetPluginAgents must not fail wholly on a per-adapter skip: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unavailable adapter must not produce errors, got %v", res.Errors)
	}
	if len(res.SkippedUnavailable) != 1 || res.SkippedUnavailable[0] != "claude-code/caveman" {
		t.Fatalf("expected SkippedUnavailable [claude-code/caveman], got %v", res.SkippedUnavailable)
	}
}

func TestSetPluginAgents_RejectsUnmanaged(t *testing.T) {
	stub := &stubPluginAdapter{id: "claude-code", available: true}
	a := newPluginTestApp(t, config.AgentsConfig{}, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	if _, err := a.SetPluginAgents(context.Background(), "ghost", []string{"claude-code"}); err == nil {
		t.Fatal("expected error for unmanaged plugin")
	}
	if len(stub.installedPlugin) != 0 || len(stub.removedNames) != 0 {
		t.Fatalf("expected zero adapter calls, got installed=%v removed=%v", stub.installedPlugin, stub.removedNames)
	}
}

func TestImportPlugins_ReturnsUnmanagedAndKnownMarketplace(t *testing.T) {
	stub := &stubPluginAdapter{
		id:            "claude-code",
		available:     true,
		listedPlugins: []app.InstalledPlugin{{Name: "hand-added", Marketplace: "caveman", Version: "1.0.0"}},
		listedMarkets: []app.InstalledMarketplace{{Name: "caveman", Source: "a/b"}},
	}
	agents := config.AgentsConfig{Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}}}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	diff, err := a.ImportPlugins(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Unmanaged["claude-code"]) != 1 || diff.Unmanaged["claude-code"][0].Name != "hand-added" {
		t.Fatalf("expected hand-added to be unmanaged, got %v", diff.Unmanaged)
	}
}

var errBoom = errors.New("boom")

func TestUpdatePlugin_PassesNameToAdapter(t *testing.T) {
	stub := &stubPluginAdapter{id: "claude-code", available: true}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
		Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman"}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	res, err := a.UpdatePlugin(context.Background(), "caveman")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}
	if len(stub.updatedNames) != 1 || stub.updatedNames[0] != "caveman" {
		t.Fatalf("expected UpdatePlugin called with %q, got %v", "caveman", stub.updatedNames)
	}
	if len(stub.updatedIdentities) != 1 || stub.updatedIdentities[0] != "caveman@caveman" {
		t.Fatalf("expected UpdatePlugin called with marketplace, got %v", stub.updatedIdentities)
	}
}

func TestUpdatePlugin_GuardedByPluginsEnabled(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	root := config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{PluginsDisabled: config.BoolPtr(true)},
		Agents: config.AgentsConfig{
			Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
			Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman"}},
		},
	}
	if err := config.Save(cfgPath, &root); err != nil {
		t.Fatal(err)
	}
	stub := &stubPluginAdapter{id: "claude-code", available: true}
	a := app.New(cfgPath, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer a.Close() //nolint:errcheck

	_, err := a.UpdatePlugin(context.Background(), "caveman")
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected disabled error, got %v", err)
	}
	if len(stub.updatedNames) != 0 {
		t.Fatalf("adapter must not be called when plugins are disabled, got %v", stub.updatedNames)
	}
}

func TestUpdatePlugin_NotFoundInManifest(t *testing.T) {
	stub := &stubPluginAdapter{id: "claude-code", available: true}
	a := newPluginTestApp(t, config.AgentsConfig{}, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	if _, err := a.UpdatePlugin(context.Background(), "absent"); err == nil {
		t.Fatal("expected not-found error")
	}
}

// TestUpdatePlugin_RefreshesMarketplacesBeforeUpdating pins the marketplace-
// before-plugin ordering fix: a plugin update installs from the marketplace's
// local clone, so the clone must be refreshed first or the update can silently
// install a stale version even though the CLI call itself succeeds.
func TestUpdatePlugin_RefreshesMarketplacesBeforeUpdating(t *testing.T) {
	stub := &stubPluginAdapter{id: "claude-code", available: true}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
		Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman"}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	res, err := a.UpdatePlugin(context.Background(), "caveman")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}
	if stub.updateMarketplacesCalls != 1 {
		t.Fatalf("expected UpdateMarketplaces to be called once, got %d", stub.updateMarketplacesCalls)
	}
	if len(stub.updatedNames) != 1 {
		t.Fatalf("expected UpdatePlugin to still be called, got %v", stub.updatedNames)
	}
}

// TestUpdatePlugin_MarketplaceRefreshFailureIsNonFatal verifies a marketplace
// refresh failure is collected as an error but does not block the plugin
// update attempt itself (best-effort refresh, tolerant like every other
// per-adapter step in this file).
func TestUpdatePlugin_MarketplaceRefreshFailureIsNonFatal(t *testing.T) {
	stub := &stubPluginAdapter{id: "claude-code", available: true, updateMarketplacesErr: errBoom}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
		Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman"}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	res, err := a.UpdatePlugin(context.Background(), "caveman")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("expected 1 collected marketplace-refresh error, got %v", res.Errors)
	}
	if len(stub.updatedNames) != 1 {
		t.Fatalf("expected UpdatePlugin to still run despite the refresh failure, got %v", stub.updatedNames)
	}
}

func TestUpdatePlugin_MultiAdapterPartialFailureTolerant(t *testing.T) {
	ok := &stubPluginAdapter{id: "claude-code", available: true}
	fails := &stubPluginAdapter{id: "codex", available: true, updateErr: errBoom}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
		Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman"}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{ok, fails}))
	res, err := a.UpdatePlugin(context.Background(), "caveman")
	if err != nil {
		t.Fatal(err)
	}
	if len(ok.updatedNames) != 1 {
		t.Fatalf("expected the working adapter to still be called, got %v", ok.updatedNames)
	}
	if len(res.Errors) != 1 || res.Errors[0].AgentID != "codex" {
		t.Fatalf("expected 1 collected error from codex, got %v", res.Errors)
	}
}

// TestUpdatePlugins_RefreshesMarketplacesOncePerAdapter pins the bulk-update
// dedup: N outdated plugins on one adapter must trigger exactly one
// marketplace refresh, not N — the refresh is an update-all CLI call, so
// repeating it per plugin multiplies the same slow network fetch.
func TestUpdatePlugins_RefreshesMarketplacesOncePerAdapter(t *testing.T) {
	stub := &stubPluginAdapter{id: "claude-code", available: true}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
		Plugins: []config.Plugin{
			{Name: "one", Marketplace: "caveman"},
			{Name: "two", Marketplace: "caveman"},
			{Name: "three", Marketplace: "caveman"},
		},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	var seen []string
	res, err := a.UpdatePlugins(context.Background(), []string{"one", "two", "three"}, func(name string) {
		seen = append(seen, name)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}
	if stub.updateMarketplacesCalls != 1 {
		t.Fatalf("expected exactly one UpdateMarketplaces call, got %d", stub.updateMarketplacesCalls)
	}
	if len(stub.updatedNames) != 3 {
		t.Fatalf("expected 3 plugin updates, got %v", stub.updatedNames)
	}
	if len(seen) != 3 || seen[0] != "one" || seen[2] != "three" {
		t.Fatalf("progress callback order wrong: %v", seen)
	}
}

// TestUpdateMarketplaces_RefreshesEveryAvailableAdapter covers the
// update-all path when no plugin is outdated: UpdatePlugins is never called
// in that case, so marketplaces need their own standalone refresh to avoid
// going stale.
func TestUpdateMarketplaces_RefreshesEveryAvailableAdapter(t *testing.T) {
	claude := &stubPluginAdapter{id: "claude-code", available: true}
	codex := &stubPluginAdapter{id: "codex", available: false}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{claude, codex}))

	res, err := a.UpdateMarketplaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}
	if claude.updateMarketplacesCalls != 1 {
		t.Fatalf("expected available adapter to be refreshed once, got %d", claude.updateMarketplacesCalls)
	}
	if codex.updateMarketplacesCalls != 0 {
		t.Fatalf("expected unavailable adapter to be skipped, got %d calls", codex.updateMarketplacesCalls)
	}
}

func TestUpdateMarketplaces_CollectsPerAdapterErrors(t *testing.T) {
	failing := &stubPluginAdapter{id: "claude-code", available: true, updateMarketplacesErr: errors.New("boom")}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{failing}))

	res, err := a.UpdateMarketplaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0].Error(), "boom") {
		t.Fatalf("expected one error containing 'boom', got %v", res.Errors)
	}
}

func TestRemoveMarketplace_BlockedByReferencingPlugins(t *testing.T) {
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
		Plugins:      []config.Plugin{{Name: "keeper", Marketplace: "caveman"}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters(nil))
	err := a.RemoveMarketplace("caveman")
	if err == nil || !strings.Contains(err.Error(), "keeper") {
		t.Fatalf("expected error naming the blocking plugin, got %v", err)
	}
	remaining, mErr := a.Marketplaces()
	if mErr != nil {
		t.Fatal(mErr)
	}
	if len(remaining) != 1 {
		t.Fatalf("marketplace must remain declared, got %v", remaining)
	}
}

// TestUpdatePluginsPreRefreshed_SkipsMarketplaceRefresh pins the pre-refreshed
// contract: callers that just ran UpdateMarketplaces themselves (the TUI's
// update-all) must not pay a second refresh, while the updates still run.
func TestUpdatePluginsPreRefreshed_SkipsMarketplaceRefresh(t *testing.T) {
	stub := &stubPluginAdapter{id: "claude-code", available: true}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
		Plugins: []config.Plugin{
			{Name: "one", Marketplace: "caveman"},
			{Name: "two", Marketplace: "caveman"},
		},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	var seen []string
	res, err := a.UpdatePluginsPreRefreshed(context.Background(), []string{"one", "two"}, func(name string) {
		seen = append(seen, name)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}
	if stub.updateMarketplacesCalls != 0 {
		t.Fatalf("expected zero UpdateMarketplaces calls, got %d", stub.updateMarketplacesCalls)
	}
	if len(stub.updatedNames) != 2 {
		t.Fatalf("expected both plugins updated, got %v", stub.updatedNames)
	}
	if len(seen) != 2 || seen[0] != "one" || seen[1] != "two" {
		t.Fatalf("progress callback order wrong: %v", seen)
	}
}

func TestUpdatePluginsPreRefreshed_UnknownPluginStillErrors(t *testing.T) {
	stub := &stubPluginAdapter{id: "claude-code", available: true}
	a := newPluginTestApp(t, config.AgentsConfig{}, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	if _, err := a.UpdatePluginsPreRefreshed(context.Background(), []string{"absent"}, nil); err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestAddMarketplace_UpsertsManifestAndAddsToAdapters(t *testing.T) {
	stub := &stubPluginAdapter{id: "claude-code", available: true}
	a := newPluginTestApp(t, config.AgentsConfig{}, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	res, err := a.AddMarketplace(context.Background(), config.Marketplace{Name: "caveman", Source: "a/b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 || len(res.SkippedUnavailable) != 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if len(stub.addedMarkets) != 1 || stub.addedMarkets[0].Name != "caveman" {
		t.Fatalf("adapter AddMarketplace calls = %v, want caveman added", stub.addedMarkets)
	}
	markets, err := a.Marketplaces()
	if err != nil {
		t.Fatal(err)
	}
	if len(markets) != 1 || markets[0].Source != "a/b" {
		t.Fatalf("manifest marketplaces = %v, want caveman a/b", markets)
	}

	// Upsert: same name with a new source replaces, never duplicates.
	if _, err := a.AddMarketplace(context.Background(), config.Marketplace{Name: "caveman", Source: "a/c"}); err != nil {
		t.Fatal(err)
	}
	markets, err = a.Marketplaces()
	if err != nil {
		t.Fatal(err)
	}
	if len(markets) != 1 || markets[0].Source != "a/c" {
		t.Fatalf("manifest after upsert = %v, want single caveman a/c", markets)
	}
}

func TestAddMarketplace_SkipsAlreadyListedAndUnavailableAdapters(t *testing.T) {
	listed := &stubPluginAdapter{
		id: "claude-code", available: true,
		listedMarkets: []app.InstalledMarketplace{{Name: "caveman", Source: "a/b"}},
	}
	offline := &stubPluginAdapter{id: "codex", available: false}
	a := newPluginTestApp(t, config.AgentsConfig{}, app.WithPluginAdapters([]app.PluginAdapter{listed, offline}))
	res, err := a.AddMarketplace(context.Background(), config.Marketplace{Name: "caveman", Source: "a/b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.addedMarkets) != 0 {
		t.Fatalf("already-listed adapter must be skipped, got adds: %v", listed.addedMarkets)
	}
	if len(res.SkippedUnavailable) != 1 || res.SkippedUnavailable[0] != "codex/caveman" {
		t.Fatalf("SkippedUnavailable = %v, want codex/caveman", res.SkippedUnavailable)
	}
}

func TestFindUndeclaredMarketplace(t *testing.T) {
	stub := &stubPluginAdapter{
		id: "claude-code", available: true,
		listedMarkets: []app.InstalledMarketplace{
			{Name: "no-source", Source: ""},
			{Name: "caveman", Source: "a/b"},
		},
	}
	a := newPluginTestApp(t, config.AgentsConfig{}, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	ctx := context.Background()

	source, ok, err := a.FindUndeclaredMarketplace(ctx, "caveman", nil)
	if err != nil || !ok || source != "a/b" {
		t.Fatalf("FindUndeclaredMarketplace(caveman) = %q,%v,%v; want a/b,true,nil", source, ok, err)
	}
	if _, ok, _ := a.FindUndeclaredMarketplace(ctx, "no-source", nil); ok {
		t.Fatal("a listed marketplace without a re-addable source must not be returned")
	}
	if _, ok, _ := a.FindUndeclaredMarketplace(ctx, "absent", nil); ok {
		t.Fatal("an unlisted marketplace must not be found")
	}
}

// TestAdoptUnmanagedPlugins pins the bulk adopt: unmanaged plugins whose
// marketplace is declared get manifest entries scoped to the reporting agent;
// ones with undeclared marketplaces are counted as skipped, not adopted.
func TestAdoptUnmanagedPlugins(t *testing.T) {
	stub := &stubPluginAdapter{
		id: "claude-code", available: true,
		listedPlugins: []app.InstalledPlugin{
			{Name: "declared-market", Marketplace: "caveman"},
			{Name: "orphan-market", Marketplace: "undeclared"},
		},
	}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	adopted, skipped, err := a.AdoptUnmanagedPlugins(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if adopted != 1 || skipped != 1 {
		t.Fatalf("adopted=%d skipped=%d, want 1/1", adopted, skipped)
	}
	cfg, err := a.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.Plugins) != 1 || cfg.Agents.Plugins[0].Name != "declared-market" {
		t.Fatalf("manifest plugins = %v, want only declared-market adopted", cfg.Agents.Plugins)
	}
	if got := cfg.Agents.Plugins[0].Agents; len(got) != 1 || got[0] != "claude-code" {
		t.Fatalf("adopted plugin agents = %v, want [claude-code]", got)
	}
}
