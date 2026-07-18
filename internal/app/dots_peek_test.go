package app_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/dots"
)

func TestDotsPeekShowsRepoToLocalDiffForConflict(t *testing.T) {
	a, _, repoDir := newDotsApp(t)
	home := os.Getenv("HOME")
	repoPath := filepath.Join(repoDir, "dotfiles", "gitconfig", ".gitconfig")
	localPath := filepath.Join(home, ".gitconfig")
	mustWriteFile(t, repoPath, "[user]\n\tname = repo\n")
	mustWriteFile(t, localPath, "[user]\n\tname = local\n")

	got, err := a.DotsPeek(context.Background(), app.DotsPeekRequest{
		Entry: app.DotStatus{
			Name:       "gitconfig",
			SourcePath: repoPath,
			TargetPath: localPath,
			State:      dots.StateConflict,
		},
	})
	if err != nil {
		t.Fatalf("DotsPeek: %v", err)
	}
	if got.Mode != app.DotsPeekModeDiff {
		t.Fatalf("mode = %q, want %q", got.Mode, app.DotsPeekModeDiff)
	}
	if got.Repo.Source != app.DotsPeekSourceRepo || got.Local.Source != app.DotsPeekSourceLocal {
		t.Fatalf("sources = repo:%q local:%q", got.Repo.Source, got.Local.Source)
	}
	for _, want := range []string{
		"--- repo\t" + repoPath,
		"+++ local\t" + localPath,
		"-\tname = repo",
		"+\tname = local",
	} {
		if !strings.Contains(got.Content, want) {
			t.Fatalf("diff missing %q:\n%s", want, got.Content)
		}
	}
}

func TestDotsPeekChildReadsLocalOnlyFile(t *testing.T) {
	a, _, repoDir := newDotsApp(t)
	home := os.Getenv("HOME")
	repoRoot := filepath.Join(repoDir, "dotfiles", "nvim", ".config", "nvim")
	localRoot := filepath.Join(home, ".config", "nvim")
	localPath := filepath.Join(localRoot, "init.lua")
	mustWriteFile(t, localPath, "vim.opt.number = true\n")

	got, err := a.DotsPeek(context.Background(), app.DotsPeekRequest{
		Entry: app.DotStatus{
			Name:       "nvim",
			SourcePath: repoRoot,
			TargetPath: localRoot,
			State:      dots.StateSynced,
			IsDir:      true,
		},
		Child: &app.DotChild{
			Name:    "init.lua",
			RelPath: "init.lua",
			Path:    localPath,
			State:   dots.StateLocalOnly,
		},
	})
	if err != nil {
		t.Fatalf("DotsPeek: %v", err)
	}
	if got.Mode != app.DotsPeekModeText {
		t.Fatalf("mode = %q, want %q", got.Mode, app.DotsPeekModeText)
	}
	if got.Source != app.DotsPeekSourceLocal {
		t.Fatalf("source = %q, want %q", got.Source, app.DotsPeekSourceLocal)
	}
	if got.Repo.Path != filepath.Join(repoRoot, "init.lua") {
		t.Fatalf("repo path = %q", got.Repo.Path)
	}
	if got.Content != "vim.opt.number = true\n" {
		t.Fatalf("content = %q", got.Content)
	}
}

func TestDotsPeekOversizedDiffReturnsMetadata(t *testing.T) {
	a, _, repoDir := newDotsApp(t)
	home := os.Getenv("HOME")
	repoPath := filepath.Join(repoDir, "dotfiles", "zshrc", ".zshrc")
	localPath := filepath.Join(home, ".zshrc")
	mustWriteFile(t, repoPath, strings.Repeat("r", app.DotsPeekReadLimit+1))
	mustWriteFile(t, localPath, strings.Repeat("l", app.DotsPeekReadLimit+1))

	got, err := a.DotsPeek(context.Background(), app.DotsPeekRequest{
		Entry: app.DotStatus{Name: "zshrc", SourcePath: repoPath, TargetPath: localPath, State: dots.StateModified},
	})
	if err != nil {
		t.Fatalf("DotsPeek: %v", err)
	}
	if got.Mode != app.DotsPeekModeMetadata {
		t.Fatalf("mode = %q, want %q", got.Mode, app.DotsPeekModeMetadata)
	}
	if !got.Repo.Truncated || !got.Local.Truncated {
		t.Fatalf("truncated flags = repo:%v local:%v", got.Repo.Truncated, got.Local.Truncated)
	}
	if got.Content != "" {
		t.Fatalf("content = %q, want empty metadata-only result", got.Content)
	}
	if !strings.Contains(got.Notice, "too large") {
		t.Fatalf("notice = %q, want size explanation", got.Notice)
	}
}

func TestDotsPeekOversizedSingleFileTruncatesText(t *testing.T) {
	a, _, repoDir := newDotsApp(t)
	home := os.Getenv("HOME")
	repoPath := filepath.Join(repoDir, "dotfiles", "zshrc", ".zshrc")
	localPath := filepath.Join(home, ".zshrc")
	mustWriteFile(t, localPath, strings.Repeat("l", app.DotsPeekReadLimit+1))

	got, err := a.DotsPeek(context.Background(), app.DotsPeekRequest{
		Entry: app.DotStatus{Name: "zshrc", SourcePath: repoPath, TargetPath: localPath, State: dots.StateLocalOnly},
	})
	if err != nil {
		t.Fatalf("DotsPeek: %v", err)
	}
	if got.Mode != app.DotsPeekModeText {
		t.Fatalf("mode = %q, want %q", got.Mode, app.DotsPeekModeText)
	}
	if got.Source != app.DotsPeekSourceLocal {
		t.Fatalf("source = %q, want %q", got.Source, app.DotsPeekSourceLocal)
	}
	if !got.Truncated || !got.Local.Truncated {
		t.Fatalf("truncated flags = result:%v local:%v", got.Truncated, got.Local.Truncated)
	}
	if len(got.Content) != app.DotsPeekReadLimit {
		t.Fatalf("content length = %d, want %d", len(got.Content), app.DotsPeekReadLimit)
	}
}

func TestDotsPeekBinaryReturnsMetadata(t *testing.T) {
	a, _, repoDir := newDotsApp(t)
	home := os.Getenv("HOME")
	repoPath := filepath.Join(repoDir, "dotfiles", "secret", ".secret")
	localPath := filepath.Join(home, ".secret")
	mustWriteFile(t, localPath, "one\x00two")

	got, err := a.DotsPeek(context.Background(), app.DotsPeekRequest{
		Entry: app.DotStatus{Name: "secret", SourcePath: repoPath, TargetPath: localPath, State: dots.StateLocalOnly},
	})
	if err != nil {
		t.Fatalf("DotsPeek: %v", err)
	}
	if got.Mode != app.DotsPeekModeMetadata {
		t.Fatalf("mode = %q, want %q", got.Mode, app.DotsPeekModeMetadata)
	}
	if !got.Local.Binary {
		t.Fatalf("local binary = false, want true")
	}
	if got.Content != "" {
		t.Fatalf("content = %q, want empty metadata-only result", got.Content)
	}
}

func TestDotsPeekLocalSymlinkFollowsTarget(t *testing.T) {
	a, _, repoDir := newDotsApp(t)
	home := os.Getenv("HOME")
	repoPath := filepath.Join(repoDir, "dotfiles", "zshrc", ".zshrc")
	targetPath := filepath.Join(home, "actual-zshrc")
	localPath := filepath.Join(home, ".zshrc")
	mustWriteFile(t, targetPath, "export EDITOR=vim\n")
	if err := os.Symlink(targetPath, localPath); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	got, err := a.DotsPeek(context.Background(), app.DotsPeekRequest{
		Entry: app.DotStatus{Name: "zshrc", SourcePath: repoPath, TargetPath: localPath, State: dots.StateSynced},
	})
	if err != nil {
		t.Fatalf("DotsPeek: %v", err)
	}
	if got.Mode != app.DotsPeekModeText || got.Source != app.DotsPeekSourceLocal {
		t.Fatalf("mode/source = %q/%q, want text/local", got.Mode, got.Source)
	}
	if got.Content != "export EDITOR=vim\n" {
		t.Fatalf("content = %q", got.Content)
	}
	if got.Local.SymlinkTarget != targetPath {
		t.Fatalf("local symlink target = %q, want %q", got.Local.SymlinkTarget, targetPath)
	}
}

func TestDotsPeekRepoSymlinkOutsideRepoReturnsMetadata(t *testing.T) {
	a, _, repoDir := newDotsApp(t)
	outsidePath := filepath.Join(t.TempDir(), "outside")
	repoPath := filepath.Join(repoDir, "dotfiles", "secret", ".secret")
	mustWriteFile(t, outsidePath, "outside\n")
	if err := os.MkdirAll(filepath.Dir(repoPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Symlink(outsidePath, repoPath); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	got, err := a.DotsPeek(context.Background(), app.DotsPeekRequest{
		Entry: app.DotStatus{Name: "secret", SourcePath: repoPath, TargetPath: filepath.Join(os.Getenv("HOME"), ".secret"), State: dots.StateRepoOnly},
	})
	if err != nil {
		t.Fatalf("DotsPeek: %v", err)
	}
	if got.Mode != app.DotsPeekModeMetadata {
		t.Fatalf("mode = %q, want %q", got.Mode, app.DotsPeekModeMetadata)
	}
	if !strings.Contains(got.Repo.Notice, "outside") {
		t.Fatalf("repo notice = %q, want outside-repo explanation", got.Repo.Notice)
	}
}

func TestDotsPeekRejectsDirectoryRequest(t *testing.T) {
	a, _, repoDir := newDotsApp(t)
	repoPath := filepath.Join(repoDir, "dotfiles", "nvim", ".config", "nvim")
	localPath := filepath.Join(os.Getenv("HOME"), ".config", "nvim")

	_, err := a.DotsPeek(context.Background(), app.DotsPeekRequest{
		Entry: app.DotStatus{Name: "nvim", SourcePath: repoPath, TargetPath: localPath, State: dots.StateSynced, IsDir: true},
	})
	if err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("error = %v, want directory rejection", err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}
