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

func TestCLIAndTUIDotsLifecycleIgnoreProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	runParityFlow(t, bin, parityFlow{
		seed:    seedParityDotsMissingTarget,
		runCLI:  runDotsLifecycleIgnoreCLI,
		runTUI:  runDotsLifecycleIgnoreTUI,
		observe: observeDotsLifecycleState,
		readTUI: readDotsLifecycleThroughCLI,
	})
}

func TestCLIAndTUIDotsLifecycleDeleteProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	runParityFlow(t, bin, parityFlow{
		seed:    seedParityDotsMissingTarget,
		runCLI:  runDotsLifecycleDeleteCLI,
		runTUI:  runDotsLifecycleDeleteTUI,
		observe: observeDotsLifecycleState,
		readTUI: readDotsLifecycleThroughCLI,
	})
}

func TestCLIAndTUIDotsLifecycleDisableProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	runParityFlow(t, bin, parityFlow{
		seed:    seedParityDotsMissingTarget,
		runCLI:  runDotsLifecycleDisableCLI,
		runTUI:  runDotsLifecycleDisableTUI,
		observe: observeDotsLifecycleState,
		readTUI: readDotsLifecycleThroughCLI,
	})
}

func TestCLIAndTUIDotsLifecycleEnableProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	runParityFlow(t, bin, parityFlow{
		seed:    seedParityDotsMissingTarget,
		runCLI:  runDotsLifecycleEnableCLI,
		runTUI:  runDotsLifecycleEnableTUI,
		observe: observeDotsLifecycleState,
		readTUI: readDotsLifecycleThroughCLI,
	})
}

func runDotsLifecycleIgnoreCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	prepareDotsLifecycleSynced(t, bin, sandbox)
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"dots", "ignore", "nvim")
}

func runDotsLifecycleIgnoreTUI(t *testing.T, bin string, sandbox *paritySandbox) {
	prepareDotsLifecycleSynced(t, bin, sandbox)
	runDotsLifecycleDotsTUI(t, bin, sandbox, func(term *vttest.Terminal) {
		writeTUIKeys(t, term, "x")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("confirm ignore", "nvim"), "TUI did not arm dots ignore")
		writeTUIKeys(t, term, "x")
	}, func(cfg *config.RootConfig) bool {
		entry := dotsLifecycleEntry(cfg, "nvim")
		return entry != nil && entry.Ignored
	})
}

func runDotsLifecycleDeleteCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	prepareDotsLifecycleSynced(t, bin, sandbox)
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--yes", "--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"dots", "remove", "nvim", "--keep-local")
}

func runDotsLifecycleDeleteTUI(t *testing.T, bin string, sandbox *paritySandbox) {
	prepareDotsLifecycleSynced(t, bin, sandbox)
	runDotsLifecycleDotsTUI(t, bin, sandbox, func(term *vttest.Terminal) {
		sendDotsLifecycleKeyUntil(t, term, "d", screenHas("keep local?", "yes", "no"), "TUI did not arm dots delete")
		writeTUIKeys(t, term, "y")
	}, func(cfg *config.RootConfig) bool {
		return dotsLifecycleEntry(cfg, "nvim") == nil
	})
}

func runDotsLifecycleDisableCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	prepareDotsLifecycleSynced(t, bin, sandbox)
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--yes", "--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"dots", "disable")
}

func runDotsLifecycleDisableTUI(t *testing.T, bin string, sandbox *paritySandbox) {
	prepareDotsLifecycleSynced(t, bin, sandbox)
	runDotsLifecycleSettingsTUI(t, bin, sandbox, func(term *vttest.Terminal) {
		writeTUIKeys(t, term, "\r")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("disable dotfile sync", "keep local?", "yes", "no"), "TUI did not arm dots disable")
		writeTUIKeys(t, term, "y")
	}, func(cfg *config.RootConfig) bool {
		return dotsLifecycleDisabled(cfg)
	})
}

func runDotsLifecycleEnableCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	prepareDotsLifecycleDisabled(t, bin, sandbox)
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"dots", "enable")
}

func runDotsLifecycleEnableTUI(t *testing.T, bin string, sandbox *paritySandbox) {
	prepareDotsLifecycleDisabled(t, bin, sandbox)
	runDotsLifecycleSettingsTUI(t, bin, sandbox, func(term *vttest.Terminal) {
		writeTUIKeys(t, term, "\r")
	}, func(cfg *config.RootConfig) bool {
		target := filepath.Join(sandbox.home, ".config", "nvim", "init.lua")
		content, err := os.ReadFile(target)
		staging, globErr := filepath.Glob(filepath.Join(sandbox.home, "dotfiles", ".omni-newer-*"))
		return !dotsLifecycleDisabled(cfg) && dotsLifecycleTargetIsSymlink(sandbox) &&
			err == nil && string(content) == "repo version\n" && globErr == nil && len(staging) == 0 &&
			runCommandOutput(t, filepath.Join(sandbox.home, "dotfiles"), sandbox.env, "git", "status", "--porcelain=v1") == ""
	})
}

func prepareDotsLifecycleSynced(t *testing.T, bin string, sandbox *paritySandbox) {
	t.Helper()
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"dots", "sync", "nvim")
}

func prepareDotsLifecycleDisabled(t *testing.T, bin string, sandbox *paritySandbox) {
	t.Helper()
	prepareDotsLifecycleSynced(t, bin, sandbox)
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--yes", "--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"dots", "disable")
}

func runDotsLifecycleDotsTUI(t *testing.T, bin string, sandbox *paritySandbox, act func(*vttest.Terminal), done func(*config.RootConfig) bool) {
	t.Helper()
	runTUI(t, bin, sandbox.root, sandbox.env, []string{"--config", sandbox.configPath, "--cache-dir", sandbox.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, screenHas("Dashboard", "Dots"), "TUI did not render main tabs")
		writeTUIKeys(t, term, "\t", "\t")
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("nvim"), "TUI did not render dots entry")
		sendDotsLifecycleKeyUntil(t, term, "j", func(text string) bool {
			return strings.Contains(text, ">") && strings.Contains(text, "nvim")
		}, "TUI did not select dots entry")
		act(term)
		return waitForRequiredScreen(t, term, 10*time.Second, func(string) bool {
			cfg, err := config.Load(sandbox.configPath)
			return err == nil && done(cfg)
		}, "TUI dots lifecycle action did not persist")
	})
}

func runDotsLifecycleSettingsTUI(t *testing.T, bin string, sandbox *paritySandbox, act func(*vttest.Terminal), done func(*config.RootConfig) bool) {
	t.Helper()
	runTUI(t, bin, sandbox.root, sandbox.env, []string{"--config", sandbox.configPath, "--cache-dir", sandbox.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, screenHas("Dashboard", "Settings"), "TUI did not render main tabs")
		writeTUIKeys(t, term, "\t", "\t", "\t", "\t", "\t")
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("Provider Priority", "Dotfile Sync"), "TUI did not render settings")
		for _, row := range []string{"Import Installed Tools", "Provider Priority", "Repository", "Dotfile Sync"} {
			sendDotsLifecycleKeyUntil(t, term, "j", screenHas("> "+row), "TUI did not select "+row)
		}
		act(term)
		return waitForRequiredScreen(t, term, 12*time.Second, func(string) bool {
			cfg, err := config.Load(sandbox.configPath)
			return err == nil && done(cfg)
		}, "TUI dots settings action did not persist")
	})
}

func sendDotsLifecycleKeyUntil(t *testing.T, term *vttest.Terminal, key string, ready func(string) bool, message string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		writeTUIKeys(t, term, key)
		if _, ok := waitForScreen(term, 500*time.Millisecond, ready); ok {
			return
		}
	}
	t.Fatalf("%s; screen:\n%s", message, currentScreenText(term))
}

func dotsLifecycleDisabled(cfg *config.RootConfig) bool {
	settings := cfg.EffectiveSettings("testhost")
	return settings.DotsDisabled != nil && *settings.DotsDisabled
}

func dotsLifecycleTargetIsSymlink(sandbox *paritySandbox) bool {
	info, err := os.Lstat(filepath.Join(sandbox.home, ".config", "nvim", "init.lua"))
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

func dotsLifecycleEntry(cfg *config.RootConfig, name string) *config.DotEntry {
	for _, group := range cfg.Groups {
		if group == nil {
			continue
		}
		for i := range group.Dots {
			if group.Dots[i].Name == name {
				return &group.Dots[i]
			}
		}
	}
	return nil
}

type dotsLifecycleState struct {
	Config        any
	RepoTree      string
	RepoStatus    string
	SourceExists  bool
	SourceContent string
	TargetExists  bool
	TargetKind    string
	TargetLink    string
	TargetContent string
}

func observeDotsLifecycleState(t *testing.T, sandbox *paritySandbox) any {
	t.Helper()
	repo := filepath.Join(sandbox.home, "dotfiles")
	source := filepath.Join(repo, "dotfiles", "nvim", ".config", "nvim", "init.lua")
	target := filepath.Join(sandbox.home, ".config", "nvim", "init.lua")
	state := dotsLifecycleState{
		Config:     normalizedParityConfig(t, sandbox),
		RepoTree:   runCommandOutput(t, repo, sandbox.env, "git", "rev-parse", "HEAD^{tree}"),
		RepoStatus: runCommandOutput(t, repo, sandbox.env, "git", "status", "--porcelain=v1"),
	}
	if raw, err := os.ReadFile(source); err == nil {
		state.SourceExists = true
		state.SourceContent = string(raw)
	} else if !os.IsNotExist(err) {
		t.Fatalf("read dots source: %v", err)
	}
	if info, err := os.Lstat(target); err == nil {
		state.TargetExists = true
		state.TargetKind = info.Mode().Type().String()
		if info.Mode()&os.ModeSymlink != 0 {
			link, linkErr := filepath.EvalSymlinks(target)
			if linkErr != nil {
				t.Fatalf("resolve dots target: %v", linkErr)
			}
			state.TargetLink = strings.ReplaceAll(link, sandbox.root, "$ROOT")
		}
		raw, readErr := os.ReadFile(target)
		if readErr != nil {
			t.Fatalf("read dots target: %v", readErr)
		}
		state.TargetContent = string(raw)
	} else if !os.IsNotExist(err) {
		t.Fatalf("lstat dots target: %v", err)
	}
	return state
}

func readDotsLifecycleThroughCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"dots", "list", "--format", "json")
}
