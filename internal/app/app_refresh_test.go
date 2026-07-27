package app_test

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider/script"
)

func TestRefreshOutdated_ScriptLatestCommandMarksConfiguredToolOutdated(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "sh -c exit 0", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "sh -c bun-latest", Response: executor.MockCall{Stdout: "1.3.0\n"}},
	).WithFallback(executor.MockCall{})
	a, cfgPath := newImportApp(t, script.New(mock))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"bun": {Providers: []config.ToolInstallSpec{{
				Provider: "script",
				Options: map[string]string{
					"install": "bun-install",
					"check":   "bun-check",
					"version": "bun --version",
					"latest":  "bun-latest",
				},
			}}},
		},
		Groups: []*config.GroupConfig{{Tools: groupTools("bun")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "bun",
		Provider:      "script",
		Package:       "bun",
		Installed:     true,
		InstalledWith: "script",
		Version:       sql.NullString{String: "1.2.3", Valid: true},
		LastChecked:   time.Now(),
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
	if !got.Outdated || !got.LatestVersion.Valid || got.LatestVersion.String != "1.3.0" {
		t.Fatalf("outdated = %v, latest = %q; want true, %q", got.Outdated, got.LatestVersion.String, "1.3.0")
	}
}

func TestRefreshOutdated_ScriptLatestCommandClearsCurrentTool(t *testing.T) {
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
	if err := a.DB().UpdateOutdated(ctx, "bun", "script", "bun", true, "1.3.0"); err != nil {
		t.Fatalf("seed outdated: %v", err)
	}

	if err := a.RefreshOutdated(ctx, false, nil); err != nil {
		t.Fatalf("RefreshOutdated: %v", err)
	}

	got, err := a.DB().Get(ctx, "bun", "script", "bun")
	if err != nil {
		t.Fatalf("Get bun: %v", err)
	}
	if got.Outdated || got.LatestVersion.Valid {
		t.Fatalf("outdated=%v latest=%q valid=%v; want cleared", got.Outdated, got.LatestVersion.String, got.LatestVersion.Valid)
	}
}

func TestRefreshOutdated_ScriptLatestCommandChecksSecondaryConfiguredCandidate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "sh -c exit 0", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "sh -c bun-latest", Response: executor.MockCall{Stdout: "1.3.0\n"}},
	).WithFallback(executor.MockCall{})
	a, cfgPath := newImportApp(t, &stubProvider{name: "brew", available: true}, script.New(mock))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{"bun": {Providers: []config.ToolInstallSpec{
			{Provider: "brew", Package: "bun"},
			{Provider: "script", Options: map[string]string{
				"install": "bun-install", "check": "bun-check", "version": "bun --version", "latest": "bun-latest",
			}},
		}}},
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
	if !got.Outdated || got.LatestVersion.String != "1.3.0" {
		t.Fatalf("secondary script outdated=%v latest=%q; want true, 1.3.0", got.Outdated, got.LatestVersion.String)
	}
}

func TestRefreshOutdated_GitHubRecipeMarksConfiguredScriptOutdated(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asset := currentPlatformGitHubCLIAsset(t)
	calls := int32(0)
	a, cfgPath := newImportApp(t, script.New(executor.NewMatchMock().WithFallback(executor.MockCall{})))
	a.SetGitHubFallbackAPIForTest("https://api.github.test", githubFallbackLatestReleaseClient(t, &calls, func() io.ReadCloser {
		return githubFallbackReleaseBody("v2.93.0", "2026-05-27T17:47:41Z", asset.name, asset.downloadURL)
	}))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"gh": {Providers: []config.ToolInstallSpec{{
				Provider: "script",
				Bin:      "gh",
				Options:  map[string]string{"version": "gh --version"},
				Source:   &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "cli", Repo: "cli"},
				Recipe:   &config.FallbackRecipe{Type: config.FallbackRecipeGitHubReleaseAsset, AssetPattern: asset.name},
			}}},
		},
		Groups: []*config.GroupConfig{{Tools: groupTools("gh")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "gh",
		Provider:      "script",
		Package:       "gh",
		Installed:     true,
		InstalledWith: "script",
		Version:       sql.NullString{String: "2.92.0", Valid: true},
		LastChecked:   time.Now(),
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	if err := a.RefreshOutdated(ctx, false, nil); err != nil {
		t.Fatalf("RefreshOutdated: %v", err)
	}

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("GitHub latest release calls = %d, want 1", got)
	}
	got, err := a.DB().Get(ctx, "gh", "script", "gh")
	if err != nil {
		t.Fatalf("Get gh: %v", err)
	}
	if !got.Outdated || !got.LatestVersion.Valid || got.LatestVersion.String != "v2.93.0" {
		t.Fatalf("outdated = %v, latest = %q; want true, %q", got.Outdated, got.LatestVersion.String, "v2.93.0")
	}
}

func TestRefreshOutdated_GitHubRecipeRequiresConfiguredReleaseAsset(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asset := currentPlatformGitHubCLIAsset(t)
	a, cfgPath := newImportApp(t, script.New(executor.NewMatchMock().WithFallback(executor.MockCall{})))
	a.SetGitHubFallbackAPIForTest("https://api.github.test", githubFallbackLatestReleaseClient(t, nil, func() io.ReadCloser {
		return githubFallbackReleaseBody("v2.93.0", "2026-05-27T17:47:41Z", asset.name, asset.downloadURL)
	}))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{"gh": {Providers: []config.ToolInstallSpec{{
			Provider: "script", Bin: "gh", Options: map[string]string{"version": "gh --version"},
			Source: &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "cli", Repo: "cli"},
			Recipe: &config.FallbackRecipe{Type: config.FallbackRecipeGitHubReleaseAsset, AssetPattern: "gh_{version}_unsupported.zip"},
		}}}},
		Groups: []*config.GroupConfig{{Tools: groupTools("gh")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name: "gh", Provider: "script", Package: "gh", Installed: true, InstalledWith: "script",
		Version: sql.NullString{String: "2.92.0", Valid: true}, Outdated: true, LastChecked: time.Now(),
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	if err := a.RefreshOutdated(ctx, false, nil); err != nil {
		t.Fatalf("RefreshOutdated: %v", err)
	}
	got, err := a.DB().Get(ctx, "gh", "script", "gh")
	if err != nil {
		t.Fatalf("Get gh: %v", err)
	}
	if got.Outdated || got.LatestVersion.Valid {
		t.Fatalf("missing configured asset left outdated=%v latest=%q; want cleared", got.Outdated, got.LatestVersion.String)
	}
}

func TestRefreshProviderOutdated_ScriptLatestCommandMarksConfiguredToolOutdated(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "sh -c exit 0", Response: executor.MockCall{}},
		executor.MatchRule{Pattern: "sh -c bun-latest", Response: executor.MockCall{Stdout: "1.3.0\n"}},
	).WithFallback(executor.MockCall{})
	a, cfgPath := newImportApp(t, script.New(mock))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"bun": {Providers: []config.ToolInstallSpec{{Provider: "script", Options: map[string]string{
				"install": "bun-install", "check": "bun-check", "version": "bun --version", "latest": "bun-latest",
			}}}},
		},
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

	if err := a.RefreshProviderOutdated(ctx, "script", false); err != nil {
		t.Fatalf("RefreshProviderOutdated: %v", err)
	}

	got, err := a.DB().Get(ctx, "bun", "script", "bun")
	if err != nil {
		t.Fatalf("Get bun: %v", err)
	}
	if !got.Outdated || got.LatestVersion.String != "1.3.0" {
		t.Fatalf("outdated = %v, latest = %q; want true, %q", got.Outdated, got.LatestVersion.String, "1.3.0")
	}
}

func TestRefreshOutdated_GitHubRecipesShareLatestReleaseLookup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asset := currentPlatformGitHubCLIAsset(t)
	calls := int32(0)
	a, cfgPath := newImportApp(t, script.New(executor.NewMatchMock().WithFallback(executor.MockCall{})))
	a.SetGitHubFallbackAPIForTest("https://api.github.test", githubFallbackLatestReleaseClient(t, &calls, func() io.ReadCloser {
		return githubFallbackReleaseBody("v2.93.0", "2026-05-27T17:47:41Z", asset.name, asset.downloadURL)
	}))
	tools := make(map[string]config.ToolSpec)
	for _, name := range []string{"gh-one", "gh-two"} {
		tools[name] = config.ToolSpec{Providers: []config.ToolInstallSpec{{
			Provider: "script", Bin: "gh", Options: map[string]string{"version": "gh --version"},
			Source: &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "cli", Repo: "cli"},
			Recipe: &config.FallbackRecipe{Type: config.FallbackRecipeGitHubReleaseAsset, AssetPattern: asset.name},
		}}}
		if err := a.DB().Upsert(ctx, &database.ToolCache{
			Name: name, Provider: "script", Package: name, Installed: true, InstalledWith: "script",
			Version: sql.NullString{String: "2.92.0", Valid: true}, LastChecked: time.Now(),
		}); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: tools, Groups: []*config.GroupConfig{{Tools: groupTools("gh-one", "gh-two")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.RefreshOutdated(ctx, false, nil); err != nil {
		t.Fatalf("RefreshOutdated: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("GitHub latest release calls = %d, want 1", got)
	}
}

func TestRefreshProviderOutdated_ConcurrentGitHubRecipesShareLatestReleaseLookup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asset := currentPlatformGitHubCLIAsset(t)
	calls := int32(0)
	a, cfgPath := newImportApp(t, script.New(executor.NewMatchMock().WithFallback(executor.MockCall{})))
	a.SetGitHubFallbackAPIForTest("https://api.github.test", githubFallbackLatestReleaseClient(t, &calls, func() io.ReadCloser {
		time.Sleep(20 * time.Millisecond)
		return githubFallbackReleaseBody("v2.93.0", "2026-05-27T17:47:41Z", asset.name, asset.downloadURL)
	}))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{"gh": {Providers: []config.ToolInstallSpec{{
			Provider: "script", Bin: "gh", Options: map[string]string{"version": "gh --version"},
			Source: &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "cli", Repo: "cli"},
			Recipe: &config.FallbackRecipe{Type: config.FallbackRecipeGitHubReleaseAsset, AssetPattern: asset.name},
		}}}},
		Groups: []*config.GroupConfig{{Tools: groupTools("gh")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name: "gh", Provider: "script", Package: "gh", Installed: true, InstalledWith: "script",
		Version: sql.NullString{String: "2.92.0", Valid: true}, LastChecked: time.Now(),
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- a.RefreshProviderOutdated(ctx, "script", false)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("RefreshProviderOutdated: %v", err)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("concurrent GitHub latest release calls = %d, want 1", got)
	}
}

func TestRefreshOutdated_PinnedGitHubRecipeDoesNotTrackLatest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asset := currentPlatformGitHubCLIAsset(t)
	calls := int32(0)
	a, cfgPath := newImportApp(t, script.New(executor.NewMatchMock().WithFallback(executor.MockCall{})))
	a.SetGitHubFallbackAPIForTest("https://api.github.test", githubFallbackLatestReleaseClient(t, &calls, func() io.ReadCloser {
		t.Fatal("latest release endpoint should not be called for a pinned recipe")
		return http.NoBody
	}))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{"gh": {Providers: []config.ToolInstallSpec{{
			Provider: "script", Bin: "gh", Options: map[string]string{"version": "gh --version"},
			Source: &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "cli", Repo: "cli"},
			Recipe: &config.FallbackRecipe{Type: config.FallbackRecipeGitHubReleaseAsset, AssetPattern: asset.name, TagName: "v2.92.0"},
		}}}},
		Groups: []*config.GroupConfig{{Tools: groupTools("gh")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name: "gh", Provider: "script", Package: "gh", Installed: true, InstalledWith: "script",
		Version: sql.NullString{String: "2.92.0", Valid: true}, LastChecked: time.Now(),
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	if err := a.DB().UpdateOutdated(ctx, "gh", "script", "gh", true, "v2.93.0"); err != nil {
		t.Fatalf("seed outdated: %v", err)
	}

	if err := a.RefreshOutdated(ctx, false, nil); err != nil {
		t.Fatalf("RefreshOutdated: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("GitHub latest release calls = %d, want 0", got)
	}
	got, err := a.DB().Get(ctx, "gh", "script", "gh")
	if err != nil {
		t.Fatalf("Get gh: %v", err)
	}
	if got.Outdated || got.LatestVersion.Valid {
		t.Fatalf("pinned recipe outdated=%v latest=%q; want cleared", got.Outdated, got.LatestVersion.String)
	}
}

func TestRefreshOutdated_GitHubFallbackMarksOutdatedFromLatestRelease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true})
	asset := currentPlatformGitHubCLIAsset(t)
	calls := int32(0)
	a.SetGitHubFallbackAPIForTest("https://api.github.test", githubFallbackLatestReleaseClient(t, &calls, func() io.ReadCloser {
		body, err := os.Open("testdata/github_cli_latest_release.json")
		if err != nil {
			t.Fatalf("open GitHub fixture: %v", err)
		}
		return body
	}))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"gh": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback:  githubFallbackSpec("v2.92.0", "2026-05-01T00:00:00Z", asset),
			},
		},
		Groups: []*config.GroupConfig{{Tools: groupTools("gh")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	seedGitHubFallbackCacheRow(t, a.DB(), false)

	if err := a.RefreshOutdated(ctx, false, nil); err != nil {
		t.Fatalf("RefreshOutdated: %v", err)
	}

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("GitHub latest release calls = %d, want 1", got)
	}
	assertGitHubFallbackOutdated(t, a.DB(), true, "v2.93.0")
}

func TestRefreshOutdated_GitHubFallbackClearsOutdatedWhenLatestNotNewer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true})
	asset := currentPlatformGitHubCLIAsset(t)
	calls := int32(0)
	a.SetGitHubFallbackAPIForTest("https://api.github.test", githubFallbackLatestReleaseClient(t, &calls, func() io.ReadCloser {
		return githubFallbackReleaseBody("v2.93.0", "2026-05-27T17:47:41Z", asset.name, asset.downloadURL)
	}))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"gh": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback:  githubFallbackSpec("v2.93.0", "2026-05-27T17:47:41Z", asset),
			},
		},
		Groups: []*config.GroupConfig{{Tools: groupTools("gh")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	seedGitHubFallbackCacheRow(t, a.DB(), true)

	if err := a.RefreshOutdated(ctx, false, nil); err != nil {
		t.Fatalf("RefreshOutdated: %v", err)
	}

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("GitHub latest release calls = %d, want 1", got)
	}
	assertGitHubFallbackOutdated(t, a.DB(), false, "")
}

func TestRefreshOutdated_GitHubFallbackClearsOutdatedWhenLatestOlder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true})
	asset := currentPlatformGitHubCLIAsset(t)
	calls := int32(0)
	a.SetGitHubFallbackAPIForTest("https://api.github.test", githubFallbackLatestReleaseClient(t, &calls, func() io.ReadCloser {
		return githubFallbackReleaseBody("v2.92.0", "2026-05-01T00:00:00Z", asset.name, asset.downloadURL)
	}))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"gh": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback:  githubFallbackSpec("v2.93.0", "2026-05-27T17:47:41Z", asset),
			},
		},
		Groups: []*config.GroupConfig{{Tools: groupTools("gh")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	seedGitHubFallbackCacheRow(t, a.DB(), true)

	if err := a.RefreshOutdated(ctx, false, nil); err != nil {
		t.Fatalf("RefreshOutdated: %v", err)
	}

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("GitHub latest release calls = %d, want 1", got)
	}
	assertGitHubFallbackOutdated(t, a.DB(), false, "")
}

func TestRefreshOutdated_GitHubFallbackSkipsLatestReleaseWithoutCurrentPlatformAsset(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true})
	asset := currentPlatformGitHubCLIAsset(t)
	calls := int32(0)
	a.SetGitHubFallbackAPIForTest("https://api.github.test", githubFallbackLatestReleaseClient(t, &calls, func() io.ReadCloser {
		return githubFallbackReleaseBody("v2.94.0", "2026-06-01T00:00:00Z", "gh_2.94.0_plan9_mips.tar.gz", "https://github.com/cli/cli/releases/download/v2.94.0/gh_2.94.0_plan9_mips.tar.gz")
	}))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"gh": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback:  githubFallbackSpec("v2.93.0", "2026-05-27T17:47:41Z", asset),
			},
		},
		Groups: []*config.GroupConfig{{Tools: groupTools("gh")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	seedGitHubFallbackCacheRow(t, a.DB(), true)

	if err := a.RefreshOutdated(ctx, false, nil); err != nil {
		t.Fatalf("RefreshOutdated: %v", err)
	}

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("GitHub latest release calls = %d, want 1", got)
	}
	assertGitHubFallbackOutdated(t, a.DB(), false, "")
}

func TestRefreshOutdated_GitHubFallbackSkipsIncompleteReleaseMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true})
	calls := int32(0)
	a.SetGitHubFallbackAPIForTest("https://api.github.test", githubFallbackLatestReleaseClient(t, &calls, func() io.ReadCloser {
		t.Fatal("GitHub latest release endpoint should not be called for incomplete saved fallback metadata")
		return http.NoBody
	}))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"gh": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback:  githubFallbackSpec("", "", currentPlatformGitHubCLIAsset(t)),
			},
		},
		Groups: []*config.GroupConfig{{Tools: groupTools("gh")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	seedGitHubFallbackCacheRow(t, a.DB(), true)

	if err := a.RefreshOutdated(ctx, false, nil); err != nil {
		t.Fatalf("RefreshOutdated: %v", err)
	}

	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("GitHub latest release calls = %d, want 0", got)
	}
	assertGitHubFallbackOutdated(t, a.DB(), false, "")
}

func TestRefreshOutdated_GitHubFallbackIgnoresNativeOwnedRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	brew := &provOutdatedStub{
		stubProvider: stubProvider{name: "brew", available: true},
		outdatedMap:  map[string]string{},
	}
	a, cfgPath := newImportApp(t, brew, &stubProvider{name: "system", available: true})
	a.SetGitHubFallbackAPIForTest("https://api.github.test", githubFallbackLatestReleaseClient(t, nil, func() io.ReadCloser {
		t.Fatal("GitHub latest release endpoint should not be called for native-owned rows")
		return http.NoBody
	}))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"gh": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback:  githubFallbackSpec("v2.92.0", "2026-05-01T00:00:00Z", currentPlatformGitHubCLIAsset(t)),
			},
		},
		Groups: []*config.GroupConfig{{Tools: groupTools("gh")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "gh",
		Provider:      "apt",
		Package:       "gh",
		Installed:     true,
		InstalledWith: "brew",
		Outdated:      true,
		LastChecked:   time.Now(),
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	if err := a.RefreshOutdated(ctx, false, nil); err != nil {
		t.Fatalf("RefreshOutdated: %v", err)
	}

	assertGitHubFallbackOutdated(t, a.DB(), false, "")
}

func TestRefreshProviderOutdated_GitHubFallbackMarksOutdatedFromLatestRelease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true})
	asset := currentPlatformGitHubCLIAsset(t)
	calls := int32(0)
	a.SetGitHubFallbackAPIForTest("https://api.github.test", githubFallbackLatestReleaseClient(t, &calls, func() io.ReadCloser {
		body, err := os.Open("testdata/github_cli_latest_release.json")
		if err != nil {
			t.Fatalf("open GitHub fixture: %v", err)
		}
		return body
	}))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"gh": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback:  githubFallbackSpec("v2.92.0", "2026-05-01T00:00:00Z", asset),
			},
		},
		Groups: []*config.GroupConfig{{Tools: groupTools("gh")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	seedGitHubFallbackCacheRow(t, a.DB(), false)

	if err := a.RefreshProviderOutdated(ctx, "apt", false); err != nil {
		t.Fatalf("RefreshProviderOutdated: %v", err)
	}

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("GitHub latest release calls = %d, want 1", got)
	}
	assertGitHubFallbackOutdated(t, a.DB(), true, "v2.93.0")
}

func githubFallbackSpec(tagName, publishedAt string, asset githubReleaseAssetFixture) *config.FallbackSpec {
	return &config.FallbackSpec{
		Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "cli", Repo: "cli"},
		Status: config.FallbackStatusVerified,
		Binary: "gh",
		Recipe: config.FallbackRecipe{
			Type:             config.FallbackRecipeGitHubReleaseAsset,
			ReleaseID:        "330388700",
			TagName:          tagName,
			PublishedAt:      publishedAt,
			AssetID:          "431301800",
			AssetName:        asset.name,
			AssetPattern:     asset.name,
			AssetDownloadURL: asset.downloadURL,
		},
		Commands: config.FallbackCommands{
			Check:   "command -v gh",
			Upgrade: "upgrade gh",
		},
	}
}

func seedGitHubFallbackCacheRow(t *testing.T, db *database.DB, outdated bool) {
	t.Helper()
	if err := db.Upsert(context.Background(), &database.ToolCache{
		Name:          "gh",
		Provider:      "apt",
		Package:       "gh",
		Installed:     true,
		InstalledWith: "gh",
		Outdated:      outdated,
		LastChecked:   time.Now(),
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	if outdated {
		if err := db.UpdateOutdated(context.Background(), "gh", "apt", "gh", true, "old"); err != nil {
			t.Fatalf("seed outdated state: %v", err)
		}
	}
}

func assertGitHubFallbackOutdated(t *testing.T, db *database.DB, wantOutdated bool, wantLatest string) {
	t.Helper()
	got, err := db.Get(context.Background(), "gh", "apt", "gh")
	if err != nil {
		t.Fatalf("Get gh: %v", err)
	}
	if got.Outdated != wantOutdated {
		t.Fatalf("outdated = %v, want %v", got.Outdated, wantOutdated)
	}
	if wantLatest == "" {
		if got.LatestVersion.Valid {
			t.Fatalf("latest_version = %q, want cleared", got.LatestVersion.String)
		}
		return
	}
	if !got.LatestVersion.Valid || got.LatestVersion.String != wantLatest {
		t.Fatalf("latest_version = %q (valid=%v), want %q", got.LatestVersion.String, got.LatestVersion.Valid, wantLatest)
	}
}

func githubFallbackLatestReleaseClient(t *testing.T, calls *int32, body func() io.ReadCloser) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/repos/cli/cli/releases/latest" {
			t.Fatalf("unexpected GitHub API path %q", req.URL.Path)
		}
		if calls != nil {
			atomic.AddInt32(calls, 1)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       body(),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}
}

func TestRefreshOutdated_GitHubFallbackUsesVersionComparisonWhenInstalledVersionStored(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true})
	asset := currentPlatformGitHubCLIAsset(t)
	a.SetGitHubFallbackAPIForTest("https://api.github.test", githubFallbackLatestReleaseClient(t, nil, func() io.ReadCloser {
		return githubFallbackReleaseBody("v2.93.0", "2026-05-27T17:47:41Z", asset.name, asset.downloadURL)
	}))
	spec := githubFallbackSpec("v2.92.0", "2026-05-01T00:00:00Z", asset)
	spec.Recipe.InstalledVersion = "2.92.0"
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"gh": {Providers: []config.ToolInstallSpec{{Provider: "apt"}}, Fallback: spec},
		},
		Groups: []*config.GroupConfig{{Tools: groupTools("gh")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	seedGitHubFallbackCacheRow(t, a.DB(), false)

	if err := a.RefreshOutdated(ctx, false, nil); err != nil {
		t.Fatalf("RefreshOutdated: %v", err)
	}
	assertGitHubFallbackOutdated(t, a.DB(), true, "v2.93.0")
}

func TestRefreshOutdated_GitHubFallbackNotOutdatedWhenInstalledVersionMatchesLatest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true})
	asset := currentPlatformGitHubCLIAsset(t)
	a.SetGitHubFallbackAPIForTest("https://api.github.test", githubFallbackLatestReleaseClient(t, nil, func() io.ReadCloser {
		return githubFallbackReleaseBody("v2.93.0", "2026-05-27T17:47:41Z", asset.name, asset.downloadURL)
	}))
	spec := githubFallbackSpec("v2.93.0", "2026-05-01T00:00:00Z", asset)
	spec.Recipe.InstalledVersion = "2.93.0"
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"gh": {Providers: []config.ToolInstallSpec{{Provider: "apt"}}, Fallback: spec},
		},
		Groups: []*config.GroupConfig{{Tools: groupTools("gh")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	seedGitHubFallbackCacheRow(t, a.DB(), true)

	if err := a.RefreshOutdated(ctx, false, nil); err != nil {
		t.Fatalf("RefreshOutdated: %v", err)
	}
	assertGitHubFallbackOutdated(t, a.DB(), false, "")
}

func githubFallbackReleaseBody(tagName, publishedAt, assetName, assetURL string) io.ReadCloser {
	return io.NopCloser(strings.NewReader(`{
  "id": 330388700,
  "tag_name": "` + tagName + `",
  "published_at": "` + publishedAt + `",
  "draft": false,
  "prerelease": false,
  "assets": [
    {
      "id": 431301800,
      "name": "` + assetName + `",
      "browser_download_url": "` + assetURL + `"
    }
  ]
}`))
}
