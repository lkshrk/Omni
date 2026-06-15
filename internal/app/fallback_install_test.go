package app_test

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ulikunitz/xz"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

// stubGitHubServer serves a minimal fake GitHub API + asset + checksums.
// Each test configures its own asset content and expected behaviour.
type stubGitHubServer struct {
	assetName    string
	assetContent []byte
	// checksumOverride: when set, served instead of real digest (triggers mismatch)
	checksumOverride string
	// omitChecksums: serve no checksums asset in the release
	omitChecksums bool
	// assetStatusCode: non-zero overrides the asset download response code
	assetStatusCode int
}

func (s *stubGitHubServer) serve(t *testing.T) *httptest.Server {
	t.Helper()

	digest := sha256Hex(s.assetContent)
	checkContent := digest + "  " + s.assetName + "\n"
	if s.checksumOverride != "" {
		checkContent = s.checksumOverride + "  " + s.assetName + "\n"
	}

	checkAssetName := "checksums.txt"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/tags/v1.0.0"):
			// Release by tag — used by checksum fetcher.
			assets := []map[string]any{
				{"id": 1, "name": s.assetName, "browser_download_url": "http://" + r.Host + "/asset/" + s.assetName},
			}
			if !s.omitChecksums {
				assets = append(assets, map[string]any{
					"id":                   2,
					"name":                 checkAssetName,
					"browser_download_url": "http://" + r.Host + "/checksums",
				})
			}
			writeJSON(w, map[string]any{
				"id":       1,
				"tag_name": "v1.0.0",
				"assets":   assets,
			})

		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			// Latest release — used by SaveToolFallbackFromGitHub resolver.
			assets := []map[string]any{
				{"id": 1, "name": s.assetName, "browser_download_url": "http://" + r.Host + "/asset/" + s.assetName},
			}
			writeJSON(w, map[string]any{
				"id":           1,
				"tag_name":     "v1.0.0",
				"published_at": "2026-06-01T00:00:00Z",
				"assets":       assets,
			})

		case strings.HasPrefix(r.URL.Path, "/asset/"):
			if s.assetStatusCode != 0 {
				http.Error(w, "error", s.assetStatusCode)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write(s.assetContent) //nolint:errcheck

		case r.URL.Path == "/checksums":
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte(checkContent)) //nolint:errcheck

		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// buildTarGz builds an in-memory tar.gz archive containing one file.
func buildTarGz(t *testing.T, entryName string, content []byte) []byte {
	t.Helper()
	var buf strings.Builder
	gw := gzip.NewWriter(&stringWriter{&buf})
	tw := tar.NewWriter(gw)
	tw.WriteHeader(&tar.Header{Name: entryName, Mode: 0o755, Size: int64(len(content))}) //nolint:errcheck
	tw.Write(content)                                                                    //nolint:errcheck
	tw.Close()                                                                           //nolint:errcheck
	gw.Close()                                                                           //nolint:errcheck
	return []byte(buf.String())
}

// buildTarXz builds an in-memory tar.xz archive containing one file.
func buildTarXz(t *testing.T, entryName string, content []byte) []byte {
	t.Helper()
	pr, pw := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		xw, err := xz.NewWriter(pw)
		if err != nil {
			pw.CloseWithError(err)
			errCh <- err
			return
		}
		tw := tar.NewWriter(xw)
		tw.WriteHeader(&tar.Header{Name: entryName, Mode: 0o755, Size: int64(len(content))}) //nolint:errcheck
		tw.Write(content)                                                                    //nolint:errcheck
		tw.Close()                                                                           //nolint:errcheck
		xw.Close()                                                                           //nolint:errcheck
		pw.Close()
		errCh <- nil
	}()
	b, err := io.ReadAll(pr)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("build tar.xz: %v", err)
	}
	return b
}

// buildZip builds an in-memory zip archive containing one file.
func buildZip(t *testing.T, entryName string, content []byte) []byte {
	t.Helper()
	var sw stringWriter
	zw := zip.NewWriter(&sw)
	w, err := zw.Create(entryName)
	if err != nil {
		t.Fatal(err)
	}
	w.Write(content) //nolint:errcheck
	zw.Close()       //nolint:errcheck
	return []byte(sw.buf.String())
}

type stringWriter struct{ buf *strings.Builder }

func (sw *stringWriter) Write(p []byte) (int, error) { return sw.buf.Write(p) }

func newStringWriter() *stringWriter { return &stringWriter{buf: &strings.Builder{}} }

func init() {
	// Ensure the zero value is usable by tests that use buildTarGz/buildZip directly.
	_ = newStringWriter
}

// --- full pipeline tests ---

func TestNativeInstallPipeline_TarGz(t *testing.T) {
	binaryContent := []byte("#!/bin/sh\nexit 0\n")
	assetContent := buildTarGz(t, "tool_v1.0/mytool", binaryContent)

	srv := (&stubGitHubServer{
		assetName:    "tool_v1.0_darwin_arm64.tar.gz",
		assetContent: assetContent,
	}).serve(t)

	a, cfgPath := newImportApp(t, &stubProvider{name: "apt", available: true})
	a.CacheDir = t.TempDir()
	a.SetGitHubFallbackAPIForTest(srv.URL, srv.Client())

	fallbackSpec := githubReleaseAssetFallback(srv.URL, "tool_v1.0_darwin_arm64.tar.gz", "mytool")
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"mytool": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback:  &fallbackSpec,
			},
		},
	}); err != nil {
		t.Fatalf("saveAppConfig: %v", err)
	}

	if err := a.InstallToolFallback(context.Background(), "mytool"); err != nil {
		t.Fatalf("InstallToolFallback: %v", err)
	}

	assertBinaryInstalled(t, a, fallbackSpec, "mytool", binaryContent)
	assertFallbackStatusVerified(t, cfgPath, "mytool")
}

func TestNativeInstallPipeline_TarXz(t *testing.T) {
	binaryContent := []byte("#!/bin/sh\nexit 0\n")
	assetContent := buildTarXz(t, "tool_v1.0/mytool", binaryContent)

	srv := (&stubGitHubServer{
		assetName:    "tool_v1.0_darwin_arm64.tar.xz",
		assetContent: assetContent,
	}).serve(t)

	a, cfgPath := newImportApp(t, &stubProvider{name: "apt", available: true})
	a.CacheDir = t.TempDir()
	a.SetGitHubFallbackAPIForTest(srv.URL, srv.Client())

	fallbackSpec := githubReleaseAssetFallback(srv.URL, "tool_v1.0_darwin_arm64.tar.xz", "mytool")
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"mytool": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback:  &fallbackSpec,
			},
		},
	}); err != nil {
		t.Fatalf("saveAppConfig: %v", err)
	}

	if err := a.InstallToolFallback(context.Background(), "mytool"); err != nil {
		t.Fatalf("InstallToolFallback tar.xz: %v", err)
	}

	assertBinaryInstalled(t, a, fallbackSpec, "mytool", binaryContent)
	assertFallbackStatusVerified(t, cfgPath, "mytool")
}

func TestNativeInstallPipeline_Zip(t *testing.T) {
	binaryContent := []byte("#!/bin/sh\nexit 0\n")
	sw := newStringWriter()
	zw := zip.NewWriter(sw)
	w, _ := zw.Create("mytool_v1.0/mytool")
	w.Write(binaryContent) //nolint:errcheck
	zw.Close()             //nolint:errcheck
	assetContent := []byte(sw.buf.String())

	srv := (&stubGitHubServer{
		assetName:    "tool_v1.0_darwin_arm64.zip",
		assetContent: assetContent,
	}).serve(t)

	a, cfgPath := newImportApp(t, &stubProvider{name: "apt", available: true})
	a.CacheDir = t.TempDir()
	a.SetGitHubFallbackAPIForTest(srv.URL, srv.Client())

	fallbackSpec := githubReleaseAssetFallback(srv.URL, "tool_v1.0_darwin_arm64.zip", "mytool")
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"mytool": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback:  &fallbackSpec,
			},
		},
	}); err != nil {
		t.Fatalf("saveAppConfig: %v", err)
	}

	if err := a.InstallToolFallback(context.Background(), "mytool"); err != nil {
		t.Fatalf("InstallToolFallback zip: %v", err)
	}

	assertBinaryInstalled(t, a, fallbackSpec, "mytool", binaryContent)
}

func TestNativeInstallPipeline_RawBinary(t *testing.T) {
	binaryContent := []byte("ELF binary data here")

	srv := (&stubGitHubServer{
		assetName:    "mytool_v1.0_darwin_arm64",
		assetContent: binaryContent,
	}).serve(t)

	a, cfgPath := newImportApp(t, &stubProvider{name: "apt", available: true})
	a.CacheDir = t.TempDir()
	a.SetGitHubFallbackAPIForTest(srv.URL, srv.Client())

	fallbackSpec := githubReleaseAssetFallback(srv.URL, "mytool_v1.0_darwin_arm64", "mytool")
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"mytool": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback:  &fallbackSpec,
			},
		},
	}); err != nil {
		t.Fatalf("saveAppConfig: %v", err)
	}

	if err := a.InstallToolFallback(context.Background(), "mytool"); err != nil {
		t.Fatalf("InstallToolFallback raw: %v", err)
	}

	assertBinaryInstalled(t, a, fallbackSpec, "mytool", binaryContent)
}

func TestNativeInstallPipeline_ChecksumMatch_Persisted(t *testing.T) {
	binaryContent := []byte("binary content")
	assetContent := buildTarGz(t, "mytool", binaryContent)

	srv := (&stubGitHubServer{
		assetName:    "tool_darwin_arm64.tar.gz",
		assetContent: assetContent,
	}).serve(t)

	a, cfgPath := newImportApp(t, &stubProvider{name: "apt", available: true})
	a.CacheDir = t.TempDir()
	a.SetGitHubFallbackAPIForTest(srv.URL, srv.Client())

	fallbackSpec := githubReleaseAssetFallback(srv.URL, "tool_darwin_arm64.tar.gz", "mytool")
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"mytool": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback:  &fallbackSpec,
			},
		},
	}); err != nil {
		t.Fatalf("saveAppConfig: %v", err)
	}

	if err := a.InstallToolFallback(context.Background(), "mytool"); err != nil {
		t.Fatalf("InstallToolFallback: %v", err)
	}

	// Verify the checksum was persisted into the recipe.
	got, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	persisted := got.Tools["mytool"].Fallback.Recipe.Checksum
	expected := sha256Hex(assetContent)
	if persisted != expected {
		t.Errorf("persisted checksum = %q, want %q", persisted, expected)
	}
}

func TestNativeInstallPipeline_ChecksumMismatch_HardFail(t *testing.T) {
	binaryContent := []byte("binary content")
	assetContent := buildTarGz(t, "mytool", binaryContent)

	srv := (&stubGitHubServer{
		assetName:        "tool_darwin_arm64.tar.gz",
		assetContent:     assetContent,
		checksumOverride: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
	}).serve(t)

	a, cfgPath := newImportApp(t, &stubProvider{name: "apt", available: true})
	a.CacheDir = t.TempDir()
	a.SetGitHubFallbackAPIForTest(srv.URL, srv.Client())

	fallbackSpec := githubReleaseAssetFallback(srv.URL, "tool_darwin_arm64.tar.gz", "mytool")
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"mytool": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback:  &fallbackSpec,
			},
		},
	}); err != nil {
		t.Fatalf("saveAppConfig: %v", err)
	}

	err := a.InstallToolFallback(context.Background(), "mytool")
	if err == nil {
		t.Fatal("expected checksum mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("error %q does not mention checksum mismatch", err)
	}

	// Status must be recorded as failed.
	assertFallbackStatusFailed(t, cfgPath, "mytool")
}

func TestNativeInstallPipeline_ChecksumsAbsent_Proceeds(t *testing.T) {
	binaryContent := []byte("binary content")
	assetContent := buildTarGz(t, "mytool", binaryContent)

	srv := (&stubGitHubServer{
		assetName:     "tool_darwin_arm64.tar.gz",
		assetContent:  assetContent,
		omitChecksums: true,
	}).serve(t)

	a, cfgPath := newImportApp(t, &stubProvider{name: "apt", available: true})
	a.CacheDir = t.TempDir()
	a.SetGitHubFallbackAPIForTest(srv.URL, srv.Client())

	fallbackSpec := githubReleaseAssetFallback(srv.URL, "tool_darwin_arm64.tar.gz", "mytool")
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"mytool": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback:  &fallbackSpec,
			},
		},
	}); err != nil {
		t.Fatalf("saveAppConfig: %v", err)
	}

	// Must succeed even without a checksums asset (best-effort).
	if err := a.InstallToolFallback(context.Background(), "mytool"); err != nil {
		t.Fatalf("InstallToolFallback without checksums: %v", err)
	}
	assertFallbackStatusVerified(t, cfgPath, "mytool")
}

func TestNativeInstallPipeline_DownloadFailure_SetsStatusFailed(t *testing.T) {
	srv := (&stubGitHubServer{
		assetName:       "tool_darwin_arm64.tar.gz",
		assetContent:    []byte("data"),
		assetStatusCode: http.StatusInternalServerError,
	}).serve(t)

	a, cfgPath := newImportApp(t, &stubProvider{name: "apt", available: true})
	a.CacheDir = t.TempDir()
	a.SetGitHubFallbackAPIForTest(srv.URL, srv.Client())

	fallbackSpec := githubReleaseAssetFallback(srv.URL, "tool_darwin_arm64.tar.gz", "mytool")
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"mytool": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback:  &fallbackSpec,
			},
		},
	}); err != nil {
		t.Fatalf("saveAppConfig: %v", err)
	}

	err := a.InstallToolFallback(context.Background(), "mytool")
	if err == nil {
		t.Fatal("expected download failure error, got nil")
	}
	assertFallbackStatusFailed(t, cfgPath, "mytool")
}

func TestNativeInstallPipeline_NativeUninstall(t *testing.T) {
	binaryContent := []byte("#!/bin/sh\nexit 0\n")
	assetContent := buildTarGz(t, "mytool", binaryContent)

	srv := (&stubGitHubServer{
		assetName:    "tool_darwin_arm64.tar.gz",
		assetContent: assetContent,
	}).serve(t)

	a, cfgPath := newImportApp(t, &stubProvider{name: "apt", available: true})
	a.CacheDir = t.TempDir()
	a.SetGitHubFallbackAPIForTest(srv.URL, srv.Client())

	fallbackSpec := githubReleaseAssetFallback(srv.URL, "tool_darwin_arm64.tar.gz", "mytool")
	// No explicit Uninstall command — native path should be used.
	fallbackSpec.Commands.Uninstall = ""
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"mytool": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback:  &fallbackSpec,
			},
		},
	}); err != nil {
		t.Fatalf("saveAppConfig: %v", err)
	}

	if err := a.InstallToolFallback(context.Background(), "mytool"); err != nil {
		t.Fatalf("InstallToolFallback: %v", err)
	}

	// Confirm the binary exists before uninstall.
	cacheDir, _ := a.FallbackCacheDir()
	binDir := filepath.Join(cacheDir, "bin")
	binPath := filepath.Join(binDir, "mytool")
	if _, err := os.Stat(binPath); err != nil {
		t.Fatalf("binary not found before uninstall: %v", err)
	}

	if err := a.UninstallToolFallback(context.Background(), "mytool"); err != nil {
		t.Fatalf("UninstallToolFallback: %v", err)
	}

	if _, err := os.Stat(binPath); !os.IsNotExist(err) {
		t.Errorf("binary still exists after uninstall")
	}
}

// TestNativeInstallPipeline_TransientFailureRetried verifies the download retry
// path: the stub returns 503 on the first asset request and 200 on the second.
// The install must succeed — the retry absorbed the transient failure.
func TestNativeInstallPipeline_TransientFailureRetried(t *testing.T) {
	binaryContent := []byte("#!/bin/sh\nexit 0\n")
	assetContent := buildTarGz(t, "mytool", binaryContent)

	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/tags/v1.0.0"),
			strings.HasSuffix(r.URL.Path, "/releases/latest"):
			writeJSON(w, map[string]any{
				"id": 1, "tag_name": "v1.0.0", "published_at": "2026-06-01T00:00:00Z",
				"assets": []map[string]any{
					{"id": 1, "name": "tool.tar.gz", "browser_download_url": "http://" + r.Host + "/asset/tool.tar.gz"},
				},
			})
		case strings.HasPrefix(r.URL.Path, "/asset/"):
			// First call returns 503 (transient); subsequent calls succeed.
			attempts++
			if attempts == 1 {
				http.Error(w, "service unavailable", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write(assetContent) //nolint:errcheck
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	a, cfgPath := newImportApp(t, &stubProvider{name: "apt", available: true})
	a.CacheDir = t.TempDir()
	a.SetGitHubFallbackAPIForTest(srv.URL, srv.Client())

	fallbackSpec := githubReleaseAssetFallback(srv.URL, "tool.tar.gz", "mytool")
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"mytool": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback:  &fallbackSpec,
			},
		},
	}); err != nil {
		t.Fatalf("saveAppConfig: %v", err)
	}

	if err := a.InstallToolFallback(context.Background(), "mytool"); err != nil {
		t.Fatalf("InstallToolFallback after retry: %v", err)
	}
	if attempts < 2 {
		t.Errorf("asset endpoint called %d times, want ≥2 (retry)", attempts)
	}
	assertBinaryInstalled(t, a, fallbackSpec, "mytool", binaryContent)
	assertFallbackStatusVerified(t, cfgPath, "mytool")
}

// TestNativeInstallPipeline_NonRetriable404NeverRetried verifies that a 404
// response is not retried — the endpoint must be called exactly once.
func TestNativeInstallPipeline_NonRetriable404NeverRetried(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/tags/v1.0.0"),
			strings.HasSuffix(r.URL.Path, "/releases/latest"):
			writeJSON(w, map[string]any{
				"id": 1, "tag_name": "v1.0.0", "published_at": "2026-06-01T00:00:00Z",
				"assets": []map[string]any{
					{"id": 1, "name": "tool.tar.gz", "browser_download_url": "http://" + r.Host + "/asset/tool.tar.gz"},
				},
			})
		case strings.HasPrefix(r.URL.Path, "/asset/"):
			attempts++
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	a, cfgPath := newImportApp(t, &stubProvider{name: "apt", available: true})
	a.CacheDir = t.TempDir()
	a.SetGitHubFallbackAPIForTest(srv.URL, srv.Client())

	fallbackSpec := githubReleaseAssetFallback(srv.URL, "tool.tar.gz", "mytool")
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"mytool": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback:  &fallbackSpec,
			},
		},
	}); err != nil {
		t.Fatalf("saveAppConfig: %v", err)
	}

	err := a.InstallToolFallback(context.Background(), "mytool")
	if err == nil {
		t.Fatal("expected error for 404, got nil")
	}
	if attempts != 1 {
		t.Errorf("asset endpoint called %d times, want exactly 1 (no retry on 404)", attempts)
	}
	assertFallbackStatusFailed(t, cfgPath, "mytool")
}

// --- helpers ---

// githubReleaseAssetFallback builds a minimal FallbackSpec for a GitHub
// release asset recipe pointing at srvURL.
func githubReleaseAssetFallback(srvURL, assetName, binary string) config.FallbackSpec {
	return config.FallbackSpec{
		Source: config.FallbackSource{
			Type:  config.FallbackSourceGitHub,
			Owner: "testowner",
			Repo:  "testrepo",
			URL:   "https://github.com/testowner/testrepo",
		},
		Status: config.FallbackStatusUnverified,
		Binary: binary,
		Recipe: config.FallbackRecipe{
			Type:             config.FallbackRecipeGitHubReleaseAsset,
			ReleaseID:        "1",
			TagName:          "v1.0.0",
			PublishedAt:      "2026-06-01T00:00:00Z",
			AssetID:          "1",
			AssetName:        assetName,
			AssetDownloadURL: fmt.Sprintf("%s/asset/%s", srvURL, assetName),
		},
		Commands: config.FallbackCommands{
			// Check uses native path (test-overridable); schema requires a non-empty Check.
			Check: `test -x {{bin_dir}}/{{binary}}`,
		},
	}
}

func assertBinaryInstalled(t *testing.T, a *app.App, fallback config.FallbackSpec, binary string, wantContent []byte) {
	t.Helper()
	cacheDir, err := a.FallbackCacheDir()
	if err != nil {
		t.Fatalf("FallbackCacheDir: %v", err)
	}
	binDir := filepath.Join(cacheDir, "bin")
	if fallback.BinDir != "" {
		binDir = fallback.BinDir
	}
	binPath := filepath.Join(binDir, binary)
	got, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("binary not installed at %s: %v", binPath, err)
	}
	if string(got) != string(wantContent) {
		t.Errorf("binary content = %q, want %q", got, wantContent)
	}
	info, _ := os.Stat(binPath)
	if info.Mode()&0o111 == 0 {
		t.Error("installed binary is not executable")
	}
}

func assertFallbackStatusVerified(t *testing.T, cfgPath, toolName string) {
	t.Helper()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.Tools[toolName].Fallback == nil {
		t.Fatal("fallback is nil")
	}
	if s := cfg.Tools[toolName].Fallback.Status; s != config.FallbackStatusVerified {
		t.Errorf("fallback status = %q, want verified", s)
	}
}

func assertFallbackStatusFailed(t *testing.T, cfgPath, toolName string) {
	t.Helper()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.Tools[toolName].Fallback == nil {
		t.Fatal("fallback is nil")
	}
	if s := cfg.Tools[toolName].Fallback.Status; s != config.FallbackStatusFailed {
		t.Errorf("fallback status = %q, want failed", s)
	}
}

func TestInstallToolFallback_PersistsInstalledVersion(t *testing.T) {
	binaryContent := []byte("#!/bin/sh\nexit 0\n")
	assetContent := buildTarGz(t, "tool_v1.0/mytool", binaryContent)

	srv := (&stubGitHubServer{
		assetName:    "tool_v1.0_darwin_arm64.tar.gz",
		assetContent: assetContent,
	}).serve(t)

	a, cfgPath := newImportApp(t, &stubProvider{name: "apt", available: true})
	a.CacheDir = t.TempDir()
	a.SetGitHubFallbackAPIForTest(srv.URL, srv.Client())

	fallbackSpec := githubReleaseAssetFallback(srv.URL, "tool_v1.0_darwin_arm64.tar.gz", "mytool")
	// TagName is set to v1.0.0 so it gets persisted as InstalledVersion.
	fallbackSpec.Recipe.TagName = "v1.0.0"
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"mytool": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback:  &fallbackSpec,
			},
		},
	}); err != nil {
		t.Fatalf("saveAppConfig: %v", err)
	}

	if err := a.InstallToolFallback(context.Background(), "mytool"); err != nil {
		t.Fatalf("InstallToolFallback: %v", err)
	}

	assertFallbackStatusVerified(t, cfgPath, "mytool")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	got := cfg.Tools["mytool"].Fallback.Recipe.InstalledVersion
	if got != "1.0.0" {
		t.Errorf("InstalledVersion = %q, want %q", got, "1.0.0")
	}
}

func TestInstallToolFallback_NoTagName_InstalledVersionEmpty(t *testing.T) {
	binaryContent := []byte("#!/bin/sh\nexit 0\n")
	assetContent := buildTarGz(t, "tool_v1.0/mytool", binaryContent)

	srv := (&stubGitHubServer{
		assetName:    "tool_v1.0_darwin_arm64.tar.gz",
		assetContent: assetContent,
	}).serve(t)

	a, cfgPath := newImportApp(t, &stubProvider{name: "apt", available: true})
	a.CacheDir = t.TempDir()
	a.SetGitHubFallbackAPIForTest(srv.URL, srv.Client())

	fallbackSpec := githubReleaseAssetFallback(srv.URL, "tool_v1.0_darwin_arm64.tar.gz", "mytool")
	fallbackSpec.Recipe.TagName = "" // no tag → InstalledVersion stays empty
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"mytool": {
				Providers: []config.ToolInstallSpec{{Provider: "apt"}},
				Fallback:  &fallbackSpec,
			},
		},
	}); err != nil {
		t.Fatalf("saveAppConfig: %v", err)
	}

	if err := a.InstallToolFallback(context.Background(), "mytool"); err != nil {
		t.Fatalf("InstallToolFallback: %v", err)
	}

	assertFallbackStatusVerified(t, cfgPath, "mytool")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	got := cfg.Tools["mytool"].Fallback.Recipe.InstalledVersion
	if got != "" {
		t.Errorf("InstalledVersion = %q, want empty when no TagName", got)
	}
}
