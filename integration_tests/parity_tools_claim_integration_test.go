//go:build integration

package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

func TestCLIAndTUIToolsClaimProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	cli, tui := newParityTwins(t)
	seedToolsClaimBoundary(t, cli)
	seedToolsClaimBoundary(t, tui)
	runOmniCommand(t, bin, cli.root, cli.env, "--config", cli.configPath, "--cache-dir", cli.cache, "tools", "add", "omni-test-tool", "--provider", "brew", "--group", "testhost")
	runToolsClaimTUI(t, bin, tui)
	if got, want := observeToolsClaim(t, tui), observeToolsClaim(t, cli); !reflect.DeepEqual(got, want) {
		t.Fatalf("claim semantic state differs\nCLI: %#v\nTUI: %#v", want, got)
	}
}

func seedToolsClaimBoundary(t *testing.T, s *paritySandbox) {
	state := filepath.Join(s.root, "brew-state")
	logPath := filepath.Join(s.root, "brew.log")
	writeIntegrationFile(t, state, "1.2.3\n")
	writeExecutable(t, filepath.Join(s.home, ".test-stub-bin", "omni-test-tool"), "#!/bin/sh\necho 1.2.3\n")
	writeExecutable(t, filepath.Join(s.home, ".test-stub-bin", "brew"), `#!/bin/sh
set -eu
state="${OMNI_TEST_BREW_STATE:?}"
printf '%s\n' "$*" >> "${OMNI_TEST_BREW_LOG:?}"
case "$*" in
  "--version") echo 'Homebrew 6.0.0' ;;
  "leaves --installed-on-request") echo omni-test-tool ;;
  "list --cask") ;;
  "list --versions --formula") echo 'omni-test-tool 1.2.3' ;;
  "list --versions omni-test-tool") echo 'omni-test-tool 1.2.3' ;;
  "list --versions --cask omni-test-tool") exit 1 ;;
  "info --json=v2 --installed") echo '{"formulae":[{"name":"omni-test-tool","full_name":"omni-test-tool","installed":[{"version":"1.2.3","installed_on_request":true}]}],"casks":[]}' ;;
  "outdated --json=v2 --greedy") echo '{"formulae":[],"casks":[]}' ;;
  "update --quiet"|"tap") ;;
  info\ --json=v2*) echo '{"formulae":[{"name":"omni-test-tool","full_name":"omni-test-tool","installed":[]}],"casks":[]}' ;;
  *) echo "unexpected brew command: $*" >&2; exit 64 ;;
esac
`)
	s.env = append(s.env, "OMNI_TEST_BREW_STATE="+state, "OMNI_TEST_BREW_LOG="+logPath)
	if err := config.Save(s.configPath, &config.RootConfig{Version: config.CurrentVersion, Settings: config.Settings{DisabledProviders: []string{"apt", "apk", "dnf", "node", "pacman", "pip", "python", "zypper"}}, Tools: map[string]config.ToolSpec{"brew-scope-anchor": {Providers: []config.ToolInstallSpec{{Provider: "brew"}}}}, Hosts: map[string][]string{"testhost": {}}, Groups: []*config.GroupConfig{{Name: "testhost", Special: "host", Tools: []config.ToolEntry{{Name: "brew-scope-anchor"}}}}}); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(filepath.Join(s.cache, "omni.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertDiscoveredBatch(context.Background(), []database.DiscoveredUpsert{{Name: "omni-test-tool", Provider: "brew", InstalledWith: "brew", Version: "1.2.3"}}); err != nil {
		t.Fatal(err)
	}
}

func runToolsClaimTUI(t *testing.T, bin string, s *paritySandbox) {
	runTUI(t, bin, s.root, s.env, []string{"--config", s.configPath, "--cache-dir", s.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 7*time.Second, screenHas("Dashboard", "Tools"), "TUI did not start")
		writeTUIKeys(t, term, "\t")
		screen, ok := waitForScreen(term, 12*time.Second, screenHas("omni-test-tool", "1.2.3"))
		if !ok {
			log, _ := os.ReadFile(filepath.Join(s.root, "brew.log"))
			t.Fatalf("TUI did not expose discovered row; brew log:\n%s\nscreen:\n%s", log, screen)
		}
		writeTUIKeys(t, term, "j")
		waitForRequiredScreen(t, term, 3*time.Second, func(text string) bool { return strings.Contains(text, ">") }, "TUI did not reveal tool cursor")
		writeTUIKeys(t, term, "j")
		waitForRequiredScreen(t, term, 3*time.Second, func(text string) bool { return strings.Contains(text, "> + omni-test-tool") }, "TUI did not select discovered row")
		writeTUIKeys(t, term, "c")
		waitForRequiredScreen(t, term, 4*time.Second, screenHas("testhost", "enter confirm"), "TUI did not open claim picker")
		writeTUIKeys(t, term, "\r")
		return waitForRequiredScreen(t, term, 10*time.Second, func(string) bool { return claimBoundarySettled(s) }, "TUI did not claim tool")
	})
}

type claimState struct {
	Config                           any
	Marker                           string
	Installed, Tracked               bool
	Provider, InstalledWith, Version string
}

func observeToolsClaim(t *testing.T, s *paritySandbox) claimState {
	raw, err := os.ReadFile(filepath.Join(s.root, "brew-state"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(filepath.Join(s.cache, "omni.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tool, err := db.Get(context.Background(), "omni-test-tool", "brew", "omni-test-tool")
	if err != nil {
		t.Fatal(err)
	}
	return claimState{Config: normalizedParityConfig(t, s), Marker: string(raw), Installed: tool.Installed, Tracked: tool.Tracked, Provider: tool.Provider, InstalledWith: tool.InstalledWith, Version: tool.Version.String}
}

func claimBoundarySettled(s *paritySandbox) bool {
	cfg, err := config.Load(s.configPath)
	if err != nil || !claimConfigHasTool(cfg, "omni-test-tool", "testhost") {
		return false
	}
	db, err := database.Open(filepath.Join(s.cache, "omni.db"))
	if err != nil {
		return false
	}
	defer db.Close()
	tool, err := db.Get(context.Background(), "omni-test-tool", "brew", "omni-test-tool")
	return err == nil && tool.Tracked && tool.Installed && tool.InstalledWith == "brew" && tool.Version.String == "1.2.3"
}

func claimConfigHasTool(cfg *config.RootConfig, name, groupName string) bool {
	if _, ok := cfg.Tools[name]; !ok {
		return false
	}
	for _, group := range cfg.Groups {
		if group != nil && group.Name == groupName {
			for _, tool := range group.Tools {
				if tool.Name == name {
					return true
				}
			}
		}
	}
	return false
}
