//go:build integration

package integration_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

func TestTUIAdminTerminalCompletesInteractiveBrewCaskUninstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PTY admin terminal is unsupported on Windows")
	}

	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	binDir := filepath.Join(root, "bin")
	installedState := filepath.Join(root, "parsec-installed")
	completedMarker := filepath.Join(root, "parsec-uninstalled")
	commandLog := filepath.Join(root, "brew.log")

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create fake bin dir: %v", err)
	}
	if err := os.WriteFile(installedState, []byte("installed\n"), 0o644); err != nil {
		t.Fatalf("seed fake brew state: %v", err)
	}
	writeFakeInteractiveBrew(t, binDir)
	env := append(isolatedTUIEnv(t, home, cache),
		"PATH="+binDir+":/usr/bin:/bin",
		"OMNI_TEST_BREW_STATE="+installedState,
		"OMNI_TEST_BREW_MARKER="+completedMarker,
		"OMNI_TEST_BREW_LOG="+commandLog,
	)

	if err := config.Save(configPath, &config.RootConfig{
		Version: config.CurrentVersion,
		Settings: config.Settings{
			DisabledProviders: []string{"apt", "apk", "dnf", "node", "pacman", "pip", "python", "zypper"},
		},
		Tools: map[string]config.ToolSpec{
			"parsec": {Providers: []config.ToolInstallSpec{{
				Provider: "brew",
				Package:  "parsec",
				Options:  map[string]string{"brew_kind": "cask"},
			}}},
		},
		Hosts: map[string][]string{"testhost": {"dev"}},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "dev", Tools: []config.ToolEntry{{Name: "parsec"}}},
		},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	seedTUIToolCache(t, cache, &database.ToolCache{
		Name:            "parsec",
		Provider:        "brew",
		Package:         "parsec",
		Installed:       true,
		InstalledWith:   "brew",
		Version:         sql.NullString{String: "150-103a", Valid: true},
		Tracked:         true,
		Privilege:       "maybe",
		PrivilegeReason: sql.NullString{String: "brew cask parsec uses pkgutil uninstall", Valid: true},
	})

	screen := runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, func(text string) bool {
			return strings.Contains(text, "Dashboard") && strings.Contains(text, "Tools")
		}, "TUI did not render main tabs")
		writeTUIKeys(t, term, "\t")
		waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "parsec") && strings.Contains(text, "brew")
		}, "TUI did not render the installed fake brew cask")
		writeTUIKeys(t, term, "d")
		waitForRequiredScreen(t, term, 2*time.Second, func(text string) bool {
			return strings.Contains(text, "confirm") && strings.Contains(text, "parsec")
		}, "TUI did not request delete confirmation")
		writeTUIKeys(t, term, "d")
		waitForRequiredScreen(t, term, 4*time.Second, func(text string) bool {
			return strings.Contains(text, "Admin Approval Required") &&
				strings.Contains(text, "brew uninstall --cask parsec") &&
				strings.Contains(text, "continue")
		}, "TUI did not open the admin terminal approval")
		writeTUIKeys(t, term, "\r")
		waitForRequiredScreen(t, term, 4*time.Second, func(text string) bool {
			return strings.Contains(text, "Password:")
		}, "nested admin terminal did not expose the fake password prompt")
		writeTUIKeys(t, term, "approve\r")
		waitForRequiredScreen(t, term, 6*time.Second, func(text string) bool {
			return strings.Contains(text, "PTY_OK") &&
				strings.Contains(text, "done") &&
				strings.Contains(text, "press any key to close")
		}, "nested admin terminal did not finish successfully")
		writeTUIKeys(t, term, " ")
		return waitForRequiredScreen(t, term, 6*time.Second, func(text string) bool {
			return strings.Contains(text, "Tools") &&
				!strings.Contains(text, "press any key to close")
		}, "finished admin terminal did not dismiss back to the tools view")
	})

	if strings.Contains(strings.ToLower(screen), "error") {
		t.Fatalf("TUI showed an error after admin-terminal completion; screen:\n%s", screen)
	}
	marker, err := os.ReadFile(completedMarker)
	if err != nil || strings.TrimSpace(string(marker)) != "parsec removed" {
		t.Fatalf("admin terminal completion marker = %q, %v", marker, err)
	}
	if _, err := os.Stat(installedState); !os.IsNotExist(err) {
		t.Fatalf("fake brew installed state still exists after uninstall: %v", err)
	}
	log, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatalf("read fake brew command log: %v", err)
	}
	for _, want := range []string{"uninstall --cask parsec", "stdin=tty", "stdout=tty", "stderr=tty", "answer=approve"} {
		if !strings.Contains(string(log), want) {
			t.Fatalf("fake brew log missing %q:\n%s", want, log)
		}
	}
}

func writeFakeInteractiveBrew(t *testing.T, binDir string) {
	t.Helper()
	script := `#!/bin/sh
set -eu
state="${OMNI_TEST_BREW_STATE:?}"
marker="${OMNI_TEST_BREW_MARKER:?}"
log="${OMNI_TEST_BREW_LOG:?}"
cask='{"token":"parsec","installed":"150-103a","artifacts":[{"uninstall":[{"pkgutil":"tv.parsec.www"}]},{"pkg":["parsec-macos.pkg"]}]}'
case "$*" in
  "--version") echo "Homebrew 4.0.0" ;;
  "leaves --installed-on-request") ;;
  "list --cask") [ ! -f "$state" ] || echo "parsec" ;;
  "list --versions --cask parsec") [ ! -f "$state" ] || echo "parsec 150-103a" ;;
  "info --json=v2 --installed"|"info --json=v2 --cask parsec")
    if [ -f "$state" ]; then printf '{"formulae":[],"casks":[%s]}\n' "$cask"; else echo '{"formulae":[],"casks":[]}'; fi
    ;;
  "outdated --json=v2 --greedy") echo '{"formulae":[],"casks":[]}' ;;
  "tap"|"update --quiet") ;;
  "uninstall --cask parsec")
	printf '%s\n' "$*" >> "$log"
	if [ -t 0 ]; then echo "stdin=tty" >> "$log"; else echo "stdin=not-tty" >> "$log"; exit 10; fi
	if [ -t 1 ]; then echo "stdout=tty" >> "$log"; else echo "stdout=not-tty" >> "$log"; exit 11; fi
	if [ -t 2 ]; then echo "stderr=tty" >> "$log"; else echo "stderr=not-tty" >> "$log"; exit 12; fi
	printf 'Password:'
	IFS= read -r answer
	printf 'answer=%s\n' "$answer" >> "$log"
	if [ "$answer" != "approve" ]; then echo "bad approval input" >&2; exit 13; fi
	rm -f "$state"
	echo "parsec removed" > "$marker"
	echo "PTY_OK"
    ;;
  *) echo "unexpected fake brew command: $*" >&2; exit 64 ;;
esac
`
	if err := os.WriteFile(filepath.Join(binDir, "brew"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake brew: %v", err)
	}
}
