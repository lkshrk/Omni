package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestSafeDotsMutationChecksEntryLocationWithoutFollowingFinalSymlink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	out := filepath.Join(t.TempDir(), "managed")
	if err := os.MkdirAll(out, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".managed")
	if err := os.Symlink(out, link); err != nil {
		t.Fatal(err)
	}

	a := &App{testMode: true}
	if err := a.requireSafeTestDotsMutation("", []config.DotEntry{{Name: "managed", Path: link}}); err != nil {
		t.Fatalf("final symlink entry was rejected: %v", err)
	}
}

func TestSafeDotsMutationRejectsIntermediateSymlinkOutsideHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	out := t.TempDir()
	if err := os.Symlink(out, filepath.Join(home, ".config")); err != nil {
		t.Fatal(err)
	}

	a := &App{testMode: true}
	err := a.requireSafeTestDotsMutation("", []config.DotEntry{{Name: "nvim", Path: filepath.Join(home, ".config", "nvim")}})
	if err == nil || !strings.Contains(err.Error(), "outside test HOME") {
		t.Fatalf("intermediate symlink escape error = %v", err)
	}
}
