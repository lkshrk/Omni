//go:build integration

package integration_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
)

func TestTUIDotsReminderInstallsSandboxService(t *testing.T) {
	bin, root, home, cache, configPath, env, systemctlLog := batch16DotsServiceFixture(t)
	service := filepath.Join(home, ".config", "systemd", "user", "omni-dots-reminder.service")
	timer := filepath.Join(home, ".config", "systemd", "user", "omni-dots-reminder.timer")
	runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		openTUISettingsActions(t, term)
		revealTUISettingsCursor(t, term)
		writeTUIKeys(t, term, "j", "j", "j", "j")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("> Reminder Notifications"), "TUI did not select reminder settings")
		writeTUIKeys(t, term, " ")
		return waitForRequiredScreen(t, term, 10*time.Second, func(string) bool {
			_, serviceErr := os.Stat(service)
			_, timerErr := os.Stat(timer)
			return serviceErr == nil && timerErr == nil
		}, "TUI did not install reminder service files")
	})
	assertFileContains(t, systemctlLog, "enable --now omni-dots-reminder.timer")
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
