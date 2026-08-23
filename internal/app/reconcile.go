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

type ReconcileOptions struct {
	BackupMessage  string
	SkipPrivileged bool
	Force          bool
	Progress       func(string)
	ToolProgress   func(isync.ProgressEvent)
}

type ReconcileResult struct {
	NvmManaged         *NvmManagedMigrationBatchResult
	SyncAll            *SyncAllResult
	UpgradeAll         *UpgradeAllResult
	Agents             *AgentsSyncAllResult
	FixedIgnoreEntries []string
	DotsOps            []dots.Op
	DotsBackedUp       bool
	DotsSkipped        string
	DotsEntries        []DotStatus // post-reconcile dots health snapshot
}

type ReconcileSummary struct {
	NvmManaged   int
	NvmRemoved   int
	Installed    int
	Claimed      int
	Upgraded     int
	Quarantined  int
	DotOps       int
	DotsBackedUp bool
	DotsSkipped  string
}

type ReconcileIssueSummary struct {
	NvmFailures     int
	SyncFailures    int
	UpgradeFailures int
	DotsConflicts   int
	DotsMissing     int
}

func SummarizeReconcile(result *ReconcileResult) ReconcileSummary {
	if result == nil {
		return ReconcileSummary{}
	}
	syncSummary := SummarizeSyncAll(result.SyncAll)
	summary := ReconcileSummary{
		Installed:    syncSummary.Installed,
		Claimed:      syncSummary.Claimed,
		DotOps:       len(result.DotsOps),
		DotsBackedUp: result.DotsBackedUp,
		DotsSkipped:  result.DotsSkipped,
	}
	if result.NvmManaged != nil {
		for _, item := range result.NvmManaged.Items {
			if item.Removed {
				summary.NvmRemoved++
			} else {
				summary.NvmManaged++
			}
		}
	}
	if result.UpgradeAll != nil {
		upgradeSummary := SummarizeUpgradeAll(result.UpgradeAll)
		summary.Upgraded = upgradeSummary.Upgraded
		summary.Quarantined = upgradeSummary.Quarantined
	}
	return summary
}

func SummarizeReconcileIssues(result *ReconcileResult) ReconcileIssueSummary {
	if result == nil {
		return ReconcileIssueSummary{}
	}
	var summary ReconcileIssueSummary
	if result.NvmManaged != nil {
		summary.NvmFailures = len(result.NvmManaged.Failures)
	}
	if result.SyncAll != nil {
		summary.SyncFailures = len(result.SyncAll.Failures)
	}
	if result.UpgradeAll != nil {
		summary.UpgradeFailures = len(result.UpgradeAll.Failures)
	}
	for _, entry := range result.DotsEntries {
		switch entry.Health {
		case HealthConflict:
			summary.DotsConflicts++
		case HealthMissing, HealthNoSource:
			summary.DotsMissing++
		}
	}
	return summary
}

func (s ReconcileIssueSummary) Total() int {
	return s.NvmFailures + s.SyncFailures + s.UpgradeFailures +
		s.DotsConflicts + s.DotsMissing
}

func (s ReconcileIssueSummary) HasIssues() bool {
	return s.Total() > 0
}

// Reconcile — Sync tools, upgrade, install the global APM workspace, sync dotfiles, then back up dirty repo state.
func (a *App) Reconcile(ctx context.Context, opts ReconcileOptions) (*ReconcileResult, error) {
	result := &ReconcileResult{}
	var errs []error

	a.reconcileProgress(opts, "fixing nvm-managed tools...")
	nvmResult, err := a.MigrateAllNvmManagedTools(ctx)
	result.NvmManaged = nvmResult
	if err != nil {
		errs = append(errs, fmt.Errorf("fix nvm-managed tools: %w", err))
	}

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
		Force:          opts.Force,
	})
	result.UpgradeAll = upgradeResult
	if err != nil {
		errs = append(errs, fmt.Errorf("upgrade tools: %w", err))
	}

	if err := a.reconcileAgents(ctx, opts, result); err != nil {
		errs = append(errs, fmt.Errorf("sync agents: %w", err))
	}

	fixedIgnoreEntries, err := a.DotsFixIgnorePatterns()
	result.FixedIgnoreEntries = fixedIgnoreEntries
	if err != nil {
		errs = append(errs, fmt.Errorf("fix dotfile ignore patterns: %w", err))
	} else if len(fixedIgnoreEntries) > 0 {
		a.reconcileProgress(opts, "fixed dotfile ignore patterns: "+strings.Join(fixedIgnoreEntries, ", "))
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
		a.reconcileProgress(opts, "backing up dotfile changes...")
		message := opts.BackupMessage
		if strings.TrimSpace(message) == "" {
			message = "dots: reconcile"
		}
		if err := a.DotsBackup(ctx, message); err != nil {
			errs = append(errs, fmt.Errorf("backup dotfiles: %w", err))
		} else {
			result.DotsBackedUp = true
		}
	}

	return result, errors.Join(errs...)
}

// Agent installation is global APM state; deployment targeting belongs in ~/.apm/apm.yml.
func (a *App) reconcileAgents(ctx context.Context, opts ReconcileOptions, result *ReconcileResult) error {
	a.reconcileProgress(opts, "syncing agents...")
	res, err := a.AgentsSyncAll(ctx, AgentsSyncAllOptions{Progress: opts.Progress})
	result.Agents = &res
	return err
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
