//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

func TestTUIToolsUpdateUpgradesOnlySelectedTool(t *testing.T) {
	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	commandLog := filepath.Join(root, "brew.log")
	providerState := filepath.Join(root, "brew-state")
	if err := os.MkdirAll(providerState, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"omni-old-one", "omni-old-two"} {
		writeIntegrationFile(t, filepath.Join(providerState, name), "1.0.0\n")
	}
	env := append(isolatedTUIEnv(t, home, cache), "OMNI_TEST_BREW_LOG="+commandLog, "OMNI_TEST_BREW_STATE="+providerState)
	writeFakeBulkUpgradeBrew(t, filepath.Join(home, ".test-stub-bin", "brew"))

	tools := map[string]config.ToolSpec{}
	entries := make([]config.ToolEntry, 0, 2)
	for _, name := range []string{"omni-old-one", "omni-old-two"} {
		tools[name] = config.ToolSpec{Providers: []config.ToolInstallSpec{{Provider: "brew", Package: name}}}
		entries = append(entries, config.ToolEntry{Name: name})
		seedTUIToolCache(t, cache, &database.ToolCache{
			Name: name, Provider: "brew", Package: name, Installed: true, InstalledWith: "brew", Tracked: true,
			Version: sql.NullString{String: "1.0.0", Valid: true}, LatestVersion: sql.NullString{String: "2.0.0", Valid: true}, Outdated: true,
		})
	}
	if err := config.Save(configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{DisabledProviders: []string{"apt", "apk", "dnf", "node", "pacman", "pip", "python", "zypper"}},
		Tools:    tools,
		Hosts:    map[string][]string{"testhost": {"dev"}},
		Groups:   []*config.GroupConfig{{Name: "testhost", Special: "host"}, {Name: "dev", Tools: entries}},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, screenHas("Dashboard", "Tools"), "TUI did not render main tabs")
		writeTUIKeys(t, term, "\t")
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("omni-old-one", "omni-old-two"), "TUI did not render outdated tools")
		writeTUIKeys(t, term, "j", "u")
		return waitForRequiredScreen(t, term, 12*time.Second, func(_ string) bool {
			one, errOne := os.ReadFile(filepath.Join(providerState, "omni-old-one"))
			two, errTwo := os.ReadFile(filepath.Join(providerState, "omni-old-two"))
			return errOne == nil && errTwo == nil && strings.TrimSpace(string(one)) == "2.0.0" && strings.TrimSpace(string(two)) == "1.0.0" && selectedUpgradeCacheSettled(cache)
		}, "TUI did not upgrade only the selected tool")
	})
}

func selectedUpgradeCacheSettled(cache string) bool {
	db, err := database.Open(filepath.Join(cache, "omni.db"))
	if err != nil {
		return false
	}
	defer db.Close()
	one, errOne := db.Get(context.Background(), "omni-old-one", "brew", "omni-old-one")
	two, errTwo := db.Get(context.Background(), "omni-old-two", "brew", "omni-old-two")
	return errOne == nil && errTwo == nil && !one.Outdated && two.Outdated
}
