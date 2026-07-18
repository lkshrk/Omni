//go:build integration

package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
)

func TestTUIDotConflictCancelThenUseRepo(t *testing.T) {
	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	repo := filepath.Join(home, "dotfiles")
	target := filepath.Join(home, ".config", "nvim", "init.lua")
	source := filepath.Join(repo, "dotfiles", "nvim", ".config", "nvim", "init.lua")
	backup := filepath.Join(home, "dotfiles.bkp", ".config", "nvim", "init.lua")
	env := isolatedTUIEnv(t, home, cache)

	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	initDotsRepo(t, repo, env)
	writeIntegrationFile(t, source, "repo version\n")
	runCommand(t, repo, env, "git", "add", ".")
	runCommand(t, repo, env, "git", "commit", "-m", "add nvim dotfile")
	writeIntegrationFile(t, target, "local version\n")
	localTime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(target, localTime, localTime); err != nil {
		t.Fatalf("set local timestamp: %v", err)
	}
	if err := os.Chtimes(source, localTime.Add(time.Hour), localTime.Add(time.Hour)); err != nil {
		t.Fatalf("set repo timestamp: %v", err)
	}
	if err := config.Save(configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{DotsRepo: repo},
		Hosts:    map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{{
			Name:    "testhost",
			Special: "host",
			Dots:    []config.DotEntry{{Name: "nvim", Path: target}},
		}},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "hosts", "ensure", "testhost")

	runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, func(text string) bool {
			return strings.Contains(text, "Dashboard") && strings.Contains(text, "Tools")
		}, "TUI did not render main tabs")
		writeTUIKeys(t, term, "\t", "\t")
		conflictScreen := waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "nvim") && strings.Contains(strings.ToLower(text), "conflict")
		}, "TUI did not render the nvim conflict")

		writeTUIKeys(t, term, "u")
		waitForRequiredScreen(t, term, 3*time.Second, func(text string) bool {
			return strings.Contains(text, "confirm use repo")
		}, "TUI did not arm use-repo confirmation")
		writeTUIKeys(t, term, "\x1b")
		waitForRequiredScreen(t, term, 3*time.Second, func(text string) bool {
			return strings.Contains(text, "nvim") &&
				strings.Contains(strings.ToLower(text), "conflict") &&
				!strings.Contains(text, "confirm use repo")
		}, "TUI did not cancel use-repo confirmation")
		assertRegularFileContent(t, target, "local version\n")
		if _, err := os.Stat(backup); !os.IsNotExist(err) {
			t.Fatalf("backup exists after cancelled resolution: %v", err)
		}

		writeTUIKeys(t, term, "u")
		waitForRequiredScreen(t, term, 3*time.Second, func(text string) bool {
			return strings.Contains(text, "confirm use repo")
		}, "TUI did not re-arm use-repo confirmation")
		writeTUIKeys(t, term, "u")
		waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "nvim") && strings.Contains(strings.ToLower(text), "synced")
		}, "TUI did not resolve the conflict with the repo version")
		return conflictScreen
	})

	assertSymlinkContent(t, target, "repo version\n")
	assertRegularFileContent(t, backup, "local version\n")
}

func assertRegularFileContent(t *testing.T, path, want string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("%s mode = %s, want regular file", path, info.Mode())
	}
	assertFileContent(t, path, want)
}

func assertSymlinkContent(t *testing.T, path, want string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s mode = %s, want symlink", path, info.Mode())
	}
	assertFileContent(t, path, want)
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("%s content = %q, want %q", path, got, want)
	}
}
