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

func TestTUIToolsRefreshRechecksProviderState(t *testing.T) {
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
	if err := config.Save(configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{DisabledProviders: []string{"apt", "apk", "dnf", "node", "pacman", "pip", "python", "zypper"}},
		Tools: map[string]config.ToolSpec{
			"omni-old-one": {Providers: []config.ToolInstallSpec{{Provider: "brew", Package: "omni-old-one"}}},
			"omni-old-two": {Providers: []config.ToolInstallSpec{{Provider: "brew", Package: "omni-old-two"}}},
		},
		Hosts: map[string][]string{"testhost": {"dev"}},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "dev", Tools: []config.ToolEntry{{Name: "omni-old-one"}, {Name: "omni-old-two"}}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, screenHas("Dashboard", "Tools"), "TUI did not render main tabs")
		writeTUIKeys(t, term, "\t")
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("Updates", "omni-old-one", "omni-old-two"), "TUI did not render the initial outdated provider state")
		before := exactLineCount(commandLog, "outdated --json=v2 --greedy")
		for _, name := range []string{"omni-old-one", "omni-old-two"} {
			writeIntegrationFile(t, filepath.Join(providerState, name), "2.0.0\n")
		}
		deadline := time.Now().Add(8 * time.Second)
		for exactLineCount(commandLog, "outdated --json=v2 --greedy") == before && time.Now().Before(deadline) {
			writeTUIKeys(t, term, "R")
			_, _ = waitForScreen(term, 500*time.Millisecond, func(_ string) bool {
				return exactLineCount(commandLog, "outdated --json=v2 --greedy") > before
			})
		}
		return waitForRequiredScreen(t, term, 12*time.Second, func(string) bool {
			return exactLineCount(commandLog, "outdated --json=v2 --greedy") > before &&
				bulkUpgradeCacheSettled(cache)
		}, "TUI refresh did not persist the changed provider state")
	})
}

func exactLineCount(path, want string) int {
	content, _ := os.ReadFile(path)
	count := 0
	for _, line := range strings.Split(string(content), "\n") {
		if line == want {
			count++
		}
	}
	return count
}
