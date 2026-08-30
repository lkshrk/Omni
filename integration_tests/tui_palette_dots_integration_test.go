//go:build integration

package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"
)

func TestTUIDotsCommitPersistsLocalGitCommitWithoutPush(t *testing.T) {
	bin := buildOmniBinary(t)
	sandbox := newParitySandbox(t, t.TempDir())
	seedDotsGitCommitParity(t, sandbox)
	runDotsGitCommitParityTUI(t, bin, sandbox)
	state := observeDotsGitCommitParity(t, sandbox).(dotsGitCommitState)
	if state.Status != "" || state.Subject == "seed" || state.RemoteSubject != "seed" || state.SourceContent != "changed\n" {
		t.Fatalf("TUI commit state = %#v", state)
	}
}

func TestTUIDotsRefreshDetectsBrokenLinkWithoutMutatingIt(t *testing.T) {
	bin, root, cache, configPath, target, _, env := newTUIDotActionSandbox(t)
	broken := filepath.Join(root, "missing")

	runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, screenHas("Dashboard", "Tools"), "TUI did not render main tabs")
		writeTUIKeys(t, term, "\t", "\t")
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("nvim", "synced"), "TUI did not render the synced dot entry")
		if err := os.Remove(target); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(broken, target); err != nil {
			t.Fatal(err)
		}
		writeTUIKeys(t, term, "R")
		return waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
			link, err := os.Readlink(target)
			return err == nil && link == broken && strings.Contains(text, "nvim") && strings.Contains(text, "broken") && !strings.Contains(strings.ToLower(text), "refreshing")
		}, "TUI refresh did not report the broken link without mutation")
	})
}
