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

const (
	backupRef    = "refs/heads/omni/backup"
	backupMarker = "Omni-Backup: true"
)

var cleanGitRepoEnv = []string{
	"GIT_DIR",
	"GIT_WORK_TREE",
	"GIT_COMMON_DIR",
	"GIT_NAMESPACE",
	"GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES",
	"GIT_QUARANTINE_PATH",
	"GIT_INDEX_FILE",
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

// BackupAll snapshots the worktree on omni/backup without touching HEAD, the user's index, or the worktree.
func (g *Git) BackupAll(ctx context.Context, message string) error {
	headOID, hasHead, err := g.refOID(ctx, "HEAD")
	if err != nil {
		return err
	}
	backupOID, hasBackup, err := g.refOID(ctx, backupRef)
	if err != nil {
		return err
	}
	if hasBackup {
		if err := g.validateBackupRef(ctx); err != nil {
			return err
		}
	}

	index, err := temporaryIndexPath()
	if err != nil {
		return fmt.Errorf("create temporary git index: %w", err)
	}
	defer func() { _ = os.Remove(index) }()
	env := []string{"GIT_INDEX_FILE=" + index}
	realIndex, stderr, indexErr := g.runBackup(ctx, "rev-parse", "--git-path", "index")
	if indexErr != nil {
		return gitErr("resolve git index", indexErr, stderr)
	}
	realIndex = strings.TrimSpace(realIndex)
	if !filepath.IsAbs(realIndex) {
		realIndex = filepath.Join(g.RepoPath, realIndex)
	}
	indexData, readIndexErr := os.ReadFile(realIndex)
	if readIndexErr == nil {
		if err := os.WriteFile(index, indexData, 0o600); err != nil {
			return fmt.Errorf("copy git index for backup: %w", err)
		}
	} else if !os.IsNotExist(readIndexErr) {
		return fmt.Errorf("read git index for backup: %w", readIndexErr)
	} else if hasHead {
		if _, stderr, readErr := g.runEnv(ctx, env, "read-tree", headOID); readErr != nil {
			return gitErr("seed temporary git index", readErr, stderr)
		}
	} else if _, stderr, readErr := g.runEnv(ctx, env, "read-tree", "--empty"); readErr != nil {
		return gitErr("seed empty temporary git index", readErr, stderr)
	}
	if _, stderr, addErr := g.runEnv(ctx, env, "add", "-A", "--", "."); addErr != nil {
		return gitErr("snapshot dotfiles worktree", addErr, stderr)
	}
	treeOID, stderr, writeErr := g.runEnv(ctx, env, "write-tree")
	if writeErr != nil {
		return gitErr("write omni backup tree", writeErr, stderr)
	}
	treeOID = strings.TrimSpace(treeOID)

	comparisonRef := ""
	if hasBackup {
		comparisonRef = backupRef
	} else if hasHead {
		comparisonRef = headOID
	}
	if comparisonRef != "" {
		currentTree, stderr, treeErr := g.runBackup(ctx, "rev-parse", comparisonRef+"^{tree}")
		if treeErr != nil {
			return gitErr("inspect existing backup tree", treeErr, stderr)
		}
		if strings.TrimSpace(currentTree) == treeOID {
			return nil
		}
	}

	if strings.TrimSpace(message) == "" {
		message = "omni dotfiles backup"
	}
	for attempt := 0; attempt < 2; attempt++ {
		parentOID := backupOID
		if !hasBackup {
			parentOID = headOID
		}
		commitOID, err := g.createBackupCommit(ctx, env, treeOID, parentOID, headOID, message)
		if err != nil {
			return err
		}
		expectedOld := backupOID
		if expectedOld == "" {
			expectedOld = strings.Repeat("0", len(commitOID))
		}
		if _, stderr, updateErr := g.runBackup(ctx, "update-ref", "--create-reflog", backupRef, commitOID, expectedOld); updateErr == nil {
			return nil
		} else if attempt == 1 {
			return gitErr("update omni backup branch", updateErr, stderr)
		}

		latestOID, exists, refErr := g.refOID(ctx, backupRef)
		if refErr != nil {
			return refErr
		}
		if !exists {
			return fmt.Errorf("update omni backup branch: %s disappeared during concurrent backup", backupRef)
		}
		if err := g.validateBackupRef(ctx); err != nil {
			return err
		}
		latestTree, stderr, treeErr := g.runBackup(ctx, "rev-parse", latestOID+"^{tree}")
		if treeErr != nil {
			return gitErr("inspect concurrent backup tree", treeErr, stderr)
		}
		if strings.TrimSpace(latestTree) == treeOID {
			return nil
		}
		backupOID, hasBackup = latestOID, true
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

func (g *Git) runBackup(ctx context.Context, args ...string) (stdout, stderr string, err error) {
	fullArgs := append([]string{"-C", g.RepoPath}, args...)
	return executor.RunWithEnv(ctx, g.Exec, cleanGitRepoEnv, "git", fullArgs...)
}

func (g *Git) runEnv(ctx context.Context, env []string, args ...string) (stdout, stderr string, err error) {
	fullArgs := append([]string{"-C", g.RepoPath}, args...)
	cleanEnv := append(append([]string{}, cleanGitRepoEnv...), env...)
	return executor.RunWithEnv(ctx, g.Exec, cleanEnv, "git", fullArgs...)
}

func (g *Git) validateBackupRef(ctx context.Context) error {
	body, stderr, err := g.runBackup(ctx, "show", "-s", "--format=%B", backupRef)
	if err != nil {
		return gitErr("inspect omni backup branch", err, stderr)
	}
	if !hasLine(body, backupMarker) {
		return fmt.Errorf("%s already exists and is not managed by omni", backupRef)
	}
	worktrees, stderr, err := g.runBackup(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return gitErr("inspect git worktrees", err, stderr)
	}
	if hasLine(worktrees, "branch "+backupRef) {
		return fmt.Errorf("refusing to update %s while it is checked out", backupRef)
	}
	return nil
}

func (g *Git) createBackupCommit(ctx context.Context, env []string, treeOID, parentOID, headOID, message string) (string, error) {
	args := []string{"commit-tree", treeOID}
	if parentOID != "" {
		args = append(args, "-p", parentOID)
	}
	metadata := backupMarker
	if headOID != "" {
		metadata += "\nSource-HEAD: " + headOID
	}
	args = append(args, "-m", message, "-m", metadata)
	identityEnv := append(append([]string{}, env...),
		"GIT_AUTHOR_NAME=omni",
		"GIT_AUTHOR_EMAIL=omni@localhost",
		"GIT_COMMITTER_NAME=omni",
		"GIT_COMMITTER_EMAIL=omni@localhost",
	)
	commitOID, stderr, err := g.runEnv(ctx, identityEnv, args...)
	if err != nil {
		return "", gitErr("create omni backup commit", err, stderr)
	}
	return strings.TrimSpace(commitOID), nil
}

func (g *Git) refOID(ctx context.Context, ref string) (string, bool, error) {
	stdout, stderr, err := g.runBackup(ctx, "rev-parse", "--verify", "--quiet", ref)
	if err != nil {
		if strings.TrimSpace(stdout) == "" && strings.TrimSpace(stderr) == "" {
			return "", false, nil
		}
		return "", false, gitErr("resolve git ref "+ref, err, stderr)
	}
	return strings.TrimSpace(stdout), true, nil
}

func temporaryIndexPath() (string, error) {
	f, err := os.CreateTemp("", "omni-dots-index-*")
	if err != nil {
		return "", err
	}
	path := f.Name()
	if closeErr := f.Close(); closeErr != nil {
		_ = os.Remove(path)
		return "", closeErr
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return path, nil
}

func hasLine(text, want string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

// Appends stderr so callers see git's message, not just an exit code.
func gitErr(op string, err error, stderr string) error {
	if s := strings.TrimSpace(stderr); s != "" {
		return fmt.Errorf("%s: %w\n%s", op, err, s)
	}
	return fmt.Errorf("%s: %w", op, err)
}
