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
	textutil "github.com/lkshrk/omni/internal/text"
)

// dotsHistoryIDCounter ensures unique IDs even when two operations
// occur within the same nanosecond.
var dotsHistoryIDCounter atomic.Uint64

const (
	dotsHistoryStateKey = "dots.history"
	dotsHistoryLimit    = 50
)

type dotsHistorySuppressKey struct{}

type dotsHistorySuppressUnchangedKey struct{}

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

type DotsWatchSyncOpCounts struct {
	Changes   int
	Conflicts int
}

func CountDotsWatchSyncOps(ops []dots.Op) DotsWatchSyncOpCounts {
	var counts DotsWatchSyncOpCounts
	for _, op := range ops {
		switch op.Kind {
		case dots.OpLink, dots.OpRepair, dots.OpAdopt, dots.OpUnlink, dots.OpUnlinkSkip, dots.OpUnlinkConflict:
			counts.Changes++
		case dots.OpConflict:
			counts.Conflicts++
		}
	}
	return counts
}

func DotsDisableDetail(ops []dots.Op) string {
	if len(ops) == 0 {
		return "dots disabled"
	}
	var unlinked, conflicts int
	for _, op := range ops {
		switch op.Kind {
		case dots.OpUnlink:
			unlinked++
		case dots.OpUnlinkConflict:
			conflicts++
		}
	}
	detail := fmt.Sprintf("%d unlinked", unlinked)
	if conflicts > 0 {
		detail += fmt.Sprintf(", %d conflicts", conflicts)
	}
	return detail
}

// RecentDotsHistory returns recent dotfile operation records, newest first.
func (a *App) RecentDotsHistory(ctx context.Context, limit int) ([]DotsHistoryEntry, error) {
	entries, err := a.readDotsHistory(ctx)
	if err != nil {
		return nil, err
	}
	entries = normalizeDotsHistoryEntries(entries)
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

func suppressDotsHistory(ctx context.Context) context.Context {
	return context.WithValue(ctx, dotsHistorySuppressKey{}, true)
}

func suppressUnchangedDotsHistory(ctx context.Context) context.Context {
	return context.WithValue(ctx, dotsHistorySuppressUnchangedKey{}, true)
}

func dotsHistorySuppressed(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	suppressed, _ := ctx.Value(dotsHistorySuppressKey{}).(bool)
	return suppressed
}

func unchangedDotsHistorySuppressed(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	suppressed, _ := ctx.Value(dotsHistorySuppressUnchangedKey{}).(bool)
	return suppressed
}

func (a *App) recordDotsHistoryResult(ctx context.Context, operation, entry, repoPath string, ops []dots.Op, opErr error, dryRun bool) {
	if dryRun || dotsHistorySuppressed(ctx) || (unchangedDotsHistorySuppressed(ctx) && dotsHistoryUnchangedSuccess(ops, opErr)) || a.readDB() == nil {
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

func dotsHistoryUnchangedSuccess(ops []dots.Op, opErr error) bool {
	if opErr != nil {
		return false
	}
	for _, op := range ops {
		if op.Kind != dots.OpSkip && op.Kind != dots.OpUnlinkSkip {
			return false
		}
	}
	return true
}

func dotsHistorySummary(operation string, ops []dots.Op, opErr error) string {
	if opErr != nil {
		return operation + " failed"
	}
	if len(ops) == 0 {
		return operation + " completed"
	}
	changed, unchanged := dotsHistoryOpCounts(ops)
	return dotsHistoryCountsSummary(operation, changed, unchanged)
}

func dotsHistoryCountsSummary(operation string, changed, unchanged int) string {
	switch {
	case changed == 0:
		return fmt.Sprintf("%s completed: no changes, %s unchanged", operation, textutil.PluralCount(unchanged, "dotfile", "dotfiles"))
	case unchanged == 0:
		return fmt.Sprintf("%s completed: %s changed", operation, textutil.PluralCount(changed, "dotfile", "dotfiles"))
	default:
		return fmt.Sprintf("%s completed: %s changed, %s unchanged", operation,
			textutil.PluralCount(changed, "dotfile", "dotfiles"),
			textutil.PluralCount(unchanged, "dotfile", "dotfiles"))
	}
}

func normalizeDotsHistoryEntries(entries []DotsHistoryEntry) []DotsHistoryEntry {
	for i := range entries {
		entries[i] = normalizeDotsHistoryEntry(entries[i])
	}
	return entries
}

func normalizeDotsHistoryEntry(entry DotsHistoryEntry) DotsHistoryEntry {
	if entry.Status != "success" || len(entry.Ops) == 0 {
		return entry
	}
	changed, unchanged := dotsHistoryStoredOpCounts(entry.Ops)
	entry.Summary = dotsHistoryCountsSummary(entry.Operation, changed, unchanged)
	return entry
}

func dotsHistoryOpCounts(ops []dots.Op) (changed, unchanged int) {
	for _, op := range ops {
		if op.Kind == dots.OpSkip || op.Kind == dots.OpUnlinkSkip {
			unchanged++
			continue
		}
		changed++
	}
	return changed, unchanged
}

func dotsHistoryStoredOpCounts(ops []DotsHistoryOp) (changed, unchanged int) {
	for _, op := range ops {
		if op.Kind == "skip" || op.Kind == "unlink:skip" {
			unchanged++
			continue
		}
		changed++
	}
	return changed, unchanged
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
