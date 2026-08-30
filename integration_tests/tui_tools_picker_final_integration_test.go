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

func TestTUIToolsDeleteSpecRemovesMissingTrackedTool(t *testing.T) {
	bin, root, cache, configPath, env := finalTUIToolPickerFixture(t)

	runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		openFinalTUIToolRow(t, term, "omni-missing")
		writeTUIKeys(t, term, "d")
		waitForRequiredScreen(t, term, 4*time.Second, screenHas("confirm delete", "omni-missing"), "TUI did not arm tool-spec deletion")
		writeTUIKeys(t, term, "d")
		return waitForRequiredScreen(t, term, 10*time.Second, func(string) bool {
			cfg, err := config.Load(configPath)
			return err == nil && !finalTUIToolExists(cfg, "omni-missing") && !finalTUIToolInGroup(cfg, "testhost", "omni-missing")
		}, "TUI did not delete the missing tracked tool spec")
	})
}

func finalTUIToolPickerFixture(t *testing.T) (bin, root, cache, configPath string, env []string) {
	t.Helper()
	bin = buildOmniBinary(t)
	root = t.TempDir()
	home := filepath.Join(root, "home")
	cache = filepath.Join(root, "cache")
	configPath = filepath.Join(root, "settings.json")
	env = isolatedTUIEnv(t, home, cache)
	cfg := &config.RootConfig{
		Version: config.CurrentVersion,
		Settings: config.Settings{DisabledProviders: []string{
			"apk", "brew", "dnf", "node", "pacman", "pip", "python", "zypper",
		}},
		Hosts:  map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{{Name: "testhost", Special: "host"}},
	}
	tool := &database.ToolCache{
		Name:      "omni-missing",
		Provider:  "apt",
		Package:   "omni-missing",
		Installed: false,
		Tracked:   true,
		Version:   sql.NullString{},
	}
	cfg.Tools = map[string]config.ToolSpec{"omni-missing": {Providers: []config.ToolInstallSpec{{Provider: "apt", Package: "omni-missing"}}}}
	cfg.Groups[0].Tools = []config.ToolEntry{{Name: "omni-missing"}}
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	seedTUIToolCache(t, cache, tool)
	return bin, root, cache, configPath, env
}

func openFinalTUIToolRow(t *testing.T, term *vttest.Terminal, name string) {
	t.Helper()
	waitForRequiredScreen(t, term, 7*time.Second, screenHas("Dashboard", "Tools"), "TUI did not render main tabs")
	writeTUIKeys(t, term, "\t")
	waitForRequiredScreen(t, term, 9*time.Second, screenHas(name, "apt"), "TUI did not render the tool fixture")
	writeTUIKeys(t, term, "j")
	waitForRequiredScreen(t, term, 4*time.Second, screenHas(name, ">"), "TUI did not select the tool fixture")
}

func finalTUIToolExists(cfg *config.RootConfig, name string) bool {
	_, ok := cfg.Tools[name]
	return ok
}

func finalTUIToolInGroup(cfg *config.RootConfig, groupName, toolName string) bool {
	for _, group := range cfg.Groups {
		if group == nil || group.BaseName() != groupName {
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
