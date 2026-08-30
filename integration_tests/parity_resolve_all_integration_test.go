//go:build integration

package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"
	"github.com/lkshrk/omni/internal/config"
)

func TestCLIAndTUIDotsResolveAllUseRepoProduceEquivalentSemanticState(t *testing.T) {
	runResolveAllParity(t, "--use-repo", 'U', "repo-")
}

func TestCLIAndTUIDotsResolveAllUseLocalProduceEquivalentSemanticState(t *testing.T) {
	runResolveAllParity(t, "--use-local", 'L', "local-")
}

func runResolveAllParity(t *testing.T, flag string, key rune, wantPrefix string) {
	bin := buildOmniBinary(t)
	cli, tui := newParityTwins(t)
	seedResolveAllParity(t, cli)
	seedResolveAllParity(t, tui)
	runOmniCommand(t, bin, cli.root, cli.env, "--yes", "--config", cli.configPath, "--cache-dir", cli.cache, "dots", "sync", flag)
	runResolveAllParityTUI(t, bin, tui, key, wantPrefix)
	if got, want := observeResolveAllParity(t, tui), observeResolveAllParity(t, cli); got != want {
		t.Fatalf("resolve-all semantic state differs\nCLI: %#v\nTUI: %#v", want, got)
	}
}

func seedResolveAllParity(t *testing.T, s *paritySandbox) {
	repo := filepath.Join(s.home, "dotfiles")
	initDotsRepo(t, repo, s.env)
	entries := []config.DotEntry{{Name: "alpha", Path: filepath.Join(s.home, ".config", "alpha")}, {Name: "beta", Path: filepath.Join(s.home, ".config", "beta")}}
	for _, e := range entries {
		rf, lf := filepath.Join(repo, "dotfiles", e.Name, ".config", e.Name, e.Name+".conf"), filepath.Join(e.Path, e.Name+".conf")
		writeIntegrationFile(t, rf, "repo-"+e.Name+"\n")
		writeIntegrationFile(t, lf, "local-"+e.Name+"\n")
		base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
		if err := os.Chtimes(lf, base, base); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(rf, base.Add(time.Hour), base.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if err := config.Save(s.configPath, &config.RootConfig{Version: config.CurrentVersion, Settings: config.Settings{DotsRepo: repo}, Hosts: map[string][]string{"testhost": {}}, Groups: []*config.GroupConfig{{Name: "testhost", Special: "host", Dots: entries}}}); err != nil {
		t.Fatal(err)
	}
}

func runResolveAllParityTUI(t *testing.T, bin string, s *paritySandbox, actionKey rune, wantPrefix string) {
	runTUI(t, bin, s.root, s.env, []string{"--config", s.configPath, "--cache-dir", s.cache}, func(term *vttest.Terminal) string {
		if err := term.Resize(160, 30); err != nil {
			t.Fatalf("resize TUI: %v", err)
		}
		waitForRequiredScreen(t, term, 6*time.Second, screenHas("Dashboard", "Dots"), "TUI did not start")
		writeTUIKeys(t, term, "\t", "\t")
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("alpha", "beta", "Conflict"), "TUI did not render conflicts")
		writeTUIKeys(t, term, "j")
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("use repo (all)", "use local (all)"), "TUI never rendered bulk resolve hints")
		sendTUIKey(term, actionKey)
		waitForRequiredScreen(t, term, 3*time.Second, func(text string) bool {
			return strings.Contains(strings.ToLower(text), strings.TrimSuffix(strings.ReplaceAll(wantPrefix, "_", " "), "-"))
		}, "TUI did not arm bulk resolve")
		sendTUIKey(term, actionKey)
		return waitForRequiredScreen(t, term, 12*time.Second, func(string) bool { return resolveAllSettled(s, wantPrefix) }, "TUI did not resolve all")
	})
}

type resolveAllState struct {
	Config, Files string
	Tree, Status  string
}

func observeResolveAllParity(t *testing.T, s *paritySandbox) resolveAllState {
	repo := filepath.Join(s.home, "dotfiles")
	var files []string
	for _, name := range []string{"alpha", "beta"} {
		p := filepath.Join(s.home, ".config", name, name+".conf")
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		link, err := filepath.EvalSymlinks(p)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, name+":"+string(raw)+":"+strings.ReplaceAll(link, s.root, "$ROOT"))
	}
	return resolveAllState{Config: stringifyNormalizedConfig(t, s), Files: strings.Join(files, "|"), Tree: runCommandOutput(t, repo, s.env, "git", "rev-parse", "HEAD^{tree}"), Status: runCommandOutput(t, repo, s.env, "git", "status", "--porcelain=v1")}
}

func stringifyNormalizedConfig(t *testing.T, s *paritySandbox) string {
	return strings.ReplaceAll(strings.TrimSpace(string(mustJSON(t, normalizedParityConfig(t, s)))), s.root, "$ROOT")
}
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func resolveAllSettled(s *paritySandbox, prefix string) bool {
	for _, name := range []string{"alpha", "beta"} {
		p := filepath.Join(s.home, ".config", name, name+".conf")
		i, err := os.Lstat(p)
		if err != nil || i.Mode()&os.ModeSymlink == 0 {
			return false
		}
		raw, err := os.ReadFile(p)
		if err != nil || string(raw) != prefix+name+"\n" {
			return false
		}
	}
	return true
}
