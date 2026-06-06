package app_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestSaveToolFallbackFromGitHub_ResolvesReleaseAssetFromGitHubAPI(t *testing.T) {
	ctx := context.Background()
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
	if fallback.Commands.Install == "" || fallback.Commands.Check == "" {
		t.Fatalf("commands = %+v, want install and check from resolver", fallback.Commands)
	}
	if fallback.Binary != "gh" {
		t.Fatalf("binary = %q, want gh", fallback.Binary)
	}
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
