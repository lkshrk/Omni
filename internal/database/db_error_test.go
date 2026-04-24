package database_test

// ─── Error-path coverage ──────────────────────────────────────────────────────
//
// These tests exercise the error-return branches of DB methods by closing the
// underlying connection before calling the method. A closed SQLite connection
// causes every statement to return an error, hitting the fmt.Errorf return paths.

import (
	"context"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/database"
)

// zeroCutoff returns a zero time.Time value for use as a PruneDiscovered cutoff.
func zeroCutoff() time.Time {
	return time.Time{}
}

// closedDB returns a DB whose underlying connection is already closed.
// All operations on it will return an error, which exercises error-return paths.
func closedDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	// Close the connection — subsequent calls will fail.
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return db
}

func TestUpsert_ClosedDB_ReturnsError(t *testing.T) {
	db := closedDB(t)
	err := db.Upsert(context.Background(), &database.ToolCache{
		Name: "ripgrep", Provider: "brew", Package: "ripgrep",
	})
	if err == nil {
		t.Error("expected error on closed DB, got nil")
	}
}

func TestUpdateDescription_ClosedDB_ReturnsError(t *testing.T) {
	db := closedDB(t)
	err := db.UpdateDescription(context.Background(), "git", "brew", "git", "vcs")
	if err == nil {
		t.Error("expected error on closed DB, got nil")
	}
}

func TestDelete_ClosedDB_ReturnsError(t *testing.T) {
	db := closedDB(t)
	err := db.Delete(context.Background(), "ripgrep", "brew", "ripgrep")
	if err == nil {
		t.Error("expected error on closed DB, got nil")
	}
}

func TestMarkInstalled_ClosedDB_ReturnsError(t *testing.T) {
	db := closedDB(t)
	err := db.MarkInstalled(context.Background(), "ripgrep", "brew", "ripgrep", "14.1.0")
	if err == nil {
		t.Error("expected error on closed DB, got nil")
	}
}

func TestMarkFailed_ClosedDB_ReturnsError(t *testing.T) {
	db := closedDB(t)
	err := db.MarkFailed(context.Background(), "ripgrep", "brew", "ripgrep", "install failed")
	if err == nil {
		t.Error("expected error on closed DB, got nil")
	}
}

func TestMarkUninstalled_ClosedDB_ReturnsError(t *testing.T) {
	db := closedDB(t)
	err := db.MarkUninstalled(context.Background(), "ripgrep", "brew", "ripgrep")
	if err == nil {
		t.Error("expected error on closed DB, got nil")
	}
}

func TestMarkTracked_ClosedDB_ReturnsError(t *testing.T) {
	db := closedDB(t)
	err := db.MarkTracked(context.Background(), "ripgrep", "brew", "ripgrep")
	if err == nil {
		t.Error("expected error on closed DB, got nil")
	}
}

func TestPruneDiscovered_ClosedDB_ReturnsError(t *testing.T) {
	db := closedDB(t)
	err := db.PruneDiscovered(context.Background(), zeroCutoff())
	if err == nil {
		t.Error("expected error on closed DB, got nil")
	}
}

func TestList_ClosedDB_ReturnsError(t *testing.T) {
	db := closedDB(t)
	_, err := db.List(context.Background())
	if err == nil {
		t.Error("expected error on closed DB, got nil")
	}
}

func TestListByProvider_ClosedDB_ReturnsError(t *testing.T) {
	db := closedDB(t)
	_, err := db.ListByProvider(context.Background(), "brew")
	if err == nil {
		t.Error("expected error on closed DB, got nil")
	}
}

func TestUpdateOutdated_ClosedDB_ReturnsError(t *testing.T) {
	db := closedDB(t)
	err := db.UpdateOutdated(context.Background(), "ripgrep", "brew", "ripgrep", true, "15.0.0")
	if err == nil {
		t.Error("expected error on closed DB, got nil")
	}
}

func TestListFailed_ClosedDB_ReturnsError(t *testing.T) {
	db := closedDB(t)
	_, err := db.ListFailed(context.Background())
	if err == nil {
		t.Error("expected error on closed DB, got nil")
	}
}

func TestUpsertDiscovered_ClosedDB_ReturnsError(t *testing.T) {
	db := closedDB(t)
	err := db.UpsertDiscovered(context.Background(), "ripgrep", "brew", "brew", "14.1.0")
	if err == nil {
		t.Error("expected error on closed DB, got nil")
	}
}

func TestListDiscovered_ClosedDB_ReturnsError(t *testing.T) {
	db := closedDB(t)
	_, err := db.ListDiscovered(context.Background())
	if err == nil {
		t.Error("expected error on closed DB, got nil")
	}
}
