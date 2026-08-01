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

// EmitOutput reports synthetic command output as one terminal-safe line.
func EmitOutput(ctx context.Context, text string) bool {
	observer := outputObserver(ctx)
	if observer == nil {
		return false
	}
	if line := strings.Join(strings.Fields(redactTraceText(text)), " "); line != "" {
		observer(line)
	}
	return true
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

type environmentExecutor interface {
	RunEnv(ctx context.Context, env []string, name string, args ...string) (stdout, stderr string, err error)
}

// RunWithEnv overlays environment variables without mutating the process environment.
func RunWithEnv(ctx context.Context, exec Executor, env []string, name string, args ...string) (string, string, error) {
	if runner, ok := exec.(environmentExecutor); ok {
		return runner.RunEnv(ctx, env, name, args...)
	}
	fullArgs := make([]string, 0, len(env)+len(args)+1)
	for _, entry := range env {
		if strings.Contains(entry, "=") {
			fullArgs = append(fullArgs, entry)
		} else {
			fullArgs = append(fullArgs, "-u", entry)
		}
	}
	fullArgs = append(fullArgs, name)
	fullArgs = append(fullArgs, args...)
	return exec.Run(ctx, "env", fullArgs...)
}
