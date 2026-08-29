package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/executor"
)

func (a *App) newExecutor() executor.Executor {
	return executor.NewTracing(executor.New(), a)
}

func (a *App) CommandExecutor() executor.Executor {
	return a.newExecutor()
}

// RecordCommandTrace — Best-effort: command tracing must not break package-manager operations.
func (a *App) RecordCommandTrace(ctx context.Context, trace executor.TraceRecord) error {
	if a.diagnosticMode {
		return nil
	}
	db := a.readDB()
	if db == nil {
		return nil
	}
	trace = executor.SanitizeTraceRecord(trace)
	return db.RecordCommandTrace(context.WithoutCancel(ctx), &database.CommandTrace{
		StartedAt:  trace.StartedAt,
		FinishedAt: trace.FinishedAt,
		DurationMS: trace.DurationMS,
		Reason:     trace.Reason,
		Command:    trace.Command,
		Status:     trace.Status,
		ExitCode:   trace.ExitCode,
		Error:      trace.Error,
		Stdout:     trace.Stdout,
		Stderr:     trace.Stderr,
	})
}

func (a *App) CommandTraces(ctx context.Context, limit int) ([]CommandTraceView, error) {
	db := a.readDB()
	if db == nil {
		return nil, fmt.Errorf("database is not initialized")
	}
	rows, err := db.ListCommandTraces(ctx, limit)
	if err != nil {
		return nil, err
	}
	return commandTraceViewsFromRows(rows), nil
}

func traceReason(ctx context.Context, action, name, providerName string) context.Context {
	action = strings.TrimSpace(action)
	name = strings.TrimSpace(name)
	providerName = strings.TrimSpace(providerName)
	if action == "" || name == "" {
		return ctx
	}
	if providerName == "" {
		return executor.WithTraceReason(ctx, fmt.Sprintf("%s %s", action, name))
	}
	return executor.WithTraceReason(ctx, fmt.Sprintf("%s %s (%s)", action, name, providerName))
}

func traceReasonDetail(ctx context.Context, action, name, providerName, detail string) context.Context {
	providerName = strings.TrimSpace(providerName)
	detail = strings.TrimSpace(detail)
	if detail != "" {
		if providerName != "" {
			providerName += "/" + detail
		} else {
			providerName = detail
		}
	}
	return traceReason(ctx, action, name, providerName)
}
