package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/lkshrk/omni/internal/testguard"
)

// Holds a real write lock so the predicate is checked against a driver-produced SQLITE_BUSY, not a fabricated one.
func TestIsBusyErr(t *testing.T) {
	path := filepath.Join(t.TempDir(), "busy.db")

	holder, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatalf("open holder: %v", err)
	}
	defer holder.Close()
	holder.SetMaxOpenConns(1)

	conn, err := holder.Conn(context.Background())
	if err != nil {
		t.Fatalf("holder conn: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), "BEGIN EXCLUSIVE"); err != nil {
		t.Fatalf("begin exclusive: %v", err)
	}

	other, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatalf("open other: %v", err)
	}
	defer other.Close()

	_, busyErr := other.ExecContext(context.Background(), "CREATE TABLE t (x INTEGER)")
	if busyErr == nil {
		t.Fatal("write against an exclusively locked database succeeded, want SQLITE_BUSY")
	}
	if !isBusyErr(busyErr) {
		t.Errorf("isBusyErr(%v) = false, want true", busyErr)
	}

	if _, err := conn.ExecContext(context.Background(), "ROLLBACK"); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	_, syntaxErr := other.ExecContext(context.Background(), "SELECT nope FROM nowhere")
	if syntaxErr == nil {
		t.Fatal("bogus statement succeeded")
	}
	if isBusyErr(syntaxErr) {
		t.Errorf("isBusyErr(%v) = true, want false", syntaxErr)
	}
}
