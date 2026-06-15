package executor

import (
	"context"
	"database/sql"
	"errors"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

const tracePreviewLimit = 4096

type traceReasonKey struct{}

// TraceRecord is the executor-level command event stored by app-owned sinks.
type TraceRecord struct {
	StartedAt  time.Time
	FinishedAt time.Time
	DurationMS int64
	Reason     string
	Command    string
	Status     string
	ExitCode   sql.NullInt64
	Error      string
	Stderr     string
}

// TraceSink persists command traces. Implementations must be best-effort from
// the executor's perspective; tracing failures never change command behavior.
type TraceSink interface {
	RecordCommandTrace(ctx context.Context, trace TraceRecord) error
}

// TracingExecutor records every command it delegates to Next.
type TracingExecutor struct {
	Next Executor
	Sink TraceSink
	Now  func() time.Time
}

// NewTracing wraps next with command tracing.
func NewTracing(next Executor, sink TraceSink) *TracingExecutor {
	return &TracingExecutor{Next: next, Sink: sink}
}

// WithTraceReason annotates command executions below ctx with a user-visible
// reason, for example "upgrading bam (brew/cask)".
func WithTraceReason(ctx context.Context, reason string) context.Context {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return ctx
	}
	return context.WithValue(ctx, traceReasonKey{}, reason)
}

// TraceReason returns the command trace reason attached to ctx.
func TraceReason(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	reason, _ := ctx.Value(traceReasonKey{}).(string)
	return strings.TrimSpace(reason)
}

// Run delegates to the wrapped executor and records a compact trace event.
func (t *TracingExecutor) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	if t == nil || t.Next == nil {
		return "", "", errors.New("tracing executor missing wrapped executor")
	}
	started := t.now()
	stdout, stderr, err := t.Next.Run(ctx, name, args...)
	finished := t.now()
	t.record(ctx, TraceRecord{
		StartedAt:  started,
		FinishedAt: finished,
		DurationMS: finished.Sub(started).Milliseconds(),
		Reason:     traceReasonOrDefault(ctx),
		Command:    RenderCommand(name, args...),
		Status:     traceStatus(ctx, err),
		ExitCode:   traceExitCode(err),
		Error:      limitTracePreview(redactTraceText(errorString(err))),
		Stderr:     limitTracePreview(redactTraceText(stderr)),
	})
	return stdout, stderr, err
}

func (t *TracingExecutor) CommandAvailable(name string) bool {
	if checker, ok := t.Next.(interface{ CommandAvailable(string) bool }); ok {
		return checker.CommandAvailable(name)
	}
	return CommandAvailable(name)
}

func (t *TracingExecutor) now() time.Time {
	if t != nil && t.Now != nil {
		return t.Now().UTC()
	}
	return time.Now().UTC()
}

func (t *TracingExecutor) record(ctx context.Context, trace TraceRecord) {
	if t == nil || t.Sink == nil {
		return
	}
	_ = t.Sink.RecordCommandTrace(ctx, trace)
}

func traceReasonOrDefault(ctx context.Context) string {
	if reason := TraceReason(ctx); reason != "" {
		return reason
	}
	return "external command"
}

func traceStatus(ctx context.Context, err error) string {
	if err == nil {
		return "success"
	}
	if ctx != nil && ctx.Err() != nil {
		return "canceled"
	}
	return "failed"
}

func traceExitCode(err error) sql.NullInt64 {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return sql.NullInt64{Int64: int64(exitErr.ExitCode()), Valid: true}
	}
	return sql.NullInt64{}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// RenderCommand returns a shell-readable, redacted command line.
func RenderCommand(name string, args ...string) string {
	parts := append([]string{name}, redactArgs(args)...)
	for i, part := range parts {
		parts[i] = shellQuote(part)
	}
	return strings.Join(parts, " ")
}

func redactArgs(args []string) []string {
	out := append([]string(nil), args...)
	redactNext := false
	for i, arg := range out {
		if redactNext {
			out[i] = "[redacted]"
			redactNext = false
			continue
		}
		if sensitiveFlag(arg) {
			if strings.Contains(arg, "=") {
				out[i] = redactFlagValue(arg)
			} else {
				redactNext = true
			}
			continue
		}
		out[i] = redactTraceText(arg)
	}
	return out
}

func sensitiveFlag(arg string) bool {
	key := strings.ToLower(strings.TrimLeft(arg, "-"))
	if before, _, ok := strings.Cut(key, "="); ok {
		key = before
	}
	return sensitiveKey(key)
}

func sensitiveKey(key string) bool {
	for _, marker := range []string{"token", "password", "passwd", "secret", "apikey", "api-key", "credential", "auth"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

func redactFlagValue(arg string) string {
	before, _, ok := strings.Cut(arg, "=")
	if !ok {
		return arg
	}
	return before + "=[redacted]"
}

var envAssignmentPattern = regexp.MustCompile(`(?i)\b((?:TOKEN|PASSWORD|PASSWD|SECRET|API_KEY|AUTH)[A-Z0-9_]*|[A-Z_][A-Z0-9_]*(?:TOKEN|PASSWORD|PASSWD|SECRET|API_KEY|AUTH)[A-Z0-9_]*)=([^ \t\n'"]+)`)

func redactTraceText(s string) string {
	if s == "" {
		return ""
	}
	// Strip terminal escape/control sequences so command output captured from
	// untrusted formulae/taps can't corrupt the TUI when the trace is rendered.
	s = ansi.Strip(s)
	return envAssignmentPattern.ReplaceAllString(s, "$1=[redacted]")
}

func limitTracePreview(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= tracePreviewLimit {
		return s
	}
	return s[:tracePreviewLimit] + "...[truncated]"
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if isShellSafe(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func isShellSafe(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case strings.ContainsRune("@%_+=:,./-", r):
		default:
			return false
		}
	}
	return true
}
