package database_test

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/database"
)

func markOutdatedUnknown(t *testing.T, db *database.DB) {
	t.Helper()
	ctx := context.Background()
	if err := db.Upsert(ctx, &database.ToolCache{Name: "docker", Provider: "script", Package: "docker", Installed: true}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := db.UpdateOutdatedBatch(ctx, []database.OutdatedUpdate{{
		Name: "docker", Provider: "script", Package: "docker", OutdatedUnknown: true,
	}}); err != nil {
		t.Fatalf("UpdateOutdatedBatch: %v", err)
	}
	got, err := db.Get(ctx, "docker", "script", "docker")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.OutdatedUnknown {
		t.Fatal("fixture did not set outdated_unknown")
	}
}

func assertOutdatedUnknownCleared(t *testing.T, db *database.DB) {
	t.Helper()
	got, err := db.Get(context.Background(), "docker", "script", "docker")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.OutdatedUnknown {
		t.Fatal("outdated_unknown still set; the row keeps offering an upgrade that already happened")
	}
}

func TestUpdateOutdated_ClearsOutdatedUnknown(t *testing.T) {
	db := newTestDB(t)
	markOutdatedUnknown(t, db)
	if err := db.UpdateOutdated(context.Background(), "docker", "script", "docker", false, ""); err != nil {
		t.Fatalf("UpdateOutdated: %v", err)
	}
	assertOutdatedUnknownCleared(t, db)
}

func TestMarkInstalled_ClearsOutdatedUnknown(t *testing.T) {
	db := newTestDB(t)
	markOutdatedUnknown(t, db)
	if err := db.MarkInstalled(context.Background(), "docker", "script", "docker", "2.0.0"); err != nil {
		t.Fatalf("MarkInstalled: %v", err)
	}
	assertOutdatedUnknownCleared(t, db)
}

func TestMarkUninstalled_ClearsOutdatedUnknown(t *testing.T) {
	db := newTestDB(t)
	markOutdatedUnknown(t, db)
	if err := db.MarkUninstalled(context.Background(), "docker", "script", "docker"); err != nil {
		t.Fatalf("MarkUninstalled: %v", err)
	}
	assertOutdatedUnknownCleared(t, db)
}

func TestOpenContext_CancelledContextDoesNotSpin(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	db, err := database.OpenContext(ctx, ":memory:")
	if err == nil {
		_ = db.Close()
		t.Fatal("OpenContext with a cancelled context should fail rather than run the busy budget")
	}
}
