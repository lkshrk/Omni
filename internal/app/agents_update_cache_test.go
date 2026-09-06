package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/apm"
)

func newAgentsCacheTestApp(t *testing.T) *App {
	t.Helper()
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func TestCacheAgentsOutdated_RoundTrip(t *testing.T) {
	ctx := context.Background()
	a := newAgentsCacheTestApp(t)

	want := apm.OutdatedResult{
		Rows: []apm.OutdatedRow{
			{Package: "zeta/tool", Current: "1.0.0", Latest: "1.1.0", Source: "git tags"},
			{Package: "acme/tool", Current: "2.0.0", Latest: "2.5.0", Source: "registry"},
		},
		Unknown: 3,
	}
	if err := a.CacheAgentsOutdated(ctx, want); err != nil {
		t.Fatalf("CacheAgentsOutdated: %v", err)
	}

	got := a.CachedAgentsOutdated(ctx)
	if got.Unknown != 3 {
		t.Fatalf("Unknown = %d, want 3", got.Unknown)
	}
	if len(got.Rows) != 2 {
		t.Fatalf("rows = %#v, want 2", got.Rows)
	}
	if got.Rows[0] != (apm.OutdatedRow{Package: "acme/tool", Current: "2.0.0", Latest: "2.5.0", Source: "registry"}) {
		t.Fatalf("rows[0] = %#v", got.Rows[0])
	}
	if got.Rows[1] != (apm.OutdatedRow{Package: "zeta/tool", Current: "1.0.0", Latest: "1.1.0", Source: "git tags"}) {
		t.Fatalf("rows[1] = %#v", got.Rows[1])
	}
}

func TestCachedAgentsOutdated_EmptyWhenNothingCached(t *testing.T) {
	a := newAgentsCacheTestApp(t)

	got := a.CachedAgentsOutdated(context.Background())
	if len(got.Rows) != 0 || got.Unknown != 0 {
		t.Fatalf("cached result = %#v, want empty", got)
	}
}

func TestForgetAgentsOutdated_ClearsRowsAndUnknown(t *testing.T) {
	ctx := context.Background()
	a := newAgentsCacheTestApp(t)

	if err := a.CacheAgentsOutdated(ctx, apm.OutdatedResult{
		Rows:    []apm.OutdatedRow{{Package: "acme/tool", Current: "1.0.0", Latest: "2.0.0"}},
		Unknown: 2,
	}); err != nil {
		t.Fatalf("CacheAgentsOutdated: %v", err)
	}
	if err := a.ForgetAgentsOutdated(ctx); err != nil {
		t.Fatalf("ForgetAgentsOutdated: %v", err)
	}

	got := a.CachedAgentsOutdated(ctx)
	if len(got.Rows) != 0 || got.Unknown != 0 {
		t.Fatalf("cached result = %#v, want cleared", got)
	}
}
