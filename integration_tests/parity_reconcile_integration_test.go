//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

func TestCLIAndTUIReconcileProduceEquivalentCompositeState(t *testing.T) {
	if _, err := exec.LookPath("apm"); err != nil {
		t.Fatalf("integration tests require apm on PATH: %v", err)
	}
	runParityFlow(t, buildOmniBinary(t), parityFlow{
		seed: seedReconcileParity, runCLI: runReconcileParityCLI, runTUI: runReconcileParityTUI,
		observe: observeReconcileParity, readTUI: readReconcileParityThroughCLI,
	})
}

func seedReconcileParity(t *testing.T, sandbox *paritySandbox) {
	t.Helper()
	paths := seedReconcileBinaryFixture(t, sandbox.root, sandbox.home, sandbox.configPath, &sandbox.env, true)
	writeIntegrationFile(t, paths.state, "1.0.0\n")
	source := filepath.Join(paths.repo, "dotfiles", "nvim", ".config", "nvim", "init.lua")
	target := filepath.Join(sandbox.home, ".config", "nvim", "init.lua")
	writeIntegrationFile(t, source, "reconciled config\n")
	cfg := loadConfigActions(t, sandbox.configPath)
	cfg.Groups[0].Dots[0].Path = target
	if err := config.Save(sandbox.configPath, cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, target); err != nil {
		t.Fatal(err)
	}
	writeIntegrationFile(t, filepath.Join(sandbox.home, ".apm", "apm.yml"), `name: omni-parity
version: 1.0.0
targets: [codex]
dependencies:
  apm: []
  mcp:
    - name: omni-parity
      registry: false
      transport: http
      url: https://example.invalid/parity
`)
	realAPM, err := exec.LookPath("apm")
	if err != nil || strings.ContainsAny(realAPM, "'\n") {
		t.Fatalf("resolve real apm for reconcile fixture: %q, %v", realAPM, err)
	}
	apmLog := filepath.Join(sandbox.root, "apm.log")
	writeExecutable(t, filepath.Join(sandbox.root, "bin", "apm"), "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '"+apmLog+"'\nexec '"+realAPM+"' \"$@\"\n")
}

func runReconcileParityCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env, "--yes", "--config", sandbox.configPath, "--cache-dir", sandbox.cache, "reconcile", "--skip-privileged")
}

func runReconcileParityTUI(t *testing.T, bin string, sandbox *paritySandbox) {
	runTUI(t, bin, sandbox.root, sandbox.env, []string{"--config", sandbox.configPath, "--cache-dir", sandbox.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("Dashboard", "Tools"), "TUI did not start for reconcile parity")
		waitForRequiredScreen(t, term, 15*time.Second, func(text string) bool {
			lower := strings.ToLower(text)
			return strings.Contains(text, "fixture (1.1.0)") &&
				!strings.Contains(lower, "scanning") && !strings.Contains(lower, "refreshing") && !strings.Contains(lower, "checking")
		}, "TUI reconcile inputs did not settle")
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			writeTUIKeys(t, term, "A")
			plan, open := waitForScreen(term, 700*time.Millisecond, func(text string) bool { return strings.Contains(text, "Reconcile Plan") })
			if open && screenHas("Upgrade tools", "Sync agents", "Commit dotfiles")(plan) {
				writeTUIKeys(t, term, "\r")
				completed, done := waitForScreen(term, 30*time.Second, func(text string) bool {
					return reconcileParityDone(sandbox) && strings.Contains(strings.ToLower(text), "reconciled")
				})
				if !done {
					t.Fatalf("TUI did not complete composite reconcile (%s); screen:\n%s", reconcileParityProgress(sandbox), completed)
				}
				return completed
			}
			if open {
				writeTUIKeys(t, term, "\x1b")
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("TUI did not expose reconcile upgrade and backup steps; screen:\n%s", currentScreenText(term))
		return ""
	})
}

func reconcileParityProgress(sandbox *paritySandbox) string {
	state, _ := os.ReadFile(filepath.Join(sandbox.root, "provider-state"))
	checks := []string{"provider=" + strings.TrimSpace(string(state))}
	for name, path := range map[string]string{
		"lock": filepath.Join(sandbox.home, ".apm", "apm.lock.yaml"), "codex": filepath.Join(sandbox.home, ".codex", "config.toml"),
	} {
		_, err := os.Stat(path)
		checks = append(checks, name+"="+errorText(err))
	}
	link, linkErr := os.Readlink(filepath.Join(sandbox.home, ".config", "nvim", "init.lua"))
	checks = append(checks, "link="+link+":"+errorText(linkErr))
	cmd := exec.Command("git", "-C", filepath.Join(sandbox.home, "dotfiles"), "rev-parse", "--verify", "omni/backup")
	cmd.Env = sandbox.env
	_, backupErr := cmd.CombinedOutput()
	checks = append(checks, "backup="+errorText(backupErr))
	return strings.Join(checks, " ")
}

func errorText(err error) string {
	if err == nil {
		return "ok"
	}
	return err.Error()
}

func reconcileParityDone(sandbox *paritySandbox) bool {
	state, err := os.ReadFile(filepath.Join(sandbox.root, "provider-state"))
	if err != nil || strings.TrimSpace(string(state)) == "" {
		return false
	}
	for _, path := range []string{filepath.Join(sandbox.home, ".apm", "apm.lock.yaml"), filepath.Join(sandbox.home, ".codex", "config.toml")} {
		if _, err := os.Stat(path); err != nil {
			return false
		}
	}
	if info, err := os.Lstat(filepath.Join(sandbox.home, ".config", "nvim", "init.lua")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		return false
	}
	cmd := exec.Command("git", "-C", filepath.Join(sandbox.home, "dotfiles"), "rev-parse", "--verify", "omni/backup")
	cmd.Env = sandbox.env
	return cmd.Run() == nil
}

type reconcileParityState struct {
	Config any
	Tool   struct {
		Installed                      bool
		InstalledWith, Version, Latest string
		Outdated, Tracked              bool
	}
	APM    agentsSyncState
	APMRun string
	Dot    struct{ Kind, Target, Content, RepoTree, Status string }
	Backup struct{ Tree, Subject string }
}

func observeReconcileParity(t *testing.T, sandbox *paritySandbox) any {
	t.Helper()
	db, err := database.Open(filepath.Join(sandbox.cache, "omni.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tool, err := db.Get(context.Background(), "fixture", "script", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(sandbox.home, ".config", "nvim", "init.lua")
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(sandbox.home, "dotfiles")
	state := reconcileParityState{Config: normalizedParityConfig(t, sandbox), APM: observeAgentsSyncParity(t, sandbox)}
	if raw, err := os.ReadFile(filepath.Join(sandbox.root, "apm.log")); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(line, "install -g") {
				state.APMRun = line
				break
			}
		}
	}
	if state.APMRun == "" {
		t.Fatal("reconcile did not invoke APM install")
	}
	state.Tool.Installed, state.Tool.InstalledWith = tool.Installed, tool.InstalledWith
	state.Tool.Version, state.Tool.Latest = reconcileNullString(tool.Version), reconcileNullString(tool.LatestVersion)
	state.Tool.Outdated, state.Tool.Tracked = tool.Outdated, tool.Tracked
	state.Dot.Kind, state.Dot.Target, state.Dot.Content = info.Mode().Type().String(), strings.ReplaceAll(resolved, sandbox.root, "$ROOT"), string(content)
	state.Dot.RepoTree = runCommandOutput(t, repo, sandbox.env, "git", "rev-parse", "HEAD^{tree}")
	state.Dot.Status = runCommandOutput(t, repo, sandbox.env, "git", "status", "--porcelain=v1")
	state.Backup.Tree = runCommandOutput(t, repo, sandbox.env, "git", "rev-parse", "omni/backup^{tree}")
	state.Backup.Subject = runCommandOutput(t, repo, sandbox.env, "git", "show", "-s", "--format=%s", "omni/backup")
	return state
}

func reconcileNullString(value sql.NullString) string {
	if value.Valid {
		return value.String
	}
	return ""
}

func readReconcileParityThroughCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	tools := runOmniOutput(t, bin, sandbox.root, sandbox.env, "--config", sandbox.configPath, "--cache-dir", sandbox.cache, "tools", "list", "fixture", "--format", "json")
	if !strings.Contains(tools, `"name":"fixture"`) {
		t.Fatalf("CLI did not observe TUI reconciled tool: %s", tools)
	}
	if agents := runOmniOutput(t, bin, sandbox.root, sandbox.env, "--config", sandbox.configPath, "--cache-dir", sandbox.cache, "agents", "targets"); !strings.Contains(strings.ToLower(agents), "codex") {
		t.Fatalf("CLI did not observe TUI reconciled APM state: %s", agents)
	}
	if dots := runOmniOutput(t, bin, sandbox.root, sandbox.env, "--config", sandbox.configPath, "--cache-dir", sandbox.cache, "dots", "status", "nvim", "--format", "json"); !strings.Contains(dots, "synced") {
		t.Fatalf("CLI did not observe TUI reconciled dot: %s", dots)
	}
}
