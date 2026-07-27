package app_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

// The Execute signal handler cancels this context; before it reached Open, Ctrl+C during a contended database open did nothing for the whole busy budget.
func TestInit_HonoursACancelledContext(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := config.Save(cfgPath, &config.RootConfig{Version: config.CurrentVersion}); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath)
	a.CacheDir = dir
	t.Cleanup(func() { _ = a.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := a.Init(ctx)
	if err == nil {
		t.Fatal("Init with a cancelled context should fail rather than run the busy budget")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Init error = %v, want it to wrap context.Canceled", err)
	}
	// Distinguishes the open from the migrate: with a context-free Open the cancellation is only noticed one step later, by Migrate, after the busy budget has already been spent.
	if !strings.HasPrefix(err.Error(), "opening database") {
		t.Fatalf("Init error = %v, want the cancellation observed by the open, not a later step", err)
	}
}
