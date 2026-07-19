package dots_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/dots"
)

func TestTrashLocalPath_MovesRealFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".xdg"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))

	target := filepath.Join(home, "junk.txt")
	mkFile(t, target, "content")

	if err := dots.TrashLocalPath(context.Background(), nil, target); err != nil {
		t.Fatalf("TrashLocalPath: %v", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("target should be removed: %v", err)
	}
	trashed := filepath.Join(expectedTrashRoot(t, home), "junk.txt")
	body, err := os.ReadFile(trashed)
	if err != nil {
		t.Fatalf("read trashed file: %v", err)
	}
	if string(body) != "content" {
		t.Fatalf("trashed content = %q, want content", body)
	}
}

func TestTrashLocalPath_UnlinksSymlinkWithoutTrashing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".xdg"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))

	realFile := filepath.Join(home, "real.txt")
	mkFile(t, realFile, "keep me")
	link := filepath.Join(home, "link.txt")
	if err := os.Symlink(realFile, link); err != nil {
		t.Fatal(err)
	}

	if err := dots.TrashLocalPath(context.Background(), nil, link); err != nil {
		t.Fatalf("TrashLocalPath: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("symlink should be unlinked: %v", err)
	}
	// The link's target must be untouched, and nothing should be trashed.
	if body, err := os.ReadFile(realFile); err != nil || string(body) != "keep me" {
		t.Fatalf("symlink target changed: body=%q err=%v", body, err)
	}
	if _, err := os.Lstat(filepath.Join(expectedTrashRoot(t, home), "link.txt")); !os.IsNotExist(err) {
		t.Fatal("unlinking a symlink should not trash anything")
	}
}

func TestTrashLocalPath_MissingIsNoop(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := dots.TrashLocalPath(context.Background(), nil, filepath.Join(home, "absent")); err != nil {
		t.Fatalf("TrashLocalPath on missing path = %v, want nil", err)
	}
}
