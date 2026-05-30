package app_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/dots"
)

type dotsHistoryWant struct {
	operation       string
	entry           string
	status          string
	summaryContains string
	errorContains   string
}

func TestDotsCommitRecordsHistory(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNI_HOSTNAME", "testhost")

	repoDir := filepath.Join(home, "dotfiles")
	sourceFile := filepath.Join(repoDir, "dotfiles", "nvim", ".config", "nvim", "init.lua")
	if err := os.MkdirAll(filepath.Dir(sourceFile), 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.WriteFile(sourceFile, []byte("-- seed\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, string(out))
		}
	}
	git("init")
	git("config", "user.email", "t@t.com")
	git("config", "user.name", "T")
	git("config", "commit.gpgsign", "false")
	git("config", "tag.gpgsign", "false")
	git("add", "dotfiles")
	git("commit", "-m", "seed")
	if err := os.WriteFile(sourceFile, []byte("-- changed\n"), 0o644); err != nil {
		t.Fatalf("dirty source: %v", err)
	}

	cfgPath := filepath.Join(home, "settings.json")
	if err := os.WriteFile(cfgPath, []byte(`{"host_settings":{"testhost":{"dots_repo":"~/dotfiles"}},"hosts":{"testhost":[]},"groups":[{"name":"testhost","special":"host","dots":[{"name":"nvim","path":"~/.config/nvim"}]}]}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	a := app.New(cfgPath)
	a.CacheDir = filepath.Join(home, "cache")
	if err := a.InitTestMode(ctx); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer a.Close()

	if err := a.DotsCommit(ctx, "dots: history"); err != nil {
		t.Fatalf("DotsCommit: %v", err)
	}
	history, err := a.RecentDotsHistory(ctx, 10)
	if err != nil {
		t.Fatalf("RecentDotsHistory: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history len = %d, want 1: %+v", len(history), history)
	}
	got := history[0]
	if got.Operation != "commit" || got.Status != "success" || got.RepoPath != repoDir {
		t.Fatalf("history[0] = %+v, want commit success for %s", got, repoDir)
	}
}

func TestDotsHistoryRecordsOperationsFailuresAndRetention(t *testing.T) {
	ctx := context.Background()
	a, cfgDir, repoDir := newDotsApp(t)
	home := os.Getenv("HOME")
	sourceFile := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim", "init.lua")
	if err := os.MkdirAll(filepath.Dir(sourceFile), 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.WriteFile(sourceFile, []byte("-- repo\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim"}}, home)

	if _, err := a.DotsSyncEntry(ctx, "missing-entry", dots.SyncOptions{}); err == nil {
		t.Fatal("DotsSyncEntry missing-entry should fail")
	}
	history, err := a.RecentDotsHistory(ctx, 10)
	if err != nil {
		t.Fatalf("RecentDotsHistory: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history len = %d, want 1: %+v", len(history), history)
	}
	got := history[0]
	if got.Operation != "sync entry" || got.Entry != "missing-entry" || got.Status != "failed" {
		t.Fatalf("history[0] = %+v, want failed sync-entry record", got)
	}
	if got.Summary != "sync entry failed" || !strings.Contains(got.Error, `dots entry "missing-entry" not found`) {
		t.Fatalf("history[0] summary/error = %q/%q, want failed missing-entry details", got.Summary, got.Error)
	}

	if _, err := a.DotsSyncEntry(ctx, "dry-run-missing", dots.SyncOptions{DryRun: true}); err == nil {
		t.Fatal("dry-run DotsSyncEntry missing entry should still fail")
	}
	history, err = a.RecentDotsHistory(ctx, 10)
	if err != nil {
		t.Fatalf("RecentDotsHistory after dry-run: %v", err)
	}
	if len(history) != 1 || history[0].Entry != "missing-entry" {
		t.Fatalf("dry-run should not record history, got %+v", history)
	}

	if err := a.DotsDelete(ctx, "ghost-delete"); err == nil {
		t.Fatal("DotsDelete ghost-delete should fail")
	}
	history, err = a.RecentDotsHistory(ctx, 10)
	if err != nil {
		t.Fatalf("RecentDotsHistory after delete: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("history len = %d, want 2: %+v", len(history), history)
	}
	if history[0].Operation != "delete" || history[0].Entry != "ghost-delete" || history[0].Status != "failed" {
		t.Fatalf("latest history = %+v, want failed delete record", history[0])
	}

	for i := range 55 {
		name := fmt.Sprintf("entry-%02d", i)
		if _, err := a.DotsSyncEntry(ctx, name, dots.SyncOptions{}); err == nil {
			t.Fatalf("DotsSyncEntry %s should fail", name)
		}
	}
	history, err = a.RecentDotsHistory(ctx, 100)
	if err != nil {
		t.Fatalf("RecentDotsHistory retention: %v", err)
	}
	if len(history) != 50 {
		t.Fatalf("history len = %d, want retention cap 50", len(history))
	}
	if history[0].Entry != "entry-54" || history[49].Entry != "entry-05" {
		t.Fatalf("history order/retention = first %q last %q, want entry-54..entry-05", history[0].Entry, history[49].Entry)
	}
	limited, err := a.RecentDotsHistory(ctx, 3)
	if err != nil {
		t.Fatalf("RecentDotsHistory limit: %v", err)
	}
	want := []string{"entry-54", "entry-53", "entry-52"}
	for i, entry := range limited {
		if entry.Entry != want[i] {
			t.Fatalf("limited[%d].Entry = %q, want %q; entries=%+v", i, entry.Entry, want[i], limited)
		}
	}
}

func TestDotsHistoryRecordsSuccessfulFilesystemOperations(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	ctx := context.Background()
	a, cfgDir, repoDir := newDotsApp(t)
	home := os.Getenv("HOME")
	seedHistoryNvimEntry(t, cfgDir, repoDir, home)

	if _, err := a.DotsSync(dots.SyncOptions{}); err != nil {
		t.Fatalf("DotsSync: %v", err)
	}
	assertLatestDotsHistory(t, a, ctx, dotsHistoryWant{operation: "sync", status: "success", summaryContains: "sync completed"})

	if _, err := a.DotsSyncEntry(ctx, "nvim", dots.SyncOptions{}); err != nil {
		t.Fatalf("DotsSyncEntry: %v", err)
	}
	assertLatestDotsHistory(t, a, ctx, dotsHistoryWant{operation: "sync entry", entry: "nvim", status: "success", summaryContains: "sync entry completed"})

	if err := a.DotsDelete(ctx, "nvim"); err != nil {
		t.Fatalf("DotsDelete: %v", err)
	}
	assertLatestDotsHistory(t, a, ctx, dotsHistoryWant{operation: "delete", entry: "nvim", status: "success", summaryContains: "delete completed"})

	localFile := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(localFile, []byte("# zsh\n"), 0o644); err != nil {
		t.Fatalf("write local file: %v", err)
	}
	if _, err := a.DotsAdd(ctx, localFile, app.DotsAddOptions{Name: "zshrc", Adopt: true}); err != nil {
		t.Fatalf("DotsAdd: %v", err)
	}
	assertLatestDotsHistory(t, a, ctx, dotsHistoryWant{operation: "add", entry: "zshrc", status: "success", summaryContains: "add completed"})
}

func TestDotsSyncSuppressesUnchangedHistoryWhenRequested(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	ctx := context.Background()
	a, cfgDir, repoDir := newDotsApp(t)
	home := os.Getenv("HOME")
	seedHistoryNvimEntry(t, cfgDir, repoDir, home)

	if _, err := a.DotsSyncContext(ctx, dots.SyncOptions{SuppressUnchangedHistory: true}); err != nil {
		t.Fatalf("initial DotsSyncContext: %v", err)
	}
	before, err := a.RecentDotsHistory(ctx, 10)
	if err != nil {
		t.Fatalf("RecentDotsHistory before unchanged sync: %v", err)
	}
	if len(before) != 1 {
		t.Fatalf("history before unchanged sync len = %d, want 1: %+v", len(before), before)
	}

	if _, err := a.DotsSyncContext(ctx, dots.SyncOptions{SuppressUnchangedHistory: true}); err != nil {
		t.Fatalf("unchanged DotsSyncContext: %v", err)
	}
	after, err := a.RecentDotsHistory(ctx, 10)
	if err != nil {
		t.Fatalf("RecentDotsHistory after unchanged sync: %v", err)
	}
	if len(after) != len(before) || after[0].ID != before[0].ID {
		t.Fatalf("unchanged sync should not add history, before=%+v after=%+v", before, after)
	}
}

func TestDotsHistoryRecordsGitOperationSuccesses(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	ctx := context.Background()
	a, cfgDir, repoDir := newDotsApp(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}

	remoteDir := filepath.Join(t.TempDir(), "remote.git")
	seedDir := filepath.Join(t.TempDir(), "seed")
	otherDir := filepath.Join(t.TempDir(), "other")
	historyGit(t, "", "init", "--bare", "--initial-branch=main", remoteDir)
	historyGit(t, "", "clone", remoteDir, seedDir)
	configureHistoryGitUser(t, seedDir)
	seedFile := filepath.Join(seedDir, "dotfiles", "nvim", ".config", "nvim", "init.lua")
	if err := os.MkdirAll(filepath.Dir(seedFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(seedFile, []byte("-- seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	historyGit(t, seedDir, "add", "dotfiles")
	historyGit(t, seedDir, "commit", "-m", "seed")
	historyGit(t, seedDir, "push", "-u", "origin", "main")

	historyGit(t, "", "clone", remoteDir, repoDir)
	configureHistoryGitUser(t, repoDir)
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: filepath.Join(home, ".config", "nvim")},
	}, home)
	if _, err := a.DotsSyncContext(ctx, dots.SyncOptions{}); err != nil {
		t.Fatalf("initial DotsSyncContext: %v", err)
	}

	historyGit(t, "", "clone", remoteDir, otherDir)
	configureHistoryGitUser(t, otherDir)
	remoteFile := filepath.Join(otherDir, "dotfiles", "nvim", ".config", "nvim", "init.lua")
	if err := os.WriteFile(remoteFile, []byte("-- remote change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	historyGit(t, otherDir, "add", "dotfiles/nvim/.config/nvim/init.lua")
	historyGit(t, otherDir, "commit", "-m", "remote-change")
	historyGit(t, otherDir, "push")

	if _, err := a.DotsPull(ctx); err != nil {
		t.Fatalf("DotsPull: %v", err)
	}
	assertLatestDotsHistory(t, a, ctx, dotsHistoryWant{operation: "pull", status: "success", summaryContains: "pull completed"})

	repoFile := filepath.Join(repoDir, "dotfiles", "nvim", ".config", "nvim", "init.lua")
	if err := os.WriteFile(repoFile, []byte("-- local change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.DotsPush(ctx, "dots: local change"); err != nil {
		t.Fatalf("DotsPush: %v", err)
	}
	assertLatestDotsHistory(t, a, ctx, dotsHistoryWant{operation: "push", status: "success", summaryContains: "push completed"})

	if err := os.WriteFile(repoFile, []byte("-- committed change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.DotsCommit(ctx, "dots: committed change"); err != nil {
		t.Fatalf("DotsCommit: %v", err)
	}
	assertLatestDotsHistory(t, a, ctx, dotsHistoryWant{operation: "commit", status: "success", summaryContains: "commit completed"})
}

func TestDotsHistoryRecordsEnableDisableAndResolveSuccesses(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	ctx := context.Background()

	t.Run("enable disable", func(t *testing.T) {
		a, cfgDir, repoDir := newDotsApp(t)
		home := os.Getenv("HOME")
		seedHistoryNvimEntry(t, cfgDir, repoDir, home)
		if _, err := a.DotsSync(dots.SyncOptions{}); err != nil {
			t.Fatalf("DotsSync: %v", err)
		}
		if _, err := a.DisableDotsForHost(ctx, app.DisableDotsOptions{}); err != nil {
			t.Fatalf("DisableDotsForHost: %v", err)
		}
		assertLatestDotsHistory(t, a, ctx, dotsHistoryWant{operation: "disable", status: "success", summaryContains: "disable completed"})
		if _, err := a.EnableDotsForHost(ctx); err != nil {
			t.Fatalf("EnableDotsForHost: %v", err)
		}
		assertLatestDotsHistory(t, a, ctx, dotsHistoryWant{operation: "enable", status: "success", summaryContains: "enable completed"})
	})

	t.Run("resolve use repo", func(t *testing.T) {
		a, _, _ := newHistoryConflictApp(t)
		if _, err := a.DotsResolveConflict(ctx, "nvim", app.DotResolveUseRepo); err != nil {
			t.Fatalf("DotsResolveConflict use-repo: %v", err)
		}
		assertLatestDotsHistory(t, a, ctx, dotsHistoryWant{operation: "resolve use-repo", entry: "nvim", status: "success", summaryContains: "resolve use-repo completed"})
	})

	t.Run("resolve use local", func(t *testing.T) {
		a, _, _ := newHistoryConflictApp(t)
		if _, err := a.DotsResolveConflict(ctx, "nvim", app.DotResolveUseLocal); err != nil {
			t.Fatalf("DotsResolveConflict use-local: %v", err)
		}
		assertLatestDotsHistory(t, a, ctx, dotsHistoryWant{operation: "resolve use-local", entry: "nvim", status: "success", summaryContains: "resolve use-local completed"})
	})
}

func TestDotsHistoryRecordsOperationFailureModes(t *testing.T) {
	ctx := context.Background()

	t.Run("add", func(t *testing.T) {
		a, _, _ := newDotsApp(t)
		_, err := a.DotsAdd(ctx, filepath.Join(os.Getenv("HOME"), "missing"), app.DotsAddOptions{Name: "missing", Adopt: true})
		if err == nil {
			t.Fatal("DotsAdd should fail for missing path")
		}
		assertLatestDotsHistory(t, a, ctx, dotsHistoryWant{operation: "add", status: "failed", summaryContains: "add failed", errorContains: "no such file"})
	})

	t.Run("sync", func(t *testing.T) {
		if _, err := exec.LookPath("stow"); err != nil {
			t.Skip("stow not available")
		}
		a, _, _ := newHistoryConflictApp(t)
		_, err := a.DotsSync(dots.SyncOptions{})
		if err == nil {
			t.Fatal("DotsSync should fail on choice conflict")
		}
		assertLatestDotsHistory(t, a, ctx, dotsHistoryWant{operation: "sync", status: "partial", summaryContains: "sync failed", errorContains: "requires choosing"})
	})

	t.Run("delete", func(t *testing.T) {
		a, _, _ := newDotsApp(t)
		if err := a.DotsDelete(ctx, "ghost-delete"); err == nil {
			t.Fatal("DotsDelete ghost-delete should fail")
		}
		assertLatestDotsHistory(t, a, ctx, dotsHistoryWant{operation: "delete", entry: "ghost-delete", status: "failed", summaryContains: "delete failed", errorContains: "not found"})
	})

	for _, tc := range []struct {
		name      string
		operation string
		run       func(*app.App) error
	}{
		{name: "pull", operation: "pull", run: func(a *app.App) error {
			_, err := a.DotsPull(ctx)
			return err
		}},
		{name: "push", operation: "push", run: func(a *app.App) error {
			return a.DotsPush(ctx, "dots: push")
		}},
		{name: "commit", operation: "commit", run: func(a *app.App) error {
			return a.DotsCommit(ctx, "dots: commit")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _, _ := newDotsApp(t)
			if err := tc.run(a); err == nil {
				t.Fatalf("%s should fail in a non-git repo", tc.operation)
			}
			assertLatestDotsHistory(t, a, ctx, dotsHistoryWant{operation: tc.operation, status: "failed", summaryContains: tc.operation + " failed"})
		})
	}

	t.Run("enable", func(t *testing.T) {
		if _, err := exec.LookPath("stow"); err != nil {
			t.Skip("stow not available")
		}
		a, _, _ := newHistoryConflictApp(t)
		if err := a.SaveDotsDisabled(ctx, true); err != nil {
			t.Fatalf("SaveDotsDisabled: %v", err)
		}
		_, err := a.EnableDotsForHost(ctx)
		if err == nil {
			t.Fatal("EnableDotsForHost should fail on sync conflict")
		}
		assertLatestDotsHistory(t, a, ctx, dotsHistoryWant{operation: "enable", status: "partial", summaryContains: "enable failed", errorContains: "requires choosing"})
	})

	t.Run("disable", func(t *testing.T) {
		a, cfgDir, repoDir := newDotsApp(t)
		home := os.Getenv("HOME")
		srcFile := filepath.Join(dotsContentDir(repoDir), "zsh", ".zshrc")
		dstFile := filepath.Join(home, ".zshrc")
		if err := os.MkdirAll(filepath.Dir(srcFile), 0o755); err != nil {
			t.Fatal(err)
		}
		secret := filepath.Join(home, "secret")
		if err := os.WriteFile(secret, []byte("# external"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(secret, srcFile); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dstFile, []byte("# local"), 0o644); err != nil {
			t.Fatal(err)
		}
		writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{Name: "zsh", Path: dstFile}}, home)
		_, err := a.DisableDotsForHost(ctx, app.DisableDotsOptions{ConflictOverwrite: true})
		if err == nil {
			t.Fatal("DisableDotsForHost should fail on external symlink source")
		}
		assertLatestDotsHistory(t, a, ctx, dotsHistoryWant{operation: "disable", status: "failed", summaryContains: "disable failed"})
	})

	t.Run("resolve", func(t *testing.T) {
		if _, err := exec.LookPath("stow"); err != nil {
			t.Skip("stow not available")
		}
		a, cfgDir, repoDir := newDotsApp(t)
		home := os.Getenv("HOME")
		seedHistoryNvimEntry(t, cfgDir, repoDir, home)
		_, err := a.DotsResolveConflict(ctx, "ghost", app.DotResolveUseRepo)
		if err == nil {
			t.Fatal("DotsResolveConflict should fail for missing entry")
		}
		assertLatestDotsHistory(t, a, ctx, dotsHistoryWant{operation: "resolve use-repo", entry: "ghost", status: "failed", summaryContains: "resolve use-repo failed", errorContains: "not found"})
	})
}

func TestDotsHistoryConcurrentWritesPreserveAllEntries(t *testing.T) {
	ctx := context.Background()
	a, _, _ := newDotsApp(t)

	// Fire 10 concurrent sync-entry calls (all will fail on missing entries,
	// which is fine — we just need them to record history concurrently).
	const n = 10
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("concurrent-%02d", i)
			_, _ = a.DotsSyncEntry(ctx, name, dots.SyncOptions{})
		}()
	}
	wg.Wait()

	history, err := a.RecentDotsHistory(ctx, 100)
	if err != nil {
		t.Fatalf("RecentDotsHistory: %v", err)
	}
	if len(history) != n {
		t.Fatalf("history len = %d, want %d (concurrent writes lost entries)", len(history), n)
	}
}

func TestDotsHistoryIDTimestampMatchesTimeField(t *testing.T) {
	ctx := context.Background()
	a, _, _ := newDotsApp(t)

	// Record one entry and verify the ID's timestamp prefix is
	// consistent with the Time field (within 1 second).
	if _, err := a.DotsSyncEntry(ctx, "ts-check", dots.SyncOptions{}); err == nil {
		t.Fatal("expected error for missing entry")
	}
	history, err := a.RecentDotsHistory(ctx, 1)
	if err != nil {
		t.Fatalf("RecentDotsHistory: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history len = %d, want 1", len(history))
	}
	entry := history[0]
	// ID format: "<unixnano>-<counter>"
	parts := strings.SplitN(entry.ID, "-", 2)
	if len(parts) != 2 {
		t.Fatalf("unexpected ID format: %q", entry.ID)
	}
	var idNano int64
	if _, err := fmt.Sscanf(parts[0], "%d", &idNano); err != nil {
		t.Fatalf("parse ID nano: %v", err)
	}
	idTime := time.Unix(0, idNano)
	diff := idTime.Sub(entry.Time)
	if diff < 0 {
		diff = -diff
	}
	if diff > time.Second {
		t.Fatalf("ID time %v differs from Time %v by %v (want <1s)", idTime, entry.Time, diff)
	}
}

func seedHistoryNvimEntry(t *testing.T, cfgDir, repoDir, home string) (sourceFile, targetDir string) {
	t.Helper()
	sourceFile = filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim", "init.lua")
	if err := os.MkdirAll(filepath.Dir(sourceFile), 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.WriteFile(sourceFile, []byte("-- repo\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	targetDir = filepath.Join(home, ".config", "nvim")
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{Name: "nvim", Path: targetDir}}, home)
	return sourceFile, targetDir
}

func newHistoryConflictApp(t *testing.T) (*app.App, string, string) {
	t.Helper()
	a, cfgDir, repoDir := newDotsApp(t)
	home := os.Getenv("HOME")
	sourceFile, targetDir := seedHistoryNvimEntry(t, cfgDir, repoDir, home)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	targetFile := filepath.Join(targetDir, "init.lua")
	if err := os.WriteFile(targetFile, []byte("-- local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	setDotTestModTime(t, sourceFile, time.Unix(1_700_000_000, 0).Add(time.Hour))
	setDotTestModTime(t, targetFile, time.Unix(1_700_000_000, 0))
	return a, cfgDir, repoDir
}

func assertLatestDotsHistory(t *testing.T, a *app.App, ctx context.Context, want dotsHistoryWant) {
	t.Helper()
	history, err := a.RecentDotsHistory(ctx, 1)
	if err != nil {
		t.Fatalf("RecentDotsHistory: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history len = %d, want 1", len(history))
	}
	got := history[0]
	if got.Operation != want.operation {
		t.Fatalf("history operation = %q, want %q: %+v", got.Operation, want.operation, got)
	}
	if got.Entry != want.entry {
		t.Fatalf("history entry = %q, want %q: %+v", got.Entry, want.entry, got)
	}
	if got.Status != want.status {
		t.Fatalf("history status = %q, want %q: %+v", got.Status, want.status, got)
	}
	if want.summaryContains != "" && !strings.Contains(got.Summary, want.summaryContains) {
		t.Fatalf("history summary = %q, want substring %q: %+v", got.Summary, want.summaryContains, got)
	}
	if want.errorContains != "" && !strings.Contains(got.Error, want.errorContains) {
		t.Fatalf("history error = %q, want substring %q: %+v", got.Error, want.errorContains, got)
	}
}

func historyGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git -C %s %v: %v\n%s", dir, args, err, out)
	}
}

func configureHistoryGitUser(t *testing.T, dir string) {
	t.Helper()
	historyGit(t, dir, "config", "user.email", "test@example.com")
	historyGit(t, dir, "config", "user.name", "Test")
	historyGit(t, dir, "config", "commit.gpgsign", "false")
	historyGit(t, dir, "config", "tag.gpgsign", "false")
}
