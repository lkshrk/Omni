package dots_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/dots"
)

func TestUnlinkEntry_DirSymlinkPointsElsewhere(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	srcDir := filepath.Join(repo, "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "init.lua"), []byte("-- cfg"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	otherDir := t.TempDir()
	dstLink := filepath.Join(home, ".config", "nvim")
	if err := os.Symlink(otherDir, dstLink); err != nil {
		t.Fatal(err)
	}

	m, err := dots.NewEngine(repo, []config.DotEntry{
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
	if _, err := os.Lstat(dstLink); err != nil {
		t.Errorf("foreign symlink was removed: %v", err)
	}
}

func TestUnlinkEntry_SourceMissing(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Source dir is never created.
	m, err := dots.NewEngine(repo, []config.DotEntry{
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

	m, err := dots.NewEngine(repo, []config.DotEntry{
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

	m, err := dots.NewEngine(repo, []config.DotEntry{
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

	m, err := dots.NewEngine(repo, []config.DotEntry{
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

func TestUnlinkEntry_WalkIgnoresPatterns(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	// The walk branch needs SourcePath to be a directory and TargetPath a real dir, not a symlink.
	srcDir := filepath.Join(repo, "cfg", ".config")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "keep.conf"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "debug.log"), []byte("log"), 0o644); err != nil {
		t.Fatal(err)
	}

	dstDir := filepath.Join(home, ".config")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}

	m, err := dots.NewEngine(repo, []config.DotEntry{
		{Name: "cfg", Path: dstDir, Ignore: []string{"*.log"}},
	})
	if err != nil {
		t.Fatalf("dots.New: %v", err)
	}

	ops, err := m.UnlinkAll(dots.UnlinkOptions{})
	if err != nil {
		t.Fatalf("UnlinkAll: %v", err)
	}
	for _, op := range ops {
		if op.File == "debug.log" {
			t.Errorf("ignored file debug.log appeared in ops: %v", ops)
		}
	}
}

func TestCopyDir_CopiesTree(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

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

	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	dstLink := filepath.Join(home, ".config", "nvim")
	if err := os.Symlink(srcDir, dstLink); err != nil {
		t.Fatal(err)
	}

	m, err := dots.NewEngine(repo, []config.DotEntry{
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

	fi, err := os.Lstat(dstLink)
	if err != nil {
		t.Fatalf("Lstat %q: %v", dstLink, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Error("dstLink is still a symlink after UnlinkAll")
	}
	if _, err := os.Stat(filepath.Join(dstLink, "init.lua")); err != nil {
		t.Errorf("init.lua missing from copy: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dstLink, "lua", "user.lua")); err != nil {
		t.Errorf("lua/user.lua missing from copy: %v", err)
	}
}

func TestUnlinkFile_WrongSymlink(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	src := filepath.Join(repo, "zsh", ".zshrc")
	writeFile(t, src, "# zsh")

	otherFile := filepath.Join(t.TempDir(), "other.txt")
	writeFile(t, otherFile, "# other")

	dst := filepath.Join(home, ".zshrc")
	if err := os.Symlink(otherFile, dst); err != nil {
		t.Fatal(err)
	}

	m, err := dots.NewEngine(repo, []config.DotEntry{
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
	if _, err := os.Lstat(dst); err != nil {
		t.Errorf("wrong symlink was removed: %v", err)
	}
}

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

	m, err := dots.NewEngine(repo, []config.DotEntry{
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

func TestNew_PathNotUnderHome(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	outsidePath := t.TempDir() // guaranteed different from home
	_, err := dots.NewEngine(repo, []config.DotEntry{
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

	m, err := dots.NewEngine(repo, []config.DotEntry{
		{Name: "dot", Path: "$HOME/.config/zsh"},
	})
	if err != nil {
		t.Fatalf("dots.New: %v", err)
	}
	if len(m.Entries) != 1 {
		t.Fatalf("len(Entries) = %d, want 1", len(m.Entries))
	}
	if got := m.Entries[0].TargetPath; filepath.Clean(got) != filepath.Clean(filepath.Join(home, ".config/zsh")) {
		t.Fatalf("TargetPath = %q, want %q", got, filepath.Join(home, ".config/zsh"))
	}
}

func TestNew_UsesExplicitDotPackage(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	m, err := dots.NewEngine(repo, []config.DotEntry{
		{Name: "nvim", Package: "nvim-work", Path: "~/.config/nvim"},
	})
	if err != nil {
		t.Fatalf("dots.New: %v", err)
	}
	if len(m.Entries) != 1 {
		t.Fatalf("len(Entries) = %d, want 1", len(m.Entries))
	}
	entry := m.Entries[0]
	if entry.Name != "nvim" {
		t.Fatalf("Name = %q, want nvim", entry.Name)
	}
	if entry.Package != "nvim-work" {
		t.Fatalf("Package = %q, want nvim-work", entry.Package)
	}
	wantSource := filepath.Join(repo, "nvim-work", ".config", "nvim")
	if entry.SourcePath != wantSource {
		t.Fatalf("SourcePath = %q, want %q", entry.SourcePath, wantSource)
	}
}
