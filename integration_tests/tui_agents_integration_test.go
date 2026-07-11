//go:build integration

package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/config"
)

func TestTUIAgentsTabRendersFakeClaudeStub(t *testing.T) {
	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	binDir := filepath.Join(root, "bin")
	env := isolatedTUIEnv(home, cache)

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create fake bin dir: %v", err)
	}
	writeFakeClaudeStub(t, binDir)
	// InstalledAgents detects claude-code by the presence of ~/.claude, not by
	// PATH, so the agents tab needs this directory to expand any rows for it.
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatalf("create fake ~/.claude dir: %v", err)
	}
	// PATH is replaced, not prepended, so no real codex binary on the host
	// leaks in and turns this claude-only fixture into a two-agent scenario.
	env = append(env, "PATH="+binDir+":/usr/bin:/bin")

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

	screen := runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(tty *os.File, capture *lockedBuffer) string {
		waitForRequiredScreen(t, capture, 6*time.Second, func(text string) bool {
			return strings.Contains(text, "Dashboard") && strings.Contains(text, "Tools")
		}, "TUI did not render main tabs")
		writeTUIKeys(t, tty, "\t", "\t", "\t")
		return waitForRequiredScreen(t, capture, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "Updates Available") &&
				strings.Contains(text, "1.0.0 → 2.0.0") &&
				strings.Contains(text, "claude-code") &&
				strings.Contains(text, "✓") &&
				strings.Contains(text, "grafana") &&
				strings.Contains(text, "caveman")
		}, "TUI did not render agents tab from fake claude stub")
	})
	if strings.Contains(strings.ToLower(screen), "error") {
		t.Fatalf("TUI showed an error while rendering agents tab; screen:\n%s", screen)
	}
}

func writeFakeClaudeStub(t *testing.T, binDir string) {
	t.Helper()
	script := `#!/bin/sh
set -eu
case "$*" in
"plugins list --json --available")
  printf '{"installed":[{"id":"caveman@lkshrk","version":"1.0.0","scope":"user","enabled":true}],"available":[{"name":"caveman","marketplaceName":"lkshrk","latestVersion":"2.0.0"}]}\n'
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
