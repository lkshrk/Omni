//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

func TestTUIConfiguredHostStartsDashboard(t *testing.T) {
	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	env := isolatedTUIEnv(t, home, cache)

	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "hosts", "ensure", "testhost")

	screen := runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, func(text string) bool {
			return strings.Contains(text, "Dashboard") && strings.Contains(text, "Tools")
		}, "TUI did not render main tabs")
		if err := term.Resize(120, 30); err != nil {
			t.Fatalf("resize TUI: %v", err)
		}
		waitForRequiredScreen(t, term, 3*time.Second, func(text string) bool {
			return strings.Contains(text, "Dashboard") && strings.Contains(text, "Settings")
		}, "TUI did not redraw after resize")

		writeTUIKeys(t, term, "?")
		waitForRequiredScreen(t, term, 3*time.Second, func(text string) bool {
			return strings.Contains(text, "Current Tab Actions") && strings.Contains(text, "Navigation")
		}, "TUI did not open contextual help")
		writeTUIKeys(t, term, "?")
		waitForRequiredScreen(t, term, 3*time.Second, func(text string) bool {
			return !strings.Contains(text, "Current Tab Actions")
		}, "TUI did not close contextual help")
		writeTUIKeys(t, term, "\t")
		waitForRequiredScreen(t, term, 3*time.Second, func(text string) bool {
			return strings.Contains(text, "[all]") && strings.Contains(text, "S sync all")
		}, "TUI did not navigate to tools")
		writeTUIKeys(t, term, "/", "zz")
		searchScreen := waitForRequiredScreen(t, term, 3*time.Second, func(text string) bool {
			return strings.Contains(text, "no search results for 'zz'")
		}, "TUI did not accept a tools search query")
		writeTUIKeys(t, term, "\x1b")
		waitForRequiredScreen(t, term, 3*time.Second, func(text string) bool {
			return !strings.Contains(text, "> zz")
		}, "TUI did not close tools search")
		return searchScreen
	})
	if !strings.Contains(screen, "Dashboard") || !strings.Contains(screen, "Tools") {
		t.Fatalf("TUI did not render main tabs; screen:\n%s", screen)
	}
	if strings.Contains(strings.ToLower(screen), "setup") {
		t.Fatalf("configured host opened setup instead of dashboard; screen:\n%s", screen)
	}
}

func TestCurrentScreenOmitsOverwrittenOutput(t *testing.T) {
	term, err := vttest.NewTerminal(t, 20, 2)
	if err != nil {
		t.Fatalf("create terminal: %v", err)
	}
	defer term.Close()
	cmd := exec.CommandContext(t.Context(), "sh", "-c", "printf 'obsolete\\rreplacement'")
	if err := term.Start(cmd); err != nil {
		t.Fatalf("start overwrite fixture: %v", err)
	}
	screen := waitForRequiredScreen(t, term, time.Second, func(text string) bool {
		return strings.Contains(text, "replacement")
	}, "current screen omitted replacement output")
	if err := term.Wait(cmd); err != nil {
		t.Fatalf("wait for overwrite fixture: %v", err)
	}

	if strings.Contains(screen, "obsolete") {
		t.Fatalf("current screen retained overwritten output: %q", screen)
	}
}

func TestTUIFallbackProviderListSmoke(t *testing.T) {
	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	env := isolatedTUIEnv(t, home, cache)

	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatalf("create cache: %v", err)
	}
	if err := config.Save(configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{DisabledProviders: []string{"node", "python", "pip"}},
		Tools: map[string]config.ToolSpec{
			"rg": {
				Providers: []config.ToolInstallSpec{{Provider: "apt", Package: "rg"}},
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
					Status: config.FallbackStatusUnverified,
					Binary: "rg",
					Commands: config.FallbackCommands{
						Install: "install rg",
						Check:   "command -v rg",
					},
				},
			},
			"jq": {
				Providers: []config.ToolInstallSpec{{Provider: "apt", Package: "jq"}},
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "jqlang", Repo: "jq"},
					Status: config.FallbackStatusVerified,
					Commands: config.FallbackCommands{
						Check: "command -v jq",
					},
				},
			},
		},
		Hosts: map[string][]string{"testhost": {"dev"}},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "dev", Tools: []config.ToolEntry{{Name: "rg"}, {Name: "jq"}}},
		},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	seedTUIToolCache(t, cache,
		&database.ToolCache{Name: "rg", Provider: "apt", Package: "rg", Installed: false, Tracked: true},
		&database.ToolCache{Name: "jq", Provider: "apt", Package: "jq", Installed: true, InstalledWith: "apt", Version: sql.NullString{String: "1.7", Valid: true}, Tracked: true},
	)
	listOut := runOmniOutput(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "tools", "list")
	if !strings.Contains(listOut, "rg") || !strings.Contains(listOut, "jq") {
		t.Fatalf("seeded tools are not visible through app list:\n%s", listOut)
	}

	screen := runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, func(text string) bool {
			return strings.Contains(text, "Dashboard") && strings.Contains(text, "Tools")
		}, "TUI did not render main tabs")
		writeTUIKeys(t, term, "\t")
		toolsScreen := waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "rg") &&
				strings.Contains(text, "gh?") &&
				strings.Contains(text, "jq") &&
				strings.Contains(text, "apt")
		}, "TUI did not render provider-list fallback/native tool states")
		writeTUIKeys(t, term, "f")
		editorScreen := waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "Set Fallback: rg") &&
				strings.Contains(text, "BurntSushi/ripgrep") &&
				strings.Contains(text, "install rg")
		}, "TUI did not open fallback editor for missing provider-list tool")
		return toolsScreen + "\n" + editorScreen
	})
	if strings.Contains(strings.ToLower(screen), "error") {
		t.Fatalf("TUI showed an error during fallback/provider-list smoke; screen:\n%s", screen)
	}
}

func TestTUIFallbackEditorPrefillsConfiguredGitHint(t *testing.T) {
	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	env := isolatedTUIEnv(t, home, cache)

	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatalf("create cache: %v", err)
	}
	if err := config.Save(configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{DisabledProviders: []string{"node", "python", "pip"}},
		Tools: map[string]config.ToolSpec{
			// synthetic name — must never resolve on PATH, or executableDetectSingleTool marks it installed and f is ineligible
			"omni-test-fbtool": {
				Providers: []config.ToolInstallSpec{{Provider: "apt", Package: "omni-test-fbtool"}},
				Git:       "https://github.com/BurntSushi/ripgrep",
			},
		},
		Hosts: map[string][]string{"testhost": {"dev"}},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "dev", Tools: []config.ToolEntry{{Name: "omni-test-fbtool"}}},
		},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	seedTUIToolCache(t, cache,
		&database.ToolCache{Name: "omni-test-fbtool", Provider: "apt", Package: "omni-test-fbtool", Installed: false, Tracked: true},
	)
	listOut := runOmniOutput(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "tools", "list")
	if !strings.Contains(listOut, "omni-test-fbtool") {
		t.Fatalf("seeded tool is not visible through app list:\n%s", listOut)
	}

	screen := runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, func(text string) bool {
			return strings.Contains(text, "Dashboard") && strings.Contains(text, "Tools")
		}, "TUI did not render main tabs")
		writeTUIKeys(t, term, "\t")
		toolsScreen := waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "omni-test-fbtool") &&
				strings.Contains(text, "apt") &&
				!strings.Contains(text, "gh?")
		}, "TUI did not render provider-list tool without fallback status")
		writeTUIKeys(t, term, "f")
		editorScreen := waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "Set Fallback: omni-test-fbtool") &&
				strings.Contains(text, "BurntSushi/ripgrep")
		}, "TUI did not prefill fallback editor from configured git hint")
		writeTUIKeys(t, term, "\r")
		// Status text is transient and width-truncated; assert the persisted
		// fallback instead, which is the behavior this flow owns.
		savedScreen := waitForRequiredScreen(t, term, 8*time.Second, func(_ string) bool {
			cfg, err := config.Load(configPath)
			if err != nil {
				return false
			}
			fallback := cfg.Tools["omni-test-fbtool"].Fallback
			return fallback != nil &&
				fallback.Source.Type == config.FallbackSourceGitHub &&
				fallback.Source.Owner == "BurntSushi" &&
				fallback.Source.Repo == "ripgrep"
		}, "TUI did not persist fallback from configured git hint")
		return toolsScreen + "\n" + editorScreen + "\n" + savedScreen
	})
	if strings.Contains(strings.ToLower(screen), "error") {
		t.Fatalf("TUI showed an error during configured-git fallback smoke; screen:\n%s", screen)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load config after fallback save: %v", err)
	}
	fallback := cfg.Tools["omni-test-fbtool"].Fallback
	if fallback == nil ||
		fallback.Source.Type != config.FallbackSourceGitHub ||
		fallback.Source.Owner != "BurntSushi" ||
		fallback.Source.Repo != "ripgrep" ||
		fallback.Status != config.FallbackStatusUnresolved {
		t.Fatalf("saved fallback = %+v, want unresolved GitHub fallback for BurntSushi/ripgrep", fallback)
	}
}

func TestTUIInstallsMissingToolWithFakeBrew(t *testing.T) {
	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	installedMarker := filepath.Join(root, "brew-installed")
	brewLog := filepath.Join(root, "brew.log")
	env := append(isolatedTUIEnv(t, home, cache), "OMNI_TEST_BREW_STATE="+installedMarker, "OMNI_TEST_BREW_LOG="+brewLog)

	brew := `#!/bin/sh
set -eu
state="${OMNI_TEST_BREW_STATE:?}"
log="${OMNI_TEST_BREW_LOG:?}"
printf '%s\n' "$*" >> "$log"
case "$*" in
	"--version") echo "Homebrew 4.0.0" ;;
	"install omni-test-tool") echo "omni-test-tool" > "$state" ;;
	"uninstall omni-test-tool"|"uninstall --formula omni-test-tool") rm -f "$state" ;;
	"list --versions omni-test-tool")
		[ -f "$state" ] || exit 1
		echo "omni-test-tool 1.2.3"
		;;
	"list --versions --cask omni-test-tool") exit 1 ;;
	"leaves --installed-on-request") [ ! -f "$state" ] || echo "omni-test-tool" ;;
	"list --cask") ;;
	"list --versions --formula") [ ! -f "$state" ] || echo "omni-test-tool 1.2.3" ;;
	"info --json=v2 --installed")
		if [ -f "$state" ]; then
			echo '{"formulae":[{"name":"omni-test-tool","full_name":"omni-test-tool","desc":"integration fixture","installed":[{"version":"1.2.3","installed_on_request":true}]}],"casks":[]}'
		else
			echo '{"formulae":[],"casks":[]}'
		fi
		;;
	info\ --json=v2*) echo '{"formulae":[{"name":"omni-test-tool","full_name":"omni-test-tool","desc":"integration fixture","installed":[]}],"casks":[]}' ;;
	"outdated --json=v2 --greedy") echo '{"formulae":[],"casks":[]}' ;;
	"update --quiet") ;;
	"search omni-test-tool") echo "omni-test-tool" ;;
	"tap") ;;
	*) echo "unexpected fake brew command: $*" >&2; exit 64 ;;
esac
`
	if err := os.WriteFile(filepath.Join(home, ".test-stub-bin", "brew"), []byte(brew), 0o755); err != nil {
		t.Fatalf("write fake brew: %v", err)
	}
	if err := config.Save(configPath, &config.RootConfig{
		Version: config.CurrentVersion,
		Settings: config.Settings{
			DisabledProviders: []string{"apt", "apk", "dnf", "node", "pacman", "pip", "python", "zypper"},
		},
		Tools: map[string]config.ToolSpec{
			"omni-test-tool": {Providers: []config.ToolInstallSpec{{Provider: "brew", Package: "omni-test-tool"}}},
		},
		Hosts: map[string][]string{"testhost": {"dev"}},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "dev", Tools: []config.ToolEntry{{Name: "omni-test-tool"}}},
		},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	screen := runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, func(text string) bool {
			return strings.Contains(text, "Dashboard") && strings.Contains(text, "Tools")
		}, "TUI did not render main tabs")
		writeTUIKeys(t, term, "\t")
		missingScreen := waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "omni-test-tool") && strings.Contains(text, "brew")
		}, "TUI did not render the missing fake-brew tool")
		writeTUIKeys(t, term, "i")
		installedScreen := waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
			_, err := os.Stat(installedMarker)
			return err == nil && strings.Contains(strings.ToLower(text), "installed omni-test-tool")
		}, "TUI did not durably install the fake-brew tool")

		writeTUIKeys(t, term, "d")
		confirmScreen := waitForRequiredScreen(t, term, 3*time.Second, func(text string) bool {
			return strings.Contains(text, "confirm delete")
		}, "TUI did not arm tool deletion")
		writeTUIKeys(t, term, "j")
		waitForRequiredScreen(t, term, 3*time.Second, func(text string) bool {
			cfg, err := config.Load(configPath)
			_, markerErr := os.Stat(installedMarker)
			return err == nil && markerErr == nil && !strings.Contains(text, "confirm delete") && cfg.Tools["omni-test-tool"].Providers != nil
		}, "non-confirming key did not cancel deletion")

		writeTUIKeys(t, term, "d")
		waitForRequiredScreen(t, term, 3*time.Second, func(text string) bool {
			return strings.Contains(text, "confirm delete")
		}, "TUI did not re-arm tool deletion")
		writeTUIKeys(t, term, "d")
		deletedScreen := waitForRequiredScreen(t, term, 8*time.Second, func(_ string) bool {
			cfg, err := config.Load(configPath)
			_, configured := cfg.Tools["omni-test-tool"]
			_, markerErr := os.Stat(installedMarker)
			return err == nil && !configured && os.IsNotExist(markerErr)
		}, "TUI did not uninstall and remove the tool from config")
		return missingScreen + "\n" + installedScreen + "\n" + confirmScreen + "\n" + deletedScreen
	})
	if strings.Contains(strings.ToLower(screen), "error") {
		t.Fatalf("TUI showed an error during fake-brew lifecycle; screen:\n%s", screen)
	}
	logData, err := os.ReadFile(brewLog)
	if err != nil {
		t.Fatalf("read fake brew log: %v", err)
	}
	logText := string(logData)
	if !strings.Contains(logText, "install omni-test-tool") || !strings.Contains(logText, "uninstall omni-test-tool") {
		t.Fatalf("fake brew lifecycle log:\n%s", logText)
	}
}

func TestTUIIncludesStaticIgnoredDotCandidate(t *testing.T) {
	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	env := isolatedTUIEnv(t, home, cache)
	repo := filepath.Join(home, "dotfiles")

	if err := os.MkdirAll(filepath.Join(home, ".config", "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	initDotsRepo(t, repo, env)
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "hosts", "ensure", "testhost")
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "settings", "set", "dots_repo", "~/dotfiles")

	screen := runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, func(text string) bool {
			return strings.Contains(text, "Dashboard") && strings.Contains(text, "Tools")
		}, "TUI did not render main tabs")
		writeTUIKeys(t, term, "\t", "\t")
		screen := waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "node_modules") &&
				strings.Contains(text, "Ignored") &&
				strings.Contains(text, "dots synced")
		}, "TUI did not render ignored node_modules candidate")
		var confirmScreen string
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			writeTUIKeys(t, term, "x")
			if current, ok := waitForScreen(term, 500*time.Millisecond, func(text string) bool {
				return strings.Contains(text, "confirm include")
			}); ok {
				confirmScreen = current
				break
			}
		}
		if confirmScreen == "" {
			t.Fatalf("TUI did not arm ignored candidate include; screen:\n%s", screen)
		}
		writeTUIKeys(t, term, "x")
		if !waitForConfigDot(configPath, "testhost", "node_modules", false, 8*time.Second) {
			t.Fatalf("node_modules was not included after confirmation; screen:\n%s", confirmScreen)
		}
		return currentScreenText(term)
	})
	if strings.Contains(strings.ToLower(screen), "error") {
		t.Fatalf("TUI showed an error while including ignored candidate; screen:\n%s", screen)
	}
}

func TestTUISyncsDiscoveredDotCandidate(t *testing.T) {
	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	env := isolatedTUIEnv(t, home, cache)
	repo := filepath.Join(home, "dotfiles")

	candidateDir := filepath.Join(home, ".config", "ghost")
	if err := os.MkdirAll(candidateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidateDir, "config.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	initDotsRepo(t, repo, env)
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "hosts", "ensure", "testhost")
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "settings", "set", "dots_repo", "~/dotfiles")

	screen := runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, func(text string) bool {
			return strings.Contains(text, "Dashboard") && strings.Contains(text, "Tools")
		}, "TUI did not render main tabs")
		writeTUIKeys(t, term, "\t", "\t")
		screen := waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "ghost") && strings.Contains(text, "~/.config/ghost")
		}, "TUI did not render discovered ghost candidate")
		writeTUIKeys(t, term, "s")
		if !waitForConfigDot(configPath, "testhost", "ghost", false, 8*time.Second) {
			t.Fatalf("ghost was not persisted after sync; screen:\n%s", screen)
		}
		return currentScreenText(term)
	})
	if strings.Contains(strings.ToLower(screen), "error") {
		t.Fatalf("TUI showed an error while syncing discovered candidate; screen:\n%s", screen)
	}
}

func TestTUIDotsRootFileIgnoreCompletesPromptly(t *testing.T) {
	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	repo := filepath.Join(home, "dotfiles")
	env := isolatedTUIEnv(t, home, cache)

	source := filepath.Join(repo, "dotfiles", "claude", ".claude")
	writeIntegrationFile(t, filepath.Join(source, "settings.json"), "{}\n")
	for i := range 5_000 {
		writeIntegrationFile(t, filepath.Join(home, ".claude", "cache", fmt.Sprintf("entry-%05d", i), "data.json"), "x")
	}
	if err := os.Symlink(filepath.Join(source, "settings.json"), filepath.Join(home, ".claude", "settings.json")); err != nil {
		t.Fatalf("link settings.json: %v", err)
	}
	initDotsRepo(t, repo, env)
	runCommand(t, repo, env, "git", "add", ".")
	runCommand(t, repo, env, "git", "commit", "-m", "fixture")
	if err := config.Save(configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{DotsRepo: repo},
		Hosts:    map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{{
			Name:    "testhost",
			Special: "host",
			Dots: []config.DotEntry{{
				Name: "claude",
				Path: filepath.Join(home, ".claude"),
			}},
		}},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	const maxIgnoreLatency = 2 * time.Second
	runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, func(text string) bool {
			return strings.Contains(text, "Dashboard") && strings.Contains(text, "Tools")
		}, "TUI did not render main tabs")
		writeTUIKeys(t, term, "\t", "\t")
		waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "Dots") && strings.Contains(text, "claude")
		}, "TUI did not load the claude dots entry")
		writeTUIKeys(t, term, " ")
		waitForRequiredScreen(t, term, 3*time.Second, func(text string) bool {
			return strings.Contains(text, "settings.json")
		}, "TUI did not expand the claude dots entry")
		writeTUIKeys(t, term, "j", "j", "x")
		waitForRequiredScreen(t, term, 2*time.Second, func(text string) bool {
			return strings.Contains(text, "confirm ignore")
		}, "TUI did not arm settings.json ignore")

		started := time.Now()
		writeTUIKeys(t, term, "x")
		screen := waitForRequiredScreen(t, term, 5*time.Second, func(text string) bool {
			return strings.Contains(text, "settings.json ignored for claude")
		}, "TUI did not finish settings.json ignore")
		elapsed := time.Since(started)
		t.Logf("actual TUI root-file ignore with 5,000 unrelated files: %s", elapsed)
		if elapsed > maxIgnoreLatency {
			t.Fatalf("actual TUI root-file ignore took %s, want <= %s; screen:\n%s", elapsed, maxIgnoreLatency, screen)
		}
		return screen
	})
}

func TestTUIDashboardReconcileFixesDotIgnorePatterns(t *testing.T) {
	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	env := isolatedTUIEnv(t, home, cache)
	cfg := &config.RootConfig{
		Settings: config.Settings{
			DisabledProviders: []string{"system", "node", "python", "pip"},
		},
		Hosts: map[string][]string{"testhost": {"dev"}},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{
				Name: "dev",
				Dots: []config.DotEntry{
					// Not an agent config dir: those are dropped from dots by the
					// v13→v14 migration on load, which would empty this scenario.
					{Name: "editor", Path: "~/.editor", Ignore: []string{"*", "!/settings.json", "!/skills/", "skills", "!/hooks/", "hooks"}},
				},
			},
		},
	}
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	screen := runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, func(text string) bool {
			return strings.Contains(text, "Dashboard") && strings.Contains(text, "Tools")
		}, "TUI did not render main tabs")

		var planScreen string
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			writeTUIKeys(t, term, "A")
			if screen, ok := waitForScreen(term, 500*time.Millisecond, func(text string) bool {
				return strings.Contains(text, "Reconcile Plan") && strings.Contains(text, "Fix ignore patterns")
			}); ok {
				planScreen = screen
				break
			}
		}
		if planScreen == "" {
			t.Fatalf("TUI did not open reconcile plan with fix-ignore step; screen:\n%s", currentScreenText(term))
		}

		writeTUIKeys(t, term, "\r")
		if !waitForConfigDotIgnore(configPath, "dev", "editor", []string{"*", "!/settings.json"}, 8*time.Second) {
			t.Fatalf("editor ignore patterns were not fixed from dashboard reconcile; screen:\n%s", planScreen)
		}
		return currentScreenText(term)
	})
	if strings.Contains(strings.ToLower(screen), "error") {
		t.Fatalf("TUI showed an error while fixing dot ignore patterns; screen:\n%s", screen)
	}
}

var (
	omniBinaryOnce   sync.Once
	omniBinaryPath   string
	omniBinaryOutput []byte
	omniBinaryErr    error
)

func buildOmniBinary(t *testing.T) string {
	t.Helper()
	omniBinaryOnce.Do(func() {
		dir, err := os.MkdirTemp("", "omni-integration-")
		if err != nil {
			omniBinaryErr = err
			return
		}
		omniBinaryPath = filepath.Join(dir, "omni")
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "go", "build", "-o", omniBinaryPath, "./cmd/omni")
		cmd.Dir = integrationRepoRoot(t)
		omniBinaryOutput, omniBinaryErr = cmd.CombinedOutput()
	})
	if omniBinaryErr != nil {
		t.Fatalf("build omni: %v\n%s", omniBinaryErr, omniBinaryOutput)
	}
	return omniBinaryPath
}

func integrationRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolve working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("resolve repository root from working directory")
		}
		dir = parent
	}
}

// minimalTestPATH deliberately excludes any developer-machine bin directory
// that might hold real agent CLIs (claude, grok, codex, ...). The TUI's
// agents feature shells out to those binaries by name (e.g. "claude plugins
// list --json --available", "grok plugin list --json --available") to
// enumerate plugins/mcp servers; if a real install is reachable on PATH it
// runs for real and bootstraps its own state (e.g. ~/.claude, ~/.claude.json,
// ~/.grok/...) inside the test's otherwise-isolated HOME. Those freshly
// created directories then surface as unexpected dots candidates, shifting
// row order/cursor position and breaking assertions that assume a specific
// candidate is at the cursor.
// Tool dirs stay (dots needs stow, typically brew-installed), but agent CLIs
// must not run for real: agentCLIStubs shadows them via a dir prepended to
// PATH, so a brew- or ~/.local/bin-installed claude/grok can never win.
var minimalTestPATH = strings.Join([]string{
	"/usr/bin", "/bin", "/usr/sbin", "/sbin",
	"/opt/homebrew/bin", "/opt/homebrew/sbin", "/usr/local/bin",
}, string(os.PathListSeparator))

// agentCLIStubs are the binaries the agents feature execs by name (plugin/mcp
// adapters and catalog detection). Each stub exits 0 with no output and no
// side effects — the real CLIs bootstrap first-run state (~/.claude.json,
// ~/.grok/...) into the test HOME, which then surfaces as unexpected dots
// candidates and shifts cursor-position-sensitive assertions.
var agentCLIStubs = []string{"claude", "codex", "grok", "cursor", "gemini", "opencode", "cline"}

// stubAgentCLIDir creates a directory of no-op agent CLI stubs to prepend to
// the isolated PATH.
func stubAgentCLIDir(t *testing.T, home string) string {
	t.Helper()
	dir := filepath.Join(home, ".test-stub-bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range agentCLIStubs {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func isolatedTUIEnv(t *testing.T, home, cache string) []string {
	stubDir := stubAgentCLIDir(t, home)
	return append(os.Environ(),
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
		"XDG_DATA_HOME="+filepath.Join(home, ".local", "share"),
		"OMNI_CACHE_DIR="+cache,
		"OMNI_HOSTNAME=testhost",
		"OMNI_TEST_ISOLATED=1",
		"TERM=xterm-256color",
		"PATH="+stubDir+string(os.PathListSeparator)+minimalTestPATH,
	)
}

func initDotsRepo(t *testing.T, repo string, env []string) {
	t.Helper()
	runCommand(t, filepath.Dir(repo), env, "git", "init", repo)
	runCommand(t, repo, env, "git", "config", "user.email", "t@t.com")
	runCommand(t, repo, env, "git", "config", "user.name", "T")
	runCommand(t, repo, env, "git", "config", "commit.gpgsign", "false")
	runCommand(t, repo, env, "git", "config", "tag.gpgsign", "false")
	runCommand(t, repo, env, "git", "config", "core.hooksPath", "/dev/null")
	runCommand(t, repo, env, "git", "commit", "--allow-empty", "-m", "init")
}

func runCommand(t *testing.T, dir string, env []string, name string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

func runOmniCommand(t *testing.T, bin, dir string, env []string, args ...string) {
	t.Helper()
	_ = runOmniOutput(t, bin, dir, env, args...)
}

func runOmniOutput(t *testing.T, bin, dir string, env []string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("omni %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func seedTUIToolCache(t *testing.T, cache string, tools ...*database.ToolCache) {
	t.Helper()
	db, err := database.Open(filepath.Join(cache, "omni.db"))
	if err != nil {
		t.Fatalf("open TUI cache db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate TUI cache db: %v", err)
	}
	now := time.Now().UTC()
	for _, tool := range tools {
		if tool.Package == "" {
			tool.Package = tool.Name
		}
		if tool.LastChecked.IsZero() {
			tool.LastChecked = now
		}
		if err := db.Upsert(ctx, tool); err != nil {
			t.Fatalf("seed TUI tool cache %s/%s: %v", tool.Provider, tool.Name, err)
		}
	}
}

func runTUISmoke(t *testing.T, bin, dir string, env []string, args ...string) string {
	return runTUI(t, bin, dir, env, args, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, func(text string) bool {
			return strings.Contains(text, "Dashboard") && strings.Contains(text, "Tools")
		}, "TUI did not render expected startup screen")
		return currentScreenText(term)
	})
}

func runTUI(t *testing.T, bin, dir string, env []string, args []string, interact func(*vttest.Terminal) string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	term, err := vttest.NewTerminal(t, 100, 24)
	if err != nil {
		t.Fatalf("create TUI terminal: %v", err)
	}
	started := false
	waited := false
	defer func() {
		if started && !waited {
			_ = cmd.Process.Kill()
			_ = term.Wait(cmd)
		}
		// x/vttest.Close races its emulator reader (upstream x/vt Close is not
		// synchronized). The package process owns this bounded set of PTYs, so
		// let process exit reclaim them instead of making the race lane unsound.
	}()
	if err := term.Start(cmd); err != nil {
		t.Fatalf("start TUI: %v", err)
	}
	started = true

	screen := interact(term)
	writeTUIKeys(t, term, "q", "q")

	waitDone := make(chan error, 1)
	go func() { waitDone <- term.Wait(cmd) }()
	select {
	case err := <-waitDone:
		waited = true
		if err != nil {
			t.Fatalf("TUI exit: %v\n%s", err, screen)
		}
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("TUI did not quit after confirmation keys; screen:\n%s", currentScreenText(term))
	}

	return screen
}

func writeTUIKeys(t *testing.T, term *vttest.Terminal, keys ...string) {
	t.Helper()
	// vttest.Terminal.SendText takes the terminal mutex before the emulator
	// mutex, while output callbacks take them in the opposite order. Sending
	// through the concurrency-safe emulator avoids that upstream lock inversion.
	term.Emulator.SendText(strings.Join(keys, ""))
}

func waitForRequiredScreen(t *testing.T, term *vttest.Terminal, timeout time.Duration, ready func(string) bool, message string) string {
	t.Helper()
	screen, ok := waitForScreen(term, timeout, ready)
	if !ok {
		t.Fatalf("%s; screen:\n%s", message, screen)
	}
	return screen
}

func waitForScreen(term *vttest.Terminal, timeout time.Duration, ready func(string) bool) (string, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		text := currentScreenText(term)
		if ready(text) {
			return text, true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return currentScreenText(term), false
}

func waitForConfigDot(path, groupName, dotName string, ignored bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cfg, err := config.Load(path)
		if err == nil {
			for _, group := range cfg.Groups {
				if group == nil || group.Name != groupName {
					continue
				}
				for _, dot := range group.Dots {
					if dot.Name == dotName && dot.Ignored == ignored {
						return true
					}
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func waitForConfigDotIgnore(path, groupName, dotName string, ignore []string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cfg, err := config.Load(path)
		if err == nil {
			for _, group := range cfg.Groups {
				if group == nil || group.Name != groupName {
					continue
				}
				for _, dot := range group.Dots {
					if dot.Name == dotName && slices.Equal(dot.Ignore, ignore) {
						return true
					}
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func currentScreenText(term *vttest.Terminal) string {
	return strings.TrimSpace(ansi.Strip(term.Emulator.Render()))
}
