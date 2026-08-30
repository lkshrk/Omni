//go:build integration

package integration_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vttest"
)

func TestTUIToolsSyncPaletteMatchesCLIProviderState(t *testing.T) {
	bin := buildOmniBinary(t)
	sandbox := newParitySandbox(t, t.TempDir())
	seedParityToolInstall(t, sandbox)
	runTUI(t, bin, sandbox.root, sandbox.env, []string{"--config", sandbox.configPath, "--cache-dir", sandbox.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, screenHas("Dashboard", "Tools"), "TUI did not start")
		runTUICommandPalette(t, term, "tools sync")
		return waitForRequiredScreen(t, term, 10*time.Second, func(_ string) bool { return parityToolInstalled(sandbox) }, "TUI palette sync did not install the configured tool")
	})
}

func TestTUIDotsPullPaletteMatchesRemoteState(t *testing.T) {
	bin := buildOmniBinary(t)
	sandbox := newParitySandbox(t, t.TempDir())
	seedDotsGitCommitParity(t, sandbox)
	repo := filepath.Join(sandbox.home, "dotfiles")
	remote := filepath.Join(sandbox.root, "remote.git")
	runCommand(t, repo, sandbox.env, "git", "checkout", "--", ".")
	other := filepath.Join(sandbox.root, "other")
	runCommand(t, sandbox.root, sandbox.env, "git", "clone", remote, other)
	dotsActionConfigureGit(t, other, sandbox.env)
	remoteFile := filepath.Join(other, "dotfiles", "nvim", ".config", "nvim", "init.lua")
	writeIntegrationFile(t, remoteFile, "remote change\n")
	runCommand(t, other, sandbox.env, "git", "add", "dotfiles")
	runCommand(t, other, sandbox.env, "git", "commit", "-m", "remote change")
	runCommand(t, other, sandbox.env, "git", "push")
	target := filepath.Join(sandbox.home, ".config", "nvim", "init.lua")
	runTUI(t, bin, sandbox.root, sandbox.env, []string{"--config", sandbox.configPath, "--cache-dir", sandbox.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, screenHas("Dashboard", "Dots"), "TUI did not start")
		runTUICommandPalette(t, term, "dots pull")
		return waitForRequiredScreen(t, term, 10*time.Second, func(_ string) bool {
			content, err := os.ReadFile(target)
			return err == nil && string(content) == "remote change\n" && runCommandOutput(t, repo, sandbox.env, "git", "rev-parse", "HEAD") == runCommandOutput(t, remote, sandbox.env, "git", "rev-parse", "refs/heads/main")
		}, "TUI palette pull did not converge to remote state")
	})
}

func TestTUIDotsPushPaletteMatchesRemoteState(t *testing.T) {
	bin := buildOmniBinary(t)
	sandbox := newParitySandbox(t, t.TempDir())
	seedDotsGitCommitParity(t, sandbox)
	repo := filepath.Join(sandbox.home, "dotfiles")
	remote := filepath.Join(sandbox.root, "remote.git")
	runTUI(t, bin, sandbox.root, sandbox.env, []string{"--config", sandbox.configPath, "--cache-dir", sandbox.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, screenHas("Dashboard", "Dots"), "TUI did not start")
		runTUICommandPalette(t, term, "dots push")
		return waitForRequiredScreen(t, term, 10*time.Second, func(_ string) bool {
			return runCommandOutput(t, repo, sandbox.env, "git", "status", "--porcelain") == "" && runCommandOutput(t, repo, sandbox.env, "git", "rev-parse", "HEAD") == runCommandOutput(t, remote, sandbox.env, "git", "rev-parse", "refs/heads/main")
		}, "TUI palette push did not converge the remote")
	})
}

func runTUICommandPalette(t *testing.T, term *vttest.Terminal, command string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		sendTUIKey(term, ':')
		if _, ok := waitForScreen(term, 500*time.Millisecond, screenHas(command)); ok {
			break
		}
	}
	waitForRequiredScreen(t, term, time.Second, screenHas(command), "TUI did not open the command palette")
	writeTUIKeys(t, term, command)
	waitForRequiredScreen(t, term, 3*time.Second, screenHas(": > "+command), "TUI did not filter the command palette")
	sendTUIKey(term, uv.KeyEnter)
}
