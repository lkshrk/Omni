//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

func TestCLIAndTUIToolsConsolidateProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	cli, tui := newParityTwins(t)
	seedToolsConsolidateParity(t, cli)
	seedToolsConsolidateParity(t, tui)

	runOmniCommand(t, bin, cli.root, cli.env,
		"--config", cli.configPath, "--cache-dir", cli.cache,
		"tools", "consolidate", "node", "pnpm")
	runPaletteTUI(t, bin, tui, "tools consolidate node pnpm", func() bool {
		return toolsConsolidateSettled(tui)
	})

	readToolsConsolidateThroughCLI(t, bin, cli)
	readToolsConsolidateThroughCLI(t, bin, tui)
	want, got := observeToolsConsolidateParity(t, cli), observeToolsConsolidateParity(t, tui)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tools.consolidate semantic state differs\nCLI: %#v\nTUI: %#v", want, got)
	}
	assertToolsConsolidateOutcome(t, cli, want)
	assertToolsConsolidateOutcome(t, tui, got)
}

func seedToolsConsolidateParity(t *testing.T, s *paritySandbox) {
	t.Helper()
	binDir := filepath.Join(s.home, ".test-stub-bin")
	npmState := filepath.Join(s.root, "npm-installed")
	pnpmState := filepath.Join(s.root, "pnpm-installed")
	logPath := filepath.Join(s.root, "node-managers.log")
	if err := os.WriteFile(npmState, []byte("fixture-cli@1.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s.env = append(s.env,
		"OMNI_TEST_NPM_STATE="+npmState,
		"OMNI_TEST_PNPM_STATE="+pnpmState,
		"OMNI_TEST_NODE_LOG="+logPath,
	)
	writeExecutable(t, filepath.Join(binDir, "npm"), `#!/bin/sh
set -eu
state="${OMNI_TEST_NPM_STATE:?}"
printf 'npm %s\n' "$*" >> "${OMNI_TEST_NODE_LOG:?}"
case "$*" in
  "--version") echo "10.0.0" ;;
  "list -g --depth=0"|"list -g --depth=0 fixture-cli")
    [ -f "$state" ] || exit 1
    echo '└── fixture-cli@1.0.0'
    ;;
  "uninstall -g fixture-cli") rm -f "$state" ;;
  "outdated -g --json") echo '{}' ;;
  *) echo "unexpected fake npm command: $*" >&2; exit 64 ;;
esac
`)
	writeExecutable(t, filepath.Join(binDir, "pnpm"), `#!/bin/sh
set -eu
state="${OMNI_TEST_PNPM_STATE:?}"
printf 'pnpm %s\n' "$*" >> "${OMNI_TEST_NODE_LOG:?}"
case "$*" in
  "--version") echo "9.0.0" ;;
  "add -g fixture-cli") printf 'fixture-cli@2.0.0\n' > "$state" ;;
  "ls -g --depth=0"|"ls -g --depth=0 fixture-cli")
    [ -f "$state" ] || exit 1
    echo '└── fixture-cli@2.0.0'
    ;;
  "outdated -g --json") echo '{}' ;;
  *) echo "unexpected fake pnpm command: $*" >&2; exit 64 ;;
esac
`)
	writeExecutable(t, filepath.Join(binDir, "bun"), "#!/bin/sh\nexit 127\n")

	if err := config.Save(s.configPath, &config.RootConfig{
		Version: config.CurrentVersion,
		Settings: config.Settings{DisabledProviders: []string{
			"apt", "apk", "brew", "cargo", "dnf", "gh", "go", "pacman", "pip", "python", "system", "uv", "zypper",
		}},
		HostSettings: map[string]config.Settings{
			"testhost": {ProviderPriority: []string{"npm"}},
		},
		Tools: map[string]config.ToolSpec{
			"fixture": {Providers: []config.ToolInstallSpec{
				{Provider: "npm", Package: "fixture-cli"},
				{Provider: "pnpm", Package: "fixture-cli"},
			}},
		},
		Hosts: map[string][]string{"testhost": {"dev"}},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "dev", Tools: []config.ToolEntry{{Name: "fixture"}}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	seedTUIToolCache(t, s.cache, &database.ToolCache{
		Name: "fixture", Provider: "node", Package: "fixture-cli", Installed: true, InstalledWith: "npm", Tracked: true,
	})
}

func toolsConsolidateSettled(s *paritySandbox) bool {
	if _, err := os.Stat(filepath.Join(s.root, "npm-installed")); !os.IsNotExist(err) {
		return false
	}
	if _, err := os.Stat(filepath.Join(s.root, "pnpm-installed")); err != nil {
		return false
	}
	cfg, err := config.Load(s.configPath)
	if err != nil || len(cfg.HostSettings["testhost"].ProviderPriority) == 0 || cfg.HostSettings["testhost"].ProviderPriority[0] != "pnpm" {
		return false
	}
	db, err := database.Open(filepath.Join(s.cache, "omni.db"))
	if err != nil {
		return false
	}
	defer db.Close()
	row, err := db.Get(context.Background(), "fixture", "pnpm", "fixture-cli")
	return err == nil && row.Installed && row.InstalledWith == "pnpm" && row.Version.String == "2.0.0"
}

type toolsConsolidateCacheRow struct {
	Name, Provider, Package, InstalledWith, Version, LatestVersion, LastError string
	Installed, Outdated, OutdatedUnknown, Tracked                             bool
	FailureCount                                                              int
}

type toolsConsolidateState struct {
	Config           any
	Rows             []toolsConsolidateCacheRow
	Memberships      []string
	NPMMarker        string
	PNPMMarker       string
	ManagerMutations []string
}

func observeToolsConsolidateParity(t *testing.T, s *paritySandbox) toolsConsolidateState {
	t.Helper()
	state := toolsConsolidateState{
		Config:     normalizedParityConfig(t, s),
		NPMMarker:  readOptionalParityFile(t, filepath.Join(s.root, "npm-installed")),
		PNPMMarker: readOptionalParityFile(t, filepath.Join(s.root, "pnpm-installed")),
	}
	cfg, err := config.Load(s.configPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range cfg.Groups {
		for _, tool := range group.Tools {
			if tool.Name == "fixture" {
				state.Memberships = append(state.Memberships, group.Name+":"+tool.Name)
			}
		}
	}
	sort.Strings(state.Memberships)

	db, err := database.Open(filepath.Join(s.cache, "omni.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Name != "fixture" {
			continue
		}
		state.Rows = append(state.Rows, toolsConsolidateCacheRow{
			Name: row.Name, Provider: row.Provider, Package: row.Package, InstalledWith: row.InstalledWith,
			Version: row.Version.String, LatestVersion: row.LatestVersion.String, LastError: row.LastError.String,
			Installed: row.Installed, Outdated: row.Outdated, OutdatedUnknown: row.OutdatedUnknown,
			Tracked: row.Tracked, FailureCount: row.FailureCount,
		})
	}
	sort.Slice(state.Rows, func(i, j int) bool {
		left, right := state.Rows[i], state.Rows[j]
		return left.Provider+"\x00"+left.Package < right.Provider+"\x00"+right.Package
	})

	for _, line := range strings.Split(readOptionalParityFile(t, filepath.Join(s.root, "node-managers.log")), "\n") {
		if strings.Contains(line, " add -g ") || strings.Contains(line, " uninstall -g ") || strings.Contains(line, " remove -g ") {
			state.ManagerMutations = append(state.ManagerMutations, line)
		}
	}
	sort.Strings(state.ManagerMutations)
	return state
}

func readOptionalParityFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(content))
}

func readToolsConsolidateThroughCLI(t *testing.T, bin string, s *paritySandbox) {
	t.Helper()
	out := runOmniOutput(t, bin, s.root, s.env,
		"--config", s.configPath, "--cache-dir", s.cache,
		"tools", "list", "fixture", "--format", "json")
	var rows []struct {
		Name          string `json:"name"`
		Provider      string `json:"provider"`
		Package       string `json:"package"`
		Installed     bool   `json:"installed"`
		InstalledWith string `json:"installed_with"`
		Version       string `json:"version"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("decode tools.consolidate CLI readback: %v\n%s", err, out)
	}
	for _, row := range rows {
		if row.Name == "fixture" && row.Provider == "pnpm" && row.Package == "fixture-cli" && row.Installed && row.InstalledWith == "pnpm" && row.Version == "2.0.0" {
			return
		}
	}
	t.Fatalf("CLI did not observe consolidated node tool: %#v", rows)
}

func assertToolsConsolidateOutcome(t *testing.T, s *paritySandbox, state toolsConsolidateState) {
	t.Helper()
	cfg, err := config.Load(s.configPath)
	if err != nil {
		t.Fatal(err)
	}
	spec := cfg.Tools["fixture"]
	if spec.Provider != "" || spec.Package != "" || spec.InstallWith != "" || len(spec.Providers) != 2 || spec.Providers[0].Provider != "npm" || spec.Providers[1].Provider != "pnpm" {
		t.Fatalf("consolidated provider candidates = %#v", spec)
	}
	if got := cfg.HostSettings["testhost"].ProviderPriority; len(got) == 0 || got[0] != "pnpm" {
		t.Fatalf("consolidated provider priority = %#v", got)
	}
	if !reflect.DeepEqual(state.Memberships, []string{"dev:fixture"}) {
		t.Fatalf("consolidated memberships = %#v", state.Memberships)
	}
	if state.NPMMarker != "" || state.PNPMMarker != "fixture-cli@2.0.0" {
		t.Fatalf("consolidated manager markers: npm=%q pnpm=%q", state.NPMMarker, state.PNPMMarker)
	}
	wantMutations := []string{"npm uninstall -g fixture-cli", "pnpm add -g fixture-cli"}
	if !reflect.DeepEqual(state.ManagerMutations, wantMutations) {
		t.Fatalf("consolidated manager mutations = %#v, want %#v", state.ManagerMutations, wantMutations)
	}
	if len(state.Rows) != 1 || state.Rows[0].Provider != "pnpm" || state.Rows[0].Package != "fixture-cli" || !state.Rows[0].Installed || state.Rows[0].InstalledWith != "pnpm" || state.Rows[0].Version != "2.0.0" || !state.Rows[0].Tracked {
		t.Fatalf("consolidated cache rows = %#v", state.Rows)
	}
}
