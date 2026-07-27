package executor

import (
	"context"
	"strings"
)

type outputObserverKey struct{}

// WithOutputObserver — stdout and stderr may invoke observer concurrently.
func WithOutputObserver(ctx context.Context, observer func(string)) context.Context {
	if ctx == nil || observer == nil {
		return ctx
	}
	return context.WithValue(ctx, outputObserverKey{}, observer)
}

func outputObserver(ctx context.Context) func(string) {
	if ctx == nil {
		return nil
	}
	observer, _ := ctx.Value(outputObserverKey{}).(func(string))
	return observer
}

type outputLimitKey struct{}

// WithOutputLimit caps captured bytes without stopping the command, preserving early version output.
func WithOutputLimit(ctx context.Context, limit int) context.Context {
	if ctx == nil || limit <= 0 {
		return ctx
	}
	return context.WithValue(ctx, outputLimitKey{}, limit)
}

func outputLimit(ctx context.Context) int {
	if ctx == nil {
		return 0
	}
	limit, _ := ctx.Value(outputLimitKey{}).(int)
	return limit
}

func sanitizeOutputLine(line []byte) string {
	return strings.TrimSpace(redactTraceText(string(line)))
}

type Executor interface {
	Run(ctx context.Context, name string, args ...string) (stdout, stderr string, err error)
}
