package app

import (
	"database/sql"
	"time"

	"github.com/lkshrk/omni/internal/database"
)

// ToolView is the app→TUI contract for a tool row. It is a plain value type: no
// bun.BaseModel, no column tags, and sql.NullString flattened to string. This
// decouples the UI and app business logic from the persistence schema
// (database.ToolCache), so the SQLite/bun row can evolve without rippling into
// the view. It carries every ToolCache field so the conversion round-trips
// losslessly for tools that flow back into app operations (e.g. SyncAll's
// discovered set); once those round-trips are removed the surface can be
// trimmed to what the view actually reads.
//
// The nullable columns (Version, LatestVersion, Description, LastError,
// PrivilegeReason) use empty string for "absent" — every reader already treats
// empty as absent, so no NULL-vs-"" distinction is lost.
type ToolView struct {
	ID              int64
	Name            string
	Provider        string
	Package         string
	Installed       bool
	InstalledWith   string
	Version         string
	Outdated        bool
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

// toolViewFromCache maps a persistence row to the view DTO. Returns nil for a
// nil input so slice conversions can pass rows through unchanged.
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

// toolCacheFromView rebuilds a persistence row from the view DTO. Used where a
// view row flows back into an app operation (e.g. SyncAll's discovered set)
// until those round-trips are removed.
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

// toolViewsFromCache converts a slice, preserving order and nil entries.
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

// toolCachesFromView is the inverse of toolViewsFromCache.
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

// nullString wraps s as an absent value when empty, matching how every reader
// treats an empty nullable column.
func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
