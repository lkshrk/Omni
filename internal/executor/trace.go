package executor

import (
	"context"
	"database/sql"
	"errors"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

const (
	tracePreviewLimit  = 4096
	traceTruncatedMark = "...[truncated]"
)

type traceReasonKey struct{}

type TraceRecord struct {
	StartedAt  time.Time
	FinishedAt time.Time
	DurationMS int64
	Reason     string
	Command    string
	Status     string
	ExitCode   sql.NullInt64
	Error      string
	Stdout     string
	Stderr     string
}

// TraceSink — Sinks must be best-effort: a tracing failure never changes command behavior.
type TraceSink interface {
	RecordCommandTrace(ctx context.Context, trace TraceRecord) error
}

type TracingExecutor struct {
	Next Executor
	Sink TraceSink
	Now  func() time.Time
}

func NewTracing(next Executor, sink TraceSink) *TracingExecutor {
	return &TracingExecutor{Next: next, Sink: sink}
}

func WithTraceReason(ctx context.Context, reason string) context.Context {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return ctx
	}
	return context.WithValue(ctx, traceReasonKey{}, reason)
}

func TraceReason(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	reason, _ := ctx.Value(traceReasonKey{}).(string)
	return strings.TrimSpace(reason)
}

func (t *TracingExecutor) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	return t.run(ctx, name, args, func() (string, string, error) {
		return t.Next.Run(ctx, name, args...)
	})
}

func (t *TracingExecutor) RunEnv(ctx context.Context, env []string, name string, args ...string) (string, string, error) {
	return t.run(ctx, name, args, func() (string, string, error) {
		return RunWithEnv(ctx, t.Next, env, name, args...)
	})
}

func (t *TracingExecutor) RunDir(ctx context.Context, dir, name string, args ...string) (string, string, error) {
	return t.run(ctx, name, args, func() (string, string, error) {
		return RunInDirWithEnv(ctx, t.Next, dir, nil, name, args...)
	})
}

func (t *TracingExecutor) RunDirEnv(ctx context.Context, dir string, env []string, name string, args ...string) (string, string, error) {
	return t.run(ctx, name, args, func() (string, string, error) {
		return RunInDirWithEnv(ctx, t.Next, dir, env, name, args...)
	})
}

func (t *TracingExecutor) RunDirEnvStdin(ctx context.Context, dir string, env []string, stdin []byte, name string, args ...string) (string, string, error) {
	return t.run(ctx, name, args, func() (string, string, error) {
		return RunInDirWithEnvAndStdin(ctx, t.Next, dir, env, stdin, name, args...)
	})
}

func (t *TracingExecutor) run(ctx context.Context, name string, args []string, run func() (string, string, error)) (string, string, error) {
	if t == nil || t.Next == nil {
		return "", "", errors.New("tracing executor missing wrapped executor")
	}
	started := t.now()
	stdout, stderr, err := run()
	finished := t.now()
	t.record(ctx, SanitizeTraceRecord(TraceRecord{
		StartedAt:  started,
		FinishedAt: finished,
		DurationMS: finished.Sub(started).Milliseconds(),
		Reason:     traceReasonOrDefault(ctx),
		Command:    RenderCommand(name, args...),
		Status:     traceStatus(ctx, err),
		ExitCode:   traceExitCode(err),
		Error:      errorString(err),
		Stdout:     stdout,
		Stderr:     stderr,
	}))
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

// SanitizeTraceRecord — Idempotent, so producers and sinks may both call it.
func SanitizeTraceRecord(trace TraceRecord) TraceRecord {
	trace.Reason = redactTraceText(trace.Reason)
	trace.Command = redactTraceText(trace.Command)
	trace.Status = redactTraceText(trace.Status)
	trace.Error = limitTracePreview(redactTraceText(trace.Error))
	trace.Stdout = limitTraceTail(redactTraceText(trace.Stdout))
	trace.Stderr = limitTracePreview(redactTraceText(trace.Stderr))
	return trace
}

// RenderCommand returns a shell-readable, redacted command line.
func RenderCommand(name string, args ...string) string {
	parts := append([]string{redactTraceText(name)}, redactArgs(args)...)
	for i, part := range parts {
		parts[i] = shellQuote(part)
	}
	return strings.Join(parts, " ")
}

func redactArgs(args []string) []string {
	out := append([]string(nil), args...)
	redactNext := false
	for i, arg := range out {
		arg = sanitizeTraceText(arg)
		out[i] = arg
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
	s = sanitizeTraceText(s)
	return envAssignmentPattern.ReplaceAllString(s, "$1=[redacted]")
}

// Carriage returns become newlines so stored traces cannot overwrite rendered content.
func sanitizeTraceText(s string) string {
	if s == "" {
		return ""
	}
	s = strings.ToValidUTF8(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = ansi.Strip(s)

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r >= 0x20 && r != 0x7f && (r < 0x80 || r > 0x9f):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Tools that report their outcome last (apm above all) need the end of stdout, not its beginning.
func limitTraceTail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= tracePreviewLimit {
		return s
	}
	tail := s[len(s)-(tracePreviewLimit-len(traceTruncatedMark)-1):]
	for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
		tail = tail[1:]
	}
	if i := strings.IndexByte(tail, '\n'); i >= 0 && i+1 < len(tail) {
		tail = tail[i+1:]
	}
	return traceTruncatedMark + "\n" + tail
}

func limitTracePreview(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= tracePreviewLimit {
		return s
	}
	if strings.HasSuffix(s, traceTruncatedMark) {
		contentLen := len(s) - len(traceTruncatedMark)
		if contentLen >= tracePreviewLimit-(utf8.UTFMax-1) && contentLen <= tracePreviewLimit {
			return s
		}
	}
	end := tracePreviewLimit
	for end > 0 && !utf8.ValidString(s[:end]) {
		end--
	}
	return s[:end] + traceTruncatedMark
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
