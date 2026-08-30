//go:build integration

package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestCLIBinaryReconcileConvergesToolDotsAndBackupState(t *testing.T) {
	root, home, cache, env, configPath, state, logPath, repo := reconcileBinaryFixture(t, true)
	repoFile := filepath.Join(repo, "dotfiles", "nvim", ".config", "nvim", "init.lua")
	writeIntegrationFile(t, repoFile, "reconciled config\n")

	out := runOmniOutput(t, buildOmniBinary(t), root, env, "--yes", "--config", configPath, "--cache-dir", cache, "reconcile", "--skip-privileged", "--force", "--message", "dots: blackbox reconcile")
	if !strings.Contains(out, "Reconcile complete") || !strings.Contains(out, "syncing tools") || !strings.Contains(out, "syncing dotfiles") {
		t.Fatalf("reconcile output omitted lifecycle stages: %s", out)
	}
	// Reconcile converges sequentially: SyncAll installs, then UpgradeAll evaluates the refreshed state.
	if raw, err := os.ReadFile(state); err != nil || strings.TrimSpace(string(raw)) != "1.1.0" {
		t.Fatalf("reconciled provider state = %q, %v", raw, err)
	}
	assertFileContains(t, logPath, "install")
	assertFileContains(t, logPath, "upgrade")
	target := filepath.Join(home, ".config", "nvim", "init.lua")
	if info, err := os.Lstat(target); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("reconciled dot target is not a symlink: %v, %v", info, err)
	}
	if raw, err := os.ReadFile(target); err != nil || string(raw) != "reconciled config\n" {
		t.Fatalf("reconciled dot content = %q, %v", raw, err)
	}
	if got := runCommandOutput(t, repo, env, "git", "log", "-1", "--pretty=%s", "omni/backup"); got != "dots: blackbox reconcile" {
		t.Fatalf("backup commit subject = %q", got)
	}
}

func TestCLIBinaryToolsSyncThenUpdateAllUsesBulkProviderLifecycle(t *testing.T) {
	root, _, cache, env, configPath, state, logPath, _ := reconcileBinaryFixture(t, false)
	bin := buildOmniBinary(t)

	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "tools", "sync")
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "tools", "refresh")
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "tools", "upgrade", "--all", "--force")
	if raw, err := os.ReadFile(state); err != nil || strings.TrimSpace(string(raw)) != "1.1.0" {
		t.Fatalf("bulk-updated provider state = %q, %v", raw, err)
	}
	assertFileContains(t, logPath, "install")
	assertFileContains(t, logPath, "upgrade")
}

func TestCLIBinaryToolsDeletePurgesProviderAndLogicalSpec(t *testing.T) {
	root, _, cache, env, configPath, state, logPath, _ := reconcileBinaryFixture(t, false)
	bin := buildOmniBinary(t)
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "tools", "sync")

	runOmniCommand(t, bin, root, env, "--yes", "--config", configPath, "--cache-dir", cache, "tools", "remove", "fixture", "--purge", "--provider", "script")
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("purged provider state remained: %v", err)
	}
	assertFileContains(t, logPath, "uninstall")
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Tools["fixture"]; ok {
		t.Fatalf("purged logical spec remained: %#v", cfg.Tools["fixture"])
	}
	for _, group := range cfg.Groups {
		for _, tool := range group.Tools {
			if tool.Name == "fixture" {
				t.Fatalf("purged group membership remained: %#v", group)
			}
		}
	}
}

func reconcileBinaryFixture(t *testing.T, withDots bool) (root, home, cache string, env []string, configPath, state, logPath, repo string) {
	t.Helper()
	root, home, cache, env = newCLIBinarySandbox(t)
	configPath = filepath.Join(root, "settings.json")
	paths := seedReconcileBinaryFixture(t, root, home, configPath, &env, withDots)
	return root, home, cache, env, configPath, paths.state, paths.logPath, paths.repo
}

type reconcileFixturePaths struct {
	state   string
	logPath string
	repo    string
}

func seedReconcileBinaryFixture(t *testing.T, root, home, configPath string, env *[]string, withDots bool) reconcileFixturePaths {
	t.Helper()
	binDir := filepath.Join(root, "bin")
	state := filepath.Join(root, "provider-state")
	logPath := filepath.Join(root, "provider.log")
	writeExecutable(t, filepath.Join(binDir, "fake-provider"), `#!/bin/sh
set -eu
printf '%s\n' "$1" >> "$FAKE_PROVIDER_LOG"
case "$1" in
  install) printf '1.0.0\n' > "$FAKE_PROVIDER_STATE" ;;
  check) test -f "$FAKE_PROVIDER_STATE" ;;
  version) cat "$FAKE_PROVIDER_STATE" ;;
  latest) printf '1.1.0\n' ;;
  upgrade) printf '1.1.0\n' > "$FAKE_PROVIDER_STATE" ;;
  uninstall) rm -f "$FAKE_PROVIDER_STATE" ;;
  *) exit 64 ;;
esac
`)
	*env = replaceIntegrationEnv(*env, "PATH", binDir+string(os.PathListSeparator)+integrationEnvValue(*env, "PATH"))
	*env = append(*env, "FAKE_PROVIDER_STATE="+state, "FAKE_PROVIDER_LOG="+logPath)
	settings := config.Settings{DisabledProviders: []string{"apt", "apk", "dnf", "pacman", "zypper", "brew", "node", "bun", "pnpm", "npm", "python", "uv", "pip"}}
	group := &config.GroupConfig{Name: "testhost", Special: "host", Tools: []config.ToolEntry{{Name: "fixture"}}}
	var repo string
	if withDots {
		repo = filepath.Join(home, "dotfiles")
		initDotsRepo(t, repo, *env)
		settings.DotsRepo = repo
		group.Dots = []config.DotEntry{{Name: "nvim", Path: filepath.Join(home, ".config", "nvim")}}
	}
	if err := config.Save(configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: settings,
		Tools: map[string]config.ToolSpec{"fixture": {
			Provider: "script",
			Options: map[string]string{
				"install":   "fake-provider install",
				"check":     "fake-provider check",
				"version":   "fake-provider version",
				"latest":    "fake-provider latest",
				"upgrade":   "fake-provider upgrade",
				"uninstall": "fake-provider uninstall",
			},
		}},
		Hosts:  map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{group},
	}); err != nil {
		t.Fatal(err)
	}
	return reconcileFixturePaths{state: state, logPath: logPath, repo: repo}
}
