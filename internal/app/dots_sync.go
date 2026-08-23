package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/lkshrk/omni/internal/dots"
)

// DotsSync — Falls back to all groups when no active host is configured.
func (a *App) DotsSync(opts dots.SyncOptions) ([]dots.Op, error) {
	return a.DotsSyncContext(context.Background(), opts)
}

func (a *App) DotsSyncContext(ctx context.Context, opts dots.SyncOptions) (ops []dots.Op, err error) {
	pf, err := a.dotService().preflight()
	if err != nil {
		return nil, fmt.Errorf("dots sync: %w", err)
	}
	defer func() {
		historyCtx := ctx
		if opts.SuppressUnchangedHistory {
			historyCtx = suppressUnchangedDotsHistory(historyCtx)
		}
		a.recordDotsHistoryResult(historyCtx, "sync", "", pf.repoPath, ops, err, opts.DryRun)
		a.refreshDotsStateAfterSuccess(ctx, &err, opts.DryRun)
	}()
	engine, err := a.dotService().engineFor(pf, opts.DryRun, true)
	if err != nil {
		return nil, err
	}
	if len(engine.Entries) == 0 {
		return nil, nil
	}
	if err := a.requireStow(ctx); err != nil {
		return nil, err
	}
	return engine.Sync(ctx, opts)
}

// DotsSyncEntry — Choice-based conflicts are reported but not resolved.
func (a *App) DotsSyncEntry(ctx context.Context, name string, opts dots.SyncOptions) (ops []dots.Op, err error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("dots sync: entry name is required")
	}
	pf, err := a.dotService().preflight()
	if err != nil {
		return nil, fmt.Errorf("dots sync: %w", err)
	}
	defer func() {
		historyCtx := ctx
		if opts.SuppressUnchangedHistory {
			historyCtx = suppressUnchangedDotsHistory(historyCtx)
		}
		a.recordDotsHistoryResult(historyCtx, "sync entry", name, pf.repoPath, ops, err, opts.DryRun)
		a.refreshDotsStateAfterSuccess(ctx, &err, opts.DryRun)
	}()
	engine, err := a.dotService().engineFor(pf, opts.DryRun, true)
	if err != nil {
		return nil, err
	}
	// An unknown entry is reported without requiring stow; stow is only required once the entry is found.
	if !engineHasEntry(engine, name) {
		return nil, fmt.Errorf("dots entry %q not found", name)
	}
	if err := a.requireStow(ctx); err != nil {
		return nil, err
	}
	return engine.SyncEntry(ctx, name, opts)
}
