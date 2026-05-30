package database_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/database"
)

func TestUpdateMetadata_UpsertAndGet(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	availableAt := time.Date(2026, 5, 30, 10, 0, 0, 0, time.UTC)
	checkedAt := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

	if _, err := db.GetUpdateMetadata(ctx, "npm", "typescript", "5.8.0"); err == nil || err != sql.ErrNoRows {
		t.Fatalf("GetUpdateMetadata missing err = %v, want sql.ErrNoRows", err)
	}
	if err := db.UpsertUpdateMetadata(ctx, database.UpdateMetadata{
		Provider:    "npm",
		Package:     "typescript",
		Version:     "5.8.0",
		AvailableAt: availableAt,
		DateSource:  "npm_time",
		CheckedAt:   checkedAt,
	}); err != nil {
		t.Fatalf("UpsertUpdateMetadata: %v", err)
	}
	got, err := db.GetUpdateMetadata(ctx, "npm", "typescript", "5.8.0")
	if err != nil {
		t.Fatalf("GetUpdateMetadata: %v", err)
	}
	if got.Provider != "npm" || got.Package != "typescript" || got.Version != "5.8.0" {
		t.Fatalf("metadata key = %+v, want npm/typescript/5.8.0", got)
	}
	if !got.AvailableAt.Equal(availableAt) || got.DateSource != "npm_time" || !got.CheckedAt.Equal(checkedAt) {
		t.Fatalf("metadata = %+v, want available/source/checked preserved", got)
	}

	updatedAt := availableAt.Add(2 * time.Hour)
	if err := db.UpsertUpdateMetadata(ctx, database.UpdateMetadata{
		Provider:    "npm",
		Package:     "typescript",
		Version:     "5.8.0",
		AvailableAt: updatedAt,
		DateSource:  "npm_time",
		CheckedAt:   checkedAt.Add(time.Hour),
	}); err != nil {
		t.Fatalf("UpsertUpdateMetadata update: %v", err)
	}
	got, err = db.GetUpdateMetadata(ctx, "npm", "typescript", "5.8.0")
	if err != nil {
		t.Fatalf("GetUpdateMetadata after update: %v", err)
	}
	if !got.AvailableAt.Equal(updatedAt) {
		t.Fatalf("AvailableAt = %s, want %s", got.AvailableAt, updatedAt)
	}
}
