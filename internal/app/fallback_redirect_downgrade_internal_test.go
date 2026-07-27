package app

import (
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

// An https origin answering 302 with an http Location. The scheme check on the download URL only ever
// saw the first hop, so this is the shape that walked the attacker payload past it.
func downgradingTLSOrigin(t *testing.T, plainURL string) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plainURL+"/payload", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestNativeGitHubInstallPipeline_RefusesRedirectToPlainHTTP(t *testing.T) {
	t.Parallel()
	attacker, requests := plainHTTPAttackerServer(t)
	origin := downgradingTLSOrigin(t, attacker.URL)

	dir := t.TempDir()
	a := &App{ConfigPath: filepath.Join(dir, "settings.json"), CacheDir: dir}
	a.SetGitHubFallbackAPIForTest(origin.URL, origin.Client())

	fallback := &config.FallbackSpec{
		Binary: "tool",
		Recipe: config.FallbackRecipe{
			Type:             config.FallbackRecipeGitHubReleaseAsset,
			AssetName:        "tool",
			AssetDownloadURL: origin.URL + "/asset/tool",
		},
	}

	err := a.nativeGitHubInstallPipeline(t.Context(), "tool", fallback)

	binPath := filepath.Join(dir, "fallback", "bin", "tool")
	if n := requests.Load(); n != 0 {
		installed, _ := os.ReadFile(binPath)
		t.Fatalf("plain-http redirect target was fetched %d time(s); installed binary = %q", n, installed)
	}
	if err == nil {
		t.Fatal("nativeGitHubInstallPipeline followed an https to plain-http redirect")
	}
	if !errors.Is(err, errRedirectSchemeDowngrade) {
		t.Errorf("error = %v, want it to wrap errRedirectSchemeDowngrade", err)
	}
	if installed, readErr := os.ReadFile(binPath); readErr == nil {
		t.Errorf("binary was installed from a downgraded redirect: %q", installed)
	}
}

// The seam that lets a test point the App at a stub supplies a transport, not a fetch policy. If an
// injected client could arrive without the guard, every https assertion made through this seam would
// be describing a client the guard never ran on.
func TestGitHubHTTPClient_InjectedClientCannotDropTheRedirectGuard(t *testing.T) {
	t.Parallel()
	attacker, requests := plainHTTPAttackerServer(t)
	origin := downgradingTLSOrigin(t, attacker.URL)

	a := &App{}
	a.SetGitHubFallbackAPIForTest(origin.URL, origin.Client())

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, origin.URL+"/start", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := a.githubHTTPClient().Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if n := requests.Load(); n != 0 {
		t.Fatalf("plain-http redirect target was fetched %d time(s) through an injected client", n)
	}
	if err == nil {
		t.Fatal("injected client followed an https to plain-http redirect")
	}
	if !errors.Is(err, errRedirectSchemeDowngrade) {
		t.Errorf("error = %v, want it to wrap errRedirectSchemeDowngrade", err)
	}
}

// An http origin staying on http is a loopback stub, not a downgrade, and must keep working: narrowing
// the guard to "every hop is https" would silently retire the Authorization-stripping coverage below it.
func TestGitHubHTTPClient_AllowsPlainHTTPChainWithoutDowngrade(t *testing.T) {
	t.Parallel()
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(final.Close)
	hop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL+"/resource", http.StatusFound)
	}))
	t.Cleanup(hop.Close)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, hop.URL+"/start", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&App{}).githubHTTPClient().Do(req)
	if err != nil {
		t.Fatalf("plain-http chain with no downgrade was refused: %v", err)
	}
	_ = resp.Body.Close()
}

// Executes the command omni actually persists. curl's default --proto-redir permits http on a redirect,
// so without the flag the attacker body lands on disk under the https URL the guard approved.
func TestGeneratedCurlCommand_RefusesRedirectToPlainHTTP(t *testing.T) {
	t.Parallel()
	curlPath, err := exec.LookPath("curl")
	if err != nil {
		t.Skipf("curl not available: %v", err)
	}

	attacker, requests := plainHTTPAttackerServer(t)
	origin := downgradingTLSOrigin(t, attacker.URL)

	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	caBundle := filepath.Join(dir, "origin.pem")
	writeTestCABundle(t, caBundle, origin)

	spec := config.ToolInstallSpec{
		Bin: "tool",
		Source: &config.FallbackSource{
			Type:  config.FallbackSourceGitHub,
			Owner: "owner",
			Repo:  "repo",
		},
		Recipe: &config.FallbackRecipe{
			Type:             config.FallbackRecipeGitHubReleaseAsset,
			AssetPattern:     "tool",
			AssetDownloadURL: origin.URL + "/tool",
		},
	}
	materialized, err := config.MaterializeInstallSpec("tool", spec, binDir)
	if err != nil {
		t.Fatalf("MaterializeInstallSpec: %v", err)
	}
	install := materialized.Options["install"]
	installed := filepath.Join(binDir, "tool")

	guarded := runInstallScript(t, install, caBundle)
	if n := requests.Load(); n != 0 {
		body, _ := os.ReadFile(installed)
		t.Fatalf("plain-http redirect target was fetched %d time(s) by the generated command; wrote %q", n, body)
	}
	if body, readErr := os.ReadFile(installed); readErr == nil && strings.Contains(string(body), attackerPayload) {
		t.Fatalf("attacker payload was installed at %s", installed)
	}
	if guarded == nil {
		t.Fatal("generated curl command followed an https to plain-http redirect")
	}

	// Control: the same command minus the flag reaches the attacker, so the zero count above is the guard doing its job and not a stub that was never live.
	unguarded := strings.ReplaceAll(install, " --proto-redir =https", "")
	if unguarded == install {
		t.Fatalf("generated install command has no redirect protocol guard to strip: %q", install)
	}
	if runErr := runInstallScript(t, unguarded, caBundle); runErr != nil {
		t.Fatalf("control run without the flag failed for an unrelated reason: %v", runErr)
	}
	if requests.Load() == 0 {
		t.Fatalf("control run never reached the plain-http server, so this test proves nothing (curl %s)", curlPath)
	}
}

// curl reads CURL_CA_BUNDLE, so the test server's cert can be trusted without the generated command carrying an -k or --cacert it would never carry in production.
func writeTestCABundle(t *testing.T, path string, srv *httptest.Server) {
	t.Helper()
	cert := srv.Certificate()
	if cert == nil {
		t.Fatal("TLS test server exposed no certificate")
	}
	encoded := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

func runInstallScript(t *testing.T, script, caBundle string) error {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "bash", "-c", script)
	cmd.Env = append(os.Environ(), "CURL_CA_BUNDLE="+caBundle)
	out, err := cmd.CombinedOutput()
	t.Logf("install script exit=%v output=%q", err, out)
	return err
}
