package testguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequireTempPathRejectsLiveLocalPath(t *testing.T) {
	if !Active() {
		t.Skip("local test guard inactive")
	}
	err := RequireTempPath("config", filepath.Join(string(filepath.Separator), "etc", "omni", "settings.json"))
	if err == nil {
		t.Fatal("RequireTempPath accepted a live path")
	}
	if !strings.Contains(err.Error(), "outside a temp directory") {
		t.Fatalf("err = %v, want outside-temp message", err)
	}
}

func TestRequireTempPathAcceptsTempPath(t *testing.T) {
	if err := RequireTempPath("config", filepath.Join(t.TempDir(), "settings.json")); err != nil {
		t.Fatalf("RequireTempPath rejected temp path: %v", err)
	}
}

func TestPathInRootRejectsIntermediateSymlinkEscape(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "root")
	sibling := filepath.Join(tmp, "sibling")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sibling, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if PathInRoot(filepath.Join(root, "linked", "settings.json"), root) {
		t.Fatal("PathInRoot accepted a path through an intermediate symlink outside root")
	}
}

func TestPathInRootPreservesFinalSymlinkAndCanonicalRootAlias(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "root")
	sibling := filepath.Join(tmp, "sibling")
	alias := filepath.Join(tmp, "root-alias")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(sibling, "settings.json")
	if err := os.WriteFile(outsideFile, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	finalLink := filepath.Join(root, "settings.json")
	if err := os.Symlink(outsideFile, finalLink); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}

	if !PathInRoot(finalLink, root) {
		t.Fatal("PathInRoot rejected a final symlink located inside root")
	}
	if !PathInRoot(filepath.Join(root, "new.json"), alias) {
		t.Fatal("PathInRoot rejected a path through a canonical root alias")
	}
}

func TestPathInRootAcceptsDescendantBelowRegularFile(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("blocker"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(blocker, "child", "settings.json")

	if !PathInRoot(target, root) {
		t.Fatal("PathInRoot rejected a lexical descendant below a regular file")
	}
	if err := RequireTempPath("config", target); err != nil {
		t.Fatalf("RequireTempPath rejected a temp path below a regular file: %v", err)
	}
}

func TestEnsureSafeEnvSetsExplicitOmniPaths(t *testing.T) {
	if !Active() {
		t.Skip("local test guard inactive")
	}
	for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "OMNI_CACHE_DIR", "OMNI_CONFIG"} {
		got := os.Getenv(key)
		if got == "" {
			t.Fatalf("%s is empty", key)
		}
		if !PathInTempRoot(got) {
			t.Fatalf("%s = %q, want path under temp root", key, got)
		}
	}
	if got := os.Getenv("OMNI_CONFIG"); !strings.HasSuffix(got, filepath.Join("xdg-config", "omni", "settings.json")) {
		t.Fatalf("OMNI_CONFIG = %q, want xdg-config/omni/settings.json", got)
	}
}
