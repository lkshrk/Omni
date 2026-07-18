//go:build integration

package integration_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
)

func TestTUIFirstRunCreatesHostAndReachesDashboard(t *testing.T) {
	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	env := append(isolatedTUIEnv(t, home, cache), "PATH="+filepath.Join(home, ".test-stub-bin"))

	runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, func(text string) bool {
			return strings.Contains(text, "No settings.json was found.")
		}, "TUI did not start first-run setup")
		writeTUIKeys(t, term, "n")

		waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "save & continue")
		}, "TUI did not advance to provider selection")
		writeTUIKeys(t, term, "\r")

		waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "Provider Priority")
		}, "TUI did not open provider priority")
		writeTUIKeys(t, term, "\r")

		waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "Enable dotfile sync?")
		}, "TUI did not advance to dotfile setup")
		writeTUIKeys(t, term, "n")

		return waitForRequiredScreen(t, term, 12*time.Second, func(text string) bool {
			return strings.Contains(text, "Dashboard") && strings.Contains(text, "Tools")
		}, "TUI did not finish setup at the dashboard")
	})

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("reload first-run config: %v", err)
	}
	if _, ok := cfg.Hosts["testhost"]; !ok {
		t.Fatalf("first-run config hosts = %v, want testhost", cfg.Hosts)
	}
}
