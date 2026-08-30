//go:build integration

package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"
)

func TestCLIAndTUIAgentsAddProduceEquivalentAPMState(t *testing.T) {
	bin := buildOmniBinary(t)
	runParityFlow(t, bin, parityFlow{
		seed:    seedAgentsAddParity,
		runCLI:  runAgentsAddParityCLI,
		runTUI:  runAgentsAddParityTUI,
		observe: observeAgentsAddParity,
		readTUI: readAgentsActionsThroughCLI,
	})
}

func seedAgentsAddParity(t *testing.T, sandbox *paritySandbox) {
	seedAgentsActionsParity(t, sandbox)
	apmDir := filepath.Join(sandbox.home, ".apm")
	writeIntegrationFile(t, filepath.Join(apmDir, "marketplaces.json"), `{"marketplaces":[{"name":"superpowers-dev","owner":"obra","repo":"superpowers"}]}`)
	writeIntegrationFile(t, filepath.Join(apmDir, "cache", "marketplace", "superpowers-dev.json"), `{"name":"superpowers-dev","owner":{"name":"obra"},"plugins":[{"name":"zz-brainstorming","description":"ideas","source":{"source":"git-subdir","url":"https://github.com/obra/superpowers","path":"plugins/brainstorming"}}]}`)
	writeExecutable(t, filepath.Join(sandbox.root, "bin", "apm"), `#!/bin/sh
set -eu
if [ "${1:-}" = "--version" ]; then echo 'Agent Package Manager (APM) CLI version 0.29.0'; exit 0; fi
printf '%s|%s\n' "$PWD" "$*" >> "${OMNI_TEST_APM_LOG:?}"
case "$*" in
  outdated*) echo '[✓] All dependencies are up-to-date' ;;
  'install -g zz-brainstorming@superpowers-dev') touch "${OMNI_TEST_APM_STATE:?}/added"; echo '[✓] installed' ;;
  *) echo "delegated: $*" ;;
esac
`)
}

func runAgentsAddParityCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"agents", "add", "zz-brainstorming@superpowers-dev")
}

func runAgentsAddParityTUI(t *testing.T, bin string, sandbox *paritySandbox) {
	runTUI(t, bin, sandbox.root, sandbox.env, []string{"--config", sandbox.configPath, "--cache-dir", sandbox.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("Dashboard", "Agents"), "TUI did not start")
		writeTUIKeys(t, term, "\t", "\t", "\t")
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("Packages", "tool"), "TUI did not render Agents")
		sendAgentsActionsKeyUntil(t, term, "a", screenHas("Registry", "zz-brainstorming"), "TUI did not open registry")
		writeTUIKeys(t, term, "brain")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("zz-brainstorming", "available"), "TUI did not filter registry")
		writeTUIKeys(t, term, "\r", "\r")
		return waitForRequiredScreen(t, term, 10*time.Second, func(string) bool {
			_, err := os.Stat(filepath.Join(sandbox.root, "apm-state", "added"))
			return err == nil
		}, "TUI did not install registry package")
	})
}

type agentsAddState struct {
	Config  any
	Action  string
	Added   bool
	CwdOkay bool
}

func observeAgentsAddParity(t *testing.T, sandbox *paritySandbox) any {
	t.Helper()
	state := agentsAddState{Config: normalizedParityConfig(t, sandbox)}
	raw, err := os.ReadFile(filepath.Join(sandbox.root, "apm.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		cwd, args, ok := strings.Cut(line, "|")
		if ok && args == "install -g zz-brainstorming@superpowers-dev" {
			state.Action = args
			state.CwdOkay = cwd == filepath.Join(sandbox.home, ".apm")
		}
	}
	_, err = os.Stat(filepath.Join(sandbox.root, "apm-state", "added"))
	state.Added = err == nil
	return state
}
