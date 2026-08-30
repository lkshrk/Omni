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

type agentsSyncState struct {
	Manifest string
	Lock     string
	Codex    string
}

func TestCLIAndTUIAgentsSyncProduceEquivalentAPMState(t *testing.T) {
	if _, err := exec.LookPath("apm"); err != nil {
		t.Fatalf("integration tests require apm on PATH: %v", err)
	}
	bin := buildOmniBinary(t)
	cliRoot, tuiRoot := newParityTwins(t)
	seedAgentsSyncParity(t, cliRoot)
	seedAgentsSyncParity(t, tuiRoot)

	runOmniCommand(t, bin, cliRoot.root, cliRoot.env,
		"--config", cliRoot.configPath, "--cache-dir", cliRoot.cache, "agents", "sync")
	screen := runTUI(t, bin, tuiRoot.root, tuiRoot.env, []string{"--config", tuiRoot.configPath, "--cache-dir", tuiRoot.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("Dashboard", "Tools"), "TUI did not start for agents parity")
		writeTUIKeys(t, term, "\t", "\t", "\t")
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("~/.apm/apm.yml", "MCP servers", "omni-parity"), "TUI did not render parity APM workspace")
		waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
			return !strings.Contains(text, "checking package updates")
		}, "agents update check did not settle")
		writeTUIKeys(t, term, "S")
		return waitForRequiredScreen(t, term, 10*time.Second, func(text string) bool {
			return strings.Contains(text, "omni agents sync") && !strings.Contains(text, "running omni agents sync")
		}, "TUI did not complete agents sync")
	})
	if strings.Contains(strings.ToLower(screen), "error") {
		t.Fatalf("TUI showed an error while syncing agents; screen:\n%s", screen)
	}

	cliState := observeAgentsSyncParity(t, cliRoot)
	tuiState := observeAgentsSyncParity(t, tuiRoot)
	if cliState != tuiState {
		t.Fatalf("agents sync semantic state differs\nCLI: %#v\nTUI: %#v", cliState, tuiState)
	}
}

func seedAgentsSyncParity(t *testing.T, sandbox *paritySandbox) {
	t.Helper()
	if err := config.Save(sandbox.configPath, &config.RootConfig{
		Version: config.CurrentVersion,
		Hosts:   map[string][]string{"testhost": {}},
		Groups:  []*config.GroupConfig{{Name: "testhost", Special: "host"}},
	}); err != nil {
		t.Fatal(err)
	}
	apmDir := filepath.Join(sandbox.home, ".apm")
	if err := os.MkdirAll(apmDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeIntegrationFile(t, filepath.Join(apmDir, "apm.yml"), `name: omni-parity
version: 1.0.0
targets: [codex]
dependencies:
  apm: []
  mcp:
    - name: omni-parity
      registry: false
      transport: http
      url: https://example.invalid/parity
`)
}

func observeAgentsSyncParity(t *testing.T, sandbox *paritySandbox) agentsSyncState {
	t.Helper()
	read := func(path string) string {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read agents parity state %s: %v", path, err)
		}
		var normalized []string
		for _, line := range strings.Split(strings.ReplaceAll(string(content), sandbox.root, "$ROOT"), "\n") {
			if !strings.HasPrefix(line, "generated_at:") {
				normalized = append(normalized, line)
			}
		}
		return strings.Join(normalized, "\n")
	}
	return agentsSyncState{
		Manifest: read(filepath.Join(sandbox.home, ".apm", "apm.yml")),
		Lock:     read(filepath.Join(sandbox.home, ".apm", "apm.lock.yaml")),
		Codex:    read(filepath.Join(sandbox.home, ".codex", "config.toml")),
	}
}
