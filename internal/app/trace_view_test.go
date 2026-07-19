package app

import (
	"database/sql"
	"testing"

	"github.com/lkshrk/omni/internal/database"
)

func TestCommandTraceViewFromRow_FlattensExitCodeAndFields(t *testing.T) {
	t.Parallel()
	code := int64(0)
	row := database.CommandTrace{
		ID:         7,
		DurationMS: 123,
		Reason:     "install missing tool",
		Command:    "brew install ripgrep",
		Status:     "ok",
		Error:      "boom",
		Stderr:     "stderr text",
		ExitCode:   sql.NullInt64{Int64: code, Valid: true},
	}
	v := commandTraceViewFromRow(row)

	if v.ExitCode == nil {
		t.Fatal("ExitCode: got nil, want a set *int64 for a valid NullInt64")
	}
	if *v.ExitCode != code {
		t.Errorf("ExitCode = %d, want %d", *v.ExitCode, code)
	}
	if v.ID != row.ID || v.DurationMS != row.DurationMS || v.Reason != row.Reason ||
		v.Command != row.Command || v.Status != row.Status || v.Error != row.Error ||
		v.Stderr != row.Stderr {
		t.Errorf("field mismatch: got %+v from row %+v", v, row)
	}
}

func TestCommandTraceViewFromRow_AbsentExitCodeIsNil(t *testing.T) {
	t.Parallel()
	v := commandTraceViewFromRow(database.CommandTrace{ExitCode: sql.NullInt64{Valid: false}})
	if v.ExitCode != nil {
		t.Errorf("ExitCode = %v, want nil for an invalid NullInt64", *v.ExitCode)
	}
}
