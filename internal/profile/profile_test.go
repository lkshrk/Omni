package profile

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnabled(t *testing.T) {
	t.Setenv(EnvName, "")
	if Enabled() {
		t.Fatal("expected profiling to be disabled by default")
	}

	t.Setenv(EnvName, "1")
	if !Enabled() {
		t.Fatal("expected profiling to be enabled")
	}

	t.Setenv(EnvName, "false")
	if Enabled() {
		t.Fatal("expected false to disable profiling")
	}
}

func TestStartWritesElapsedPhase(t *testing.T) {
	var buf bytes.Buffer
	restore := setWriterForTest(&buf)
	defer restore()
	t.Setenv(EnvName, "1")

	done := Start("test.phase")
	done()

	got := buf.String()
	if !strings.Contains(got, "[omni profile] test.phase ") {
		t.Fatalf("expected profile line with phase name, got %q", got)
	}
}

func TestStartDisabledWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	restore := setWriterForTest(&buf)
	defer restore()
	t.Setenv(EnvName, "")

	done := Start("test.phase")
	done()

	if got := buf.String(); got != "" {
		t.Fatalf("expected no profile output, got %q", got)
	}
}

func TestLogCanAppendToFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "omni-profile.log")
	t.Setenv(EnvName, path)

	Log("file.phase", 1500*time.Microsecond)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read profile output: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "[omni profile] file.phase 1.5ms") {
		t.Fatalf("expected file profile output, got %q", got)
	}
}
