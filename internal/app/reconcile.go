package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/dots"
	isync "github.com/lkshrk/omni/internal/sync"
)

// ReconcileOptions controls the host lifecycle reconcile operation.
type ReconcileOptions struct {
	CommitMessage  string
	SkipPrivileged bool
	Progress       func(string)
	ToolProgress   func(isync.ProgressEvent)
}

// ReconcileResult records the operations attempted by Reconcile.
type ReconcileResult struct {
	SyncAll       *SyncAllResult
	UpgradeAll    *UpgradeAllResult
	DotsOps       []dots.Op
	DotsCommitted bool
	DotsSkipped   string
	DotsEntries   []DotStatus // post-reconcile dots health snapshot
}

// Reconcile brings the current host toward the configured desired state:
// sync tools, upgrade outdated tools, sync dotfiles, then commit dotfile repo
// changes when dots are configured and enabled.
func (a *App) Reconcile(ctx context.Context, opts ReconcileOptions) (*ReconcileResult, error) {
	result := &ReconcileResult{}
	var errs []error

	a.reconcileProgress(opts, "syncing tools...")
	syncResult, err := a.SyncAll(ctx, SyncAllOptions{
		Progress:       opts.Progress,
		ToolProgress:   opts.ToolProgress,
		SkipPrivileged: opts.SkipPrivileged,
	})
	result.SyncAll = syncResult
	if err != nil {
		errs = append(errs, fmt.Errorf("sync tools: %w", err))
	}

	a.reconcileProgress(opts, "upgrading tools...")
	upgradeResult, err := a.UpgradeAllDetailedWithOptions(ctx, opts.Progress, opts.ToolProgress, UpgradeAllOptions{
		SkipPrivileged: opts.SkipPrivileged,
	})
	result.UpgradeAll = upgradeResult
	if err != nil {
		errs = append(errs, fmt.Errorf("upgrade tools: %w", err))
	}

	dotsReady, skipReason, err := a.reconcileDotsReady()
	if err != nil {
		errs = append(errs, fmt.Errorf("inspect dotfiles: %w", err))
		return result, errors.Join(errs...)
	}
	if !dotsReady {
		result.DotsSkipped = skipReason
		return result, errors.Join(errs...)
	}

	a.reconcileProgress(opts, "syncing dotfiles...")
	dotOps, err := a.DotsSyncContext(ctx, dots.SyncOptions{})
	result.DotsOps = dotOps
	if err != nil {
		errs = append(errs, fmt.Errorf("sync dotfiles: %w", err))
	}

	status, err := a.DotsStatus(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("status dotfiles: %w", err))
		return result, errors.Join(errs...)
	}
	result.DotsEntries = status.Entries
	if strings.TrimSpace(status.GitStatus) != "" {
		a.reconcileProgress(opts, "committing dotfiles...")
		if err := a.DotsCommit(ctx, opts.CommitMessage); err != nil {
			errs = append(errs, fmt.Errorf("commit dotfiles: %w", err))
		} else {
			result.DotsCommitted = true
		}
	}

	return result, errors.Join(errs...)
}

func (a *App) reconcileProgress(opts ReconcileOptions, message string) {
	if opts.Progress != nil {
		opts.Progress(message)
	}
}

func (a *App) reconcileDotsReady() (bool, string, error) {
	rootCfg, err := a.loadConfig()
	if err != nil {
		return false, "", err
	}
	settings := a.effectiveSettings(rootCfg)
	switch {
	case strings.TrimSpace(settings.DotsRepo) == "":
		return false, "dotfiles not configured", nil
	case config.BoolVal(settings.DotsDisabled):
		return false, "dotfiles disabled for this host", nil
	default:
		return true, "", nil
	}
}
