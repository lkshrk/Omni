package app_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

func TestConsolidateToProvider_MigratesToolAndCleansOldCache(t *testing.T) {
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

func consolidateToolNames(tools []app.ConsolidateTool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}
