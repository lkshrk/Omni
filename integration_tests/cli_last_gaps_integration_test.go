//go:build integration

package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/config"
)

func TestCLIBinaryDotsResolveAllUseRepoRepairsEveryConflict(t *testing.T) {
	root, home, cache, env, configPath, repo := lastGapDotsFixture(t)
	out := runOmniOutput(t, buildOmniBinary(t), root, env, "--yes", "--config", configPath, "--cache-dir", cache, "dots", "sync", "--use-repo")
	if !strings.Contains(out, "repaired") || !strings.Contains(out, "symlink(s) updated") {
		t.Fatalf("resolve-all use-repo output: %s", out)
	}
	assertLastGapDotsResolved(t, home, repo, "repo")
}

func TestCLIBinaryDotsResolveAllUseLocalAdoptsEveryConflict(t *testing.T) {
	root, home, cache, env, configPath, repo := lastGapDotsFixture(t)
	out := runOmniOutput(t, buildOmniBinary(t), root, env, "--yes", "--config", configPath, "--cache-dir", cache, "dots", "sync", "--use-local")
	if !strings.Contains(out, "adopted") || !strings.Contains(out, "symlink(s) updated") {
		t.Fatalf("resolve-all use-local output: %s", out)
	}
	assertLastGapDotsResolved(t, home, repo, "local")
}

func TestCLIBinaryAgentsRefreshDelegatesOutdatedQuery(t *testing.T) {
	root, home, cache, env, logPath := agentsRemainingBinaryFixture(t)
	out := runOmniOutput(t, buildOmniBinary(t), root, env, "--cache-dir", cache, "agents", "outdated")
	if !strings.Contains(out, "delegated: outdated -g") {
		t.Fatalf("agents refresh output: %s", out)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".apm") + "|outdated -g"
	if strings.TrimSpace(string(raw)) != want {
		t.Fatalf("agents refresh invocation = %q, want %q", raw, want)
	}
}

func lastGapDotsFixture(t *testing.T) (root, home, cache string, env []string, configPath, repo string) {
	t.Helper()
	root, home, cache, env = newCLIBinarySandbox(t)
	repo = filepath.Join(home, "dotfiles")
	initDotsRepo(t, repo, env)
	entries := []config.DotEntry{
		{Name: "alpha", Path: filepath.Join(home, ".config", "alpha")},
		{Name: "beta", Path: filepath.Join(home, ".config", "beta")},
	}
	for _, entry := range entries {
		repoFile := filepath.Join(repo, "dotfiles", entry.Name, ".config", entry.Name, entry.Name+".conf")
		localFile := filepath.Join(entry.Path, entry.Name+".conf")
		writeIntegrationFile(t, repoFile, "repo-"+entry.Name+"\n")
		writeIntegrationFile(t, localFile, "local-"+entry.Name+"\n")
		localTime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
		repoTime := localTime.Add(time.Hour)
		if err := os.Chtimes(localFile, localTime, localTime); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(repoFile, repoTime, repoTime); err != nil {
			t.Fatal(err)
		}
	}
	configPath = filepath.Join(root, "settings.json")
	if err := config.Save(configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{DotsRepo: repo},
		Hosts:    map[string][]string{"testhost": {}},
		Groups:   []*config.GroupConfig{{Name: "testhost", Special: "host", Dots: entries}},
	}); err != nil {
		t.Fatal(err)
	}
	return root, home, cache, env, configPath, repo
}

func assertLastGapDotsResolved(t *testing.T, home, repo, kept string) {
	t.Helper()
	for _, name := range []string{"alpha", "beta"} {
		localFile := filepath.Join(home, ".config", name, name+".conf")
		repoFile := filepath.Join(repo, "dotfiles", name, ".config", name, name+".conf")
		info, err := os.Lstat(localFile)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%s local target is not a symlink: %v, %v", name, info, err)
		}
		want := kept + "-" + name + "\n"
		for _, path := range []string{localFile, repoFile} {
			raw, err := os.ReadFile(path)
			if err != nil || string(raw) != want {
				t.Fatalf("resolved %s = %q, %v; want %q", path, raw, err, want)
			}
		}
	}
}
