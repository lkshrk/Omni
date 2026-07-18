package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/dots"
)

type DotsState struct {
	Entries         []DotStatus
	GitStatus       string
	DotMemberships  map[string][]string
	DiscoveredCount int
	ObservedAt      time.Time
	Loaded          bool
}

type DotsOperationStateResult struct {
	Ops         []dots.Op
	State       *DotsState
	Settings    config.Settings
	HasSettings bool
}

type DotsOperationStateProgressEvent struct {
	Entry    string
	Index    int
	Total    int
	Done     bool
	Text     string
	Err      error
	Ops      []dots.Op
	State    *DotsState
	StateErr error
}

type DotsDiscoveredOperationStateResult struct {
	Added config.DotEntry
	Ops   []dots.Op
	State *DotsState
}

type DotsVariantStateResult struct {
	Info  DotVariantInfo
	Ops   []dots.Op
	State *DotsState
}

func (a *App) RefreshDotsState(ctx context.Context) (*DotsState, error) {
	result, err := a.DiscoverDotsStatus(ctx)
	if result == nil {
		return nil, err
	}
	observedAt := time.Now().UTC()
	state, cacheErr := a.cacheDotsStatusResult(ctx, result, observedAt)
	return state, errors.Join(err, cacheErr)
}

func (a *App) DotsSyncContextWithState(ctx context.Context, opts dots.SyncOptions) (*DotsOperationStateResult, error) {
	return a.dotsOperationStateAfter(ctx, func() ([]dots.Op, error) {
		return a.DotsSyncContext(ctx, opts)
	})
}

func (a *App) DotsSyncContextWithStateProgress(ctx context.Context, opts dots.SyncOptions, progress func(DotsOperationStateProgressEvent)) (*DotsOperationStateResult, error) {
	if progress == nil {
		return a.DotsSyncContextWithState(ctx, opts)
	}
	upstream := opts.Progress
	opts.Progress = func(event dots.SyncProgressEvent) {
		if upstream != nil {
			upstream(event)
		}
		update := DotsOperationStateProgressEvent{
			Entry: event.Entry,
			Index: event.Index,
			Total: event.Total,
			Done:  event.Done,
			Text:  DotsSyncActivityProgressText(event),
			Err:   event.Err,
			Ops:   event.Ops,
		}
		if event.Done {
			update.State, update.StateErr = a.RefreshDotsState(ctx)
		}
		progress(update)
	}
	return a.DotsSyncContextWithState(ctx, opts)
}

func (a *App) DotsSyncEntryWithState(ctx context.Context, name string, opts dots.SyncOptions) (*DotsOperationStateResult, error) {
	return a.dotsOperationStateAfter(ctx, func() ([]dots.Op, error) {
		return a.DotsSyncEntry(ctx, name, opts)
	})
}

func (a *App) SaveDotsRepoAndSync(ctx context.Context, repo string, opts dots.SyncOptions) (*DotsOperationStateResult, error) {
	settings, err := a.LoadSettings()
	if err != nil {
		return nil, err
	}
	settings.DotsRepo = repo
	settings.DotsDisabled = config.BoolPtr(false)
	if err := a.SaveSettings(ctx, settings); err != nil {
		return nil, fmt.Errorf("save dots repo: %w", err)
	}
	savedSettings, err := a.LoadSettings()
	if err != nil {
		return nil, err
	}
	result, syncErr := a.DotsSyncContextWithState(ctx, opts)
	if result == nil {
		result = &DotsOperationStateResult{}
	}
	result.Settings = savedSettings
	result.HasSettings = true
	return result, syncErr
}

func (a *App) DotsSyncDiscoveredWithState(ctx context.Context, nameOrPath, groupName string) (*DotsDiscoveredOperationStateResult, error) {
	var added config.DotEntry
	result, err := a.dotsOperationStateAfter(ctx, func() ([]dots.Op, error) {
		var addErr error
		added, addErr = a.DotsAddDiscoveredEntryContext(ctx, nameOrPath, groupName)
		if addErr != nil {
			return nil, addErr
		}
		name := added.Name
		if name == "" {
			name = nameOrPath
		}
		return a.DotsSyncEntry(ctx, name, dots.SyncOptions{})
	})
	return &DotsDiscoveredOperationStateResult{Added: added, Ops: result.Ops, State: result.State}, err
}

func (a *App) DotsAddDiscoveredHostVariantWithState(ctx context.Context, nameOrPath string, opts DotsAddVariantOptions) (*DotsVariantStateResult, error) {
	var info DotVariantInfo
	result, err := a.dotsOperationStateAfter(ctx, func() ([]dots.Op, error) {
		added, addErr := a.DotsAddDiscoveredEntryContext(ctx, nameOrPath, "")
		if addErr != nil {
			return nil, addErr
		}
		name := added.Name
		if name == "" {
			name = nameOrPath
		}
		sync := opts.Sync
		opts.Sync = false
		var ops []dots.Op
		info, ops, addErr = a.DotsAddHostVariant(ctx, name, opts)
		if addErr != nil || !sync || !info.Active {
			return ops, addErr
		}
		resolveOps, resolveErr := a.DotsResolveConflict(ctx, name, DotResolveUseRepo)
		return append(ops, resolveOps...), resolveErr
	})
	return &DotsVariantStateResult{Info: info, Ops: result.Ops, State: result.State}, err
}

func (a *App) DotsResolveDiscoveredWithState(ctx context.Context, nameOrPath, groupName string, strategy DotsResolveStrategy) (*DotsDiscoveredOperationStateResult, error) {
	var added config.DotEntry
	result, err := a.dotsOperationStateAfter(ctx, func() ([]dots.Op, error) {
		var addErr error
		added, addErr = a.DotsAddDiscoveredEntryContext(ctx, nameOrPath, groupName)
		if addErr != nil {
			return nil, addErr
		}
		name := added.Name
		if name == "" {
			name = nameOrPath
		}
		return a.DotsResolveConflict(ctx, name, strategy)
	})
	return &DotsDiscoveredOperationStateResult{Added: added, Ops: result.Ops, State: result.State}, err
}

func (a *App) DotsAddWithState(ctx context.Context, path string, opts DotsAddOptions) (*DotsOperationStateResult, error) {
	return a.dotsOperationStateAfter(ctx, func() ([]dots.Op, error) {
		return a.DotsAdd(ctx, path, opts)
	})
}

func (a *App) DotsDeleteWithState(ctx context.Context, name string, opts DotsDeleteOptions) (*DotsOperationStateResult, error) {
	return a.dotsOperationStateAfter(ctx, func() ([]dots.Op, error) {
		return nil, a.DotsDeleteWithOptions(ctx, name, opts)
	})
}

func (a *App) DotsDeleteLocalWithState(ctx context.Context, status DotStatus) (*DotsOperationStateResult, error) {
	return a.dotsOperationStateAfter(ctx, func() ([]dots.Op, error) {
		return nil, a.DotsDeleteLocal(ctx, status)
	})
}

func (a *App) DotsResolveConflictWithState(ctx context.Context, name string, strategy DotsResolveStrategy) (*DotsOperationStateResult, error) {
	return a.dotsOperationStateAfter(ctx, func() ([]dots.Op, error) {
		return a.DotsResolveConflict(ctx, name, strategy)
	})
}

// DotsForceResolveAllWithState runs a full sync that force-resolves every
// conflicting entry with the given strategy (the bulk equivalent of
// `dots sync --use-repo|--use-local`), returning the refreshed dots state.
func (a *App) DotsForceResolveAllWithState(ctx context.Context, strategy DotsResolveStrategy) (*DotsOperationStateResult, error) {
	conflictStrategy := ""
	switch strategy {
	case DotResolveUseRepo:
		conflictStrategy = "use_repo"
	case DotResolveUseLocal:
		conflictStrategy = "use_local"
	}
	return a.dotsOperationStateAfter(ctx, func() ([]dots.Op, error) {
		return a.DotsSync(dots.SyncOptions{ConflictStrategy: conflictStrategy})
	})
}

func (a *App) DotsAddIgnorePatternWithState(ctx context.Context, name, pattern string) (*DotsOperationStateResult, error) {
	return a.dotsOperationStateAfter(ctx, func() ([]dots.Op, error) {
		return nil, a.DotsAddIgnorePatternContext(ctx, name, pattern)
	})
}

func (a *App) DotsIncludeIgnoredPathWithState(ctx context.Context, name, relPath string) (*DotsOperationStateResult, error) {
	return a.dotsOperationStateAfter(ctx, func() ([]dots.Op, error) {
		return nil, a.DotsIncludeIgnoredPathContext(ctx, name, relPath)
	})
}

func (a *App) DotsSetEntryIgnoredWithState(ctx context.Context, name, path string, ignored bool) (*DotsOperationStateResult, error) {
	return a.dotsOperationStateAfter(ctx, func() ([]dots.Op, error) {
		return nil, a.DotsSetEntryIgnoredContext(ctx, name, path, ignored)
	})
}

func (a *App) DotsAddHostVariantWithState(ctx context.Context, name string, opts DotsAddVariantOptions) (*DotsVariantStateResult, error) {
	var info DotVariantInfo
	result, err := a.dotsOperationStateAfter(ctx, func() ([]dots.Op, error) {
		var opErr error
		var ops []dots.Op
		info, ops, opErr = a.DotsAddHostVariant(ctx, name, opts)
		return ops, opErr
	})
	return &DotsVariantStateResult{Info: info, Ops: result.Ops, State: result.State}, err
}

func (a *App) DotsRemoveHostVariantWithState(ctx context.Context, name string, opts DotsRemoveVariantOptions) (*DotsVariantStateResult, error) {
	var info DotVariantInfo
	result, err := a.dotsOperationStateAfter(ctx, func() ([]dots.Op, error) {
		var opErr error
		info, opErr = a.DotsRemoveHostVariant(ctx, name, opts)
		return nil, opErr
	})
	return &DotsVariantStateResult{Info: info, Ops: result.Ops, State: result.State}, err
}

func (a *App) DotsPullWithState(ctx context.Context) (*DotsOperationStateResult, error) {
	return a.dotsOperationStateAfter(ctx, func() ([]dots.Op, error) {
		return a.DotsPull(ctx)
	})
}

func (a *App) DotsPushWithState(ctx context.Context, message string) (*DotsOperationStateResult, error) {
	return a.dotsOperationStateAfter(ctx, func() ([]dots.Op, error) {
		return nil, a.DotsPush(ctx, message)
	})
}

func (a *App) DotsCommitWithState(ctx context.Context, message string) (*DotsOperationStateResult, error) {
	return a.dotsOperationStateAfter(ctx, func() ([]dots.Op, error) {
		return nil, a.DotsCommit(ctx, message)
	})
}

func (a *App) dotsOperationStateAfter(ctx context.Context, run func() ([]dots.Op, error)) (*DotsOperationStateResult, error) {
	ops, opErr := run()
	state, stateErr := a.RefreshDotsState(ctx)
	return &DotsOperationStateResult{Ops: ops, State: state}, errors.Join(opErr, stateErr)
}

func (a *App) cacheDotsStatusResult(ctx context.Context, result *DotsStatusResult, observedAt time.Time) (*DotsState, error) {
	state := &DotsState{
		Entries:         append([]DotStatus(nil), result.Entries...),
		GitStatus:       result.GitStatus,
		DotMemberships:  cloneStringSliceMap(result.DotMemberships),
		DiscoveredCount: result.DiscoveredCount,
		ObservedAt:      observedAt,
		Loaded:          true,
	}
	db := a.readDB()
	if db == nil {
		return state, nil
	}
	rows, encodeErr := dotsStatusCacheRows(result.Entries, observedAt)
	if encodeErr != nil {
		return state, encodeErr
	}
	if cacheErr := db.ReplaceDotsSnapshot(ctx, rows, result.GitStatus, result.DiscoveredCount, observedAt); cacheErr != nil {
		return state, fmt.Errorf("cache dots state: %w", cacheErr)
	}
	return state, nil
}

func cloneStringSliceMap(in map[string][]string) map[string][]string {
	if in == nil {
		return nil
	}
	out := make(map[string][]string, len(in))
	for key, values := range in {
		out[key] = append([]string(nil), values...)
	}
	return out
}

func (a *App) CachedDotsState(ctx context.Context) (*DotsState, error) {
	db := a.readDB()
	if db == nil {
		return &DotsState{}, nil
	}
	snapshot, ok, err := db.GetDotsSnapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("load cached dots state: %w", err)
	}
	if !ok {
		return &DotsState{}, nil
	}
	entries, err := dotsStatusesFromCacheRows(snapshot.Entries)
	if err != nil {
		return nil, err
	}
	return &DotsState{
		Entries:         entries,
		GitStatus:       snapshot.GitStatus,
		DiscoveredCount: snapshot.DiscoveredCount,
		ObservedAt:      snapshot.ObservedAt,
		Loaded:          true,
	}, nil
}

func (a *App) refreshDotsStateAfterSuccess(ctx context.Context, opErr *error, skip bool) *DotsState {
	if skip || opErr == nil || *opErr != nil {
		return nil
	}
	if !a.DotsConfigured() {
		return nil
	}
	repoPath, err := resolveRepoPath(a.dotsRepoPath())
	if err != nil {
		*opErr = errors.Join(*opErr, err)
		return nil
	}
	if _, err := os.Lstat(repoPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		*opErr = errors.Join(*opErr, err)
		return nil
	}
	state, err := a.RefreshDotsState(ctx)
	*opErr = errors.Join(*opErr, err)
	return state
}

func dotsStatusCacheRows(entries []DotStatus, observedAt time.Time) ([]*database.DotStatusCache, error) {
	rows := make([]*database.DotStatusCache, 0, len(entries))
	for i, entry := range entries {
		actionsJSON, err := marshalDotsStateJSON(entry.Actions)
		if err != nil {
			return nil, fmt.Errorf("encode actions for dots entry %q: %w", entry.Name, err)
		}
		countsJSON, err := marshalDotsStateJSON(entry.Counts)
		if err != nil {
			return nil, fmt.Errorf("encode counts for dots entry %q: %w", entry.Name, err)
		}
		childrenJSON, err := marshalDotsStateJSON(entry.Children)
		if err != nil {
			return nil, fmt.Errorf("encode children for dots entry %q: %w", entry.Name, err)
		}
		rows = append(rows, &database.DotStatusCache{
			Name:         entry.Name,
			Package:      entry.Package,
			Variant:      entry.Variant,
			SourcePath:   entry.SourcePath,
			TargetPath:   entry.TargetPath,
			ConfigPath:   entry.ConfigPath,
			Health:       string(entry.Health),
			State:        string(entry.State),
			ActionsJSON:  actionsJSON,
			Group:        entry.Group,
			FileCount:    entry.FileCount,
			CountsJSON:   countsJSON,
			IsDir:        entry.IsDir,
			ChildrenJSON: childrenJSON,
			Position:     i,
			ObservedAt:   observedAt,
		})
	}
	return rows, nil
}

func dotsStatusesFromCacheRows(rows []*database.DotStatusCache) ([]DotStatus, error) {
	entries := make([]DotStatus, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		var actions []DotAction
		if err := unmarshalDotsStateJSON(row.ActionsJSON, &actions); err != nil {
			return nil, fmt.Errorf("decode actions for dots entry %q: %w", row.Name, err)
		}
		var counts DotFileCounts
		if err := unmarshalDotsStateJSON(row.CountsJSON, &counts); err != nil {
			return nil, fmt.Errorf("decode counts for dots entry %q: %w", row.Name, err)
		}
		var children []DotChild
		if err := unmarshalDotsStateJSON(row.ChildrenJSON, &children); err != nil {
			return nil, fmt.Errorf("decode children for dots entry %q: %w", row.Name, err)
		}
		entries = append(entries, DotStatus{
			Name:       row.Name,
			Package:    row.Package,
			Variant:    row.Variant,
			SourcePath: row.SourcePath,
			TargetPath: row.TargetPath,
			ConfigPath: row.ConfigPath,
			Health:     DotHealth(row.Health),
			State:      DotState(row.State),
			Actions:    actions,
			Group:      row.Group,
			FileCount:  row.FileCount,
			Counts:     counts,
			IsDir:      row.IsDir,
			Children:   children,
		})
	}
	return entries, nil
}

func marshalDotsStateJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	if string(encoded) == "null" || string(encoded) == "{}" {
		return "", nil
	}
	return string(encoded), nil
}

func unmarshalDotsStateJSON(raw string, target any) error {
	if raw == "" {
		return nil
	}
	return json.Unmarshal([]byte(raw), target)
}
