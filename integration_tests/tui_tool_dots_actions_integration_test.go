//go:build integration

package integration_test

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

func TestTUIToolsChangeGroupPersistsAdditionalMembership(t *testing.T) {
	bin, root, cache, configPath, env := newTUIToolActionSandbox(t)

	runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		openTUIToolActionRow(t, term)
		writeTUIKeys(t, term, "g")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("Change Groups: omni-local", "dev", "work"), "TUI did not open tool group membership")
		writeTUIKeys(t, term, "j", "j", " ", "\r")
		return waitForRequiredScreen(t, term, 8*time.Second, func(_ string) bool {
			cfg, err := config.Load(configPath)
			return err == nil && toolInConfigGroup(cfg, "work", "omni-local") && toolInConfigGroup(cfg, "dev", "omni-local")
		}, "TUI did not persist the additional tool group")
	})
}

func newTUIToolActionSandbox(t *testing.T) (bin, root, cache, configPath string, env []string) {
	t.Helper()
	bin = buildOmniBinary(t)
	root = t.TempDir()
	home := filepath.Join(root, "home")
	cache = filepath.Join(root, "cache")
	configPath = filepath.Join(root, "settings.json")
	env = isolatedTUIEnv(t, home, cache)
	if err := config.Save(configPath, &config.RootConfig{
		Version: config.CurrentVersion,
		Settings: config.Settings{
			DisabledProviders: []string{"system", "apt", "apk", "brew", "dnf", "node", "pacman", "pip", "python", "zypper"},
		},
		Tools: map[string]config.ToolSpec{
			"omni-local": {Providers: []config.ToolInstallSpec{{Provider: "apt", Package: "omni-local"}}},
		},
		Hosts: map[string][]string{"testhost": {"dev", "work"}},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "dev", Tools: []config.ToolEntry{{Name: "omni-local"}}},
			{Name: "work"},
		},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	seedTUIToolCache(t, cache, &database.ToolCache{
		Name:          "omni-local",
		Provider:      "apt",
		Package:       "omni-local",
		Installed:     true,
		InstalledWith: "apt",
		Version:       sql.NullString{String: "1.0.0", Valid: true},
		Tracked:       true,
	})
	return bin, root, cache, configPath, env
}

func openTUIToolActionRow(t *testing.T, term *vttest.Terminal) {
	t.Helper()
	waitForRequiredScreen(t, term, 6*time.Second, screenHas("Dashboard", "Tools"), "TUI did not render main tabs")
	writeTUIKeys(t, term, "\t")
	waitForRequiredScreen(t, term, 8*time.Second, screenHas("omni-local", "apt"), "TUI did not render the configured tool")
	writeTUIKeys(t, term, "j")
	waitForRequiredScreen(t, term, 3*time.Second, screenHas("omni-local", "edit groups", "ignore"), "TUI did not select the configured tool")
}

func newTUIDotActionSandbox(t *testing.T) (bin, root, cache, configPath string, env []string) {
	t.Helper()
	bin = buildOmniBinary(t)
	root = t.TempDir()
	home := filepath.Join(root, "home")
	cache = filepath.Join(root, "cache")
	configPath = filepath.Join(root, "settings.json")
	repo := filepath.Join(home, "dotfiles")
	target := filepath.Join(home, ".config", "nvim", "init.lua")
	source := filepath.Join(repo, "dotfiles", "nvim", ".config", "nvim", "init.lua")
	env = isolatedTUIEnv(t, home, cache)
	initDotsRepo(t, repo, env)
	writeIntegrationFile(t, source, "repo version\n")
	runCommand(t, repo, env, "git", "add", ".")
	runCommand(t, repo, env, "git", "commit", "-m", "add nvim dotfile")
	if err := config.Save(configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{DotsRepo: repo},
		Hosts:    map[string][]string{"testhost": {"work"}},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "work", Dots: []config.DotEntry{{Name: "nvim", Path: target}}},
		},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	return bin, root, cache, configPath, env
}

func toolInConfigGroup(cfg *config.RootConfig, groupName, toolName string) bool {
	for _, group := range cfg.Groups {
		if group == nil || group.Name != groupName {
			continue
		}
		for _, tool := range group.Tools {
			if tool.Name == toolName {
				return true
			}
		}
	}
	return false
}

func dotInConfigGroup(cfg *config.RootConfig, groupName, dotName string) bool {
	for _, group := range cfg.Groups {
		if group == nil || group.Name != groupName {
			continue
		}
		for _, dot := range group.Dots {
			if dot.Name == dotName {
				return true
			}
		}
	}
	return false
}
