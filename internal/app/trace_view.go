package app

import (
	"time"

	"github.com/lkshrk/omni/internal/database"
)

// CommandTraceView is the app→TUI contract for one recorded command trace. Like
// ToolView, it is a plain value type — no bun.BaseModel, no column tags, and the
// nullable ExitCode flattened to *int64 (nil = no exit code recorded) — so the
// trace-log view depends on internal/app instead of the persistence row
// database.CommandTrace.
type CommandTraceView struct {
	ID         int64
	StartedAt  time.Time
	FinishedAt time.Time
	DurationMS int64
	Reason     string
	Command    string
	Status     string
	ExitCode   *int64
	Error      string
	Stderr     string
}

func commandTraceViewFromRow(t database.CommandTrace) CommandTraceView {
	v := CommandTraceView{
		ID:         t.ID,
		StartedAt:  t.StartedAt,
		FinishedAt: t.FinishedAt,
		DurationMS: t.DurationMS,
		Reason:     t.Reason,
		Command:    t.Command,
		Status:     t.Status,
		Error:      t.Error,
		Stderr:     t.Stderr,
	}
	if t.ExitCode.Valid {
		code := t.ExitCode.Int64
		v.ExitCode = &code
	}
	return v
}

func commandTraceViewsFromRows(rows []database.CommandTrace) []CommandTraceView {
	if rows == nil {
		return nil
	}
	out := make([]CommandTraceView, len(rows))
	for i, r := range rows {
		out[i] = commandTraceViewFromRow(r)
	}
	return out
}
