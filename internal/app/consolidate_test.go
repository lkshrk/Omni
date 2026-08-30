package app_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

type managerInstallStub struct {
	stubProvider
	installedByManager   map[string][]provider.Tool
	uninstalledByManager map[string][]provider.Tool
	installErr           map[string]error
	version              string
	onInstall            func()
}

type consolidateInstallFailStub struct {
	stubProvider
	err error
}

func (s *consolidateInstallFailStub) Install(_ context.Context, _ provider.Tool) error {
	return s.err
}

func (s *managerInstallStub) InstallWithManager(_ context.Context, tool provider.Tool, manager string) error {
	if err := s.installErr[tool.EffectivePackage()]; err != nil {
		return err
	}
	if s.onInstall != nil {
		s.onInstall()
	}
	if s.installedByManager == nil {
		s.installedByManager = make(map[string][]provider.Tool)
	}
	s.installedByManager[manager] = append(s.installedByManager[manager], tool)
	return nil
}

func (s *managerInstallStub) UninstallFrom(_ context.Context, tool provider.Tool, manager string) error {
	if s.uninstalledByManager == nil {
		s.uninstalledByManager = make(map[string][]provider.Tool)
	}
	s.uninstalledByManager[manager] = append(s.uninstalledByManager[manager], tool)
	installed := s.installedByManager[manager]
	for i := range installed {
		if installed[i].EffectivePackage() == tool.EffectivePackage() {
			s.installedByManager[manager] = append(installed[:i], installed[i+1:]...)
			break
		}
	}
	return nil
}

func (s *managerInstallStub) IsInstalledWithManager(_ context.Context, tool provider.Tool, manager string) (bool, string, error) {
	for _, installed := range s.installedByManager[manager] {
		if installed.Name == tool.Name && installed.EffectivePackage() == tool.EffectivePackage() {
			version := s.version
			if version == "" {
				version = "2.0.0"
			}
			return true, version, nil
		}
	}
	return false, "", nil
}

type consolidateUninstallFailStub struct {
	stubProvider
	err      error
	attempts []provider.Tool
}

func (s *consolidateUninstallFailStub) Uninstall(_ context.Context, tool provider.Tool) error {
	s.attempts = append(s.attempts, tool)
	return s.err
}

func TestConsolidateToProvider_MigratesToolAndCleansOldCache(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	brew := &stubProvider{name: "brew", available: true}
	npm := &uninstallCaptureStub{stubProvider: stubProvider{name: "npm", available: true}}
	a, cfgPath := newImportApp(t, brew, npm)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"prettier": {Providers: []config.ToolInstallSpec{{Provider: "npm", Package: "prettier"}}},
			"ripgrep":  {Provider: "brew", Package: "rg"},
		},
		Groups: []*config.GroupConfig{testHostToolGroup("prettier", "ripgrep")},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "prettier",
		Provider:      "npm",
		Package:       "prettier",
		Installed:     true,
		InstalledWith: "npm",
	}); err != nil {
		t.Fatalf("seed npm cache: %v", err)
	}

	result, err := a.ConsolidateToProvider(ctx, "brew", false, nil)
	if err != nil {
		t.Fatalf("ConsolidateToProvider: %v", err)
	}
	if result.Manager != "brew" || result.SettingsUpdated {
		t.Fatalf("result = %+v, want brew without settings update", result)
	}
	wantMigrated := []string{"prettier"}
	if got := consolidateToolNames(result.Migrated); !reflect.DeepEqual(got, wantMigrated) {
		t.Fatalf("migrated = %v, want %v", got, wantMigrated)
	}
	if len(result.Failed) != 0 || len(result.UninstallWarnings) != 0 {
		t.Fatalf("result failures = %+v warnings = %+v, want none", result.Failed, result.UninstallWarnings)
	}
	if len(brew.installed) != 1 || brew.installed[0].Name != "prettier" || brew.installed[0].Provider != "brew" {
		t.Fatalf("brew installs = %+v, want prettier via brew", brew.installed)
	}
	if len(npm.uninstalled) != 1 || npm.uninstalled[0].Name != "prettier" || npm.uninstalled[0].Provider != "npm" {
		t.Fatalf("npm uninstalls = %+v, want prettier via npm", npm.uninstalled)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	spec := cfg.Tools["prettier"].DefaultInstallSpec()
	if spec.Provider != "brew" || spec.Package != "prettier" || spec.InstallWith != "" {
		t.Fatalf("prettier spec = %+v, want brew/prettier without install_with", spec)
	}
	if rg := cfg.Tools["ripgrep"].DefaultInstallSpec(); rg.Provider != "brew" || rg.Package != "rg" {
		t.Fatalf("ripgrep spec = %+v, want unchanged brew/rg", rg)
	}

	cached, err := a.DB().Get(ctx, "prettier", "brew", "prettier")
	if err != nil {
		t.Fatalf("brew cache get: %v", err)
	}
	if !cached.Installed || cached.InstalledWith != "brew" {
		t.Fatalf("brew cache = %+v, want installed with brew", cached)
	}
	if _, err := a.DB().Get(ctx, "prettier", "npm", "prettier"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("npm cache get error = %v, want sql.ErrNoRows", err)
	}
}

func TestConsolidateOptions_ReturnsAvailableEcosystemManagers(t *testing.T) {
	t.Parallel()
	a, _ := newImportApp(t, &stubProvider{name: "node", available: true})

	var got []string
	for _, opt := range a.ConsolidateOptions() {
		if opt.Ecosystem == "node" {
			got = append(got, opt.Manager)
		}
	}
	want := []string{"bun", "pnpm", "npm"}
	if !slices.Equal(got, want) {
		t.Fatalf("node consolidate options = %v, want %v", got, want)
	}
}

func TestConsolidatePlan_CurrentConcreteProviderListMigratesEffectiveRoute(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	node := &managerInstallStub{stubProvider: stubProvider{name: "node", available: true}}
	npm := &uninstallCaptureStub{stubProvider: stubProvider{name: "npm", available: true}}
	pnpm := provider.Named("pnpm", &stubProvider{name: "node", available: true})
	a, cfgPath := newImportApp(t, node, npm, pnpm)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{ProviderPriority: []string{"npm"}},
		Tools: map[string]config.ToolSpec{
			"prettier": {Providers: []config.ToolInstallSpec{{Provider: "npm", Package: "prettier"}}},
		},
		Groups: []*config.GroupConfig{testHostToolGroup("prettier")},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	result, err := a.ConsolidatePlan(ctx, "node", "pnpm")
	if err != nil {
		t.Fatalf("ConsolidatePlan: %v", err)
	}
	if got := consolidateToolNames(result.Migrated); !reflect.DeepEqual(got, []string{"prettier"}) {
		t.Fatalf("planned migrations = %v, want prettier", got)
	}
	if len(node.installedByManager) != 0 {
		t.Fatalf("installs during dry-run = %+v, want none", node.installedByManager)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	spec := cfg.Tools["prettier"].DefaultInstallSpec()
	if spec.Provider != "npm" || spec.InstallWith != "" || len(cfg.Tools["prettier"].Providers) != 1 {
		t.Fatalf("prettier spec after dry-run = %+v, want unchanged npm", spec)
	}
}

func TestConsolidate_CurrentSchemaUsesTargetCandidateAndConcreteCache(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	node := &managerInstallStub{stubProvider: stubProvider{name: "node", available: true}, version: "3.1.0"}
	npm := &uninstallCaptureStub{stubProvider: stubProvider{name: "npm", available: true}}
	a, cfgPath := newImportApp(t, node, npm)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{ProviderPriority: []string{"npm", "pnpm"}},
		Tools: map[string]config.ToolSpec{
			"prettier": {Providers: []config.ToolInstallSpec{
				{Provider: "npm", Package: "prettier-source", Options: map[string]string{"source": "yes"}},
				{Provider: "pnpm", Package: "prettier-target", Options: map[string]string{"target": "yes"}},
			}},
		},
		Groups: []*config.GroupConfig{testHostToolGroup("prettier")},
	}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []*database.ToolCache{
		{Name: "prettier", Provider: "node", Package: "prettier-source", Installed: true, InstalledWith: "npm", Tracked: true},
		{Name: "prettier", Provider: "npm", Package: "prettier-source", Installed: true, InstalledWith: "npm", Tracked: false},
	} {
		if err := a.DB().Upsert(ctx, row); err != nil {
			t.Fatal(err)
		}
	}

	result, err := a.Consolidate(ctx, "node", "pnpm", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := consolidateToolNames(result.Migrated); !reflect.DeepEqual(got, []string{"prettier"}) {
		t.Fatalf("migrated = %v", got)
	}
	installed := node.installedByManager["pnpm"]
	if len(installed) != 1 || installed[0].Package != "prettier-target" || !reflect.DeepEqual(installed[0].Options, map[string]string{"target": "yes"}) {
		t.Fatalf("pnpm installs = %#v", installed)
	}
	if len(npm.uninstalled) != 1 || npm.uninstalled[0].Package != "prettier-source" {
		t.Fatalf("npm uninstalls = %#v", npm.uninstalled)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	tool := cfg.Tools["prettier"]
	if tool.Provider != "" || tool.Package != "" || tool.InstallWith != "" || len(tool.Providers) != 2 || tool.Providers[1].Package != "prettier-target" {
		t.Fatalf("canonical tool spec = %#v", tool)
	}
	if got := cfg.HostSettings[testShortHostname()].ProviderPriority; len(got) == 0 || got[0] != "pnpm" {
		t.Fatalf("provider priority = %#v", got)
	}
	cached, err := a.DB().Get(ctx, "prettier", "pnpm", "prettier-target")
	if err != nil || !cached.Installed || cached.InstalledWith != "pnpm" || !cached.Tracked || cached.Version.String != "3.1.0" {
		t.Fatalf("pnpm cache = %#v, err=%v", cached, err)
	}
	for _, providerName := range []string{"npm", "node"} {
		if _, err := a.DB().Get(ctx, "prettier", providerName, "prettier-source"); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("stale %s cache error = %v", providerName, err)
		}
	}
}

func TestConsolidate_MissingTargetAppendsCloneAndRerunIsFixedPoint(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	node := &managerInstallStub{stubProvider: stubProvider{name: "node", available: true}}
	npm := &uninstallCaptureStub{stubProvider: stubProvider{name: "npm", available: true}}
	pnpm := provider.Named("pnpm", &stubProvider{name: "node", available: true})
	a, cfgPath := newImportApp(t, node, npm, pnpm)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{ProviderPriority: []string{"npm"}},
		Tools: map[string]config.ToolSpec{
			"prettier": {Providers: []config.ToolInstallSpec{{Provider: "npm", Package: "prettier-cli", Options: map[string]string{"channel": "stable"}}}},
		},
		Groups: []*config.GroupConfig{testHostToolGroup("prettier")},
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{Name: "prettier", Provider: "node", Package: "prettier-cli", Installed: true, InstalledWith: "npm", Tracked: true}); err != nil {
		t.Fatal(err)
	}

	first, err := a.Consolidate(ctx, "node", "pnpm", nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.Consolidate(ctx, "node", "pnpm", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Migrated) != 1 || len(second.Migrated) != 0 || second.SettingsUpdated {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	if len(node.installedByManager["pnpm"]) != 1 || len(npm.uninstalled) != 1 {
		t.Fatalf("fixed point repeated mutations: installs=%#v uninstalls=%#v", node.installedByManager, npm.uninstalled)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	providers := cfg.Tools["prettier"].Providers
	if len(providers) != 2 || providers[0].Provider != "npm" || providers[1].Provider != "pnpm" || providers[1].Package != "prettier-cli" || !reflect.DeepEqual(providers[1].Options, map[string]string{"channel": "stable"}) {
		t.Fatalf("appended target candidate = %#v", providers)
	}
	rows, err := a.DB().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var relevant []*database.ToolCache
	for _, row := range rows {
		if row.Name == "prettier" {
			relevant = append(relevant, row)
		}
	}
	if len(relevant) != 1 || relevant[0].Provider != "pnpm" || relevant[0].InstalledWith != "pnpm" || !relevant[0].Tracked {
		t.Fatalf("fixed-point cache = %#v", relevant)
	}
}

func TestConsolidate_LegacyManagerSpecCanonicalizesToConcreteCandidates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	node := &managerInstallStub{stubProvider: stubProvider{name: "node", available: true}}
	npm := &uninstallCaptureStub{stubProvider: stubProvider{name: "npm", available: true}}
	a, cfgPath := newImportApp(t, node, npm)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"prettier": {Provider: "node", Package: "prettier-cli", InstallWith: "npm", Options: map[string]string{"legacy": "yes"}},
		},
		Groups: []*config.GroupConfig{testHostToolGroup("prettier")},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := a.Consolidate(ctx, "node", "pnpm", nil); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	tool := cfg.Tools["prettier"]
	if tool.Provider != "" || tool.Package != "" || tool.InstallWith != "" || tool.Options != nil || len(tool.Providers) != 2 {
		t.Fatalf("legacy scalar fields survived = %#v", tool)
	}
	if tool.Providers[0].Provider != "npm" || tool.Providers[1].Provider != "pnpm" || tool.Providers[1].Package != "prettier-cli" || !reflect.DeepEqual(tool.Providers[1].Options, map[string]string{"legacy": "yes"}) {
		t.Fatalf("canonical legacy candidates = %#v", tool.Providers)
	}
}

func TestConsolidate_PipAliasAlreadyOnTargetDoesNotMigrate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	python := &managerInstallStub{
		stubProvider: stubProvider{name: "python", available: true},
		installedByManager: map[string][]provider.Tool{
			"pip": {{Name: "black", Provider: "python", Package: "black"}},
		},
	}
	pip3 := &uninstallCaptureStub{stubProvider: stubProvider{name: "pip3", available: true}}
	a, cfgPath := newImportApp(t, python, pip3)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"black": {Provider: "python", Package: "black", InstallWith: "pip3"},
		},
		Groups: []*config.GroupConfig{testHostToolGroup("black")},
	}); err != nil {
		t.Fatal(err)
	}

	result, err := a.Consolidate(ctx, "python", "pip", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Migrated) != 0 || len(pip3.uninstalled) != 0 || len(python.installedByManager["pip"]) != 1 {
		t.Fatalf("pip3 alias caused migration: result=%+v installs=%#v uninstalls=%#v", result, python.installedByManager, pip3.uninstalled)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	tool := cfg.Tools["black"]
	if tool.Provider != "" || tool.InstallWith != "" || len(tool.Providers) != 1 || tool.Providers[0].Provider != "pip" {
		t.Fatalf("pip alias canonical spec = %#v", tool)
	}
}

func TestConsolidate_ConfigSaveFailureKeepsSourceAndCleansTarget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	node := &managerInstallStub{stubProvider: stubProvider{name: "node", available: true}}
	npm := &uninstallCaptureStub{stubProvider: stubProvider{name: "npm", available: true}}
	pnpm := provider.Named("pnpm", &stubProvider{name: "node", available: true})
	a, cfgPath := newImportApp(t, node, npm, pnpm)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{ProviderPriority: []string{"npm"}},
		Tools: map[string]config.ToolSpec{
			"prettier": {Providers: []config.ToolInstallSpec{{Provider: "npm", Package: "prettier"}}},
		},
		Groups: []*config.GroupConfig{testHostToolGroup("prettier")},
	}); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	node.onInstall = func() { a.ConfigPath = blocked }

	if _, err := a.Consolidate(ctx, "node", "pnpm", nil); err == nil || !strings.Contains(err.Error(), "saving consolidate config") {
		t.Fatalf("config save error = %v", err)
	}
	if len(npm.uninstalled) != 0 {
		t.Fatalf("source uninstalled before config commit: %#v", npm.uninstalled)
	}
	if len(node.installedByManager["pnpm"]) != 0 || len(node.uninstalledByManager["pnpm"]) != 1 {
		t.Fatalf("target compensation = installed %#v, uninstalled %#v", node.installedByManager, node.uninstalledByManager)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Tools["prettier"].Providers) != 1 || cfg.Tools["prettier"].Providers[0].Provider != "npm" {
		t.Fatalf("source config changed after failed save: %#v", cfg.Tools["prettier"])
	}
}

func TestConsolidate_ConfigSaveFailurePreservesPreexistingTarget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	node := &managerInstallStub{
		stubProvider: stubProvider{name: "node", available: true},
		installedByManager: map[string][]provider.Tool{
			"pnpm": {{Name: "a-existing", Provider: "node", Package: "existing-target"}},
		},
	}
	npm := &uninstallCaptureStub{stubProvider: stubProvider{name: "npm", available: true}}
	pnpm := provider.Named("pnpm", &stubProvider{name: "node", available: true})
	a, _ := newImportApp(t, node, npm, pnpm)
	originalPath := a.ConfigPath
	if err := saveAppConfig(t, originalPath, &config.RootConfig{
		Settings: config.Settings{ProviderPriority: []string{"npm"}},
		Tools: map[string]config.ToolSpec{
			"a-existing": {Providers: []config.ToolInstallSpec{{Provider: "npm", Package: "existing-source"}, {Provider: "pnpm", Package: "existing-target"}}},
			"b-new":      {Providers: []config.ToolInstallSpec{{Provider: "npm", Package: "new-source"}, {Provider: "pnpm", Package: "new-target"}}},
		},
		Groups: []*config.GroupConfig{testHostToolGroup("a-existing", "b-new")},
	}); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	node.onInstall = func() { a.ConfigPath = blocked }

	if _, err := a.Consolidate(ctx, "node", "pnpm", nil); err == nil {
		t.Fatal("expected config save failure")
	}
	installed := node.installedByManager["pnpm"]
	if len(installed) != 1 || installed[0].Package != "existing-target" {
		t.Fatalf("preexisting target was removed: %#v", installed)
	}
	uninstalled := node.uninstalledByManager["pnpm"]
	if len(uninstalled) != 1 || uninstalled[0].Package != "new-target" {
		t.Fatalf("target compensation = %#v, want only new-target", uninstalled)
	}
	if len(npm.uninstalled) != 0 {
		t.Fatalf("source uninstalled before config commit: %#v", npm.uninstalled)
	}
}

func TestConsolidate_LaterToolFailurePreservesPreexistingTarget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	installErr := errors.New("later target install failed")
	node := &managerInstallStub{
		stubProvider: stubProvider{name: "node", available: true},
		installedByManager: map[string][]provider.Tool{
			"pnpm": {{Name: "a-existing", Provider: "node", Package: "existing-target"}},
		},
		installErr: map[string]error{"failing-target": installErr},
	}
	npm := &uninstallCaptureStub{stubProvider: stubProvider{name: "npm", available: true}}
	pnpm := provider.Named("pnpm", &stubProvider{name: "node", available: true})
	a, cfgPath := newImportApp(t, node, npm, pnpm)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{ProviderPriority: []string{"npm"}},
		Tools: map[string]config.ToolSpec{
			"a-existing": {Providers: []config.ToolInstallSpec{{Provider: "npm", Package: "existing-source"}, {Provider: "pnpm", Package: "existing-target"}}},
			"b-failing":  {Providers: []config.ToolInstallSpec{{Provider: "npm", Package: "failing-source"}, {Provider: "pnpm", Package: "failing-target"}}},
		},
		Groups: []*config.GroupConfig{testHostToolGroup("a-existing", "b-failing")},
	}); err != nil {
		t.Fatal(err)
	}

	result, err := a.Consolidate(ctx, "node", "pnpm", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Failed) != 1 || !errors.Is(result.Failed[0].Err, installErr) {
		t.Fatalf("failures = %#v", result.Failed)
	}
	installed := node.installedByManager["pnpm"]
	if len(installed) != 1 || installed[0].Package != "existing-target" || len(node.uninstalledByManager["pnpm"]) != 0 {
		t.Fatalf("later failure disturbed preexisting target: installed=%#v uninstalled=%#v", installed, node.uninstalledByManager)
	}
	if len(npm.uninstalled) != 0 {
		t.Fatalf("source uninstalled on atomic install failure: %#v", npm.uninstalled)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.HostSettings[testShortHostname()].ProviderPriority; len(got) != 0 {
		t.Fatalf("settings committed despite later install failure: %#v", got)
	}
}

func TestConsolidate_FailedUninstallKeepsRefreshedUntrackedSource(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	node := &managerInstallStub{stubProvider: stubProvider{name: "node", available: true}}
	uninstallErr := errors.New("npm uninstall failed")
	npm := &consolidateUninstallFailStub{
		stubProvider: stubProvider{name: "npm", available: true, installed: []provider.InstalledTool{{Tool: provider.Tool{Name: "prettier", Provider: "npm", Package: "prettier"}, Version: "1.4.0"}}},
		err:          uninstallErr,
	}
	a, cfgPath := newImportApp(t, node, npm)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{ProviderPriority: []string{"npm"}},
		Tools: map[string]config.ToolSpec{
			"prettier": {Providers: []config.ToolInstallSpec{{Provider: "npm", Package: "prettier"}, {Provider: "pnpm", Package: "prettier"}}},
		},
		Groups: []*config.GroupConfig{testHostToolGroup("prettier")},
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{Name: "prettier", Provider: "npm", Package: "prettier", Installed: true, InstalledWith: "npm", Version: sql.NullString{String: "1.0.0", Valid: true}, Tracked: true}); err != nil {
		t.Fatal(err)
	}

	result, err := a.Consolidate(ctx, "node", "pnpm", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.UninstallWarnings) != 1 || !errors.Is(result.UninstallWarnings[0].Err, uninstallErr) {
		t.Fatalf("uninstall warnings = %#v", result.UninstallWarnings)
	}
	source, err := a.DB().Get(ctx, "prettier", "npm", "prettier")
	if err != nil || !source.Installed || source.Tracked || source.Version.String != "1.4.0" {
		t.Fatalf("failed-uninstall source cache = %#v, err=%v", source, err)
	}
	target, err := a.DB().Get(ctx, "prettier", "pnpm", "prettier")
	if err != nil || !target.Installed || !target.Tracked || target.InstalledWith != "pnpm" {
		t.Fatalf("target cache = %#v, err=%v", target, err)
	}
}

func TestConsolidate_PreservesUnrelatedSecondaryManagerCache(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	node := &managerInstallStub{stubProvider: stubProvider{name: "node", available: true}}
	npm := &uninstallCaptureStub{stubProvider: stubProvider{name: "npm", available: true}}
	a, cfgPath := newImportApp(t, node, npm)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{ProviderPriority: []string{"npm"}},
		Tools: map[string]config.ToolSpec{
			"prettier": {Providers: []config.ToolInstallSpec{{Provider: "npm", Package: "prettier"}, {Provider: "pnpm", Package: "prettier"}}},
		},
		Groups: []*config.GroupConfig{testHostToolGroup("prettier")},
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{Name: "prettier", Provider: "npm", Package: "prettier", Installed: true, InstalledWith: "npm", Tracked: true}); err != nil {
		t.Fatal(err)
	}
	if err := a.DB().UpsertDiscovered(ctx, "prettier", "bun", "bun", "1.0.0"); err != nil {
		t.Fatal(err)
	}

	if _, err := a.Consolidate(ctx, "node", "pnpm", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB().Get(ctx, "prettier", "npm", "prettier"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("removed npm source cache error = %v", err)
	}
	secondary, err := a.DB().Get(ctx, "prettier", "bun", "prettier")
	if err != nil || !secondary.Installed || secondary.Tracked {
		t.Fatalf("unrelated bun cache = %#v, err=%v", secondary, err)
	}
}

func TestConsolidateWithState_UpdatesManagerSettingAndReturnsState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	node := &managerInstallStub{stubProvider: stubProvider{name: "node", available: true}, version: "3.1.0"}
	a, cfgPath := newImportApp(t, node)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Version: config.CurrentVersion,
		Groups:  []*config.GroupConfig{testHostToolGroup()},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	state, err := a.ConsolidateWithState(ctx, "node", "npm", nil)
	if err != nil {
		t.Fatalf("ConsolidateWithState: %v", err)
	}
	if state.Result == nil || state.Result.Ecosystem != "node" || state.Result.Manager != "npm" || !state.Result.SettingsUpdated {
		t.Fatalf("result = %+v, want node/npm with settings update", state.Result)
	}
	if len(state.Result.Migrated) != 0 {
		t.Fatalf("migrated = %+v, want none", state.Result.Migrated)
	}
	if len(state.Tools) != 0 {
		t.Fatalf("state tools = %+v, want none", state.Tools)
	}
	if state.State == nil {
		t.Fatal("state.State = nil, want tool group state")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got := app.EffectiveEcosystemManager(cfg.HostSettings[testShortHostname()], "node"); got != "npm" {
		t.Fatalf("node manager = %q, want npm", got)
	}
}

func TestConsolidateToProvider_CollectsInstallAndVerificationFailures(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	installErr := errors.New("install failed")
	verifyErr := errors.New("status failed")
	tests := []struct {
		name     string
		target   provider.Provider
		wantText string
	}{
		{
			name:     "install failure",
			target:   &consolidateInstallFailStub{stubProvider: stubProvider{name: "brew", available: true}, err: installErr},
			wantText: "install failed",
		},
		{
			name:     "verification failure",
			target:   &installVerifyStub{stubProvider: stubProvider{name: "brew", available: true}, verifyErr: verifyErr},
			wantText: "status failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a, cfgPath := newImportApp(t, tt.target, &stubProvider{name: "npm", available: true})
			if err := saveAppConfig(t, cfgPath, &config.RootConfig{
				Tools: map[string]config.ToolSpec{
					"prettier": {Providers: []config.ToolInstallSpec{{Provider: "npm", Package: "prettier"}}},
				},
			}); err != nil {
				t.Fatalf("save config: %v", err)
			}
			result, err := a.ConsolidateToProvider(ctx, "brew", false, nil)
			if err != nil {
				t.Fatalf("ConsolidateToProvider: %v", err)
			}
			if len(result.Failed) != 1 || result.Failed[0].Name != "prettier" || !strings.Contains(result.Failed[0].Err.Error(), tt.wantText) {
				t.Fatalf("failed = %+v, want prettier error containing %q", result.Failed, tt.wantText)
			}
			cfg, err := config.Load(cfgPath)
			if err != nil {
				t.Fatalf("config.Load: %v", err)
			}
			if got := cfg.Tools["prettier"].DefaultInstallSpec().Provider; got != "npm" {
				t.Fatalf("provider after failed consolidate = %q, want npm", got)
			}
		})
	}
}

func consolidateToolNames(tools []app.ConsolidateTool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}
