package app_test

import (
	"context"
	"testing"
)

// ─── ListDiscovered ───────────────────────────────────────────────────────────

func TestListDiscovered_EmptyInitially(t *testing.T) {
	a, _ := newImportApp(t)

	discovered, err := a.ListDiscovered(context.Background())
	if err != nil {
		t.Fatalf("ListDiscovered: %v", err)
	}
	if len(discovered) != 0 {
		t.Errorf("expected 0 discovered tools on fresh app, got %d", len(discovered))
	}
}

// ─── RefreshDiscovered ────────────────────────────────────────────────────────

func TestRefreshDiscovered_NoPanic(t *testing.T) {
	a, _ := newImportApp(t)

	// Best-effort: skips providers that are unavailable (CI has no brew/apt/etc.).
	// Must not return an error or panic.
	if err := a.RefreshDiscovered(context.Background()); err != nil {
		t.Fatalf("RefreshDiscovered returned unexpected error: %v", err)
	}
}
