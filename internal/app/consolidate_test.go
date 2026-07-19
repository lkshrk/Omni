package app_test

import (
	"context"
	"database/sql"
	"errors"
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
	installedByManager map[string][]provider.Tool
	version            string
}

type consolidateInstallFailStub struct {
	stubProvider
	err error
}

func (s *consolidateInstallFailStub) Install(_ context.Context, _ provider.Tool) error {
	return s.err
}

func (s *managerInstallStub) InstallWithManager(_ context.Context, tool provider.Tool, manager string) error {
	if s.installedByManager == nil {
		s.installedByManager = make(map[string][]provider.Tool)
	}
	s.installedByManager[manager] = append(s.installedByManager[manager], tool)
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

func TestConsolidatePlan_CurrentConcreteProviderListDoesNotMigrate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	node := &managerInstallStub{stubProvider: stubProvider{name: "node", available: true}}
	a, cfgPath := newImportApp(t, node)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Version: config.CurrentVersion,
		Tools: map[string]config.ToolSpec{
			"prettier": {Providers: []config.ToolInstallSpec{{Provider: "npm", Package: "prettier"}}},
		},
		Groups: []*config.GroupConfig{testHostToolGroup("prettier")},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	result, err := a.ConsolidatePlan(ctx, "node", "bun")
	if err != nil {
		t.Fatalf("ConsolidatePlan: %v", err)
	}
	if len(result.Migrated) != 0 {
		t.Fatalf("planned migrations = %+v, want none for current concrete provider list", result.Migrated)
	}
	if len(node.installedByManager) != 0 {
		t.Fatalf("installs during dry-run = %+v, want none", node.installedByManager)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	spec := cfg.Tools["prettier"].DefaultInstallSpec()
	if spec.Provider != "npm" || spec.InstallWith != "" {
		t.Fatalf("prettier spec after dry-run = %+v, want unchanged npm", spec)
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

	// Consolidate to npm (non-default) so the setting is explicitly written.
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
