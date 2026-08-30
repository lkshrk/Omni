//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/config"
)

func TestCLIBinaryDotsServiceCommands(t *testing.T) {
	t.Run("dots.reminder", func(t *testing.T) {
		root, home, cache, env, configPath, systemctlLog := dotsServicesBinaryFixture(t)
		bin := buildOmniBinary(t)
		out := runOmniOutput(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "dots", "reminder", "install", "--interval", "2m", "--notify=false")
		service := filepath.Join(home, ".config", "systemd", "user", "omni-dots-reminder.service")
		timer := filepath.Join(home, ".config", "systemd", "user", "omni-dots-reminder.timer")
		if !strings.Contains(out, "Installed dots reminder service") {
			t.Fatalf("install output: %s", out)
		}
		for _, path := range []string{service, timer} {
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("missing reminder service file %s: %v", path, err)
			}
		}
		assertFileContains(t, systemctlLog, "enable --now omni-dots-reminder.timer")

		runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "dots", "reminder", "uninstall")
		for _, path := range []string{service, timer} {
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("reminder service file survived uninstall %s: %v", path, err)
			}
		}
		assertFileContains(t, systemctlLog, "disable --now omni-dots-reminder.timer")
	})

	t.Run("dots.reminder.check", func(t *testing.T) { runReminderCheckRun(t, "check") })
	t.Run("dots.reminder.run", func(t *testing.T) { runReminderCheckRun(t, "run") })

	t.Run("dots.reminder.status", func(t *testing.T) {
		root, _, cache, env, configPath, _ := dotsServicesBinaryFixture(t)
		bin := buildOmniBinary(t)
		runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "dots", "reminder", "install", "--interval", "2m", "--notify=false")
		out := runOmniOutput(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "dots", "reminder", "status", "--format", "json")
		var status struct {
			Platform  string        `json:"platform"`
			Interval  time.Duration `json:"interval"`
			Notify    bool          `json:"notify"`
			Installed bool          `json:"installed"`
		}
		if err := json.Unmarshal([]byte(out), &status); err != nil || !status.Installed || status.Platform != "systemd" || status.Interval != 2*time.Minute || status.Notify {
			t.Fatalf("reminder status = %+v, %v\n%s", status, err, out)
		}
	})

	t.Run("dots.watch", func(t *testing.T) {
		root, home, cache, env, configPath, systemctlLog := dotsServicesBinaryFixture(t)
		bin := buildOmniBinary(t)
		out := runOmniOutput(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "dots", "watch", "install", "--debounce", "2s")
		service := filepath.Join(home, ".config", "systemd", "user", "omni-dots-watch.service")
		if !strings.Contains(out, "Installed dots watch service") {
			t.Fatalf("install output: %s", out)
		}
		if _, err := os.Stat(service); err != nil {
			t.Fatalf("missing watch service: %v", err)
		}
		assertFileContains(t, systemctlLog, "enable --now omni-dots-watch.service")

		runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "dots", "watch", "uninstall")
		if _, err := os.Stat(service); !os.IsNotExist(err) {
			t.Fatalf("watch service survived uninstall: %v", err)
		}
		assertFileContains(t, systemctlLog, "disable --now omni-dots-watch.service")
	})

	t.Run("dots.watch.run", func(t *testing.T) {
		root, _, cache, env, configPath, _ := dotsServicesBinaryFixture(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, buildOmniBinary(t), "--config", configPath, "--cache-dir", cache, "dots", "watch", "run", "--debounce", "500ms")
		cmd.Dir = root
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) || err == nil || !strings.Contains(string(out), "Watching 0 dotfile path(s).") {
			t.Fatalf("watch run = err %v, ctx %v\n%s", err, ctx.Err(), out)
		}
	})

	t.Run("dots.watch.status", func(t *testing.T) {
		root, _, cache, env, configPath, _ := dotsServicesBinaryFixture(t)
		bin := buildOmniBinary(t)
		runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "dots", "watch", "install", "--debounce", "2s")
		out := runOmniOutput(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "dots", "watch", "status", "--format", "json")
		var status struct {
			Platform  string        `json:"platform"`
			Debounce  time.Duration `json:"debounce"`
			Installed bool          `json:"installed"`
		}
		if err := json.Unmarshal([]byte(out), &status); err != nil || !status.Installed || status.Platform != "systemd" || status.Debounce != 2*time.Second {
			t.Fatalf("watch status = %+v, %v\n%s", status, err, out)
		}
	})

	t.Run("dots.services.status", func(t *testing.T) {
		root, _, cache, env, configPath, _ := dotsServicesBinaryFixture(t)
		bin := buildOmniBinary(t)
		runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "dots", "reminder", "install", "--interval", "2m", "--notify=false")
		runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "dots", "watch", "install", "--debounce", "2s")
		out := runOmniOutput(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "dots", "services", "status", "--format", "json")
		var status struct {
			Reminder *struct {
				Installed bool `json:"installed"`
			} `json:"reminder"`
			Watch *struct {
				Installed bool `json:"installed"`
			} `json:"watch"`
		}
		if err := json.Unmarshal([]byte(out), &status); err != nil || status.Reminder == nil || !status.Reminder.Installed || status.Watch == nil || !status.Watch.Installed {
			t.Fatalf("services status = %+v, %v\n%s", status, err, out)
		}
	})

	t.Run("dots.refresh", func(t *testing.T) {
		root, _, cache, env, configPath, _ := dotsServicesBinaryFixture(t)
		out := runOmniOutput(t, buildOmniBinary(t), root, env, "--config", configPath, "--cache-dir", cache, "dots", "discover", "--format", "json")
		var discovered []json.RawMessage
		if err := json.Unmarshal([]byte(out), &discovered); err != nil {
			t.Fatalf("dots discover output: %v\n%s", err, out)
		}
	})
}

func runReminderCheckRun(t *testing.T, command string) {
	t.Helper()
	root, _, cache, env, configPath, _ := dotsServicesBinaryFixture(t)
	out := runOmniOutput(t, buildOmniBinary(t), root, env, "--config", configPath, "--cache-dir", cache, "dots", "reminder", command, "--format", "json")
	var result struct {
		NeedsReminder bool `json:"needs_reminder"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil || result.NeedsReminder {
		t.Fatalf("reminder %s output = %+v, %v\n%s", command, result, err, out)
	}
}

func dotsServicesBinaryFixture(t *testing.T) (root, home, cache string, env []string, configPath, systemctlLog string) {
	t.Helper()
	root, home, cache, env = newCLIBinarySandbox(t)
	repo := filepath.Join(home, "dotfiles")
	initDotsRepo(t, repo, env)
	configPath = filepath.Join(root, "settings.json")
	if err := config.Save(configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{DotsRepo: repo},
		Hosts:    map[string][]string{"testhost": {}},
		Groups:   []*config.GroupConfig{{Name: "testhost", Special: "host"}},
	}); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(root, "bin")
	systemctlLog = filepath.Join(root, "systemctl.log")
	writeExecutable(t, filepath.Join(binDir, "systemctl"), "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$OMNI_TEST_SYSTEMCTL_LOG\"\n")
	env = replaceIntegrationEnv(env, "PATH", binDir+string(os.PathListSeparator)+integrationEnvValue(env, "PATH"))
	env = append(env, "OMNI_TEST_SYSTEMCTL_LOG="+systemctlLog)
	return root, home, cache, env, configPath, systemctlLog
}
