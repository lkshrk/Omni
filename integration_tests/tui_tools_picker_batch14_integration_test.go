//go:build integration

package integration_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
)

func TestTUIToolsSyncInstallsConfiguredTool(t *testing.T) {
	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	env := isolatedTUIEnv(t, home, cache)
	binDir := filepath.Join(root, "bin")
	configuredState := filepath.Join(root, "configured-state")
	writeExecutable(t, filepath.Join(binDir, "fake-provider"), `#!/bin/sh
set -eu
case "$1" in
  install) echo 1.0.0 > "$OMNI_TEST_CONFIGURED_STATE" ;;
  check) test -f "$OMNI_TEST_CONFIGURED_STATE" ;;
  version) cat "$OMNI_TEST_CONFIGURED_STATE" ;;
  latest) echo 1.0.0 ;;
  upgrade) echo 1.0.0 > "$OMNI_TEST_CONFIGURED_STATE" ;;
  *) exit 64 ;;
esac
`)
	env = replaceIntegrationEnv(env, "PATH", binDir+string(os.PathListSeparator)+integrationEnvValue(env, "PATH"))
	env = append(env, "OMNI_TEST_CONFIGURED_STATE="+configuredState)
	if err := config.Save(configPath, &config.RootConfig{
		Version: config.CurrentVersion,
		Settings: config.Settings{DisabledProviders: []string{
			"apt", "apk", "dnf", "pacman", "zypper", "node", "bun", "pnpm", "npm", "python", "uv", "pip",
		}},
		Tools: map[string]config.ToolSpec{"omni-configured": {
			Provider: "script",
			Options: map[string]string{
				"install": "fake-provider install", "check": "fake-provider check", "version": "fake-provider version",
				"latest": "fake-provider latest", "upgrade": "fake-provider upgrade",
			},
		}},
		Hosts:  map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{{Name: "testhost", Special: "host", Tools: []config.ToolEntry{{Name: "omni-configured"}}}},
	}); err != nil {
		t.Fatal(err)
	}

	runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 7*time.Second, screenHas("Dashboard", "Tools"), "TUI did not render main tabs")
		writeTUIKeys(t, term, "\t")
		waitForRequiredScreen(t, term, 10*time.Second, screenHas("omni-configured"), "TUI did not render the configured tool")
		writeTUIKeys(t, term, "S")
		waitForRequiredScreen(t, term, 4*time.Second, screenHas("press S again to sync all"), "TUI did not arm sync all")
		writeTUIKeys(t, term, "S")
		return waitForRequiredScreen(t, term, 15*time.Second, func(string) bool {
			_, installErr := os.Stat(configuredState)
			return installErr == nil
		}, "TUI sync all did not install the configured tool")
	})
}
