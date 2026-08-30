//go:build integration

package integration_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
)

func TestCLIAndTUIDotsEditGroupsProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	runParityFlow(t, bin, parityFlow{
		seed:    seedMutationsFinalDotsGroups,
		runCLI:  runMutationsFinalDotsGroupsCLI,
		runTUI:  runMutationsFinalDotsGroupsTUI,
		observe: observeDotsLifecycleState,
		readTUI: readDotsLifecycleThroughCLI,
	})
}

func seedMutationsFinalDotsGroups(t *testing.T, sandbox *paritySandbox) {
	seedParityDotsConflict(t, sandbox)
	cfg, err := config.Load(sandbox.configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Hosts["testhost"] = []string{"work"}
	cfg.Groups = append(cfg.Groups, &config.GroupConfig{Name: "work"})
	if err := config.Save(sandbox.configPath, cfg); err != nil {
		t.Fatalf("save dots groups parity fixture: %v", err)
	}
}

func runMutationsFinalDotsGroupsCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"dots", "groups", "nvim", "--group", "work")
}

func runMutationsFinalDotsGroupsTUI(t *testing.T, bin string, sandbox *paritySandbox) {
	runTUI(t, bin, sandbox.root, sandbox.env, []string{"--config", sandbox.configPath, "--cache-dir", sandbox.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, screenHas("Dashboard", "Dots"), "TUI did not start")
		writeTUIKeys(t, term, "\t", "\t")
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("nvim", "conflict"), "TUI did not render dots entry")
		sendMutationsFinalKeyUntil(t, term, "j", func(text string) bool {
			return strings.Contains(text, ">") && strings.Contains(text, "nvim")
		}, "TUI did not select dots entry")
		sendMutationsFinalKeyUntil(t, term, "g", screenHas("Change Groups: nvim", "work", "testhost", "enter confirm"), "TUI did not open dots group picker")
		writeTUIKeys(t, term, " ", "j", " ", "\r")
		return waitForRequiredScreen(t, term, 10*time.Second, func(string) bool {
			cfg, err := config.Load(sandbox.configPath)
			return err == nil && mutationsFinalDotGroups(cfg, "nvim", "work")
		}, "TUI did not persist dots groups")
	})
}

func sendMutationsFinalKeyUntil(t *testing.T, term *vttest.Terminal, key string, ready func(string) bool, message string) {
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

func mutationsFinalDotGroups(cfg *config.RootConfig, name string, want ...string) bool {
	var got []string
	for _, group := range cfg.Groups {
		if group == nil {
			continue
		}
		for _, entry := range group.Dots {
			if entry.Name == name {
				got = append(got, group.Name)
			}
		}
	}
	slices.Sort(got)
	slices.Sort(want)
	return slices.Equal(got, want)
}
