package app

import (
	"context"
	"fmt"

	"github.com/lkshrk/omni/internal/dots"
)

func (a *App) DotsPull(ctx context.Context) (ops []dots.Op, err error) {
	pf, err := a.dotService().preflight()
	if err != nil {
		return nil, err
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

// DotsPush stages all changes in the dots repo, commits, and pushes.
// When message is empty the commit message is auto-generated from the git
// status of the repo (e.g. "dots: update nvim, zshrc").
func (a *App) DotsPush(ctx context.Context, message string) (err error) {
	pf, err := a.dotService().preflight()
	if err != nil {
		return err
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

// DotsCommit stages and commits all changes in the dots repo without pushing.
// When message is empty the commit message is auto-generated from the git
// status of the repo (e.g. "dots: update nvim, zshrc").
func (a *App) DotsCommit(ctx context.Context, message string) (err error) {
	pf, err := a.dotService().preflight()
	if err != nil {
		return err
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
