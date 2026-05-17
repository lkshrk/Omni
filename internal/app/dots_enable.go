package app

import (
	"context"
	"fmt"

	"github.com/lkshrk/omni/internal/dots"
)

type DisableDotsOptions struct {
	// ConflictOverwrite, when true, moves any real (non-managed) files at target
	// paths to trash and replaces them with the repo version. When false those
	// files are left in place and an OpUnlinkConflict is recorded.
	ConflictOverwrite bool
	// KeepExistingLocal, when true, leaves real non-managed local files in place
	// instead of recording unlink conflicts.
	KeepExistingLocal bool
	// RemoveLocal removes local real targets via trash, or unlinks local
	// symlinks, instead of materializing repo copies.
	RemoveLocal bool
}

// DotsDisable removes all managed symlinks and replaces them with real file
// copies from the repo. This leaves the user in a clean local state after
// disabling dots management. It does NOT remove dots entries from config —
// call SaveSettings to clear DotsRepo if the user also wants to stop tracking.
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

// DisableDotsForHost disables dots on this machine. When a repo is configured,
// managed symlinks are first replaced with real local copies.
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

// EnableDotsForHost enables dots on this machine. If dots_repo is configured,
// it immediately runs a sync so managed symlinks are restored.
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

// DotsResolveConflict resolves a choice-based conflict for one tracked entry.
// Use-repo backs up the local target, moves it to trash, and restows the repo
// version.
// Use-local commits the current repo state first when the repo source exists,
// copies local content into the repo, then replaces the local target with the
