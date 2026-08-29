package app

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveModulePathRejectsUnsafeAndAmbiguousPaths(t *testing.T) {
	root := filepath.Join(t.TempDir(), "apm_modules")
	valid := filepath.Join(root, "acme", "bundle")
	if err := os.MkdirAll(valid, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok := resolveModulePath(root, "ACME/bundle"); !ok || got != valid {
		t.Fatalf("valid case-insensitive path = %q, %v", got, ok)
	}
	for _, source := range []string{"", ".", "..", "/acme/bundle", "acme//bundle", "acme/../bundle", `acme\bundle`} {
		if got, ok := resolveModulePath(root, source); ok {
			t.Fatalf("unsafe source %q resolved to %q", source, got)
		}
	}

	if err := os.Mkdir(filepath.Join(root, "ACME"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok := resolveModulePath(root, "acme/bundle"); ok {
		t.Fatalf("ambiguous case-insensitive path resolved to %q", got)
	}
}

func TestModuleManifestReadRejectsSymlinkAndEscape(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, "apm_modules")
	module := filepath.Join(root, "acme", "bundle")
	outside := filepath.Join(workspace, "outside")
	if err := os.MkdirAll(module, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideManifest := filepath.Join(outside, "apm.yml")
	if err := os.WriteFile(outsideManifest, []byte("name: secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(module, "apm.yml")
	if err := os.Symlink(outsideManifest, manifestPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := readAPMModuleManifest(root, module, manifestPath); err == nil {
		t.Fatal("symlinked module manifest accepted")
	}
	if _, err := readAPMModuleManifest(root, outside, outsideManifest); err == nil {
		t.Fatal("module outside apm_modules accepted")
	}
}

func TestModuleManifestParseIssueRedactsSecretLiteral(t *testing.T) {
	a := setupAgentsWorkspace(t, "dependencies:\n  apm:\n  - git: acme/bundle\n", "dependencies:\n- repo_url: acme/bundle\n  name: bundle\n  version: 1.0.0\n")
	_ = a
	home, _ := os.UserHomeDir()
	manifestPath := filepath.Join(home, ".apm", "apm_modules", "acme", "bundle", "apm.yml")
	const secret = "super-secret-literal"
	writeFile(t, manifestPath, "name: bundle\ndependencies: [\n  "+secret+"\n")
	manifest, lock, err := readAPMWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	rows := joinAPMPackages(manifest, lock)
	evidence := readAPMModuleManifests(filepath.Join(home, ".apm"), rows)
	issues := strings.Join(rows[0].Issues, "\n")
	if len(evidence.Unavailable) != 1 || !strings.Contains(issues, "invalid package manifest") || strings.Contains(issues, secret) {
		t.Fatalf("unavailable=%v issues=%q", evidence.Unavailable, issues)
	}
}

func TestResolveModulePathRejectsSymlinkedRootsAndComponents(t *testing.T) {
	base := t.TempDir()
	realRoot := filepath.Join(base, "real")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(filepath.Join(realRoot, "acme"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(outside, "bundle"), 0o755); err != nil {
		t.Fatal(err)
	}
	linkedRoot := filepath.Join(base, "linked-root")
	if err := os.Symlink(realRoot, linkedRoot); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if got, ok := resolveModulePath(linkedRoot, "acme"); ok {
		t.Fatalf("symlinked root resolved to %q", got)
	}
	if err := os.Symlink(filepath.Join(outside, "bundle"), filepath.Join(realRoot, "acme", "bundle")); err != nil {
		t.Fatal(err)
	}
	if got, ok := resolveModulePath(realRoot, "acme/bundle"); ok {
		t.Fatalf("symlinked component resolved to %q", got)
	}
}

func TestModuleDiscoveryUsesLockedSourceAndReturnsIdentity(t *testing.T) {
	a := setupAgentsWorkspace(t, `dependencies:
  apm:
  - name: bundle
    marketplace: catalog
`, `dependencies:
- repo_url: acme/plugins
  name: bundle
  virtual_path: plugins/bundle
  version: 1.0.0
`)
	_ = a
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(home, ".apm", "apm_modules", "acme", "plugins", "plugins", "bundle", "apm.yml")
	manifestRaw := []byte("name: bundle\ndescription: locked module\ndependencies: {}\n")
	writeFile(t, manifestPath, string(manifestRaw))
	manifest, lock, err := readAPMWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	rows := joinAPMPackages(manifest, lock)
	if len(rows) != 1 || rows[0].Source != "bundle@catalog" || rows[0].ModuleSource != "acme/plugins/plugins/bundle" {
		t.Fatalf("package identity = %#v", rows)
	}
	evidence := readAPMModuleManifests(filepath.Join(home, ".apm"), rows)
	if rows[0].Description != "locked module" || len(evidence.Manifests) != 1 || evidence.Manifests[0].Path != manifestPath {
		t.Fatalf("rows=%#v evidence=%#v", rows, evidence)
	}
	sum := sha256.Sum256(manifestRaw)
	if evidence.Manifests[0].Hash != hex.EncodeToString(sum[:]) {
		t.Fatalf("manifest hash = %q", evidence.Manifests[0].Hash)
	}
}
