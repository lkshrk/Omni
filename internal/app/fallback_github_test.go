package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestSaveToolFallbackFromGitHub_ResolvesReleaseAssetFromGitHubAPI(t *testing.T) {
	ctx := context.Background()
	expectedAsset := currentPlatformGitHubCLIAsset(t)
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/repos/cli/cli/releases/latest" {
			t.Fatalf("unexpected GitHub API path %q", req.URL.Path)
		}
		body, err := os.Open("testdata/github_cli_latest_release.json")
		if err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       body,
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true})
	a.SetGitHubFallbackAPIForTest("https://api.github.test", client)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("gh", "system")),
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.SaveToolFallbackFromGitHub(ctx, "gh", "cli/cli"); err != nil {
		t.Fatalf("SaveToolFallbackFromGitHub: %v", err)
	}

	got, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	fallback := got.Tools["gh"].Fallback
	if fallback == nil {
		t.Fatal("fallback missing")
	}
	if fallback.Status != config.FallbackStatusUnverified {
		t.Fatalf("fallback status = %q, want unverified resolved recipe", fallback.Status)
	}
	if fallback.Source.Owner != "cli" || fallback.Source.Repo != "cli" {
		t.Fatalf("source = %+v, want cli/cli", fallback.Source)
	}
	if fallback.Recipe.Type != config.FallbackRecipeGitHubReleaseAsset {
		t.Fatalf("recipe type = %q, want GitHub release asset", fallback.Recipe.Type)
	}
	if fallback.Recipe.AssetPattern == "" {
		t.Fatal("asset pattern empty, want matched GitHub release asset")
	}
	if fallback.Recipe.ReleaseID != "330388700" {
		t.Fatalf("release id = %q, want 330388700", fallback.Recipe.ReleaseID)
	}
	if fallback.Recipe.TagName != "v2.93.0" {
		t.Fatalf("tag name = %q, want v2.93.0", fallback.Recipe.TagName)
	}
	if fallback.Recipe.PublishedAt != "2026-05-27T17:47:41Z" {
		t.Fatalf("published_at = %q, want normalized GitHub published_at", fallback.Recipe.PublishedAt)
	}
	if fallback.Recipe.AssetID != expectedAsset.id {
		t.Fatalf("asset id = %q, want matched GitHub asset id %q", fallback.Recipe.AssetID, expectedAsset.id)
	}
	if fallback.Recipe.AssetName != expectedAsset.name {
		t.Fatalf("asset name = %q, want matched GitHub asset name %q", fallback.Recipe.AssetName, expectedAsset.name)
	}
	if fallback.Recipe.AssetDownloadURL != expectedAsset.downloadURL {
		t.Fatalf("asset download URL = %q, want matched GitHub asset URL %q", fallback.Recipe.AssetDownloadURL, expectedAsset.downloadURL)
	}
	if fallback.Commands.Install == "" || fallback.Commands.Check == "" {
		t.Fatalf("commands = %+v, want install and check from resolver", fallback.Commands)
	}
	if fallback.Binary != "gh" {
		t.Fatalf("binary = %q, want gh", fallback.Binary)
	}
}

func TestSaveToolFallbackFromGitHub_RejectsReleaseWithoutPublishedAtAndPreservesConfig(t *testing.T) {
	ctx := context.Background()
	expectedAsset := currentPlatformGitHubCLIAsset(t)
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/repos/cli/cli/releases/latest" {
			t.Fatalf("unexpected GitHub API path %q", req.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{
  "id": 330388700,
  "tag_name": "v2.93.0",
  "draft": false,
  "prerelease": false,
  "assets": [
    {
      "id": %s,
      "name": %q,
      "browser_download_url": %q
    }
  ]
}`, expectedAsset.id, expectedAsset.name, expectedAsset.downloadURL))),
			Header:  make(http.Header),
			Request: req,
		}, nil
	})}

	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true})
	a.SetGitHubFallbackAPIForTest("https://api.github.test", client)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"gh": {
				Provider: "system",
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "old", Repo: "repo"},
					Status: config.FallbackStatusUnverified,
					Binary: "gh",
					Recipe: config.FallbackRecipe{
						Type:         config.FallbackRecipeGitHubReleaseAsset,
						AssetPattern: "old-gh.zip",
					},
				},
			},
		},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	err := a.SaveToolFallbackFromGitHub(ctx, "gh", "cli/cli")
	if err == nil {
		t.Fatal("SaveToolFallbackFromGitHub err = nil, want missing published_at error")
	}
	if !strings.Contains(err.Error(), "published_at") {
		t.Fatalf("SaveToolFallbackFromGitHub err = %v, want published_at error", err)
	}

	got, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	fallback := got.Tools["gh"].Fallback
	if fallback == nil {
		t.Fatal("fallback missing after failed resolver")
	}
	if fallback.Source.Owner != "old" || fallback.Source.Repo != "repo" {
		t.Fatalf("source after failed resolver = %+v, want preserved old/repo fallback", fallback.Source)
	}
	if fallback.Recipe.AssetPattern != "old-gh.zip" {
		t.Fatalf("asset pattern after failed resolver = %q, want preserved old-gh.zip", fallback.Recipe.AssetPattern)
	}
	if fallback.Recipe.PublishedAt != "" {
		t.Fatalf("published_at after failed resolver = %q, want no new metadata saved", fallback.Recipe.PublishedAt)
	}
}

type githubReleaseAssetFixture struct {
	id          string
	name        string
	downloadURL string
}

func currentPlatformGitHubCLIAsset(t *testing.T) githubReleaseAssetFixture {
	t.Helper()
	assets := map[string]githubReleaseAssetFixture{
		"linux/amd64":   {id: "431301770", name: "gh_2.93.0_linux_amd64.tar.gz"},
		"linux/arm64":   {id: "431301780", name: "gh_2.93.0_linux_arm64.tar.gz"},
		"darwin/amd64":  {id: "431301796", name: "gh_2.93.0_macOS_amd64.zip"},
		"darwin/arm64":  {id: "431301800", name: "gh_2.93.0_macOS_arm64.zip"},
		"windows/amd64": {id: "431301821", name: "gh_2.93.0_windows_amd64.zip"},
	}
	key := runtime.GOOS + "/" + runtime.GOARCH
	asset, ok := assets[key]
	if !ok {
		t.Skipf("github_cli_latest_release fixture has no current-platform asset for %s", key)
	}
	asset.downloadURL = "https://github.com/cli/cli/releases/download/v2.93.0/" + asset.name
	return asset
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func githubNotFoundClient() *http.Client {
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Status:     "404 Not Found",
			Body:       http.NoBody,
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}
}

func TestGitHubFallbackLiveAPI_ResolvesLatestRelease(t *testing.T) {
	if testing.Short() {
		t.Skip("live GitHub API test is skipped in short mode")
	}
	if live := os.Getenv("OMNI_LIVE_GITHUB_TESTS"); live != "1" {
		t.Skip("set OMNI_LIVE_GITHUB_TESTS=1 to run live GitHub API fallback test")
	}
	ctx := context.Background()
	if status, err := liveGitHubLatestReleaseStatus(ctx, "cli", "cli"); err != nil {
		t.Skipf("live GitHub API unavailable: %v", err)
	} else if status != http.StatusOK {
		t.Skipf("live GitHub API unavailable: status %d", status)
	}
	a, cfgPath := newImportApp(t, &stubProvider{name: "system", available: true})
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("gh", "system")),
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.SaveToolFallbackFromGitHub(ctx, "gh", "cli/cli"); err != nil {
		t.Fatalf("SaveToolFallbackFromGitHub live API: %v", err)
	}

	got, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	fallback := got.Tools["gh"].Fallback
	if fallback == nil {
		t.Fatal("fallback missing")
	}
	if fallback.Source.Owner != "cli" || fallback.Source.Repo != "cli" {
		t.Fatalf("source = %+v, want cli/cli", fallback.Source)
	}
	if fallback.Status == config.FallbackStatusUnresolved {
		encoded, _ := json.MarshalIndent(fallback, "", "  ")
		t.Fatalf("live fallback unresolved, want resolved release recipe: %s", encoded)
	}
}

func liveGitHubLatestReleaseStatus(ctx context.Context, owner, repo string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+owner+"/"+repo+"/releases/latest", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "omni-tests")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close() //nolint:errcheck
	return resp.StatusCode, nil
}
