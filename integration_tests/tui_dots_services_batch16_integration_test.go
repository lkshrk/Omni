//go:build integration

package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
)

func TestCLIAndTUIDotsReminderProduceEquivalentServiceState(t *testing.T) {
	cliBin, cliRoot, _, cliCache, cliConfig, cliEnv, cliLog := batch16DotsServiceFixture(t)
	tuiBin, tuiRoot, tuiHome, tuiCache, tuiConfig, tuiEnv, tuiLog := batch16DotsServiceFixture(t)
	runOmniCommand(t, cliBin, cliRoot, cliEnv, "--config", cliConfig, "--cache-dir", cliCache, "dots", "reminder", "install")
	installBatch16ReminderTUI(t, tuiBin, tuiRoot, tuiHome, tuiCache, tuiConfig, tuiEnv, tuiLog)
	cli := batch16ReminderStatus(t, cliBin, cliRoot, cliEnv, cliConfig, cliCache)
	tui := batch16ReminderStatus(t, tuiBin, tuiRoot, tuiEnv, tuiConfig, tuiCache)
	if !reflect.DeepEqual(cli, tui) {
		t.Fatalf("reminder service state differs\nCLI: %#v\nTUI: %#v", cli, tui)
	}
	cliCommands := batch16ServiceCommands(t, cliLog)
	tuiCommands := batch16ServiceCommands(t, tuiLog)
	if !reflect.DeepEqual(cliCommands, tuiCommands) {
		t.Fatalf("reminder service commands differ\nCLI: %#v\nTUI: %#v", cliCommands, tuiCommands)
	}
}

func TestCLIAndTUIDotsWatchProduceEquivalentServiceState(t *testing.T) {
	cliBin, cliRoot, _, cliCache, cliConfig, cliEnv, cliLog := batch16DotsServiceFixture(t)
	tuiBin, tuiRoot, tuiHome, tuiCache, tuiConfig, tuiEnv, tuiLog := batch16DotsServiceFixture(t)
	runOmniCommand(t, cliBin, cliRoot, cliEnv, "--config", cliConfig, "--cache-dir", cliCache, "dots", "watch", "install", "--debounce", "5s")
	installBatch16WatchTUI(t, tuiBin, tuiRoot, tuiHome, tuiCache, tuiConfig, tuiEnv, tuiLog)
	cliStatus := batch16WatchStatus(t, cliBin, cliRoot, cliEnv, cliConfig, cliCache)
	tuiStatus := batch16WatchStatus(t, tuiBin, tuiRoot, tuiEnv, tuiConfig, tuiCache)
	if !reflect.DeepEqual(cliStatus, tuiStatus) {
		t.Fatalf("watch service state differs\nCLI: %#v\nTUI: %#v", cliStatus, tuiStatus)
	}
	cliCommands := batch16ServiceCommands(t, cliLog)
	tuiCommands := batch16ServiceCommands(t, tuiLog)
	if !reflect.DeepEqual(cliCommands, tuiCommands) {
		t.Fatalf("watch service commands differ\nCLI: %#v\nTUI: %#v", cliCommands, tuiCommands)
	}
	cliCfg := batch16NormalizedServiceConfig(t, cliConfig)
	tuiCfg := batch16NormalizedServiceConfig(t, tuiConfig)
	if !reflect.DeepEqual(cliCfg, tuiCfg) {
		t.Fatalf("watch config differs\nCLI: %#v\nTUI: %#v", cliCfg, tuiCfg)
	}
}

func TestCLIAndTUIDotsRefreshPreserveEquivalentBrokenLinkState(t *testing.T) {
	cli := batch16BrokenDotsFixture(t)
	tui := batch16BrokenDotsFixture(t)
	runOmniCommand(t, cli.bin, cli.root, cli.env, "--config", cli.configPath, "--cache-dir", cli.cache, "dots", "discover", "--format", "json")
	runOmniCommand(t, tui.bin, tui.root, tui.env, "--config", tui.configPath, "--cache-dir", tui.cache, "dots", "sync")
	runTUI(t, tui.bin, tui.root, tui.env, []string{"--config", tui.configPath, "--cache-dir", tui.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 7*time.Second, screenHas("Dashboard", "Tools"), "TUI did not start")
		writeTUIKeys(t, term, "\t", "\t")
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("nvim", "synced"), "TUI did not render synced dot state")
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			writeTUIKeys(t, term, "R")
			if _, ok := waitForScreen(term, 600*time.Millisecond, screenHas("no untracked dotfile candidates")); ok {
				break
			}
		}
		waitForRequiredScreen(t, term, time.Second, screenHas("no untracked dotfile candidates"), "TUI launch dots sync did not settle before refresh")
		if err := os.Remove(tui.target); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(tui.root, "missing"), tui.target); err != nil {
			t.Fatal(err)
		}
		writeTUIKeys(t, term, "R")
		return waitForRequiredScreen(t, term, 8*time.Second, screenHas("nvim", "broken"), "TUI refresh lost broken dot state")
	})
	cliState := batch16ReadBrokenDotsState(t, cli)
	tuiState := batch16ReadBrokenDotsState(t, tui)
	if !reflect.DeepEqual(cliState, tuiState) {
		t.Fatalf("dots.refresh state differs\nCLI: %#v\nTUI: %#v", cliState, tuiState)
	}
}

type batch16ReminderObservation struct {
	Platform  string        `json:"platform"`
	Interval  time.Duration `json:"interval"`
	Notify    bool          `json:"notify"`
	Installed bool          `json:"installed"`
}

type batch16WatchObservation struct {
	Platform  string        `json:"platform"`
	Debounce  time.Duration `json:"debounce"`
	Installed bool          `json:"installed"`
}

type batch16DotsFixture struct {
	bin, root, cache, configPath, target string
	env                                  []string
}

type batch16BrokenDotsState struct {
	LinkTarget string
	Config     *config.RootConfig
}

func batch16BrokenDotsFixture(t *testing.T) batch16DotsFixture {
	t.Helper()
	bin := batch16OmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	env := isolatedTUIEnv(t, home, cache)
	repo := filepath.Join(home, "dotfiles")
	target := filepath.Join(home, ".config", "nvim", "init.lua")
	source := filepath.Join(repo, "dotfiles", "nvim", ".config", "nvim", "init.lua")
	initDotsRepo(t, repo, env)
	writeIntegrationFile(t, source, "repo\n")
	if err := config.Save(configPath, &config.RootConfig{
		Version: config.CurrentVersion, Settings: config.Settings{DotsRepo: repo}, Hosts: map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{{Name: "testhost", Special: "host", Dots: []config.DotEntry{{Name: "nvim", Path: target}}}},
	}); err != nil {
		t.Fatal(err)
	}
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "dots", "sync")
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "missing"), target); err != nil {
		t.Fatal(err)
	}
	return batch16DotsFixture{bin: bin, root: root, cache: cache, configPath: configPath, target: target, env: env}
}

func batch16ReadBrokenDotsState(t *testing.T, fixture batch16DotsFixture) batch16BrokenDotsState {
	t.Helper()
	link, err := os.Readlink(fixture.target)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(fixture.configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Settings.DotsRepo = ""
	for _, group := range cfg.Groups {
		for i := range group.Dots {
			group.Dots[i].Path = "~/.config/nvim/init.lua"
		}
	}
	return batch16BrokenDotsState{LinkTarget: filepath.Base(link), Config: cfg}
}

func installBatch16ReminderTUI(t *testing.T, bin, root, home, cache, configPath string, env []string, systemctlLog string) {
	t.Helper()
	service := filepath.Join(home, ".config", "systemd", "user", "omni-dots-reminder.service")
	runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		openTUISettingsActions(t, term)
		batch16RevealSettingsCursor(t, term)
		writeTUIKeys(t, term, "j", "j", "j", "j", " ")
		return waitForRequiredScreen(t, term, 10*time.Second, func(string) bool {
			_, serviceErr := os.Stat(service)
			log, logErr := os.ReadFile(systemctlLog)
			return serviceErr == nil && logErr == nil && strings.Contains(string(log), "enable --now omni-dots-reminder.timer")
		}, "TUI did not install reminder service")
	})
}

func installBatch16WatchTUI(t *testing.T, bin, root, home, cache, configPath string, env []string, systemctlLog string) {
	t.Helper()
	service := filepath.Join(home, ".config", "systemd", "user", "omni-dots-watch.service")
	runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		openTUISettingsActions(t, term)
		waitForRequiredScreen(t, term, 10*time.Second, func(text string) bool {
			return strings.Contains(text, "Watch Sync") && !strings.Contains(text, "Running doctor") && !strings.Contains(text, "Refreshing doctor")
		}, "TUI doctor activity did not settle")
		batch16RevealSettingsCursor(t, term)
		writeTUIKeys(t, term, "j", "j", "j", "j", "j", "j", " ")
		return waitForRequiredScreen(t, term, 10*time.Second, func(string) bool {
			_, serviceErr := os.Stat(service)
			log, logErr := os.ReadFile(systemctlLog)
			return serviceErr == nil && logErr == nil && strings.Contains(string(log), "enable --now omni-dots-watch.service")
		}, "TUI did not install watch service")
	})
}

func batch16ReminderStatus(t *testing.T, bin, root string, env []string, configPath, cache string) batch16ReminderObservation {
	t.Helper()
	out := runOmniOutput(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "dots", "reminder", "status", "--format", "json")
	var status batch16ReminderObservation
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		t.Fatal(err)
	}
	return status
}

func batch16WatchStatus(t *testing.T, bin, root string, env []string, configPath, cache string) batch16WatchObservation {
	t.Helper()
	out := runOmniOutput(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "dots", "watch", "status", "--format", "json")
	var status batch16WatchObservation
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		t.Fatal(err)
	}
	return status
}

func batch16ServiceCommands(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var commands []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			commands = append(commands, line)
		}
	}
	return commands
}

func batch16NormalizedServiceConfig(t *testing.T, path string) *config.RootConfig {
	t.Helper()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Settings.DotsRepo = "$ROOT/dotfiles"
	return cfg
}

func batch16RevealSettingsCursor(t *testing.T, term *vttest.Terminal) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		sendTUIKey(term, uv.KeyHome)
		if _, ok := waitForScreen(term, 500*time.Millisecond, screenHas("> Import Installed Tools")); ok {
			return
		}
	}
	t.Fatalf("TUI did not reveal the settings cursor; screen:\n%s", currentScreenText(term))
}

func batch16DotsServiceFixture(t *testing.T) (bin, root, home, cache, configPath string, env []string, systemctlLog string) {
	t.Helper()
	bin = batch16OmniBinary(t)
	root = t.TempDir()
	home = filepath.Join(root, "home")
	cache = filepath.Join(root, "cache")
	configPath = filepath.Join(root, "settings.json")
	env = isolatedTUIEnv(t, home, cache)
	repo := filepath.Join(home, "dotfiles")
	initDotsRepo(t, repo, env)
	if err := config.Save(configPath, &config.RootConfig{
		Version: config.CurrentVersion, Settings: config.Settings{DotsRepo: repo},
		Hosts: map[string][]string{"testhost": {}}, Groups: []*config.GroupConfig{{Name: "testhost", Special: "host"}},
	}); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(root, "bin")
	systemctlLog = filepath.Join(root, "systemctl.log")
	writeExecutable(t, filepath.Join(binDir, "systemctl"), "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$OMNI_TEST_SYSTEMCTL_LOG\"\n")
	env = replaceIntegrationEnv(env, "PATH", binDir+string(os.PathListSeparator)+integrationEnvValue(env, "PATH"))
	env = append(env, "OMNI_TEST_SYSTEMCTL_LOG="+systemctlLog)
	return bin, root, home, cache, configPath, env, systemctlLog
}

func batch16OmniBinary(t *testing.T) string {
	t.Helper()
	prebuilt := os.Getenv("OMNI_TEST_HELPER_BATCH16_BINARY")
	if prebuilt == "" {
		return buildOmniBinary(t)
	}
	raw, err := os.ReadFile(prebuilt)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "omni")
	if err := os.WriteFile(path, raw, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
