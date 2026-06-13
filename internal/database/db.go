// Package database manages the SQLite tool-cache using bun.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	_ "modernc.org/sqlite" // register "sqlite" driver

	"github.com/lkshrk/omni/internal/testguard"
)

// ToolCache is the database model for a cached tool entry.
type ToolCache struct {
	bun.BaseModel `bun:"table:tool_cache,alias:tc"`

	ID                 int64             `bun:"id,pk,autoincrement"`
	Name               string            `bun:"name,notnull"`
	Provider           string            `bun:"provider,notnull"`
	Package            string            `bun:"package,notnull"`
	Installed          bool              `bun:"installed,notnull,default:false"`
	InstalledWith      string            `bun:"installed_with,notnull,default:''"`
	Version            sql.NullString    `bun:"version"`
	Outdated           bool              `bun:"outdated,notnull,default:false"`
	LatestVersion      sql.NullString    `bun:"latest_version"`
	Description        sql.NullString    `bun:"description"`
	LastChecked        time.Time         `bun:"last_checked,notnull"`
	FailedAt           *time.Time        `bun:"failed_at"`
	FailureCount       int               `bun:"failure_count,notnull,default:0"`
	LastError          sql.NullString    `bun:"last_error"`
	Tracked            bool              `bun:"tracked,notnull,default:true"`
	Privilege          string            `bun:"privilege,notnull,default:''"`
	PrivilegeReason    sql.NullString    `bun:"privilege_reason"`
	PrivilegeAt        *time.Time        `bun:"privilege_at"`
	Options            map[string]string `bun:"-"`
	UpdateBlocked      string            `bun:"-"`
	UpdateBlockedUntil *time.Time        `bun:"-"`
	UpdateAvailableAt  *time.Time        `bun:"-"`
	UpdateDateSource   string            `bun:"-"`
}

// ToolMetadata is provider registry metadata cached independently from
// install/config state.
type ToolMetadata struct {
	bun.BaseModel `bun:"table:tool_metadata,alias:tm"`

	ID              int64          `bun:"id,pk,autoincrement"`
	Name            string         `bun:"name,notnull"`
	Provider        string         `bun:"provider,notnull"`
	Package         string         `bun:"package,notnull"`
	Version         sql.NullString `bun:"version"`
	Description     sql.NullString `bun:"description"`
	SourceType      string         `bun:"source_type,notnull,default:''"`
	SourceOwner     string         `bun:"source_owner,notnull,default:''"`
	SourceRepo      string         `bun:"source_repo,notnull,default:''"`
	SourceURL       sql.NullString `bun:"source_url"`
	Privilege       string         `bun:"privilege,notnull,default:''"`
	PrivilegeReason sql.NullString `bun:"privilege_reason"`
	UpdatedAt       time.Time      `bun:"updated_at,notnull"`
}

// UpdateMetadata is package-manager metadata for a concrete package version.
// It is keyed by the concrete provider/manager because availability timestamps
// are only meaningful for the PM that reported them.
type UpdateMetadata struct {
	bun.BaseModel `bun:"table:update_metadata,alias:um"`

	ID          int64     `bun:"id,pk,autoincrement"`
	Provider    string    `bun:"provider,notnull"`
	Package     string    `bun:"package,notnull"`
	Version     string    `bun:"version,notnull"`
	AvailableAt time.Time `bun:"available_at,notnull"`
	DateSource  string    `bun:"date_source,notnull"`
	CheckedAt   time.Time `bun:"checked_at,notnull"`
}

// LocalState stores machine-local app markers. These rows are intentionally in
// the cache DB, not settings.json, because they describe this checkout/host.
type LocalState struct {
	bun.BaseModel `bun:"table:local_state,alias:ls"`

	Key       string    `bun:"key,pk,notnull"`
	Value     string    `bun:"value,notnull"`
	UpdatedAt time.Time `bun:"updated_at,notnull"`
}

// PackageAvailability stores explicit provider/package availability probes.
type PackageAvailability struct {
	bun.BaseModel `bun:"table:package_availability,alias:pa"`

	ID        int64     `bun:"id,pk,autoincrement"`
	Name      string    `bun:"name,notnull"`
	Provider  string    `bun:"provider,notnull"`
	Package   string    `bun:"package,notnull"`
	Available bool      `bun:"available,notnull,default:false"`
	Reason    string    `bun:"reason,notnull,default:''"`
	CheckedAt time.Time `bun:"checked_at,notnull"`
}

// CommandTrace records subprocesses Omni issues through the shared executor.
// It is intentionally compact: stdout is not stored, and stderr/error fields
// are caller-truncated/redacted before insertion.
type CommandTrace struct {
	bun.BaseModel `bun:"table:command_traces,alias:ct"`

	ID         int64         `bun:"id,pk,autoincrement"`
	StartedAt  time.Time     `bun:"started_at,notnull"`
	FinishedAt time.Time     `bun:"finished_at,notnull"`
	DurationMS int64         `bun:"duration_ms,notnull,default:0"`
	Reason     string        `bun:"reason,notnull,default:''"`
	Command    string        `bun:"command,type:TEXT,notnull"`
	Status     string        `bun:"status,notnull,default:''"`
	ExitCode   sql.NullInt64 `bun:"exit_code"`
	Error      string        `bun:"error,type:TEXT,notnull,default:''"`
	Stderr     string        `bun:"stderr,type:TEXT,notnull,default:''"`
}

// TrustedTap records a Homebrew tap omni has already run `brew trust` on, so
// tap sync can skip the trust call on subsequent runs.
type TrustedTap struct {
	bun.BaseModel `bun:"table:trusted_taps,alias:tt"`

	Name      string    `bun:"name,pk"`
	TrustedAt time.Time `bun:"trusted_at,notnull"`
}

type DotStatusCache struct {
	bun.BaseModel `bun:"table:dot_status_cache,alias:dsc"`

	ID           int64     `bun:"id,pk,autoincrement"`
	Name         string    `bun:"name,notnull"`
	Package      string    `bun:"package,notnull,default:''"`
	Variant      bool      `bun:"variant,notnull,default:false"`
	SourcePath   string    `bun:"source_path,notnull,default:''"`
	TargetPath   string    `bun:"target_path,notnull,default:''"`
	ConfigPath   string    `bun:"config_path,notnull,default:''"`
	Health       string    `bun:"health,notnull,default:''"`
	State        string    `bun:"state,notnull,default:''"`
	ActionsJSON  string    `bun:"actions_json,type:TEXT,notnull,default:''"`
	Group        string    `bun:"group_name,notnull,default:''"`
	FileCount    int       `bun:"file_count,notnull,default:0"`
	CountsJSON   string    `bun:"counts_json,type:TEXT,notnull,default:''"`
	IsDir        bool      `bun:"is_dir,notnull,default:false"`
	ChildrenJSON string    `bun:"children_json,type:TEXT,notnull,default:''"`
	Position     int       `bun:"position,notnull,default:0"`
	ObservedAt   time.Time `bun:"observed_at,notnull"`
}

type DotSnapshotMeta struct {
	bun.BaseModel `bun:"table:dot_snapshot_meta,alias:dsm"`

	Key             string    `bun:"key,pk,notnull"`
	GitStatus       string    `bun:"git_status,type:TEXT,notnull,default:''"`
	DiscoveredCount int       `bun:"discovered_count,notnull,default:0"`
	ObservedAt      time.Time `bun:"observed_at,notnull"`
}

type DotsSnapshot struct {
	Entries         []*DotStatusCache
	GitStatus       string
	DiscoveredCount int
	ObservedAt      time.Time
}

const dotsSnapshotMetaKey = "current"
const providerListCacheClearStateKey = "migration.provider_list_cache_cleared"
const commandTraceRetentionLimit = 5000

// MetadataUpdate is registry metadata learned without changing install state.
type MetadataUpdate struct {
	Name            string
	Provider        string
	Package         string
	Version         string
	Description     string
	SourceType      string
	SourceOwner     string
	SourceRepo      string
	SourceURL       string
	Privilege       string
	PrivilegeReason string
}

// DB wraps a bun.DB and provides typed tool-cache operations.
type DB struct {
	bun *bun.DB
}

func requirePackage(name, provider, pkg string) error {
	if pkg == "" {
		return fmt.Errorf("missing package for %s/%s cache key", provider, name)
	}
	return nil
}

func metadataKey(name, provider, pkg string) string {
	return name + "\x00" + provider + "\x00" + pkg
}

// Open opens (or creates) the SQLite database at path and returns a DB.
// Use ":memory:" for an in-process test database.
func Open(path string) (*DB, error) {
	if path != ":memory:" {
		if err := testguard.RequireTempPath("database", path); err != nil {
			return nil, err
		}
		// 0o700: cache directory holds the per-user tool DB; restrict to owner.
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("creating database directory: %w", err)
		}
	}
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite at %s: %w", path, err)
	}
	// SQLite is single-writer; limit to 1 connection to avoid BUSY errors.
	sqlDB.SetMaxOpenConns(1)

	// Enable WAL mode so concurrent readers (TUI, background sync) don't block
	// each other. Must be set before any schema work.
	if _, err := sqlDB.ExecContext(context.Background(), "PRAGMA journal_mode=WAL"); err != nil {
		return nil, fmt.Errorf("enabling WAL mode: %w", err)
	}

	bunDB := bun.NewDB(sqlDB, sqlitedialect.New())
	return &DB{bun: bunDB}, nil
}

// Close releases the underlying connection pool.
func (db *DB) Close() error {
	return db.bun.Close()
}

// Bun exposes the raw *bun.DB for advanced queries in tests.
func (db *DB) Bun() *bun.DB {
	return db.bun
}

// TrustedTaps returns the set of Homebrew taps omni has already trusted.
func (db *DB) TrustedTaps(ctx context.Context) (map[string]bool, error) {
	var rows []TrustedTap
	if err := db.bun.NewSelect().Model(&rows).Scan(ctx); err != nil {
		return nil, fmt.Errorf("loading trusted taps: %w", err)
	}
	out := make(map[string]bool, len(rows))
	for _, r := range rows {
		out[r.Name] = true
	}
	return out, nil
}

// MarkTapTrusted records that a tap has been trusted so future syncs skip the
// `brew trust` call.
func (db *DB) MarkTapTrusted(ctx context.Context, name string, at time.Time) error {
	_, err := db.bun.NewInsert().
		Model(&TrustedTap{Name: name, TrustedAt: at}).
		On("CONFLICT (name) DO UPDATE").
		Set("trusted_at = EXCLUDED.trusted_at").
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("recording trusted tap %s: %w", name, err)
	}
	return nil
}

// RecordCommandTrace appends one command trace and prunes older rows beyond the
// rotating retention limit. Callers should treat errors as non-fatal.
func (db *DB) RecordCommandTrace(ctx context.Context, trace *CommandTrace) error {
	if trace == nil {
		return nil
	}
	if trace.StartedAt.IsZero() {
		trace.StartedAt = time.Now().UTC()
	}
	if trace.FinishedAt.IsZero() {
		trace.FinishedAt = trace.StartedAt
	}
	if trace.DurationMS < 0 {
		trace.DurationMS = 0
	}
	trace.ID = 0
	if _, err := db.bun.NewInsert().Model(trace).Exec(ctx); err != nil {
		return fmt.Errorf("recording command trace: %w", err)
	}
	return db.pruneCommandTraces(ctx, commandTraceRetentionLimit)
}

func (db *DB) pruneCommandTraces(ctx context.Context, limit int) error {
	if limit <= 0 {
		return nil
	}
	_, err := db.bun.ExecContext(ctx, `
		DELETE FROM command_traces
		WHERE id NOT IN (
			SELECT id
			FROM command_traces
			ORDER BY started_at DESC, id DESC
			LIMIT ?
		)`, limit)
	if err != nil {
		return fmt.Errorf("pruning command traces: %w", err)
	}
	return nil
}

// ListCommandTraces returns newest command traces first.
func (db *DB) ListCommandTraces(ctx context.Context, limit int) ([]CommandTrace, error) {
	if limit <= 0 {
		limit = 50
	}
	var traces []CommandTrace
	if err := db.bun.NewSelect().
		Model(&traces).
		Order("started_at DESC", "id DESC").
		Limit(limit).
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("listing command traces: %w", err)
	}
	return traces, nil
}

// Migrate creates schema tables if they do not already exist (idempotent).
// For existing databases it also adds new columns via ALTER TABLE, suppressing
// "duplicate column" errors so the migration is safe to run repeatedly.
func (db *DB) Migrate(ctx context.Context) error {
	if err := db.dropLegacyToolCache(ctx); err != nil {
		return err
	}
	_, err := db.bun.NewCreateTable().
		Model((*ToolCache)(nil)).
		IfNotExists().
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("creating tool_cache table: %w", err)
	}
	_, err = db.bun.NewCreateTable().
		Model((*ToolMetadata)(nil)).
		IfNotExists().
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("creating tool_metadata table: %w", err)
	}
	_, err = db.bun.NewCreateTable().
		Model((*UpdateMetadata)(nil)).
		IfNotExists().
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("creating update_metadata table: %w", err)
	}
	_, err = db.bun.NewCreateTable().
		Model((*LocalState)(nil)).
		IfNotExists().
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("creating local_state table: %w", err)
	}
	_, err = db.bun.NewCreateTable().
		Model((*PackageAvailability)(nil)).
		IfNotExists().
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("creating package_availability table: %w", err)
	}
	_, err = db.bun.NewCreateTable().
		Model((*CommandTrace)(nil)).
		IfNotExists().
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("creating command_traces table: %w", err)
	}
	_, err = db.bun.NewCreateTable().
		Model((*DotStatusCache)(nil)).
		IfNotExists().
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("creating dot_status_cache table: %w", err)
	}
	_, err = db.bun.NewCreateTable().
		Model((*TrustedTap)(nil)).
		IfNotExists().
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("creating trusted_taps table: %w", err)
	}
	_, err = db.bun.NewCreateTable().
		Model((*DotSnapshotMeta)(nil)).
		IfNotExists().
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("creating dot_snapshot_meta table: %w", err)
	}
	// Ensure the unique index exists (bun doesn't auto-create indexes from tags).
	_, err = db.bun.ExecContext(ctx,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_tool_cache_name_provider_package
		 ON tool_cache (name, provider, package)`)
	if err != nil {
		return fmt.Errorf("creating unique index: %w", err)
	}
	_, err = db.bun.ExecContext(ctx,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_tool_metadata_name_provider_package
		 ON tool_metadata (name, provider, package)`)
	if err != nil {
		return fmt.Errorf("creating metadata unique index: %w", err)
	}
	_, err = db.bun.ExecContext(ctx,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_update_metadata_provider_package_version
		 ON update_metadata (provider, package, version)`)
	if err != nil {
		return fmt.Errorf("creating update metadata unique index: %w", err)
	}
	if _, err := db.bun.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_dot_status_cache_position ON dot_status_cache (position)`); err != nil {
		return fmt.Errorf("creating dot status cache position index: %w", err)
	}
	_, err = db.bun.ExecContext(ctx,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_package_availability_name_provider_package
		 ON package_availability (name, provider, package)`)
	if err != nil {
		return fmt.Errorf("creating package availability unique index: %w", err)
	}
	if _, err := db.bun.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_command_traces_started_at ON command_traces (started_at DESC)`); err != nil {
		return fmt.Errorf("creating command trace started_at index: %w", err)
	}
	// Add columns introduced after initial schema; duplicate-column errors are
	// expected (column already created by the CREATE TABLE above) and suppressed.
	// Any other error is returned.
	addCol := func(col, def string) error {
		_, e := db.bun.ExecContext(ctx, "ALTER TABLE tool_cache ADD COLUMN "+col+" "+def)
		if e != nil && !strings.Contains(e.Error(), "duplicate column") && !strings.Contains(e.Error(), "already has column") {
			return fmt.Errorf("adding column %s: %w", col, e)
		}
		return nil
	}
	for _, c := range []struct{ col, def string }{
		{"description", "TEXT"},
		{"outdated", "BOOLEAN NOT NULL DEFAULT FALSE"},
		{"latest_version", "TEXT"},
		{"failed_at", "DATETIME"},
		{"failure_count", "INTEGER NOT NULL DEFAULT 0"},
		{"last_error", "TEXT"},
		{"installed_with", "TEXT NOT NULL DEFAULT ''"},
		{"tracked", "BOOLEAN NOT NULL DEFAULT TRUE"},
		{"privilege", "TEXT NOT NULL DEFAULT ''"},
		{"privilege_reason", "TEXT"},
		{"privilege_at", "DATETIME"},
	} {
		if err := addCol(c.col, c.def); err != nil {
			return err
		}
	}
	addMetaCol := func(col, def string) error {
		_, e := db.bun.ExecContext(ctx, "ALTER TABLE tool_metadata ADD COLUMN "+col+" "+def)
		if e != nil && !strings.Contains(e.Error(), "duplicate column") && !strings.Contains(e.Error(), "already has column") {
			return fmt.Errorf("adding metadata column %s: %w", col, e)
		}
		return nil
	}
	for _, c := range []struct{ col, def string }{
		{"source_type", "TEXT NOT NULL DEFAULT ''"},
		{"source_owner", "TEXT NOT NULL DEFAULT ''"},
		{"source_repo", "TEXT NOT NULL DEFAULT ''"},
		{"source_url", "TEXT"},
	} {
		if err := addMetaCol(c.col, c.def); err != nil {
			return err
		}
	}
	// Secondary indices for filtered list queries hit on every TUI background
	// refresh. Without these, ListByProvider/ListDiscovered/ListFailed perform
	// full table scans.
	for _, idx := range []struct{ name, def string }{
		{"idx_tool_cache_provider", "ON tool_cache (provider)"},
		{"idx_tool_cache_tracked", "ON tool_cache (tracked)"},
		{"idx_tool_cache_failed_at", "ON tool_cache (failed_at) WHERE failed_at IS NOT NULL"},
	} {
		if _, err := db.bun.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS "+idx.name+" "+idx.def); err != nil {
			return fmt.Errorf("creating index %s: %w", idx.name, err)
		}
	}
	if err := db.migrateExistingToolMetadata(ctx); err != nil {
		return err
	}
	return db.clearProviderDerivedCacheForProviderList(ctx)
}

func (db *DB) clearProviderDerivedCacheForProviderList(ctx context.Context) error {
	if _, err := db.GetState(ctx, providerListCacheClearStateKey); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	for _, table := range []string{"tool_cache", "tool_metadata", "package_availability", "update_metadata"} {
		if _, err := db.bun.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("clearing %s for provider-list migration: %w", table, err)
		}
	}
	return db.SetState(ctx, providerListCacheClearStateKey, "1")
}

func (db *DB) migrateExistingToolMetadata(ctx context.Context) error {
	_, err := db.bun.ExecContext(ctx,
		`INSERT INTO tool_metadata (
		     name, provider, package, description,
		     privilege, privilege_reason, updated_at
		 )
		 SELECT
		     name,
		     provider,
		     package,
		     NULLIF(description, ''),
		     privilege,
		     privilege_reason,
		     COALESCE(privilege_at, last_checked, CURRENT_TIMESTAMP)
		 FROM tool_cache
		 WHERE package != ''
		   AND (
		       (description IS NOT NULL AND description != '')
		       OR (privilege IS NOT NULL AND privilege != '')
		   )
		 ON CONFLICT (name, provider, package) DO UPDATE SET
		     description = CASE
		         WHEN EXCLUDED.description IS NOT NULL AND EXCLUDED.description != '' THEN EXCLUDED.description
		         ELSE tool_metadata.description
		     END,
		     privilege = CASE
		         WHEN EXCLUDED.privilege != '' THEN EXCLUDED.privilege
		         ELSE tool_metadata.privilege
		     END,
		     privilege_reason = CASE
		         WHEN EXCLUDED.privilege != '' THEN EXCLUDED.privilege_reason
		         ELSE tool_metadata.privilege_reason
		     END,
		     updated_at = EXCLUDED.updated_at`)
	if err != nil {
		return fmt.Errorf("migrating existing tool metadata: %w", err)
	}
	return nil
}

func (db *DB) dropLegacyToolCache(ctx context.Context) error {
	var indexes []struct {
		Name string `bun:"name"`
	}
	if err := db.bun.NewRaw(`PRAGMA index_list('tool_cache')`).Scan(ctx, &indexes); err != nil {
		return nil
	}
	for _, idx := range indexes {
		if idx.Name == "idx_tool_cache_name_provider" {
			if _, err := db.bun.ExecContext(ctx, `DROP TABLE IF EXISTS tool_cache`); err != nil {
				return fmt.Errorf("dropping legacy tool_cache: %w", err)
			}
			return nil
		}
	}
	return nil
}

// Upsert inserts or updates a tool entry by (name, provider, package).
// Config-tracked tools always have tracked = TRUE.
func (db *DB) Upsert(ctx context.Context, t *ToolCache) error {
	if t.Package == "" {
		t.Package = t.Name
	}
	t.ID = 0
	_, err := db.bun.NewInsert().
		Model(t).
		On("CONFLICT (name, provider, package) DO UPDATE").
		Set("installed = EXCLUDED.installed").
		Set("installed_with = EXCLUDED.installed_with").
		Set("version = EXCLUDED.version").
		// outdated and latest_version are managed exclusively by UpdateOutdated.
		// Omitting them here prevents RefreshInstalled from racing with
		// RefreshOutdated and wiping the ↑ update flags prematurely.
		Set("last_checked = EXCLUDED.last_checked").
		Set("failure_count = CASE WHEN EXCLUDED.installed THEN 0 ELSE failure_count END").
		Set("failed_at = CASE WHEN EXCLUDED.installed THEN NULL ELSE failed_at END").
		Set("last_error = CASE WHEN EXCLUDED.installed THEN NULL ELSE last_error END").
		Set("tracked = TRUE").
		Set("privilege = CASE WHEN EXCLUDED.privilege != '' THEN EXCLUDED.privilege ELSE privilege END").
		Set("privilege_reason = CASE WHEN EXCLUDED.privilege != '' THEN EXCLUDED.privilege_reason ELSE privilege_reason END").
		Set("privilege_at = CASE WHEN EXCLUDED.privilege != '' THEN EXCLUDED.privilege_at ELSE privilege_at END").
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("upserting tool %s/%s: %w", t.Provider, t.Name, err)
	}
	return nil
}

// UpsertBatch applies many Upsert calls inside a single transaction so SQLite
// batches the writes into one fsync. Empty slices are no-ops. Returns the
// first error encountered; partial state is rolled back. Each entry's Package
// defaults to Name if empty, matching Upsert's behaviour.
func (db *DB) UpsertBatch(ctx context.Context, tools []*ToolCache) error {
	if len(tools) == 0 {
		return nil
	}
	return db.bun.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for _, t := range tools {
			if t == nil {
				continue
			}
			if t.Package == "" {
				t.Package = t.Name
			}
			t.ID = 0
			_, err := tx.NewInsert().
				Model(t).
				On("CONFLICT (name, provider, package) DO UPDATE").
				Set("installed = EXCLUDED.installed").
				Set("installed_with = EXCLUDED.installed_with").
				Set("version = EXCLUDED.version").
				Set("last_checked = EXCLUDED.last_checked").
				Set("failure_count = CASE WHEN EXCLUDED.installed THEN 0 ELSE failure_count END").
				Set("failed_at = CASE WHEN EXCLUDED.installed THEN NULL ELSE failed_at END").
				Set("last_error = CASE WHEN EXCLUDED.installed THEN NULL ELSE last_error END").
				Set("tracked = TRUE").
				Set("privilege = CASE WHEN EXCLUDED.privilege != '' THEN EXCLUDED.privilege ELSE privilege END").
				Set("privilege_reason = CASE WHEN EXCLUDED.privilege != '' THEN EXCLUDED.privilege_reason ELSE privilege_reason END").
				Set("privilege_at = CASE WHEN EXCLUDED.privilege != '' THEN EXCLUDED.privilege_at ELSE privilege_at END").
				Exec(ctx)
			if err != nil {
				return fmt.Errorf("upserting tool %s/%s: %w", t.Provider, t.Name, err)
			}
		}
		return nil
	})
}

// UpsertMetadataBatch caches registry metadata for tools that may not be
// installed or configured yet. It never changes install/config state.
func (db *DB) UpsertMetadataBatch(ctx context.Context, updates []MetadataUpdate) error {
	if len(updates) == 0 {
		return nil
	}
	return db.bun.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		now := time.Now()
		for _, u := range updates {
			if err := requirePackage(u.Name, u.Provider, u.Package); err != nil {
				return err
			}
			version := sql.NullString{String: u.Version, Valid: u.Version != ""}
			description := sql.NullString{String: u.Description, Valid: u.Description != ""}
			sourceURL := sql.NullString{String: u.SourceURL, Valid: u.SourceURL != ""}
			privilegeReason := sql.NullString{String: u.PrivilegeReason, Valid: u.PrivilegeReason != ""}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO tool_metadata (
				     name, provider, package, version, description,
				     source_type, source_owner, source_repo, source_url,
				     privilege, privilege_reason, updated_at
				 )
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				 ON CONFLICT (name, provider, package) DO UPDATE SET
				     description = CASE
				         WHEN EXCLUDED.description IS NOT NULL AND EXCLUDED.description != '' THEN EXCLUDED.description
				         ELSE tool_metadata.description
				     END,
				     version = CASE WHEN EXCLUDED.version IS NOT NULL THEN EXCLUDED.version ELSE tool_metadata.version END,
				     source_type = CASE
				         WHEN EXCLUDED.source_type != '' THEN EXCLUDED.source_type
				         ELSE tool_metadata.source_type
				     END,
				     source_owner = CASE
				         WHEN EXCLUDED.source_type != '' THEN EXCLUDED.source_owner
				         ELSE tool_metadata.source_owner
				     END,
				     source_repo = CASE
				         WHEN EXCLUDED.source_type != '' THEN EXCLUDED.source_repo
				         ELSE tool_metadata.source_repo
				     END,
				     source_url = CASE
				         WHEN EXCLUDED.source_url IS NOT NULL AND EXCLUDED.source_url != '' THEN EXCLUDED.source_url
				         ELSE tool_metadata.source_url
				     END,
				     privilege = CASE
				         WHEN EXCLUDED.privilege != '' THEN EXCLUDED.privilege
				         ELSE tool_metadata.privilege
				     END,
				     privilege_reason = CASE
				         WHEN EXCLUDED.privilege != '' THEN EXCLUDED.privilege_reason
				         ELSE tool_metadata.privilege_reason
				     END,
				     updated_at = EXCLUDED.updated_at`,
				u.Name, u.Provider, u.Package, version, description,
				u.SourceType, u.SourceOwner, u.SourceRepo, sourceURL,
				u.Privilege, privilegeReason, now); err != nil {
				return fmt.Errorf("upserting metadata for %s/%s: %w", u.Provider, u.Name, err)
			}
		}
		return nil
	})
}

func (db *DB) UpsertUpdateMetadata(ctx context.Context, metadata UpdateMetadata) error {
	if strings.TrimSpace(metadata.Provider) == "" {
		return fmt.Errorf("missing provider for update metadata")
	}
	if strings.TrimSpace(metadata.Package) == "" {
		return fmt.Errorf("missing package for update metadata")
	}
	if strings.TrimSpace(metadata.Version) == "" {
		return fmt.Errorf("missing version for update metadata")
	}
	if metadata.AvailableAt.IsZero() {
		return fmt.Errorf("missing available_at for update metadata")
	}
	if metadata.CheckedAt.IsZero() {
		metadata.CheckedAt = time.Now()
	}
	if _, err := db.bun.NewInsert().
		Model(&metadata).
		On("CONFLICT (provider, package, version) DO UPDATE").
		Set("available_at = EXCLUDED.available_at").
		Set("date_source = EXCLUDED.date_source").
		Set("checked_at = EXCLUDED.checked_at").
		Exec(ctx); err != nil {
		return fmt.Errorf("upserting update metadata for %s/%s@%s: %w", metadata.Provider, metadata.Package, metadata.Version, err)
	}
	return nil
}

func (db *DB) UpsertUpdateMetadataBatch(ctx context.Context, updates []UpdateMetadata) error {
	if len(updates) == 0 {
		return nil
	}
	return db.bun.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for _, update := range updates {
			if strings.TrimSpace(update.Provider) == "" {
				return fmt.Errorf("missing provider for update metadata")
			}
			if strings.TrimSpace(update.Package) == "" {
				return fmt.Errorf("missing package for update metadata")
			}
			if strings.TrimSpace(update.Version) == "" {
				return fmt.Errorf("missing version for update metadata")
			}
			if update.AvailableAt.IsZero() {
				return fmt.Errorf("missing available_at for update metadata")
			}
			if update.CheckedAt.IsZero() {
				update.CheckedAt = time.Now()
			}
			if _, err := tx.NewInsert().
				Model(&update).
				On("CONFLICT (provider, package, version) DO UPDATE").
				Set("available_at = EXCLUDED.available_at").
				Set("date_source = EXCLUDED.date_source").
				Set("checked_at = EXCLUDED.checked_at").
				Exec(ctx); err != nil {
				return fmt.Errorf("upserting update metadata for %s/%s@%s: %w", update.Provider, update.Package, update.Version, err)
			}
		}
		return nil
	})
}

func (db *DB) GetUpdateMetadata(ctx context.Context, providerName, pkg, version string) (*UpdateMetadata, error) {
	var metadata UpdateMetadata
	if err := db.bun.NewSelect().
		Model(&metadata).
		Where("provider = ? AND package = ? AND version = ?", providerName, pkg, version).
		Scan(ctx); err != nil {
		return nil, err
	}
	return &metadata, nil
}

// UpdateOutdated sets the outdated flag and latest version for a tool.
func (db *DB) UpdateOutdated(ctx context.Context, name, provider, pkg string, outdated bool, latestVersion string) error {
	if err := requirePackage(name, provider, pkg); err != nil {
		return err
	}
	lv := sql.NullString{String: latestVersion, Valid: latestVersion != ""}
	_, err := db.bun.NewUpdate().
		Model((*ToolCache)(nil)).
		Set("outdated = ?", outdated).
		Set("latest_version = ?", lv).
		Where("name = ? AND provider = ? AND package = ?", name, provider, pkg).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("updating outdated for %s/%s: %w", provider, name, err)
	}
	return nil
}

// UpdateDescription caches a one-line description for a tool without changing
// install/config state.
func (db *DB) UpdateDescription(ctx context.Context, name, prov, pkg, description string) error {
	if err := requirePackage(name, prov, pkg); err != nil {
		return err
	}
	return db.UpsertMetadataBatch(ctx, []MetadataUpdate{{
		Name:        name,
		Provider:    prov,
		Package:     pkg,
		Description: description,
	}})
}

// DescriptionUpdate is one row of UpdateDescriptionBatch input.
type DescriptionUpdate struct {
	Name        string
	Provider    string
	Package     string
	Description string
}

// UpdateDescriptionBatch caches many tool descriptions without changing
// install/config state. Empty slices are no-ops.
func (db *DB) UpdateDescriptionBatch(ctx context.Context, updates []DescriptionUpdate) error {
	if len(updates) == 0 {
		return nil
	}
	metadata := make([]MetadataUpdate, 0, len(updates))
	for _, u := range updates {
		metadata = append(metadata, MetadataUpdate{
			Name:        u.Name,
			Provider:    u.Provider,
			Package:     u.Package,
			Description: u.Description,
		})
	}
	return db.UpsertMetadataBatch(ctx, metadata)
}

// Get retrieves a single tool entry by name, provider, and package.
// Returns sql.ErrNoRows if not found.
func (db *DB) Get(ctx context.Context, name, provider, pkg string) (*ToolCache, error) {
	if err := requirePackage(name, provider, pkg); err != nil {
		return nil, err
	}
	t := new(ToolCache)
	err := db.bun.NewSelect().
		Model(t).
		Where("name = ? AND provider = ? AND package = ?", name, provider, pkg).
		Limit(1).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting tool %s/%s: %w", provider, name, err)
	}
	if err := db.hydrateMetadata(ctx, []*ToolCache{t}); err != nil {
		return nil, err
	}
	return t, nil
}

// GetMetadata retrieves central registry metadata by name, provider, and package.
// Returns sql.ErrNoRows if not found.
func (db *DB) GetMetadata(ctx context.Context, name, provider, pkg string) (*ToolMetadata, error) {
	if err := requirePackage(name, provider, pkg); err != nil {
		return nil, err
	}
	m := new(ToolMetadata)
	err := db.bun.NewSelect().
		Model(m).
		Where("name = ? AND provider = ? AND package = ?", name, provider, pkg).
		Limit(1).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting metadata %s/%s: %w", provider, name, err)
	}
	return m, nil
}

// ListMetadata returns all central registry metadata rows ordered by provider
// then name.
func (db *DB) ListMetadata(ctx context.Context) ([]*ToolMetadata, error) {
	var metadata []*ToolMetadata
	if err := db.bun.NewSelect().
		Model(&metadata).
		OrderExpr("provider, name").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("listing tool metadata: %w", err)
	}
	return metadata, nil
}

// GetState retrieves a machine-local state value by key.
// Returns sql.ErrNoRows if not found.
func (db *DB) GetState(ctx context.Context, key string) (string, error) {
	state := new(LocalState)
	err := db.bun.NewSelect().
		Model(state).
		Where("key = ?", key).
		Limit(1).
		Scan(ctx)
	if err != nil {
		return "", fmt.Errorf("getting local state %q: %w", key, err)
	}
	return state.Value, nil
}

// SetState upserts a machine-local state value.
func (db *DB) SetState(ctx context.Context, key, value string) error {
	_, err := db.bun.ExecContext(ctx,
		`INSERT INTO local_state (key, value, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT (key) DO UPDATE SET
		     value = EXCLUDED.value,
		     updated_at = EXCLUDED.updated_at`,
		key, value, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("setting local state %q: %w", key, err)
	}
	return nil
}

func (db *DB) UpsertPackageAvailability(ctx context.Context, availability PackageAvailability) error {
	if strings.TrimSpace(availability.Name) == "" {
		return fmt.Errorf("missing name for package availability")
	}
	if err := requirePackage(availability.Name, availability.Provider, availability.Package); err != nil {
		return err
	}
	if availability.CheckedAt.IsZero() {
		availability.CheckedAt = time.Now().UTC()
	}
	if availability.Available {
		availability.Reason = ""
	}
	_, err := db.bun.NewInsert().
		Model(&availability).
		On("CONFLICT (name, provider, package) DO UPDATE").
		Set("available = EXCLUDED.available").
		Set("reason = EXCLUDED.reason").
		Set("checked_at = EXCLUDED.checked_at").
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("upserting package availability %s/%s: %w", availability.Provider, availability.Name, err)
	}
	return nil
}

func (db *DB) GetPackageAvailability(ctx context.Context, name, provider, pkg string) (*PackageAvailability, error) {
	if err := requirePackage(name, provider, pkg); err != nil {
		return nil, err
	}
	availability := new(PackageAvailability)
	err := db.bun.NewSelect().
		Model(availability).
		Where("name = ? AND provider = ? AND package = ?", name, provider, pkg).
		Limit(1).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting package availability %s/%s: %w", provider, name, err)
	}
	return availability, nil
}

func (db *DB) ReplaceDotsSnapshot(ctx context.Context, entries []*DotStatusCache, gitStatus string, discoveredCount int, observedAt time.Time) error {
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	return db.bun.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM dot_status_cache`); err != nil {
			return fmt.Errorf("clearing dots snapshot: %w", err)
		}
		for i, entry := range entries {
			if entry == nil {
				continue
			}
			row := *entry
			row.ID = 0
			row.Position = i
			row.ObservedAt = observedAt
			if _, err := tx.NewInsert().Model(&row).Exec(ctx); err != nil {
				return fmt.Errorf("inserting dots snapshot entry %q: %w", row.Name, err)
			}
		}
		meta := &DotSnapshotMeta{
			Key:             dotsSnapshotMetaKey,
			GitStatus:       gitStatus,
			DiscoveredCount: discoveredCount,
			ObservedAt:      observedAt,
		}
		_, err := tx.NewInsert().
			Model(meta).
			On("CONFLICT (key) DO UPDATE").
			Set("git_status = EXCLUDED.git_status").
			Set("discovered_count = EXCLUDED.discovered_count").
			Set("observed_at = EXCLUDED.observed_at").
			Exec(ctx)
		if err != nil {
			return fmt.Errorf("upserting dots snapshot metadata: %w", err)
		}
		return nil
	})
}

func (db *DB) GetDotsSnapshot(ctx context.Context) (*DotsSnapshot, bool, error) {
	meta := new(DotSnapshotMeta)
	if err := db.bun.NewSelect().
		Model(meta).
		Where("key = ?", dotsSnapshotMetaKey).
		Limit(1).
		Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("getting dots snapshot metadata: %w", err)
	}
	var entries []*DotStatusCache
	if err := db.bun.NewSelect().
		Model(&entries).
		OrderExpr("position, name").
		Scan(ctx); err != nil {
		return nil, false, fmt.Errorf("listing dots snapshot entries: %w", err)
	}
	return &DotsSnapshot{
		Entries:         entries,
		GitStatus:       meta.GitStatus,
		DiscoveredCount: meta.DiscoveredCount,
		ObservedAt:      meta.ObservedAt,
	}, true, nil
}

func (db *DB) hydrateMetadata(ctx context.Context, tools []*ToolCache) error {
	if len(tools) == 0 {
		return nil
	}
	providerSet := make(map[string]struct{})
	for _, t := range tools {
		if t == nil || t.Provider == "" {
			continue
		}
		providerSet[t.Provider] = struct{}{}
	}
	if len(providerSet) == 0 {
		return nil
	}
	providers := make([]string, 0, len(providerSet))
	for p := range providerSet {
		providers = append(providers, p)
	}
	var metadata []*ToolMetadata
	if err := db.bun.NewSelect().
		Model(&metadata).
		Where("provider IN (?)", bun.List(providers)).
		Scan(ctx); err != nil {
		return fmt.Errorf("listing tool metadata: %w", err)
	}
	byKey := make(map[string]*ToolMetadata, len(metadata))
	for _, m := range metadata {
		if m == nil {
			continue
		}
		byKey[metadataKey(m.Name, m.Provider, m.Package)] = m
	}
	for _, t := range tools {
		if t == nil {
			continue
		}
		pkg := t.Package
		if pkg == "" {
			pkg = t.Name
		}
		if m := byKey[metadataKey(t.Name, t.Provider, pkg)]; m != nil {
			applyToolMetadata(t, m)
		}
	}
	return nil
}

func applyToolMetadata(t *ToolCache, m *ToolMetadata) {
	if m.Description.Valid && strings.TrimSpace(m.Description.String) != "" {
		t.Description = m.Description
	}
	if t.Privilege == "" && m.Privilege != "" {
		t.Privilege = m.Privilege
		t.PrivilegeReason = m.PrivilegeReason
	}
}

// List returns all tool entries ordered by provider then name.
func (db *DB) List(ctx context.Context) ([]*ToolCache, error) {
	var tools []*ToolCache
	err := db.bun.NewSelect().
		Model(&tools).
		OrderExpr("provider, name").
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing tools: %w", err)
	}
	if err := db.hydrateMetadata(ctx, tools); err != nil {
		return nil, err
	}
	return tools, nil
}

// ListByProvider returns tool entries for a specific provider.
func (db *DB) ListByProvider(ctx context.Context, provider string) ([]*ToolCache, error) {
	var tools []*ToolCache
	err := db.bun.NewSelect().
		Model(&tools).
		Where("provider = ?", provider).
		OrderExpr("name").
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing tools for provider %s: %w", provider, err)
	}
	if err := db.hydrateMetadata(ctx, tools); err != nil {
		return nil, err
	}
	return tools, nil
}

// Delete removes a tool entry.
func (db *DB) Delete(ctx context.Context, name, provider, pkg string) error {
	if err := requirePackage(name, provider, pkg); err != nil {
		return err
	}
	_, err := db.bun.NewDelete().
		Model((*ToolCache)(nil)).
		Where("name = ? AND provider = ? AND package = ?", name, provider, pkg).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("deleting tool %s/%s: %w", provider, name, err)
	}
	return nil
}

// MarkInstalled marks a tool as installed with a known version and clears any
// outdated flag, since the tool is now at the latest version.
func (db *DB) MarkInstalled(ctx context.Context, name, provider, pkg, version string) error {
	if err := requirePackage(name, provider, pkg); err != nil {
		return err
	}
	_, err := db.bun.NewUpdate().
		Model((*ToolCache)(nil)).
		Set("installed = TRUE").
		Set("version = ?", version).
		Set("outdated = FALSE").
		Set("latest_version = NULL").
		Set("last_checked = ?", time.Now()).
		Where("name = ? AND provider = ? AND package = ?", name, provider, pkg).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("marking tool %s/%s installed: %w", provider, name, err)
	}
	return nil
}

// MarkFailed records a failed install attempt, incrementing the failure count
// and storing the error message. Creates a stub row if none exists yet.
func (db *DB) MarkFailed(ctx context.Context, name, prov, pkg, errMsg string) error {
	if err := requirePackage(name, prov, pkg); err != nil {
		return err
	}
	_, err := db.bun.ExecContext(ctx,
		`INSERT INTO tool_cache (name, provider, package, installed, last_checked, failure_count, failed_at, last_error)
		 VALUES (?, ?, ?, FALSE, CURRENT_TIMESTAMP, 1, CURRENT_TIMESTAMP, ?)
		 ON CONFLICT (name, provider, package) DO UPDATE SET
		     failure_count = tool_cache.failure_count + 1,
		     failed_at     = CURRENT_TIMESTAMP,
		     last_error    = EXCLUDED.last_error,
		     installed     = FALSE,
		     last_checked  = CURRENT_TIMESTAMP`,
		name, prov, pkg, errMsg)
	if err != nil {
		return fmt.Errorf("marking failed for %s/%s: %w", prov, name, err)
	}
	return nil
}

// MarkPrivilegeRequired records that an attempted operation needs privileged
// package-manager access. Creates a stub row if none exists yet.
func (db *DB) MarkPrivilegeRequired(ctx context.Context, name, prov, pkg, requirement, reason string) error {
	if err := requirePackage(name, prov, pkg); err != nil {
		return err
	}
	_, err := db.bun.ExecContext(ctx,
		`INSERT INTO tool_cache (name, provider, package, installed, last_checked, privilege, privilege_reason, privilege_at)
		 VALUES (?, ?, ?, FALSE, CURRENT_TIMESTAMP, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT (name, provider, package) DO UPDATE SET
		     privilege        = EXCLUDED.privilege,
		     privilege_reason = EXCLUDED.privilege_reason,
		     privilege_at     = CURRENT_TIMESTAMP,
		     last_checked     = CURRENT_TIMESTAMP`,
		name, prov, pkg, requirement, sql.NullString{String: reason, Valid: reason != ""})
	if err != nil {
		return fmt.Errorf("marking privilege for %s/%s: %w", prov, name, err)
	}
	return nil
}

// ClearFailure removes the retry/failure marker for a tool without changing
// installed state. Used when an operation was cancelled rather than failed.
func (db *DB) ClearFailure(ctx context.Context, name, prov, pkg string) error {
	if err := requirePackage(name, prov, pkg); err != nil {
		return err
	}
	_, err := db.bun.NewUpdate().
		Model((*ToolCache)(nil)).
		Set("failed_at = NULL").
		Set("failure_count = 0").
		Set("last_error = NULL").
		Where("name = ? AND provider = ? AND package = ?", name, prov, pkg).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("clearing failure for %s/%s: %w", prov, name, err)
	}
	return nil
}

// ListFailed returns all tool entries with a non-null failed_at, ordered by
// provider then name.
func (db *DB) ListFailed(ctx context.Context) ([]*ToolCache, error) {
	var tools []*ToolCache
	err := db.bun.NewSelect().
		Model(&tools).
		Where("failed_at IS NOT NULL").
		OrderExpr("provider, name").
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing failed tools: %w", err)
	}
	if err := db.hydrateMetadata(ctx, tools); err != nil {
		return nil, err
	}
	return tools, nil
}

// MarkUninstalled clears the installed flag and version.
func (db *DB) MarkUninstalled(ctx context.Context, name, provider, pkg string) error {
	if err := requirePackage(name, provider, pkg); err != nil {
		return err
	}
	_, err := db.bun.NewUpdate().
		Model((*ToolCache)(nil)).
		Set("installed = FALSE").
		Set("version = NULL").
		Set("outdated = FALSE").
		Set("latest_version = NULL").
		Set("last_checked = ?", time.Now()).
		Where("name = ? AND provider = ? AND package = ?", name, provider, pkg).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("marking tool %s/%s uninstalled: %w", provider, name, err)
	}
	return nil
}

// UpsertDiscovered inserts or updates a locally-installed tool that is not
// declared in config (tracked=false). Never overwrites a config-tracked row.
// installedWith is the concrete manager that owns the tool (e.g. "pnpm", "npm", "bun").
func (db *DB) UpsertDiscovered(ctx context.Context, name, provider, installedWith, version string) error {
	now := time.Now()
	_, err := db.bun.ExecContext(ctx,
		`INSERT INTO tool_cache (name, provider, package, installed, installed_with, version, last_checked, tracked)
		 VALUES (?, ?, ?, 1, ?, ?, ?, 0)
		 ON CONFLICT (name, provider, package) DO UPDATE SET
		     installed = 1,
		     installed_with = EXCLUDED.installed_with,
		     version = EXCLUDED.version,
		     last_checked = EXCLUDED.last_checked
		 WHERE tool_cache.tracked = 0`,
		name, provider, name, installedWith, sql.NullString{String: version, Valid: version != ""}, now)
	if err != nil {
		return fmt.Errorf("upserting discovered tool %s/%s: %w", provider, name, err)
	}
	return nil
}

// DiscoveredUpsert is one row of UpsertDiscoveredBatch input.
type DiscoveredUpsert struct {
	Name          string
	Provider      string
	InstalledWith string
	Version       string
}

// UpsertDiscoveredBatch applies many UpsertDiscovered calls inside a single
// transaction. Empty slices are no-ops.
func (db *DB) UpsertDiscoveredBatch(ctx context.Context, entries []DiscoveredUpsert) error {
	if len(entries) == 0 {
		return nil
	}
	return db.bun.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		now := time.Now()
		for _, e := range entries {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO tool_cache (name, provider, package, installed, installed_with, version, last_checked, tracked)
				 VALUES (?, ?, ?, 1, ?, ?, ?, 0)
				 ON CONFLICT (name, provider, package) DO UPDATE SET
				     installed = 1,
				     installed_with = EXCLUDED.installed_with,
				     version = EXCLUDED.version,
				     last_checked = EXCLUDED.last_checked
				 WHERE tool_cache.tracked = 0`,
				e.Name, e.Provider, e.Name, e.InstalledWith, sql.NullString{String: e.Version, Valid: e.Version != ""}, now); err != nil {
				return fmt.Errorf("upserting discovered tool %s/%s: %w", e.Provider, e.Name, err)
			}
		}
		return nil
	})
}

// MarkTracked promotes a discovered (tracked=false) row to config-tracked.
// Best-effort: if the row doesn't exist yet this is a no-op.
// Called after claiming an orphan so it stops appearing as a discovered tool.
func (db *DB) MarkTracked(ctx context.Context, name, provider, pkg string) error {
	return db.MarkTrackedBatch(ctx, []TrackedTool{{Name: name, Provider: provider, Package: pkg}})
}

// TrackedTool is one tool-cache row to promote from discovered to config-tracked.
type TrackedTool struct {
	Name     string
	Provider string
	Package  string
}

// MarkTrackedBatch promotes discovered rows to config-tracked in one
// transaction. Best-effort: rows that do not exist are ignored.
func (db *DB) MarkTrackedBatch(ctx context.Context, tools []TrackedTool) error {
	if len(tools) == 0 {
		return nil
	}
	for _, t := range tools {
		if err := requirePackage(t.Name, t.Provider, t.Package); err != nil {
			return err
		}
	}
	return db.bun.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for _, t := range tools {
			if _, err := tx.ExecContext(ctx,
				`UPDATE tool_cache
				 SET tracked = TRUE
				 WHERE name = ? AND provider = ? AND package = ?`,
				t.Name, t.Provider, t.Package); err != nil {
				return fmt.Errorf("marking %s/%s tracked: %w", t.Provider, t.Name, err)
			}
		}
		return nil
	})
}

// ListDiscovered returns all tool entries that are installed locally but not
// declared in config (tracked=false), ordered by provider then name.
func (db *DB) ListDiscovered(ctx context.Context) ([]*ToolCache, error) {
	var tools []*ToolCache
	err := db.bun.NewSelect().
		Model(&tools).
		Where("tracked = 0").
		OrderExpr("provider, name").
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing discovered tools: %w", err)
	}
	if err := db.hydrateMetadata(ctx, tools); err != nil {
		return nil, err
	}
	return tools, nil
}

// ReconcileTracked marks the current desired resolved keys as tracked and any
// previously tracked keys outside that set as untracked.
func (db *DB) ReconcileTracked(ctx context.Context, desired []*ToolCache) error {
	// Wrap the reset + per-tool re-mark in a single transaction so the
	// per-tool UPDATE loop completes in one fsync rather than N.
	return db.bun.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewUpdate().
			Model((*ToolCache)(nil)).
			Set("tracked = FALSE").
			Where("tracked = TRUE").
			Exec(ctx); err != nil {
			return fmt.Errorf("marking stale tracked tools untracked: %w", err)
		}
		for _, t := range desired {
			if t == nil {
				continue
			}
			if err := requirePackage(t.Name, t.Provider, t.Package); err != nil {
				return err
			}
			if _, err := tx.NewUpdate().
				Model((*ToolCache)(nil)).
				Set("tracked = TRUE").
				Where("name = ? AND provider = ? AND package = ?", t.Name, t.Provider, t.Package).
				Exec(ctx); err != nil {
				return fmt.Errorf("marking desired tool %s/%s tracked: %w", t.Provider, t.Name, err)
			}
		}
		return nil
	})
}

// OutdatedUpdate is one row of the UpdateOutdatedBatch input.
type OutdatedUpdate struct {
	Name          string
	Provider      string
	Package       string
	Outdated      bool
	LatestVersion string
}

// UpdateOutdatedBatch applies many UpdateOutdated calls inside a single
// transaction so SQLite batches the writes into one fsync. Empty slices are
// no-ops. Returns the first error encountered; partial state is rolled back.
func (db *DB) UpdateOutdatedBatch(ctx context.Context, updates []OutdatedUpdate) error {
	if len(updates) == 0 {
		return nil
	}
	return db.bun.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for _, u := range updates {
			if err := requirePackage(u.Name, u.Provider, u.Package); err != nil {
				return err
			}
			lv := sql.NullString{String: u.LatestVersion, Valid: u.LatestVersion != ""}
			if _, err := tx.NewUpdate().
				Model((*ToolCache)(nil)).
				Set("outdated = ?", u.Outdated).
				Set("latest_version = ?", lv).
				Where("name = ? AND provider = ? AND package = ?", u.Name, u.Provider, u.Package).
				Exec(ctx); err != nil {
				return fmt.Errorf("updating outdated for %s/%s: %w", u.Provider, u.Name, err)
			}
		}
		return nil
	})
}

// PruneDiscovered removes tracked=false entries that have not been seen since
// the given cutoff time. Call after a discovery scan to evict stale entries.
func (db *DB) PruneDiscovered(ctx context.Context, cutoff time.Time) error {
	_, err := db.bun.ExecContext(ctx,
		`DELETE FROM tool_cache WHERE tracked = 0 AND last_checked < ?`,
		cutoff)
	if err != nil {
		return fmt.Errorf("pruning discovered tools: %w", err)
	}
	return nil
}
