package app

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ulikunitz/xz"

	"github.com/lkshrk/omni/internal/config"
)

// --- isChecksumAsset ---

func TestIsChecksumAsset(t *testing.T) {
	t.Parallel()
	yes := []string{
		"sha256sums",
		"SHA256SUMS",
		"sha256sums.txt",
		"SHA256SUMS.TXT",
		"checksums.txt",
		"fd_checksums.txt",
		"fd-checksums.txt",
		"tool_v1.0.0_checksums.txt",
		"binary.sha256sum",
		"binary.sha256sums",
	}
	no := []string{
		"fd.tar.gz",
		"fd.zip",
		"fd.tar.xz",
		"fd.sig",
		"fd.asc",
		"fd_README.md",
	}
	for _, name := range yes {
		if !isChecksumAsset(strings.ToLower(name)) {
			t.Errorf("isChecksumAsset(%q) = false, want true", name)
		}
	}
	for _, name := range no {
		if isChecksumAsset(strings.ToLower(name)) {
			t.Errorf("isChecksumAsset(%q) = true, want false", name)
		}
	}
}

// --- extractChecksumForFile ---

func TestExtractChecksumForFile_MatchesFilename(t *testing.T) {
	t.Parallel()
	content := "abc123  fd_1.0_darwin_arm64.tar.gz\ndef456  fd_1.0_linux_amd64.tar.gz\n"
	got, err := extractChecksumForFile(strings.NewReader(content), "fd_1.0_darwin_arm64.tar.gz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "abc123" {
		t.Errorf("got %q, want abc123", got)
	}
}

func TestExtractChecksumForFile_StripsLeadingPath(t *testing.T) {
	t.Parallel()
	content := "abc123  ./dist/fd_1.0_darwin_arm64.tar.gz\n"
	got, err := extractChecksumForFile(strings.NewReader(content), "fd_1.0_darwin_arm64.tar.gz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "abc123" {
		t.Errorf("got %q, want abc123", got)
	}
}

func TestExtractChecksumForFile_NotFound(t *testing.T) {
	t.Parallel()
	content := "abc123  other_file.tar.gz\n"
	_, err := extractChecksumForFile(strings.NewReader(content), "fd_1.0_darwin_arm64.tar.gz")
	if err == nil {
		t.Fatal("expected error for missing entry, got nil")
	}
}

func TestExtractChecksumForFile_SkipsComments(t *testing.T) {
	t.Parallel()
	content := "# comment\nabc123  target.tar.gz\n"
	got, err := extractChecksumForFile(strings.NewReader(content), "target.tar.gz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "abc123" {
		t.Errorf("got %q, want abc123", got)
	}
}

// --- verifyFileChecksum ---

func TestVerifyFileChecksum_Match(t *testing.T) {
	t.Parallel()
	content := []byte("hello world")
	sum := sha256.Sum256(content)
	expected := hex.EncodeToString(sum[:])

	f, err := os.CreateTemp(t.TempDir(), "checkfile-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(content); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if err := verifyFileChecksum(f.Name(), expected, "test"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVerifyFileChecksum_Mismatch(t *testing.T) {
	t.Parallel()
	f, err := os.CreateTemp(t.TempDir(), "checkfile-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("real content")); err != nil {
		t.Fatal(err)
	}
	f.Close()

	err = verifyFileChecksum(f.Name(), "deadbeef", "test")
	if err == nil {
		t.Fatal("expected mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("error %q does not mention checksum mismatch", err)
	}
}

// --- extractAndInstall: zip ---

func TestExtractAndInstall_Zip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := []byte("#!/bin/sh\nexit 0\n")
	archivePath := filepath.Join(dir, "tool_v1.0_darwin_arm64.zip")

	// Build a zip with a nested path.
	zf, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	w, err := zw.Create("tool_v1.0_darwin_arm64/mytool")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	zf.Close()

	destPath := filepath.Join(dir, "mytool")
	if err := extractAndInstall(archivePath, "tool_v1.0_darwin_arm64.zip", "mytool", "", destPath); err != nil {
		t.Fatalf("extractAndInstall: %v", err)
	}
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Errorf("installed content = %q, want %q", got, content)
	}
	info, err := os.Stat(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Error("installed binary is not executable")
	}
}

func TestExtractAndInstall_ZipExactPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := []byte("correct binary")
	wrong := []byte("wrong binary")
	archivePath := filepath.Join(dir, "archive.zip")

	zf, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	// Write a file with the same base name but different path.
	w1, _ := zw.Create("other/mytool")
	w1.Write(wrong) //nolint:errcheck
	// Write the exact binaryPath target.
	w2, _ := zw.Create("exact/path/mytool")
	w2.Write(content) //nolint:errcheck
	zw.Close()
	zf.Close()

	destPath := filepath.Join(dir, "mytool")
	if err := extractAndInstall(archivePath, "archive.zip", "mytool", "exact/path/mytool", destPath); err != nil {
		t.Fatalf("extractAndInstall: %v", err)
	}
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Errorf("exact path: got %q, want %q", got, content)
	}
}

func TestExtractAndInstall_ZipBinaryNotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "archive.zip")
	zf, _ := os.Create(archivePath)
	zw := zip.NewWriter(zf)
	w, _ := zw.Create("other_binary")
	w.Write([]byte("data")) //nolint:errcheck
	zw.Close()
	zf.Close()

	err := extractAndInstall(archivePath, "archive.zip", "mytool", "", filepath.Join(dir, "mytool"))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

// --- extractAndInstall: tar.gz ---

func TestExtractAndInstall_TarGz(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := []byte("#!/bin/sh\nexit 0\n")
	archivePath := filepath.Join(dir, "tool.tar.gz")
	writeTarGz(t, archivePath, "dir/mytool", content)

	destPath := filepath.Join(dir, "mytool")
	if err := extractAndInstall(archivePath, "tool.tar.gz", "mytool", "", destPath); err != nil {
		t.Fatalf("extractAndInstall tar.gz: %v", err)
	}
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Errorf("tar.gz: got %q, want %q", got, content)
	}
	info, _ := os.Stat(destPath)
	if info.Mode()&0o111 == 0 {
		t.Error("tar.gz: installed binary is not executable")
	}
}

// --- extractAndInstall: tar.xz ---

func TestExtractAndInstall_TarXz(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := []byte("#!/bin/sh\nexit 0\n")
	archivePath := filepath.Join(dir, "tool.tar.xz")
	writeTarXz(t, archivePath, "dir/mytool", content)

	destPath := filepath.Join(dir, "mytool")
	if err := extractAndInstall(archivePath, "tool.tar.xz", "mytool", "", destPath); err != nil {
		t.Fatalf("extractAndInstall tar.xz: %v", err)
	}
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Errorf("tar.xz: got %q, want %q", got, content)
	}
	info, _ := os.Stat(destPath)
	if info.Mode()&0o111 == 0 {
		t.Error("tar.xz: installed binary is not executable")
	}
}

// --- extractAndInstall: raw binary ---

func TestExtractAndInstall_RawBinary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := []byte("ELF binary data")
	srcPath := filepath.Join(dir, "mytool-v1.0-raw")
	if err := os.WriteFile(srcPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	destPath := filepath.Join(dir, "mytool")
	if err := extractAndInstall(srcPath, "mytool-v1.0-raw", "mytool", "", destPath); err != nil {
		t.Fatalf("extractAndInstall raw: %v", err)
	}
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Errorf("raw: got %q, want %q", got, content)
	}
	info, _ := os.Stat(destPath)
	if info.Mode()&0o111 == 0 {
		t.Error("raw: installed binary is not executable")
	}
}

// --- tar exact-path match (parity with zip) ---

func TestExtractAndInstall_TarGzExactPath(t *testing.T) {
	t.Parallel()
	for _, compression := range []string{"gz", "xz"} {
		compression := compression
		t.Run(compression, func(t *testing.T) {
			dir := t.TempDir()
			correct := []byte("correct binary")
			wrong := []byte("wrong binary")
			ext := ".tar." + compression
			archivePath := filepath.Join(dir, "archive"+ext)

			// Write two entries with the same base name; exact path wins.
			writeTarMulti(t, archivePath, compression, []tarEntry{
				{name: "other/mytool", typeflag: tar.TypeReg, content: wrong},
				{name: "exact/path/mytool", typeflag: tar.TypeReg, content: correct},
			})

			destPath := filepath.Join(dir, "mytool")
			if err := extractAndInstall(archivePath, "archive"+ext, "mytool", "exact/path/mytool", destPath); err != nil {
				t.Fatalf("extractAndInstall exact path %s: %v", compression, err)
			}
			got, err := os.ReadFile(destPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(correct) {
				t.Errorf("%s exact path: got %q, want %q", compression, got, correct)
			}
		})
	}
}

func TestExtractAndInstall_TarBinaryNotFound(t *testing.T) {
	t.Parallel()
	for _, compression := range []string{"gz", "xz"} {
		compression := compression
		t.Run(compression, func(t *testing.T) {
			dir := t.TempDir()
			ext := ".tar." + compression
			archivePath := filepath.Join(dir, "archive"+ext)

			writeTarMulti(t, archivePath, compression, []tarEntry{
				{name: "other_binary", typeflag: tar.TypeReg, content: []byte("data")},
			})

			err := extractAndInstall(archivePath, "archive"+ext, "mytool", "", filepath.Join(dir, "mytool"))
			if err == nil || !strings.Contains(err.Error(), "not found") {
				t.Fatalf("%s: expected not-found error, got %v", compression, err)
			}
		})
	}
}

// --- symlink edge: symlink with matching name must not be selected ---

func TestExtractAndInstall_TarSymlinkSkipped(t *testing.T) {
	t.Parallel()
	for _, compression := range []string{"gz", "xz"} {
		compression := compression
		t.Run(compression, func(t *testing.T) {
			dir := t.TempDir()
			real := []byte("real binary content")
			ext := ".tar." + compression
			archivePath := filepath.Join(dir, "archive"+ext)

			// Symlink entry appears first with the target name; real regular
			// file appears second — the symlink must be skipped.
			writeTarMulti(t, archivePath, compression, []tarEntry{
				{name: "mytool", typeflag: tar.TypeSymlink, content: nil},
				{name: "dir/mytool", typeflag: tar.TypeReg, content: real},
			})

			destPath := filepath.Join(dir, "mytool")
			if err := extractAndInstall(archivePath, "archive"+ext, "mytool", "", destPath); err != nil {
				t.Fatalf("%s symlink skip: %v", compression, err)
			}
			got, err := os.ReadFile(destPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(real) {
				t.Errorf("%s symlink skip: got %q, want %q", compression, got, real)
			}
		})
	}
}

// --- size cap: oversized download and tar entry are rejected ---

func TestDownloadToFile_OversizedBodyRejected(t *testing.T) {
	t.Parallel(
	// Override the cap via a tiny limit so the test stays fast.
	)

	const testLimit = 16
	origMax := maxDownloadBytes
	// Can't change the constant at runtime; test via a crafted response body
	// that is exactly testLimit+1 bytes and verify the production limit works
	// by feeding maxDownloadBytes+1 bytes into a real downloadToFile call.
	_ = origMax // constant, can't reassign — test the real cap via a pipe

	// Pipe maxDownloadBytes+1 bytes through downloadToFile by serving them
	// from a local handler and verifying the error.
	// To keep the test fast we serve only 1 byte over the cap, which triggers
	// the limit check (n > maxDownloadBytes).
	importHTTP := &countingHandler{limit: maxDownloadBytes + 1}
	srv := newSingleUseServer(t, importHTTP)

	dir := t.TempDir()
	destPath := filepath.Join(dir, "out")
	err := downloadToFile(t.Context(), srv.Client(), srv.URL+"/big", destPath)
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("oversized download: want limit error, got %v", err)
	}
	// destPath must not exist (temp file cleaned up on error).
	if _, statErr := os.Stat(destPath); !os.IsNotExist(statErr) {
		t.Error("partial download file left behind after size-cap error")
	}
}

func TestExtractAndInstall_TarOversizedEntryRejected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "big.tar.gz")

	// Write a tar entry that is maxEntryBytes+1 bytes — the header claims the
	// right size so the writer succeeds; the reader hits the limit check.
	const bigSize = maxEntryBytes + 1
	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	hdr := &tar.Header{Name: "mytool", Typeflag: tar.TypeReg, Mode: 0o755, Size: bigSize}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	// Write bigSize zero bytes without buffering them all in memory.
	chunk := make([]byte, 1<<20) // 1 MiB chunks
	remaining := int64(bigSize)
	for remaining > 0 {
		n := int64(len(chunk))
		if n > remaining {
			n = remaining
		}
		if _, err := tw.Write(chunk[:n]); err != nil {
			t.Fatal(err)
		}
		remaining -= n
	}
	tw.Close() //nolint:errcheck
	gw.Close() //nolint:errcheck
	f.Close()  //nolint:errcheck

	destPath := filepath.Join(dir, "mytool")
	err = extractAndInstall(archivePath, "big.tar.gz", "mytool", "", destPath)
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("oversized tar entry: want limit error, got %v", err)
	}
	if _, statErr := os.Stat(destPath); !os.IsNotExist(statErr) {
		t.Error("partial binary left behind after entry size-cap error")
	}
}

// --- atomic install: no partial file on write failure ---

func TestWriteExecutable_AtomicNoBinaryOnFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	destPath := filepath.Join(dir, "mytool")

	// errReader always returns an error on Read, simulating a mid-stream failure.
	err := writeExecutable(errReader{}, destPath)
	if err == nil {
		t.Fatal("expected write error, got nil")
	}
	// The destination must not exist — the temp file was cleaned up.
	if _, statErr := os.Stat(destPath); !os.IsNotExist(statErr) {
		t.Error("partial binary exists at destPath after failed writeExecutable")
	}
}

func TestWriteExecutable_Mode0755(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	destPath := filepath.Join(dir, "mytool")
	content := []byte("binary content")

	if err := writeExecutable(strings.NewReader(string(content)), destPath); err != nil {
		t.Fatalf("writeExecutable: %v", err)
	}
	info, err := os.Stat(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %o, want 0755", info.Mode().Perm())
	}
}

// --- helpers ---

type tarEntry struct {
	name     string
	typeflag byte
	content  []byte
}

// writeTarMulti writes a tar archive (gz or xz compressed) with the given entries.
func writeTarMulti(t *testing.T, path, compression string, entries []tarEntry) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck

	var tw *tar.Writer
	switch compression {
	case "xz":
		xw, err := xz.NewWriter(f)
		if err != nil {
			t.Fatalf("xz.NewWriter: %v", err)
		}
		defer xw.Close() //nolint:errcheck
		tw = tar.NewWriter(xw)
	default:
		gw := gzip.NewWriter(f)
		defer gw.Close() //nolint:errcheck
		tw = tar.NewWriter(gw)
	}
	defer tw.Close() //nolint:errcheck

	for _, e := range entries {
		hdr := &tar.Header{
			Name:     e.name,
			Typeflag: e.typeflag,
			Mode:     0o755,
			Size:     int64(len(e.content)),
		}
		if e.typeflag == tar.TypeSymlink {
			hdr.Linkname = "target"
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if len(e.content) > 0 {
			if _, err := tw.Write(e.content); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// errReader returns an error on every Read call.
type errReader struct{}

func (errReader) Read(_ []byte) (int, error) {
	return 0, fmt.Errorf("injected read error")
}

// countingHandler serves exactly n bytes of zero data (one byte over the cap
// triggers the limit check in downloadToFile).
type countingHandler struct{ limit int64 }

func (h *countingHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/octet-stream")
	chunk := make([]byte, 32*1024)
	remaining := h.limit
	for remaining > 0 {
		n := int64(len(chunk))
		if n > remaining {
			n = remaining
		}
		w.Write(chunk[:n]) //nolint:errcheck
		remaining -= n
	}
}

func newSingleUseServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// --- helpers ---

func writeTarGz(t *testing.T, path, entryName string, content []byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck
	gw := gzip.NewWriter(f)
	defer gw.Close() //nolint:errcheck
	tw := tar.NewWriter(gw)
	defer tw.Close() //nolint:errcheck
	hdr := &tar.Header{
		Name: entryName,
		Mode: 0o755,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
}

func writeTarXz(t *testing.T, path, entryName string, content []byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck
	xw, err := xz.NewWriter(f)
	if err != nil {
		t.Fatalf("xz.NewWriter: %v", err)
	}
	defer xw.Close() //nolint:errcheck
	tw := tar.NewWriter(xw)
	defer tw.Close() //nolint:errcheck
	hdr := &tar.Header{
		Name: entryName,
		Mode: 0o755,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
}

// --- path traversal guard ---

func TestNativeGitHubInstallPipeline_RejectsTraversalAssetName(t *testing.T) {
	t.Parallel()
	traversalCases := []string{
		"../../etc/passwd",
		"../outside",
		"subdir/../../escape",
	}
	for _, assetName := range traversalCases {
		t.Run(assetName, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Should never be reached; the guard must reject before download.
				http.NotFound(w, r)
			}))
			t.Cleanup(srv.Close)

			cfgDir := t.TempDir()
			a := &App{ConfigPath: filepath.Join(cfgDir, "settings.json"), CacheDir: cfgDir}
			a.SetGitHubFallbackAPIForTest(srv.URL, srv.Client())

			fallback := &config.FallbackSpec{
				Binary: "mytool",
				Recipe: config.FallbackRecipe{
					Type:             config.FallbackRecipeGitHubReleaseAsset,
					AssetName:        assetName,
					AssetDownloadURL: srv.URL + "/asset/file.tar.gz",
					TagName:          "v1.0.0",
				},
			}
			err := a.nativeGitHubInstallPipeline(t.Context(), "mytool", fallback)
			if err == nil {
				t.Fatalf("expected error for asset_name %q, got nil", assetName)
			}
			// The resolved path must not exist outside the cache dir.
			cacheDir := filepath.Join(cfgDir, "fallback")
			entries, _ := filepath.Glob(cacheDir + "/../*")
			for _, e := range entries {
				if filepath.Base(e) == "passwd" || filepath.Base(e) == "outside" || filepath.Base(e) == "escape" {
					t.Errorf("traversal path %q was created on disk", e)
				}
			}
		})
	}
}

func TestNativeGitHubInstallPipeline_RejectsTraversalBinary(t *testing.T) {
	t.Parallel(
	// Build a minimal tar.gz so the download phase would succeed if the binary
	// guard were absent. The guard must reject before extraction.
	)

	content := []byte("#!/bin/sh\necho hi")
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "tool.tar.gz")
	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	_ = tw.WriteHeader(&tar.Header{Name: "mytool", Mode: 0o755, Size: int64(len(content))})
	_, _ = tw.Write(content)
	_ = tw.Close()
	_ = gw.Close()
	_ = f.Close()
	archiveBytes, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/tags/v1.0.0"):
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"id": 1, "tag_name": "v1.0.0", "assets": []any{}}) //nolint:errcheck
		default:
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write(archiveBytes) //nolint:errcheck
		}
	}))
	t.Cleanup(srv.Close)

	cfgDir := t.TempDir()
	a := &App{ConfigPath: filepath.Join(cfgDir, "settings.json"), CacheDir: cfgDir}
	a.SetGitHubFallbackAPIForTest(srv.URL, srv.Client())

	traversalBinaries := []string{"../../injected", "../bad"}
	for _, binary := range traversalBinaries {
		t.Run(binary, func(t *testing.T) {
			fallback := &config.FallbackSpec{
				Binary: binary,
				Recipe: config.FallbackRecipe{
					Type:             config.FallbackRecipeGitHubReleaseAsset,
					AssetName:        "tool.tar.gz",
					AssetDownloadURL: srv.URL + "/asset/tool.tar.gz",
					TagName:          "v1.0.0",
				},
			}
			err := a.nativeGitHubInstallPipeline(t.Context(), "mytool", fallback)
			if err == nil {
				t.Fatalf("expected error for binary %q, got nil", binary)
			}
		})
	}
}

// --- redirect token stripping ---

// TestGitHubHTTPClient_RedirectStripsAuthorizationHeader verifies that the
// Authorization header is not forwarded when a download URL redirects to a
// non-GitHub host. A leak here would hand GITHUB_TOKEN to an arbitrary server.
func TestGitHubHTTPClient_RedirectStripsAuthorizationHeader(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "secret-token")

	// Second-hop server: records whether it received an Authorization header.
	var receivedAuth string
	hop2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(hop2.Close)

	// First-hop server: redirects to the non-GitHub hop2 server.
	hop1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, hop2.URL+"/resource", http.StatusFound)
	}))
	t.Cleanup(hop1.Close)

	a := &App{}
	client := a.githubHTTPClient()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, hop1.URL+"/start", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer secret-token")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if receivedAuth != "" {
		t.Errorf("Authorization header was forwarded to non-GitHub redirect target: %q", receivedAuth)
	}
}
