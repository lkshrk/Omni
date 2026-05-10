package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/dots"
)

// dotsHistoryIDCounter ensures unique IDs even when two operations
// occur within the same nanosecond.
var dotsHistoryIDCounter atomic.Uint64

const (
	dotsHistoryStateKey = "dots.history"
	dotsHistoryLimit    = 50
)

type dotsHistorySuppressKey struct{}

// DotsHistoryEntry is a machine-local recovery trail for a dotfile operation.
// It intentionally lives in the cache DB instead of settings.json because it
// can contain absolute local paths and host-specific errors.
type DotsHistoryEntry struct {
	ID        string          `json:"id"`
	Time      time.Time       `json:"time"`
	Operation string          `json:"operation"`
	Entry     string          `json:"entry,omitempty"`
	Status    string          `json:"status"`
	Summary   string          `json:"summary"`
	Error     string          `json:"error,omitempty"`
	RepoPath  string          `json:"repo_path,omitempty"`
	Ops       []DotsHistoryOp `json:"ops,omitempty"`
}

// DotsHistoryOp is a JSON-safe summary of one low-level dots operation.
type DotsHistoryOp struct {
	Kind  string `json:"kind"`
	Entry string `json:"entry,omitempty"`
	File  string `json:"file,omitempty"`
	Src   string `json:"src,omitempty"`
	Dst   string `json:"dst,omitempty"`
	Error string `json:"error,omitempty"`
}

// RecentDotsHistory returns recent dotfile operation records, newest first.
func (a *App) RecentDotsHistory(ctx context.Context, limit int) ([]DotsHistoryEntry, error) {
	entries, err := a.readDotsHistory(ctx)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

func suppressDotsHistory(ctx context.Context) context.Context {
	return context.WithValue(ctx, dotsHistorySuppressKey{}, true)
}

func dotsHistorySuppressed(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	suppressed, _ := ctx.Value(dotsHistorySuppressKey{}).(bool)
	return suppressed
}

func (a *App) recordDotsHistoryResult(ctx context.Context, operation, entry, repoPath string, ops []dots.Op, opErr error, dryRun bool) {
	if dryRun || dotsHistorySuppressed(ctx) || a.readDB() == nil {
		return
	}
	historyCtx := context.WithoutCancel(ctx)
	now := time.Now()
	record := DotsHistoryEntry{
		ID:        fmt.Sprintf("%d-%d", now.UnixNano(), dotsHistoryIDCounter.Add(1)),
		Time:      now.UTC(),
		Operation: operation,
		Entry:     entry,
		Status:    dotsHistoryStatus(ops, opErr),
		Summary:   dotsHistorySummary(operation, ops, opErr),
		RepoPath:  repoPath,
		Ops:       dotsHistoryOps(ops),
	}
	if opErr != nil {
		record.Error = opErr.Error()
	}
	if err := a.prependDotsHistory(historyCtx, record); err != nil {
		fmt.Fprintf(os.Stderr, "warning: omni: record dots history: %v\n", err)
	}
}

func (a *App) prependDotsHistory(ctx context.Context, record DotsHistoryEntry) error {
	a.historyMu.Lock()
	defer a.historyMu.Unlock()

	db := a.readDB()
	if db == nil {
		return fmt.Errorf("database is not initialised")
	}
	entries, err := readDotsHistoryFrom(ctx, db)
	if err != nil {
		return err
	}
	entries = append([]DotsHistoryEntry{record}, entries...)
	if len(entries) > dotsHistoryLimit {
		entries = entries[:dotsHistoryLimit]
	}
	body, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("marshal dots history: %w", err)
	}
	return db.SetState(ctx, dotsHistoryStateKey, string(body))
}

func (a *App) readDotsHistory(ctx context.Context) ([]DotsHistoryEntry, error) {
	db := a.readDB()
	if db == nil {
		return nil, nil
	}
	return readDotsHistoryFrom(ctx, db)
}

func readDotsHistoryFrom(ctx context.Context, db *database.DB) ([]DotsHistoryEntry, error) {
	raw, err := db.GetState(ctx, dotsHistoryStateKey)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("read dots history: %w", err)
	}
	var entries []DotsHistoryEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return nil, fmt.Errorf("parse dots history: %w", err)
	}
	return entries, nil
}

// dotsHistoryStatus classifies the outcome of a dots operation.
// ops contains the operations that completed before the error (if any);
// a non-nil opErr with completed ops means partial success.
func dotsHistoryStatus(ops []dots.Op, opErr error) string {
	if opErr == nil {
		return "success"
	}
	if len(ops) > 0 {
		return "partial"
	}
	return "failed"
}

func dotsHistorySummary(operation string, ops []dots.Op, opErr error) string {
	if opErr != nil {
		return operation + " failed"
	}
	if len(ops) == 0 {
		return operation + " completed"
	}
	return fmt.Sprintf("%s completed with %s", operation, dotsHistoryPlural(len(ops), "dotfile op", "dotfile ops"))
}

func dotsHistoryOps(ops []dots.Op) []DotsHistoryOp {
	if len(ops) == 0 {
		return nil
	}
	out := make([]DotsHistoryOp, 0, len(ops))
	for _, op := range ops {
		item := DotsHistoryOp{
			Kind:  op.Kind.String(),
			Entry: op.Entry,
			File:  op.File,
			Src:   op.Src,
			Dst:   op.Dst,
		}
		if op.Err != nil {
			item.Error = op.Err.Error()
		}
		out = append(out, item)
	}
	return out
}

func dotsHistoryPlural(count int, singular, plural string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s", count, singular)
	}
	return fmt.Sprintf("%d %s", count, plural)
}
