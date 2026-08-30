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

func TestCLIAndTUIToolsFinalChangeGroupProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	runParityFlow(t, bin, parityFlow{
		seed:    seedToolsFinalGroupChange,
		runCLI:  runToolsFinalChangeGroupCLI,
		runTUI:  runToolsFinalChangeGroupTUI,
		observe: observeParityConfig,
		readTUI: readToolsFinalThroughCLI,
	})
}

func seedToolsFinalGroupChange(t *testing.T, sandbox *paritySandbox) {
	seedParityToolInstall(t, sandbox)
	cfg, err := config.Load(sandbox.configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Hosts["testhost"] = []string{"dev", "work"}
	cfg.Groups = append(cfg.Groups, &config.GroupConfig{Name: "work"})
	if err := config.Save(sandbox.configPath, cfg); err != nil {
		t.Fatalf("save group-change fixture: %v", err)
	}
}

func runToolsFinalChangeGroupCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"groups", "set-tool", "omni-test-tool", "work")
}

func runToolsFinalChangeGroupTUI(t *testing.T, bin string, sandbox *paritySandbox) {
	runToolsFinalTUI(t, bin, sandbox, func(term *vttest.Terminal) {
		sendToolsFinalKeyUntil(t, term, "g", screenHas("Change Groups: omni-test-tool", "dev", "work", "enter confirm"), "TUI did not open group picker")
		writeTUIKeys(t, term, "j", " ")
		sendToolsFinalKeyUntil(t, term, "j", func(text string) bool {
			for _, line := range strings.Split(text, "\n") {
				if strings.Contains(line, "work") && strings.Contains(line, "›") {
					return true
				}
			}
			return false
		}, "TUI did not select work group")
		writeTUIKeys(t, term, " ", "\r")
	}, func(cfg *config.RootConfig) bool {
		return toolsFinalMemberships(cfg, "omni-test-tool", "work")
	})
}

func runToolsFinalTUI(t *testing.T, bin string, sandbox *paritySandbox, act func(*vttest.Terminal), done func(*config.RootConfig) bool) {
	t.Helper()
	runTUI(t, bin, sandbox.root, sandbox.env, []string{"--config", sandbox.configPath, "--cache-dir", sandbox.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, screenHas("Dashboard", "Tools"), "TUI did not start")
		writeTUIKeys(t, term, "\t")
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("omni-test-tool", "brew"), "TUI did not render tool")
		sendToolsFinalKeyUntil(t, term, "j", func(text string) bool {
			return strings.Contains(text, ">") && strings.Contains(text, "omni-test-tool")
		}, "TUI did not select tool")
		act(term)
		return waitForRequiredScreen(t, term, 10*time.Second, func(string) bool {
			cfg, err := config.Load(sandbox.configPath)
			return err == nil && done(cfg)
		}, "TUI tool mutation did not persist")
	})
}

func sendToolsFinalKeyUntil(t *testing.T, term *vttest.Terminal, key string, ready func(string) bool, message string) {
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

func toolsFinalMemberships(cfg *config.RootConfig, tool string, want ...string) bool {
	var got []string
	for _, group := range cfg.Groups {
		if group == nil {
			continue
		}
		for _, entry := range group.Tools {
			if entry.Name == tool {
				got = append(got, group.Name)
			}
		}
	}
	slices.Sort(got)
	slices.Sort(want)
	return slices.Equal(got, want)
}

func readToolsFinalThroughCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"tools", "list", "--format", "json")
}
