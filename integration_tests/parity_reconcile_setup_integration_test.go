//go:build integration

package integration_test

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"
)

func TestCLIAndTUIReconcileProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	runParityFlow(t, bin, parityFlow{
		seed:    seedParityToolInstall,
		runCLI:  runReconcileParityCLI,
		runTUI:  runReconcileParityTUI,
		observe: observeParityToolInstall,
		readTUI: readParityToolThroughCLI,
	})
}

func runReconcileParityCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--yes", "--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"reconcile")
}

func runReconcileParityTUI(t *testing.T, bin string, sandbox *paritySandbox) {
	runTUI(t, bin, sandbox.root, sandbox.env, []string{"--config", sandbox.configPath, "--cache-dir", sandbox.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("Dashboard", "Tools"), "TUI did not start")
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			writeTUIKeys(t, term, "A")
			if _, ok := waitForScreen(term, 500*time.Millisecond, screenHas("Reconcile Plan", "Sync tools")); ok {
				writeTUIKeys(t, term, "\r")
				return waitForRequiredScreen(t, term, 12*time.Second, func(text string) bool {
					return parityToolInstalled(sandbox) && strings.Contains(strings.ToLower(text), "reconciled")
				}, "TUI reconcile did not install configured tool")
			}
		}
		t.Fatalf("TUI did not open reconcile plan; screen:\n%s", currentScreenText(term))
		return ""
	})
}
