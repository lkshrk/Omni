//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

func TestCLIAndTUIToolUpdateAllProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	runParityFlow(t, bin, parityFlow{
		seed:    seedParityToolUpdateAll,
		runCLI:  runParityToolUpdateAllCLI,
		runTUI:  runParityToolUpdateAllTUI,
		observe: observeParityToolUpdateAll,
		readTUI: readParityToolUpdateAllThroughCLI,
	})
}

func TestCLIAndTUIToolSyncAllProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	runParityFlow(t, bin, parityFlow{
		seed:    seedParityToolInstall,
		runCLI:  runParityToolSyncAllCLI,
		runTUI:  runParityToolSyncAllTUI,
		observe: observeParityToolInstall,
		readTUI: readParityToolThroughCLI,
	})
}

func seedParityToolUpdateAll(t *testing.T, sandbox *paritySandbox) {
	t.Helper()
	stateDir := filepath.Join(sandbox.root, "brew-state")
	logPath := filepath.Join(sandbox.root, "brew.log")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sandbox.env = append(sandbox.env, "OMNI_TEST_BREW_LOG="+logPath, "OMNI_TEST_BREW_STATE="+stateDir)
	writeFakeBulkUpgradeBrew(t, filepath.Join(sandbox.home, ".test-stub-bin", "brew"))

	tools := make(map[string]config.ToolSpec, 2)
	entries := make([]config.ToolEntry, 0, 2)
	for _, name := range []string{"omni-old-one", "omni-old-two"} {
		writeIntegrationFile(t, filepath.Join(stateDir, name), "1.0.0\n")
		tools[name] = config.ToolSpec{Providers: []config.ToolInstallSpec{{Provider: "brew", Package: name}}}
		entries = append(entries, config.ToolEntry{Name: name})
		seedTUIToolCache(t, sandbox.cache, &database.ToolCache{
			Name: name, Provider: "brew", Package: name, Installed: true, InstalledWith: "brew",
			Version:       sql.NullString{String: "1.0.0", Valid: true},
			LatestVersion: sql.NullString{String: "2.0.0", Valid: true},
			Outdated:      true, Tracked: true,
		})
	}
	if err := config.Save(sandbox.configPath, &config.RootConfig{
		Version: config.CurrentVersion,
		Settings: config.Settings{
			DisabledProviders: []string{"apt", "apk", "dnf", "node", "pacman", "pip", "python", "zypper"},
		},
		Tools: tools,
		Hosts: map[string][]string{"testhost": {"dev"}},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "dev", Tools: entries},
		},
	}); err != nil {
		t.Fatalf("save update-all parity config: %v", err)
	}
}

func runParityToolUpdateAllCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"tools", "upgrade", "--all", "--force")
}

func runParityToolUpdateAllTUI(t *testing.T, bin string, sandbox *paritySandbox) {
	runTUI(t, bin, sandbox.root, sandbox.env, []string{"--config", sandbox.configPath, "--cache-dir", sandbox.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, screenHas("Dashboard", "Tools"), "TUI did not render main tabs")
		writeTUIKeys(t, term, "\t")
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("omni-old-one", "omni-old-two"), "TUI did not render outdated tools")
		writeTUIKeys(t, term, "U")
		return waitForRequiredScreen(t, term, 12*time.Second, func(string) bool {
			return parityUpdateAllSettled(sandbox)
		}, "TUI did not update every tool")
	})
}

func runParityToolSyncAllCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--yes", "--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"tools", "sync", "--all")
}

func runParityToolSyncAllTUI(t *testing.T, bin string, sandbox *paritySandbox) {
	runTUI(t, bin, sandbox.root, sandbox.env, []string{"--config", sandbox.configPath, "--cache-dir", sandbox.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, screenHas("Dashboard", "Tools"), "TUI did not render main tabs")
		writeTUIKeys(t, term, "\t")
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("omni-test-tool", "brew"), "TUI did not render missing tool")
		writeTUIKeys(t, term, "S")
		waitForRequiredScreen(t, term, 3*time.Second, func(text string) bool {
			return strings.Contains(strings.ToLower(text), "press s again to sync all")
		}, "TUI did not arm sync all")
		writeTUIKeys(t, term, "S")
		return waitForRequiredScreen(t, term, 10*time.Second, func(string) bool {
			return parityToolInstalled(sandbox)
		}, "TUI did not sync all tools")
	})
}

type parityUpdateAllState struct {
	Config   any
	Versions map[string]string
	Cache    map[string]parityUpdateCacheState
	Upgrades []string
}

type parityUpdateCacheState struct {
	Installed     bool
	Outdated      bool
	Tracked       bool
	Provider      string
	Package       string
	InstalledWith string
	Version       string
}

func observeParityToolUpdateAll(t *testing.T, sandbox *paritySandbox) any {
	t.Helper()
	state := parityUpdateAllState{
		Config:   normalizedParityConfig(t, sandbox),
		Versions: make(map[string]string, 2),
		Cache:    make(map[string]parityUpdateCacheState, 2),
	}
	db, err := database.Open(filepath.Join(sandbox.cache, "omni.db"))
	if err != nil {
		t.Fatalf("open update-all cache: %v", err)
	}
	defer db.Close()
	for _, name := range []string{"omni-old-one", "omni-old-two"} {
		raw, err := os.ReadFile(filepath.Join(sandbox.root, "brew-state", name))
		if err != nil {
			t.Fatalf("read %s provider state: %v", name, err)
		}
		state.Versions[name] = strings.TrimSpace(string(raw))
		tool, err := db.Get(context.Background(), name, "brew", name)
		if err != nil {
			t.Fatalf("read %s cache: %v", name, err)
		}
		state.Cache[name] = parityUpdateCacheState{
			Installed: tool.Installed, Outdated: tool.Outdated, Tracked: tool.Tracked,
			Provider: tool.Provider, Package: tool.Package, InstalledWith: tool.InstalledWith, Version: tool.Version.String,
		}
	}
	if raw, err := os.ReadFile(filepath.Join(sandbox.root, "brew.log")); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(line, "upgrade --formula ") {
				state.Upgrades = append(state.Upgrades, line)
			}
		}
	}
	sort.Strings(state.Upgrades)
	return state
}

func parityUpdateAllSettled(sandbox *paritySandbox) bool {
	db, err := database.Open(filepath.Join(sandbox.cache, "omni.db"))
	if err != nil {
		return false
	}
	defer db.Close()
	for _, name := range []string{"omni-old-one", "omni-old-two"} {
		tool, err := db.Get(context.Background(), name, "brew", name)
		if err != nil || tool.Version.String != "2.0.0" || tool.Outdated {
			return false
		}
	}
	return true
}

func readParityToolUpdateAllThroughCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"tools", "list", "--format", "json")
}
