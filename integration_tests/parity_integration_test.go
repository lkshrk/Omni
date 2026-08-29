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
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

type paritySandbox struct {
	root       string
	home       string
	cache      string
	configPath string
	env        []string
}

type parityFlow struct {
	seed    func(*testing.T, *paritySandbox)
	runCLI  func(*testing.T, string, *paritySandbox)
	runTUI  func(*testing.T, string, *paritySandbox)
	observe func(*testing.T, *paritySandbox) any
	readTUI func(*testing.T, string, *paritySandbox)
}

func TestCLIAndTUIProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	// The adapters are intentionally asymmetric: CLI commands invoke actions directly while the
	// TUI driver uses screen text only to synchronize real PTY input. Only persisted semantics below
	// define parity; rendered output is never compared.
	t.Run("tools.install uses the same provider state and mutation", func(t *testing.T) {
		runParityFlow(t, bin, parityFlow{
			seed:    seedParityToolInstall,
			runCLI:  runParityToolInstallCLI,
			runTUI:  runParityToolInstallTUI,
			observe: observeParityToolInstall,
			readTUI: readParityToolThroughCLI,
		})
	})
	t.Run("settings.auto_import persists the same effective setting", func(t *testing.T) {
		runParityFlow(t, bin, parityFlow{
			seed:    seedParitySetting,
			runCLI:  runParitySettingCLI,
			runTUI:  runParitySettingTUI,
			observe: observeParityConfig,
			readTUI: readParitySettingThroughCLI,
		})
	})
	t.Run("dots.use_repo produces the same managed symlink and backup", func(t *testing.T) {
		runParityFlow(t, bin, parityFlow{
			seed:    seedParityDotsConflict,
			runCLI:  runParityDotsUseRepoCLI,
			runTUI:  runParityDotsUseRepoTUI,
			observe: observeParityDots,
			readTUI: readParityDotsThroughCLI,
		})
	})
}

func runParityFlow(t *testing.T, bin string, flow parityFlow) {
	t.Helper()
	cliRoot, tuiRoot := newParityTwins(t)
	flow.seed(t, cliRoot)
	flow.seed(t, tuiRoot)

	flow.runCLI(t, bin, cliRoot)
	flow.runTUI(t, bin, tuiRoot)
	flow.readTUI(t, bin, tuiRoot)

	cliState := flow.observe(t, cliRoot)
	tuiState := flow.observe(t, tuiRoot)
	if !reflect.DeepEqual(cliState, tuiState) {
		t.Fatalf("semantic state differs\nCLI: %#v\nTUI: %#v", cliState, tuiState)
	}
}

func newParityTwins(t *testing.T) (*paritySandbox, *paritySandbox) {
	t.Helper()
	root := t.TempDir()
	cliRoot := newParitySandbox(t, filepath.Join(root, "cli"))
	tuiRoot := newParitySandbox(t, filepath.Join(root, "tui"))
	for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "OMNI_CONFIG", "OMNI_CACHE_DIR", "OMNI_STATE_DIR", "TMPDIR"} {
		if integrationEnvValue(cliRoot.env, key) == integrationEnvValue(tuiRoot.env, key) {
			t.Fatalf("parity twins share writable %s", key)
		}
	}
	return cliRoot, tuiRoot
}

func newParitySandbox(t *testing.T, root string) *paritySandbox {
	t.Helper()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	return &paritySandbox{
		root:       root,
		home:       home,
		cache:      cache,
		configPath: filepath.Join(root, "settings.json"),
		env:        isolatedTUIEnv(t, home, cache),
	}
}

func seedParityToolInstall(t *testing.T, sandbox *paritySandbox) {
	t.Helper()
	state := filepath.Join(sandbox.root, "brew-installed")
	logPath := filepath.Join(sandbox.root, "brew.log")
	sandbox.env = append(sandbox.env, "OMNI_TEST_BREW_STATE="+state, "OMNI_TEST_BREW_LOG="+logPath)
	brew := `#!/bin/sh
set -eu
state="${OMNI_TEST_BREW_STATE:?}"
log="${OMNI_TEST_BREW_LOG:?}"
printf '%s\n' "$*" >> "$log"
case "$*" in
	"--version") echo "Homebrew 4.0.0" ;;
	"install omni-test-tool")
		if [ -n "${OMNI_TEST_BREW_BARRIER:-}" ]; then
			touch "${OMNI_TEST_BREW_BARRIER}.started"
			waits=0
			while [ ! -f "${OMNI_TEST_BREW_BARRIER}.release" ]; do
				waits=$((waits + 1))
				[ "$waits" -lt 1000 ] || exit 124
				sleep 0.01
			done
		fi
		echo "omni-test-tool" > "$state"
		;;
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
	if err := os.WriteFile(filepath.Join(sandbox.home, ".test-stub-bin", "brew"), []byte(brew), 0o755); err != nil {
		t.Fatalf("write fake brew: %v", err)
	}
	if err := config.Save(sandbox.configPath, &config.RootConfig{
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
		t.Fatalf("save tool parity config: %v", err)
	}
}

func runParityToolInstallCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"tools", "install", "omni-test-tool", "--provider", "brew")
}

func runParityToolInstallTUI(t *testing.T, bin string, sandbox *paritySandbox) {
	runTUI(t, bin, sandbox.root, sandbox.env, []string{"--config", sandbox.configPath, "--cache-dir", sandbox.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, func(text string) bool {
			return strings.Contains(text, "Dashboard") && strings.Contains(text, "Tools")
		}, "TUI did not render main tabs")
		writeTUIKeys(t, term, "\t")
		waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "omni-test-tool") && strings.Contains(text, "brew")
		}, "TUI did not render the missing parity tool")
		writeTUIKeys(t, term, "i")
		return waitForRequiredScreen(t, term, 8*time.Second, func(_ string) bool {
			return parityToolInstalled(sandbox)
		}, "TUI did not install the parity tool")
	})
}

func parityToolInstalled(sandbox *paritySandbox) bool {
	if _, err := os.Stat(filepath.Join(sandbox.root, "brew-installed")); err != nil {
		return false
	}
	db, err := database.Open(filepath.Join(sandbox.cache, "omni.db"))
	if err != nil {
		return false
	}
	defer func() { _ = db.Close() }()
	tool, err := db.Get(context.Background(), "omni-test-tool", "brew", "omni-test-tool")
	return err == nil && tool.Installed
}

type parityToolState struct {
	Config        any
	Name          string
	Provider      string
	Package       string
	Installed     bool
	InstalledWith string
	Version       string
	Tracked       bool
	Marker        string
	Mutations     []string
}

func observeParityToolInstall(t *testing.T, sandbox *paritySandbox) any {
	t.Helper()
	db, err := database.Open(filepath.Join(sandbox.cache, "omni.db"))
	if err != nil {
		t.Fatalf("open parity tool cache: %v", err)
	}
	defer db.Close()
	tool, err := db.Get(context.Background(), "omni-test-tool", "brew", "omni-test-tool")
	if err != nil {
		t.Fatalf("read parity tool cache: %v", err)
	}
	marker, err := os.ReadFile(filepath.Join(sandbox.root, "brew-installed"))
	if err != nil {
		t.Fatalf("read fake brew marker: %v", err)
	}
	return parityToolState{
		Config:        normalizedParityConfig(t, sandbox),
		Name:          tool.Name,
		Provider:      tool.Provider,
		Package:       tool.Package,
		Installed:     tool.Installed,
		InstalledWith: tool.InstalledWith,
		Version:       tool.Version.String,
		Tracked:       tool.Tracked,
		Marker:        string(marker),
		Mutations:     normalizedParityMutations(t, filepath.Join(sandbox.root, "brew.log")),
	}
}

func readParityToolThroughCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	out := runOmniOutput(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"tools", "list", "omni-test-tool", "--format", "json")
	var tools []struct {
		Name      string `json:"name"`
		Installed bool   `json:"installed"`
	}
	if err := json.Unmarshal([]byte(out), &tools); err != nil {
		t.Fatalf("decode CLI tool read over TUI state: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "omni-test-tool" || !tools[0].Installed {
		t.Fatalf("CLI did not observe TUI-installed tool: %#v", tools)
	}
}

func seedParitySetting(t *testing.T, sandbox *paritySandbox) {
	t.Helper()
	if err := config.Save(sandbox.configPath, &config.RootConfig{
		Version: config.CurrentVersion,
		Settings: config.Settings{
			DisabledProviders: []string{"system", "node", "python", "pip"},
		},
		Hosts: map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "work"},
		},
	}); err != nil {
		t.Fatalf("save setting parity config: %v", err)
	}
}

func runParitySettingCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"settings", "set", "auto_import", "true")
}

func runParitySettingTUI(t *testing.T, bin string, sandbox *paritySandbox) {
	runTUI(t, bin, sandbox.root, sandbox.env, []string{"--config", sandbox.configPath, "--cache-dir", sandbox.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, func(text string) bool {
			return strings.Contains(text, "Dashboard") && strings.Contains(text, "Tools")
		}, "TUI did not render main tabs")
		writeTUIKeys(t, term, "\t", "\t", "\t", "\t", "\t")
		waitForRequiredScreen(t, term, 5*time.Second, func(text string) bool {
			return strings.Contains(text, "Import Installed Tools")
		}, "TUI did not render settings")
		writeTUIKeys(t, term, " ")
		return waitForRequiredScreen(t, term, 8*time.Second, func(_ string) bool {
			cfg, err := config.Load(sandbox.configPath)
			return err == nil && cfg.Settings.AutoImport
		}, "TUI did not persist auto-import")
	})
}

func observeParityConfig(t *testing.T, sandbox *paritySandbox) any {
	return normalizedParityConfig(t, sandbox)
}

func readParitySettingThroughCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	out := runOmniOutput(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"settings", "show", "auto_import", "--format", "json")
	var values map[string]bool
	if err := json.Unmarshal([]byte(out), &values); err != nil {
		t.Fatalf("decode CLI setting read over TUI state: %v", err)
	}
	if !values["auto_import"] {
		t.Fatalf("CLI did not observe TUI setting mutation: %#v", values)
	}
}

func seedParityDotsConflict(t *testing.T, sandbox *paritySandbox) {
	t.Helper()
	repo := filepath.Join(sandbox.home, "dotfiles")
	target := filepath.Join(sandbox.home, ".config", "nvim", "init.lua")
	source := filepath.Join(repo, "dotfiles", "nvim", ".config", "nvim", "init.lua")
	if err := os.MkdirAll(sandbox.home, 0o755); err != nil {
		t.Fatal(err)
	}
	initDotsRepo(t, repo, sandbox.env)
	writeIntegrationFile(t, source, "repo version\n")
	runCommand(t, repo, sandbox.env, "git", "add", ".")
	runCommand(t, repo, sandbox.env, "git", "commit", "-m", "add nvim dotfile")
	writeIntegrationFile(t, target, "local version\n")
	localTime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(target, localTime, localTime); err != nil {
		t.Fatalf("set local timestamp: %v", err)
	}
	if err := os.Chtimes(source, localTime.Add(time.Hour), localTime.Add(time.Hour)); err != nil {
		t.Fatalf("set repo timestamp: %v", err)
	}
	if err := config.Save(sandbox.configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{DotsRepo: repo},
		Hosts:    map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{{
			Name:    "testhost",
			Special: "host",
			Dots:    []config.DotEntry{{Name: "nvim", Path: target}},
		}},
	}); err != nil {
		t.Fatalf("save dots parity config: %v", err)
	}
}

func runParityDotsUseRepoCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--yes", "--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"dots", "sync", "nvim", "--use-repo")
}

func runParityDotsUseRepoTUI(t *testing.T, bin string, sandbox *paritySandbox) {
	runTUI(t, bin, sandbox.root, sandbox.env, []string{"--config", sandbox.configPath, "--cache-dir", sandbox.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, func(text string) bool {
			return strings.Contains(text, "Dashboard") && strings.Contains(text, "Tools")
		}, "TUI did not render main tabs")
		writeTUIKeys(t, term, "\t", "\t")
		waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "nvim") && strings.Contains(strings.ToLower(text), "conflict")
		}, "TUI did not render the dots conflict")
		writeTUIKeys(t, term, "u")
		waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "confirm use repo")
		}, "TUI did not arm use-repo confirmation")
		writeTUIKeys(t, term, "u")
		return waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "nvim") && strings.Contains(strings.ToLower(text), "synced")
		}, "TUI did not resolve the dots conflict")
	})
}

type parityDotsState struct {
	Config        any
	RepoTree      string
	RepoStatus    string
	TargetKind    string
	TargetLink    string
	TargetContent string
	BackupKind    string
	BackupContent string
}

func observeParityDots(t *testing.T, sandbox *paritySandbox) any {
	t.Helper()
	repo := filepath.Join(sandbox.home, "dotfiles")
	target := filepath.Join(sandbox.home, ".config", "nvim", "init.lua")
	backup := filepath.Join(sandbox.home, "dotfiles.bkp", ".config", "nvim", "init.lua")
	targetInfo, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("lstat dots target: %v", err)
	}
	backupInfo, err := os.Lstat(backup)
	if err != nil {
		t.Fatalf("lstat dots backup: %v", err)
	}
	link, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("resolve dots symlink: %v", err)
	}
	targetContent, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read dots target: %v", err)
	}
	backupContent, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("read dots backup: %v", err)
	}
	return parityDotsState{
		Config:        normalizedParityConfig(t, sandbox),
		RepoTree:      runCommandOutput(t, repo, sandbox.env, "git", "rev-parse", "HEAD^{tree}"),
		RepoStatus:    runCommandOutput(t, repo, sandbox.env, "git", "status", "--porcelain=v1"),
		TargetKind:    targetInfo.Mode().Type().String(),
		TargetLink:    strings.ReplaceAll(link, sandbox.root, "$ROOT"),
		TargetContent: string(targetContent),
		BackupKind:    backupInfo.Mode().Type().String(),
		BackupContent: string(backupContent),
	}
}

func readParityDotsThroughCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	out := runOmniOutput(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"dots", "status", "nvim", "--format", "json")
	var status any
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		t.Fatalf("decode CLI dots read over TUI state: %v", err)
	}
	if !parityJSONContains(status, "synced") {
		t.Fatalf("CLI did not observe TUI-synced dots state: %#v", status)
	}
}

func normalizedParityConfig(t *testing.T, sandbox *paritySandbox) any {
	t.Helper()
	content, err := os.ReadFile(sandbox.configPath)
	if err != nil {
		t.Fatalf("read parity config: %v", err)
	}
	content = []byte(strings.ReplaceAll(string(content), sandbox.root, "$ROOT"))
	var normalized any
	if err := json.Unmarshal(content, &normalized); err != nil {
		t.Fatalf("decode parity config: %v", err)
	}
	return normalized
}

func normalizedParityMutations(t *testing.T, path string) []string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read parity command log: %v", err)
	}
	var mutations []string
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(line, "install ") || strings.HasPrefix(line, "uninstall ") || strings.HasPrefix(line, "upgrade ") {
			mutations = append(mutations, line)
		}
	}
	sort.Strings(mutations)
	return mutations
}

func parityJSONContains(value any, want string) bool {
	switch value := value.(type) {
	case string:
		return value == want
	case []any:
		for _, item := range value {
			if parityJSONContains(item, want) {
				return true
			}
		}
	case map[string]any:
		for _, item := range value {
			if parityJSONContains(item, want) {
				return true
			}
		}
	}
	return false
}
