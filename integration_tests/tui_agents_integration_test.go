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

func TestTUIAgentsTabInstallsMissingPluginWithFakeClaudeStub(t *testing.T) {
	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	binDir := filepath.Join(root, "bin")
	installedMarker := filepath.Join(root, "caveman-installed")
	env := isolatedTUIEnv(t, home, cache)

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create fake bin dir: %v", err)
	}
	writeFakeClaudeStub(t, binDir)
	// InstalledAgents detects claude-code by ~/.claude existing, not by PATH.
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatalf("create fake ~/.claude dir: %v", err)
	}
	// PATH is replaced, not prepended, so a real codex on the host cannot turn this claude-only fixture into two agents.
	env = append(env,
		"PATH="+binDir+":/usr/bin:/bin",
		"OMNI_TEST_CLAUDE_PLUGIN_STATE="+installedMarker,
	)

	if err := config.Save(configPath, &config.RootConfig{
		Version: config.CurrentVersion,
		Agents: config.AgentsConfig{
			McpServers: []config.McpServer{
				{Name: "grafana", Transport: "http", URL: "https://mcp.example.com"},
			},
			Marketplaces: []config.Marketplace{
				{Name: "lkshrk", Source: "lkshrk/marketplace"},
			},
			Plugins: []config.Plugin{
				{Name: "caveman", Marketplace: "lkshrk"},
			},
		},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "hosts", "ensure", "testhost")

	screen := runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, func(text string) bool {
			return strings.Contains(text, "Dashboard") && strings.Contains(text, "Tools")
		}, "TUI did not render main tabs")
		writeTUIKeys(t, term, "\t", "\t", "\t")
		waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "Out of Sync") &&
				strings.Contains(text, "missing") &&
				strings.Contains(text, "caveman") &&
				strings.Contains(text, "✓ lkshrk")
		}, "TUI did not render configured caveman plugin as missing")
		writeTUIKeys(t, term, "]", "]", "]")
		waitForRequiredScreen(t, term, 2*time.Second, func(text string) bool {
			return strings.Contains(text, "[plugin]") && strings.Contains(text, "caveman")
		}, "TUI did not select the plugin filter")
		writeTUIKeys(t, term, "[", "[", "[")
		writeTUIKeys(t, term, "j")
		waitForRequiredScreen(t, term, 2*time.Second, func(text string) bool {
			return strings.Contains(text, "[all]") && strings.Contains(text, "> ✗ caveman")
		}, "TUI did not select the caveman row in the all filter")
		writeTUIKeys(t, term, "i")
		return waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
			_, markerErr := os.Stat(installedMarker)
			return markerErr == nil &&
				strings.Contains(text, "Updates Available") &&
				strings.Contains(text, "1.0.0 → 2.0.0") &&
				strings.Contains(text, "claude-code") &&
				strings.Contains(text, "✓") &&
				strings.Contains(text, "grafana") &&
				strings.Contains(text, "caveman")
		}, "TUI did not install and refresh the configured caveman plugin")
	})
	if strings.Contains(strings.ToLower(screen), "error") {
		t.Fatalf("TUI showed an error while rendering agents tab; screen:\n%s", screen)
	}
	if marker, err := os.ReadFile(installedMarker); err != nil || strings.TrimSpace(string(marker)) != "caveman@lkshrk" {
		t.Fatalf("fake claude install marker = %q, %v", marker, err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load config after plugin install: %v", err)
	}
	if got := cfg.Agents.Plugins[0].Agents; len(got) != 1 || got[0] != "claude-code" {
		t.Fatalf("persisted caveman agents = %v, want [claude-code]", got)
	}
}

func writeFakeClaudeStub(t *testing.T, binDir string) {
	t.Helper()
	script := `#!/bin/sh
set -eu
state="${OMNI_TEST_CLAUDE_PLUGIN_STATE:?}"
case "$*" in
"plugins list --json --available")
  if [ -f "$state" ]; then
    printf '{"installed":[{"id":"caveman@lkshrk","version":"1.0.0","scope":"user","enabled":true}],"available":[{"name":"caveman","marketplaceName":"lkshrk","latestVersion":"2.0.0"}]}\n'
  else
    printf '{"installed":[],"available":[{"name":"caveman","marketplaceName":"lkshrk","latestVersion":"2.0.0"}]}\n'
  fi
  exit 0
  ;;
"plugins marketplace list --json")
  printf '[{"name":"lkshrk","source":"github","repo":"lkshrk/marketplace"}]\n'
  exit 0
  ;;
"plugins install caveman@lkshrk")
  printf 'caveman@lkshrk\n' > "$state"
  exit 0
  ;;
"mcp list")
  printf 'Checking MCP server health…\n'
  printf '\n'
  printf 'grafana: https://mcp.example.com (HTTP) - ✔ Connected\n'
  exit 0
  ;;
esac
exit 1
`
	path := filepath.Join(binDir, "claude")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude stub: %v", err)
	}
}
