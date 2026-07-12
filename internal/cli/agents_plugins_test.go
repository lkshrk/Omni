package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func TestAgentsPluginsSubcmdsRegistered(t *testing.T) {
	state := &rootState{}
	cmd := newAgentsPluginsCmd(state)
	want := map[string]bool{"list": false, "add": false, "remove": false, "restore": false, "import": false, "marketplace": false}
	for _, sub := range cmd.Commands() {
		want[sub.Name()] = true
	}
	for name, found := range want {
		if !found {
			t.Errorf("subcommand %q not registered", name)
		}
	}
}

func TestAgentsPluginsMarketplaceSubcmdsRegistered(t *testing.T) {
	state := &rootState{}
	cmd := newAgentsPluginsMarketplaceCmd(state)
	want := map[string]bool{"list": false, "add": false, "remove": false}
	for _, sub := range cmd.Commands() {
		want[sub.Name()] = true
	}
	for name, found := range want {
		if !found {
			t.Errorf("marketplace subcommand %q not registered", name)
		}
	}
}

type pluginStubAdapter struct {
	id            string
	listedPlugins []app.InstalledPlugin
	listedMarkets []app.InstalledMarketplace
	installed     []config.Plugin
	addedMarkets  []config.Marketplace
	removedNames  []string
}

func (s *pluginStubAdapter) ID() string      { return s.id }
func (s *pluginStubAdapter) Available() bool { return true }
func (s *pluginStubAdapter) ListPlugins(_ context.Context) ([]app.InstalledPlugin, error) {
	return append([]app.InstalledPlugin(nil), s.listedPlugins...), nil
}
func (s *pluginStubAdapter) InstallPlugin(_ context.Context, p config.Plugin) error {
	s.installed = append(s.installed, p)
	return nil
}
func (s *pluginStubAdapter) RemovePlugin(_ context.Context, p config.Plugin) error {
	s.removedNames = append(s.removedNames, p.Name)
	return nil
}
func (s *pluginStubAdapter) UpdatePlugin(_ context.Context, _, _ string) error {
	return nil
}
func (s *pluginStubAdapter) ListMarketplaces(_ context.Context) ([]app.InstalledMarketplace, error) {
	return append([]app.InstalledMarketplace(nil), s.listedMarkets...), nil
}
func (s *pluginStubAdapter) AddMarketplace(_ context.Context, m config.Marketplace) error {
	s.addedMarkets = append(s.addedMarkets, m)
	return nil
}
func (s *pluginStubAdapter) UpdateMarketplaces(_ context.Context) error {
	return nil
}

func newPluginCLITestApp(t *testing.T, adapters []app.PluginAdapter, agents config.AgentsConfig) *app.App {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	root := config.RootConfig{Version: config.CurrentVersion, Agents: agents}
	if err := config.Save(cfgPath, &root); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath, app.WithPluginAdapters(adapters))
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func TestAgentsPluginsAdd_RejectsUnknownMarketplace(t *testing.T) {
	stub := &pluginStubAdapter{id: "claude-code"}
	a := newPluginCLITestApp(t, []app.PluginAdapter{stub}, config.AgentsConfig{})
	state := &rootState{app: a}
	cmd := newAgentsPluginsAddCmd(state)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--name", "caveman", "--marketplace", "ghost"})
	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatal("expected error for unknown marketplace")
	}
}

func TestAgentsPluginsAdd_InstallsAndPersists(t *testing.T) {
	stub := &pluginStubAdapter{id: "claude-code"}
	agents := config.AgentsConfig{Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}}}
	a := newPluginCLITestApp(t, []app.PluginAdapter{stub}, agents)
	state := &rootState{app: a}
	cmd := newAgentsPluginsAddCmd(state)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--name", "caveman", "--marketplace", "caveman", "--agents", "claude-code"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("added caveman")) {
		t.Fatalf("expected confirmation, got: %s", out.String())
	}
	if len(stub.installed) != 1 {
		t.Fatalf("expected adapter install call, got %v", stub.installed)
	}
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.Plugins) != 1 || cfg.Agents.Plugins[0].Name != "caveman" {
		t.Fatalf("expected manifest entry, got %+v", cfg.Agents.Plugins)
	}
}

func TestAgentsPluginsRemove_UninstallsAndDeletesManifest(t *testing.T) {
	stub := &pluginStubAdapter{id: "claude-code", listedPlugins: []app.InstalledPlugin{{Name: "caveman", Marketplace: "caveman"}}}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
		Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman"}},
	}
	a := newPluginCLITestApp(t, []app.PluginAdapter{stub}, agents)
	state := &rootState{app: a}
	cmd := newAgentsPluginsRemoveCmd(state)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"caveman"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("removed caveman")) {
		t.Fatalf("expected confirmation, got: %s", out.String())
	}
	if len(stub.removedNames) != 1 || stub.removedNames[0] != "caveman" {
		t.Fatalf("expected adapter remove call, got %v", stub.removedNames)
	}
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.Plugins) != 0 {
		t.Fatalf("expected manifest entry removed, got %+v", cfg.Agents.Plugins)
	}
}

func TestAgentsPluginsRestore_DryRunPrintsWouldInstallOnly(t *testing.T) {
	stub := &pluginStubAdapter{id: "claude-code"}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
		Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman"}},
	}
	a := newPluginCLITestApp(t, []app.PluginAdapter{stub}, agents)
	state := &rootState{app: a}
	cmd := newAgentsPluginsRestoreCmd(state)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--dry-run"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("would install:")) {
		t.Fatalf("expected would-install line, got: %s", out.String())
	}
	if bytes.Contains(out.Bytes(), []byte("installed:")) {
		t.Fatalf("dry-run must not print installed lines, got: %s", out.String())
	}
	if len(stub.installed) != 0 {
		t.Fatalf("dry-run must not call adapter install, got %v", stub.installed)
	}
}

func TestAgentsPluginsRestore_InstallsWithoutDryRun(t *testing.T) {
	stub := &pluginStubAdapter{id: "claude-code"}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
		Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman"}},
	}
	a := newPluginCLITestApp(t, []app.PluginAdapter{stub}, agents)
	state := &rootState{app: a}
	cmd := newAgentsPluginsRestoreCmd(state)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(nil)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("installed:")) {
		t.Fatalf("expected installed line, got: %s", out.String())
	}
	if len(stub.installed) != 1 {
		t.Fatalf("expected adapter install call, got %v", stub.installed)
	}
}

func TestAgentsPluginsImport_NoArgListsUnmanaged(t *testing.T) {
	stub := &pluginStubAdapter{
		id:            "claude-code",
		listedPlugins: []app.InstalledPlugin{{Name: "hand-added", Marketplace: "caveman", Version: "9.9.9"}},
	}
	agents := config.AgentsConfig{Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}}}
	a := newPluginCLITestApp(t, []app.PluginAdapter{stub}, agents)
	state := &rootState{app: a}
	cmd := newAgentsPluginsImportCmd(state)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(nil)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("hand-added")) {
		t.Fatalf("expected unmanaged plugin listed, got: %s", out.String())
	}
}

func TestAgentsPluginsImport_WithNameAdoptsIdentityOnly(t *testing.T) {
	stub := &pluginStubAdapter{
		id:            "claude-code",
		listedPlugins: []app.InstalledPlugin{{Name: "hand-added", Marketplace: "caveman", Version: "9.9.9"}},
	}
	agents := config.AgentsConfig{Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}}}
	a := newPluginCLITestApp(t, []app.PluginAdapter{stub}, agents)
	state := &rootState{app: a}
	cmd := newAgentsPluginsImportCmd(state)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"hand-added"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range cfg.Agents.Plugins {
		if p.Name != "hand-added" {
			continue
		}
		found = true
		if p.Marketplace != "caveman" {
			t.Fatalf("expected adopted marketplace, got %+v", p)
		}
	}
	if !found {
		t.Fatalf("expected imported plugin in manifest, got %+v", cfg.Agents.Plugins)
	}
}

func TestAgentsPluginsImport_UndeclaredMarketplaceAdoptsRealSource(t *testing.T) {
	stub := &pluginStubAdapter{
		id:            "claude-code",
		listedPlugins: []app.InstalledPlugin{{Name: "hand-added", Marketplace: "ghost", Version: "1.0.0"}},
		listedMarkets: []app.InstalledMarketplace{{Name: "ghost", Source: "https://github.com/lkshrk/ghost.git"}},
	}
	a := newPluginCLITestApp(t, []app.PluginAdapter{stub}, config.AgentsConfig{})
	state := &rootState{app: a}
	cmd := newAgentsPluginsImportCmd(state)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"hand-added"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	var market *config.Marketplace
	for i := range cfg.Agents.Marketplaces {
		if cfg.Agents.Marketplaces[i].Name == "ghost" {
			market = &cfg.Agents.Marketplaces[i]
		}
	}
	if market == nil {
		t.Fatalf("expected marketplace adopted into manifest, got %+v", cfg.Agents.Marketplaces)
	}
	if market.Source != "https://github.com/lkshrk/ghost.git" {
		t.Fatalf("expected adapter-reported source, got %+v", market)
	}
	foundPlugin := false
	for _, p := range cfg.Agents.Plugins {
		if p.Name == "hand-added" && p.Marketplace == "ghost" {
			foundPlugin = true
		}
	}
	if !foundPlugin {
		t.Fatalf("expected plugin adopted into manifest, got %+v", cfg.Agents.Plugins)
	}
}

func TestAgentsPluginsImport_UndeclaredMarketplaceWithoutSourceErrors(t *testing.T) {
	stub := &pluginStubAdapter{
		id:            "claude-code",
		listedPlugins: []app.InstalledPlugin{{Name: "hand-added", Marketplace: "ghost", Version: "1.0.0"}},
		listedMarkets: []app.InstalledMarketplace{{Name: "ghost", Source: ""}},
	}
	a := newPluginCLITestApp(t, []app.PluginAdapter{stub}, config.AgentsConfig{})
	state := &rootState{app: a}
	cmd := newAgentsPluginsImportCmd(state)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"hand-added"})
	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatal("expected error when no real source is discoverable")
	}
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.Marketplaces) != 0 || len(cfg.Agents.Plugins) != 0 {
		t.Fatalf("expected manifest untouched, got marketplaces=%+v plugins=%+v", cfg.Agents.Marketplaces, cfg.Agents.Plugins)
	}
}

func TestAgentsPluginsImport_UnknownNameErrors(t *testing.T) {
	stub := &pluginStubAdapter{id: "claude-code"}
	a := newPluginCLITestApp(t, []app.PluginAdapter{stub}, config.AgentsConfig{})
	state := &rootState{app: a}
	cmd := newAgentsPluginsImportCmd(state)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"nonexistent"})
	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatal("expected error for unknown plugin name")
	}
}

func TestAgentsPluginsImport_TooManyArgsRejected(t *testing.T) {
	stub := &pluginStubAdapter{id: "claude-code"}
	a := newPluginCLITestApp(t, []app.PluginAdapter{stub}, config.AgentsConfig{})
	state := &rootState{app: a}
	cmd := newAgentsPluginsImportCmd(state)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"one", "two"})
	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatal("expected error for too many args")
	}
}

func TestAgentsPluginsMarketplaceAdd_DeclaresAndCallsAdapter(t *testing.T) {
	stub := &pluginStubAdapter{id: "claude-code"}
	a := newPluginCLITestApp(t, []app.PluginAdapter{stub}, config.AgentsConfig{})
	state := &rootState{app: a}
	cmd := newAgentsPluginsMarketplaceAddCmd(state)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"caveman", "--source", "a/b"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stub.addedMarkets) != 1 || stub.addedMarkets[0].Name != "caveman" {
		t.Fatalf("expected adapter AddMarketplace call, got %v", stub.addedMarkets)
	}
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.Marketplaces) != 1 || cfg.Agents.Marketplaces[0].Source != "a/b" {
		t.Fatalf("expected manifest entry, got %+v", cfg.Agents.Marketplaces)
	}
}

func TestAgentsPluginsMarketplaceList_PrintsDeclared(t *testing.T) {
	agents := config.AgentsConfig{Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}}}
	a := newPluginCLITestApp(t, nil, agents)
	state := &rootState{app: a}
	cmd := newAgentsPluginsMarketplaceListCmd(state)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("caveman")) || !bytes.Contains(out.Bytes(), []byte("a/b")) {
		t.Fatalf("expected marketplace listed, got: %s", out.String())
	}
}

func TestAgentsPluginsMarketplaceRemove_DeletesManifestOnly(t *testing.T) {
	stub := &pluginStubAdapter{id: "claude-code"}
	agents := config.AgentsConfig{Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}}}
	a := newPluginCLITestApp(t, []app.PluginAdapter{stub}, agents)
	state := &rootState{app: a}
	cmd := newAgentsPluginsMarketplaceRemoveCmd(state)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"caveman"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.Marketplaces) != 0 {
		t.Fatal("expected marketplace removed from manifest")
	}
}
