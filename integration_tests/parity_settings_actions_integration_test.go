//go:build integration

package integration_test

import (
	"context"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

var parityProviderPriority = []string{"brew", "apt", "apk", "dnf", "pacman", "zypper", "bun", "pnpm", "npm", "uv", "pip", "cargo"}

func TestCLIAndTUISettingsProviderProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	runParityFlow(t, bin, parityFlow{seed: seedParitySettingsActions, runCLI: func(t *testing.T, bin string, s *paritySandbox) {
		runOmniCommand(t, bin, s.root, s.env, "--config", s.configPath, "--cache-dir", s.cache, "settings", "disable-provider", "brew")
	}, runTUI: func(t *testing.T, bin string, s *paritySandbox) {
		runParitySettingsTUI(t, bin, s, func(term *vttest.Terminal) {
			sendTUIKey(term, uv.KeyHome)
			writeTUIKeys(t, term, "j\r")
			waitForRequiredScreen(t, term, 3*time.Second, screenHas("Provider Priority", "x on/off"), "TUI did not open provider editor")
			writeTUIKeys(t, term, "x\r")
		}, func(cfg *config.RootConfig) bool {
			return slices.Contains(cfg.HostSettings["testhost"].DisabledProviders, "brew")
		})
	}, observe: observeParityProviderSettings, readTUI: readParitySettingsThroughCLI})
}

type parityProviderSettingsState struct {
	Priority, Disabled string
	Hosts, Groups      int
}

func observeParityProviderSettings(t *testing.T, sandbox *paritySandbox) any {
	t.Helper()
	cfg, err := config.Load(sandbox.configPath)
	if err != nil {
		t.Fatal(err)
	}
	effective := cfg.EffectiveSettings("testhost")
	disabled := append([]string(nil), effective.DisabledProviders...)
	sort.Strings(disabled)
	return parityProviderSettingsState{Priority: strings.Join(effective.ProviderPriority, ","), Disabled: strings.Join(disabled, ","), Hosts: len(cfg.Hosts), Groups: len(cfg.Groups)}
}

func TestCLIAndTUISettingsResetProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	runParityFlow(t, bin, parityFlow{
		seed:    seedParitySettingsActions,
		runCLI:  runParitySettingsResetCLI,
		runTUI:  runParitySettingsResetTUI,
		observe: observeParityConfig,
		readTUI: readParitySettingsThroughCLI,
	})
}

func TestCLIAndTUISettingsResetCacheProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	runParityFlow(t, bin, parityFlow{
		seed:    seedParitySettingsCache,
		runCLI:  runParitySettingsResetCacheCLI,
		runTUI:  runParitySettingsResetCacheTUI,
		observe: observeParitySettingsCache,
		readTUI: readParityCacheThroughCLI,
	})
}

func seedParitySettingsActions(t *testing.T, sandbox *paritySandbox) {
	t.Helper()
	if err := config.Save(sandbox.configPath, &config.RootConfig{
		Version: config.CurrentVersion,
		Settings: config.Settings{
			AutoImport:       true,
			ProviderPriority: append([]string(nil), parityProviderPriority...),
			UpdateQuarantine: "24h",
		},
		HostSettings: map[string]config.Settings{
			"testhost": {
				ProviderPriority:  []string{"brew", "apt", "apk"},
				DisabledProviders: []string{"system", "node", "python", "pip"},
			},
		},
		Hosts: map[string][]string{"testhost": {"work"}},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "work"},
		},
	}); err != nil {
		t.Fatalf("save settings parity fixture: %v", err)
	}
}

func seedParitySettingsCache(t *testing.T, sandbox *paritySandbox) {
	seedParitySettingsActions(t, sandbox)
	db, err := database.Open(filepath.Join(sandbox.cache, "omni.db"))
	if err != nil {
		t.Fatalf("open parity cache: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate parity cache: %v", err)
	}
	if err := db.Upsert(context.Background(), &database.ToolCache{
		Name: "fixture", Provider: "script", Package: "fixture", Installed: true,
	}); err != nil {
		t.Fatalf("seed parity cache: %v", err)
	}
}

func runParitySettingsResetCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--yes", "--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"settings", "reset")
}

func runParitySettingsResetTUI(t *testing.T, bin string, sandbox *paritySandbox) {
	runParitySettingsTUI(t, bin, sandbox, func(term *vttest.Terminal) {
		selectParityResetCache(t, term)
		writeTUIKeys(t, term, "k")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("> Reset Settings"), "TUI did not select reset settings")
		writeTUIKeys(t, term, "\r")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("Reset Settings", "confirm"), "TUI did not arm settings reset")
		writeTUIKeys(t, term, "\r")
	}, func(cfg *config.RootConfig) bool {
		return !cfg.Settings.AutoImport && len(cfg.Settings.ProviderPriority) == 0 && cfg.Settings.UpdateQuarantine == ""
	})
}

func runParitySettingsResetCacheCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--yes", "--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"settings", "reset-cache")
}

func runParitySettingsResetCacheTUI(t *testing.T, bin string, sandbox *paritySandbox) {
	runParitySettingsTUI(t, bin, sandbox, func(term *vttest.Terminal) {
		selectParityResetCache(t, term)
		writeTUIKeys(t, term, "\r")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("Reset Cache", "confirm"), "TUI did not arm cache reset")
		writeTUIKeys(t, term, "\r")
	}, func(*config.RootConfig) bool {
		return parityCacheToolCount(sandbox) == 0
	})
}

func runParitySettingsTUI(t *testing.T, bin string, sandbox *paritySandbox, act func(*vttest.Terminal), done func(*config.RootConfig) bool) {
	t.Helper()
	runTUI(t, bin, sandbox.root, sandbox.env, []string{"--config", sandbox.configPath, "--cache-dir", sandbox.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, screenHas("Dashboard", "Settings"), "TUI did not render main tabs")
		writeTUIKeys(t, term, "\t", "\t", "\t", "\t", "\t")
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("Provider Priority", "Maintenance"), "TUI did not render settings")
		act(term)
		screen, ok := waitForScreen(term, 10*time.Second, func(string) bool {
			cfg, err := config.Load(sandbox.configPath)
			return err == nil && done(cfg)
		})
		if !ok {
			cfg, err := config.Load(sandbox.configPath)
			t.Fatalf("TUI settings action did not persist: settings=%+v err=%v; screen:\n%s", cfg.Settings, err, screen)
		}
		return screen
	})
}

func selectParityResetCache(t *testing.T, term *vttest.Terminal) {
	t.Helper()
	sendParityKeyUntil(t, term, "k", screenHas("> Import Installed Tools"), "TUI did not reveal settings cursor")
	sendParityKeyUntil(t, term, "k", screenHas("> Reset Cache"), "TUI did not select reset cache")
}

func sendParityKeyUntil(t *testing.T, term *vttest.Terminal, key string, ready func(string) bool, message string) {
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

type paritySettingsCacheState struct {
	Config    any
	ToolCount int
}

func observeParitySettingsCache(t *testing.T, sandbox *paritySandbox) any {
	t.Helper()
	return paritySettingsCacheState{
		Config:    normalizedParityConfig(t, sandbox),
		ToolCount: parityCacheToolCount(sandbox),
	}
}

func parityCacheToolCount(sandbox *paritySandbox) int {
	db, err := database.Open(filepath.Join(sandbox.cache, "omni.db"))
	if err != nil {
		return -1
	}
	defer db.Close()
	tools, err := db.List(context.Background())
	if err != nil {
		return -1
	}
	return len(tools)
}

func readParitySettingsThroughCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"settings", "show", "--format", "json")
}

func readParityCacheThroughCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"tools", "list", "--format", "json")
}
