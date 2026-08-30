//go:build integration

package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestCLIBinaryDotsEntryLifecyclePersistsFilesGroupsAndHistory(t *testing.T) {
	root, home, cache, env, configPath, repo := dotsActionsFixture(t, []*config.GroupConfig{
		{Name: "testhost", Special: "host"},
		{Name: "work"},
	}, []string{"work"})
	bin := buildOmniBinary(t)
	target := filepath.Join(home, ".config", "fixture")
	targetFile := filepath.Join(target, "settings.toml")
	writeIntegrationFile(t, targetFile, "managed = true\n")

	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "dots", "add", target, "--adopt", "--group", "testhost", "--name", "fixture")
	if info, err := os.Lstat(targetFile); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("adopted target is not a symlink: %v, %v", info, err)
	}
	repoFile := filepath.Join(repo, "dotfiles", "fixture", ".config", "fixture", "settings.toml")
	if raw, err := os.ReadFile(repoFile); err != nil || string(raw) != "managed = true\n" {
		t.Fatalf("adopted repo file = %q, %v", raw, err)
	}
	var listed []struct {
		Name string `json:"name"`
	}
	out := runOmniOutput(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "dots", "list", "fixture", "--format", "json")
	if err := json.Unmarshal([]byte(out), &listed); err != nil || len(listed) != 1 || listed[0].Name != "fixture" {
		t.Fatalf("dots list = %+v, %v\n%s", listed, err, out)
	}

	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "dots", "groups", "fixture", "--move", "work")
	if dot := dotsActionEntry(loadDotsActionsConfig(t, configPath), "work", "fixture"); dot == nil {
		t.Fatal("group move did not persist fixture in work")
	}
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "dots", "ignore", "fixture", "*.tmp")
	if dot := dotsActionEntry(loadDotsActionsConfig(t, configPath), "work", "fixture"); dot == nil || len(dot.Ignore) != 1 || dot.Ignore[0] != "*.tmp" {
		t.Fatalf("ignore pattern did not persist: %#v", dot)
	}
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "dots", "unignore", "fixture", "*.tmp")
	if dot := dotsActionEntry(loadDotsActionsConfig(t, configPath), "work", "fixture"); dot == nil || len(dot.Ignore) != 0 {
		t.Fatalf("ignore pattern remained: %#v", dot)
	}

	runOmniCommand(t, bin, root, env, "--yes", "--config", configPath, "--cache-dir", cache, "dots", "remove", "fixture")
	if info, err := os.Lstat(targetFile); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("kept local target = %v, %v", info, err)
	}
	if raw, err := os.ReadFile(targetFile); err != nil || string(raw) != "managed = true\n" {
		t.Fatalf("kept local content = %q, %v", raw, err)
	}
	if _, err := os.Stat(filepath.Dir(filepath.Dir(filepath.Dir(repoFile)))); !os.IsNotExist(err) {
		t.Fatalf("repo package survived delete: %v", err)
	}
	if dotsActionEntry(loadDotsActionsConfig(t, configPath), "work", "fixture") != nil {
		t.Fatal("deleted dot entry remained in config")
	}
	var history []struct {
		Operation string `json:"operation"`
		Status    string `json:"status"`
	}
	out = runOmniOutput(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "dots", "history", "--format", "json")
	if err := json.Unmarshal([]byte(out), &history); err != nil || !dotsActionHistoryHas(history, "add") || !dotsActionHistoryHas(history, "delete") {
		t.Fatalf("dots history = %+v, %v\n%s", history, err, out)
	}
}

func TestCLIBinaryDotsVariantLifecyclePersistsAndListsHostPackage(t *testing.T) {
	root, _, cache, env, configPath, repo := dotsActionsFixture(t, []*config.GroupConfig{{
		Name: "testhost", Special: "host", Dots: []config.DotEntry{{Name: "nvim", Path: filepath.Join(homePlaceholder, ".config", "nvim")}},
	}}, nil)
	writeIntegrationFile(t, filepath.Join(repo, "dotfiles", "nvim", ".config", "nvim", "init.lua"), "default\n")
	variantDir := filepath.Join(repo, "dotfiles", "nvim-work")
	writeIntegrationFile(t, filepath.Join(variantDir, ".config", "nvim", "init.lua"), "work\n")
	bin := buildOmniBinary(t)

	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "dots", "variant", "add", "nvim", "--host", "work.local", "--package", "nvim-work")
	dot := dotsActionEntry(loadDotsActionsConfig(t, configPath), "testhost", "nvim")
	if dot == nil || dot.Hosts["work"].Package != "nvim-work" {
		t.Fatalf("variant did not persist: %#v", dot)
	}
	out := runOmniOutput(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "dots", "variant", "list", "nvim", "--format", "json")
	if !strings.Contains(out, `"host":"work"`) || !strings.Contains(out, `"package":"nvim-work"`) {
		t.Fatalf("variant list omitted persisted package: %s", out)
	}

	runOmniCommand(t, bin, root, env, "--yes", "--config", configPath, "--cache-dir", cache, "dots", "variant", "remove", "nvim", "--host", "work")
	dot = dotsActionEntry(loadDotsActionsConfig(t, configPath), "testhost", "nvim")
	if dot == nil || len(dot.Hosts) != 0 {
		t.Fatalf("variant remained in config: %#v", dot)
	}
	if _, err := os.Stat(variantDir); !os.IsNotExist(err) {
		t.Fatalf("unused variant package survived removal: %v", err)
	}
}

func TestCLIBinaryDotsDisableEnableRoundTripPreservesLocalContent(t *testing.T) {
	root, home, cache, env, configPath, repo := dotsActionsFixture(t, []*config.GroupConfig{{
		Name: "testhost", Special: "host", Dots: []config.DotEntry{{Name: "fixture", Path: filepath.Join(homePlaceholder, ".config", "fixture")}},
	}}, nil)
	repoFile := filepath.Join(repo, "dotfiles", "fixture", ".config", "fixture", "settings.toml")
	writeIntegrationFile(t, repoFile, "managed = true\n")
	bin := buildOmniBinary(t)
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "dots", "sync")
	target := filepath.Join(home, ".config", "fixture")
	targetFile := filepath.Join(target, "settings.toml")

	runOmniCommand(t, bin, root, env, "--yes", "--config", configPath, "--cache-dir", cache, "dots", "disable")
	if info, err := os.Lstat(targetFile); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("disabled target is not local: %v, %v", info, err)
	}
	if content, err := os.ReadFile(targetFile); err != nil || string(content) != "managed = true\n" {
		t.Fatalf("disabled target content = %q, %v", content, err)
	}
	cfg := loadDotsActionsConfig(t, configPath)
	if disabled := cfg.HostSettings["testhost"].DotsDisabled; disabled == nil || !*disabled {
		t.Fatalf("dots_disabled after disable = %#v", disabled)
	}

	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "dots", "enable")
	if info, err := os.Lstat(targetFile); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("enabled target is not a symlink: %v, %v", info, err)
	}
	cfg = loadDotsActionsConfig(t, configPath)
	if disabled := cfg.HostSettings["testhost"].DotsDisabled; disabled == nil || *disabled {
		t.Fatalf("dots_disabled after enable = %#v", disabled)
	}
}

func TestCLIBinaryDotsCommitPersistsGitCommitAndHistory(t *testing.T) {
	root, _, cache, env, configPath, repo := dotsActionsFixture(t, []*config.GroupConfig{{Name: "testhost", Special: "host"}}, nil)
	writeIntegrationFile(t, filepath.Join(repo, "dotfiles", "fixture", ".config", "fixture", "settings.toml"), "changed\n")

	runOmniCommand(t, buildOmniBinary(t), root, env, "--config", configPath, "--cache-dir", cache, "dots", "commit", "--message", "dots: blackbox commit")
	if got := runCommandOutput(t, repo, env, "git", "log", "-1", "--pretty=%s"); got != "dots: blackbox commit" {
		t.Fatalf("commit subject = %q", got)
	}
	if got := runCommandOutput(t, repo, env, "git", "status", "--porcelain"); got != "" {
		t.Fatalf("dots repo remained dirty: %s", got)
	}
	out := runOmniOutput(t, buildOmniBinary(t), root, env, "--config", configPath, "--cache-dir", cache, "dots", "history", "--format", "json")
	if !strings.Contains(out, `"operation":"commit"`) || !strings.Contains(out, `"status":"success"`) {
		t.Fatalf("commit history missing: %s", out)
	}
}

func TestCLIBinaryDotsPullPushSynchronizesLocalBareRemote(t *testing.T) {
	root, home, cache, env := newCLIBinarySandbox(t)
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	repo := filepath.Join(home, "dotfiles")
	runCommand(t, root, env, "git", "init", "--bare", "--initial-branch=main", remote)
	runCommand(t, root, env, "git", "clone", remote, seed)
	dotsActionConfigureGit(t, seed, env)
	writeIntegrationFile(t, filepath.Join(seed, "dotfiles", "nvim", ".config", "nvim", "init.lua"), "seed\n")
	runCommand(t, seed, env, "git", "add", "dotfiles")
	runCommand(t, seed, env, "git", "commit", "-m", "seed")
	runCommand(t, seed, env, "git", "push", "-u", "origin", "main")
	runCommand(t, root, env, "git", "clone", remote, repo)
	dotsActionConfigureGit(t, repo, env)
	configPath := filepath.Join(root, "settings.json")
	if err := config.Save(configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{DotsRepo: repo},
		Hosts:    map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{{
			Name: "testhost", Special: "host", Dots: []config.DotEntry{{Name: "nvim", Path: filepath.Join(home, ".config", "nvim")}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	bin := buildOmniBinary(t)
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "dots", "sync")

	other := filepath.Join(root, "other")
	runCommand(t, root, env, "git", "clone", remote, other)
	dotsActionConfigureGit(t, other, env)
	remoteFile := filepath.Join(other, "dotfiles", "nvim", ".config", "nvim", "init.lua")
	writeIntegrationFile(t, remoteFile, "remote change\n")
	runCommand(t, other, env, "git", "add", "dotfiles")
	runCommand(t, other, env, "git", "commit", "-m", "remote change")
	runCommand(t, other, env, "git", "push")
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "dots", "pull")
	if raw, err := os.ReadFile(filepath.Join(home, ".config", "nvim", "init.lua")); err != nil || string(raw) != "remote change\n" {
		t.Fatalf("pulled local target = %q, %v", raw, err)
	}

	localFile := filepath.Join(repo, "dotfiles", "nvim", ".config", "nvim", "init.lua")
	writeIntegrationFile(t, localFile, "local change\n")
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "dots", "push", "--message", "dots: local change")
	verify := filepath.Join(root, "verify")
	runCommand(t, root, env, "git", "clone", remote, verify)
	if raw, err := os.ReadFile(filepath.Join(verify, "dotfiles", "nvim", ".config", "nvim", "init.lua")); err != nil || string(raw) != "local change\n" {
		t.Fatalf("pushed remote content = %q, %v", raw, err)
	}
}

const homePlaceholder = "/OMNI_TEST_HOME"

func dotsActionsFixture(t *testing.T, groups []*config.GroupConfig, hostGroups []string) (root, home, cache string, env []string, configPath, repo string) {
	t.Helper()
	root, home, cache, env = newCLIBinarySandbox(t)
	repo = filepath.Join(home, "dotfiles")
	initDotsRepo(t, repo, env)
	configPath = filepath.Join(root, "settings.json")
	for _, group := range groups {
		for i := range group.Dots {
			group.Dots[i].Path = strings.ReplaceAll(group.Dots[i].Path, homePlaceholder, home)
		}
	}
	if err := config.Save(configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{DotsRepo: repo},
		Hosts:    map[string][]string{"testhost": hostGroups},
		Groups:   groups,
	}); err != nil {
		t.Fatal(err)
	}
	return root, home, cache, env, configPath, repo
}

func loadDotsActionsConfig(t *testing.T, path string) *config.RootConfig {
	t.Helper()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func dotsActionEntry(cfg *config.RootConfig, groupName, entryName string) *config.DotEntry {
	for _, group := range cfg.Groups {
		if group == nil || group.BaseName() != groupName {
			continue
		}
		for i := range group.Dots {
			if group.Dots[i].Name == entryName {
				return &group.Dots[i]
			}
		}
	}
	return nil
}

func dotsActionHistoryHas(entries []struct {
	Operation string `json:"operation"`
	Status    string `json:"status"`
}, operation string) bool {
	for _, entry := range entries {
		if entry.Operation == operation && entry.Status == "success" {
			return true
		}
	}
	return false
}

func dotsActionConfigureGit(t *testing.T, repo string, env []string) {
	t.Helper()
	runCommand(t, repo, env, "git", "config", "user.email", "t@t.com")
	runCommand(t, repo, env, "git", "config", "user.name", "T")
	runCommand(t, repo, env, "git", "config", "commit.gpgsign", "false")
	runCommand(t, repo, env, "git", "config", "tag.gpgsign", "false")
	runCommand(t, repo, env, "git", "config", "core.hooksPath", "/dev/null")
}
