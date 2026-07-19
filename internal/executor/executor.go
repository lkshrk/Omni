package executor

import (
	"context"
	"strings"
)

type outputObserverKey struct{}

// WithOutputObserver reports sanitized subprocess output as it is produced.
// stdout and stderr may invoke observer concurrently.
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

func sanitizeOutputLine(line []byte) string {
	return strings.TrimSpace(redactTraceText(string(line)))
}

// Executor abstracts shell command execution.
// Inject a mock in tests to avoid touching the real system.
type Executor interface {
	Run(ctx context.Context, name string, args ...string) (stdout, stderr string, err error)
}
