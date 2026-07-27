package app_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider/script"
	isync "github.com/lkshrk/omni/internal/sync"
)

func unpinnedActionlintConfig() *config.RootConfig {
	return &config.RootConfig{
		Tools: map[string]config.ToolSpec{"actionlint": {Providers: []config.ToolInstallSpec{{
			Provider: "script",
			Bin:      "actionlint",
			Source:   &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "rhysd", Repo: "actionlint"},
			Recipe: &config.FallbackRecipe{
				Type:         config.FallbackRecipeGitHubReleaseAsset,
				AssetPattern: "actionlint_{version}_linux_amd64.tar.gz",
			},
		}}}},
		Groups: []*config.GroupConfig{{Tools: groupTools("actionlint")}},
	}
}

func actionlintLatestReleaseClient(t *testing.T, calls *int32, tag string) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/repos/rhysd/actionlint/releases/latest" {
			t.Fatalf("unexpected GitHub API path %q", req.URL.Path)
		}
		if calls != nil {
			atomic.AddInt32(calls, 1)
		}
		asset := "actionlint_1.7.12_linux_amd64.tar.gz"
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body: githubFallbackReleaseBody(tag, "2026-05-27T17:47:41Z", asset,
				"https://github.com/rhysd/actionlint/releases/download/"+tag+"/"+asset),
			Header:  make(http.Header),
			Request: req,
		}, nil
	})}
}

// Reports the binary as absent until the install command has run, so a sync sees a real install.
type installOnceExecutor struct {
	mu        sync.Mutex
	installed bool
}

func (e *installOnceExecutor) Run(_ context.Context, name string, args ...string) (string, string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cmd := strings.Join(args, " ")
	switch {
	case strings.Contains(cmd, "mkdir -p"):
		e.installed = true
	case strings.HasPrefix(strings.TrimPrefix(cmd, "-c "), "test -x") && !e.installed:
		return "", "", errors.New("not installed")
	}
	_ = name
	return "", "", nil
}

func TestSync_UnpinnedGitHubRecipeRecordsResolvedRelease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a, cfgPath := newImportApp(t, script.New(&installOnceExecutor{}))
	a.SetGitHubFallbackAPIForTest("https://api.github.test", actionlintLatestReleaseClient(t, nil, "v1.7.12"))
	if err := saveAppConfig(t, cfgPath, unpinnedActionlintConfig()); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	result, err := a.Sync(ctx, isync.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(result.Installed()) != 1 {
		t.Fatalf("installed ops = %d, want 1 (ops: %+v)", len(result.Installed()), result.Ops)
	}

	got, err := a.DB().Get(ctx, "actionlint", "script", "actionlint")
	if err != nil {
		t.Fatalf("Get actionlint: %v", err)
	}
	if !got.Version.Valid || got.Version.String != "1.7.12" {
		t.Fatalf("version = %q valid=%v; want %q", got.Version.String, got.Version.Valid, "1.7.12")
	}
}

func TestInstall_UnpinnedGitHubRecipeRecordsResolvedRelease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mock := executor.NewMatchMock().WithFallback(executor.MockCall{})
	a, cfgPath := newImportApp(t, script.New(mock))
	a.SetGitHubFallbackAPIForTest("https://api.github.test", actionlintLatestReleaseClient(t, nil, "v1.7.12"))
	if err := saveAppConfig(t, cfgPath, unpinnedActionlintConfig()); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.Install(ctx, "actionlint", "script"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	got, err := a.DB().Get(ctx, "actionlint", "script", "actionlint")
	if err != nil {
		t.Fatalf("Get actionlint: %v", err)
	}
	if !got.Version.Valid || got.Version.String != "1.7.12" {
		t.Fatalf("version = %q valid=%v; want %q", got.Version.String, got.Version.Valid, "1.7.12")
	}
}

func TestInstall_UnpinnedGitHubRecipeVersionSurvivesRefresh(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mock := executor.NewMatchMock().WithFallback(executor.MockCall{})
	a, cfgPath := newImportApp(t, script.New(mock))
	a.SetGitHubFallbackAPIForTest("https://api.github.test", actionlintLatestReleaseClient(t, nil, "v1.7.12"))
	if err := saveAppConfig(t, cfgPath, unpinnedActionlintConfig()); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.Install(ctx, "actionlint", "script"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if err := a.RefreshInstalled(ctx, nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	got, err := a.DB().Get(ctx, "actionlint", "script", "actionlint")
	if err != nil {
		t.Fatalf("Get actionlint: %v", err)
	}
	if got.Version.String != "1.7.12" {
		t.Fatalf("version = %q; want the recorded release to outlive the install", got.Version.String)
	}
}

func TestInstall_PinnedGitHubRecipeDoesNotResolveARelease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mock := executor.NewMatchMock().WithFallback(executor.MockCall{})
	a, cfgPath := newImportApp(t, script.New(mock))
	a.SetGitHubFallbackAPIForTest("https://api.github.test", &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("a pinned recipe must not call the GitHub API")
			return nil, nil
		}),
	})
	cfg := unpinnedActionlintConfig()
	spec := cfg.Tools["actionlint"]
	spec.Providers[0].Recipe.TagName = "v1.7.11"
	cfg.Tools["actionlint"] = spec
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.Install(ctx, "actionlint", "script"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	got, err := a.DB().Get(ctx, "actionlint", "script", "actionlint")
	if err != nil {
		t.Fatalf("Get actionlint: %v", err)
	}
	if got.Version.String != "1.7.11" {
		t.Fatalf("version = %q; want the pinned tag %q", got.Version.String, "1.7.11")
	}
}

func TestInstall_UnpinnedGitHubRecipeIgnoresAReleaseMissingTheAsset(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mock := executor.NewMatchMock().WithFallback(executor.MockCall{})
	a, cfgPath := newImportApp(t, script.New(mock))
	a.SetGitHubFallbackAPIForTest("https://api.github.test", &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body: githubFallbackReleaseBody("v1.7.12", "2026-05-27T17:47:41Z", "actionlint_1.7.12_windows_amd64.zip",
					"https://github.com/rhysd/actionlint/releases/download/v1.7.12/actionlint_1.7.12_windows_amd64.zip"),
				Header:  make(http.Header),
				Request: req,
			}, nil
		}),
	})
	if err := saveAppConfig(t, cfgPath, unpinnedActionlintConfig()); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.Install(ctx, "actionlint", "script"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	got, err := a.DB().Get(ctx, "actionlint", "script", "actionlint")
	if err != nil {
		t.Fatalf("Get actionlint: %v", err)
	}
	if got.Version.Valid {
		t.Fatalf("version = %q; want none when the release lacks the configured asset", got.Version.String)
	}
}

func TestUpgrade_UnpinnedGitHubRecipeReplacesTheRecordedRelease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mock := executor.NewMatchMock().WithFallback(executor.MockCall{})
	a, cfgPath := newImportApp(t, script.New(mock))
	a.SetGitHubFallbackAPIForTest("https://api.github.test", actionlintLatestReleaseClient(t, nil, "v1.7.12"))
	cfg := unpinnedActionlintConfig()
	spec := cfg.Tools["actionlint"]
	spec.Providers[0].Recipe.InstalledVersion = "1.7.9"
	cfg.Tools["actionlint"] = spec
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.RefreshInstalled(ctx, nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	if err := a.Upgrade(ctx, "actionlint", "script"); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	got, err := a.DB().Get(ctx, "actionlint", "script", "actionlint")
	if err != nil {
		t.Fatalf("Get actionlint: %v", err)
	}
	if got.Version.String != "1.7.12" {
		t.Fatalf("version = %q; want the release the upgrade actually pulled", got.Version.String)
	}
}

// Reinstall installs through MigrateInstallation rather than App.Install, so it needs the same recording or the tool it just replaced goes back to reporting no version.
func TestReinstall_UnpinnedGitHubRecipeRecordsResolvedRelease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mock := executor.NewMatchMock().WithFallback(executor.MockCall{})
	a, cfgPath := newImportApp(t, script.New(mock))
	a.SetGitHubFallbackAPIForTest("https://api.github.test", actionlintLatestReleaseClient(t, nil, "v1.7.12"))
	if err := saveAppConfig(t, cfgPath, unpinnedActionlintConfig()); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.RefreshInstalled(ctx, nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	if _, err := a.ReinstallWithDefault(ctx, "actionlint", "script"); err != nil {
		t.Fatalf("ReinstallWithDefault: %v", err)
	}

	got, err := a.DB().Get(ctx, "actionlint", "script", "actionlint")
	if err != nil {
		t.Fatalf("Get actionlint: %v", err)
	}
	if got.Version.String != "1.7.12" {
		t.Fatalf("version = %q; want the release the reinstall pulled", got.Version.String)
	}
}

// Reinstall used to write the resolved entry back as a bare provider row, dropping the source and recipe
// the tool was authored with and leaving it with no way to install itself again.
func TestReinstall_KeepsTheRecipeItWasAuthoredWith(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mock := executor.NewMatchMock().WithFallback(executor.MockCall{})
	a, cfgPath := newImportApp(t, script.New(mock))
	a.SetGitHubFallbackAPIForTest("https://api.github.test", actionlintLatestReleaseClient(t, nil, "v1.7.12"))
	if err := saveAppConfig(t, cfgPath, unpinnedActionlintConfig()); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.RefreshInstalled(ctx, nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	if _, err := a.ReinstallWithDefault(ctx, "actionlint", "script"); err != nil {
		t.Fatalf("ReinstallWithDefault: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	entry := cfg.Tools["actionlint"].Providers[0]
	if entry.Bin != "actionlint" {
		t.Errorf("bin = %q, want it preserved", entry.Bin)
	}
	if entry.Source == nil || entry.Source.Repo != "actionlint" {
		t.Errorf("source = %+v, want the authored GitHub source", entry.Source)
	}
	if entry.Recipe == nil || entry.Recipe.AssetPattern == "" {
		t.Errorf("recipe = %+v, want the authored release-asset recipe", entry.Recipe)
	}
	if entry.Options["install"] != "" {
		t.Errorf("options.install = %q, want the recipe left unflattened", entry.Options["install"])
	}
}
