package dots

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lkshrk/omni/internal/executor"
)

type Git struct {
	RepoPath string
	Exec     executor.Executor
}

func NewGit(repoPath string, exec executor.Executor) *Git {
	return &Git{RepoPath: repoPath, Exec: exec}
}

// IsRepo — Worktrees and some submodules use a .git file, not a directory.
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

func (g *Git) Status(ctx context.Context) (string, error) {
	stdout, stderr, err := g.run(ctx, "status", "--short")
	if err != nil {
		return "", gitErr("git status", err, stderr)
	}
	return strings.TrimSpace(stdout), nil
}

func (g *Git) Pull(ctx context.Context) error {
	if _, stderr, err := g.run(ctx, "pull"); err != nil {
		return gitErr("git pull", err, stderr)
	}
	return nil
}

// CommitAll — Honours the user's commit-signing config; a clean tree is not an error.
func (g *Git) CommitAll(ctx context.Context, message string) error {
	return g.commitAll(ctx, message, true)
}

// SnapshotAll — Signing is off so a locked key cannot abort omni's internal safety checkpoints.
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
		return nil
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

// Appends stderr so callers see git's message, not just an exit code.
func gitErr(op string, err error, stderr string) error {
	if s := strings.TrimSpace(stderr); s != "" {
		return fmt.Errorf("%s: %w\n%s", op, err, s)
	}
	return fmt.Errorf("%s: %w", op, err)
}
