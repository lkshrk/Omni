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

func TestCountIgnoredTree_DepthGuardPreventsRunaway(t *testing.T) {
	// Build a chain deeper than countIgnoredTreeMaxDepth to verify
	// the depth guard returns 0 instead of overflowing the stack.
	leaf := DotChild{Name: "file.txt", IsDir: false}
	for i := range countIgnoredTreeMaxDepth + 10 {
		leaf = DotChild{
			Name:     "d" + string(rune('0'+i%10)),
			IsDir:    true,
			Children: []DotChild{leaf},
		}
	}
	got := countIgnoredTree(leaf)
	if got != 0 {
		t.Fatalf("countIgnoredTree = %d, want 0 for tree deeper than maxDepth", got)
	}
}

func TestCountIgnoredTree_NormalDepthWorks(t *testing.T) {
	tree := DotChild{
		Name:  "root",
		IsDir: true,
		Children: []DotChild{
			{Name: "a.txt"},
			{Name: "sub", IsDir: true, Children: []DotChild{
				{Name: "b.txt"},
				{Name: "c.txt"},
			}},
		},
	}
	got := countIgnoredTree(tree)
	if got != 3 {
		t.Fatalf("countIgnoredTree = %d, want 3", got)
	}
}

func TestCountIgnoredTree_LeafDirCountsAsOne(t *testing.T) {
	// An ignored leaf directory (e.g. node_modules) with no sub-tree
	// should count as 1, not 0.
	tree := DotChild{
		Name:  "root",
		IsDir: true,
		Children: []DotChild{
			{Name: "a.txt"},
			{Name: "node_modules", IsDir: true, Ignored: true}, // leaf dir
		},
	}
	got := countIgnoredTree(tree)
	if got != 2 {
		t.Fatalf("countIgnoredTree = %d, want 2 (file + leaf dir)", got)
	}
}

// TestBuildIgnoredChildTree_IntermediateNotIgnored verifies that intermediate
// directories synthesized by buildIgnoredChildTree have Ignored=false while
// leaf entries (explicitly ignored files) keep Ignored=true.
func TestBuildIgnoredChildTree_IntermediateNotIgnored(t *testing.T) {
	flat := []DotChild{
		{Name: "a", RelPath: "claude/a", Path: "/home/.config/claude/a", Ignored: true, Depth: 2},
		{Name: "b", RelPath: "claude/b", Path: "/home/.config/claude/b", Ignored: true, Depth: 2},
	}
	result := buildIgnoredChildTree(flat, "/home/.config")

	if len(result) != 1 {
		t.Fatalf("expected 1 top-level child (claude dir), got %d", len(result))
	}
	claude := result[0]
	if claude.Name != "claude" {
		t.Errorf("top-level child name = %q, want %q", claude.Name, "claude")
	}
	if !claude.IsDir {
		t.Errorf("intermediate dir 'claude' should have IsDir=true")
	}
	if claude.Ignored {
		t.Errorf("intermediate dir 'claude' should have Ignored=false, got Ignored=true")
	}
	if claude.Counts.Ignored != 2 {
		t.Errorf("intermediate dir 'claude' Counts.Ignored = %d, want 2", claude.Counts.Ignored)
	}
	if claude.FileCount != 2 {
		t.Errorf("intermediate dir 'claude' FileCount = %d, want 2", claude.FileCount)
	}
	if len(claude.Children) != 2 {
		t.Fatalf("expected 2 children under claude, got %d", len(claude.Children))
	}
	for _, leaf := range claude.Children {
		if !leaf.Ignored {
			t.Errorf("leaf %q should have Ignored=true", leaf.Name)
		}
		if leaf.Counts.Ignored != 1 {
			t.Errorf("leaf %q Counts.Ignored = %d, want 1", leaf.Name, leaf.Counts.Ignored)
		}
	}
}

// TestBuildIgnoredChildTree_FlatLeaves verifies that flat ignored children
// (no intermediate dirs) are returned as-is with Ignored preserved.
func TestBuildIgnoredChildTree_FlatLeaves(t *testing.T) {
	flat := []DotChild{
		{Name: "foo", RelPath: "foo", Path: "/home/foo", Ignored: true, Depth: 1},
		{Name: "bar", RelPath: "bar", Path: "/home/bar", Ignored: true, Depth: 1},
	}
	result := buildIgnoredChildTree(flat, "/home")

	if len(result) != 2 {
		t.Fatalf("expected 2 top-level children, got %d", len(result))
	}
	for _, child := range result {
		if !child.Ignored {
			t.Errorf("flat leaf %q should have Ignored=true", child.Name)
		}
		if child.IsDir {
			t.Errorf("flat leaf %q should have IsDir=false", child.Name)
		}
	}
}

// TestBuildIgnoredChildTree_DeepTree verifies multi-level nesting: only the
// deepest leaf is Ignored=true; all intermediate dirs are Ignored=false.
func TestBuildIgnoredChildTree_DeepTree(t *testing.T) {
	flat := []DotChild{
		{Name: "config.toml", RelPath: "a/b/config.toml", Path: "/root/a/b/config.toml", Ignored: true, Depth: 3},
	}
	result := buildIgnoredChildTree(flat, "/root")

	if len(result) != 1 {
		t.Fatalf("expected 1 top-level dir 'a', got %d", len(result))
	}
	a := result[0]
	if a.Ignored {
		t.Errorf("intermediate 'a' should have Ignored=false")
	}
	if len(a.Children) != 1 {
		t.Fatalf("expected 1 child under 'a', got %d", len(a.Children))
	}
	b := a.Children[0]
	if b.Ignored {
		t.Errorf("intermediate 'b' should have Ignored=false")
	}
	if len(b.Children) != 1 {
		t.Fatalf("expected 1 child under 'b', got %d", len(b.Children))
	}
	leaf := b.Children[0]
	if !leaf.Ignored {
		t.Errorf("leaf 'config.toml' should have Ignored=true")
	}
}
