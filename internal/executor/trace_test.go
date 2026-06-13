package executor

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type traceSinkStub struct {
	records []TraceRecord
	err     error
}

func (s *traceSinkStub) RecordCommandTrace(_ context.Context, trace TraceRecord) error {
	s.records = append(s.records, trace)
	return s.err
}

func TestTracingExecutorRecordsCommandReasonAndFailure(t *testing.T) {
	sink := &traceSinkStub{}
	boom := errors.New("exit status 1")
	next := &MockExecutor{Responses: []MockCall{{Stderr: "TOKEN=secret failed", Err: boom}}}
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	exec := NewTracing(next, sink)
	exec.Now = func() time.Time {
		now = now.Add(25 * time.Millisecond)
		return now
	}

	_, _, err := exec.Run(WithTraceReason(context.Background(), "installing rg (brew)"), "brew", "install", "--token", "secret", "rip grep")
	if !errors.Is(err, boom) {
		t.Fatalf("Run err = %v, want wrapped response error", err)
	}
	if len(sink.records) != 1 {
		t.Fatalf("records = %d, want 1", len(sink.records))
	}
	trace := sink.records[0]
	if trace.Reason != "installing rg (brew)" {
		t.Fatalf("reason = %q", trace.Reason)
	}
	if trace.Command != "brew install --token '[redacted]' 'rip grep'" {
		t.Fatalf("command = %q", trace.Command)
	}
	if trace.Status != "failed" {
		t.Fatalf("status = %q", trace.Status)
	}
	if trace.DurationMS != 25 {
		t.Fatalf("duration = %dms, want 25ms", trace.DurationMS)
	}
	if strings.Contains(trace.Stderr, "secret") || !strings.Contains(trace.Stderr, "TOKEN=[redacted]") {
		t.Fatalf("stderr = %q, want redacted secret", trace.Stderr)
	}
}

func TestTracingExecutorIgnoresSinkFailure(t *testing.T) {
	sink := &traceSinkStub{err: errors.New("db busy")}
	next := &MockExecutor{Responses: []MockCall{{Stdout: "ok"}}}
	exec := NewTracing(next, sink)

	stdout, _, err := exec.Run(context.Background(), "printf", "ok")
	if err != nil {
		t.Fatalf("Run err = %v", err)
	}
	if stdout != "ok" {
		t.Fatalf("stdout = %q", stdout)
	}
	if len(sink.records) != 1 {
		t.Fatalf("records = %d, want 1", len(sink.records))
	}
}
