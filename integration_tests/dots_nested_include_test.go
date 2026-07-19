//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func TestDotsListReportsReincludedSubfolderDrift(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	repo := filepath.Join(root, "repo")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	sourceDir := filepath.Join(repo, "dotfiles", "claude", ".claude", "hooks")
	targetDir := filepath.Join(home, ".claude", "hooks")
	for _, dir := range []string{sourceDir, targetDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create %q: %v", dir, err)
		}
	}
	writeIntegrationFile(t, filepath.Join(targetDir, "openwolf-git-gate.sh"), "local-only")
	if err := config.Save(configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{DotsRepo: repo},
		Hosts:    map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{{
			Name:    "testhost",
			Special: "host",
			Dots: []config.DotEntry{{
				Name:   "claude",
				Path:   filepath.Join(home, ".claude"),
				Ignore: []string{"*", "!/hooks/openwolf-git-gate.sh"},
			}},
		}},
	}); err != nil {
		t.Fatalf("save dots config: %v", err)
	}

	out := runOmniOutput(t, buildOmniBinary(t), root, isolatedTUIEnv(t, home, cache),
		"--config", configPath, "--cache-dir", cache, "dots", "list", "--format", "json")
	var statuses []app.DotStatus
	if err := json.Unmarshal([]byte(out), &statuses); err != nil {
		t.Fatalf("decode dots list: %v\n%s", err, out)
	}
	if len(statuses) != 1 || statuses[0].Name != "claude" {
		t.Fatalf("dots statuses = %#v, want only claude", statuses)
	}
	for _, child := range statuses[0].Children {
		if child.RelPath != "hooks" {
			continue
		}
		if child.Ignored || child.State == app.DotStateIgnored {
			t.Fatalf("hooks = %#v, want actionable state for its re-included local-only file", child)
		}
		if child.Counts.OutOfSync != 1 {
			t.Fatalf("hooks counts = %#v, want one out-of-sync descendant", child.Counts)
		}
		return
	}
	t.Fatalf("hooks child missing: %#v", statuses[0].Children)
}

func TestDotsNestedIgnoredIncludeCriticalPaths(t *testing.T) {
	for _, tc := range []struct {
		name   string
		ignore []string
	}{
		{name: "local file under rooted ignored ancestor", ignore: []string{".claude/projects"}},
		{name: "local file under basename ignored ancestor", ignore: []string{"projects"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, home, repo := newNestedIncludeApp(t, tc.ignore)
			selected := filepath.Join(home, ".claude", "projects", "session.json")
			sibling := filepath.Join(home, ".claude", "projects", "private.json")
			writeIntegrationFile(t, selected, "selected")
			writeIntegrationFile(t, sibling, "private")

			result, err := a.DotsIncludeIgnoredPathWithState(context.Background(), "claude", "projects/session.json")
			if err != nil {
				t.Fatalf("include nested file: %v", err)
			}

			assertIntegrationDotSynced(t, result.State, "claude")
			selectedSource := filepath.Join(repo, "dotfiles", "claude", ".claude", "projects", "session.json")
			assertIntegrationSymlinkTo(t, selected, selectedSource)
			assertIntegrationRegularFile(t, sibling, "private")
			assertIntegrationMissing(t, filepath.Join(repo, "dotfiles", "claude", ".claude", "projects", "private.json"))
		})
	}

	t.Run("local directory under broad ignore", func(t *testing.T) {
		a, home, repo := newNestedIncludeApp(t, []string{"*"})
		selectedOne := filepath.Join(home, ".claude", "projects", "acme", "session.json")
		selectedTwo := filepath.Join(home, ".claude", "projects", "acme", "nested", "notes.md")
		sibling := filepath.Join(home, ".claude", "projects", "other", "private.json")
		writeIntegrationFile(t, selectedOne, "session")
		writeIntegrationFile(t, selectedTwo, "notes")
		writeIntegrationFile(t, sibling, "private")

		result, err := a.DotsIncludeIgnoredPathWithState(context.Background(), "claude", "projects/acme")
		if err != nil {
			t.Fatalf("include nested directory: %v", err)
		}

		assertIntegrationDotSynced(t, result.State, "claude")
		selectedSourceOne := filepath.Join(repo, "dotfiles", "claude", ".claude", "projects", "acme", "session.json")
		selectedSourceTwo := filepath.Join(repo, "dotfiles", "claude", ".claude", "projects", "acme", "nested", "notes.md")
		assertIntegrationSymlinkTo(t, selectedOne, selectedSourceOne)
		assertIntegrationSymlinkTo(t, selectedTwo, selectedSourceTwo)
		assertIntegrationRegularFile(t, sibling, "private")
		assertIntegrationMissing(t, filepath.Join(repo, "dotfiles", "claude", ".claude", "projects", "other", "private.json"))
	})

	t.Run("repo-only directory under broad ignore", func(t *testing.T) {
		a, home, repo := newNestedIncludeApp(t, []string{"*"})
		targetOne := filepath.Join(home, ".claude", "projects", "acme", "session.json")
		targetTwo := filepath.Join(home, ".claude", "projects", "acme", "nested", "notes.md")
		sourceOne := filepath.Join(repo, "dotfiles", "claude", ".claude", "projects", "acme", "session.json")
		sourceTwo := filepath.Join(repo, "dotfiles", "claude", ".claude", "projects", "acme", "nested", "notes.md")
		siblingSource := filepath.Join(repo, "dotfiles", "claude", ".claude", "projects", "other", "private.json")
		writeIntegrationFile(t, sourceOne, "session")
		writeIntegrationFile(t, sourceTwo, "notes")
		writeIntegrationFile(t, siblingSource, "private")

		result, err := a.DotsIncludeIgnoredPathWithState(context.Background(), "claude", "projects/acme")
		if err != nil {
			t.Fatalf("include repo-only nested directory: %v", err)
		}

		assertIntegrationDotSynced(t, result.State, "claude")
		assertIntegrationSymlinkTo(t, targetOne, sourceOne)
		assertIntegrationSymlinkTo(t, targetTwo, sourceTwo)
		assertIntegrationMissing(t, filepath.Join(home, ".claude", "projects", "other", "private.json"))
		assertIntegrationMissing(t, siblingSource)
	})
}

func newNestedIncludeApp(t *testing.T, ignore []string) (*app.App, string, string) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	repo := filepath.Join(root, "repo")
	configPath := filepath.Join(root, "settings.json")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("create HOME: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("OMNI_HOSTNAME", "testhost")
	if err := config.Save(configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{DotsRepo: repo},
		Hosts:    map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{{
			Name:    "testhost",
			Special: "host",
			Dots: []config.DotEntry{{
				Name:   "claude",
				Path:   filepath.Join(home, ".claude"),
				Ignore: append([]string(nil), ignore...),
			}},
		}},
	}); err != nil {
		t.Fatalf("save dots config: %v", err)
	}

	a := app.New(configPath)
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("initialize app test mode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a, home, repo
}

func writeIntegrationFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create parent for %q: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func assertIntegrationDotSynced(t *testing.T, state *app.DotsState, name string) {
	t.Helper()
	if state == nil || !state.Loaded {
		t.Fatalf("dots state = %+v, want loaded", state)
	}
	for _, entry := range state.Entries {
		if entry.Name != name {
			continue
		}
		if entry.State != app.DotStateSynced {
			t.Fatalf("dot %q state = %q, want synced", name, entry.State)
		}
		return
	}
	t.Fatalf("dots state does not contain %q: %+v", name, state.Entries)
}

func assertIntegrationSymlinkTo(t *testing.T, linkPath, sourcePath string) {
	t.Helper()
	info, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("lstat %q: %v", linkPath, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%q mode = %v, want symlink", linkPath, info.Mode())
	}
	got, err := filepath.EvalSymlinks(linkPath)
	if err != nil {
		t.Fatalf("resolve %q: %v", linkPath, err)
	}
	want, err := filepath.EvalSymlinks(sourcePath)
	if err != nil {
		t.Fatalf("resolve source %q: %v", sourcePath, err)
	}
	if got != want {
		t.Fatalf("%q resolves to %q, want %q", linkPath, got, want)
	}
}

func assertIntegrationRegularFile(t *testing.T, path, content string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %q: %v", path, err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("%q mode = %v, want regular file", path, info.Mode())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	if string(got) != content {
		t.Fatalf("%q content = %q, want %q", path, got, content)
	}
}

func assertIntegrationMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("lstat %q error = %v, want not exist", path, err)
	}
}
