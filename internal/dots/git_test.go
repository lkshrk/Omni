package dots_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/dots"
	"github.com/lkshrk/omni/internal/executor"
)

func newMockGit(t *testing.T, responses ...executor.MockCall) (*dots.Git, *executor.MockExecutor) {
	t.Helper()
	mock := &executor.MockExecutor{Responses: responses}
	repo := t.TempDir()
	return dots.NewGit(repo, mock), mock
}

func TestGit_IsRepo_True(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	g := dots.NewGit(repo, &executor.MockExecutor{})
	if !g.IsRepo() {
		t.Error("expected IsRepo() == true")
	}
}

func TestGit_IsRepo_WorktreeGitFile(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: ../.git/worktrees/repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := dots.NewGit(repo, &executor.MockExecutor{})
	if !g.IsRepo() {
		t.Error("expected IsRepo() == true for worktree .git file")
	}
}

func TestGit_IsRepo_False(t *testing.T) {
	repo := t.TempDir() // no .git
	g := dots.NewGit(repo, &executor.MockExecutor{})
	if g.IsRepo() {
		t.Error("expected IsRepo() == false")
	}
}

func TestGit_Status(t *testing.T) {
	g, mock := newMockGit(t, executor.MockCall{Stdout: " M init.lua\n"})
	status, err := g.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status != "M init.lua" {
		t.Errorf("Status = %q, want %q", status, "M init.lua")
	}
	if len(mock.Calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(mock.Calls))
	}
	assertGitArgs(t, mock.Calls[0], "status", "--short")
}

func TestGit_Pull(t *testing.T) {
	g, mock := newMockGit(t)
	if err := g.Pull(context.Background()); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if len(mock.Calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(mock.Calls))
	}
	assertGitArgs(t, mock.Calls[0], "pull")
}

func TestGit_CommitAll_WithChanges(t *testing.T) {
	g, mock := newMockGit(t,
		executor.MockCall{},               // add -A
		executor.MockCall{Stdout: " M x"}, // status --porcelain → has changes
		executor.MockCall{},               // commit
	)
	if err := g.CommitAll(context.Background(), "dots: add nvim"); err != nil {
		t.Fatalf("CommitAll: %v", err)
	}
	if len(mock.Calls) != 3 {
		t.Fatalf("want 3 calls, got %d", len(mock.Calls))
	}
	assertGitArgs(t, mock.Calls[0], "add", "-A")
	assertGitArgs(t, mock.Calls[1], "status", "--porcelain")
	assertGitArgs(t, mock.Calls[2], "commit", "-m", "dots: add nvim")
}

// TestGit_SnapshotAll_DisablesSigning guards the fix for internal safety
// checkpoints aborting when the user has commit.gpgsign enabled without an
// available signing key: SnapshotAll must pass -c commit.gpgsign=false so the
// pre-resolve/pre-sync snapshot commits cannot be blocked by signing.
func TestGit_SnapshotAll_DisablesSigning(t *testing.T) {
	g, mock := newMockGit(t,
		executor.MockCall{},               // add -A
		executor.MockCall{Stdout: " M x"}, // status --porcelain → has changes
		executor.MockCall{},               // commit
	)
	if err := g.SnapshotAll(context.Background(), "dots: pre-resolve nvim"); err != nil {
		t.Fatalf("SnapshotAll: %v", err)
	}
	if len(mock.Calls) != 3 {
		t.Fatalf("want 3 calls, got %d", len(mock.Calls))
	}
	assertGitArgs(t, mock.Calls[0], "add", "-A")
	assertGitArgs(t, mock.Calls[1], "status", "--porcelain")
	assertGitArgs(t, mock.Calls[2], "-c", "commit.gpgsign=false", "commit", "-m", "dots: pre-resolve nvim")
}

func TestGit_CommitAll_NothingToCommit(t *testing.T) {
	g, mock := newMockGit(t,
		executor.MockCall{},           // add -A
		executor.MockCall{Stdout: ""}, // status --porcelain → empty
	)
	if err := g.CommitAll(context.Background(), "dots: add nvim"); err != nil {
		t.Fatalf("CommitAll: %v", err)
	}
	// No commit call should be made.
	if len(mock.Calls) != 2 {
		t.Fatalf("want 2 calls (add + status), got %d", len(mock.Calls))
	}
}

func TestGit_Push(t *testing.T) {
	g, mock := newMockGit(t,
		executor.MockCall{},               // add -A
		executor.MockCall{Stdout: " M x"}, // status --porcelain
		executor.MockCall{},               // commit
		executor.MockCall{},               // push
	)
	if err := g.Push(context.Background(), "dots: update"); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if len(mock.Calls) != 4 {
		t.Fatalf("want 4 calls, got %d", len(mock.Calls))
	}
	assertGitArgs(t, mock.Calls[3], "push")
}

func TestGit_Status_Error(t *testing.T) {
	g, _ := newMockGit(t, executor.MockCall{Err: fmt.Errorf("permission denied")})
	_, err := g.Status(context.Background())
	if err == nil {
		t.Error("expected error from Status")
	}
}

func TestGit_Status_IncludesStderr(t *testing.T) {
	g, _ := newMockGit(t, executor.MockCall{
		Stderr: "fatal: not a git repository",
		Err:    fmt.Errorf("exit status 128"),
	})
	_, err := g.Status(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); !strings.Contains(got, "fatal: not a git repository") {
		t.Errorf("error %q does not contain stderr output", got)
	}
}

func TestGit_Pull_Error(t *testing.T) {
	g, _ := newMockGit(t, executor.MockCall{Err: fmt.Errorf("network error")})
	if err := g.Pull(context.Background()); err == nil {
		t.Error("expected error from Pull")
	}
}

func TestGit_Pull_IncludesStderr(t *testing.T) {
	g, _ := newMockGit(t, executor.MockCall{
		Stderr: "Permission denied (publickey).",
		Err:    fmt.Errorf("exit status 128"),
	})
	err := g.Pull(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); !strings.Contains(got, "Permission denied (publickey).") {
		t.Errorf("error %q does not contain stderr output", got)
	}
}

func TestGit_CommitAll_AddError(t *testing.T) {
	g, _ := newMockGit(t, executor.MockCall{Err: fmt.Errorf("disk full")})
	if err := g.CommitAll(context.Background(), "msg"); err == nil {
		t.Error("expected error when git add fails")
	}
}

func TestGit_CommitAll_StatusError(t *testing.T) {
	g, _ := newMockGit(t,
		executor.MockCall{}, // add -A succeeds
		executor.MockCall{Err: fmt.Errorf("disk error")}, // status --porcelain fails
	)
	if err := g.CommitAll(context.Background(), "msg"); err == nil {
		t.Error("expected error when git status --porcelain fails")
	}
}

func TestGit_CommitAll_CommitError(t *testing.T) {
	g, _ := newMockGit(t,
		executor.MockCall{},               // add -A
		executor.MockCall{Stdout: " M x"}, // status --porcelain → has changes
		executor.MockCall{Err: fmt.Errorf("commit hook rejected")},
	)
	if err := g.CommitAll(context.Background(), "msg"); err == nil {
		t.Error("expected error when git commit fails")
	}
}

func TestGit_Push_Error(t *testing.T) {
	g, _ := newMockGit(t,
		executor.MockCall{},                            // add -A
		executor.MockCall{Stdout: " M x"},              // status → has changes
		executor.MockCall{},                            // commit
		executor.MockCall{Err: fmt.Errorf("rejected")}, // push error
	)
	if err := g.Push(context.Background(), "msg"); err == nil {
		t.Error("expected error from Push when git push fails")
	}
}

func TestGit_Push_IncludesStderr(t *testing.T) {
	g, _ := newMockGit(t,
		executor.MockCall{},               // add -A
		executor.MockCall{Stdout: " M x"}, // status → has changes
		executor.MockCall{},               // commit
		executor.MockCall{
			Stderr: "remote: Repository not found.",
			Err:    fmt.Errorf("exit status 128"),
		},
	)
	err := g.Push(context.Background(), "msg")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); !strings.Contains(got, "remote: Repository not found.") {
		t.Errorf("error %q does not contain stderr output", got)
	}
}

func TestGit_Push_NothingToCommit(t *testing.T) {
	g, mock := newMockGit(t,
		executor.MockCall{},           // add -A
		executor.MockCall{Stdout: ""}, // status --porcelain → nothing
		// No commit call.
		executor.MockCall{}, // push
	)
	if err := g.Push(context.Background(), "msg"); err != nil {
		t.Fatalf("Push: %v", err)
	}
	// add + status + push = 3 calls (no commit since nothing to commit).
	if len(mock.Calls) != 3 {
		t.Fatalf("want 3 calls, got %d", len(mock.Calls))
	}
	assertGitArgs(t, mock.Calls[2], "push")
}

func TestGit_Push_CommitError(t *testing.T) {
	// CommitAll fails inside Push → Push returns the CommitAll error.
	g, _ := newMockGit(t,
		executor.MockCall{Err: fmt.Errorf("disk full")}, // add -A fails
	)
	if err := g.Push(context.Background(), "msg"); err == nil {
		t.Error("expected error when CommitAll fails inside Push")
	}
}

// assertGitArgs checks that the call used "git" with the expected sub-args
// (ignoring the leading "-C <repo>" prefix added by Git.run).
func assertGitArgs(t *testing.T, call executor.MockCall, wantArgs ...string) {
	t.Helper()
	if call.Name != "git" {
		t.Errorf("command = %q, want git", call.Name)
	}
	// args layout: ["-C", repoPath, wantArgs...]
	if len(call.Args) < 2+len(wantArgs) {
		t.Errorf("args too short: %v", call.Args)
		return
	}
	if call.Args[0] != "-C" {
		t.Errorf("args[0] = %q, want -C", call.Args[0])
	}
	for i, want := range wantArgs {
		if got := call.Args[2+i]; got != want {
			t.Errorf("args[%d] = %q, want %q", 2+i, got, want)
		}
	}
}
