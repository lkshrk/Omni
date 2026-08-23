package app

import (
	"context"
	"fmt"

	"github.com/lkshrk/omni/internal/dots"
)

func (a *App) DotsPull(ctx context.Context) (ops []dots.Op, err error) {
	pf, err := a.dotService().preflight()
	if err != nil {
		return nil, fmt.Errorf("dots pull: %w", err)
	}
	defer func() {
		a.recordDotsHistoryResult(ctx, "pull", "", pf.repoPath, ops, err, false)
	}()
	g := newGitForRepo(pf.repoPath, a.newExecutor())
	if err := g.Pull(ctx); err != nil {
		return nil, fmt.Errorf("dots pull: %w", err)
	}
	return a.DotsSyncContext(suppressDotsHistory(ctx), dots.SyncOptions{})
}

// DotsPush — An empty message is auto-generated from the repo's git status.
func (a *App) DotsPush(ctx context.Context, message string) (err error) {
	pf, err := a.dotService().preflight()
	if err != nil {
		return fmt.Errorf("dots push: %w", err)
	}
	defer func() {
		a.recordDotsHistoryResult(ctx, "push", "", pf.repoPath, nil, err, false)
		a.refreshDotsStateAfterSuccess(ctx, &err, false)
	}()
	g := newGitForRepo(pf.repoPath, a.newExecutor())
	if message == "" {
		gitStatus, err := g.Status(ctx)
		if err != nil {
			return fmt.Errorf("dots push: %w", err)
		}
		message = dots.CommitMessageFromStatus(gitStatus)
	}
	return g.Push(ctx, message)
}

// DotsCommit — An empty message is auto-generated from the repo's git status.
func (a *App) DotsCommit(ctx context.Context, message string) (err error) {
	pf, err := a.dotService().preflight()
	if err != nil {
		return fmt.Errorf("dots commit: %w", err)
	}
	defer func() {
		a.recordDotsHistoryResult(ctx, "commit", "", pf.repoPath, nil, err, false)
		a.refreshDotsStateAfterSuccess(ctx, &err, false)
	}()
	g := newGitForRepo(pf.repoPath, a.newExecutor())
	if message == "" {
		gitStatus, err := g.Status(ctx)
		if err != nil {
			return fmt.Errorf("dots commit: %w", err)
		}
		message = dots.CommitMessageFromStatus(gitStatus)
	}
	return g.CommitAll(ctx, message)
}

func (a *App) DotsBackup(ctx context.Context, message string) (err error) {
	pf, err := a.dotService().preflight()
	if err != nil {
		return fmt.Errorf("dots backup: %w", err)
	}
	defer func() {
		a.recordDotsHistoryResult(ctx, "backup", "", pf.repoPath, nil, err, false)
	}()
	return newGitForRepo(pf.repoPath, a.newExecutor()).BackupAll(ctx, message)
}
