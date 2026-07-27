package app

import (
	"database/sql"
	"reflect"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/database"
)

func TestToolViewRoundTripLossless(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Truncate(time.Second)
	failedAt := now.Add(-time.Hour)
	blockedUntil := now.Add(time.Hour)

	orig := &database.ToolCache{
		ID:                 7,
		Name:               "ripgrep",
		Provider:           "brew",
		Package:            "rg",
		Installed:          true,
		InstalledWith:      "brew",
		Version:            sql.NullString{String: "14.1.0", Valid: true},
		Outdated:           true,
		LatestVersion:      sql.NullString{String: "14.1.1", Valid: true},
		Description:        sql.NullString{String: "fast grep", Valid: true},
		LastChecked:        now,
		FailedAt:           &failedAt,
		FailureCount:       2,
		LastError:          sql.NullString{String: "boom", Valid: true},
		Tracked:            true,
		Privilege:          "sudo",
		PrivilegeReason:    sql.NullString{String: "needs root", Valid: true},
		PrivilegeAt:        &now,
		Options:            map[string]string{"tap": "custom/tap"},
		UpdateBlocked:      "quarantine",
		UpdateBlockedUntil: &blockedUntil,
		UpdateAvailableAt:  &now,
		UpdateDateSource:   "release",
	}

	got := toolCacheFromView(toolViewFromCache(orig))

	if !reflect.DeepEqual(orig, got) {
		t.Fatalf("round-trip changed the row:\n orig = %+v\n got  = %+v", orig, got)
	}
}

func TestToolViewFromCacheNilSafe(t *testing.T) {
	t.Parallel()
	if toolViewFromCache(nil) != nil {
		t.Fatal("toolViewFromCache(nil) should be nil")
	}
	if toolCacheFromView(nil) != nil {
		t.Fatal("toolCacheFromView(nil) should be nil")
	}
	if toolViewsFromCache(nil) != nil || toolCachesFromView(nil) != nil {
		t.Fatal("slice converters should pass nil through")
	}
}

func TestToolViewEmptyNullableIsAbsent(t *testing.T) {
	t.Parallel()
	v := toolViewFromCache(&database.ToolCache{Name: "x"})
	if v.Version != "" || v.Description != "" {
		t.Fatal("absent nullable columns should flatten to empty string")
	}
	back := toolCacheFromView(v)
	if back.Version.Valid || back.Description.Valid {
		t.Fatal("empty view strings should rebuild as absent (Valid=false)")
	}
}
