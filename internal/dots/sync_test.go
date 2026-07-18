package dots

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceDotSourceFromLocal_CopyFailureKeepsOldSource(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "repo", "nvim", ".config", "nvim")
	packageRoot := filepath.Dir(filepath.Dir(sourcePath))
	if err := os.MkdirAll(sourcePath, 0o755); err != nil {
		t.Fatal(err)
	}
	oldFile := filepath.Join(sourcePath, "init.lua")
	if err := os.WriteFile(oldFile, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := replaceDotSourceFromLocal(filepath.Join(t.TempDir(), "missing"), sourcePath, packageRoot, nil)
	if err == nil {
		t.Fatal("replaceDotSourceFromLocal succeeded with missing copy source")
	}
	got, readErr := os.ReadFile(oldFile)
	if readErr != nil || string(got) != "old" {
		t.Fatalf("old source changed after copy failure: body=%q err=%v", got, readErr)
	}
}

func TestReplaceDotSourceFromLocal_FollowsNestedSymlink(t *testing.T) {
	tmp := t.TempDir()
	sourcePath := filepath.Join(tmp, "repo", "nvim", ".config", "nvim")
	copySource := filepath.Join(tmp, "home", ".config", "nvim")
	if err := os.MkdirAll(sourcePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(copySource, 0o755); err != nil {
		t.Fatal(err)
	}
	oldFile := filepath.Join(sourcePath, "init.lua")
	if err := os.WriteFile(oldFile, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copySource, "init.lua"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	externalFile := filepath.Join(tmp, "outside.lua")
	if err := os.WriteFile(externalFile, []byte("external"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(externalFile, filepath.Join(copySource, "external.lua")); err != nil {
		t.Fatal(err)
	}

	replacement, err := replaceDotSourceFromLocal(copySource, sourcePath, filepath.Join(tmp, "repo", "nvim"), nil)
	if err != nil {
		t.Fatalf("replaceDotSourceFromLocal: %v", err)
	}
	if err := replacement.commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if got, readErr := os.ReadFile(oldFile); readErr != nil || string(got) != "new" {
		t.Fatalf("regular local file copy = %q err=%v, want new", got, readErr)
	}
	got, readErr := os.ReadFile(filepath.Join(sourcePath, "external.lua"))
	if readErr != nil || string(got) != "external" {
		t.Fatalf("followed symlink copy = %q err=%v, want external content", got, readErr)
	}
	info, statErr := os.Lstat(filepath.Join(sourcePath, "external.lua"))
	if statErr != nil {
		t.Fatalf("copied external file stat: %v", statErr)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("copied external file is still a symlink")
	}
}

func TestReplaceDotSourceFromLocal_RollbackRestoresOldSource(t *testing.T) {
	tmp := t.TempDir()
	sourcePath := filepath.Join(tmp, "repo", "nvim", ".config", "nvim")
	copySource := filepath.Join(tmp, "home", ".config", "nvim")
	if err := os.MkdirAll(sourcePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(copySource, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourcePath, "init.lua"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copySource, "init.lua"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	replacement, err := replaceDotSourceFromLocal(copySource, sourcePath, filepath.Join(tmp, "repo", "nvim"), nil)
	if err != nil {
		t.Fatalf("replaceDotSourceFromLocal: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(sourcePath, "init.lua")); err != nil || string(got) != "new" {
		t.Fatalf("replacement source = %q err=%v, want new", got, err)
	}
	if err := replacement.rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(sourcePath, "init.lua")); err != nil || string(got) != "old" {
		t.Fatalf("rolled back source = %q err=%v, want old", got, err)
	}
}

func TestRestoreDotTargetAfterFailedRestow(t *testing.T) {
	t.Run("restores file backup", func(t *testing.T) {
		tmp := t.TempDir()
		home := filepath.Join(tmp, "home")
		t.Setenv("HOME", home)

		backupPath := filepath.Join(tmp, "backup.txt")
		if err := os.WriteFile(backupPath, []byte("original"), 0o644); err != nil {
			t.Fatal(err)
		}
		targetPath := filepath.Join(tmp, "target.txt")
		if err := os.WriteFile(targetPath, []byte("partial"), 0o644); err != nil {
			t.Fatal(err)
		}

		entry := ResolvedEntry{Name: "test", TargetPath: targetPath}
		prep := preparedDotTarget{backupPath: backupPath, preservedDirectory: false}

		if err := restoreDotTargetAfterFailedRestow(context.Background(), nil, entry, prep); err != nil {
			t.Fatalf("restoreDotTargetAfterFailedRestow: %v", err)
		}
		got, err := os.ReadFile(targetPath)
		if err != nil {
			t.Fatalf("read target: %v", err)
		}
		if string(got) != "original" {
			t.Fatalf("target content = %q, want %q", got, "original")
		}
	})

	t.Run("restores directory backup", func(t *testing.T) {
		tmp := t.TempDir()
		home := filepath.Join(tmp, "home")
		t.Setenv("HOME", home)

		backupPath := filepath.Join(tmp, "backup")
		if err := os.MkdirAll(filepath.Join(backupPath, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(backupPath, "sub", "file.txt"), []byte("original"), 0o644); err != nil {
			t.Fatal(err)
		}

		targetDir := filepath.Join(tmp, "targetdir")
		if err := os.MkdirAll(targetDir, 0o755); err != nil {
			t.Fatal(err)
		}
		// partial content from failed restow
		if err := os.WriteFile(filepath.Join(targetDir, "partial.txt"), []byte("partial"), 0o644); err != nil {
			t.Fatal(err)
		}

		entry := ResolvedEntry{Name: "test", TargetPath: targetDir}
		prep := preparedDotTarget{
			backupPath:          backupPath,
			preservedDirectory:  true,
			removedManagedPaths: true,
		}

		if err := restoreDotTargetAfterFailedRestow(context.Background(), nil, entry, prep); err != nil {
			t.Fatalf("restoreDotTargetAfterFailedRestow: %v", err)
		}
		got, err := os.ReadFile(filepath.Join(targetDir, "sub", "file.txt"))
		if err != nil {
			t.Fatalf("read restored file: %v", err)
		}
		if string(got) != "original" {
			t.Fatalf("restored file content = %q, want %q", got, "original")
		}
	})

	t.Run("no-op when no backup and preservedDirectory", func(t *testing.T) {
		tmp := t.TempDir()
		home := filepath.Join(tmp, "home")
		t.Setenv("HOME", home)

		entry := ResolvedEntry{Name: "test", TargetPath: filepath.Join(tmp, "target")}
		prep := preparedDotTarget{
			backupPath:          "",
			preservedDirectory:  true,
			removedManagedPaths: false,
		}

		if err := restoreDotTargetAfterFailedRestow(context.Background(), nil, entry, prep); err != nil {
			t.Fatalf("restoreDotTargetAfterFailedRestow: %v", err)
		}
	})
}
