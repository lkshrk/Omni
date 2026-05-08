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
