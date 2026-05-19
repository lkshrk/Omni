package app_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

type lifecycleProvider struct {
	stubProvider
	uninstalled       []provider.Tool
	uninstallManagers []string
	upgraded          []provider.Tool
	installedChecks   []provider.Tool
	managerChecks     []string
	resolvedName      string
	installed         bool
	version           string
	outdated          map[string]string
}

type multiManagerLifecycleProvider struct {
	lifecycleProvider
	entries map[string]provider.InstalledEntry
}

func (p *multiManagerLifecycleProvider) InstalledByManager(_ context.Context) (map[string]provider.InstalledEntry, error) {
	return p.entries, nil
}

func (p *lifecycleProvider) Uninstall(_ context.Context, tool provider.Tool) error {
	p.uninstalled = append(p.uninstalled, tool)
	return nil
}

func (p *lifecycleProvider) UninstallFrom(_ context.Context, tool provider.Tool, manager string) error {
	p.uninstallManagers = append(p.uninstallManagers, manager)
	p.uninstalled = append(p.uninstalled, tool)
	return nil
}

func (p *lifecycleProvider) Upgrade(_ context.Context, tool provider.Tool) error {
	p.upgraded = append(p.upgraded, tool)
	return nil
}

func (p *lifecycleProvider) IsInstalled(_ context.Context, tool provider.Tool) (bool, string, error) {
	p.installedChecks = append(p.installedChecks, tool)
	return p.installed, p.version, nil
}

func (p *lifecycleProvider) IsInstalledWithManager(_ context.Context, tool provider.Tool, manager string) (bool, string, error) {
	p.managerChecks = append(p.managerChecks, manager)
	p.installedChecks = append(p.installedChecks, tool)
	return p.installed, p.version, nil
}

func (p *lifecycleProvider) OutdatedMap(_ context.Context) (map[string]string, error) {
	return p.outdated, nil
}

func (p *lifecycleProvider) ResolvedName(_ context.Context) (string, error) {
	return p.resolvedName, nil
}

func TestCompleteExternalToolAction_UpgradeVerifiesAndClearsOutdated(t *testing.T) {
	ctx := context.Background()
	brew := &lifecycleProvider{
		stubProvider: stubProvider{name: "brew", available: true},
		installed:    true,
		version:      "2.0.0",
	}
	a, _ := newImportApp(t, brew)
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "parsec",
		Provider:      "system",
		Package:       "parsec",
		Installed:     true,
		InstalledWith: "brew",
		Version:       sql.NullString{String: "1.0.0", Valid: true},
		LastChecked:   time.Now(),
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	if err := a.DB().UpdateOutdated(ctx, "parsec", "system", "parsec", true, "2.0.0"); err != nil {
		t.Fatalf("seed outdated: %v", err)
	}

	if err := a.CompleteExternalToolAction(ctx, provider.PrivilegeActionUpgrade, "parsec", "system", "parsec", "brew"); err != nil {
		t.Fatalf("CompleteExternalToolAction: %v", err)
	}

	cached, err := a.DB().Get(ctx, "parsec", "system", "parsec")
	if err != nil {
		t.Fatalf("db.Get: %v", err)
	}
	if !cached.Installed || cached.InstalledWith != "brew" || !cached.Version.Valid || cached.Version.String != "2.0.0" {
		t.Fatalf("cached row = installed=%v installedWith=%q version=%+v, want installed via brew version 2.0.0", cached.Installed, cached.InstalledWith, cached.Version)
	}
	if cached.Outdated || cached.LatestVersion.Valid {
		t.Fatalf("outdated state = %v latest=%+v, want cleared", cached.Outdated, cached.LatestVersion)
	}
	if len(brew.installedChecks) != 1 || brew.installedChecks[0].Provider != "brew" {
		t.Fatalf("installed checks = %#v, want one brew-owned verification", brew.installedChecks)
	}
}

func TestCompleteExternalToolAction_UninstallRemovesConfigAndCache(t *testing.T) {
	ctx := context.Background()
	brew := &lifecycleProvider{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, brew)
	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("parsec", "system")),
		Groups: []*config.GroupConfig{{
			Name:  "testhost",
			Tools: groupTools("parsec"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "parsec",
		Provider:      "system",
		Package:       "parsec",
		Installed:     true,
		InstalledWith: "brew",
		LastChecked:   time.Now(),
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	if err := a.CompleteExternalToolAction(ctx, provider.PrivilegeActionUninstall, "parsec", "system", "parsec", "brew"); err != nil {
		t.Fatalf("CompleteExternalToolAction: %v", err)
	}

	if _, err := a.DB().Get(ctx, "parsec", "system", "parsec"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cache row after uninstall err = %v, want sql.ErrNoRows", err)
	}
	gotCfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	if _, ok := gotCfg.Tools["parsec"]; ok {
		t.Fatal("logical tool spec survived external uninstall completion")
	}
	if len(gotCfg.Groups) != 1 || len(gotCfg.Groups[0].Tools) != 0 {
		t.Fatalf("group tools after uninstall = %+v, want parsec removed", gotCfg.Groups)
	}
}

func TestCompleteExternalToolAction_UninstallRejectsProviderTool(t *testing.T) {
	ctx := context.Background()
	node := &lifecycleProvider{stubProvider: stubProvider{name: "node", available: true}}
	a, cfgPath := newImportApp(t, node)
	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalFixtureTool{Name: "npm", Provider: "node", InstallWith: "npm"}),
		Groups: []*config.GroupConfig{{
			Name:  "testhost",
			Tools: groupTools("npm"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "npm",
		Provider:      "node",
		Package:       "npm",
		Installed:     true,
		InstalledWith: "npm",
		LastChecked:   time.Now(),
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	err := a.CompleteExternalToolAction(ctx, provider.PrivilegeActionUninstall, "npm", "node", "npm", "npm")
	if err == nil || !strings.Contains(err.Error(), "package manager/provider") {
		t.Fatalf("CompleteExternalToolAction err = %v, want protected provider tool error", err)
	}

	if _, err := a.DB().Get(ctx, "npm", "node", "npm"); err != nil {
		t.Fatalf("cache row after rejected uninstall err = %v, want still present", err)
	}
	gotCfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	if _, ok := gotCfg.Tools["npm"]; !ok {
		t.Fatal("npm logical tool spec was removed despite protected provider guard")
	}
	if len(gotCfg.Groups) != 1 || !logicalTestGroupHasTool(gotCfg.Groups[0], "npm") {
		t.Fatalf("group tools after rejected uninstall = %+v, want npm preserved", gotCfg.Groups)
	}
}

func TestRefreshProviderInstalled_ManagerPinnedPersistsCheckedManager(t *testing.T) {
	python := &lifecycleProvider{
		stubProvider: stubProvider{name: "python", available: true},
		resolvedName: "uv",
		installed:    true,
		version:      "24.4.0",
	}
	a, cfgPath := newImportApp(t, python)
	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalFixtureTool{Name: "black", Provider: "python", InstallWith: "pip3"}),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("black"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if err := a.RefreshProviderInstalled(context.Background(), "python"); err != nil {
		t.Fatalf("RefreshProviderInstalled: %v", err)
	}

	cached, err := a.DB().Get(context.Background(), "black", "python", "black")
	if err != nil {
		t.Fatalf("db.Get: %v", err)
	}
	if cached.InstalledWith != "pip3" {
		t.Fatalf("InstalledWith = %q, want pip3", cached.InstalledWith)
	}
	if len(python.managerChecks) != 1 || python.managerChecks[0] != "pip3" {
		t.Fatalf("manager checks = %v, want [pip3]", python.managerChecks)
	}
}

func TestRefreshProviderInstalled_ManagerPinnedDoesNotDisableBulkForUnpinned(t *testing.T) {
	python := &multiManagerLifecycleProvider{
		lifecycleProvider: lifecycleProvider{
			stubProvider: stubProvider{name: "python", available: true},
			installed:    true,
			version:      "6.0.0",
		},
		entries: map[string]provider.InstalledEntry{
			"ruff": {Version: "0.4.0", ConcreteManager: "uv"},
		},
	}
	a, cfgPath := newImportApp(t, python)
	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ruff", "python"),
			logicalFixtureTool{Name: "flake8", Provider: "python", InstallWith: "pip3"},
		),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ruff", "flake8"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if err := a.RefreshProviderInstalled(context.Background(), "python"); err != nil {
		t.Fatalf("RefreshProviderInstalled: %v", err)
	}

	ruff, err := a.DB().Get(context.Background(), "ruff", "python", "ruff")
	if err != nil {
		t.Fatalf("get ruff: %v", err)
	}
	flake8, err := a.DB().Get(context.Background(), "flake8", "python", "flake8")
	if err != nil {
		t.Fatalf("get flake8: %v", err)
	}
	if !ruff.Installed || ruff.InstalledWith != "uv" {
		t.Fatalf("ruff installed/with = %v/%q, want true/uv", ruff.Installed, ruff.InstalledWith)
	}
	if !flake8.Installed || flake8.InstalledWith != "pip3" {
		t.Fatalf("flake8 installed/with = %v/%q, want true/pip3", flake8.Installed, flake8.InstalledWith)
	}
	if len(python.managerChecks) != 1 || python.managerChecks[0] != "pip3" {
		t.Fatalf("manager checks = %v, want [pip3]", python.managerChecks)
	}
	if len(python.installedChecks) != 1 || python.installedChecks[0].Name != "flake8" {
		t.Fatalf("per-tool checks = %+v, want only flake8", python.installedChecks)
	}
}

func TestUninstall_UsesRegisteredInstalledWithProvider(t *testing.T) {
	system := &lifecycleProvider{stubProvider: stubProvider{name: "system", available: true}}
	brew := &lifecycleProvider{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, system, brew)
	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalToolPackage("ripgrep", "system", "rg")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.DB().Upsert(context.Background(), &database.ToolCache{
		Name:          "ripgrep",
		Provider:      "system",
		Package:       "rg",
		Installed:     true,
		InstalledWith: "brew",
		LastChecked:   time.Now(),
	}); err != nil {
		t.Fatalf("db.Upsert: %v", err)
	}

	if err := a.Uninstall(context.Background(), "ripgrep", "system"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	if len(brew.uninstalled) != 1 || brew.uninstalled[0].Package != "rg" {
		t.Fatalf("brew uninstalled = %+v, want package rg", brew.uninstalled)
	}
	if len(system.uninstalled) != 0 {
		t.Fatalf("system uninstalled = %+v, want no calls", system.uninstalled)
	}
}

func TestInstall_ConcreteProviderRequestUsesConfiguredEcosystemTool(t *testing.T) {
	system := &lifecycleProvider{
		stubProvider: stubProvider{name: "system", available: true},
		resolvedName: "brew",
		installed:    true,
		version:      "14.1.1",
	}
	brew := &lifecycleProvider{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, system, brew)
	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalToolPackage("ripgrep", "system", "rg")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	if err := a.Install(context.Background(), "ripgrep", "brew"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if _, err := a.DB().Get(context.Background(), "ripgrep", "system", "rg"); err != nil {
		t.Fatalf("configured ecosystem cache row missing: %v", err)
	}
	if _, err := a.DB().Get(context.Background(), "ripgrep", "brew", "ripgrep"); err == nil {
		t.Fatal("unexpected fallback concrete cache row for brew/ripgrep")
	}
	if len(brew.stubProvider.installed) != 0 {
		t.Fatalf("brew direct install = %+v, want configured ecosystem path", brew.stubProvider.installed)
	}
}

func TestUninstall_ConcreteProviderRequestUsesConfiguredEcosystemTool(t *testing.T) {
	system := &lifecycleProvider{stubProvider: stubProvider{name: "system", available: true}, resolvedName: "brew"}
	brew := &lifecycleProvider{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, system, brew)
	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalToolPackage("ripgrep", "system", "rg")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.DB().Upsert(context.Background(), &database.ToolCache{
		Name:          "ripgrep",
		Provider:      "system",
		Package:       "rg",
		Installed:     true,
		InstalledWith: "brew",
		LastChecked:   time.Now(),
	}); err != nil {
		t.Fatalf("db.Upsert: %v", err)
	}

	if err := a.Uninstall(context.Background(), "ripgrep", "brew"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	if len(brew.uninstalled) != 1 || brew.uninstalled[0].Package != "rg" {
		t.Fatalf("brew uninstalled = %+v, want package rg", brew.uninstalled)
	}
	if _, err := a.DB().Get(context.Background(), "ripgrep", "system", "rg"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("configured cache row should be deleted, got err=%v", err)
	}
}

func TestUninstall_ReturnsCacheReadErrorBeforeProviderCall(t *testing.T) {
	brew := &lifecycleProvider{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, brew)
	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.DB().Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}

	if err := a.Uninstall(context.Background(), "ripgrep", "brew"); err == nil {
		t.Fatal("Uninstall error = nil, want cache read error")
	}
	if len(brew.uninstalled) != 0 {
		t.Fatalf("provider was called despite cache read error: %+v", brew.uninstalled)
	}
}

func TestUninstall_UsesCachedManagerForEcosystemProvider(t *testing.T) {
	python := &lifecycleProvider{stubProvider: stubProvider{name: "python", available: true}}
	a, cfgPath := newImportApp(t, python)
	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("black", "python")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("black"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.DB().Upsert(context.Background(), &database.ToolCache{
		Name:          "black",
		Provider:      "python",
		Package:       "black",
		Installed:     true,
		InstalledWith: "pip3",
		LastChecked:   time.Now(),
	}); err != nil {
		t.Fatalf("db.Upsert: %v", err)
	}

	if err := a.Uninstall(context.Background(), "black", "python"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	if len(python.uninstallManagers) != 1 || python.uninstallManagers[0] != "pip3" {
		t.Fatalf("uninstall managers = %v, want [pip3]", python.uninstallManagers)
	}
}

func TestUpgrade_UsesRegisteredInstalledWithProvider(t *testing.T) {
	system := &lifecycleProvider{stubProvider: stubProvider{name: "system", available: true}}
	brew := &lifecycleProvider{
		stubProvider: stubProvider{name: "brew", available: true},
		installed:    true,
		version:      "15.0.0",
	}
	a, cfgPath := newImportApp(t, system, brew)
	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalToolPackage("ripgrep", "system", "rg")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.DB().Upsert(context.Background(), &database.ToolCache{
		Name:          "ripgrep",
		Provider:      "system",
		Package:       "rg",
		Installed:     true,
		InstalledWith: "brew",
		Version:       sql.NullString{String: "14.0.0", Valid: true},
		Outdated:      true,
		LastChecked:   time.Now(),
	}); err != nil {
		t.Fatalf("db.Upsert: %v", err)
	}

	if err := a.Upgrade(context.Background(), "ripgrep", "system"); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	if len(brew.upgraded) != 1 || brew.upgraded[0].Package != "rg" {
		t.Fatalf("brew upgraded = %+v, want package rg", brew.upgraded)
	}
	if len(system.upgraded) != 0 {
		t.Fatalf("system upgraded = %+v, want no calls", system.upgraded)
	}
}

func TestUpgrade_ConcreteProviderRequestUsesConfiguredEcosystemTool(t *testing.T) {
	system := &lifecycleProvider{stubProvider: stubProvider{name: "system", available: true}, resolvedName: "brew"}
	brew := &lifecycleProvider{
		stubProvider: stubProvider{name: "brew", available: true},
		installed:    true,
		version:      "15.0.0",
	}
	a, cfgPath := newImportApp(t, system, brew)
	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalToolPackage("ripgrep", "system", "rg")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.DB().Upsert(context.Background(), &database.ToolCache{
		Name:          "ripgrep",
		Provider:      "system",
		Package:       "rg",
		Installed:     true,
		InstalledWith: "brew",
		Version:       sql.NullString{String: "14.0.0", Valid: true},
		Outdated:      true,
		LastChecked:   time.Now(),
	}); err != nil {
		t.Fatalf("db.Upsert: %v", err)
	}

	if err := a.Upgrade(context.Background(), "ripgrep", "brew"); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	if len(brew.upgraded) != 1 || brew.upgraded[0].Package != "rg" {
		t.Fatalf("brew upgraded = %+v, want package rg", brew.upgraded)
	}
	got, err := a.DB().Get(context.Background(), "ripgrep", "system", "rg")
	if err != nil {
		t.Fatalf("Get configured row: %v", err)
	}
	if got.Outdated {
		t.Fatal("configured row should no longer be outdated")
	}
	if _, err := a.DB().Get(context.Background(), "ripgrep", "brew", "ripgrep"); err == nil {
		t.Fatal("unexpected fallback concrete cache row for brew/ripgrep")
	}
}

func TestUpgrade_ReturnsCacheReadErrorBeforeProviderCall(t *testing.T) {
	brew := &lifecycleProvider{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, brew)
	cfg := &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{
			Tools: groupTools("ripgrep"),
		}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.DB().Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}

	if err := a.Upgrade(context.Background(), "ripgrep", "brew"); err == nil {
		t.Fatal("Upgrade error = nil, want cache read error")
	}
	if len(brew.upgraded) != 0 {
		t.Fatalf("provider was called despite cache read error: %+v", brew.upgraded)
	}
}
