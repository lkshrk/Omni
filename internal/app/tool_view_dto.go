package app

import (
	"database/sql"
	"time"

	"github.com/lkshrk/omni/internal/database"
)

// ToolView — A plain value type so the UI does not depend on the persistence schema; empty string means absent for nullable columns.
type ToolView struct {
	ID              int64
	Name            string
	Provider        string
	Package         string
	Installed       bool
	InstalledWith   string
	Version         string
	Outdated        bool
	OutdatedUnknown bool
	LatestVersion   string
	Description     string
	LastChecked     time.Time
	FailedAt        *time.Time
	FailureCount    int
	LastError       string
	Tracked         bool
	Privilege       string
	PrivilegeReason string
	PrivilegeAt     *time.Time

	// View-only fields (ToolCache carries these as bun:"-").
	Options            map[string]string
	UpdateBlocked      string
	UpdateBlockedUntil *time.Time
	UpdateAvailableAt  *time.Time
	UpdateDateSource   string
}

// Returns nil for a nil input so slice conversions pass rows through unchanged.
func toolViewFromCache(t *database.ToolCache) *ToolView {
	if t == nil {
		return nil
	}
	return &ToolView{
		ID:                 t.ID,
		Name:               t.Name,
		Provider:           t.Provider,
		Package:            t.Package,
		Installed:          t.Installed,
		InstalledWith:      t.InstalledWith,
		Version:            t.Version.String,
		Outdated:           t.Outdated,
		OutdatedUnknown:    t.OutdatedUnknown,
		LatestVersion:      t.LatestVersion.String,
		Description:        t.Description.String,
		LastChecked:        t.LastChecked,
		FailedAt:           t.FailedAt,
		FailureCount:       t.FailureCount,
		LastError:          t.LastError.String,
		Tracked:            t.Tracked,
		Privilege:          t.Privilege,
		PrivilegeReason:    t.PrivilegeReason.String,
		PrivilegeAt:        t.PrivilegeAt,
		Options:            t.Options,
		UpdateBlocked:      t.UpdateBlocked,
		UpdateBlockedUntil: t.UpdateBlockedUntil,
		UpdateAvailableAt:  t.UpdateAvailableAt,
		UpdateDateSource:   t.UpdateDateSource,
	}
}

// Used where a view row flows back into an app operation.
func toolCacheFromView(v *ToolView) *database.ToolCache {
	if v == nil {
		return nil
	}
	return &database.ToolCache{
		ID:                 v.ID,
		Name:               v.Name,
		Provider:           v.Provider,
		Package:            v.Package,
		Installed:          v.Installed,
		InstalledWith:      v.InstalledWith,
		Version:            nullString(v.Version),
		Outdated:           v.Outdated,
		OutdatedUnknown:    v.OutdatedUnknown,
		LatestVersion:      nullString(v.LatestVersion),
		Description:        nullString(v.Description),
		LastChecked:        v.LastChecked,
		FailedAt:           v.FailedAt,
		FailureCount:       v.FailureCount,
		LastError:          nullString(v.LastError),
		Tracked:            v.Tracked,
		Privilege:          v.Privilege,
		PrivilegeReason:    nullString(v.PrivilegeReason),
		PrivilegeAt:        v.PrivilegeAt,
		Options:            v.Options,
		UpdateBlocked:      v.UpdateBlocked,
		UpdateBlockedUntil: v.UpdateBlockedUntil,
		UpdateAvailableAt:  v.UpdateAvailableAt,
		UpdateDateSource:   v.UpdateDateSource,
	}
}

func toolViewsFromCache(rows []*database.ToolCache) []*ToolView {
	if rows == nil {
		return nil
	}
	out := make([]*ToolView, len(rows))
	for i, r := range rows {
		out[i] = toolViewFromCache(r)
	}
	return out
}

func toolCachesFromView(views []*ToolView) []*database.ToolCache {
	if views == nil {
		return nil
	}
	out := make([]*database.ToolCache, len(views))
	for i, v := range views {
		out[i] = toolCacheFromView(v)
	}
	return out
}

func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
