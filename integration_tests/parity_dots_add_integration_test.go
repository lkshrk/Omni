//go:build integration

package integration_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
)

func TestCLIAndTUIDotsAddProduceEquivalentSemanticState(t *testing.T) {
	cli, tui := batch21DotsAddFixture(t), batch21DotsAddFixture(t)
	runOmniCommand(t, cli.bin, cli.root, cli.env, "--config", cli.configPath, "--cache-dir", cli.cache, "dots", "add", cli.target, "--adopt", "--group", "testhost")
	runTUI(t, tui.bin, tui.root, tui.env, []string{"--config", tui.configPath, "--cache-dir", tui.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 7*time.Second, screenHas("Dashboard", "Tools"), "TUI did not start")
		writeTUIKeys(t, term, "\t", "\t")
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("fixture", "local-only", "a add"), "TUI did not settle on Dots")
		writeTUIKeys(t, term, "a")
		waitForRequiredScreen(t, term, 5*time.Second, screenHas("Add dotfile path"), "TUI did not open path picker")
		term.Paste(tui.target)
		waitForRequiredScreen(t, term, 3*time.Second, screenHas("home/.config/fixture", "enter pick"), "TUI did not accept pasted path")
		writeTUIKeys(t, term, "\r")
		waitForRequiredScreen(t, term, 5*time.Second, screenHas("Choose Group: ~/.config/fixture", "testhost"), "TUI did not render dot-add group choices")
		writeTUIKeys(t, term, "\r")
		return waitForRequiredScreen(t, term, 12*time.Second, func(string) bool { return batch21DotsAddPersisted(tui) }, "TUI did not adopt dot path")
	})
	if left, right := batch21ObserveDotsAdd(t, cli), batch21ObserveDotsAdd(t, tui); !reflect.DeepEqual(left, right) {
		t.Fatalf("dots.add semantic state differs\nCLI: %#v\nTUI: %#v", left, right)
	}
}

type batch21DotsAddSandbox struct {
	bin, root, cache, configPath, repo, target string
	env                                        []string
}

type batch21DotsAddObservation struct {
	Name, Path, Package string
	Groups              []string
	Repo                []batch19FileObservation
	Target              batch19TargetObservation
	GitStatus           string
}

func batch21DotsAddFixture(t *testing.T) batch21DotsAddSandbox {
	t.Helper()
	bin, root := batch16OmniBinary(t), t.TempDir()
	home := filepath.Join(root, "home")
	cache, configPath := filepath.Join(root, "cache"), filepath.Join(root, "settings.json")
	repo, target := filepath.Join(home, "dotfiles"), filepath.Join(home, ".config", "fixture")
	env := isolatedTUIEnv(t, home, cache)
	initDotsRepo(t, repo, env)
	writeIntegrationFile(t, filepath.Join(target, "settings.toml"), "managed = true\n")
	if err := config.Save(configPath, &config.RootConfig{Version: config.CurrentVersion, Settings: config.Settings{DotsRepo: repo}, Hosts: map[string][]string{"testhost": {}}, Groups: []*config.GroupConfig{{Name: "testhost", Special: "host"}}}); err != nil {
		t.Fatal(err)
	}
	return batch21DotsAddSandbox{bin: bin, root: root, cache: cache, configPath: configPath, repo: repo, target: target, env: env}
}

func batch21DotsAddPersisted(s batch21DotsAddSandbox) bool {
	cfg, err := config.Load(s.configPath)
	if err != nil {
		return false
	}
	dot, groups := batch21FindDot(cfg)
	info, linkErr := os.Lstat(filepath.Join(s.target, "settings.toml"))
	_, repoErr := os.Stat(filepath.Join(s.repo, "dotfiles", "fixture", ".config", "fixture", "settings.toml"))
	return dot != nil && len(groups) == 1 && groups[0] == "testhost" && linkErr == nil && info.Mode()&os.ModeSymlink != 0 && repoErr == nil
}

func batch21ObserveDotsAdd(t *testing.T, s batch21DotsAddSandbox) batch21DotsAddObservation {
	t.Helper()
	cfg, err := config.Load(s.configPath)
	if err != nil {
		t.Fatal(err)
	}
	dot, groups := batch21FindDot(cfg)
	if dot == nil {
		t.Fatal("fixture dot missing")
	}
	sort.Strings(groups)
	leaf := filepath.Join(s.target, "settings.toml")
	link, err := os.Readlink(leaf)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(leaf)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(leaf)
	if err != nil {
		t.Fatal(err)
	}
	return batch21DotsAddObservation{
		Name: dot.Name, Path: "~/.config/fixture", Package: dot.Package, Groups: groups,
		Repo:      batch19RepoTree(t, filepath.Join(s.repo, "dotfiles")),
		Target:    batch19TargetObservation{Symlink: filepath.Base(link), Resolved: batch19RepoRelative(t, s.repo, resolved), Hash: fmt.Sprintf("%x", sha256.Sum256(raw))},
		GitStatus: runCommandOutput(t, s.repo, s.env, "git", "status", "--short"),
	}
}

func batch21FindDot(cfg *config.RootConfig) (*config.DotEntry, []string) {
	var found *config.DotEntry
	var groups []string
	for _, group := range cfg.Groups {
		for i := range group.Dots {
			if group.Dots[i].Name == "fixture" {
				copy := group.Dots[i]
				found, groups = &copy, append(groups, group.BaseName())
			}
		}
	}
	return found, groups
}
