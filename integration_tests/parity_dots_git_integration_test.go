//go:build integration

package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
)

func TestCLIAndTUIDotsGitCommitProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	runParityFlow(t, bin, parityFlow{
		seed:    seedDotsGitCommitParity,
		runCLI:  runDotsGitCommitParityCLI,
		runTUI:  runDotsGitCommitParityTUI,
		observe: observeDotsGitCommitParity,
		readTUI: readDotsGitCommitThroughCLI,
	})
}

func seedDotsGitCommitParity(t *testing.T, sandbox *paritySandbox) {
	t.Helper()
	remote := filepath.Join(sandbox.root, "remote.git")
	seed := filepath.Join(sandbox.root, "seed")
	repo := filepath.Join(sandbox.home, "dotfiles")
	runCommand(t, sandbox.root, sandbox.env, "git", "init", "--bare", "--initial-branch=main", remote)
	runCommand(t, sandbox.root, sandbox.env, "git", "clone", remote, seed)
	dotsActionConfigureGit(t, seed, sandbox.env)
	writeIntegrationFile(t, filepath.Join(seed, "dotfiles", "nvim", ".config", "nvim", "init.lua"), "seed\n")
	runCommand(t, seed, sandbox.env, "git", "add", "dotfiles")
	runCommand(t, seed, sandbox.env, "git", "commit", "-m", "seed")
	runCommand(t, seed, sandbox.env, "git", "push", "-u", "origin", "main")
	runCommand(t, sandbox.root, sandbox.env, "git", "clone", remote, repo)
	dotsActionConfigureGit(t, repo, sandbox.env)
	if err := config.Save(sandbox.configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{DotsRepo: repo},
		Hosts:    map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{{
			Name: "testhost", Special: "host",
			Dots: []config.DotEntry{{Name: "nvim", Path: filepath.Join(sandbox.home, ".config", "nvim", "init.lua")}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	writeIntegrationFile(t, filepath.Join(repo, "dotfiles", "nvim", ".config", "nvim", "init.lua"), "changed\n")
}

func runDotsGitCommitParityCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"dots", "commit")
}

func runDotsGitCommitParityTUI(t *testing.T, bin string, sandbox *paritySandbox) {
	runTUI(t, bin, sandbox.root, sandbox.env, []string{"--config", sandbox.configPath, "--cache-dir", sandbox.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, screenHas("Dashboard", "Dots"), "TUI did not start")
		writeTUIKeys(t, term, "\t", "\t")
		waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "nvim") && strings.Contains(strings.ToLower(text), "dirty")
		}, "TUI did not render dirty dots repo")
		sendDotsGitKeyUntil(t, term, "C", func(string) bool {
			return runCommandOutput(t, filepath.Join(sandbox.home, "dotfiles"), sandbox.env, "git", "status", "--porcelain") == ""
		}, "TUI did not commit dots")
		return currentScreenText(term)
	})
}

func sendDotsGitKeyUntil(t *testing.T, term *vttest.Terminal, key string, ready func(string) bool, message string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		writeTUIKeys(t, term, key)
		if _, ok := waitForScreen(term, 700*time.Millisecond, ready); ok {
			return
		}
	}
	t.Fatalf("%s; screen:\n%s", message, currentScreenText(term))
}

type dotsGitCommitState struct {
	Config        any
	Tree          string
	Subject       string
	Status        string
	RemoteTree    string
	RemoteSubject string
	SourceContent string
}

func observeDotsGitCommitParity(t *testing.T, sandbox *paritySandbox) any {
	t.Helper()
	repo := filepath.Join(sandbox.home, "dotfiles")
	remote := filepath.Join(sandbox.root, "remote.git")
	source := filepath.Join(repo, "dotfiles", "nvim", ".config", "nvim", "init.lua")
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	return dotsGitCommitState{
		Config:        normalizedParityConfig(t, sandbox),
		Tree:          runCommandOutput(t, repo, sandbox.env, "git", "rev-parse", "HEAD^{tree}"),
		Subject:       runCommandOutput(t, repo, sandbox.env, "git", "log", "-1", "--pretty=%s"),
		Status:        runCommandOutput(t, repo, sandbox.env, "git", "status", "--porcelain=v1"),
		RemoteTree:    runCommandOutput(t, remote, sandbox.env, "git", "rev-parse", "refs/heads/main^{tree}"),
		RemoteSubject: runCommandOutput(t, remote, sandbox.env, "git", "log", "-1", "--pretty=%s", "refs/heads/main"),
		SourceContent: string(raw),
	}
}

func readDotsGitCommitThroughCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"dots", "history", "--format", "json")
}
