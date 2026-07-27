package app_test

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider/aptrepo"
	"github.com/lkshrk/omni/internal/provider/script"
)

func TestRefreshInstalled_PinnedGitHubRecipeReportsTagAsVersion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mock := executor.NewMatchMock().WithFallback(executor.MockCall{})
	a, cfgPath := newImportApp(t, script.New(mock))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{"actionlint": {Providers: []config.ToolInstallSpec{{
			Provider: "script",
			Bin:      "actionlint",
			Options:  map[string]string{"arch_map": "amd64=amd64,arm64=arm64"},
			Source:   &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "rhysd", Repo: "actionlint"},
			Recipe: &config.FallbackRecipe{
				Type:         config.FallbackRecipeGitHubReleaseAsset,
				AssetPattern: "actionlint_{version}_linux_{arch}.tar.gz",
				TagName:      "v1.7.12",
			},
		}}}},
		Groups: []*config.GroupConfig{{Tools: groupTools("actionlint")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.RefreshInstalled(ctx, nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	got, err := a.DB().Get(ctx, "actionlint", "script", "actionlint")
	if err != nil {
		t.Fatalf("Get actionlint: %v", err)
	}
	if !got.Installed {
		t.Fatalf("installed = %v, want true", got.Installed)
	}
	if !got.Version.Valid || got.Version.String != "1.7.12" {
		t.Fatalf("version = %q valid=%v; want %q", got.Version.String, got.Version.Valid, "1.7.12")
	}
}

func TestRefreshOutdated_PinnedGitHubRecipeIsKnownNotUnknown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mock := executor.NewMatchMock().WithFallback(executor.MockCall{})
	a, cfgPath := newImportApp(t, script.New(mock))
	a.SetGitHubFallbackAPIForTest("https://api.github.test", githubFallbackLatestReleaseClient(t, nil, func() io.ReadCloser {
		t.Fatal("latest release endpoint should not be called for a pinned recipe")
		return http.NoBody
	}))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{"actionlint": {Providers: []config.ToolInstallSpec{{
			Provider: "script",
			Bin:      "actionlint",
			Source:   &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "rhysd", Repo: "actionlint"},
			Recipe: &config.FallbackRecipe{
				Type:         config.FallbackRecipeGitHubReleaseAsset,
				AssetPattern: "actionlint_{version}_linux_amd64.tar.gz",
				TagName:      "v1.7.12",
			},
		}}}},
		Groups: []*config.GroupConfig{{Tools: groupTools("actionlint")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.RefreshInstalled(ctx, nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	if err := a.RefreshOutdated(ctx, false, nil); err != nil {
		t.Fatalf("RefreshOutdated: %v", err)
	}

	got, err := a.DB().Get(ctx, "actionlint", "script", "actionlint")
	if err != nil {
		t.Fatalf("Get actionlint: %v", err)
	}
	if got.Version.String != "1.7.12" {
		t.Fatalf("version = %q; want %q", got.Version.String, "1.7.12")
	}
	if got.Outdated || got.OutdatedUnknown {
		t.Fatalf("outdated=%v unknown=%v; want both false for a pinned recipe with a known version", got.Outdated, got.OutdatedUnknown)
	}
}

func TestRefreshInstalled_RecipeInstalledVersionOutranksTag(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mock := executor.NewMatchMock().WithFallback(executor.MockCall{})
	a, cfgPath := newImportApp(t, script.New(mock))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{"actionlint": {Providers: []config.ToolInstallSpec{{
			Provider: "script",
			Bin:      "actionlint",
			Source:   &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "rhysd", Repo: "actionlint"},
			Recipe: &config.FallbackRecipe{
				Type:             config.FallbackRecipeGitHubReleaseAsset,
				AssetPattern:     "actionlint_{version}_linux_amd64.tar.gz",
				TagName:          "v1.7.12",
				InstalledVersion: "1.7.11",
			},
		}}}},
		Groups: []*config.GroupConfig{{Tools: groupTools("actionlint")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.RefreshInstalled(ctx, nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	got, err := a.DB().Get(ctx, "actionlint", "script", "actionlint")
	if err != nil {
		t.Fatalf("Get actionlint: %v", err)
	}
	if got.Version.String != "1.7.11" {
		t.Fatalf("version = %q; want %q", got.Version.String, "1.7.11")
	}
}

func TestRefreshInstalled_AptRepoRecipeReportsPackageVersion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mock := executor.NewMatchMock(
		executor.MatchRule{
			Pattern:  "dpkg-query --showformat=${Version} --show docker-ce",
			Response: executor.MockCall{Stdout: "5:28.0.1-1~debian.12~bookworm\n"},
		},
		executor.MatchRule{
			Pattern:  "dpkg-query --showformat=${Version} --show docker-ce-cli",
			Response: executor.MockCall{Stdout: "5:28.0.1-1~debian.12~bookworm\n"},
		},
	).WithFallback(executor.MockCall{})
	a, cfgPath := newImportApp(t, aptrepo.New(mock))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{"docker": {Providers: []config.ToolInstallSpec{{
			Provider: "script",
			Options: map[string]string{
				"key_url":        "https://download.docker.com/linux/debian/gpg",
				"signed_by":      "/etc/apt/keyrings/docker.asc",
				"sources_format": "deb [arch={arch} signed-by={signed_by}] https://download.docker.com/linux/debian {suite} stable",
				"packages":       "docker-ce docker-ce-cli",
			},
			Recipe: &config.FallbackRecipe{Type: config.FallbackRecipeAptRepo},
		}}}},
		Groups: []*config.GroupConfig{{Tools: groupTools("docker")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.RefreshInstalled(ctx, nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	got, err := a.DB().Get(ctx, "docker", "apt_repo", "docker")
	if err != nil {
		t.Fatalf("Get docker: %v", err)
	}
	if !got.Installed {
		t.Fatalf("installed = %v, want true", got.Installed)
	}
	if !got.Version.Valid || got.Version.String != "5:28.0.1-1~debian.12~bookworm" {
		t.Fatalf("version = %q valid=%v; want the docker-ce apt version", got.Version.String, got.Version.Valid)
	}
}
