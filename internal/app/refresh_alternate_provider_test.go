package app_test

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

func TestRefreshInstalled_DetectsAlternateConfiguredProvider(t *testing.T) {
	t.Parallel()
	brew := &bulkCheckingStub{
		stubProvider: stubProvider{name: "brew", available: true},
		bulk:         map[string]string{},
	}
	bun := &bulkCheckingStub{
		stubProvider: stubProvider{name: "bun", available: true},
		bulk:         map[string]string{"pnpm": "11.11.0"},
	}
	a, cfgPath := newImportApp(t, brew, bun)
	ctx := context.Background()

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"pnpm": {
				Providers: []config.ToolInstallSpec{
					{Provider: "bun", Package: "pnpm"},
					{Provider: "brew", Package: "pnpm"},
				},
			},
		},
		Groups: []*config.GroupConfig{testHostToolGroup("pnpm")},
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.SaveSettings(ctx, config.Settings{
		ProviderPriority: []string{"brew", "bun", "pnpm", "npm"},
	}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	if err := a.DB().UpsertPackageAvailability(ctx, database.PackageAvailability{
		Name: "pnpm", Provider: "brew", Package: "pnpm", Available: true,
	}); err != nil {
		t.Fatalf("UpsertPackageAvailability: %v", err)
	}

	if err := a.RefreshInstalled(ctx, nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	all, listErr := a.DB().List(ctx)
	if listErr != nil {
		t.Fatalf("List: %v", listErr)
	}
	if len(all) != 1 || all[0].Name != "pnpm" {
		t.Fatalf("cached rows = %+v", all)
	}
	got := all[0]
	if got.Provider != "brew" {
		t.Fatalf("resolved provider = %q, want brew (row=%+v)", got.Provider, got)
	}
	if !got.Installed || got.InstalledWith != "bun" || got.Version.String != "11.11.0" {
		t.Fatalf("cache = installed:%v owner:%q version:%q, want true/bun/11.11.0", got.Installed, got.InstalledWith, got.Version.String)
	}

	class := app.ClassifyToolView(viewFromCache(got), app.ToolClassificationContext{})
	if class.SyncStatus != app.ToolSyncWrongProvider {
		t.Fatalf("sync status = %q, want wrong-provider", class.SyncStatus)
	}
}

func TestRefreshInstalled_StaleCachedOwnerFallsThroughToAlternateProvider(t *testing.T) {
	t.Parallel()
	brew := &bulkCheckingStub{
		stubProvider: stubProvider{name: "brew", available: true},
		bulk:         map[string]string{},
	}
	bun := &bulkCheckingStub{
		stubProvider: stubProvider{name: "bun", available: true},
		bulk:         map[string]string{"pnpm": "11.11.0"},
	}
	a, cfgPath := newImportApp(t, brew, bun)
	ctx := context.Background()

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"pnpm": {
				Providers: []config.ToolInstallSpec{
					{Provider: "bun", Package: "pnpm"},
					{Provider: "brew", Package: "pnpm"},
				},
			},
		},
		Groups: []*config.GroupConfig{testHostToolGroup("pnpm")},
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.SaveSettings(ctx, config.Settings{
		ProviderPriority: []string{"brew", "bun", "pnpm", "npm"},
	}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "pnpm",
		Provider:      "brew",
		Package:       "pnpm",
		Installed:     false,
		InstalledWith: "bun",
	}); err != nil {
		t.Fatalf("Upsert stale cache: %v", err)
	}

	if err := a.RefreshInstalled(ctx, nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	got, err := a.DB().Get(ctx, "pnpm", "brew", "pnpm")
	if err != nil {
		t.Fatalf("Get pnpm: %v", err)
	}
	if !got.Installed || got.InstalledWith != "bun" || got.Version.String != "11.11.0" {
		t.Fatalf("cache = installed:%v owner:%q version:%q, want true/bun/11.11.0", got.Installed, got.InstalledWith, got.Version.String)
	}
}

func TestRefreshProviderInstalled_DetectsAlternateConfiguredProvider(t *testing.T) {
	t.Parallel()
	brew := &bulkCheckingStub{
		stubProvider: stubProvider{name: "brew", available: true},
		bulk:         map[string]string{},
	}
	bun := &bulkCheckingStub{
		stubProvider: stubProvider{name: "bun", available: true},
		bulk:         map[string]string{"pnpm": "11.11.0"},
	}
	a, cfgPath := newImportApp(t, brew, bun)
	ctx := context.Background()

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"pnpm": {
				Providers: []config.ToolInstallSpec{
					{Provider: "bun", Package: "pnpm"},
					{Provider: "brew", Package: "pnpm"},
				},
			},
		},
		Groups: []*config.GroupConfig{testHostToolGroup("pnpm")},
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}
	if err := a.SaveSettings(ctx, config.Settings{
		ProviderPriority: []string{"brew", "bun", "pnpm", "npm"},
	}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	if err := a.RefreshProviderInstalled(ctx, "brew"); err != nil {
		t.Fatalf("RefreshProviderInstalled brew: %v", err)
	}

	got, err := a.DB().Get(ctx, "pnpm", "brew", "pnpm")
	if err != nil {
		t.Fatalf("Get pnpm: %v", err)
	}
	if !got.Installed || got.InstalledWith != "bun" || got.Version.String != "11.11.0" {
		t.Fatalf("cache = installed:%v owner:%q version:%q, want true/bun/11.11.0", got.Installed, got.InstalledWith, got.Version.String)
	}
}
