package executor

import (
	"context"
	"strings"
	"testing"
)

func TestRun_OutputLimitTruncatesWithoutLosingTheHead(t *testing.T) {
	ctx := WithOutputLimit(context.Background(), 32)
	stdout, _, err := (&RealExecutor{}).Run(ctx, "sh", "-c", `printf 'tool 1.2.3\n'; head -c 200000 /dev/zero | tr '\0' 'x'`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(stdout) != 32 {
		t.Fatalf("captured %d bytes, want the 32-byte cap", len(stdout))
	}
	if !strings.HasPrefix(stdout, "tool 1.2.3\n") {
		t.Fatalf("stdout = %q, want the head of the stream preserved", stdout)
	}
}

func TestRun_StderrIsCappedIndependently(t *testing.T) {
	ctx := WithOutputLimit(context.Background(), 16)
	_, stderr, err := (&RealExecutor{}).Run(ctx, "sh", "-c", `head -c 100000 /dev/zero | tr '\0' 'y' >&2`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(stderr) != 16 {
		t.Fatalf("captured %d stderr bytes, want the 16-byte cap", len(stderr))
	}
}

func TestRun_WithoutLimitCapturesLargeOutput(t *testing.T) {
	stdout, _, err := (&RealExecutor{}).Run(context.Background(), "sh", "-c", `head -c 100000 /dev/zero | tr '\0' 'z'`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(stdout) != 100000 {
		t.Fatalf("captured %d bytes, want the full 100000 for an unlimited caller", len(stdout))
	}
}
