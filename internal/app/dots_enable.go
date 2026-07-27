package app

import (
	"context"
	"fmt"

	"github.com/lkshrk/omni/internal/dots"
)

type DisableDotsOptions struct {
	// When false those files are left in place and an OpUnlinkConflict is recorded.
	ConflictOverwrite bool
	KeepExistingLocal bool
	RemoveLocal       bool
}

// DotsDisable — Does not remove dots entries from config; clear DotsRepo via SaveSettings to stop tracking.
func (a *App) DotsDisable(opts DisableDotsOptions) ([]dots.Op, error) {
	if err := a.requireSafeTestHomeForDots(); err != nil {
		return nil, err
	}
	m, _, _, err := a.buildDotsManager()
	if err != nil {
		return nil, fmt.Errorf("dots disable: %w", err)
	}
	return m.UnlinkAll(dots.UnlinkOptions{
		ConflictOverwrite: opts.ConflictOverwrite,
		KeepExistingLocal: opts.KeepExistingLocal,
		RemoveLocal:       opts.RemoveLocal,
	})
}

// DisableDotsForHost — Managed symlinks are first replaced with real local copies when a repo is configured.
func (a *App) DisableDotsForHost(ctx context.Context, opts DisableDotsOptions) (ops []dots.Op, err error) {
	repoPath := ""
	if a.DotsConfigured() {
		if resolved, resolveErr := resolveRepoPath(a.dotsRepoPath()); resolveErr == nil {
			repoPath = resolved
		}
	}
	defer func() {
		a.recordDotsHistoryResult(ctx, "disable", "", repoPath, ops, err, false)
	}()
	var disableErr error
	if a.DotsConfigured() {
		ops, disableErr = a.DotsDisable(opts)
	}
	if saveErr := a.SaveDotsDisabled(ctx, true); saveErr != nil {
		if disableErr != nil {
			return ops, fmt.Errorf("%v; save dots disabled flag: %w", disableErr, saveErr)
		}
		return ops, fmt.Errorf("save dots disabled flag: %w", saveErr)
	}
	return ops, disableErr
}

// EnableDotsForHost — Runs a sync immediately when dots_repo is configured, so managed symlinks are restored.
func (a *App) EnableDotsForHost(ctx context.Context) (ops []dots.Op, err error) {
	repoPath := ""
	if a.DotsConfigured() {
		if resolved, resolveErr := resolveRepoPath(a.dotsRepoPath()); resolveErr == nil {
			repoPath = resolved
		}
	}
	defer func() {
		a.recordDotsHistoryResult(ctx, "enable", "", repoPath, ops, err, false)
	}()
	if saveErr := a.SaveDotsDisabled(ctx, false); saveErr != nil {
		return nil, fmt.Errorf("save dots enabled flag: %w", saveErr)
	}
	if !a.DotsConfigured() {
		return nil, nil
	}
	ops, err = a.DotsSyncContext(suppressDotsHistory(ctx), dots.SyncOptions{})
	if err != nil {
		return ops, fmt.Errorf("dots sync: %w", err)
	}
	return ops, nil
}
