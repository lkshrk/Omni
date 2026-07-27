package app_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider/script"
)

func toolViewByName(t *testing.T, views []*app.ToolView, name string) *app.ToolView {
	t.Helper()
	for _, v := range views {
		if v != nil && v.Name == name {
			return v
		}
	}
	t.Fatalf("%s missing from tool views", name)
	return nil
}

func TestRefreshOutdated_ScriptWithoutLatestCommandOffersUpgradeAsUnknown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "sh -c exit 0", Response: executor.MockCall{}},
	).WithFallback(executor.MockCall{})
	a, cfgPath := newImportApp(t, script.New(mock))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{"bun": {Providers: []config.ToolInstallSpec{{
			Provider: "script", Options: map[string]string{
				"install": "bun-install", "check": "bun-check", "upgrade": "bun-upgrade",
			},
		}}}},
		Groups: []*config.GroupConfig{{Tools: groupTools("bun")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name: "bun", Provider: "script", Package: "bun", Installed: true, InstalledWith: "script",
		LastChecked: time.Now(),
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	if err := a.RefreshOutdated(ctx, false, nil); err != nil {
		t.Fatalf("RefreshOutdated: %v", err)
	}

	got, err := a.DB().Get(ctx, "bun", "script", "bun")
	if err != nil {
		t.Fatalf("Get bun: %v", err)
	}
	if got.Outdated {
		t.Fatal("outdated = true; an unknown update state must not read as outdated")
	}
	if !got.OutdatedUnknown {
		t.Fatal("outdated_unknown = false; a script tool with no latest command has an unknown update state")
	}

	view := toolViewByName(t, mustListToolViews(t, ctx, a), "bun")
	if !view.OutdatedUnknown {
		t.Fatal("ToolView.OutdatedUnknown = false; the unknown state must reach the view layer")
	}
	if !app.ToolOffersUpgrade(view) {
		t.Fatal("ToolOffersUpgrade = false; an unknown-state installed tool must offer upgrade")
	}
	if section := app.ClassifyToolView(view, app.ToolClassificationContext{}).Section; section != app.ToolViewSectionInstalled {
		t.Fatalf("section = %q; an unknown-state tool must not sort as an update", section)
	}
}

func TestRefreshOutdated_ScriptKnownCurrentVersionDoesNotOfferUpgrade(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "sh -c exit 0", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "sh -c bun-latest", Response: executor.MockCall{Stdout: "1.2.3\n"}},
	).WithFallback(executor.MockCall{})
	a, cfgPath := newImportApp(t, script.New(mock))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{"bun": {Providers: []config.ToolInstallSpec{{
			Provider: "script", Options: map[string]string{
				"install": "bun-install", "check": "bun-check", "version": "bun --version", "latest": "bun-latest",
			},
		}}}},
		Groups: []*config.GroupConfig{{Tools: groupTools("bun")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name: "bun", Provider: "script", Package: "bun", Installed: true, InstalledWith: "script",
		Version: sql.NullString{String: "1.2.3", Valid: true}, LastChecked: time.Now(),
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	if err := a.RefreshOutdated(ctx, false, nil); err != nil {
		t.Fatalf("RefreshOutdated: %v", err)
	}

	got, err := a.DB().Get(ctx, "bun", "script", "bun")
	if err != nil {
		t.Fatalf("Get bun: %v", err)
	}
	if got.Outdated || got.OutdatedUnknown {
		t.Fatalf("outdated=%v unknown=%v; a known-current tool is neither", got.Outdated, got.OutdatedUnknown)
	}
	if app.ToolOffersUpgrade(toolViewByName(t, mustListToolViews(t, ctx, a), "bun")) {
		t.Fatal("ToolOffersUpgrade = true; a known-current tool must not offer a pointless upgrade")
	}
}

func mustListToolViews(t *testing.T, ctx context.Context, a *app.App) []*app.ToolView {
	t.Helper()
	views, err := a.ListToolsForView(ctx, "")
	if err != nil {
		t.Fatalf("ListToolsForView: %v", err)
	}
	return views
}
