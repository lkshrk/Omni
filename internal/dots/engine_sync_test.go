package dots_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/dots"
	"github.com/lkshrk/omni/internal/executor"
)

// These tests exercise Engine.Sync end-to-end against a real `stow` binary and
// a real executor, with $HOME pointed at a temp dir. They are the payoff of
// deepening the dots engine: the whole conflict matrix (link, skip, real-file
// conflict, use_repo, use_local adoption, dry-run) is now reachable through the
// engine's small interface, with no App and no injected classifier — the
// interface is the test surface.

const (
	syncTargetRel  = ".gitconfig-omnitest"
	syncEntryName  = "gitcfg"
	syncRepoBody   = "REPO VERSION\n"
	syncLocalBody  = "LOCAL VERSION\n"
	syncPackageDir = syncEntryName // package defaults to the entry name
)

// stowFailExecutor fails every `stow` invocation while delegating all other
// commands (git, trash renames) to a real executor. It lets the rollback and
// restore branches — the paths that only fire when restow fails — be driven
// deterministically through the engine seam.
type stowFailExecutor struct{ inner executor.Executor }

func (e stowFailExecutor) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	if name == "stow" {
		return "", "mock: conflict", errors.New("stow: mock failure")
	}
	return e.inner.Run(ctx, name, args...)
}

// syncFixture builds a temp $HOME plus a stow content root holding one
// single-file package, and returns an Engine wired with a real executor.
// repoBody, when non-empty, seeds the repo source file for the entry.
func syncFixture(t *testing.T, repoBody string) (*dots.Engine, string) {
	return syncFixtureExec(t, repoBody, executor.New())
}

// syncFixtureExec is syncFixture with a caller-supplied executor, so tests can
// inject a failing one to reach the rollback branches.
func syncFixtureExec(t *testing.T, repoBody string, exec executor.Executor) (*dots.Engine, string) {
	t.Helper()
	if _, err := os.Stat("/usr/bin/stow"); err != nil {
		if _, err := os.Stat("/usr/local/bin/stow"); err != nil {
			t.Skip("stow binary not available")
		}
	}
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	stow := filepath.Join(tmp, "repo", "dotfiles")
	if repoBody != "" {
		src := filepath.Join(stow, syncPackageDir, syncTargetRel)
		if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(src, []byte(repoBody), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	entries := []config.DotEntry{{Name: syncEntryName, Path: filepath.Join(home, syncTargetRel)}}
	eng, err := dots.NewEngine(stow, entries, dots.WithExecutor(exec))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return eng, home
}

func targetPath(home string) string { return filepath.Join(home, syncTargetRel) }

func writeConflictTarget(t *testing.T, eng *dots.Engine, home string) {
	t.Helper()
	target := targetPath(home)
	if err := os.WriteFile(target, []byte(syncLocalBody), 0o644); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(eng.RepoPath, syncPackageDir, syncTargetRel)
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(target, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
}

func assertManagedLink(t *testing.T, home, wantBody string) {
	t.Helper()
	target := targetPath(home)
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("lstat target: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("target %s is not a symlink (mode %v)", target, info.Mode())
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read through link: %v", err)
	}
	if string(got) != wantBody {
		t.Fatalf("linked content = %q, want %q", got, wantBody)
	}
}

func TestEngineSync_CreatesManagedLink(t *testing.T) {
	eng, home := syncFixture(t, syncRepoBody)

	ops, err := eng.Sync(context.Background(), dots.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(ops) == 0 {
		t.Fatal("Sync returned no ops")
	}
	assertManagedLink(t, home, syncRepoBody)
}

func TestEngineSync_SkipsAlreadySynced(t *testing.T) {
	eng, home := syncFixture(t, syncRepoBody)

	if _, err := eng.Sync(context.Background(), dots.SyncOptions{}); err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	ops, err := eng.Sync(context.Background(), dots.SyncOptions{})
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	var sawSkip bool
	for _, op := range ops {
		if op.Kind == dots.OpSkip {
			sawSkip = true
		}
	}
	if !sawSkip {
		t.Fatalf("second Sync ops = %+v, want an OpSkip", ops)
	}
	assertManagedLink(t, home, syncRepoBody)
}

func TestEngineSync_RealFileConflictErrors(t *testing.T) {
	eng, home := syncFixture(t, syncRepoBody)
	target := targetPath(home)
	writeConflictTarget(t, eng, home)

	_, err := eng.Sync(context.Background(), dots.SyncOptions{})
	if err == nil {
		t.Fatal("Sync over a real-file conflict should error without a strategy")
	}
	// The local file must be left untouched when the conflict is unresolved.
	info, statErr := os.Lstat(target)
	if statErr != nil {
		t.Fatalf("lstat target: %v", statErr)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("unresolved conflict replaced the local file with a symlink")
	}
	got, _ := os.ReadFile(target)
	if string(got) != syncLocalBody {
		t.Fatalf("local file changed on unresolved conflict: %q", got)
	}
}

func TestEngineSync_ConflictUseRepo(t *testing.T) {
	eng, home := syncFixture(t, syncRepoBody)
	writeConflictTarget(t, eng, home)

	if _, err := eng.Sync(context.Background(), dots.SyncOptions{ConflictStrategy: "use_repo"}); err != nil {
		t.Fatalf("Sync use_repo: %v", err)
	}
	assertManagedLink(t, home, syncRepoBody)
}

func TestEngineSync_ConflictUseLocalAdopts(t *testing.T) {
	eng, home := syncFixture(t, syncRepoBody)
	writeConflictTarget(t, eng, home)

	if _, err := eng.Sync(context.Background(), dots.SyncOptions{ConflictStrategy: "use_local"}); err != nil {
		t.Fatalf("Sync use_local: %v", err)
	}
	// Local content wins: the target is now a managed link resolving to it, and
	// the repo source has adopted the local body.
	assertManagedLink(t, home, syncLocalBody)
	src := filepath.Join(eng.RepoPath, syncPackageDir, syncTargetRel)
	got, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read adopted repo source: %v", err)
	}
	if string(got) != syncLocalBody {
		t.Fatalf("repo source after use_local = %q, want %q", got, syncLocalBody)
	}
}

func TestEngineSync_DryRunMakesNoChanges(t *testing.T) {
	eng, home := syncFixture(t, syncRepoBody)

	ops, err := eng.Sync(context.Background(), dots.SyncOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry-run Sync: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpDryLink {
		t.Fatalf("dry-run ops = %+v, want a single OpDryLink", ops)
	}
	if _, err := os.Lstat(targetPath(home)); !os.IsNotExist(err) {
		t.Fatalf("dry-run created the target: lstat err = %v", err)
	}
}

func TestEngineSync_ProgressEventsFire(t *testing.T) {
	eng, _ := syncFixture(t, syncRepoBody)

	var starts, dones int
	opts := dots.SyncOptions{Progress: func(ev dots.SyncProgressEvent) {
		if ev.Done {
			dones++
		} else {
			starts++
		}
	}}
	if _, err := eng.Sync(context.Background(), opts); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if starts != 1 || dones != 1 {
		t.Fatalf("progress events: starts=%d dones=%d, want 1/1", starts, dones)
	}
}

func TestEngineSyncEntry_UnknownEntryErrors(t *testing.T) {
	eng, _ := syncFixture(t, syncRepoBody)

	if _, err := eng.SyncEntry(context.Background(), "does-not-exist", dots.SyncOptions{}); err == nil {
		t.Fatal("SyncEntry with an unknown name should error")
	}
}

func TestEngineSync_RestowFailureSurfacesError(t *testing.T) {
	eng, home := syncFixtureExec(t, syncRepoBody, stowFailExecutor{executor.New()})

	if _, err := eng.Sync(context.Background(), dots.SyncOptions{}); err == nil {
		t.Fatal("Sync should surface a failing restow")
	}
	if _, err := os.Lstat(targetPath(home)); !os.IsNotExist(err) {
		t.Fatalf("failed restow left a target behind: lstat err = %v", err)
	}
}

func TestEngineSync_UseRepoRestowFailureRestoresLocal(t *testing.T) {
	eng, home := syncFixtureExec(t, syncRepoBody, stowFailExecutor{executor.New()})
	target := targetPath(home)
	writeConflictTarget(t, eng, home)

	if _, err := eng.Sync(context.Background(), dots.SyncOptions{ConflictStrategy: "use_repo"}); err == nil {
		t.Fatal("use_repo over a failing restow should error")
	}
	// Rollback must leave the original local file in place, not a dangling link.
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("lstat target after rollback: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("rollback left a symlink instead of restoring the local file")
	}
	got, _ := os.ReadFile(target)
	if string(got) != syncLocalBody {
		t.Fatalf("restored local content = %q, want %q", got, syncLocalBody)
	}
}

func TestEngineSync_UseLocalRestowFailureRollsBackSource(t *testing.T) {
	eng, home := syncFixtureExec(t, syncRepoBody, stowFailExecutor{executor.New()})
	writeConflictTarget(t, eng, home)

	if _, err := eng.Sync(context.Background(), dots.SyncOptions{ConflictStrategy: "use_local"}); err == nil {
		t.Fatal("use_local over a failing restow should error")
	}
	// The repo source must roll back to its original body rather than keep the
	// half-adopted local content.
	src := filepath.Join(eng.RepoPath, syncPackageDir, syncTargetRel)
	got, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read repo source after rollback: %v", err)
	}
	if string(got) != syncRepoBody {
		t.Fatalf("repo source after failed use_local = %q, want rolled back to %q", got, syncRepoBody)
	}
}

func TestEngineSync_RequiresExecutor(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	entries := []config.DotEntry{{Name: syncEntryName, Path: filepath.Join(home, syncTargetRel)}}
	// No WithExecutor: mutating Sync must refuse rather than nil-panic.
	eng, err := dots.NewEngine(filepath.Join(tmp, "repo", "dotfiles"), entries)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if _, err := eng.Sync(context.Background(), dots.SyncOptions{}); err == nil {
		t.Fatal("Sync without an executor should error")
	}
}

func TestNewEngine_RejectsIntermediateSymlinkOutsideHome(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	outside := filepath.Join(tmp, "sibling")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	target := filepath.Join(outside, "settings.json")
	if err := os.WriteFile(target, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, ".config")); err != nil {
		t.Fatal(err)
	}

	_, err := dots.NewEngine(filepath.Join(tmp, "repo"), []config.DotEntry{{
		Name: "settings",
		Path: filepath.Join(home, ".config", "settings.json"),
	}})
	if err == nil {
		t.Fatal("NewEngine accepted a target through an intermediate symlink outside HOME")
	}
	if got, readErr := os.ReadFile(target); readErr != nil || string(got) != "outside" {
		t.Fatalf("outside file changed: body=%q err=%v", got, readErr)
	}
}

func TestNewEngine_AllowsFinalManagedSymlink(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	targetParent := filepath.Join(home, ".config")
	source := filepath.Join(tmp, "repo", "settings", ".config", "settings.json")
	if err := os.MkdirAll(targetParent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("managed"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	target := filepath.Join(targetParent, "settings.json")
	if err := os.Symlink(source, target); err != nil {
		t.Fatal(err)
	}

	if _, err := dots.NewEngine(filepath.Join(tmp, "repo"), []config.DotEntry{{Name: "settings", Path: target}}); err != nil {
		t.Fatalf("NewEngine rejected final managed symlink: %v", err)
	}
}

func TestNewEngine_PreservesHomeTargetCompatibility(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	if _, err := dots.NewEngine(filepath.Join(tmp, "repo"), []config.DotEntry{{Name: "home", Path: home}}); err != nil {
		t.Fatalf("NewEngine rejected target equal to HOME: %v", err)
	}
}
