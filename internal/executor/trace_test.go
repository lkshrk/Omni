package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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

func TestTracingExecutorLabelsTruncatedStderr(t *testing.T) {
	sink := &traceSinkStub{}
	next := &MockExecutor{Responses: []MockCall{{Stderr: strings.Repeat("x", tracePreviewLimit+1), Err: errors.New("failed")}}}

	_, _, _ = NewTracing(next, sink).Run(context.Background(), "tool")
	if len(sink.records) != 1 || !strings.HasSuffix(sink.records[0].Stderr, "...[truncated]") {
		t.Fatalf("stored stderr should clearly label the capture limit: %#v", sink.records)
	}
}

func TestTracingExecutorTruncatesStderrAtUTF8Boundary(t *testing.T) {
	sink := &traceSinkStub{}
	next := &MockExecutor{Responses: []MockCall{{
		Stderr: strings.Repeat("x", tracePreviewLimit-1) + "界",
	}}}

	_, _, _ = NewTracing(next, sink).Run(context.Background(), "tool")

	if len(sink.records) != 1 {
		t.Fatalf("records = %d, want 1", len(sink.records))
	}
	stderr := sink.records[0].Stderr
	if !utf8.ValidString(stderr) {
		t.Fatalf("stderr is not valid UTF-8: %q", stderr)
	}
	if stderr != strings.Repeat("x", tracePreviewLimit-1)+"...[truncated]" {
		t.Fatalf("stderr = %q, want truncation before partial Unicode rune", stderr)
	}
}

func TestSanitizeTraceRecordTruncationIsIdempotentWithoutMarkerBypass(t *testing.T) {
	rawUnicode := TraceRecord{Stderr: strings.Repeat("x", tracePreviewLimit-3) + "😀"}
	once := SanitizeTraceRecord(rawUnicode)
	twice := SanitizeTraceRecord(once)
	if twice.Stderr != once.Stderr {
		t.Fatalf("second sanitization changed truncated Unicode preview:\nonce:  %q\ntwice: %q", once.Stderr, twice.Stderr)
	}

	rawMarker := strings.Repeat("x", 4100-len(traceTruncatedMark)) + traceTruncatedMark
	got := SanitizeTraceRecord(TraceRecord{Stderr: rawMarker}).Stderr
	if got == rawMarker {
		t.Fatalf("natural marker suffix bypassed truncation for %d-byte input", len(rawMarker))
	}
	if !strings.HasSuffix(got, traceTruncatedMark) {
		t.Fatalf("stderr = %q, want explicit truncation marker", got)
	}
}

func TestTracingExecutorSanitizesTraceTextBeforePersistence(t *testing.T) {
	sink := &traceSinkStub{}
	invalidUTF8 := string([]byte{0xff})
	next := &MockExecutor{Responses: []MockCall{{
		Stderr: "line1\rline2\r\nline3\tcol\x00\x01\x08\x0b\x0c\x0e\x1f\x7f\u0080\u0085\u009f" +
			"\x1b[31mred\x1b[0m\x1b]0;title\x07 TOKEN=topsecret " + invalidUTF8 + "界",
		Err: errors.New("AUTH_TOKEN=errsecret\rretry\x08"),
	}}}
	exec := NewTracing(next, sink)
	ctx := WithTraceReason(context.Background(), "checking\x00 trace\rnext\t界")

	_, _, _ = exec.Run(
		ctx,
		"to\x08ol",
		"--pass\x08word", "argsecret",
		"API_KEY=inline",
		"plain\targ",
		"\x1b[32msnow\x1b[0m"+invalidUTF8+"界",
	)

	if len(sink.records) != 1 {
		t.Fatalf("records = %d, want 1", len(sink.records))
	}
	trace := sink.records[0]
	if trace.Reason != "checking trace\nnext\t界" {
		t.Errorf("reason = %q", trace.Reason)
	}
	if trace.Command != "tool --password '[redacted]' 'API_KEY=[redacted]' 'plain\targ' 'snow界'" {
		t.Errorf("command = %q", trace.Command)
	}
	if trace.Error != "AUTH_TOKEN=[redacted]\nretry" {
		t.Errorf("error = %q", trace.Error)
	}
	if trace.Stderr != "line1\nline2\nline3\tcolred TOKEN=[redacted] 界" {
		t.Errorf("stderr = %q", trace.Stderr)
	}
}

func TestTraceRecordCapsStdoutToItsTail(t *testing.T) {
	var sb strings.Builder
	for i := 0; sb.Len() <= tracePreviewLimit*2; i++ {
		fmt.Fprintf(&sb, "[i] progress line %d\n", i)
	}
	sb.WriteString("[x] Install failed after 0.0s.\n")

	got := SanitizeTraceRecord(TraceRecord{Stdout: sb.String()}).Stdout
	if len(got) > tracePreviewLimit {
		t.Fatalf("stdout tail = %d bytes, want <= %d", len(got), tracePreviewLimit)
	}
	if !strings.HasPrefix(got, traceTruncatedMark+"\n") {
		t.Fatalf("tail is not marked as truncated: %q", got[:40])
	}
	if !strings.HasSuffix(got, "[x] Install failed after 0.0s.") {
		t.Fatalf("verdict line dropped from the tail: %q", got[len(got)-60:])
	}
	if strings.Contains(strings.SplitN(got, "\n", 3)[1], "progress line 0\n") {
		t.Fatal("tail kept the head of stdout")
	}
	if again := SanitizeTraceRecord(TraceRecord{Stdout: got}).Stdout; again != got {
		t.Fatalf("stdout truncation is not idempotent:\n%q\n%q", got, again)
	}
}

func TestTracingExecutorRecordsStdout(t *testing.T) {
	sink := &traceSinkStub{}
	tr := NewTracing(&MockExecutor{Responses: []MockCall{{Stdout: "[x] boom\n", Stderr: "warn\n"}}}, sink)
	if _, _, err := tr.Run(context.Background(), "apm", "install", "-g"); err != nil {
		t.Fatal(err)
	}
	if len(sink.records) != 1 {
		t.Fatalf("records = %#v", sink.records)
	}
	if sink.records[0].Stdout != "[x] boom" || sink.records[0].Stderr != "warn" {
		t.Fatalf("record = %+v", sink.records[0])
	}
}
