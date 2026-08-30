package app_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

func TestToolResolvesViaNvm_DetectsNvmBin(t *testing.T) {
	home := t.TempDir()
	nvmBin := filepath.Join(home, ".nvm", "versions", "node", "v22.1.0", "bin")
	if err := os.MkdirAll(nvmBin, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	pnpmPath := filepath.Join(nvmBin, "pnpm")
	if err := os.WriteFile(pnpmPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write pnpm: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("NVM_DIR", "")
	t.Setenv("NVM_BIN", nvmBin)
	t.Setenv("PATH", nvmBin)

	if !app.ToolResolvesViaNvm("pnpm", config.ToolSpec{}) {
		t.Fatal("ToolResolvesViaNvm(pnpm) = false, want true")
	}
}

func TestClassifyToolView_NvmManagedSystemToolIsOutOfSync(t *testing.T) {
	t.Parallel()
	for _, prov := range []string{"brew", "apt"} {
		t.Run(prov, func(t *testing.T) {
			got := app.ClassifyToolView(&app.ToolView{
				Name:      "pnpm",
				Provider:  prov,
				Tracked:   true,
				Installed: true,
			}, app.ToolClassificationContext{
				NvmManaged: map[string]bool{"pnpm": true},
			})
			if got.Section != app.ToolViewSectionOutOfSync || got.SyncStatus != app.ToolSyncNvmManaged {
				t.Fatalf("ClassifyToolView(%s) = %#v, want out-of-sync nvm-managed", prov, got)
			}
		})
	}
}

func TestMigrateNvmManagedTool_SwitchesOffBrew(t *testing.T) {
	testMigrateNvmManagedToolSwitchesOffSystemProvider(t, "brew")
}

func TestMigrateNvmManagedTool_SwitchesOffApt(t *testing.T) {
	testMigrateNvmManagedToolSwitchesOffSystemProvider(t, "apt")
}

func testMigrateNvmManagedToolSwitchesOffSystemProvider(t *testing.T, fromProvider string) {
	t.Helper()
	t.Setenv("OMNI_HOSTNAME", "testhost")

	home := t.TempDir()
	nvmBin := filepath.Join(home, ".nvm", "versions", "node", "v22.1.0", "bin")
	if err := os.MkdirAll(nvmBin, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	pnpmPath := filepath.Join(nvmBin, "pnpm")
	if err := os.WriteFile(pnpmPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write pnpm: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("NVM_DIR", "")
	t.Setenv("NVM_BIN", nvmBin)
	t.Setenv("PATH", nvmBin)

	system := &stubProvider{name: fromProvider, available: true}
	pnpm := &managerInstallStub{stubProvider: stubProvider{name: "pnpm", available: true}}
	a, cfgPath := newImportApp(t, system, pnpm)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"pnpm": {Providers: []config.ToolInstallSpec{{Provider: fromProvider, Package: "pnpm"}, {Provider: "cargo", Package: "pnpm-alt"}}},
		},
		Groups: []*config.GroupConfig{testHostToolGroup("pnpm")},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	result, err := a.MigrateNvmManagedTool(context.Background(), "pnpm")
	if err != nil {
		t.Fatalf("MigrateNvmManagedTool: %v", err)
	}
	if result == nil || result.ToProvider != "pnpm" || result.FromProvider != fromProvider {
		t.Fatalf("result = %+v, want %s→pnpm switch", result, fromProvider)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	providers := cfg.Tools["pnpm"].Providers
	if len(providers) != 2 || providers[0].Provider != "pnpm" || providers[1].Provider != "cargo" {
		t.Fatalf("pnpm candidates = %#v, want pnpm plus preserved cargo", providers)
	}
}

func TestMigrateNvmManagedToolWithStateConvergesCache(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	home := t.TempDir()
	nvmBin := filepath.Join(home, ".nvm", "versions", "node", "v22.1.0", "bin")
	if err := os.MkdirAll(nvmBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nvmBin, "pnpm"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("NVM_BIN", nvmBin)
	t.Setenv("PATH", nvmBin)
	brew := &stubProvider{name: "brew", available: true}
	pnpm := &managerInstallStub{stubProvider: stubProvider{name: "pnpm", available: true}}
	a, cfgPath := newImportApp(t, brew, pnpm)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools:  map[string]config.ToolSpec{"pnpm": {Providers: []config.ToolInstallSpec{{Provider: "brew", Package: "pnpm"}}}},
		Groups: []*config.GroupConfig{testHostToolGroup("pnpm")},
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.Install(context.Background(), "pnpm", "brew"); err != nil {
		t.Fatal(err)
	}
	state, err := a.MigrateNvmManagedToolWithState(context.Background(), "pnpm")
	if err != nil {
		t.Fatal(err)
	}
	if state.Result == nil || len(state.Tools) != 1 || state.Tools[0].Provider != "pnpm" || !state.Tools[0].Tracked {
		t.Fatalf("migration state = %#v", state)
	}
	db, err := database.Open(a.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var matched []*database.ToolCache
	for _, row := range rows {
		if row.Name == "pnpm" {
			matched = append(matched, row)
		}
	}
	if len(matched) != 1 || matched[0].Provider != "pnpm" || !matched[0].Tracked {
		t.Fatalf("migration cache = %#v", matched)
	}
}

func TestMigrateAllNvmManagedTools_MigratesDetectedTools(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")

	home := t.TempDir()
	nvmBin := filepath.Join(home, ".nvm", "versions", "node", "v22.1.0", "bin")
	if err := os.MkdirAll(nvmBin, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, name := range []string{"pnpm", "node"} {
		path := filepath.Join(nvmBin, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("NVM_DIR", "")
	t.Setenv("NVM_BIN", nvmBin)
	t.Setenv("PATH", nvmBin)

	brew := &stubProvider{name: "brew", available: true}
	pnpm := &managerInstallStub{stubProvider: stubProvider{name: "pnpm", available: true}}
	a, cfgPath := newImportApp(t, brew, pnpm)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"pnpm": {Providers: []config.ToolInstallSpec{{Provider: "brew", Package: "pnpm"}}},
			"node": {Providers: []config.ToolInstallSpec{{Provider: "brew", Package: "node"}}},
		},
		Groups: []*config.GroupConfig{testHostToolGroup("pnpm", "node")},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	result, err := a.MigrateAllNvmManagedTools(context.Background())
	if err != nil {
		t.Fatalf("MigrateAllNvmManagedTools: %v", err)
	}
	if len(result.Items) != 2 || len(result.Failures) != 0 {
		t.Fatalf("result = %+v, want 2 migrated tools", result)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, ok := cfg.Tools["node"]; ok {
		t.Fatal("node should be removed from config")
	}
	if cfg.Tools["pnpm"].DefaultInstallSpec().Provider != "pnpm" {
		t.Fatalf("pnpm provider = %q, want pnpm", cfg.Tools["pnpm"].DefaultInstallSpec().Provider)
	}
}
