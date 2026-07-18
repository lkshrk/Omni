//go:build integration

package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"
	"github.com/lkshrk/omni/internal/config"
)

func TestTUIDashboardReconcileRecoversAfterInjectedInstallFailure(t *testing.T) {
	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	installedMarker := filepath.Join(root, "brew-installed")
	failedMarker := filepath.Join(root, "brew-failed-once")
	brewLog := filepath.Join(root, "brew.log")
	env := append(isolatedTUIEnv(t, home, cache),
		"OMNI_TEST_BREW_STATE="+installedMarker,
		"OMNI_TEST_BREW_FAILED="+failedMarker,
		"OMNI_TEST_BREW_LOG="+brewLog,
	)

	brew := `#!/bin/sh
set -eu
state="${OMNI_TEST_BREW_STATE:?}"
failed="${OMNI_TEST_BREW_FAILED:?}"
log="${OMNI_TEST_BREW_LOG:?}"
printf '%s\n' "$*" >> "$log"
case "$*" in
	"--version") echo "Homebrew 4.0.0" ;;
	"install omni-reconcile-tool")
		if [ ! -f "$failed" ]; then
			touch "$failed"
			echo "boom-once" >&2
			exit 42
		fi
		echo "omni-reconcile-tool" > "$state"
		;;
	"list --versions omni-reconcile-tool")
		[ -f "$state" ] || exit 1
		echo "omni-reconcile-tool 1.2.3"
		;;
	"list --versions --cask omni-reconcile-tool") exit 1 ;;
	"leaves --installed-on-request") [ ! -f "$state" ] || echo "omni-reconcile-tool" ;;
	"list --cask") ;;
	"list --versions --formula") [ ! -f "$state" ] || echo "omni-reconcile-tool 1.2.3" ;;
	"info --json=v2 --installed")
		if [ -f "$state" ]; then
			echo '{"formulae":[{"name":"omni-reconcile-tool","full_name":"omni-reconcile-tool","desc":"integration fixture","installed":[{"version":"1.2.3","installed_on_request":true}]}],"casks":[]}'
		else
			echo '{"formulae":[],"casks":[]}'
		fi
		;;
	info\ --json=v2*) echo '{"formulae":[{"name":"omni-reconcile-tool","full_name":"omni-reconcile-tool","desc":"integration fixture","installed":[]}],"casks":[]}' ;;
	"outdated --json=v2 --greedy") echo '{"formulae":[],"casks":[]}' ;;
	"update --quiet") ;;
	"search omni-reconcile-tool") echo "omni-reconcile-tool" ;;
	"tap") ;;
	*) echo "unexpected fake brew command: $*" >&2; exit 64 ;;
esac
`
	if err := os.WriteFile(filepath.Join(home, ".test-stub-bin", "brew"), []byte(brew), 0o755); err != nil {
		t.Fatalf("write fake brew: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".test-stub-bin", "dpkg-query"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write empty dpkg-query: %v", err)
	}
	for _, name := range []string{"bun", "npm", "pnpm"} {
		if err := os.WriteFile(filepath.Join(home, ".test-stub-bin", name), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
			t.Fatalf("write unavailable %s: %v", name, err)
		}
	}
	if err := config.Save(configPath, &config.RootConfig{
		Version: config.CurrentVersion,
		Settings: config.Settings{
			DisabledProviders: []string{"apt", "apk", "dnf", "node", "pacman", "pip", "python", "system", "zypper"},
		},
		Tools: map[string]config.ToolSpec{
			"omni-reconcile-tool": {Providers: []config.ToolInstallSpec{{Provider: "brew", Package: "omni-reconcile-tool"}}},
		},
		Hosts: map[string][]string{"testhost": {"dev"}},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "dev", Tools: []config.ToolEntry{{Name: "omni-reconcile-tool"}}},
		},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	screen := runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, func(text string) bool {
			return strings.Contains(text, "Dashboard") && strings.Contains(text, "Tools")
		}, "TUI did not render main tabs")

		openReconcilePlan := func(message string) string {
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				writeTUIKeys(t, term, "A")
				if current, ok := waitForScreen(term, 500*time.Millisecond, func(text string) bool {
					return strings.Contains(text, "Reconcile Plan") && strings.Contains(text, "Sync tools")
				}); ok {
					return current
				}
			}
			t.Fatalf("%s; screen:\n%s", message, currentScreenText(term))
			return ""
		}

		openReconcilePlan("TUI did not open reconcile plan for the missing tool")
		writeTUIKeys(t, term, "\r")
		failedScreen, ok := waitForScreen(term, 10*time.Second, func(text string) bool {
			_, installedErr := os.Stat(installedMarker)
			return os.IsNotExist(installedErr) && strings.Contains(strings.ToLower(text), "reconcile finished with issue")
		})
		if !ok {
			logData, _ := os.ReadFile(brewLog)
			t.Fatalf("TUI did not show the injected reconcile failure; fake brew log:\n%s\nscreen:\n%s", logData, failedScreen)
		}

		openReconcilePlan("TUI did not allow reconcile retry after failure")
		writeTUIKeys(t, term, "\r")
		recoveredScreen := waitForRequiredScreen(t, term, 10*time.Second, func(text string) bool {
			_, installedErr := os.Stat(installedMarker)
			return installedErr == nil && strings.Contains(strings.ToLower(text), "reconciled")
		}, "TUI did not recover on reconcile retry")
		return failedScreen + "\n" + recoveredScreen
	})
	if !strings.Contains(strings.ToLower(screen), "reconcile finished with issue") ||
		!strings.Contains(strings.ToLower(screen), "reconciled") {
		t.Fatalf("TUI did not expose both failure and recovery states; screen:\n%s", screen)
	}

	listOut := runOmniOutput(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "tools", "list")
	if !strings.Contains(listOut, "omni-reconcile-tool") || !strings.Contains(listOut, "1.2.3") {
		t.Fatalf("recovered install was not durable through the real CLI:\n%s", listOut)
	}
	logData, err := os.ReadFile(brewLog)
	if err != nil {
		t.Fatalf("read fake brew log: %v", err)
	}
	if attempts := strings.Count(string(logData), "install omni-reconcile-tool"); attempts != 2 {
		t.Fatalf("install attempts = %d, want 2; fake brew log:\n%s", attempts, logData)
	}
}
