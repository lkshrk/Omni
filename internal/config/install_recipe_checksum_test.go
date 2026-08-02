package config_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ulikunitz/xz"

	"github.com/lkshrk/omni/internal/config"
)

type releaseRecipeHarness struct {
	binDir     string
	fixtureDir string
	fakeBin    string
	requests   string
	releaseURL string
	tempDir    string
	arch       string
	path       string
}

func newReleaseRecipeHarness(t *testing.T, arch string) *releaseRecipeHarness {
	t.Helper()
	root := t.TempDir()
	h := &releaseRecipeHarness{
		binDir:     filepath.Join(root, "install"),
		fixtureDir: filepath.Join(root, "fixtures"),
		fakeBin:    filepath.Join(root, "bin"),
		requests:   filepath.Join(root, "requests"),
		releaseURL: "https://github.com/owner/repo/releases/tag/v1.0.0",
		tempDir:    filepath.Join(root, "tmp"),
		arch:       arch,
	}
	h.path = h.fakeBin + ":/usr/bin:/bin"
	for _, dir := range []string{h.fixtureDir, h.fakeBin, h.tempDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeExecutableTestFile(t, filepath.Join(h.fakeBin, "curl"), `#!/bin/sh
set -eu
url=
out=
write=
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) shift; out=$1 ;;
    -w) shift; write=$1 ;;
    -*) ;;
    *) url=$1 ;;
  esac
  shift
done
if [ -n "$write" ]; then
  printf '%s' "$OMNI_RECIPE_RELEASE_URL"
  exit 0
fi
name=${url##*/}
printf '%s\n' "$name" >>"$OMNI_RECIPE_REQUESTS"
test -f "$OMNI_RECIPE_FIXTURES/$name"
cp "$OMNI_RECIPE_FIXTURES/$name" "$out"
`)
	writeExecutableTestFile(t, filepath.Join(h.fakeBin, "uname"), `#!/bin/sh
case "${1:-}" in
  -m) printf '%s\n' "$OMNI_RECIPE_ARCH" ;;
  -s) printf '%s\n' Linux ;;
  *) exec /usr/bin/uname "$@" ;;
esac
`)
	return h
}

func writeExecutableTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
}

func (h *releaseRecipeHarness) fixture(t *testing.T, name string, contents []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(h.fixtureDir, name), contents, 0o644); err != nil {
		t.Fatal(err)
	}
}

func (h *releaseRecipeHarness) run(t *testing.T, spec config.ToolInstallSpec, option string) ([]byte, error) {
	t.Helper()
	materialized, err := config.MaterializeInstallSpec("tool", spec, "")
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("sh", "-c", materialized.Options[option])
	cmd.Env = append(os.Environ(),
		"PATH="+h.path,
		"TMPDIR="+h.tempDir,
		"OMNI_RECIPE_ARCH="+h.arch,
		"OMNI_RECIPE_FIXTURES="+h.fixtureDir,
		"OMNI_RECIPE_RELEASE_URL="+h.releaseURL,
		"OMNI_RECIPE_REQUESTS="+h.requests,
	)
	return cmd.CombinedOutput()
}

func (h *releaseRecipeHarness) useShasumOnly(t *testing.T) {
	t.Helper()
	for _, name := range []string{"awk", "cp", "install", "mkdir", "mktemp", "mv", "rm", "tr"} {
		path, err := exec.LookPath(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(path, filepath.Join(h.fakeBin, name)); err != nil {
			t.Fatal(err)
		}
	}
	sha256sum, err := exec.LookPath("sha256sum")
	if err != nil {
		t.Fatal(err)
	}
	writeExecutableTestFile(t, filepath.Join(h.fakeBin, "shasum"), fmt.Sprintf(`#!/bin/sh
set -eu
test "$1" = -a
test "$2" = 256
shift 2
printf 'shasum\n' >>"$OMNI_RECIPE_REQUESTS"
exec %q "$@"
`, sha256sum))
	h.path = h.fakeBin
}

func verifiedReleaseRecipe(binDir, assetPattern, checksumPattern string) config.ToolInstallSpec {
	return config.ToolInstallSpec{
		Provider: "script",
		Source:   &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "owner", Repo: "repo"},
		Recipe: &config.FallbackRecipe{
			Type:                 config.FallbackRecipeGitHubReleaseAsset,
			AssetPattern:         assetPattern,
			ChecksumAssetPattern: checksumPattern,
			TagName:              "v1.0.0",
		},
		Bin:    "tool",
		BinDir: binDir,
		Options: map[string]string{
			"arch_map": "x86_64:amd64,amd64:amd64,aarch64:arm64,arm64:arm64",
		},
	}
}

func sha256Digest(contents []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(contents))
}

func TestGitHubReleaseAssetVerifiedDirectBinaryInstallsAtomically(t *testing.T) {
	h := newReleaseRecipeHarness(t, "x86_64")
	asset := []byte("new binary")
	h.fixture(t, "tool", asset)
	h.fixture(t, "checksums.txt", []byte(sha256Digest(asset)+" *tool\n"))

	if output, err := h.run(t, verifiedReleaseRecipe(h.binDir, "tool", "checksums.txt"), "install"); err != nil {
		t.Fatalf("install: %v\n%s", err, output)
	}
	assertInstalledBinary(t, filepath.Join(h.binDir, "tool"), asset)
}

func TestGitHubReleaseAssetVerifiedArchiveInstallsExtractedBinary(t *testing.T) {
	h := newReleaseRecipeHarness(t, "x86_64")
	asset := tarGzExecutable(t, "tool", []byte("archive binary"))
	h.fixture(t, "tool.tar.gz", asset)
	h.fixture(t, "tool_1.0.0_checksums.txt", []byte(sha256Digest(asset)+"  tool.tar.gz\n"))

	spec := verifiedReleaseRecipe(h.binDir, "tool.tar.gz", "tool_{version}_checksums.txt")
	if output, err := h.run(t, spec, "install"); err != nil {
		t.Fatalf("install: %v\n%s", err, output)
	}
	assertInstalledBinary(t, filepath.Join(h.binDir, "tool"), []byte("archive binary"))
}

func TestGitHubReleaseAssetUnpinnedVersionPreservesChecksumAndExtraction(t *testing.T) {
	h := newReleaseRecipeHarness(t, "x86_64")
	h.releaseURL = "https://github.com/owner/repo/releases/tag/v1.2.3+meta"
	assetName := "tool_1.2.3+meta_linux_amd64.tar.gz"
	checksumName := "tool_1.2.3+meta_checksums.txt"
	asset := tarGzExecutable(t, "tool", []byte("latest archive binary"))
	h.fixture(t, assetName, asset)
	h.fixture(t, checksumName, []byte(sha256Digest(asset)+"  "+assetName+"\n"))

	spec := verifiedReleaseRecipe(h.binDir, "tool_{version}_linux_{arch}.tar.gz", "tool_{version}_checksums.txt")
	spec.Recipe.TagName = ""
	if output, err := h.run(t, spec, "install"); err != nil {
		t.Fatalf("install: %v\n%s", err, output)
	}
	assertInstalledBinary(t, filepath.Join(h.binDir, "tool"), []byte("latest archive binary"))
	requests, err := os.ReadFile(h.requests)
	if err != nil {
		t.Fatal(err)
	}
	if string(requests) != assetName+"\n"+checksumName+"\n" {
		t.Fatalf("requests = %q, want resolved asset and checksum names", requests)
	}
}

func TestGitHubReleaseAssetVerifiedAdditionalArchiveFormats(t *testing.T) {
	for _, tc := range []struct {
		name  string
		asset string
		build func(*testing.T, string, []byte) []byte
	}{
		{name: "zip", asset: "tool.zip", build: zipExecutable},
		{name: "tar xz", asset: "tool.tar.xz", build: tarXzExecutable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newReleaseRecipeHarness(t, "x86_64")
			want := []byte(tc.name + " binary")
			asset := tc.build(t, "tool", want)
			h.fixture(t, tc.asset, asset)
			h.fixture(t, "checksums.txt", []byte(sha256Digest(asset)+"  "+tc.asset+"\n"))
			if output, err := h.run(t, verifiedReleaseRecipe(h.binDir, tc.asset, "checksums.txt"), "install"); err != nil {
				t.Fatalf("install: %v\n%s", err, output)
			}
			assertInstalledBinary(t, filepath.Join(h.binDir, "tool"), want)
		})
	}
}

func TestGitHubReleaseAssetVerifiedUsesShasumFallback(t *testing.T) {
	h := newReleaseRecipeHarness(t, "x86_64")
	h.useShasumOnly(t)
	asset := []byte("binary")
	h.fixture(t, "tool", asset)
	h.fixture(t, "checksums.txt", []byte(sha256Digest(asset)+"  tool\n"))
	if output, err := h.run(t, verifiedReleaseRecipe(h.binDir, "tool", "checksums.txt"), "install"); err != nil {
		t.Fatalf("install: %v\n%s", err, output)
	}
	requests, err := os.ReadFile(h.requests)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(requests), "shasum\n") {
		t.Fatalf("requests = %q, want shasum fallback", requests)
	}
}

func TestGitHubReleaseAssetVerificationFailuresPreserveExistingBinary(t *testing.T) {
	asset := []byte("new binary")
	validDigest := sha256Digest(asset)
	for _, tc := range []struct {
		name            string
		assetExists     bool
		manifestExists  bool
		manifest        string
		wantOutputMatch string
	}{
		{name: "checksum mismatch", assetExists: true, manifestExists: true, manifest: strings.Repeat("0", 64) + "  tool\n", wantOutputMatch: "checksum mismatch"},
		{name: "missing manifest entry", assetExists: true, manifestExists: true, manifest: validDigest + "  other\n", wantOutputMatch: "no entry"},
		{name: "duplicate manifest entry", assetExists: true, manifestExists: true, manifest: validDigest + "  tool\n" + validDigest + " *tool\n", wantOutputMatch: "duplicate entries"},
		{name: "malformed digest", assetExists: true, manifestExists: true, manifest: strings.Repeat("z", 64) + "  tool\n", wantOutputMatch: "malformed digest"},
		{name: "asset download failure", manifestExists: true, manifest: validDigest + "  tool\n"},
		{name: "manifest download failure", assetExists: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newReleaseRecipeHarness(t, "x86_64")
			if err := os.MkdirAll(h.binDir, 0o755); err != nil {
				t.Fatal(err)
			}
			oldPath := filepath.Join(h.binDir, "tool")
			writeExecutableTestFile(t, oldPath, "old binary")
			if tc.assetExists {
				h.fixture(t, "tool", asset)
			}
			if tc.manifestExists {
				h.fixture(t, "checksums.txt", []byte(tc.manifest))
			}

			output, err := h.run(t, verifiedReleaseRecipe(h.binDir, "tool", "checksums.txt"), "upgrade")
			if err == nil {
				t.Fatalf("upgrade succeeded, want failure\n%s", output)
			}
			if tc.wantOutputMatch != "" && !strings.Contains(string(output), tc.wantOutputMatch) {
				t.Fatalf("output = %q, want %q", output, tc.wantOutputMatch)
			}
			assertInstalledBinary(t, oldPath, []byte("old binary"))
		})
	}
}

func TestGitHubReleaseAssetRenameFailurePreservesBinaryAndCleansStaging(t *testing.T) {
	h := newReleaseRecipeHarness(t, "x86_64")
	if err := os.MkdirAll(h.binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(h.binDir, "tool")
	writeExecutableTestFile(t, oldPath, "old binary")
	asset := []byte("new binary")
	h.fixture(t, "tool", asset)
	h.fixture(t, "checksums.txt", []byte(sha256Digest(asset)+"  tool\n"))
	writeExecutableTestFile(t, filepath.Join(h.fakeBin, "mv"), "#!/bin/sh\nexit 1\n")

	if output, err := h.run(t, verifiedReleaseRecipe(h.binDir, "tool", "checksums.txt"), "upgrade"); err == nil {
		t.Fatalf("upgrade succeeded, want rename failure\n%s", output)
	}
	assertInstalledBinary(t, oldPath, []byte("old binary"))
	if matches, err := filepath.Glob(filepath.Join(h.binDir, ".tool.*")); err != nil || len(matches) != 0 {
		t.Fatalf("staged files = %v, err = %v, want none", matches, err)
	}
	entries, err := os.ReadDir(h.tempDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary directory entries = %v, want cleanup", entries)
	}
}

func TestGitHubReleaseAssetWithoutChecksumKeepsLegacyCommand(t *testing.T) {
	binDir := "/tmp/omni-bin"
	spec := verifiedReleaseRecipe(binDir, "tool", "")
	materialized, err := config.MaterializeInstallSpec("tool", spec, "")
	if err != nil {
		t.Fatal(err)
	}
	want := "mkdir -p '/tmp/omni-bin' && curl -fsSL --proto-redir =https 'https://github.com/owner/repo/releases/download/v1.0.0/tool' -o '/tmp/omni-bin'/'tool' && chmod +x '/tmp/omni-bin'/'tool'"
	if materialized.Options["install"] != want {
		t.Fatalf("install = %q, want legacy command %q", materialized.Options["install"], want)
	}
}

func TestGitHubReleaseAssetVerifiedArchitectureSubstitution(t *testing.T) {
	for _, tc := range []struct {
		uname string
		asset string
	}{
		{uname: "x86_64", asset: "tool_amd64"},
		{uname: "aarch64", asset: "tool_arm64"},
	} {
		t.Run(tc.asset, func(t *testing.T) {
			h := newReleaseRecipeHarness(t, tc.uname)
			contents := []byte(tc.asset)
			h.fixture(t, tc.asset, contents)
			h.fixture(t, "checksums.txt", []byte(sha256Digest(contents)+"  "+tc.asset+"\n"))
			if output, err := h.run(t, verifiedReleaseRecipe(h.binDir, "tool_{arch}", "checksums.txt"), "install"); err != nil {
				t.Fatalf("install: %v\n%s", err, output)
			}
			requests, err := os.ReadFile(h.requests)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(requests), tc.asset+"\n") {
				t.Fatalf("requests = %q, want asset %q", requests, tc.asset)
			}
			assertInstalledBinary(t, filepath.Join(h.binDir, "tool"), contents)
		})
	}
}

func TestGitHubReleaseAssetVerifiedChecksumPatternSubstitution(t *testing.T) {
	for _, tc := range []struct {
		name     string
		pattern  string
		manifest string
	}{
		{name: "resolved asset uses mapped architecture", pattern: "checksums_{arch}.txt", manifest: "checksums_amd64.txt"},
		{name: "checksum-only OS placeholder", pattern: "checksums_{os}.txt", manifest: "checksums_linux.txt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newReleaseRecipeHarness(t, "x86_64")
			asset := []byte("binary")
			h.fixture(t, "tool", asset)
			h.fixture(t, tc.manifest, []byte(sha256Digest(asset)+"  tool\n"))
			spec := verifiedReleaseRecipe(h.binDir, "tool", tc.pattern)
			spec.Recipe.AssetDownloadURL = "https://example.com/tool"
			if output, err := h.run(t, spec, "install"); err != nil {
				t.Fatalf("install: %v\n%s", err, output)
			}
			requests, err := os.ReadFile(h.requests)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(requests), tc.manifest+"\n") {
				t.Fatalf("requests = %q, want manifest %q", requests, tc.manifest)
			}
		})
	}
}

func TestGitHubReleaseAssetChecksumRejectsExtractDirDuringValidation(t *testing.T) {
	spec := verifiedReleaseRecipe("/tmp/bin", "tool.tar.gz", "checksums.txt")
	spec.Options["extract_dir"] = "/tmp/tool"
	cfg := &config.RootConfig{
		Version: config.CurrentVersion,
		Tools:   map[string]config.ToolSpec{"tool": {Providers: []config.ToolInstallSpec{spec}}},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{})
	for _, err := range errs {
		if strings.HasSuffix(err.Path, ".providers[0].options.extract_dir") && strings.Contains(err.Message, "checksum_asset_pattern") {
			return
		}
	}
	t.Fatalf("validation errors = %v, want checksum/extract_dir incompatibility", errs)
}

func assertInstalledBinary(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("binary = %q, want %q", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("binary mode = %o, want executable", info.Mode().Perm())
	}
}

func tarGzExecutable(t *testing.T, name string, contents []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(contents))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(contents); err != nil {
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

func zipExecutable(t *testing.T, name string, contents []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetMode(0o755)
	w, err := zw.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(contents); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func tarXzExecutable(t *testing.T, name string, contents []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	xw, err := xz.NewWriter(&out)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(xw)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(contents))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(contents); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := xw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
