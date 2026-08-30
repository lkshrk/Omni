//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

func TestTUIToolsUpgradeAllInvokesEveryOutdatedTool(t *testing.T) {
	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	commandLog := filepath.Join(root, "brew.log")
	providerState := filepath.Join(root, "brew-state")
	if err := os.MkdirAll(providerState, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"omni-old-one", "omni-old-two"} {
		writeIntegrationFile(t, filepath.Join(providerState, name), "1.0.0\n")
	}
	env := append(isolatedTUIEnv(t, home, cache), "OMNI_TEST_BREW_LOG="+commandLog, "OMNI_TEST_BREW_STATE="+providerState)
	writeFakeBulkUpgradeBrew(t, filepath.Join(home, ".test-stub-bin", "brew"))

	tools := map[string]config.ToolSpec{}
	entries := make([]config.ToolEntry, 0, 2)
	for _, name := range []string{"omni-old-one", "omni-old-two"} {
		tools[name] = config.ToolSpec{Providers: []config.ToolInstallSpec{{Provider: "brew", Package: name}}}
		entries = append(entries, config.ToolEntry{Name: name})
		seedTUIToolCache(t, cache, &database.ToolCache{
			Name:          name,
			Provider:      "brew",
			Package:       name,
			Installed:     true,
			InstalledWith: "brew",
			Version:       sql.NullString{String: "1.0.0", Valid: true},
			LatestVersion: sql.NullString{String: "2.0.0", Valid: true},
			Outdated:      true,
			Tracked:       true,
		})
	}
	if err := config.Save(configPath, &config.RootConfig{
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
		t.Fatalf("save config: %v", err)
	}

	runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, screenHas("Dashboard", "Tools"), "TUI did not render main tabs")
		writeTUIKeys(t, term, "\t")
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("omni-old-one", "omni-old-two"), "TUI did not render both outdated tools")
		writeTUIKeys(t, term, "U")
		return waitForRequiredScreen(t, term, 12*time.Second, func(text string) bool {
			log, _ := os.ReadFile(commandLog)
			return strings.Contains(string(log), "upgrade --formula omni-old-one") &&
				strings.Contains(string(log), "upgrade --formula omni-old-two") &&
				bulkUpgradeCacheSettled(cache) && !strings.Contains(strings.ToLower(text), "upgrading")
		}, "TUI did not upgrade every outdated tool")
	})
	for _, name := range []string{"omni-old-one", "omni-old-two"} {
		version, err := os.ReadFile(filepath.Join(providerState, name))
		if err != nil || strings.TrimSpace(string(version)) != "2.0.0" {
			t.Fatalf("provider state for %s = %q, %v", name, version, err)
		}
	}
}

func bulkUpgradeCacheSettled(cache string) bool {
	db, err := database.Open(filepath.Join(cache, "omni.db"))
	if err != nil {
		return false
	}
	defer db.Close()
	for _, name := range []string{"omni-old-one", "omni-old-two"} {
		tool, err := db.Get(context.Background(), name, "brew", name)
		if err != nil || !tool.Installed || tool.Version.String != "2.0.0" || tool.Outdated {
			return false
		}
	}
	return true
}

func TestTUIDotsSyncRepairsBrokenManagedLink(t *testing.T) {
	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	repo := filepath.Join(home, "dotfiles")
	target := filepath.Join(home, ".config", "nvim", "init.lua")
	source := filepath.Join(repo, "dotfiles", "nvim", ".config", "nvim", "init.lua")
	env := isolatedTUIEnv(t, home, cache)

	initDotsRepo(t, repo, env)
	writeIntegrationFile(t, source, "repo version\n")
	runCommand(t, repo, env, "git", "add", ".")
	runCommand(t, repo, env, "git", "commit", "-m", "add nvim dotfile")
	if err := config.Save(configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{DotsRepo: repo},
		Hosts:    map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{{
			Name:    "testhost",
			Special: "host",
			Dots:    []config.DotEntry{{Name: "nvim", Path: target}},
		}},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "hosts", "ensure", "testhost")

	runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, screenHas("Dashboard", "Tools"), "TUI did not render main tabs")
		writeTUIKeys(t, term, "\t", "\t")
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("nvim", "synced"), "TUI did not render the managed dotfile")
		if err := os.Remove(target); err != nil {
			t.Fatalf("break managed link: %v", err)
		}
		if err := os.Symlink(filepath.Join(root, "missing"), target); err != nil {
			t.Fatalf("replace managed link: %v", err)
		}
		writeTUIKeys(t, term, "R")
		waitForRequiredScreen(t, term, 5*time.Second, func(text string) bool {
			return strings.Contains(text, "nvim") && strings.Contains(text, "broken")
		}, "TUI refresh did not detect the broken managed link")
		writeTUIKeys(t, term, "j")
		waitForRequiredScreen(t, term, 3*time.Second, func(text string) bool {
			return strings.Contains(text, "> ✗ • nvim") && strings.Contains(text, "s repair")
		}, "TUI did not select the broken managed link")
		writeTUIKeys(t, term, "s")
		return waitForRequiredScreen(t, term, 8*time.Second, func(_ string) bool {
			info, err := os.Lstat(target)
			if err != nil || info.Mode()&os.ModeSymlink == 0 {
				return false
			}
			resolved, err := filepath.EvalSymlinks(target)
			return err == nil && resolved == source
		}, "TUI dot sync did not repair the managed link")
	})

	assertSymlinkContent(t, target, "repo version\n")
}

func writeFakeBulkUpgradeBrew(t *testing.T, path string) {
	t.Helper()
	script := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "${OMNI_TEST_BREW_LOG:?}"
state="${OMNI_TEST_BREW_STATE:?}"
version() { cat "$state/$1"; }
case "$*" in
	"--version") echo "Homebrew 4.0.0" ;;
	"leaves --installed-on-request") printf 'omni-old-one\nomni-old-two\n' ;;
	"list --versions --formula") printf 'omni-old-one %s\nomni-old-two %s\n' "$(version omni-old-one)" "$(version omni-old-two)" ;;
	"list --cask") ;;
	"info --json=v2 --installed") printf '{"formulae":[{"name":"omni-old-one","full_name":"omni-old-one","installed":[{"version":"%s","installed_on_request":true}]},{"name":"omni-old-two","full_name":"omni-old-two","installed":[{"version":"%s","installed_on_request":true}]}],"casks":[]}\n' "$(version omni-old-one)" "$(version omni-old-two)" ;;
	"outdated --json=v2 --greedy")
		printf '{"formulae":['
		sep=""
		for name in omni-old-one omni-old-two; do
			if [ "$(version "$name")" != "2.0.0" ]; then
				printf '%s{"name":"%s","installed_versions":["1.0.0"],"current_version":"2.0.0","pinned":false}' "$sep" "$name"
				sep=,
			fi
		done
		printf '],"casks":[]}\n'
		;;
	"update --quiet") ;;
	"upgrade --formula omni-old-one") printf '2.0.0\n' > "$state/omni-old-one" ;;
	"upgrade --formula omni-old-two") printf '2.0.0\n' > "$state/omni-old-two" ;;
	info\ --json=v2*) echo '{"formulae":[],"casks":[]}' ;;
	list\ --versions\ omni-old-*) echo "$3 $(version "$3")" ;;
	"list --versions --cask omni-old-one"|"list --versions --cask omni-old-two") exit 1 ;;
	"tap") ;;
	*) echo "unexpected fake brew command: $*" >&2; exit 64 ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake brew: %v", err)
	}
}
