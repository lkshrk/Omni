package app

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/dots"
)

// DotsSync creates or repairs all symlinks for all dots entries across active
// groups. When the current host has assigned groups, only the host group and
// assigned reusable groups are synced. Falls back to all groups when no active
// host is configured.
// All entries are managed via GNU Stow.
func (a *App) DotsSync(opts dots.SyncOptions) ([]dots.Op, error) {
	return a.DotsSyncContext(context.Background(), opts)
}

// DotsSyncContext creates or repairs all symlinks like DotsSync, honoring ctx
// for provider/stow commands.
func (a *App) DotsSyncContext(ctx context.Context, opts dots.SyncOptions) (ops []dots.Op, err error) {
	rootCfg, err := a.loadConfig()
	if err != nil {
		return nil, fmt.Errorf("dots sync: load config: %w", err)
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
		historyCtx := ctx
		if opts.SuppressUnchangedHistory {
			historyCtx = suppressUnchangedDotsHistory(historyCtx)
		}
		a.recordDotsHistoryResult(historyCtx, "sync", "", repoPath, ops, err, opts.DryRun)
		a.refreshDotsStateAfterSuccess(ctx, &err, opts.DryRun)
	}()
	stowPath := dotsContentPath(repoPath)
	if !opts.DryRun {
		var err error
		stowPath, err = ensureDotsContentPath(repoPath)
		if err != nil {
			return nil, fmt.Errorf("dots sync: content dir: %w", err)
		}
	}
	groups := rootCfg.Groups
	if effective, _, ok := effectiveHostGroups(rootCfg, groups, currentMachineGroupName()); ok {
		groups = effective
	}
	entries := collectDots(rootCfg, groups)
	entries = resolveDotEntryPackagesForCurrentHost(entries)
	entries = filterActiveDotEntries(entries)
	if len(entries) == 0 {
		return nil, nil
	}
	if err := a.requireSafeTestDotsMutation(repoPath, entries); err != nil {
		return nil, err
	}

	if err := a.requireStow(ctx); err != nil {
		return nil, err
	}
	mgr, err := dots.New(stowPath, entries)
	if err != nil {
		return nil, fmt.Errorf("dots sync: resolve entries: %w", err)
	}
	orderedEntries := orderResolvedDotEntries(mgr.Entries, opts.EntryOrder)
	var failures []dotSyncFailure
	total := len(orderedEntries)
	for i, entry := range orderedEntries {
		if opts.Progress != nil {
			opts.Progress(dots.SyncProgressEvent{Entry: entry.Name, Index: i + 1, Total: total})
		}
		entryOps, syncErr := syncResolvedDotEntry(ctx, a.newExecutor(), repoPath, stowPath, entry, opts, false)
		if opts.Progress != nil {
			opts.Progress(dots.SyncProgressEvent{Entry: entry.Name, Index: i + 1, Total: total, Done: true, Err: syncErr, Ops: entryOps})
		}
		ops = append(ops, entryOps...)
		if syncErr != nil {
			failures = append(failures, dotSyncFailure{entry: entry.Name, err: syncErr})
		}
	}
	if len(failures) > 0 {
		return ops, dotSyncFailures(failures)
	}
	return ops, nil
}

func orderResolvedDotEntries(entries []dots.ResolvedEntry, order []string) []dots.ResolvedEntry {
	if len(entries) == 0 || len(order) == 0 {
		return entries
	}
	rank := make(map[string]int, len(order))
	for i, name := range order {
		if name == "" {
			continue
		}
		if _, exists := rank[name]; !exists {
			rank[name] = i
		}
	}
	if len(rank) == 0 {
		return entries
	}
	ordered := append([]dots.ResolvedEntry(nil), entries...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left, leftOK := rank[ordered[i].Name]
		right, rightOK := rank[ordered[j].Name]
		switch {
		case leftOK && rightOK:
			return left < right
		case leftOK:
			return true
		case rightOK:
			return false
		default:
			return false
		}
	})
	return ordered
}

type dotSyncFailure struct {
	entry string
	err   error
}

type dotSyncFailures []dotSyncFailure

func (f dotSyncFailures) Error() string {
	parts := make([]string, 0, len(f))
	for _, failure := range f {
		parts = append(parts, fmt.Sprintf("%s: %v", failure.entry, failure.err))
	}
	return "dots sync: " + strings.Join(parts, "; ")
}

// DotsSyncEntry syncs one configured dots entry when the classifier reports a
// single safe action. Choice-based conflicts are reported but not resolved.
func (a *App) DotsSyncEntry(ctx context.Context, name string, opts dots.SyncOptions) (ops []dots.Op, err error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("dots sync: entry name is required")
	}
	rootCfg, err := a.loadConfig()
	if err != nil {
		return nil, fmt.Errorf("dots sync: load config: %w", err)
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
		historyCtx := ctx
		if opts.SuppressUnchangedHistory {
			historyCtx = suppressUnchangedDotsHistory(historyCtx)
		}
		a.recordDotsHistoryResult(historyCtx, "sync entry", name, repoPath, ops, err, opts.DryRun)
		a.refreshDotsStateAfterSuccess(ctx, &err, opts.DryRun)
	}()
	stowPath := dotsContentPath(repoPath)
	if !opts.DryRun {
		var err error
		stowPath, err = ensureDotsContentPath(repoPath)
		if err != nil {
			return nil, fmt.Errorf("dots sync %q: content dir: %w", name, err)
		}
	}
	groups := rootCfg.Groups
	if effective, _, ok := effectiveHostGroups(rootCfg, groups, currentMachineGroupName()); ok {
		groups = effective
	}
	entries := collectDots(rootCfg, groups)
	entries = resolveDotEntryPackagesForCurrentHost(entries)
	entries = filterActiveDotEntries(entries)
	if err := a.requireSafeTestDotsMutation(repoPath, entries); err != nil {
		return nil, err
	}
	mgr, err := dots.New(stowPath, entries)
	if err != nil {
		return nil, fmt.Errorf("dots sync: resolve entries: %w", err)
	}
	for _, entry := range mgr.Entries {
		if entry.Name != name {
			continue
		}
		if err := a.requireStow(ctx); err != nil {
			return nil, err
		}
		if opts.Progress != nil {
			opts.Progress(dots.SyncProgressEvent{Entry: entry.Name, Index: 1, Total: 1})
		}
		ops, syncErr := syncResolvedDotEntry(ctx, a.newExecutor(), repoPath, stowPath, entry, opts, true)
		if opts.Progress != nil {
			opts.Progress(dots.SyncProgressEvent{Entry: entry.Name, Index: 1, Total: 1, Done: true, Err: syncErr, Ops: ops})
		}
		if syncErr != nil {
			return ops, fmt.Errorf("dots sync %q: %w", name, syncErr)
		}
		return ops, nil
	}
	return nil, fmt.Errorf("dots entry %q not found", name)
}
