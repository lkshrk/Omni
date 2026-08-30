//go:build integration

package integration_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
)

func TestCLIAndTUISetupProduceEquivalentConfig(t *testing.T) {
	bin := buildOmniBinary(t)
	runParityFlow(t, bin, parityFlow{
		seed: func(_ *testing.T, sandbox *paritySandbox) {
			sandbox.env = replaceIntegrationEnv(sandbox.env, "PATH", filepath.Join(sandbox.home, ".test-stub-bin"))
		},
		runCLI: func(t *testing.T, bin string, sandbox *paritySandbox) {
			runOmniCommand(t, bin, sandbox.root, sandbox.env, "--config", sandbox.configPath, "--cache-dir", sandbox.cache, "init", "--no-import")
		},
		runTUI: func(t *testing.T, bin string, sandbox *paritySandbox) {
			runTUI(t, bin, sandbox.root, sandbox.env, []string{"--config", sandbox.configPath, "--cache-dir", sandbox.cache}, func(term *vttest.Terminal) string {
				waitForRequiredScreen(t, term, 6*time.Second, screenHas("No settings.json was found."), "TUI did not start first-run setup")
				writeTUIKeys(t, term, "n")
				waitForRequiredScreen(t, term, 8*time.Second, screenHas("save & continue"), "TUI did not advance to provider selection")
				writeTUIKeys(t, term, "\r")
				waitForRequiredScreen(t, term, 8*time.Second, screenHas("Provider Priority"), "TUI did not open provider priority")
				writeTUIKeys(t, term, "\r")
				waitForRequiredScreen(t, term, 8*time.Second, screenHas("Enable dotfile sync?"), "TUI did not advance to dotfile setup")
				writeTUIKeys(t, term, "n")
				return waitForRequiredScreen(t, term, 12*time.Second, func(text string) bool {
					if !screenHas("Dashboard", "Tools")(text) {
						return false
					}
					cfg, err := config.Load(sandbox.configPath)
					if err != nil {
						return false
					}
					_, host := cfg.Hosts["testhost"]
					for _, group := range cfg.Groups {
						if group != nil && group.Name == "testhost" && group.Special == "host" {
							return host
						}
					}
					return false
				}, "TUI did not finish setup")
			})
		},
		observe: observeSetupParityConfig,
		readTUI: func(*testing.T, string, *paritySandbox) {},
	})
}

func observeSetupParityConfig(t *testing.T, sandbox *paritySandbox) any {
	state := normalizedParityConfig(t, sandbox).(map[string]any)
	if hosts, ok := state["host_settings"].(map[string]any); ok {
		for host, settings := range hosts {
			if values, ok := settings.(map[string]any); ok && len(values) == 0 {
				delete(hosts, host)
			}
		}
		if len(hosts) == 0 {
			delete(state, "host_settings")
		}
	}
	return state
}
