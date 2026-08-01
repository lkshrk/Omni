package dots_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
		executor.MockCall{},
		executor.MockCall{Stdout: " M x"},
		executor.MockCall{},
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
	for _, call := range mock.Calls {
		if len(call.Env) != 0 {
			t.Fatalf("explicit commit received an environment overlay: %v", call.Env)
		}
	}
}

func TestGit_BackupAll_PreservesHeadIndexAndWorktree(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "config", "user.email", "user@example.com")
	runGit(t, repo, "config", "user.name", "User")
	writeGitTestFile(t, repo, "tracked.txt", "base\n")
	writeGitTestFile(t, repo, "deleted.txt", "delete me\n")
	writeGitTestFile(t, repo, ".gitignore", "secret.txt\n")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-m", "initial")

	writeGitTestFile(t, repo, "tracked.txt", "staged\n")
	runGit(t, repo, "add", "tracked.txt")
	writeGitTestFile(t, repo, "tracked.txt", "worktree\n")
	writeGitTestFile(t, repo, "untracked.txt", "new\n")
	writeGitTestFile(t, repo, "secret.txt", "forced ignored\n")
	runGit(t, repo, "add", "-f", "secret.txt")
	if err := os.Remove(filepath.Join(repo, "deleted.txt")); err != nil {
		t.Fatal(err)
	}

	headBefore := gitOutput(t, repo, "rev-parse", "HEAD")
	branchBefore := gitOutput(t, repo, "symbolic-ref", "HEAD")
	indexBefore := gitOutput(t, repo, "ls-files", "--stage")
	statusBefore := gitOutput(t, repo, "status", "--porcelain=v1")
	indexPath := gitOutput(t, repo, "rev-parse", "--git-path", "index")
	if !filepath.IsAbs(indexPath) {
		indexPath = filepath.Join(repo, indexPath)
	}
	indexBytesBefore, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}

	g := dots.NewGit(repo, executor.New())
	if err := g.BackupAll(context.Background(), "dots: pre-sync nvim"); err != nil {
		t.Fatalf("BackupAll: %v", err)
	}

	if got := gitOutput(t, repo, "rev-parse", "HEAD"); got != headBefore {
		t.Fatalf("HEAD = %q, want %q", got, headBefore)
	}
	if got := gitOutput(t, repo, "symbolic-ref", "HEAD"); got != branchBefore {
		t.Fatalf("branch = %q, want %q", got, branchBefore)
	}
	if got := gitOutput(t, repo, "ls-files", "--stage"); got != indexBefore {
		t.Fatalf("index changed:\n%s\nwant:\n%s", got, indexBefore)
	}
	indexBytesAfter, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(indexBytesAfter, indexBytesBefore) {
		t.Fatal("real git index bytes changed")
	}
	if got := gitOutput(t, repo, "status", "--porcelain=v1"); got != statusBefore {
		t.Fatalf("worktree status changed:\n%s\nwant:\n%s", got, statusBefore)
	}
	if got := gitOutput(t, repo, "show", "omni/backup:tracked.txt"); got != "worktree" {
		t.Fatalf("backup tracked.txt = %q, want worktree", got)
	}
	if got := gitOutput(t, repo, "show", "omni/backup:untracked.txt"); got != "new" {
		t.Fatalf("backup untracked.txt = %q, want new", got)
	}
	if got := gitOutput(t, repo, "show", "omni/backup:secret.txt"); got != "forced ignored" {
		t.Fatalf("backup force-staged ignored secret.txt = %q", got)
	}
	if got := gitOutput(t, repo, "ls-tree", "--name-only", "omni/backup", "deleted.txt"); got != "" {
		t.Fatalf("deleted.txt remains in backup: %q", got)
	}
	body := gitOutput(t, repo, "show", "-s", "--format=%B", "omni/backup")
	if !strings.Contains(body, "dots: pre-sync nvim") || !strings.Contains(body, "Omni-Backup: true") {
		t.Fatalf("backup message missing metadata: %q", body)
	}
}

func TestGit_BackupAll_SupportsUnbornHeadAndChainsHistory(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	writeGitTestFile(t, repo, "one.txt", "one\n")
	g := dots.NewGit(repo, executor.New())

	if err := g.BackupAll(context.Background(), "dots: pre-purge one"); err != nil {
		t.Fatalf("first BackupAll: %v", err)
	}
	first := gitOutput(t, repo, "rev-parse", "refs/heads/omni/backup")
	if _, _, err := executor.New().Run(context.Background(), "git", "-C", repo, "rev-parse", "--verify", "HEAD"); err == nil {
		t.Fatal("BackupAll created the user's unborn HEAD")
	}
	if got := gitOutput(t, repo, "show", "omni/backup:one.txt"); got != "one" {
		t.Fatalf("first backup one.txt = %q", got)
	}

	writeGitTestFile(t, repo, "two.txt", "two\n")
	if err := g.BackupAll(context.Background(), "dots: pre-purge two"); err != nil {
		t.Fatalf("second BackupAll: %v", err)
	}
	second := gitOutput(t, repo, "rev-parse", "refs/heads/omni/backup")
	if second == first {
		t.Fatal("second backup did not advance the backup branch")
	}
	if got := gitOutput(t, repo, "rev-parse", second+"^"); got != first {
		t.Fatalf("second backup parent = %q, want %q", got, first)
	}
	if err := g.BackupAll(context.Background(), "duplicate"); err != nil {
		t.Fatalf("duplicate BackupAll: %v", err)
	}
	if got := gitOutput(t, repo, "rev-parse", "refs/heads/omni/backup"); got != second {
		t.Fatalf("duplicate backup advanced ref to %q, want %q", got, second)
	}
}

func TestGit_BackupAll_RefusesUserOwnedBackupBranch(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "config", "user.email", "user@example.com")
	runGit(t, repo, "config", "user.name", "User")
	writeGitTestFile(t, repo, "tracked.txt", "base\n")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-m", "initial")
	runGit(t, repo, "branch", "omni/backup")
	writeGitTestFile(t, repo, "tracked.txt", "dirty\n")
	before := gitOutput(t, repo, "rev-parse", "refs/heads/omni/backup")

	err := dots.NewGit(repo, executor.New()).BackupAll(context.Background(), "must not overwrite")
	if err == nil || !strings.Contains(err.Error(), "not managed by omni") {
		t.Fatalf("BackupAll error = %v, want user-owned branch refusal", err)
	}
	if got := gitOutput(t, repo, "rev-parse", "refs/heads/omni/backup"); got != before {
		t.Fatalf("user branch moved to %q, want %q", got, before)
	}
}

func TestGit_BackupAll_RefusesCheckedOutBackupBranch(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "config", "user.email", "user@example.com")
	runGit(t, repo, "config", "user.name", "User")
	writeGitTestFile(t, repo, "tracked.txt", "base\n")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-m", "initial")
	writeGitTestFile(t, repo, "tracked.txt", "first backup\n")
	g := dots.NewGit(repo, executor.New())
	if err := g.BackupAll(context.Background(), "first backup"); err != nil {
		t.Fatalf("first BackupAll: %v", err)
	}
	runGit(t, repo, "restore", "tracked.txt")
	runGit(t, repo, "checkout", "omni/backup")
	writeGitTestFile(t, repo, "tracked.txt", "must not advance\n")
	before := gitOutput(t, repo, "rev-parse", "refs/heads/omni/backup")

	err := g.BackupAll(context.Background(), "must not overwrite checked out branch")
	if err == nil || !strings.Contains(err.Error(), "while it is checked out") {
		t.Fatalf("BackupAll error = %v, want checked-out branch refusal", err)
	}
	if got := gitOutput(t, repo, "rev-parse", "refs/heads/omni/backup"); got != before {
		t.Fatalf("checked-out backup branch moved to %q, want %q", got, before)
	}
}

func TestGit_BackupAll_IgnoresInheritedRepoRoutingEnvironment(t *testing.T) {
	repo := t.TempDir()
	other := t.TempDir()
	for _, dir := range []string{repo, other} {
		runGit(t, dir, "init", "-b", "main")
		runGit(t, dir, "config", "user.email", "user@example.com")
		runGit(t, dir, "config", "user.name", "User")
		writeGitTestFile(t, dir, "tracked.txt", "base\n")
		runGit(t, dir, "add", "-A")
		runGit(t, dir, "commit", "-m", "initial")
	}
	writeGitTestFile(t, repo, "tracked.txt", "intended repo\n")

	oldGitDir, hadGitDir := os.LookupEnv("GIT_DIR")
	if err := os.Setenv("GIT_DIR", filepath.Join(other, ".git")); err != nil {
		t.Fatal(err)
	}
	err := dots.NewGit(repo, executor.New()).BackupAll(context.Background(), "route safely")
	if hadGitDir {
		_ = os.Setenv("GIT_DIR", oldGitDir)
	} else {
		_ = os.Unsetenv("GIT_DIR")
	}
	if err != nil {
		t.Fatalf("BackupAll: %v", err)
	}
	if got := gitOutput(t, repo, "show", "omni/backup:tracked.txt"); got != "intended repo" {
		t.Fatalf("intended backup = %q", got)
	}
	if _, _, err := executor.New().Run(context.Background(), "git", "-C", other, "rev-parse", "--verify", "refs/heads/omni/backup"); err == nil {
		t.Fatal("backup ref was created in the repository selected by inherited GIT_DIR")
	}
}

func TestGit_BackupAll_ConcurrentCallsDoNotLoseHistory(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "config", "user.email", "user@example.com")
	runGit(t, repo, "config", "user.name", "User")
	writeGitTestFile(t, repo, "tracked.txt", "base\n")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-m", "initial")
	writeGitTestFile(t, repo, "tracked.txt", "dirty\n")

	exec := newUpdateRefBarrierExecutor(executor.New())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			errs <- dots.NewGit(repo, exec).BackupAll(ctx, "concurrent backup")
		}()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent BackupAll: %v", err)
		}
	}
	if got := gitOutput(t, repo, "rev-list", "--count", "HEAD..omni/backup"); got != "1" {
		t.Fatalf("backup commits = %s, want one shared snapshot", got)
	}
}

func TestGit_CommitAll_NothingToCommit(t *testing.T) {
	g, mock := newMockGit(t,
		executor.MockCall{},           // add -A
		executor.MockCall{Stdout: ""}, // status --porcelain → empty
	)
	if err := g.CommitAll(context.Background(), "dots: add nvim"); err != nil {
		t.Fatalf("CommitAll: %v", err)
	}
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
	for _, call := range mock.Calls {
		if len(call.Env) != 0 {
			t.Fatalf("explicit push received an environment overlay: %v", call.Env)
		}
	}
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
	g, _ := newMockGit(t,
		executor.MockCall{Err: fmt.Errorf("disk full")}, // add -A fails
	)
	if err := g.Push(context.Background(), "msg"); err == nil {
		t.Error("expected error when CommitAll fails inside Push")
	}
}

// Args layout is ["-C", repoPath, wantArgs...]; the prefix is ignored.
func assertGitArgs(t *testing.T, call executor.MockCall, wantArgs ...string) {
	t.Helper()
	if call.Name != "git" {
		t.Errorf("command = %q, want git", call.Name)
	}
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

func writeGitTestFile(t *testing.T, repo, name, content string) {
	t.Helper()
	path := filepath.Join(repo, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitOutput(t *testing.T, repo string, args ...string) string {
	t.Helper()
	stdout, stderr, err := executor.New().Run(context.Background(), "git", append([]string{"-C", repo}, args...)...)
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, stderr)
	}
	return strings.TrimSpace(stdout)
}

type updateRefBarrierExecutor struct {
	next    executor.Executor
	mu      sync.Mutex
	reached int
	release chan struct{}
}

func newUpdateRefBarrierExecutor(next executor.Executor) *updateRefBarrierExecutor {
	return &updateRefBarrierExecutor{next: next, release: make(chan struct{})}
}

func (e *updateRefBarrierExecutor) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	if err := e.waitForBothUpdates(ctx, name, args); err != nil {
		return "", "", err
	}
	return e.next.Run(ctx, name, args...)
}

func (e *updateRefBarrierExecutor) RunEnv(ctx context.Context, env []string, name string, args ...string) (string, string, error) {
	if err := e.waitForBothUpdates(ctx, name, args); err != nil {
		return "", "", err
	}
	return executor.RunWithEnv(ctx, e.next, env, name, args...)
}

func (e *updateRefBarrierExecutor) waitForBothUpdates(ctx context.Context, name string, args []string) error {
	if name != "git" || !containsGitArg(args, "update-ref") {
		return nil
	}
	e.mu.Lock()
	e.reached++
	if e.reached == 2 {
		close(e.release)
	}
	release := e.release
	e.mu.Unlock()
	select {
	case <-release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func containsGitArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
