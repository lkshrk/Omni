package dots_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/dots"
)

// ─── unlinkEntry: directory symlink pointing elsewhere ────────────────────────

// TestUnlinkEntry_DirSymlinkPointsElsewhere verifies that when the target is a
// directory symlink pointing to a path other than SourcePath, unlinkEntry
// returns OpUnlinkSkip and leaves the symlink intact.
func TestUnlinkEntry_DirSymlinkPointsElsewhere(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	// SourcePath = repo/nvim/.config/nvim (exists in repo)
	srcDir := filepath.Join(repo, "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "init.lua"), []byte("-- cfg"), 0o644); err != nil {
		t.Fatal(err)
	}

	// TargetPath = home/.config/nvim; create it as a symlink pointing ELSEWHERE.
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	otherDir := t.TempDir()
	dstLink := filepath.Join(home, ".config", "nvim")
	if err := os.Symlink(otherDir, dstLink); err != nil {
		t.Fatal(err)
	}

	m, err := dots.New(repo, []config.DotEntry{
		{Name: "nvim", Path: filepath.Join(home, ".config", "nvim")},
	})
	if err != nil {
		t.Fatalf("dots.New: %v", err)
	}

	ops, err := m.UnlinkAll(dots.UnlinkOptions{})
	if err != nil {
		t.Fatalf("UnlinkAll: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
	if ops[0].Kind != dots.OpUnlinkSkip {
		t.Errorf("op kind = %v, want OpUnlinkSkip (symlink points elsewhere)", ops[0].Kind)
	}
	// The foreign symlink must be left intact.
	if _, err := os.Lstat(dstLink); err != nil {
		t.Errorf("foreign symlink was removed: %v", err)
	}
}

// ─── unlinkEntry: source missing ─────────────────────────────────────────────

// TestUnlinkEntry_SourceMissing verifies that when SourcePath does not exist in
// the repo, unlinkEntry returns an empty op slice (nothing to do).
func TestUnlinkEntry_SourceMissing(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Source dir is never created.
	m, err := dots.New(repo, []config.DotEntry{
		{Name: "nvim", Path: filepath.Join(home, ".config", "nvim")},
	})
	if err != nil {
		t.Fatalf("dots.New: %v", err)
	}

	ops, err := m.UnlinkAll(dots.UnlinkOptions{})
	if err != nil {
		t.Fatalf("UnlinkAll: %v", err)
	}
	if len(ops) != 0 {
		t.Errorf("got %d ops, want 0 (source missing)", len(ops))
	}
}

func TestUnlinkEntry_RemoveLocalBacksUpTarget(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".xdg"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))

	srcDir := filepath.Join(repo, "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "init.lua"), []byte("-- repo"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "init.lua"), []byte("-- local"), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := dots.New(repo, []config.DotEntry{
		{Name: "nvim", Path: target},
	})
	if err != nil {
		t.Fatalf("dots.New: %v", err)
	}

	ops, err := m.UnlinkAll(dots.UnlinkOptions{RemoveLocal: true})
	if err != nil {
		t.Fatalf("UnlinkAll: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpUnlink {
		t.Fatalf("ops = %+v, want one unlink op", ops)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("target should be removed: %v", err)
	}
	backup := filepath.Join(home, dots.BackupDirName, ".config", "nvim", "init.lua")
	got, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("ReadFile backup: %v", err)
	}
	if string(got) != "-- local" {
		t.Fatalf("backup content = %q, want -- local", string(got))
	}
	trash := filepath.Join(expectedTrashRoot(t, home), "nvim", "init.lua")
	got, err = os.ReadFile(trash)
	if err != nil {
		t.Fatalf("ReadFile trash: %v", err)
	}
	if string(got) != "-- local" {
		t.Fatalf("trash content = %q, want -- local", string(got))
	}
}

func TestUnlinkEntry_RemoveLocalBacksUpManagedSymlinkContent(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".xdg"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))

	srcDir := filepath.Join(repo, "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "init.lua"), []byte("-- repo"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(srcDir, target); err != nil {
		t.Fatal(err)
	}

	m, err := dots.New(repo, []config.DotEntry{
		{Name: "nvim", Path: target},
	})
	if err != nil {
		t.Fatalf("dots.New: %v", err)
	}

	ops, err := m.UnlinkAll(dots.UnlinkOptions{RemoveLocal: true})
	if err != nil {
		t.Fatalf("UnlinkAll: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpUnlink {
		t.Fatalf("ops = %+v, want one unlink op", ops)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("target should be removed: %v", err)
	}
	backupDir := filepath.Join(home, dots.BackupDirName, ".config", "nvim")
	if _, err := os.Lstat(filepath.Join(expectedTrashRoot(t, home), "nvim")); !os.IsNotExist(err) {
		t.Fatalf("managed symlink should not move target to trash: %v", err)
	}
	if info, err := os.Lstat(backupDir); err != nil {
		t.Fatalf("Lstat backup: %v", err)
	} else if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("backup should copy managed content before remove-local, got symlink")
	}
	got, err := os.ReadFile(filepath.Join(backupDir, "init.lua"))
	if err != nil {
		t.Fatalf("ReadFile backup: %v", err)
	}
	if string(got) != "-- repo" {
		t.Fatalf("backup content = %q, want -- repo", string(got))
	}
}

func TestUnlinkEntry_RemoveLocalBacksUpBrokenManagedSymlink(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".xdg"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))

	srcDir := filepath.Join(repo, "nvim", ".config", "nvim")
	target := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(srcDir, target); err != nil {
		t.Fatal(err)
	}

	m, err := dots.New(repo, []config.DotEntry{
		{Name: "nvim", Path: target},
	})
	if err != nil {
		t.Fatalf("dots.New: %v", err)
	}

	ops, err := m.UnlinkAll(dots.UnlinkOptions{RemoveLocal: true})
	if err != nil {
		t.Fatalf("UnlinkAll: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpUnlink {
		t.Fatalf("ops = %+v, want one unlink op", ops)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("target should be removed: %v", err)
	}
	backup := filepath.Join(home, dots.BackupDirName, ".config", "nvim")
	if _, err := os.Lstat(filepath.Join(expectedTrashRoot(t, home), "nvim")); !os.IsNotExist(err) {
		t.Fatalf("broken managed symlink should not move target to trash: %v", err)
	}
	got, err := os.Readlink(backup)
	if err != nil {
		t.Fatalf("Readlink backup: %v", err)
	}
	if got != srcDir {
		t.Fatalf("backup symlink = %q, want %q", got, srcDir)
	}
}

// ─── unlinkEntry: file-level walk with ignored files ─────────────────────────

// TestUnlinkEntry_WalkIgnoresPatterns verifies that files matching per-entry
// ignore patterns are skipped during the file-level walk inside unlinkEntry.
func TestUnlinkEntry_WalkIgnoresPatterns(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Source is a file, not a directory, so the file-level path is triggered
	// when TargetPath is an existing directory (no dir-level symlink exists).
	// To hit the walk branch, SourcePath must be a directory.
	// Layout: repo/cfg/.config/ (dir), home/.config/ (real dir, not symlink)
	srcDir := filepath.Join(repo, "cfg", ".config")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "keep.conf"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	// This file matches the ignore pattern "*.log" — should be skipped.
	if err := os.WriteFile(filepath.Join(srcDir, "debug.log"), []byte("log"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Ensure TargetPath (.config) is a real dir so TargetPath itself is NOT a symlink.
	dstDir := filepath.Join(home, ".config")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}

	m, err := dots.New(repo, []config.DotEntry{
		{Name: "cfg", Path: dstDir, Ignore: []string{"*.log"}},
	})
	if err != nil {
		t.Fatalf("dots.New: %v", err)
	}

	// UnlinkAll on a real-dir target with non-symlink files → OpUnlinkSkip for each.
	ops, err := m.UnlinkAll(dots.UnlinkOptions{})
	if err != nil {
		t.Fatalf("UnlinkAll: %v", err)
	}
	// Only "keep.conf" should be visited; "debug.log" is ignored.
	for _, op := range ops {
		if op.File == "debug.log" {
			t.Errorf("ignored file debug.log appeared in ops: %v", ops)
		}
	}
}

// ─── copyDir ─────────────────────────────────────────────────────────────────

// TestCopyDir_CopiesTreeViaUnlinkAll exercises copyDir indirectly through
// the stow-managed directory-symlink unlink path. When the target IS a
// stow-managed directory symlink and opts.ConflictOverwrite would have the
// symlink removed + source tree copied, copyDir is called.
func TestCopyDir_CopiesTree(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Source tree: repo/nvim/.config/nvim/{init.lua, lua/user.lua}
	srcDir := filepath.Join(repo, "nvim", ".config", "nvim")
	if err := os.MkdirAll(filepath.Join(srcDir, "lua"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "init.lua"), []byte("-- init"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "lua", "user.lua"), []byte("-- user"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a stow-managed directory symlink at TargetPath pointing to SourcePath.
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	dstLink := filepath.Join(home, ".config", "nvim")
	if err := os.Symlink(srcDir, dstLink); err != nil {
		t.Fatal(err)
	}

	m, err := dots.New(repo, []config.DotEntry{
		{Name: "nvim", Path: dstLink},
	})
	if err != nil {
		t.Fatalf("dots.New: %v", err)
	}

	ops, err := m.UnlinkAll(dots.UnlinkOptions{})
	if err != nil {
		t.Fatalf("UnlinkAll: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpUnlink {
		t.Fatalf("got ops %v, want [OpUnlink]", ops)
	}

	// The symlink must have been replaced by a real directory tree.
	fi, err := os.Lstat(dstLink)
	if err != nil {
		t.Fatalf("Lstat %q: %v", dstLink, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Error("dstLink is still a symlink after UnlinkAll")
	}
	// Files from the source tree must exist in the copy.
	if _, err := os.Stat(filepath.Join(dstLink, "init.lua")); err != nil {
		t.Errorf("init.lua missing from copy: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dstLink, "lua", "user.lua")); err != nil {
		t.Errorf("lua/user.lua missing from copy: %v", err)
	}
}

// ─── unlinkFile: wrong symlink (points to different src) ─────────────────────

// TestUnlinkFile_WrongSymlink verifies that when a file-level symlink points to
// a path other than the expected src, it is skipped (OpUnlinkSkip).
func TestUnlinkFile_WrongSymlink(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	// SourcePath = repo/zsh/.zshrc (a file)
	src := filepath.Join(repo, "zsh", ".zshrc")
	writeFile(t, src, "# zsh")

	// The symlink at dst points to some other file, not src.
	otherFile := filepath.Join(t.TempDir(), "other.txt")
	writeFile(t, otherFile, "# other")

	dst := filepath.Join(home, ".zshrc")
	if err := os.Symlink(otherFile, dst); err != nil {
		t.Fatal(err)
	}

	m, err := dots.New(repo, []config.DotEntry{
		{Name: "zsh", Path: dst},
	})
	if err != nil {
		t.Fatalf("dots.New: %v", err)
	}

	ops, err := m.UnlinkAll(dots.UnlinkOptions{})
	if err != nil {
		t.Fatalf("UnlinkAll: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
	if ops[0].Kind != dots.OpUnlinkSkip {
		t.Errorf("op kind = %v, want OpUnlinkSkip for wrong symlink", ops[0].Kind)
	}
	// The wrong symlink must remain.
	if _, err := os.Lstat(dst); err != nil {
		t.Errorf("wrong symlink was removed: %v", err)
	}
}

// ─── unlinkFile: managed symlink replaced with real copy ─────────────────────

// TestUnlinkFile_ManagedSymlinkReplaced verifies the stow-style file link path:
// when src and dst are files (not dirs), and dst is a relative symlink pointing
// to src, UnlinkAll replaces it with a real copy and returns OpUnlink.
func TestUnlinkFile_ManagedSymlinkReplaced(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	const content = "# zsh config"
	src := filepath.Join(repo, "zsh", ".zshrc")
	dst := filepath.Join(home, ".zshrc")
	writeFile(t, src, content)

	relTarget, err := filepath.Rel(filepath.Dir(dst), src)
	if err != nil {
		t.Fatal(err)
	}
	// Stow writes relative links; this must be treated as the managed target.
	if err := os.Symlink(relTarget, dst); err != nil {
		t.Fatal(err)
	}

	m, err := dots.New(repo, []config.DotEntry{
		{Name: "zsh", Path: dst},
	})
	if err != nil {
		t.Fatalf("dots.New: %v", err)
	}

	ops, err := m.UnlinkAll(dots.UnlinkOptions{})
	if err != nil {
		t.Fatalf("UnlinkAll: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpUnlink {
		t.Fatalf("got ops %v, want [OpUnlink]", ops)
	}

	// dst should now be a real file with the same content.
	fi, err := os.Lstat(dst)
	if err != nil {
		t.Fatalf("Lstat dst: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Error("dst is still a symlink after UnlinkAll")
	}
	got, _ := os.ReadFile(dst)
	if string(got) != content {
		t.Errorf("file content = %q, want %q", string(got), content)
	}
}

// ─── dots.New: path not under home dir ───────────────────────────────────────

// TestNew_PathNotUnderHome verifies that dots.New returns an error when an
// entry path is not under the home directory (covering the rel error branch).
func TestNew_PathNotUnderHome(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Use an absolute path outside home.
	outsidePath := t.TempDir() // guaranteed different from home
	_, err := dots.New(repo, []config.DotEntry{
		{Name: "outside", Path: outsidePath},
	})
	if err == nil {
		t.Error("expected error for path not under home, got nil")
	}
}

func TestNew_ExpandsEnvironmentVariables(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	m, err := dots.New(repo, []config.DotEntry{
		{Name: "dot", Path: "$HOME/.config/zsh"},
	})
	if err != nil {
		t.Fatalf("dots.New: %v", err)
	}
	if len(m.Entries) != 1 {
		t.Fatalf("len(Entries) = %d, want 1", len(m.Entries))
	}
	if got := m.Entries[0].TargetPath; got != filepath.Join(home, ".config/zsh") {
		t.Fatalf("TargetPath = %q, want %q", got, filepath.Join(home, ".config/zsh"))
	}
}
