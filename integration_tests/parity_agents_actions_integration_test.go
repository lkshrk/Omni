//go:build integration

package integration_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
)

func TestCLIAndTUIAgentsUpdateAllProduceEquivalentAPMState(t *testing.T) {
	bin := batch16OmniBinary(t)
	runParityFlow(t, bin, parityFlow{
		seed:    seedAgentsActionsParity,
		runCLI:  runAgentsUpdateAllParityCLI,
		runTUI:  runAgentsUpdateAllParityTUI,
		observe: observeAgentsMutationParity,
		readTUI: readAgentsActionsThroughCLI,
	})
}

func TestCLIAndTUIAgentsRemoveProduceEquivalentAPMState(t *testing.T) {
	bin := batch16OmniBinary(t)
	runParityFlow(t, bin, parityFlow{
		seed:    seedAgentsActionsParity,
		runCLI:  runAgentsRemoveParityCLI,
		runTUI:  runAgentsRemoveParityTUI,
		observe: observeAgentsMutationParity,
		readTUI: readAgentsActionsThroughCLI,
	})
}

func TestCLIAndTUIAgentsRefreshProduceEquivalentAPMState(t *testing.T) {
	bin := batch16OmniBinary(t)
	runParityFlow(t, bin, parityFlow{seed: seedAgentsActionsParity, runCLI: func(t *testing.T, bin string, s *paritySandbox) {
		runOmniCommand(t, bin, s.root, s.env, "--config", s.configPath, "--cache-dir", s.cache, "agents", "outdated")
	}, runTUI: func(t *testing.T, bin string, s *paritySandbox) {
		runAgentsActionsTUI(t, bin, s, func(term *vttest.Terminal) {
			before := countAgentsAction(s, "|outdated -g --parallel-checks 4")
			sendAgentsActionsKeyUntil(t, term, "R", func(string) bool { return countAgentsAction(s, "|outdated -g --parallel-checks 4") > before }, "TUI did not refresh agents")
		}, func(s *paritySandbox) bool { return countAgentsAction(s, "|outdated -g --parallel-checks 4") >= 2 })
	}, observe: observeAgentsActionsParity, readTUI: readAgentsActionsThroughCLI})
}

func countAgentsAction(s *paritySandbox, want string) int {
	raw, _ := os.ReadFile(filepath.Join(s.root, "apm.log"))
	return strings.Count(string(raw), want)
}

func seedAgentsActionsParity(t *testing.T, sandbox *paritySandbox) {
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
	writeIntegrationFile(t, filepath.Join(apmDir, "apm.yml"), `name: agents-parity
version: 1.0.0
targets: [codex]
dependencies:
  apm:
    - git: https://github.com/acme/tool.git
`)
	writeIntegrationFile(t, filepath.Join(apmDir, "apm.lock.yaml"), `dependencies:
  - repo_url: acme/tool
    name: tool
    version: 1.0.0
`)
	binDir := filepath.Join(sandbox.root, "bin")
	logPath := filepath.Join(sandbox.root, "apm.log")
	stateDir := filepath.Join(sandbox.root, "apm-state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(binDir, "apm"), `#!/bin/sh
set -eu
if [ "${1:-}" = "--version" ]; then
  echo 'Agent Package Manager (APM) CLI version 0.29.0'
  exit 0
fi
printf '%s|%s\n' "$PWD" "$*" >> "${OMNI_TEST_APM_LOG:?}"
case "$*" in
	  outdated*) touch "${OMNI_TEST_APM_STATE:?}/refreshed"; echo '[✓] All dependencies are up-to-date' ;;
  'update -g --yes') touch "${OMNI_TEST_APM_STATE:?}/updated"; echo '[✓] updated' ;;
  'uninstall -g https://github.com/acme/tool') touch "${OMNI_TEST_APM_STATE:?}/removed"; echo '[✓] removed' ;;
  *) echo "delegated: $*" ;;
esac
`)
	sandbox.env = replaceIntegrationEnv(sandbox.env, "PATH", binDir+string(os.PathListSeparator)+integrationEnvValue(sandbox.env, "PATH"))
	sandbox.env = append(sandbox.env, "OMNI_TEST_APM_LOG="+logPath, "OMNI_TEST_APM_STATE="+stateDir)
}

func runAgentsUpdateAllParityCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"agents", "update")
}

func runAgentsUpdateAllParityTUI(t *testing.T, bin string, sandbox *paritySandbox) {
	runAgentsActionsTUI(t, bin, sandbox, func(term *vttest.Terminal) {
		sendAgentsActionsKeyUntil(t, term, "U", func(string) bool {
			_, err := os.Stat(filepath.Join(sandbox.root, "apm-state", "updated"))
			return err == nil
		}, "TUI did not invoke update all")
	}, func(sandbox *paritySandbox) bool {
		_, err := os.Stat(filepath.Join(sandbox.root, "apm-state", "updated"))
		return err == nil
	})
}

func runAgentsRemoveParityCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"agents", "remove", "https://github.com/acme/tool")
}

func runAgentsRemoveParityTUI(t *testing.T, bin string, sandbox *paritySandbox) {
	runAgentsActionsTUI(t, bin, sandbox, func(term *vttest.Terminal) {
		sendAgentsActionsKeyUntil(t, term, "j", func(text string) bool {
			return strings.Contains(text, ">") && strings.Contains(text, "tool")
		}, "TUI did not select APM package")
		writeTUIKeys(t, term, "d")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("confirm uninstall"), "TUI did not arm package removal")
		writeTUIKeys(t, term, "d")
	}, func(sandbox *paritySandbox) bool {
		_, err := os.Stat(filepath.Join(sandbox.root, "apm-state", "removed"))
		return err == nil
	})
}

func runAgentsActionsTUI(t *testing.T, bin string, sandbox *paritySandbox, act func(*vttest.Terminal), done func(*paritySandbox) bool) {
	t.Helper()
	runTUI(t, bin, sandbox.root, sandbox.env, []string{"--config", sandbox.configPath, "--cache-dir", sandbox.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("Dashboard", "Agents"), "TUI did not start")
		writeTUIKeys(t, term, "\t", "\t", "\t")
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("tool", "acme"), "TUI did not render APM package")
		waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
			return !strings.Contains(strings.ToLower(text), "checking package updates")
		}, "TUI package update check did not settle")
		act(term)
		return waitForRequiredScreen(t, term, 10*time.Second, func(string) bool { return done(sandbox) }, "TUI APM action did not complete")
	})
}

func sendAgentsActionsKeyUntil(t *testing.T, term *vttest.Terminal, key string, ready func(string) bool, message string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		writeTUIKeys(t, term, key)
		if _, ok := waitForScreen(term, 500*time.Millisecond, ready); ok {
			return
		}
	}
	t.Fatalf("%s; screen:\n%s", message, currentScreenText(term))
}

type agentsActionsState struct {
	Config    any
	Manifest  string
	Lock      string
	Actions   []string
	Updated   bool
	Removed   bool
	Refreshed bool
}

func observeAgentsActionsParity(t *testing.T, sandbox *paritySandbox) any {
	t.Helper()
	read := func(name string) string {
		raw, err := os.ReadFile(filepath.Join(sandbox.home, ".apm", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(raw)
	}
	state := agentsActionsState{
		Config:   normalizedParityConfig(t, sandbox),
		Manifest: read("apm.yml"),
		Lock:     read("apm.lock.yaml"),
	}
	if raw, err := os.ReadFile(filepath.Join(sandbox.root, "apm.log")); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.Contains(line, "|update -g --yes") || strings.Contains(line, "|uninstall -g ") {
				state.Actions = append(state.Actions, strings.ReplaceAll(line, sandbox.root, "$ROOT"))
			}
		}
	}
	sort.Strings(state.Actions)
	_, updateErr := os.Stat(filepath.Join(sandbox.root, "apm-state", "updated"))
	_, removeErr := os.Stat(filepath.Join(sandbox.root, "apm-state", "removed"))
	state.Updated = updateErr == nil
	state.Removed = removeErr == nil
	_, refreshErr := os.Stat(filepath.Join(sandbox.root, "apm-state", "refreshed"))
	state.Refreshed = refreshErr == nil
	return state
}

func observeAgentsMutationParity(t *testing.T, sandbox *paritySandbox) any {
	t.Helper()
	state := observeAgentsActionsParity(t, sandbox).(agentsActionsState)
	// TUI readiness performs an initial read-only outdated query before mutation.
	// Refresh semantics have their own parity flow; mutation parity compares only mutation effects.
	state.Refreshed = false
	return state
}

func readAgentsActionsThroughCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"agents", "list")
}
