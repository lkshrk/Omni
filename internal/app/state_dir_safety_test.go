package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveStateDirRejectsExplicitPathOutsideTestSandbox(t *testing.T) {
	outside, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	a := &App{StateDir: outside}
	if err := a.resolveStateDir(); err == nil || !strings.Contains(err.Error(), "app state directory") {
		t.Fatalf("resolveStateDir outside error = %v", err)
	}
}

func TestResolveStateDirRejectsTraversalAndSymlinkEscapes(t *testing.T) {
	outside, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	link := filepath.Join(root, "state-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		filepath.Join(os.Getenv("OMNI_TEST_ROOT"), "..", "state-escape"),
		link,
	} {
		a := &App{StateDir: path}
		if err := a.resolveStateDir(); err == nil || !strings.Contains(err.Error(), "app state directory") {
			t.Errorf("resolveStateDir escaping path %q error = %v", path, err)
		}
	}
}
