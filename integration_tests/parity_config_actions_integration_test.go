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

func TestCLIAndTUIGroupCreateProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	runParityFlow(t, bin, parityFlow{seed: seedParityConfigActions, runCLI: runParityGroupCreateCLI, runTUI: runParityGroupCreateTUI, observe: observeParityConfig, readTUI: readParityGroupsThroughCLI})
}

func runParityGroupCreateCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env, "--config", sandbox.configPath, "--cache-dir", sandbox.cache, "groups", "create", "dev")
}

func runParityGroupCreateTUI(t *testing.T, bin string, sandbox *paritySandbox) {
	runParityConfigTUI(t, bin, sandbox, func(term *vttest.Terminal) {
		sendParityKeyUntil(t, term, "n", screenHas("New Group", "group name"), "TUI did not open group creation")
		writeTUIKeys(t, term, "dev\r")
	}, func(cfg *config.RootConfig) bool {
		return configHasGroup(cfg, "dev") && !slices.Contains(cfg.Hosts["testhost"], "dev")
	})
}

func TestCLIAndTUIGroupRenameProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	runParityFlow(t, bin, parityFlow{
		seed:    seedParityConfigActions,
		runCLI:  runParityGroupRenameCLI,
		runTUI:  runParityGroupRenameTUI,
		observe: observeParityConfig,
		readTUI: readParityGroupsThroughCLI,
	})
}

func TestCLIAndTUIGroupDeleteProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	runParityFlow(t, bin, parityFlow{
		seed:    seedParityConfigActions,
		runCLI:  runParityGroupDeleteCLI,
		runTUI:  runParityGroupDeleteTUI,
		observe: observeParityConfig,
		readTUI: readParityGroupsThroughCLI,
	})
}

func TestCLIAndTUIHostCreateProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	runParityFlow(t, bin, parityFlow{
		seed:    seedParityConfigActions,
		runCLI:  runParityHostCreateCLI,
		runTUI:  runParityHostCreateTUI,
		observe: observeParityConfig,
		readTUI: readParityHostsThroughCLI,
	})
}

func TestCLIAndTUIHostDeleteProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	runParityFlow(t, bin, parityFlow{
		seed:    seedParityConfigActionsWithLaptop,
		runCLI:  runParityHostDeleteCLI,
		runTUI:  runParityHostDeleteTUI,
		observe: observeParityConfig,
		readTUI: readParityHostsThroughCLI,
	})
}

func TestCLIAndTUIHostEditGroupsProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	runParityFlow(t, bin, parityFlow{
		seed:    seedParityConfigActions,
		runCLI:  runParityHostEditGroupsCLI,
		runTUI:  runParityHostEditGroupsTUI,
		observe: observeParityConfig,
		readTUI: readParityHostsThroughCLI,
	})
}

func seedParityConfigActions(t *testing.T, sandbox *paritySandbox) {
	t.Helper()
	if err := config.Save(sandbox.configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{DisabledProviders: []string{"apt", "apk", "dnf", "pacman", "zypper", "brew", "node", "bun", "pnpm", "npm", "python", "uv", "pip"}},
		Hosts:    map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "work"},
		},
	}); err != nil {
		t.Fatalf("save config action parity fixture: %v", err)
	}
}

func seedParityConfigActionsWithLaptop(t *testing.T, sandbox *paritySandbox) {
	seedParityConfigActions(t, sandbox)
	cfg, err := config.Load(sandbox.configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Hosts["laptop"] = []string{"work"}
	cfg.Groups = append(cfg.Groups, &config.GroupConfig{Name: "laptop", Special: "host"})
	if err := config.Save(sandbox.configPath, cfg); err != nil {
		t.Fatalf("add laptop parity fixture: %v", err)
	}
}

func runParityGroupRenameCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"groups", "rename", "work", "dev")
}

func runParityGroupRenameTUI(t *testing.T, bin string, sandbox *paritySandbox) {
	runParityConfigTUI(t, bin, sandbox, func(term *vttest.Terminal) {
		selectParityWorkGroup(t, term)
		writeTUIKeys(t, term, "r")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("Rename:", "work"), "TUI did not open group rename")
		writeTUIKeys(t, term, "\x7f\x7f\x7f\x7f", "dev\r")
	}, func(cfg *config.RootConfig) bool {
		return parityGroupExists(cfg, "dev") && !parityGroupExists(cfg, "work")
	})
}

func runParityGroupDeleteCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--yes", "--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"groups", "delete", "work", "--delete-tools")
}

func runParityGroupDeleteTUI(t *testing.T, bin string, sandbox *paritySandbox) {
	runParityConfigTUI(t, bin, sandbox, func(term *vttest.Terminal) {
		selectParityWorkGroup(t, term)
		writeTUIKeys(t, term, "d")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("Delete Group", "enter delete"), "TUI did not arm group deletion")
		writeTUIKeys(t, term, "\r")
	}, func(cfg *config.RootConfig) bool {
		return !parityGroupExists(cfg, "work")
	})
}

func runParityHostCreateCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"hosts", "ensure", "laptop")
}

func runParityHostCreateTUI(t *testing.T, bin string, sandbox *paritySandbox) {
	runParityConfigTUI(t, bin, sandbox, func(term *vttest.Terminal) {
		writeTUIKeys(t, term, "p", "laptop\r", "n")
	}, func(cfg *config.RootConfig) bool {
		_, ok := cfg.Hosts["laptop"]
		return ok
	})
}

func runParityHostDeleteCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--yes", "--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"hosts", "remove", "laptop")
}

func runParityHostDeleteTUI(t *testing.T, bin string, sandbox *paritySandbox) {
	runParityConfigTUI(t, bin, sandbox, func(term *vttest.Terminal) {
		writeTUIKeys(t, term, "j", "j", "d", "d")
	}, func(cfg *config.RootConfig) bool {
		_, ok := cfg.Hosts["laptop"]
		return !ok
	})
}

func runParityHostEditGroupsCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"hosts", "set-groups", "testhost", "work")
}

func runParityHostEditGroupsTUI(t *testing.T, bin string, sandbox *paritySandbox) {
	runParityConfigTUI(t, bin, sandbox, func(term *vttest.Terminal) {
		writeTUIKeys(t, term, "j")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("testhost", "g edit groups"), "TUI did not select testhost")
		writeTUIKeys(t, term, "g")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("[ ]", "work", "save"), "TUI did not open host group picker")
		writeTUIKeys(t, term, "j", " ")
		waitForRequiredScreen(t, term, 3*time.Second, func(text string) bool {
			for _, line := range strings.Split(text, "\n") {
				if strings.Contains(line, "[x]") && strings.Contains(line, "work") {
					return true
				}
			}
			return false
		}, "TUI did not toggle work group")
		writeTUIKeys(t, term, "\r")
	}, func(cfg *config.RootConfig) bool {
		groups, ok := cfg.Hosts["testhost"]
		return ok && len(groups) == 1 && groups[0] == "work"
	})
}

func selectParityWorkGroup(t *testing.T, term *vttest.Terminal) {
	t.Helper()
	writeTUIKeys(t, term, "k")
	waitForRequiredScreen(t, term, 3*time.Second, func(text string) bool {
		return strings.Contains(text, "> testhost")
	}, "TUI did not reveal the host cursor")
	writeTUIKeys(t, term, "k")
	waitForRequiredScreen(t, term, 3*time.Second, func(text string) bool {
		return strings.Contains(text, "> work")
	}, "TUI did not select work group")
}

func runParityConfigTUI(t *testing.T, bin string, sandbox *paritySandbox, act func(*vttest.Terminal), done func(*config.RootConfig) bool) {
	t.Helper()
	runTUI(t, bin, sandbox.root, sandbox.env, []string{"--config", sandbox.configPath, "--cache-dir", sandbox.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, screenHas("Dashboard", "Tools"), "TUI did not render main tabs")
		writeTUIKeys(t, term, "\t", "\t", "\t", "\t")
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("Group Assignments", "Groups"), "TUI did not render groups")
		act(term)
		return waitForRequiredScreen(t, term, 10*time.Second, func(string) bool {
			cfg, err := config.Load(sandbox.configPath)
			return err == nil && done(cfg)
		}, "TUI config action did not persist")
	})
}

func parityGroupExists(cfg *config.RootConfig, name string) bool {
	for _, group := range cfg.Groups {
		if group != nil && group.Name == name {
			return true
		}
	}
	return false
}

func readParityGroupsThroughCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache, "groups")
}

func readParityHostsThroughCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache, "hosts", "list")
}
