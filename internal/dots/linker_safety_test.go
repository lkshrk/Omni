package dots_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/dots"
)

func TestWriteStowShapedSymlinkRejectsSandboxEscapes(t *testing.T) {
	root := os.Getenv("OMNI_TEST_ROOT")
	source := filepath.Join(t.TempDir(), "source")
	outside := filepath.Dir(root)

	t.Run("outside target entry", func(t *testing.T) {
		link := filepath.Join(outside, filepath.Base(root)+"-relink")
		requireSafetyRejection(t, dots.WriteStowShapedSymlink(link, source), "dotfiles relink target")
		if _, err := os.Lstat(link + ".omc-relink-tmp"); !os.IsNotExist(err) {
			t.Fatalf("outside temp entry was created: %v", err)
		}
	})

	t.Run("intermediate symlink", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		escape := filepath.Join(home, "escape")
		if err := os.Symlink(outside, escape); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(escape, filepath.Base(root)+"-relink")
		requireSafetyRejection(t, dots.WriteStowShapedSymlink(link, source), "dotfiles relink target")
		if _, err := os.Lstat(link + ".omc-relink-tmp"); !os.IsNotExist(err) {
			t.Fatalf("escaped temp entry was created: %v", err)
		}
	})
}

func TestWriteStowShapedSymlinkReplacesFinalSymlinkEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	source := filepath.Join(t.TempDir(), "source")
	link := filepath.Join(home, ".toolrc")
	outside := filepath.Dir(os.Getenv("OMNI_TEST_ROOT"))
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	if err := dots.WriteStowShapedSymlink(link, source); err != nil {
		t.Fatalf("WriteStowShapedSymlink: %v", err)
	}
	got, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	if resolved := filepath.Clean(filepath.Join(filepath.Dir(link), got)); resolved != source {
		t.Fatalf("resolved link = %q, want %q", resolved, source)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside final-symlink target changed: %v", err)
	}
}

func TestWriteStowShapedSymlinkRejectsEscapingSourceSymlink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	source := filepath.Join(t.TempDir(), "source")
	if err := os.Symlink(filepath.Dir(os.Getenv("OMNI_TEST_ROOT")), source); err != nil {
		t.Fatal(err)
	}

	requireSafetyRejection(t, dots.WriteStowShapedSymlink(filepath.Join(home, ".toolrc"), source), "dotfiles relink source")
}
