//go:build integration

package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/database"
)

func TestCLIAndTUIToolsRefreshProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	runParityFlow(t, bin, parityFlow{
		seed:    seedParityScriptTool,
		runCLI:  runSystemToolsRefreshCLI,
		runTUI:  runSystemToolsRefreshTUI,
		observe: observeSystemToolsRefresh,
		readTUI: readSystemToolsRefreshThroughCLI,
	})
}

func runSystemToolsRefreshCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"tools", "refresh")
}

func runSystemToolsRefreshTUI(t *testing.T, bin string, sandbox *paritySandbox) {
	runTUI(t, bin, sandbox.root, sandbox.env, []string{"--config", sandbox.configPath, "--cache-dir", sandbox.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, screenHas("Dashboard", "Tools"), "TUI did not start")
		writeTUIKeys(t, term, "\t")
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("fixture", "script"), "TUI did not render script tool")
		before := systemRefreshCommandCount(sandbox)
		sendSystemFlowKeyUntil(t, term, "R", func(string) bool {
			return systemRefreshCommandCount(sandbox) > before && systemRefreshSettled(sandbox)
		}, "TUI did not refresh tool state")
		return currentScreenText(term)
	})
}

func systemRefreshCommandCount(sandbox *paritySandbox) int {
	raw, _ := os.ReadFile(filepath.Join(sandbox.root, "provider.log"))
	count := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "latest" {
			count++
		}
	}
	return count
}

func sendSystemFlowKeyUntil(t *testing.T, term *vttest.Terminal, key string, ready func(string) bool, message string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		writeTUIKeys(t, term, key)
		if _, ok := waitForScreen(term, 700*time.Millisecond, ready); ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s; screen:\n%s", message, currentScreenText(term))
}

type systemToolsRefreshState struct {
	Config        any
	Name          string
	Provider      string
	Package       string
	Installed     bool
	InstalledWith string
	Version       string
	LatestVersion string
	Outdated      bool
	Tracked       bool
}

func observeSystemToolsRefresh(t *testing.T, sandbox *paritySandbox) any {
	t.Helper()
	db, err := database.Open(filepath.Join(sandbox.cache, "omni.db"))
	if err != nil {
		t.Fatalf("open refreshed cache: %v", err)
	}
	defer db.Close()
	tool, err := db.Get(context.Background(), "fixture", "script", "fixture")
	if err != nil {
		t.Fatalf("read refreshed tool: %v", err)
	}
	return systemToolsRefreshState{
		Config:        normalizedParityConfig(t, sandbox),
		Name:          tool.Name,
		Provider:      tool.Provider,
		Package:       tool.Package,
		Installed:     tool.Installed,
		InstalledWith: tool.InstalledWith,
		Version:       tool.Version.String,
		LatestVersion: tool.LatestVersion.String,
		Outdated:      tool.Outdated,
		Tracked:       tool.Tracked,
	}
}

func systemRefreshSettled(sandbox *paritySandbox) bool {
	db, err := database.Open(filepath.Join(sandbox.cache, "omni.db"))
	if err != nil {
		return false
	}
	defer db.Close()
	tool, err := db.Get(context.Background(), "fixture", "script", "fixture")
	return err == nil && tool.Installed && tool.Version.String == "1.0.0" && tool.LatestVersion.String == "1.1.0" && tool.Outdated
}

func readSystemToolsRefreshThroughCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	out := runOmniOutput(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"tools", "list", "fixture", "--format", "json")
	if !strings.Contains(out, `"latest_version":"1.1.0"`) {
		t.Fatalf("CLI did not observe refreshed TUI state: %s", out)
	}
}
