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

func TestCLIAndTUIToolsSyncPaletteProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	runParityFlow(t, bin, parityFlow{
		seed: seedParityToolInstall,
		runCLI: func(t *testing.T, bin string, s *paritySandbox) {
			runOmniCommand(t, bin, s.root, s.env, "--config", s.configPath, "--cache-dir", s.cache, "tools", "sync")
		},
		runTUI: func(t *testing.T, bin string, s *paritySandbox) {
			runPaletteTUI(t, bin, s, "tools sync", func() bool { return parityToolInstalled(s) })
		},
		observe: observeParityToolInstall,
		readTUI: readParityToolThroughCLI,
	})
}

func TestCLIAndTUIDotsPullPaletteProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	runParityFlow(t, bin, parityFlow{
		seed: seedPaletteDotsPull,
		runCLI: func(t *testing.T, bin string, s *paritySandbox) {
			runOmniCommand(t, bin, s.root, s.env, "--config", s.configPath, "--cache-dir", s.cache, "dots", "pull")
		},
		runTUI: func(t *testing.T, bin string, s *paritySandbox) {
			runPaletteTUI(t, bin, s, "dots pull", func() bool { return paletteDotsHeadsMatch(t, s) })
		},
		observe: observePaletteDotsState,
		readTUI: readPaletteDotsThroughCLI,
	})
}

func seedPaletteDotsPull(t *testing.T, s *paritySandbox) {
	t.Helper()
	seedDotsGitCommitParity(t, s)
	repo, remote := filepath.Join(s.home, "dotfiles"), filepath.Join(s.root, "remote.git")
	runCommand(t, repo, s.env, "git", "checkout", "--", ".")
	other := filepath.Join(s.root, "other")
	runCommand(t, s.root, s.env, "git", "clone", remote, other)
	dotsActionConfigureGit(t, other, s.env)
	writeIntegrationFile(t, filepath.Join(other, "dotfiles", "nvim", ".config", "nvim", "init.lua"), "remote change\n")
	runCommand(t, other, s.env, "git", "add", "dotfiles")
	runCommand(t, other, s.env, "git", "commit", "-m", "remote change")
	runCommand(t, other, s.env, "git", "push")
}

func runPaletteTUI(t *testing.T, bin string, s *paritySandbox, command string, done func() bool) {
	t.Helper()
	runTUI(t, bin, s.root, s.env, []string{"--config", s.configPath, "--cache-dir", s.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, screenHas("Dashboard", "Tools"), "TUI did not start")
		runTUICommandPalette(t, term, command)
		return waitForRequiredScreen(t, term, 10*time.Second, func(_ string) bool { return done() }, "TUI palette command did not converge: "+command)
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

type paletteDotsState struct {
	Config                                                                         any
	Tree, Subject, Status, RemoteTree, RemoteSubject, SourceContent, TargetContent string
	TargetSymlink                                                                  bool
}

func observePaletteDotsState(t *testing.T, s *paritySandbox) any {
	t.Helper()
	repo, remote := filepath.Join(s.home, "dotfiles"), filepath.Join(s.root, "remote.git")
	source := filepath.Join(repo, "dotfiles", "nvim", ".config", "nvim", "init.lua")
	target := filepath.Join(s.home, ".config", "nvim", "init.lua")
	sourceContent, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	targetContent, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	return paletteDotsState{
		Config: normalizedParityConfig(t, s),
		Tree:   runCommandOutput(t, repo, s.env, "git", "rev-parse", "HEAD^{tree}"), Subject: runCommandOutput(t, repo, s.env, "git", "log", "-1", "--pretty=%s"), Status: runCommandOutput(t, repo, s.env, "git", "status", "--porcelain=v1"),
		RemoteTree: runCommandOutput(t, remote, s.env, "git", "rev-parse", "refs/heads/main^{tree}"), RemoteSubject: runCommandOutput(t, remote, s.env, "git", "log", "-1", "--pretty=%s", "refs/heads/main"),
		SourceContent: string(sourceContent), TargetContent: string(targetContent), TargetSymlink: info.Mode()&os.ModeSymlink != 0,
	}
}

func paletteDotsHeadsMatch(t *testing.T, s *paritySandbox) bool {
	t.Helper()
	repo, remote := filepath.Join(s.home, "dotfiles"), filepath.Join(s.root, "remote.git")
	return runCommandOutput(t, repo, s.env, "git", "rev-parse", "HEAD") == runCommandOutput(t, remote, s.env, "git", "rev-parse", "refs/heads/main")
}

func readPaletteDotsThroughCLI(t *testing.T, bin string, s *paritySandbox) {
	t.Helper()
	runOmniCommand(t, bin, s.root, s.env, "--config", s.configPath, "--cache-dir", s.cache, "dots", "status", "--format", "json")
}
