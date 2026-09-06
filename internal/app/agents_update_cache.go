package app

import (
	"context"
	"strconv"

	"github.com/lkshrk/omni/internal/apm"
	"github.com/lkshrk/omni/internal/database"
)

const agentsOutdatedUnknownStateKey = "agents_outdated_unknown"

// CacheAgentsOutdated records what apm last reported so the next startup can show it before re-checking.
func (a *App) CacheAgentsOutdated(ctx context.Context, result apm.OutdatedResult) error {
	db := a.readDB()
	if db == nil {
		return nil
	}
	updates := make([]database.AgentUpdate, 0, len(result.Rows))
	for _, row := range result.Rows {
		updates = append(updates, database.AgentUpdate{
			Package: row.Package,
			Current: row.Current,
			Latest:  row.Latest,
			Source:  row.Source,
		})
	}
	if err := db.ReplaceAgentUpdates(ctx, updates); err != nil {
		return err
	}
	return db.SetState(ctx, agentsOutdatedUnknownStateKey, strconv.Itoa(result.Unknown))
}

// CachedAgentsOutdated returns the last recorded answer; an empty result simply means nothing is known yet.
func (a *App) CachedAgentsOutdated(ctx context.Context) apm.OutdatedResult {
	db := a.readDB()
	if db == nil {
		return apm.OutdatedResult{}
	}
	updates, err := db.ListAgentUpdates(ctx)
	if err != nil {
		return apm.OutdatedResult{}
	}
	result := apm.OutdatedResult{Rows: make([]apm.OutdatedRow, 0, len(updates))}
	for _, update := range updates {
		result.Rows = append(result.Rows, apm.OutdatedRow{
			Package: update.Package,
			Current: update.Current,
			Latest:  update.Latest,
			Source:  update.Source,
		})
	}
	if raw, err := db.GetState(ctx, agentsOutdatedUnknownStateKey); err == nil {
		if unknown, convErr := strconv.Atoi(raw); convErr == nil {
			result.Unknown = unknown
		}
	}
	return result
}

// ForgetAgentsOutdated drops the cached answer after a command that changed the workspace it described.
func (a *App) ForgetAgentsOutdated(ctx context.Context) error {
	db := a.readDB()
	if db == nil {
		return nil
	}
	if err := db.ReplaceAgentUpdates(ctx, nil); err != nil {
		return err
	}
	return db.SetState(ctx, agentsOutdatedUnknownStateKey, "0")
}
