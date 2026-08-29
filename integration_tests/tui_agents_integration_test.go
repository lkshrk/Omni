//go:build integration

package integration_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
)

func TestTUIAgentsTabSyncsMCPThroughRealAPM(t *testing.T) {
	if _, err := exec.LookPath("apm"); err != nil {
		t.Fatalf("integration tests require apm on PATH: %v", err)
	}
	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	env := isolatedTUIEnv(t, home, cache)

	if err := config.Save(configPath, &config.RootConfig{
		Version: config.CurrentVersion,
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	apmDir := filepath.Join(home, ".apm")
	if err := os.MkdirAll(apmDir, 0o700); err != nil {
		t.Fatalf("create APM workspace: %v", err)
	}
	manifest := `name: omni-tui
version: 1.0.0
targets: [codex]
dependencies:
  apm: []
  mcp:
    - name: omni-tui
      registry: false
      transport: http
      url: https://example.invalid/mcp
`
	if err := os.WriteFile(filepath.Join(apmDir, "apm.yml"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("write APM manifest: %v", err)
	}
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "hosts", "ensure", "testhost")

	screen := runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, func(text string) bool {
			return strings.Contains(text, "Dashboard") && strings.Contains(text, "Tools")
		}, "TUI did not render main tabs")
		writeTUIKeys(t, term, "\t", "\t", "\t")
		waitForRequiredScreen(t, term, 4*time.Second, func(text string) bool {
			return strings.Contains(text, "~/.apm/apm.yml") &&
				strings.Contains(text, "MCP servers") && strings.Contains(text, "omni-tui")
		}, "TUI did not render the APM workspace")
		waitForRequiredScreen(t, term, 7*time.Second, func(text string) bool {
			return !strings.Contains(text, "checking package updates")
		}, "Agents update check did not settle")

		writeTUIKeys(t, term, "S")
		return waitForRequiredScreen(t, term, 7*time.Second, func(text string) bool {
			return strings.Contains(text, "omni agents sync") && !strings.Contains(text, "running omni agents sync")
		}, "TUI did not show APM sync success")
	})
	if strings.Contains(strings.ToLower(screen), "error") {
		t.Fatalf("TUI showed an error while syncing MCP through APM; screen:\n%s", screen)
	}

	for path, wants := range map[string][]string{
		filepath.Join(home, ".apm", "apm.yml"):       {"omni-tui", "https://example.invalid/mcp"},
		filepath.Join(home, ".apm", "apm.lock.yaml"): {"codex", "omni-tui"},
		filepath.Join(home, ".codex", "config.toml"): {"omni-tui", "https://example.invalid/mcp"},
	} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read APM state %s: %v", path, err)
		}
		for _, want := range wants {
			if !strings.Contains(string(content), want) {
				t.Fatalf("%s missing %q:\n%s", path, want, content)
			}
		}
	}
}
