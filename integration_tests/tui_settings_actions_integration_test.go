//go:build integration

package integration_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

func TestTUISettingsProviderPersistsPriorityOrder(t *testing.T) {
	bin, root, cache, configPath, env := newTUISettingsActionSandbox(t)

	runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		openTUISettingsActions(t, term)
		sendTUIKey(term, uv.KeyHome)
		writeTUIKeys(t, term, "j", "\r")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("Provider Priority", "space grab", "enter save"), "TUI did not open provider priority")
		writeTUIKeys(t, term, " ", "j", " ", "\r")
		return waitForRequiredScreen(t, term, 8*time.Second, func(_ string) bool {
			cfg, err := config.Load(configPath)
			if err != nil {
				return false
			}
			priority := cfg.HostSettings["testhost"].ProviderPriority
			return len(priority) >= 2 && priority[0] == "apt" && priority[1] == "brew"
		}, "TUI did not persist provider priority")
	})
}

func TestTUISettingsResetPreservesInventoryAndClearsSettings(t *testing.T) {
	bin, root, cache, configPath, env := newTUISettingsActionSandbox(t)

	runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		openTUISettingsActions(t, term)
		revealTUISettingsCursor(t, term)
		writeTUIKeys(t, term, "k", "k")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("> Reset Settings", "Restore all settings to defaults"), "TUI did not select settings reset")
		writeTUIKeys(t, term, "\r", "\r")
		return waitForRequiredScreen(t, term, 8*time.Second, func(_ string) bool {
			cfg, err := config.Load(configPath)
			if err != nil {
				return false
			}
			host := cfg.HostSettings["testhost"]
			return !cfg.Settings.AutoImport && len(host.ProviderPriority) == 0 && len(host.DisabledProviders) == 0 && configHasGroup(cfg, "work")
		}, "TUI did not reset settings while preserving inventory")
	})
}

func TestTUISettingsResetCacheRemovesCachedToolState(t *testing.T) {
	bin, root, cache, configPath, env := newTUISettingsActionSandbox(t)
	seedTUIToolCache(t, cache, &database.ToolCache{
		Name:      "cache-only-marker",
		Provider:  "marker",
		Package:   "cache-only-marker",
		Installed: true,
	})

	runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		openTUISettingsActions(t, term)
		if !tuiCacheHasMarker(cache) {
			t.Fatal("seeded cache marker disappeared before reset")
		}
		revealTUISettingsCursor(t, term)
		writeTUIKeys(t, term, "k")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("> Reset Cache", "Delete and reinitialise"), "TUI did not select cache reset")
		writeTUIKeys(t, term, "\r", "\r")
		return waitForRequiredScreen(t, term, 8*time.Second, func(_ string) bool {
			return !tuiCacheHasMarker(cache)
		}, "TUI did not clear cached tool state")
	})
}

func newTUISettingsActionSandbox(t *testing.T) (bin, root, cache, configPath string, env []string) {
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
			AutoImport: true,
		},
		HostSettings: map[string]config.Settings{
			"testhost": {
				ProviderPriority:  []string{"brew", "apt", "apk"},
				DisabledProviders: []string{"system", "node", "python", "pip"},
			},
		},
		Hosts: map[string][]string{"testhost": {"work"}},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "work"},
		},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	return bin, root, cache, configPath, env
}

func openTUISettingsActions(t *testing.T, term *vttest.Terminal) {
	t.Helper()
	waitForRequiredScreen(t, term, 6*time.Second, screenHas("Dashboard", "Tools"), "TUI did not render main tabs")
	writeTUIKeys(t, term, "\t", "\t", "\t", "\t", "\t")
	waitForRequiredScreen(t, term, 8*time.Second, screenHas("Import Installed Tools", "Provider Priority"), "TUI did not render settings actions")
}

func revealTUISettingsCursor(t *testing.T, term *vttest.Terminal) {
	t.Helper()
	sendTUIKey(term, uv.KeyHome)
	waitForRequiredScreen(t, term, 3*time.Second, screenHas("> Import Installed Tools"), "TUI did not reveal the settings cursor")
}

func tuiCacheHasMarker(cache string) bool {
	db, err := database.Open(filepath.Join(cache, "omni.db"))
	if err != nil {
		return false
	}
	defer db.Close()
	rows, err := db.List(context.Background())
	if err != nil {
		return false
	}
	for _, row := range rows {
		if row.Name == "cache-only-marker" && row.Provider == "marker" {
			return true
		}
	}
	return false
}
