package app_test

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider/script"
)

// A latest command that cannot resolve the installed version used to drop the row entirely, which left the tool less upgradable than one that never configured a check at all.
func TestRefreshOutdated_UnresolvableLatestStillOffersUpgrade(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mock := executor.NewMatchMock(
		// No version is obtainable: the probe finds nothing usable in the binary's output.
		executor.MatchRule{Pattern: `sh -c "$1" --version`, Response: executor.MockCall{Stdout: "no version here\n"}},
	).WithFallback(executor.MockCall{})
	a, cfgPath := newImportApp(t, script.New(mock))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{"widget": {Providers: []config.ToolInstallSpec{{
			Provider: "script",
			Options: map[string]string{
				"install": "install-widget",
				"check":   "test -x /usr/bin/widget",
				"latest":  "echo 9.9.9",
			},
		}}}},
		Groups: []*config.GroupConfig{{Tools: groupTools("widget")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.RefreshInstalled(ctx, nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	if err := a.RefreshOutdated(ctx, false, nil); err != nil {
		t.Fatalf("RefreshOutdated: %v", err)
	}

	got, err := a.DB().Get(ctx, "widget", "script", "widget")
	if err != nil {
		t.Fatalf("Get widget: %v", err)
	}
	if got.Version.Valid && got.Version.String != "" {
		t.Fatalf("version = %q, want unknown for this fixture", got.Version.String)
	}
	if !got.OutdatedUnknown {
		t.Fatal("outdated_unknown = false; an unresolvable check must degrade to unknown so upgrade stays offered")
	}
}

// A latest command that merely misbehaved this run must not discard the verdict a previous run established; only a permanently unresolvable version degrades to unknown.
func TestRefreshOutdated_MalformedLatestKeepsTheCachedVerdict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "sh -c echo-version", Response: executor.MockCall{Stdout: "1.0.0\n"}},
		// Two lines violates the single-line contract, so the check fails without being unanswerable.
		executor.MatchRule{Pattern: "sh -c echo-latest", Response: executor.MockCall{Stdout: "1.1.0\n2.0.0\n"}},
	).WithFallback(executor.MockCall{})
	a, cfgPath := newImportApp(t, script.New(mock))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{"widget": {Providers: []config.ToolInstallSpec{{
			Provider: "script",
			Options: map[string]string{
				"install": "install-widget",
				"check":   "test -x /usr/bin/widget",
				"version": "echo-version",
				"latest":  "echo-latest",
			},
		}}}},
		Groups: []*config.GroupConfig{{Tools: groupTools("widget")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.RefreshInstalled(ctx, nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}
	if err := a.DB().UpdateOutdatedBatch(ctx, []database.OutdatedUpdate{{
		Name: "widget", Provider: "script", Package: "widget", Outdated: true, LatestVersion: "1.1.0",
	}}); err != nil {
		t.Fatalf("seed outdated verdict: %v", err)
	}

	if err := a.RefreshOutdated(ctx, false, nil); err != nil {
		t.Fatalf("RefreshOutdated: %v", err)
	}

	got, err := a.DB().Get(ctx, "widget", "script", "widget")
	if err != nil {
		t.Fatalf("Get widget: %v", err)
	}
	if !got.Outdated || got.LatestVersion.String != "1.1.0" {
		t.Fatalf("outdated=%v latest=%q; a malformed latest run must preserve the cached verdict", got.Outdated, got.LatestVersion.String)
	}
	if got.OutdatedUnknown {
		t.Fatal("outdated_unknown = true; a transient failure must not downgrade a known verdict")
	}
}
