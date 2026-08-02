package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
)

func TestConfiguredGitHubReleaseAssetUsesNativeInstaller(t *testing.T) {
	dir := t.TempDir()
	asset := []byte("#!/bin/sh\n[ \"$1\" != \"--version\" ]\n")
	digest := fmt.Sprintf("%x", sha256.Sum256(asset))
	checksumRequested := false
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/opentofu/opentofu/releases/latest", "/repos/opentofu/opentofu/releases/tags/v1.10.0":
			_, _ = fmt.Fprintf(w, `{"id":1,"tag_name":"v1.10.0","assets":[{"id":2,"name":"tofu","browser_download_url":%q},{"id":3,"name":"checksums_custom.txt","browser_download_url":%q}]}`, srv.URL+"/tofu", srv.URL+"/checksums")
		case "/tofu":
			_, _ = w.Write(asset)
		case "/checksums":
			checksumRequested = true
			_, _ = fmt.Fprintf(w, "%s  tofu\n", digest)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	a := &App{ConfigPath: filepath.Join(dir, "settings.json"), CacheDir: dir}
	a.SetGitHubFallbackAPIForTest(srv.URL, srv.Client())
	spec, err := config.MaterializeInstallSpec("opentofu", config.ToolInstallSpec{
		Provider: "script",
		Source:   &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "opentofu", Repo: "opentofu"},
		Recipe:   &config.FallbackRecipe{Type: config.FallbackRecipeGitHubReleaseAsset, AssetPattern: "tofu", ChecksumAssetPattern: "checksums_{arch}.txt"},
		Bin:      "tofu",
		BinDir:   filepath.Join(dir, "bin"),
		Options:  map[string]string{"arch_map": "amd64:custom,x86_64:custom,arm64:custom,aarch64:custom,arm:custom,armv7l:custom"},
	}, "")
	if err != nil {
		t.Fatalf("MaterializeInstallSpec: %v", err)
	}
	p := &githubReleaseAssetProvider{app: a, next: &internalProviderStub{name: "script"}}
	tool := provider.Tool{Name: "opentofu", Provider: p.Name(), Options: spec.Options}
	if err := p.Install(t.Context(), tool); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "bin", "tofu")); err != nil {
		t.Fatalf("native binary: %v", err)
	}
	if !checksumRequested {
		t.Fatal("mapped checksum manifest was not requested")
	}
	installed, version, err := p.IsInstalled(t.Context(), tool)
	if err != nil || !installed || version != "" {
		t.Fatalf("IsInstalled = %v, %q, %v; want true with version recorded after sync", installed, version, err)
	}
}

func TestConfiguredGitHubReleaseAssetProbesRecordedBinaryVersion(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "gadget"), []byte("#!/bin/sh\necho 'gadget v7.8.9'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	spec, err := config.MaterializeInstallSpec("gadget-tool", config.ToolInstallSpec{
		Source: &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "o", Repo: "r"},
		Recipe: &config.FallbackRecipe{Type: config.FallbackRecipeGitHubReleaseAsset, AssetPattern: "gadget.tar.gz"},
		Bin:    "gadget", BinDir: binDir,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	p := &githubReleaseAssetProvider{app: &App{ConfigPath: filepath.Join(dir, "settings.json"), CacheDir: dir}}
	installed, got, err := p.IsInstalled(t.Context(), provider.Tool{Name: "gadget-tool", Provider: p.Name(), Options: spec.Options})
	if err != nil || !installed || got != "7.8.9" {
		t.Fatalf("IsInstalled = %v, %q, %v", installed, got, err)
	}
}

func TestConfiguredGitHubReleaseAssetChecksumMismatchFails(t *testing.T) {
	dir := t.TempDir()
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/releases/latest", "/repos/o/r/releases/tags/v1.0.0":
			_, _ = fmt.Fprintf(w, `{"tag_name":"v1.0.0","assets":[{"name":"tool","browser_download_url":%q},{"name":"checksums.txt","browser_download_url":%q}]}`, srv.URL+"/tool", srv.URL+"/checksums")
		case "/tool":
			_, _ = w.Write([]byte("wrong bytes"))
		case "/checksums":
			_, _ = fmt.Fprintln(w, strings.Repeat("0", 64)+"  tool")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	p, tool := configuredGitHubTestProvider(t, dir, srv, config.ToolInstallSpec{
		Source: &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "o", Repo: "r"},
		Recipe: &config.FallbackRecipe{Type: config.FallbackRecipeGitHubReleaseAsset, AssetPattern: "tool", ChecksumAssetPattern: "checksums.txt"},
		Bin:    "tool", BinDir: filepath.Join(dir, "bin"),
	})
	err := p.Install(t.Context(), tool)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("Install error = %v, want checksum mismatch", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "bin", "tool")); !os.IsNotExist(err) {
		t.Fatalf("binary exists after checksum failure: %v", err)
	}
}

func TestConfiguredGitHubReleaseAssetLegacyExtractionOptions(t *testing.T) {
	dir := t.TempDir()
	asset := tarGzAsset(t, "release/bin/tofu", []byte("native archive binary"))
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/releases/latest":
			_, _ = fmt.Fprintf(w, `{"tag_name":"v1.0.0","assets":[{"name":"tofu.tar.gz","browser_download_url":%q}]}`, srv.URL+"/tofu.tar.gz")
		case "/tofu.tar.gz":
			_, _ = w.Write(asset)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	p, tool := configuredGitHubTestProvider(t, dir, srv, config.ToolInstallSpec{
		Source: &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "o", Repo: "r"},
		Recipe: &config.FallbackRecipe{Type: config.FallbackRecipeGitHubReleaseAsset, AssetPattern: "tofu.tar.gz"},
		Bin:    "tofu", BinDir: filepath.Join(dir, "bin"),
		Options: map[string]string{"extract_dir": filepath.Join(dir, "legacy"), "strip_components": "1"},
	})
	if err := p.Install(t.Context(), tool); err != nil {
		t.Fatalf("Install: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "bin", "tofu"))
	if err != nil || string(got) != "native archive binary" {
		t.Fatalf("installed binary = %q, %v", got, err)
	}
}

func TestConfiguredGitHubReleaseAssetUsesNativeAuthAndRetry(t *testing.T) {
	t.Setenv("GH_TOKEN", "configured-token")
	dir := t.TempDir()
	latestCalls := 0
	assetCalls := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer configured-token" {
			t.Errorf("Authorization = %q", got)
		}
		switch r.URL.Path {
		case "/repos/o/r/releases/latest":
			latestCalls++
			_, _ = fmt.Fprint(w, `{"tag_name":"v1.0.0","assets":[{"name":"tool","browser_download_url":"https://github.com/download/tool"}]}`)
		case "/download/tool":
			assetCalls++
			if assetCalls == 1 {
				http.Error(w, "retry", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte("native"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := *srv.Client()
	client.Transport = rewriteHostTransport{target: target, next: client.Transport}
	a := &App{ConfigPath: filepath.Join(dir, "settings.json"), CacheDir: dir}
	a.SetGitHubFallbackAPIForTest("https://api.github.com", &client)
	spec, err := config.MaterializeInstallSpec("tool", config.ToolInstallSpec{
		Source: &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "o", Repo: "r"},
		Recipe: &config.FallbackRecipe{Type: config.FallbackRecipeGitHubReleaseAsset, AssetPattern: "tool"},
		Bin:    "tool", BinDir: filepath.Join(dir, "bin"),
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	p := &githubReleaseAssetProvider{app: a}
	if err := p.Install(t.Context(), provider.Tool{Name: "tool", Provider: p.Name(), Options: spec.Options}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if latestCalls != 1 || assetCalls != 2 {
		t.Fatalf("calls = latest:%d asset:%d, want 1/2", latestCalls, assetCalls)
	}
}

func TestConfiguredGitHubReleaseAssetRejectsHTTPSDowngrade(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/releases/latest":
			_, _ = fmt.Fprint(w, `{"tag_name":"v1.0.0","assets":[{"name":"tool","browser_download_url":"https://github.com/download/tool"}]}`)
		case "/download/tool":
			http.Redirect(w, r, "http://example.com/tool", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := *srv.Client()
	client.Transport = rewriteHostTransport{target: target, next: client.Transport}
	a := &App{ConfigPath: filepath.Join(dir, "settings.json"), CacheDir: dir}
	a.SetGitHubFallbackAPIForTest("https://api.github.com", &client)
	spec, err := config.MaterializeInstallSpec("tool", config.ToolInstallSpec{
		Source: &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "o", Repo: "r"},
		Recipe: &config.FallbackRecipe{Type: config.FallbackRecipeGitHubReleaseAsset, AssetPattern: "tool"},
		Bin:    "tool", BinDir: filepath.Join(dir, "bin"),
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	p := &githubReleaseAssetProvider{app: a}
	err = p.Install(t.Context(), provider.Tool{Name: "tool", Provider: p.Name(), Options: spec.Options})
	if err == nil || !strings.Contains(err.Error(), "https to plain-http redirect") {
		t.Fatalf("Install error = %v, want redirect downgrade rejection", err)
	}
}

type rewriteHostTransport struct {
	target *url.URL
	next   http.RoundTripper
}

func (t rewriteHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = t.target.Scheme
	clone.URL.Host = t.target.Host
	clone.Host = t.target.Host
	return t.next.RoundTrip(clone)
}

func configuredGitHubTestProvider(t *testing.T, dir string, srv *httptest.Server, authored config.ToolInstallSpec) (*githubReleaseAssetProvider, provider.Tool) {
	t.Helper()
	a := &App{ConfigPath: filepath.Join(dir, "settings.json"), CacheDir: dir}
	a.SetGitHubFallbackAPIForTest(srv.URL, srv.Client())
	spec, err := config.MaterializeInstallSpec(authored.Bin, authored, "")
	if err != nil {
		t.Fatal(err)
	}
	p := &githubReleaseAssetProvider{app: a}
	return p, provider.Tool{Name: authored.Bin, Provider: p.Name(), Options: spec.Options}
}

func tarGzAsset(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(tw, bytes.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
