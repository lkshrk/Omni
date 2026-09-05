package dots_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/dots"
)

func requireSafetyRejection(t *testing.T, err error, kind string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), kind) {
		t.Fatalf("safety rejection error = %v, want %q", err, kind)
	}
}

func TestBackupAndTrashRejectPathsOutsideTestSandbox(t *testing.T) {
	outside, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	backup := filepath.Join(root, "backup")
	mkFile(t, backup, "safe")

	_, err = dots.BackupLocalPath(filepath.Join(outside, "backup.go"))
	requireSafetyRejection(t, err, "dotfiles backup source")
	_, err = dots.BackupAndRemoveLocalPath(outside)
	requireSafetyRejection(t, err, "dotfiles backup and removal target")
	requireSafetyRejection(t, dots.RemoveLocalPathAfterBackup(outside, backup), "dotfiles removal target")
	requireSafetyRejection(t, dots.TrashLocalPath(context.Background(), nil, filepath.Join(os.Getenv("OMNI_TEST_ROOT"), "..", "trash-escape")), "dotfiles trash target")
}

func TestBackupCopiesAndRemovalUnlinksFinalSymlink(t *testing.T) {
	outside, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	data := filepath.Join(home, "data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", data)

	link := filepath.Join(home, "outside-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	backup, err := dots.BackupLocalPath(link)
	if err != nil {
		t.Fatalf("BackupLocalPath final symlink: %v", err)
	}
	if got, err := os.Readlink(backup); err != nil || got != outside {
		t.Fatalf("backup symlink = %q, %v; want %q", got, err, outside)
	}
	if err := dots.TrashLocalPath(context.Background(), nil, link); err != nil {
		t.Fatalf("TrashLocalPath unlink final symlink: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("final symlink still exists after unlink: %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("symlink target changed: %v", err)
	}

	for name, remove := range map[string]func(string) error{
		"backup and remove": func(path string) error {
			_, err := dots.BackupAndRemoveLocalPath(path)
			return err
		},
		"remove after backup": func(path string) error {
			return dots.RemoveLocalPathAfterBackup(path, "")
		},
	} {
		t.Run(name, func(t *testing.T) {
			link := filepath.Join(home, name)
			if err := os.Symlink(outside, link); err != nil {
				t.Fatal(err)
			}
			if err := remove(link); err != nil {
				t.Fatalf("unlink final symlink: %v", err)
			}
			if _, err := os.Lstat(link); !os.IsNotExist(err) {
				t.Fatalf("final symlink still exists after unlink: %v", err)
			}
		})
	}
}

func TestRemoveRejectsBackupReadOutsideTestSandbox(t *testing.T) {
	outside, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target")
	mkFile(t, target, "keep")

	requireSafetyRejection(t, dots.RemoveLocalPathAfterBackup(target, filepath.Join(outside, "backup.go")), "dotfiles removal backup")
	if body, err := os.ReadFile(target); err != nil || string(body) != "keep" {
		t.Fatalf("rejected target changed: body=%q err=%v", body, err)
	}
}

func TestBackupAndTrashRejectEscapingDerivedRoots(t *testing.T) {
	outside, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("backup root", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		if err := os.Symlink(outside, filepath.Join(home, dots.BackupDirName)); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(home, "target")
		mkFile(t, target, "keep")
		_, err := dots.BackupLocalPath(target)
		requireSafetyRejection(t, err, "dotfiles backup root")
	})

	t.Run("trash data root", func(t *testing.T) {
		home := t.TempDir()
		data := filepath.Join(home, "data")
		if err := os.MkdirAll(data, 0o700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("HOME", home)
		t.Setenv("XDG_DATA_HOME", data)
		trashRoot := expectedTrashRoot(t, home)
		if err := os.MkdirAll(filepath.Dir(trashRoot), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, trashRoot); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(home, "target")
		mkFile(t, target, "keep")
		requireSafetyRejection(t, dots.TrashLocalPath(context.Background(), nil, target), "dotfiles trash root")
		if body, err := os.ReadFile(target); err != nil || string(body) != "keep" {
			t.Fatalf("rejected target changed: body=%q err=%v", body, err)
		}
	})
}
