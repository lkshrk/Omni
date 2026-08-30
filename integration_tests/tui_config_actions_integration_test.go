//go:build integration

package integration_test

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
)

func TestTUIGroupsCreatePersistsNewGroup(t *testing.T) {
	bin, root, cache, configPath, env := newTUIConfigActionSandbox(t, false)

	runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		openTUIConfigGroups(t, term)
		writeTUIKeys(t, term, "n")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("New Group", "group name"), "TUI did not open group creation")
		writeTUIKeys(t, term, "created", "\r")
		return waitForRequiredScreen(t, term, 8*time.Second, func(_ string) bool {
			cfg, err := config.Load(configPath)
			return err == nil && configHasGroup(cfg, "created") && !slices.Contains(cfg.Hosts["testhost"], "created")
		}, "TUI did not persist the new group")
	})
}

func TestTUIGroupsRenamePersistsNewName(t *testing.T) {
	bin, root, cache, configPath, env := newTUIConfigActionSandbox(t, false)

	runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		openTUIConfigGroups(t, term)
		selectTUIConfigGroup(t, term, "alpha")
		writeTUIKeys(t, term, "r")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("Rename:", "alpha"), "TUI did not open group rename")
		writeTUIKeys(t, term, "\x7f", "\x7f", "\x7f", "\x7f", "\x7f", "beta", "\r")
		return waitForRequiredScreen(t, term, 8*time.Second, func(_ string) bool {
			cfg, err := config.Load(configPath)
			return err == nil && configHasGroup(cfg, "beta") && !configHasGroup(cfg, "alpha")
		}, "TUI did not persist the renamed group")
	})
}

func TestTUIGroupsDeleteRemovesEmptyGroup(t *testing.T) {
	bin, root, cache, configPath, env := newTUIConfigActionSandbox(t, false)

	runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		openTUIConfigGroups(t, term)
		selectTUIConfigGroup(t, term, "alpha")
		writeTUIKeys(t, term, "d")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("alpha", "No tools or dotfiles belong to this group", "delete"), "TUI did not open group deletion")
		writeTUIKeys(t, term, "\r")
		return waitForRequiredScreen(t, term, 8*time.Second, func(_ string) bool {
			cfg, err := config.Load(configPath)
			return err == nil && !configHasGroup(cfg, "alpha")
		}, "TUI did not delete the empty group")
	})
}

func TestTUIHostsCreatePersistsFreshHost(t *testing.T) {
	bin, root, cache, configPath, env := newTUIConfigActionSandbox(t, false)

	runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		openTUIConfigGroups(t, term)
		writeTUIKeys(t, term, "p")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("New Host", "hostname"), "TUI did not open host creation")
		writeTUIKeys(t, term, "remote", "\r")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("Copy another host's config?", "start fresh"), "TUI did not offer host initialization")
		writeTUIKeys(t, term, "n")
		return waitForRequiredScreen(t, term, 8*time.Second, func(_ string) bool {
			cfg, err := config.Load(configPath)
			if err != nil {
				return false
			}
			_, ok := cfg.Hosts["remote"]
			return ok
		}, "TUI did not persist the new host")
	})
}

func TestTUIHostsDeleteRemovesSelectedHost(t *testing.T) {
	bin, root, cache, configPath, env := newTUIConfigActionSandbox(t, true)

	runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		openTUIConfigGroups(t, term)
		writeTUIKeys(t, term, "j", "j")
		waitForRequiredScreen(t, term, 3*time.Second, func(text string) bool {
			return screenHas("remote", "delete")(text) && !screenHas("> testhost")(text)
		}, "TUI did not select the removable host")
		writeTUIKeys(t, term, "d")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("remote", "press d again to delete"), "TUI did not arm host deletion")
		writeTUIKeys(t, term, "d")
		return waitForRequiredScreen(t, term, 8*time.Second, func(_ string) bool {
			cfg, err := config.Load(configPath)
			if err != nil {
				return false
			}
			_, ok := cfg.Hosts["remote"]
			return !ok
		}, "TUI did not delete the selected host")
	})
}

func newTUIConfigActionSandbox(t *testing.T, secondHost bool) (bin, root, cache, configPath string, env []string) {
	t.Helper()
	bin = buildOmniBinary(t)
	root = t.TempDir()
	home := filepath.Join(root, "home")
	cache = filepath.Join(root, "cache")
	configPath = filepath.Join(root, "settings.json")
	env = isolatedTUIEnv(t, home, cache)
	hosts := map[string][]string{"testhost": {}}
	groups := []*config.GroupConfig{{Name: "testhost", Special: "host"}, {Name: "alpha"}}
	if secondHost {
		hosts["remote"] = nil
		groups = append(groups, &config.GroupConfig{Name: "remote", Special: "host"})
	}
	if err := config.Save(configPath, &config.RootConfig{
		Version: config.CurrentVersion,
		Settings: config.Settings{
			DisabledProviders: []string{"system", "apt", "apk", "brew", "dnf", "node", "pacman", "pip", "python", "zypper"},
		},
		Hosts:  hosts,
		Groups: groups,
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	return bin, root, cache, configPath, env
}

func openTUIConfigGroups(t *testing.T, term *vttest.Terminal) {
	t.Helper()
	waitForRequiredScreen(t, term, 6*time.Second, screenHas("Dashboard", "Tools"), "TUI did not render main tabs")
	writeTUIKeys(t, term, "\t", "\t", "\t", "\t")
	waitForRequiredScreen(t, term, 8*time.Second, screenHas("Group Assignments", "testhost", "alpha"), "TUI did not render group assignments")
}

func selectTUIConfigGroup(t *testing.T, term *vttest.Terminal, name string) {
	t.Helper()
	writeTUIKeys(t, term, "j", "j", "j")
	waitForRequiredScreen(t, term, 3*time.Second, func(text string) bool {
		return screenHas(name, "rename", "delete")(text)
	}, "TUI did not select group "+name)
}

func configHasGroup(cfg *config.RootConfig, name string) bool {
	for _, group := range cfg.Groups {
		if group != nil && group.Name == name {
			return true
		}
	}
	return false
}
