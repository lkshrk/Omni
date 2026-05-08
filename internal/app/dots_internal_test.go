package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/dots"
)

func TestLstatOp_RejectsSymlinkToRepoPrefixSibling(t *testing.T) {
	repoDir := filepath.Join(t.TempDir(), "repo")
	siblingDir := repoDir + "-other"
	srcDir := filepath.Join(siblingDir, "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "nvim")
	if err := os.Symlink(srcDir, dst); err != nil {
		t.Fatal(err)
	}

	op := lstatOp("nvim", dst, repoDir, false)
	if op.Kind != dots.OpConflict {
		t.Fatalf("lstatOp kind = %v, want OpConflict for repo prefix sibling", op.Kind)
	}
}

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

func TestRollbackDotsAdd_RemovesPartialTargetBeforeRestore(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)

	targetPath := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetPath, "init.lua"), []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	backupPath, err := dots.BackupLocalPath(targetPath)
	if err != nil {
		t.Fatalf("BackupLocalPath: %v", err)
	}

	if err := os.RemoveAll(targetPath); err != nil {
		t.Fatal(err)
	}
	partialSource := filepath.Join(tmp, "repo", "dotfiles", "nvim", ".config", "nvim")
	if err := os.MkdirAll(partialSource, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(partialSource, targetPath); err != nil {
		t.Fatal(err)
	}
	packagePath := filepath.Join(tmp, "repo", "dotfiles", "nvim", ".config", "nvim")

	if err := rollbackDotsAdd(targetPath, packagePath, backupPath); err != nil {
		t.Fatalf("rollbackDotsAdd: %v", err)
	}

	info, err := os.Lstat(targetPath)
	if err != nil {
		t.Fatalf("restored target stat: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("target is still a symlink after rollback")
	}
	got, err := os.ReadFile(filepath.Join(targetPath, "init.lua"))
	if err != nil {
		t.Fatalf("read restored file: %v", err)
	}
	if string(got) != "original" {
		t.Fatalf("restored file = %q, want original", got)
	}
	if _, err := os.Lstat(packagePath); !os.IsNotExist(err) {
		t.Fatalf("package path still exists after rollback, stat err=%v", err)
	}
}

func TestDotsTestTargetPath_LeavesUnsupportedTildePrefixUntouched(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)

	startWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(tmp, "cwd")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(startWD)
	})

	got, err := dotsTestTargetPath("~user/.zshrc")
	if err != nil {
		t.Fatalf("dotsTestTargetPath: %v", err)
	}
	if strings.HasPrefix(filepath.Clean(got), filepath.Clean(home)+string(os.PathSeparator)) {
		t.Fatalf("got %q, want path outside HOME %q", got, home)
	}
}
