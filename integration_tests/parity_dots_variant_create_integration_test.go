//go:build integration

package integration_test

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
)

func TestCLIAndTUIDotsVariantCreateProduceEquivalentSemanticState(t *testing.T) {
	cli := batch19VariantFixture(t)
	tui := batch19VariantFixture(t)
	runOmniCommand(t, cli.bin, cli.root, cli.env,
		"--config", cli.configPath, "--cache-dir", cli.cache,
		"dots", "variant", "add", "nvim", "--host", "testhost", "--package", "nvim@testhost", "--sync")
	runTUI(t, tui.bin, tui.root, tui.env, []string{"--config", tui.configPath, "--cache-dir", tui.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 7*time.Second, screenHas("Dashboard", "Tools"), "TUI did not start")
		writeTUIKeys(t, term, "\t", "\t")
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("nvim", "synced"), "TUI did not render default dot entry")
		settleTUIDotsLaunch(t, term, "TUI launch dots sync did not settle before variant action")
		writeTUIKeys(t, term, "j")
		waitForRequiredScreen(t, term, 3*time.Second, screenHas(">", "nvim", "v variant"), "TUI did not select variant row")
		writeTUIKeys(t, term, "v")
		waitForRequiredScreen(t, term, 4*time.Second, screenHas("create host variant for", "nvim", "again to create variant"), "TUI did not arm variant creation")
		writeTUIKeys(t, term, "v")
		return waitForRequiredScreen(t, term, 12*time.Second, func(string) bool {
			return batch19VariantPersisted(tui.configPath, tui.repo, tui.target)
		}, "TUI did not create and activate host variant")
	})
	cliState := batch19ObserveVariant(t, cli)
	tuiState := batch19ObserveVariant(t, tui)
	if !reflect.DeepEqual(cliState, tuiState) {
		t.Fatalf("dots.variant semantic state differs\nCLI: %#v\nTUI: %#v", cliState, tuiState)
	}
}

type batch19VariantSandbox struct {
	bin, root, home, cache, configPath, repo, target string
	env                                              []string
}

type batch19VariantObservation struct {
	Dot       batch19DotDeclaration
	Repo      []batch19FileObservation
	Target    batch19TargetObservation
	GitStatus string
}

type batch19DotDeclaration struct {
	Name, Path, Package string
	Hosts               map[string]string
}

type batch19FileObservation struct {
	Path, Hash string
	Mode       fs.FileMode
}

type batch19TargetObservation struct {
	Symlink, Resolved, Hash string
}

func batch19VariantFixture(t *testing.T) batch19VariantSandbox {
	t.Helper()
	bin := batch16OmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	repo := filepath.Join(home, "dotfiles")
	target := filepath.Join(home, ".config", "nvim")
	env := isolatedTUIEnv(t, home, cache)
	initDotsRepo(t, repo, env)
	writeIntegrationFile(t, filepath.Join(repo, "dotfiles", "nvim", ".config", "nvim", "init.lua"), "default variant\n")
	runCommand(t, repo, env, "git", "add", ".")
	runCommand(t, repo, env, "git", "commit", "-m", "seed nvim")
	if err := config.Save(configPath, &config.RootConfig{
		Version: config.CurrentVersion, Settings: config.Settings{DotsRepo: repo}, Hosts: map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{{Name: "testhost", Special: "host", Dots: []config.DotEntry{{Name: "nvim", Path: target}}}},
	}); err != nil {
		t.Fatal(err)
	}
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "dots", "sync")
	return batch19VariantSandbox{bin: bin, root: root, home: home, cache: cache, configPath: configPath, repo: repo, target: target, env: env}
}

func batch19VariantPersisted(configPath, repo, target string) bool {
	cfg, err := config.Load(configPath)
	if err != nil {
		return false
	}
	dot := batch19FindDot(cfg, "nvim")
	if dot == nil || dot.Hosts["testhost"].Package != "nvim@testhost" {
		return false
	}
	if _, err := os.Stat(filepath.Join(repo, "dotfiles", "nvim@testhost", ".config", "nvim", "init.lua")); err != nil {
		return false
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(target, "init.lua"))
	return err == nil && filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(resolved)))) == "nvim@testhost"
}

func batch19ObserveVariant(t *testing.T, sandbox batch19VariantSandbox) batch19VariantObservation {
	t.Helper()
	cfg, err := config.Load(sandbox.configPath)
	if err != nil {
		t.Fatal(err)
	}
	dot := batch19FindDot(cfg, "nvim")
	if dot == nil {
		t.Fatal("nvim dot declaration missing")
	}
	hosts := make(map[string]string, len(dot.Hosts))
	for host, variant := range dot.Hosts {
		hosts[host] = variant.Package
	}
	repoTree := batch19RepoTree(t, filepath.Join(sandbox.repo, "dotfiles"))
	targetPath := filepath.Join(sandbox.target, "init.lua")
	link, err := os.Readlink(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	gitStatus := runCommandOutput(t, sandbox.repo, sandbox.env, "git", "status", "--short")
	return batch19VariantObservation{
		Dot:       batch19DotDeclaration{Name: dot.Name, Path: "~/.config/nvim", Package: dot.Package, Hosts: hosts},
		Repo:      repoTree,
		Target:    batch19TargetObservation{Symlink: filepath.Base(link), Resolved: batch19RepoRelative(t, sandbox.repo, resolved), Hash: fmt.Sprintf("%x", sha256.Sum256(raw))},
		GitStatus: gitStatus,
	}
}

func batch19FindDot(cfg *config.RootConfig, name string) *config.DotEntry {
	for _, group := range cfg.Groups {
		for i := range group.Dots {
			if group.Dots[i].Name == name {
				return &group.Dots[i]
			}
		}
	}
	return nil
}

func batch19RepoTree(t *testing.T, root string) []batch19FileObservation {
	t.Helper()
	var out []batch19FileObservation
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out = append(out, batch19FileObservation{Path: filepath.ToSlash(rel), Hash: fmt.Sprintf("%x", sha256.Sum256(raw)), Mode: info.Mode().Perm()})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func batch19RepoRelative(t *testing.T, repo, path string) string {
	t.Helper()
	rel, err := filepath.Rel(repo, path)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.ToSlash(rel)
}
