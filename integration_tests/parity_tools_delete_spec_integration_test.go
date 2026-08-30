//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

func TestCLIAndTUIToolsDeleteSpecProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	runParityFlow(t, bin, parityFlow{
		seed:    seedToolsDeleteSpecParity,
		runCLI:  runToolsDeleteSpecParityCLI,
		runTUI:  runToolsDeleteSpecParityTUI,
		observe: observeToolsDeleteSpecParity,
		readTUI: readToolsDeleteSpecThroughCLI,
	})
}

func seedToolsDeleteSpecParity(t *testing.T, s *paritySandbox) {
	if err := config.Save(s.configPath, &config.RootConfig{
		Version: config.CurrentVersion,
		Settings: config.Settings{DisabledProviders: []string{
			"apk", "brew", "dnf", "node", "pacman", "pip", "python", "zypper",
		}},
		Tools: map[string]config.ToolSpec{
			"omni-missing": {Providers: []config.ToolInstallSpec{{Provider: "apt", Package: "omni-missing"}}},
		},
		Hosts: map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{{
			Name: "testhost", Special: "host", Tools: []config.ToolEntry{{Name: "omni-missing"}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	seedTUIToolCache(t, s.cache, &database.ToolCache{
		Name: "omni-missing", Provider: "apt", Package: "omni-missing",
		Installed: false, Tracked: true, Version: sql.NullString{},
	})
}

func runToolsDeleteSpecParityCLI(t *testing.T, bin string, s *paritySandbox) {
	runOmniCommand(t, bin, s.root, s.env,
		"--yes", "--config", s.configPath, "--cache-dir", s.cache,
		"tools", "delete-spec", "omni-missing")
}

func runToolsDeleteSpecParityTUI(t *testing.T, bin string, s *paritySandbox) {
	runTUI(t, bin, s.root, s.env, []string{"--config", s.configPath, "--cache-dir", s.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 7*time.Second, screenHas("Dashboard", "Tools"), "TUI did not start")
		writeTUIKeys(t, term, "\t")
		waitForRequiredScreen(t, term, 9*time.Second, screenHas("omni-missing", "apt"), "TUI did not render missing tracked tool")
		writeTUIKeys(t, term, "j")
		waitForRequiredScreen(t, term, 4*time.Second, func(text string) bool {
			return strings.Contains(text, ">") && strings.Contains(text, "omni-missing")
		}, "TUI did not select missing tracked tool")
		writeTUIKeys(t, term, "d")
		waitForRequiredScreen(t, term, 4*time.Second, screenHas("confirm delete", "omni-missing"), "TUI did not arm spec deletion")
		writeTUIKeys(t, term, "d")
		return waitForRequiredScreen(t, term, 10*time.Second, func(string) bool {
			cfg, err := config.Load(s.configPath)
			return err == nil && !deleteSpecConfigured(cfg, "omni-missing")
		}, "TUI did not remove tool spec")
	})
}

type deleteSpecCacheRow struct {
	Name, Provider, Package, InstalledWith, Version string
	Installed, Tracked, Outdated                    bool
}

type deleteSpecState struct {
	Config any
	Rows   []deleteSpecCacheRow
}

func observeToolsDeleteSpecParity(t *testing.T, s *paritySandbox) any {
	db, err := database.Open(filepath.Join(s.cache, "omni.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tools, err := db.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state := deleteSpecState{Config: normalizedParityConfig(t, s)}
	for _, tool := range tools {
		if tool.Name != "omni-missing" {
			continue
		}
		state.Rows = append(state.Rows, deleteSpecCacheRow{
			Name: tool.Name, Provider: tool.Provider, Package: tool.Package,
			InstalledWith: tool.InstalledWith, Version: tool.Version.String,
			Installed: tool.Installed, Tracked: tool.Tracked, Outdated: tool.Outdated,
		})
	}
	return state
}

func deleteSpecConfigured(cfg *config.RootConfig, name string) bool {
	if _, ok := cfg.Tools[name]; ok {
		return true
	}
	for _, group := range cfg.Groups {
		if group == nil {
			continue
		}
		for _, tool := range group.Tools {
			if tool.Name == name {
				return true
			}
		}
	}
	return false
}

func readToolsDeleteSpecThroughCLI(t *testing.T, bin string, s *paritySandbox) {
	runOmniCommand(t, bin, s.root, s.env,
		"--config", s.configPath, "--cache-dir", s.cache,
		"tools", "list", "--format", "json")
}
