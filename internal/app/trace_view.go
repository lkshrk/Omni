package app

import (
	"time"

	"github.com/lkshrk/omni/internal/database"
)

// CommandTraceView — A plain value type so the trace-log view depends on internal/app, not database.CommandTrace.
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
