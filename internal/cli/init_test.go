package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/app"
)

// makeRepoTree creates a temporary directory tree from a slice of paths.
// Paths ending in "/" are directories; all others are empty files.
func makeRepoTree(t *testing.T, paths []string) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	for _, p := range paths {
		full := filepath.Join(root, "dotfiles", filepath.FromSlash(p))
		if p[len(p)-1] == '/' {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

func TestDiscoverDotsEntries_IgnoresRepoRootFiles(t *testing.T) {
	root := makeRepoTree(t, []string{"nvim/"})
	if err := os.WriteFile(filepath.Join(root, "README.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := app.DiscoverDotsEntries(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Name != "nvim" {
		t.Fatalf("entries = %#v, want only nvim from dotfiles dir", got)
	}
}

func TestDiscoverDotsEntries_NonHiddenDirs(t *testing.T) {
	root := makeRepoTree(t, []string{"nvim/", "kitty/", "fish/"})
	got, err := app.DiscoverDotsEntries(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]string{
		"nvim":  "~/.config/nvim",
		"kitty": "~/.config/kitty",
		"fish":  "~/.config/fish",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got), len(want))
	}
	for _, e := range got {
		if path, ok := want[e.Name]; !ok {
			t.Errorf("unexpected entry %q", e.Name)
		} else if e.Path != path {
			t.Errorf("entry %q: path = %q, want %q", e.Name, e.Path, path)
		}
	}
}

func TestDiscoverDotsEntries_ShellConfigs_Bare(t *testing.T) {
	// Files stored without a leading dot (common dotfiles convention).
	root := makeRepoTree(t, []string{"zshrc", "bashrc", "gitconfig"})
	got, err := app.DiscoverDotsEntries(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]string{
		"zshrc":     "~/.zshrc",
		"bashrc":    "~/.bashrc",
		"gitconfig": "~/.gitconfig",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %v", len(got), len(want), got)
	}
	for _, e := range got {
		if path, ok := want[e.Name]; !ok {
			t.Errorf("unexpected entry %q", e.Name)
		} else if e.Path != path {
			t.Errorf("entry %q: path = %q, want %q", e.Name, e.Path, path)
		}
	}
}

func TestDiscoverDotsEntries_ShellConfigs_DotPrefixed(t *testing.T) {
	// Files stored with a leading dot — same canonical name and path as bare.
	root := makeRepoTree(t, []string{".zshrc", ".bashrc", ".gitconfig"})
	got, err := app.DiscoverDotsEntries(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]string{
		"zshrc":     "~/.zshrc",
		"bashrc":    "~/.bashrc",
		"gitconfig": "~/.gitconfig",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %v", len(got), len(want), got)
	}
	for _, e := range got {
		if path, ok := want[e.Name]; !ok {
			t.Errorf("unexpected entry %q", e.Name)
		} else if e.Path != path {
			t.Errorf("entry %q: path = %q, want %q", e.Name, e.Path, path)
		}
	}
}

func TestDiscoverDotsEntries_WellKnownDirs(t *testing.T) {
	// Well-known dirs (claude, ssh) should map to home, not ~/.config.
	root := makeRepoTree(t, []string{"claude/", ".ssh/", "nvim/"})
	got, err := app.DiscoverDotsEntries(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	byName := make(map[string]string, len(got))
	for _, e := range got {
		byName[e.Name] = e.Path
	}
	if byName["claude"] != "~/.claude" {
		t.Errorf("claude path = %q, want ~/.claude", byName["claude"])
	}
	if byName["ssh"] != "~/.ssh" {
		t.Errorf("ssh path = %q, want ~/.ssh", byName["ssh"])
	}
	if byName["nvim"] != "~/.config/nvim" {
		t.Errorf("nvim path = %q, want ~/.config/nvim", byName["nvim"])
	}
}

func TestDiscoverDotsEntries_SkipsPlainFiles_NonWellKnown(t *testing.T) {
	// Plain files with no well-known mapping should be skipped.
	root := makeRepoTree(t, []string{"README.md", "setup.sh", "nvim/"})
	got, err := app.DiscoverDotsEntries(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	byName := make(map[string]string, len(got))
	for _, e := range got {
		byName[e.Name] = e.Path
	}
	if _, ok := byName["README"]; ok {
		t.Error("README.md should not produce an entry")
	}
	if _, ok := byName["setup"]; ok {
		t.Error("setup.sh should not produce an entry")
	}
	if byName["nvim"] != "~/.config/nvim" {
		t.Errorf("nvim path = %q, want ~/.config/nvim", byName["nvim"])
	}
}

func TestDiscoverDotsEntries_SkipsHiddenDirs_NonWellKnown(t *testing.T) {
	// Hidden dirs with no well-known mapping should be skipped.
	root := makeRepoTree(t, []string{".git/", ".local/", "nvim/"})
	got, err := app.DiscoverDotsEntries(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	byName := make(map[string]string, len(got))
	for _, e := range got {
		byName[e.Name] = e.Path
	}
	if _, ok := byName["git"]; ok {
		t.Error(".git should not produce an entry")
	}
	if _, ok := byName["local"]; ok {
		t.Error(".local should not produce an entry (not in wellKnownHome)")
	}
}

func TestDiscoverDotsEntries_BadPath(t *testing.T) {
	got, err := app.DiscoverDotsEntries("/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Errorf("expected error for bad path, got nil (entries: %v)", got)
	}
	if len(got) != 0 {
		t.Errorf("expected nil entries on error, got %v", got)
	}
}
