package dots_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/dots"
)

// writeFile creates a file with content at path, creating parent dirs.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ─── copyFile (via UnlinkAll) ─────────────────────────────────────────────────

// TestCopyFile_DstParentReadOnly verifies that when the destination's parent
// directory is read-only, copyFile returns an error surfaced from UnlinkAll.
func TestCopyFile_DstParentReadOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based permission tests not supported on Windows")
	}

	repo := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Entry: "~/.protected/.zshrc" → TargetPath=home/protected/.zshrc, SourcePath=repo/zsh/protected/.zshrc
	src := filepath.Join(repo, "zsh", "protected", ".zshrc")
	dst := filepath.Join(home, "protected", ".zshrc")
	writeFile(t, src, "# zsh")

	// Create parent dir and put the managed symlink inside it.
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(src, dst); err != nil {
		t.Fatal(err)
	}
	// Make the parent read-only so that removing the symlink fails.
	if err := os.Chmod(filepath.Dir(dst), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Dir(dst), 0o755) })

	m, err := dots.New(repo, []config.DotEntry{
		{Name: "zsh", Path: "~/protected/.zshrc"},
	})
	if err != nil {
		t.Fatalf("dots.New: %v", err)
	}

	_, err = m.UnlinkAll(dots.UnlinkOptions{})
	if err == nil {
		t.Error("expected error when dst parent is read-only, got nil")
	}
}

// TestCopyFile_SrcUnreadable verifies that copyFile returns an error when the
// source file exists but is not readable.
func TestCopyFile_SrcUnreadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based permission tests not supported on Windows")
	}

	repo := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	// SourcePath = repo/zsh/.zshrc, TargetPath = home/.zshrc
	src := filepath.Join(repo, "zsh", ".zshrc")
	dst := filepath.Join(home, ".zshrc")
	writeFile(t, src, "# zsh content")

	// Make the symlink so UnlinkAll will try to remove it and copy src→dst.
	if err := os.Symlink(src, dst); err != nil {
		t.Fatal(err)
	}
	// Make src unreadable.
	if err := os.Chmod(src, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(src, 0o644) })

	m, err := dots.New(repo, []config.DotEntry{
		{Name: "zsh", Path: "~/.zshrc"},
	})
	if err != nil {
		t.Fatalf("dots.New: %v", err)
	}

	_, err = m.UnlinkAll(dots.UnlinkOptions{})
	if err == nil {
		t.Error("expected error when src is unreadable, got nil")
	}
}

// TestCopyFile_DstReadOnly verifies that copyFile returns an error when the
// destination file exists as a regular file that is not writable (the
// ConflictOverwrite path in unlinkFile calls copyFile directly on a real file).
func TestCopyFile_DstReadOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based permission tests not supported on Windows")
	}

	repo := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	// SourcePath = repo/zsh/.zshrc, TargetPath = home/.zshrc
	src := filepath.Join(repo, "zsh", ".zshrc")
	dst := filepath.Join(home, ".zshrc")
	writeFile(t, src, "# repo version")
	writeFile(t, dst, "# local version")

	// Make dst read-only so copyFile's os.OpenFile with O_WRONLY fails.
	if err := os.Chmod(dst, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dst, 0o644) })

	m, err := dots.New(repo, []config.DotEntry{
		{Name: "zsh", Path: "~/.zshrc"},
	})
	if err != nil {
		t.Fatalf("dots.New: %v", err)
	}

	// ConflictOverwrite=true causes unlinkFile to call copyFile(src, dst)
	// even though dst is a real file (not a symlink). copyFile should fail.
	_, err = m.UnlinkAll(dots.UnlinkOptions{ConflictOverwrite: true})
	if err == nil {
		t.Error("expected error when dst is read-only, got nil")
	}
}

func TestUnlinkAll_ContinuesAfterEntryError(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	badSrc := filepath.Join(repo, "bad", ".badrc")
	badDst := filepath.Join(home, ".badrc")
	goodSrc := filepath.Join(repo, "good", ".goodrc")
	goodDst := filepath.Join(home, ".goodrc")
	writeFile(t, badSrc, "repo bad")
	writeFile(t, goodSrc, "repo good")
	if err := os.MkdirAll(badDst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(goodSrc, goodDst); err != nil {
		t.Fatal(err)
	}

	m, err := dots.New(repo, []config.DotEntry{
		{Name: "bad", Path: "~/.badrc"},
		{Name: "good", Path: "~/.goodrc"},
	})
	if err != nil {
		t.Fatalf("dots.New: %v", err)
	}

	ops, err := m.UnlinkAll(dots.UnlinkOptions{ConflictOverwrite: true})
	if err == nil {
		t.Fatal("expected aggregate unlink error")
	}
	if !strings.Contains(err.Error(), "bad") {
		t.Fatalf("error = %q, want bad entry failure", err)
	}
	if len(ops) != 1 || ops[0].Entry != "good" || ops[0].Kind != dots.OpUnlink {
		t.Fatalf("ops = %#v, want good unlink op after bad error", ops)
	}
	info, statErr := os.Lstat(goodDst)
	if statErr != nil {
		t.Fatalf("good dst missing after continuation: %v", statErr)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("good dst is still a symlink after continuation")
	}
}

func TestUnlinkAll_RefusesRepoSourceSymlink(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	srcDir := filepath.Join(repo, "nvim", ".config", "nvim")
	dstDir := filepath.Join(home, ".config", "nvim")
	writeFile(t, filepath.Join(srcDir, "init.lua"), "repo")
	secret := filepath.Join(home, "secret-token")
	writeFile(t, secret, "secret")
	if err := os.Symlink(secret, filepath.Join(srcDir, "token")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dstDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(srcDir, dstDir); err != nil {
		t.Fatal(err)
	}

	m, err := dots.New(repo, []config.DotEntry{
		{Name: "nvim", Path: "~/.config/nvim"},
	})
	if err != nil {
		t.Fatalf("dots.New: %v", err)
	}
	_, err = m.UnlinkAll(dots.UnlinkOptions{})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("UnlinkAll error = %v, want symlink refusal", err)
	}
	if got, readErr := os.ReadFile(filepath.Join(dstDir, "token")); readErr == nil {
		t.Fatalf("repo symlink target was copied into home: %q", got)
	} else if !os.IsNotExist(readErr) {
		t.Fatalf("token stat error = %v, want not exist", readErr)
	}
}

// ─── unlinkEntry (via UnlinkAll) ──────────────────────────────────────────────

// TestUnlinkEntry_TargetIsDirectory verifies that when the target path is a
// directory (not a symlink), unlinkEntry treats it as a conflict and does not
// error — it returns OpUnlinkConflict.
func TestUnlinkEntry_TargetIsDirectory(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	// SourcePath = repo/zsh/.zshrc (a file), TargetPath = home/.zshrc (will be a dir)
	src := filepath.Join(repo, "zsh", ".zshrc")
	dst := filepath.Join(home, ".zshrc") // will be created as dir below
	writeFile(t, src, "# zsh")

	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}

	m, err := dots.New(repo, []config.DotEntry{
		{Name: "zsh", Path: "~/.zshrc"},
	})
	if err != nil {
		t.Fatalf("dots.New: %v", err)
	}

	ops, err := m.UnlinkAll(dots.UnlinkOptions{ConflictOverwrite: false})
	if err != nil {
		t.Fatalf("UnlinkAll returned unexpected error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
	if ops[0].Kind != dots.OpUnlinkConflict {
		t.Errorf("op kind = %v, want OpUnlinkConflict", ops[0].Kind)
	}
}

// TestUnlinkEntry_TargetMissing verifies that unlinkEntry is idempotent when
// the target path does not exist.
func TestUnlinkEntry_TargetMissing(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	// SourcePath = repo/zsh/.zshrc (exists), TargetPath = home/.zshrc (never created)
	src := filepath.Join(repo, "zsh", ".zshrc")
	writeFile(t, src, "# zsh")
	// dst is never created.

	m, err := dots.New(repo, []config.DotEntry{
		{Name: "zsh", Path: "~/.zshrc"},
	})
	if err != nil {
		t.Fatalf("dots.New: %v", err)
	}

	ops, err := m.UnlinkAll(dots.UnlinkOptions{})
	if err != nil {
		t.Fatalf("UnlinkAll returned unexpected error for missing target: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
	if ops[0].Kind != dots.OpUnlinkSkip {
		t.Errorf("op kind = %v, want OpUnlinkSkip", ops[0].Kind)
	}
}

// ─── ExpandTilde ──────────────────────────────────────────────────────────────

// TestExpandTilde_NoTilde verifies that a path without a leading tilde is
// returned unchanged.
func TestExpandTilde_NoTilde(t *testing.T) {
	input := "/absolute/path/to/file"
	got, err := dots.ExpandTilde(input)
	if err != nil {
		t.Fatalf("ExpandTilde(%q) unexpected error: %v", input, err)
	}
	if got != input {
		t.Errorf("ExpandTilde(%q) = %q, want %q", input, got, input)
	}
}

// TestExpandTilde_RelativeNoTilde verifies that a relative path without a
// tilde is returned as-is.
func TestExpandTilde_RelativeNoTilde(t *testing.T) {
	input := "relative/path"
	got, err := dots.ExpandTilde(input)
	if err != nil {
		t.Fatalf("ExpandTilde(%q) unexpected error: %v", input, err)
	}
	if got != input {
		t.Errorf("ExpandTilde(%q) = %q, want %q", input, got, input)
	}
}

// TestExpandTilde_WithTilde verifies that a tilde prefix is replaced with the
// home directory.
func TestExpandTilde_WithTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := dots.ExpandTilde("~/.config/nvim")
	if err != nil {
		t.Fatalf("ExpandTilde unexpected error: %v", err)
	}
	want := filepath.Join(home, ".config/nvim")
	if got != want {
		t.Errorf("ExpandTilde = %q, want %q", got, want)
	}
}

func TestExpandTilde_WithDoubleSlash(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := dots.ExpandTilde("~//.config//nvim")
	if err != nil {
		t.Fatalf("ExpandTilde(~//.config//nvim) unexpected error: %v", err)
	}
	want := filepath.Join(home, ".config", "nvim")
	if got != want {
		t.Errorf("ExpandTilde(%q) = %q, want %q", "~//.config//nvim", got, want)
	}
}

func TestExpandTilde_WithUnsupportedPrefix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	in := "~user/.config/nvim"
	got, err := dots.ExpandTilde(in)
	if err != nil {
		t.Fatalf("ExpandTilde(%q) unexpected error: %v", in, err)
	}
	if got != in {
		t.Errorf("ExpandTilde(%q) = %q, want %q", in, got, in)
	}
}

func TestExpandPath_WithEnvironmentVariable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := dots.ExpandPath("$HOME/.config/nvim")
	if err != nil {
		t.Fatalf("ExpandPath($HOME/.config/nvim) unexpected error: %v", err)
	}
	want := filepath.Join(home, ".config", "nvim")
	if got != want {
		t.Errorf("ExpandPath(%q) = %q, want %q", "$HOME/.config/nvim", got, want)
	}
}

func TestExpandPath_DoesNotExpandUnsupportedTildePrefix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := dots.ExpandPath("~nobody/.config/nvim")
	if err != nil {
		t.Fatalf("ExpandPath(~nobody/.config/nvim) unexpected error: %v", err)
	}
	if got != "~nobody/.config/nvim" {
		t.Errorf("ExpandPath(~nobody/.config/nvim) = %q, want %q", got, "~nobody/.config/nvim")
	}
}

func TestExpandTilde_WithBackslashPrefix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if runtime.GOOS == "windows" {
		t.Skip("windows uses backslash-supported tilde prefix semantics")
	}

	in := `~\foo/.config/nvim`
	got, err := dots.ExpandTilde(in)
	if err != nil {
		t.Fatalf("ExpandTilde(%q) unexpected error: %v", in, err)
	}

	if got != in {
		t.Errorf("ExpandTilde(%q) = %q, want %q", in, got, in)
	}
}

// TestExpandTilde_EmptyPath verifies that an empty string is returned as-is
// (no tilde prefix, so no expansion needed).
func TestExpandTilde_EmptyPath(t *testing.T) {
	got, err := dots.ExpandTilde("")
	if err != nil {
		t.Fatalf("ExpandTilde(\"\") unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("ExpandTilde(\"\") = %q, want empty string", got)
	}
}

// TestExpandTilde_TildeOnly verifies that a bare "~" expands to the home dir.
func TestExpandTilde_TildeOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := dots.ExpandTilde("~")
	if err != nil {
		t.Fatalf("ExpandTilde(\"~\") unexpected error: %v", err)
	}
	// filepath.Join(home, "") == home
	if got != home {
		t.Errorf("ExpandTilde(\"~\") = %q, want %q", got, home)
	}
}

func TestValidateEntryName_RejectsPathComponents(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../evil", "nested/pkg", `nested\pkg`} {
		if err := dots.ValidateEntryName(name); err == nil {
			t.Fatalf("ValidateEntryName(%q) error = nil, want error", name)
		}
	}
	if err := dots.ValidateEntryName("nvim"); err != nil {
		t.Fatalf("ValidateEntryName(nvim): %v", err)
	}
}

func TestNew_AllowsHomeLocalPathStartingWithDots(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	t.Setenv("HOME", home)

	if _, err := dots.New(repo, []config.DotEntry{{Name: "dotconfig", Path: filepath.Join(home, "..config")}}); err != nil {
		t.Fatalf("dots.New with home-local ..config path: %v", err)
	}
}
