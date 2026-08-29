//go:build integration

package integration_test

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

var selectedToolDetail = regexp.MustCompile(`details for tool-\d{2}`)

func TestTUIShellContractNavigatesWithTerminalKeysAndMouse(t *testing.T) {
	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	env := isolatedTUIEnv(t, home, cache)

	tools := make(map[string]config.ToolSpec, 24)
	entries := make([]config.ToolEntry, 0, len(tools))
	cached := make([]*database.ToolCache, 0, len(tools))
	for i := range 24 {
		name := fmt.Sprintf("tool-%02d", i)
		tools[name] = config.ToolSpec{Providers: []config.ToolInstallSpec{{Provider: "apt", Package: name}}}
		entries = append(entries, config.ToolEntry{Name: name})
		cached = append(cached, &database.ToolCache{
			Name:          name,
			Provider:      "apt",
			Package:       name,
			Installed:     true,
			InstalledWith: "apt",
			Version:       sql.NullString{String: "1.0.0", Valid: true},
			Description:   sql.NullString{String: "details for " + name, Valid: true},
			Tracked:       true,
		})
	}
	if err := config.Save(configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{DisabledProviders: []string{"apt", "apk", "brew", "dnf", "node", "pacman", "pip", "python", "zypper"}},
		Tools:    tools,
		Hosts:    map[string][]string{"testhost": {"dev"}},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "dev", Tools: entries},
		},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	seedTUIToolCache(t, cache, cached...)

	runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("Dashboard", "Tools"), "TUI did not render main tabs")

		writeTUIKeys(t, term, "\x1b[Z")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("Import Installed Tools", "Maintenance"), "Shift+Tab did not wrap backward to Settings")
		writeTUIKeys(t, term, "\t")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("Health Check", "Tool Updates"), "Tab did not wrap forward to Dashboard")
		clickTUITab(t, term, "Tools")
		waitForRequiredScreen(t, term, 4*time.Second, screenHas("tool-00", "tool-01"), "mouse click did not open Tools")

		sendTUIKey(term, uv.KeyHome)
		first := waitForSelectedTool(t, term, "tool-00", "Home did not select the first tool")

		writeTUIKeys(t, term, "k")
		waitForSelectedTool(t, term, "tool-23", "Up did not wrap from the first row to the last")
		writeTUIKeys(t, term, "j")
		waitForSelectedTool(t, term, "tool-00", "Down did not wrap from the last row to the first")

		writeTUIKeys(t, term, "\x04")
		halfPage := waitForRequiredScreen(t, term, 3*time.Second, func(text string) bool {
			return selectedToolName(text) != "" && selectedToolName(text) != "tool-00"
		}, "Ctrl+D did not move the selected row")
		writeTUIKeys(t, term, "\x15")
		waitForSelectedTool(t, term, "tool-00", "Ctrl+U did not return to the first row")

		sendTUIKey(term, uv.KeyPgDown)
		pageDown := waitForRequiredScreen(t, term, 3*time.Second, func(text string) bool {
			return selectedToolName(text) != "" && selectedToolName(text) != "tool-00"
		}, "physical PageDown did not move the selected row")
		sendTUIKey(term, uv.KeyPgUp)
		waitForSelectedTool(t, term, "tool-00", "physical PageUp did not return to the first row")

		sendTUIMouse(term, uv.MouseWheelEvent(uv.Mouse{X: 10, Y: 5, Button: uv.MouseWheelDown}))
		waitForSelectedTool(t, term, "tool-01", "mouse wheel down did not move the selected row")
		sendTUIMouse(term, uv.MouseWheelEvent(uv.Mouse{X: 10, Y: 5, Button: uv.MouseWheelUp}))
		last := waitForSelectedTool(t, term, "tool-00", "mouse wheel up did not return to the first row")

		for label, screen := range map[string]string{"initial": first, "half-page": halfPage, "page-down": pageDown, "final": last} {
			if got := selectedToolName(screen); got == "" {
				t.Fatalf("%s screen omitted selected-row details:\n%s", label, screen)
			}
		}
		return last
	})
}

func TestTUIShellContractShowsTransientInstallProgress(t *testing.T) {
	bin := buildOmniBinary(t)
	root := t.TempDir()
	sandbox := newParitySandbox(t, root)
	seedParityToolInstall(t, sandbox)
	barrier := filepath.Join(root, "brew-install")
	sandbox.env = append(sandbox.env, "OMNI_TEST_BREW_BARRIER="+barrier)
	t.Cleanup(func() { _ = os.WriteFile(barrier+".release", nil, 0o600) })

	runTUI(t, bin, root, sandbox.env, []string{"--config", sandbox.configPath, "--cache-dir", sandbox.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, screenHas("Dashboard", "Tools"), "TUI did not render main tabs")
		writeTUIKeys(t, term, "\t")
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("omni-test-tool", "brew"), "TUI did not render the missing tool")
		writeTUIKeys(t, term, "i")
		progress := waitForRequiredScreen(t, term, 5*time.Second, func(text string) bool {
			_, started := os.Stat(barrier + ".started")
			return started == nil && strings.Contains(text, "Installing omni-test-tool")
		}, "TUI did not render install progress while the provider was blocked")
		if err := os.WriteFile(barrier+".release", nil, 0o600); err != nil {
			t.Fatal(err)
		}
		waitForRequiredScreen(t, term, 8*time.Second, func(string) bool {
			return parityToolInstalled(sandbox)
		}, "TUI did not finish the blocked install")
		return progress
	})
}

func screenHas(parts ...string) func(string) bool {
	return func(text string) bool {
		for _, part := range parts {
			if !strings.Contains(text, part) {
				return false
			}
		}
		return true
	}
}

func selectedToolName(screen string) string {
	detail := selectedToolDetail.FindString(screen)
	return strings.TrimPrefix(detail, "details for ")
}

func waitForSelectedTool(t *testing.T, term *vttest.Terminal, name, message string) string {
	t.Helper()
	return waitForRequiredScreen(t, term, 3*time.Second, func(text string) bool {
		return selectedToolName(text) == name
	}, message)
}

func clickTUITab(t *testing.T, term *vttest.Terminal, label string) {
	t.Helper()
	line, _, _ := strings.Cut(ansi.Strip(term.Emulator.Render()), "\n")
	byteOffset := strings.Index(line, label)
	if byteOffset < 0 {
		t.Fatalf("tab %q is not visible in header %q", label, line)
	}
	x := ansi.StringWidth(line[:byteOffset]) + ansi.StringWidth(label)/2
	sendTUIMouse(term, uv.MouseClickEvent(uv.Mouse{X: x, Y: 0, Button: uv.MouseLeft}))
}

func sendTUIKey(term *vttest.Terminal, code rune) {
	term.Emulator.SendKey(uv.KeyPressEvent{Code: code})
}

func sendTUIMouse(term *vttest.Terminal, event uv.MouseEvent) {
	term.Emulator.SendMouse(event)
}
