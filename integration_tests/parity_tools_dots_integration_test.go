//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

func TestCLIAndTUIToolDeleteProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	runParityFlow(t, bin, parityFlow{
		seed:    seedParityScriptTool,
		runCLI:  runParityToolDeleteCLI,
		runTUI:  runParityToolDeleteTUI,
		observe: observeParityScriptTool,
		readTUI: readParityDeletedToolThroughCLI,
	})
}

func TestCLIAndTUIToolUpdateProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	runParityFlow(t, bin, parityFlow{
		seed:    seedParityScriptTool,
		runCLI:  runParityToolUpdateCLI,
		runTUI:  runParityToolUpdateTUI,
		observe: observeParityScriptTool,
		readTUI: readParityUpdatedToolThroughCLI,
	})
}

func TestCLIAndTUIDotsSyncProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	runParityFlow(t, bin, parityFlow{
		seed:    seedParityDotsMissingTarget,
		runCLI:  runParityDotsSyncCLI,
		runTUI:  runParityDotsSyncTUI,
		observe: observeParityDotsSynced,
		readTUI: readParityDotsThroughCLI,
	})
}

func TestCLIAndTUIDotsUseLocalProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	runParityFlow(t, bin, parityFlow{
		seed:    seedParityDotsConflict,
		runCLI:  runParityDotsUseLocalCLI,
		runTUI:  runParityDotsUseLocalTUI,
		observe: observeParityDots,
		readTUI: readParityDotsThroughCLI,
	})
}

func seedParityScriptTool(t *testing.T, sandbox *paritySandbox) {
	t.Helper()
	binDir := filepath.Join(sandbox.root, "bin")
	state := filepath.Join(sandbox.root, "provider-state")
	logPath := filepath.Join(sandbox.root, "provider.log")
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
	writeIntegrationFile(t, state, "1.0.0\n")
	sandbox.env = replaceIntegrationEnv(sandbox.env, "PATH", binDir+string(os.PathListSeparator)+integrationEnvValue(sandbox.env, "PATH"))
	sandbox.env = append(sandbox.env, "FAKE_PROVIDER_STATE="+state, "FAKE_PROVIDER_LOG="+logPath)
	writeIntegrationFile(t, sandbox.configPath, `{
  "settings":{"disabled_providers":["apt","apk","dnf","pacman","zypper","brew","node","bun","pnpm","npm","python","uv","pip"]},
  "tools":{"fixture":{"provider":"script","options":{"install":"fake-provider install","check":"fake-provider check","version":"fake-provider version","latest":"fake-provider latest","upgrade":"fake-provider upgrade","uninstall":"fake-provider uninstall"}}},
  "hosts":{"testhost":[]},
  "groups":[{"name":"testhost","special":"host","tools":["fixture"]}]
}`)
}

func refreshParityTool(t *testing.T, bin string, sandbox *paritySandbox) {
	t.Helper()
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"tools", "refresh")
}

func runParityToolDeleteCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	refreshParityTool(t, bin, sandbox)
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--yes", "--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"tools", "remove", "fixture", "--provider", "script", "--purge")
}

func runParityToolDeleteTUI(t *testing.T, bin string, sandbox *paritySandbox) {
	refreshParityTool(t, bin, sandbox)
	runParityToolTUIAction(t, bin, sandbox, "d", func() bool {
		_, err := os.Stat(filepath.Join(sandbox.root, "provider-state"))
		if !os.IsNotExist(err) {
			return false
		}
		cfg, err := config.Load(sandbox.configPath)
		if err != nil {
			return false
		}
		_, declared := cfg.Tools["fixture"]
		if declared {
			return false
		}
		db, err := database.Open(filepath.Join(sandbox.cache, "omni.db"))
		if err != nil {
			return false
		}
		defer db.Close()
		tool, err := db.Get(context.Background(), "fixture", "script", "fixture")
		return errors.Is(err, sql.ErrNoRows) || (err == nil && !tool.Installed)
	})
}

func runParityToolUpdateCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	refreshParityTool(t, bin, sandbox)
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"tools", "upgrade", "fixture", "--force")
}

func runParityToolUpdateTUI(t *testing.T, bin string, sandbox *paritySandbox) {
	refreshParityTool(t, bin, sandbox)
	runParityToolTUIAction(t, bin, sandbox, "u", func() bool {
		return parityScriptToolVersion(sandbox) == "1.1.0"
	})
}

func runParityToolTUIAction(t *testing.T, bin string, sandbox *paritySandbox, action string, done func() bool) {
	t.Helper()
	runTUI(t, bin, sandbox.root, sandbox.env, []string{"--config", sandbox.configPath, "--cache-dir", sandbox.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "fixture") && strings.Contains(text, "script")
		}, "TUI did not render the script tool")
		writeTUIKeys(t, term, "\t")
		waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "fixture") && strings.Contains(text, "script")
		}, "TUI tools tab did not render the script tool")
		writeTUIKeys(t, term, action)
		if action == "d" {
			waitForRequiredScreen(t, term, 3*time.Second, func(text string) bool {
				return strings.Contains(strings.ToLower(text), "confirm delete")
			}, "TUI did not arm tool deletion")
			writeTUIKeys(t, term, action)
		}
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if done() {
				return currentScreenText(term)
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("TUI tool action %q did not complete; screen:\n%s", action, currentScreenText(term))
		return ""
	})
}

func parityScriptToolVersion(sandbox *paritySandbox) string {
	db, err := database.Open(filepath.Join(sandbox.cache, "omni.db"))
	if err != nil {
		return ""
	}
	defer db.Close()
	tool, err := db.Get(context.Background(), "fixture", "script", "fixture")
	if err != nil || !tool.Installed {
		return ""
	}
	return tool.Version.String
}

type parityScriptToolState struct {
	Config             any
	Installed          bool
	Version            string
	Destructive        []string
	CachePresent       bool
	CacheInstalled     bool
	CacheProvider      string
	CachePackage       string
	CacheInstalledWith string
	CacheVersion       string
	CacheTracked       bool
}

func observeParityScriptTool(t *testing.T, sandbox *paritySandbox) any {
	t.Helper()
	state := parityScriptToolState{Config: normalizedParityConfig(t, sandbox)}
	if version, err := os.ReadFile(filepath.Join(sandbox.root, "provider-state")); err == nil {
		state.Installed = true
		state.Version = strings.TrimSpace(string(version))
	} else if !os.IsNotExist(err) {
		t.Fatalf("read script provider state: %v", err)
	}
	if raw, err := os.ReadFile(filepath.Join(sandbox.root, "provider.log")); err == nil {
		for _, line := range strings.Fields(string(raw)) {
			if line == "upgrade" || line == "uninstall" {
				state.Destructive = append(state.Destructive, line)
			}
		}
	} else if !os.IsNotExist(err) {
		t.Fatalf("read script provider log: %v", err)
	}
	db, err := database.Open(filepath.Join(sandbox.cache, "omni.db"))
	if err != nil {
		t.Fatalf("open script parity cache: %v", err)
	}
	defer db.Close()
	tool, err := db.Get(context.Background(), "fixture", "script", "fixture")
	if err == nil {
		state.CachePresent = true
		state.CacheInstalled = tool.Installed
		state.CacheProvider = tool.Provider
		state.CachePackage = tool.Package
		state.CacheInstalledWith = tool.InstalledWith
		state.CacheVersion = tool.Version.String
		state.CacheTracked = tool.Tracked
	} else if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("read script parity cache: %v", err)
	}
	return state
}

func readParityDeletedToolThroughCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	out := runOmniOutput(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"tools", "list", "fixture", "--format", "json")
	var tools []struct {
		Name      string `json:"name"`
		Group     string `json:"group"`
		Installed bool   `json:"installed"`
	}
	if err := json.Unmarshal([]byte(out), &tools); err != nil {
		t.Fatalf("decode CLI tool read over TUI state: %v", err)
	}
	if len(tools) > 1 || (len(tools) == 1 && (tools[0].Name != "fixture" || tools[0].Group != "-" || tools[0].Installed)) {
		t.Fatalf("CLI did not observe TUI removal from config: %#v", tools)
	}
	db, err := database.Open(filepath.Join(sandbox.cache, "omni.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tool, err := db.Get(context.Background(), "fixture", "script", "fixture")
	if err == nil && tool.Installed {
		t.Fatalf("CLI observed deleted tool as installed: %+v", tool)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("read deleted tool cache: %v", err)
	}
}

func readParityUpdatedToolThroughCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	out := runOmniOutput(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"tools", "list", "fixture", "--format", "json")
	if !strings.Contains(out, `"version":"1.1.0"`) {
		t.Fatalf("CLI did not observe TUI-updated version: %s", out)
	}
}

func seedParityDotsMissingTarget(t *testing.T, sandbox *paritySandbox) {
	seedParityDotsConflict(t, sandbox)
	if err := os.Remove(filepath.Join(sandbox.home, ".config", "nvim", "init.lua")); err != nil {
		t.Fatalf("remove initial dots target: %v", err)
	}
}

func runParityDotsSyncCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"dots", "sync", "nvim")
}

func runParityDotsSyncTUI(t *testing.T, bin string, sandbox *paritySandbox) {
	runParityDotsTUIAction(t, bin, sandbox, "s", "synced")
}

func runParityDotsUseLocalCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--yes", "--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"dots", "sync", "nvim", "--use-local")
}

func runParityDotsUseLocalTUI(t *testing.T, bin string, sandbox *paritySandbox) {
	runParityDotsTUIAction(t, bin, sandbox, "l", "confirm use local")
}

func runParityDotsTUIAction(t *testing.T, bin string, sandbox *paritySandbox, action, confirmation string) {
	t.Helper()
	runTUI(t, bin, sandbox.root, sandbox.env, []string{"--config", sandbox.configPath, "--cache-dir", sandbox.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, screenHas("Dashboard", "Tools"), "TUI did not render main tabs")
		writeTUIKeys(t, term, "\t", "\t")
		if action == "l" {
			waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
				lower := strings.ToLower(text)
				return strings.Contains(text, "nvim") && strings.Contains(lower, "conflict") && strings.Contains(lower, "sync: partial, sync failed")
			}, "TUI launch sync did not settle on the dots conflict")
			writeTUIKeys(t, term, "j")
			waitForRequiredScreen(t, term, 3*time.Second, func(text string) bool {
				return strings.Contains(text, "> ! • nvim") && strings.Contains(strings.ToLower(text), "l use local")
			}, "TUI did not select the actionable dots conflict")
		} else {
			waitForRequiredScreen(t, term, 8*time.Second, screenHas("nvim"), "TUI did not render the dots entry")
		}
		writeTUIKeys(t, term, action)
		if strings.HasPrefix(confirmation, "confirm") {
			waitForRequiredScreen(t, term, 3*time.Second, func(text string) bool {
				return strings.Contains(strings.ToLower(text), confirmation)
			}, "TUI did not arm dots resolution")
			writeTUIKeys(t, term, action)
		}
		return waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "nvim") && strings.Contains(strings.ToLower(text), "synced")
		}, "TUI did not complete dots action")
	})
}

type parityDotsSyncedState struct {
	Config        any
	RepoTree      string
	RepoStatus    string
	TargetLink    string
	TargetContent string
}

func observeParityDotsSynced(t *testing.T, sandbox *paritySandbox) any {
	t.Helper()
	repo := filepath.Join(sandbox.home, "dotfiles")
	target := filepath.Join(sandbox.home, ".config", "nvim", "init.lua")
	info, err := os.Lstat(target)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("dots target is not a symlink: %v, %v", info, err)
	}
	link, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("resolve dots target: %v", err)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read dots target: %v", err)
	}
	return parityDotsSyncedState{
		Config:        normalizedParityConfig(t, sandbox),
		RepoTree:      runCommandOutput(t, repo, sandbox.env, "git", "rev-parse", "HEAD^{tree}"),
		RepoStatus:    runCommandOutput(t, repo, sandbox.env, "git", "status", "--porcelain=v1"),
		TargetLink:    strings.ReplaceAll(link, sandbox.root, "$ROOT"),
		TargetContent: string(content),
	}
}
