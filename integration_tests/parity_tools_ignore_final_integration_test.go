//go:build integration

package integration_test

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"
	"github.com/lkshrk/omni/internal/config"
)

func TestCLIAndTUIToolsIgnoreProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	runParityFlow(t, bin, parityFlow{seed: seedParityToolInstall, runCLI: runToolsIgnoreFinalCLI, runTUI: runToolsIgnoreFinalTUI, observe: observeParityConfig, readTUI: readToolsFinalThroughCLI})
}

func runToolsIgnoreFinalCLI(t *testing.T, bin string, s *paritySandbox) {
	runOmniCommand(t, bin, s.root, s.env, "--config", s.configPath, "--cache-dir", s.cache, "tools", "ignore", "omni-test-tool")
}

func runToolsIgnoreFinalTUI(t *testing.T, bin string, s *paritySandbox) {
	runTUI(t, bin, s.root, s.env, []string{"--config", s.configPath, "--cache-dir", s.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, screenHas("Dashboard", "Tools"), "TUI did not start")
		writeTUIKeys(t, term, "\t")
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("omni-test-tool", "brew"), "TUI did not render tool")
		sendToolsIgnoreFinalKeyUntil(t, term, "j", func(text string) bool { return strings.Contains(text, ">") && strings.Contains(text, "omni-test-tool") }, "TUI did not select tool")
		sendToolsIgnoreFinalKeyUntil(t, term, "x", screenHas("Ignore: omni-test-tool", "all hosts", "enter save"), "TUI did not open ignore picker")
		writeTUIKeys(t, term, " ")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("[x] all hosts"), "TUI did not select global tool ignore")
		writeTUIKeys(t, term, "\r")
		return waitForRequiredScreen(t, term, 10*time.Second, func(string) bool {
			cfg, err := config.Load(s.configPath)
			spec, ok := cfg.Tools["omni-test-tool"]
			return err == nil && ok && spec.Ignore
		}, "TUI did not persist global tool ignore")
	})
}

func sendToolsIgnoreFinalKeyUntil(t *testing.T, term *vttest.Terminal, key string, ready func(string) bool, message string) {
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
