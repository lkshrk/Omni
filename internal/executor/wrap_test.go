package executor

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestWrapErrorPrefersStderr(t *testing.T) {
	base := errors.New("exit status 1")
	err := WrapError(base, "brew info", "some stdout", "real failure")
	if !errors.Is(err, base) {
		t.Fatalf("does not wrap base error: %v", err)
	}
	if got := err.Error(); got != "brew info: exit status 1: real failure" {
		t.Fatalf("error = %q", got)
	}
}

func TestWrapErrorFallsBackToStdout(t *testing.T) {
	err := WrapError(errors.New("exit status 1"), "apm update failed", "No apm.yml found\n", "")
	if got := err.Error(); got != "apm update failed: exit status 1: No apm.yml found" {
		t.Fatalf("error = %q", got)
	}
}

func TestWrapErrorNoOutput(t *testing.T) {
	err := WrapError(errors.New("exit status 7"), "dnf repoquery", "  \n", "\t")
	if got := err.Error(); got != "dnf repoquery: exit status 7" {
		t.Fatalf("error = %q", got)
	}
}

func TestWrapErrorTruncatesToTailOnLineBoundary(t *testing.T) {
	long := strings.Repeat("progress line\n", 400) + "final error: ref not found"
	err := WrapError(errors.New("exit status 1"), "x", long, "")
	got := err.Error()
	if !strings.HasSuffix(got, "final error: ref not found") {
		t.Fatalf("tail lost: %q", got)
	}
	if !strings.Contains(got, "… ") {
		t.Fatalf("no truncation marker: %q", got)
	}
	if strings.Contains(got, "… progress line\nprogress") && len(got) > 2200 {
		t.Fatalf("not truncated: %d bytes", len(got))
	}
	if len(got) > 2200 {
		t.Fatalf("error too long: %d bytes", len(got))
	}
}

func TestTailDetailNeverSplitsUTF8Rune(t *testing.T) {
	for pad := 0; pad < 3; pad++ {
		detail := strings.Repeat("x", pad) + strings.Repeat("日", 900)
		got := tailDetail(detail)
		if !utf8.ValidString(got) {
			t.Fatalf("pad %d: invalid UTF-8 in tail: %q", pad, got[:12])
		}
	}
}

func TestTailDetailShortInputUnchanged(t *testing.T) {
	if got := tailDetail("short"); got != "short" {
		t.Fatalf("tailDetail = %q", got)
	}
}
