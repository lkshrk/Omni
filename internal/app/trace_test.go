package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/executor"
)

func TestRecordCommandTraceSanitizesDirectRecordBeforePersistence(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := config.Save(cfgPath, &config.RootConfig{Version: config.CurrentVersion}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	a := New(cfgPath)
	a.CacheDir = dir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	raw := executor.TraceRecord{
		Reason:  "reason\x00 TOKEN=reasonsecret\rnext",
		Command: "tool TOKEN=commandsecret\x08",
		Status:  "failed\x7f",
		Error:   "AUTH_TOKEN=errorsecret\u0085retry",
		Stderr:  "\x1b[31mred\x1b[0m\x1b]0;title\x07" + string([]byte{0xff}) + "界",
	}
	if err := a.RecordCommandTrace(context.Background(), raw); err != nil {
		t.Fatalf("RecordCommandTrace: %v", err)
	}

	traces, err := a.CommandTraces(context.Background(), 1)
	if err != nil {
		t.Fatalf("CommandTraces: %v", err)
	}
	if len(traces) != 1 {
		t.Fatalf("traces = %d, want 1", len(traces))
	}
	trace := traces[0]
	if trace.Reason != "reason TOKEN=[redacted]\nnext" {
		t.Errorf("reason = %q", trace.Reason)
	}
	if trace.Command != "tool TOKEN=[redacted]" {
		t.Errorf("command = %q", trace.Command)
	}
	if trace.Status != "failed" {
		t.Errorf("status = %q", trace.Status)
	}
	if trace.Error != "AUTH_TOKEN=[redacted]" {
		t.Errorf("error = %q", trace.Error)
	}
	if trace.Stderr != "red界" {
		t.Errorf("stderr = %q", trace.Stderr)
	}
}
