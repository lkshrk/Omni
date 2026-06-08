package dots

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lkshrk/omni/internal/executor"
)

// Git wraps git operations for the dots repo.
type Git struct {
	RepoPath string
	Exec     executor.Executor
}

// NewGit returns a Git for repoPath using the provided executor.
func NewGit(repoPath string, exec executor.Executor) *Git {
	return &Git{RepoPath: repoPath, Exec: exec}
}

// IsRepo reports whether RepoPath looks like a git checkout. Normal clones use
// a .git directory; worktrees and some submodules use a .git file containing a
// gitdir pointer.
func (g *Git) IsRepo() bool {
	gitPath := filepath.Join(g.RepoPath, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return false
	}
	if info.IsDir() {
		return true
	}
	data, err := os.ReadFile(gitPath)
	return err == nil && strings.HasPrefix(strings.TrimSpace(string(data)), "gitdir:")
}

// Status returns the short git status of the repo.
func (g *Git) Status(ctx context.Context) (string, error) {
	stdout, stderr, err := g.run(ctx, "status", "--short")
	if err != nil {
		return "", gitErr("git status", err, stderr)
	}
	return strings.TrimSpace(stdout), nil
}

// Pull runs git pull in the repo.
func (g *Git) Pull(ctx context.Context) error {
	if _, stderr, err := g.run(ctx, "pull"); err != nil {
		return gitErr("git pull", err, stderr)
	}
	return nil
}

// CommitAll stages all changes and commits with the given message, honouring
// the user's commit-signing config. Returns nil without error if there is
// nothing to commit.
func (g *Git) CommitAll(ctx context.Context, message string) error {
	return g.commitAll(ctx, message, true)
}

// SnapshotAll behaves like CommitAll but disables commit signing. It is for
// omni-internal safety checkpoints (e.g. the pre-resolve/pre-sync snapshots that
// preserve prior repo state before a mutation) which must succeed regardless of
// the user's commit.gpgsign setting or signing-key availability — otherwise a
// missing/locked signing key would abort conflict resolution and dot syncing.
// User-facing commits (dots add/commit/push) still go through CommitAll/Push and
// stay signed.
func (g *Git) SnapshotAll(ctx context.Context, message string) error {
	return g.commitAll(ctx, message, false)
}

func (g *Git) commitAll(ctx context.Context, message string, sign bool) error {
	if _, stderr, err := g.run(ctx, "add", "-A"); err != nil {
		return gitErr("git add", err, stderr)
	}
	stdout, stderr, err := g.run(ctx, "status", "--porcelain")
	if err != nil {
		return gitErr("git status (pre-commit)", err, stderr)
	}
	if strings.TrimSpace(stdout) == "" {
		return nil // nothing to commit
	}
	args := make([]string, 0, 5)
	if !sign {
		args = append(args, "-c", "commit.gpgsign=false")
	}
	args = append(args, "commit", "-m", message)
	if _, stderr, err := g.run(ctx, args...); err != nil {
		return gitErr("git commit", err, stderr)
	}
	return nil
}

// Push stages, commits, and pushes. message is used for the commit.
func (g *Git) Push(ctx context.Context, message string) error {
	if err := g.CommitAll(ctx, message); err != nil {
		return err
	}
	if _, stderr, err := g.run(ctx, "push"); err != nil {
		return gitErr("git push", err, stderr)
	}
	return nil
}

func (g *Git) run(ctx context.Context, args ...string) (stdout, stderr string, err error) {
	fullArgs := append([]string{"-C", g.RepoPath}, args...)
	return g.Exec.Run(ctx, "git", fullArgs...)
}

// gitErr wraps a git error, appending stderr output when non-empty so callers
// see the actual git message rather than just an exit-code error.
func gitErr(op string, err error, stderr string) error {
	if s := strings.TrimSpace(stderr); s != "" {
		return fmt.Errorf("%s: %w\n%s", op, err, s)
	}
	return fmt.Errorf("%s: %w", op, err)
}
