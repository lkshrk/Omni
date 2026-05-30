package app

import (
	"context"
	"fmt"

	"github.com/lkshrk/omni/internal/dots"
	"github.com/lkshrk/omni/internal/executor"
)

func (a *App) DotsPull(ctx context.Context) (ops []dots.Op, err error) {
	rootCfg, err := a.loadConfig()
	if err != nil {
		return nil, fmt.Errorf("dots pull: load config: %w", err)
	}
	if err := a.requireDotsEnabled(rootCfg); err != nil {
		return nil, err
	}
	repoPath, err := resolveRepoPath(a.dotsRepoPath())
	if err != nil {
		return nil, err
	}
	if err := a.requireSafeTestDotsMutation(repoPath, nil); err != nil {
		return nil, err
	}
	defer func() {
		a.recordDotsHistoryResult(ctx, "pull", "", repoPath, ops, err, false)
	}()
	g := newGitForRepo(repoPath, executor.New())
	if err := g.Pull(ctx); err != nil {
		return nil, fmt.Errorf("dots pull: %w", err)
	}
	return a.DotsSyncContext(suppressDotsHistory(ctx), dots.SyncOptions{})
}

// DotsPush stages all changes in the dots repo, commits, and pushes.
// When message is empty the commit message is auto-generated from the git
// status of the repo (e.g. "dots: update nvim, zshrc").
func (a *App) DotsPush(ctx context.Context, message string) (err error) {
	rootCfg, err := a.loadConfig()
	if err != nil {
		return fmt.Errorf("dots push: load config: %w", err)
	}
	if err := a.requireDotsEnabled(rootCfg); err != nil {
		return err
	}
	repoPath, err := resolveRepoPath(a.dotsRepoPath())
	if err != nil {
		return err
	}
	if err := a.requireSafeTestDotsMutation(repoPath, nil); err != nil {
		return err
	}
	defer func() {
		a.recordDotsHistoryResult(ctx, "push", "", repoPath, nil, err, false)
		a.refreshDotsStateAfterSuccess(ctx, &err, false)
	}()
	g := newGitForRepo(repoPath, executor.New())
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
	rootCfg, err := a.loadConfig()
	if err != nil {
		return fmt.Errorf("dots commit: load config: %w", err)
	}
	if err := a.requireDotsEnabled(rootCfg); err != nil {
		return err
	}
	repoPath, err := resolveRepoPath(a.dotsRepoPath())
	if err != nil {
		return err
	}
	if err := a.requireSafeTestDotsMutation(repoPath, nil); err != nil {
		return err
	}
	defer func() {
		a.recordDotsHistoryResult(ctx, "commit", "", repoPath, nil, err, false)
		a.refreshDotsStateAfterSuccess(ctx, &err, false)
	}()
	g := newGitForRepo(repoPath, executor.New())
	if message == "" {
		gitStatus, err := g.Status(ctx)
		if err != nil {
			return fmt.Errorf("dots commit: %w", err)
		}
		message = dots.CommitMessageFromStatus(gitStatus)
	}
	return g.CommitAll(ctx, message)
}
