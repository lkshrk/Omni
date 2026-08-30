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
	"modernc.org/sqlite" // registers the "sqlite" driver
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/lkshrk/omni/internal/testguard"
)

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
	OutdatedUnknown    bool              `bun:"outdated_unknown,notnull,default:false"`
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

type ToolCacheKey struct {
	Name, Provider, Package string
}

type TrackedAliasMigration struct {
	From, To ToolCacheKey
}

// ToolMetadata — Cached independently from install/config state.
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
	ArtifactKind    string         `bun:"artifact_kind,notnull,default:''"`
	SelfUpdates     bool           `bun:"self_updates,notnull,default:false"`
	UpdatedAt       time.Time      `bun:"updated_at,notnull"`
}

// UpdateMetadata — Keyed by concrete provider/manager: availability timestamps only mean something for the PM that reported them.
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

// LocalState — Lives in the cache DB, not settings.json, because these rows describe this checkout/host.
type LocalState struct {
	bun.BaseModel `bun:"table:local_state,alias:ls"`

	Key       string    `bun:"key,pk,notnull"`
	Value     string    `bun:"value,notnull"`
	UpdatedAt time.Time `bun:"updated_at,notnull"`
}

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

// CommandTrace — Intentionally compact: stdout, stderr, and error all arrive already truncated and redacted.
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
	Stdout     string        `bun:"stdout,type:TEXT,notnull,default:''"`
	Stderr     string        `bun:"stderr,type:TEXT,notnull,default:''"`
}

// TrustedTap — Lets tap sync skip the `brew trust` call on later runs.
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
const toolMetadataMigratedStateKey = "migration.tool_metadata_migrated"
const commandTraceRetentionLimit = 5000

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
	ArtifactKind    string // provider-specific artifact kind, e.g. "formula" or "cask" for brew
	SelfUpdates     bool   // cask the manager cannot upgrade (manual installer; app self-updates)
}

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

// Matches the result code rather than the driver's message text, which is not a stable API.
func isBusyErr(err error) bool {
	var serr *sqlite.Error
	if !errors.As(err, &serr) {
		return false
	}
	switch serr.Code() {
	case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_BUSY_RECOVERY, sqlite3.SQLITE_BUSY_SNAPSHOT, sqlite3.SQLITE_BUSY_TIMEOUT:
		return true
	}
	return false
}

func execWithBusyRetry(ctx context.Context, db *sql.DB, statement string, budget time.Duration) error {
	deadline := time.Now().Add(budget)
	for {
		_, err := db.ExecContext(ctx, statement)
		if err == nil || !isBusyErr(err) || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func Open(path string) (*DB, error) {
	return OpenContext(context.Background(), path)
}

// OpenContext lets cancellation stop the WAL retry loop before a competing writer exhausts the busy budget.
func OpenContext(ctx context.Context, path string) (*DB, error) {
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

	// Must precede the WAL switch: that mode change needs brief exclusive access.
	if _, err := sqlDB.ExecContext(ctx, "PRAGMA busy_timeout=5000"); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("setting busy_timeout: %w", err)
	}
	// WAL keeps readers from blocking each other; it bypasses the busy handler, hence the explicit retry.
	if err := execWithBusyRetry(ctx, sqlDB, "PRAGMA journal_mode=WAL", 5*time.Second); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("enabling WAL mode: %w", err)
	}

	bunDB := bun.NewDB(sqlDB, sqlitedialect.New())
	return &DB{bun: bunDB}, nil
}

func (db *DB) Close() error {
	return db.bun.Close()
}

func (db *DB) Bun() *bun.DB {
	return db.bun
}

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

// RecordCommandTrace — Also prunes beyond the retention limit. Callers should treat errors as non-fatal.
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

// Migrate — Idempotent: re-runs add missing columns and swallow "duplicate column" errors.
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
	// PRAGMA table_info rather than matching driver error strings, which are not a stable API.
	addCol := func(table, col, def string) error {
		var cols []struct {
			CID       int            `bun:"cid"`
			Name      string         `bun:"name"`
			Type      string         `bun:"type"`
			NotNull   int            `bun:"column:notnull"`
			DfltValue sql.NullString `bun:"dflt_value"`
			PK        int            `bun:"pk"`
		}
		if err := db.bun.NewRaw("PRAGMA table_info("+table+")").Scan(ctx, &cols); err != nil {
			return fmt.Errorf("table_info %s: %w", table, err)
		}
		for _, c := range cols {
			if c.Name == col {
				return nil
			}
		}
		if _, err := db.bun.ExecContext(ctx, "ALTER TABLE "+table+" ADD COLUMN "+col+" "+def); err != nil {
			return fmt.Errorf("adding column %s.%s: %w", table, col, err)
		}
		return nil
	}
	for _, c := range []struct{ col, def string }{
		{"description", "TEXT"},
		{"outdated", "BOOLEAN NOT NULL DEFAULT FALSE"},
		{"outdated_unknown", "BOOLEAN NOT NULL DEFAULT FALSE"},
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
		if err := addCol("tool_cache", c.col, c.def); err != nil {
			return err
		}
	}
	if err := addCol("command_traces", "stdout", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	for _, c := range []struct{ col, def string }{
		{"source_type", "TEXT NOT NULL DEFAULT ''"},
		{"source_owner", "TEXT NOT NULL DEFAULT ''"},
		{"source_repo", "TEXT NOT NULL DEFAULT ''"},
		{"source_url", "TEXT"},
		{"artifact_kind", "TEXT NOT NULL DEFAULT ''"},
		{"self_updates", "BOOLEAN NOT NULL DEFAULT FALSE"},
	} {
		if err := addCol("tool_metadata", c.col, c.def); err != nil {
			return err
		}
	}
	// Without these, the filtered list queries full-scan on every TUI background refresh.
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
	// One transaction so a mid-wipe crash leaves the DB either fully wiped or untouched.
	return db.bun.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for _, table := range []string{"tool_cache", "tool_metadata", "package_availability", "update_metadata"} {
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
				return fmt.Errorf("clearing %s for provider-list migration: %w", table, err)
			}
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO local_state (key, value, updated_at)
			 VALUES (?, ?, ?)
			 ON CONFLICT (key) DO UPDATE SET
			     value = EXCLUDED.value,
			     updated_at = EXCLUDED.updated_at`,
			providerListCacheClearStateKey, "1", time.Now().UTC())
		if err != nil {
			return fmt.Errorf("setting migration sentinel: %w", err)
		}
		return nil
	})
}

func (db *DB) migrateExistingToolMetadata(ctx context.Context) error {
	// Sentinel keeps the full tool_cache scan to once, not every startup after promotion.
	if _, err := db.GetState(ctx, toolMetadataMigratedStateKey); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
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
	return db.SetState(ctx, toolMetadataMigratedStateKey, "1")
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

// Upsert — Config-tracked tools always have tracked = TRUE.
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
		// Omitting outdated/latest_version stops RefreshInstalled racing RefreshOutdated and wiping update flags.
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

// UpsertBatch — One transaction so SQLite batches the writes into a single fsync; rolls back on first error.
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

// UpsertMetadataBatch — Never changes install/config state, so uninstalled and unconfigured tools are fine.
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
				     privilege, privilege_reason, artifact_kind, self_updates, updated_at
				 )
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
				     artifact_kind = CASE
				         WHEN EXCLUDED.artifact_kind != '' THEN EXCLUDED.artifact_kind
				         ELSE tool_metadata.artifact_kind
				     END,
				     self_updates = CASE
				         WHEN EXCLUDED.artifact_kind != '' THEN EXCLUDED.self_updates
				         ELSE tool_metadata.self_updates
				     END,
				     updated_at = EXCLUDED.updated_at`,
				u.Name, u.Provider, u.Package, version, description,
				u.SourceType, u.SourceOwner, u.SourceRepo, sourceURL,
				u.Privilege, privilegeReason, u.ArtifactKind, u.SelfUpdates, now); err != nil {
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

func (db *DB) UpdateOutdated(ctx context.Context, name, provider, pkg string, outdated bool, latestVersion string) error {
	if err := requirePackage(name, provider, pkg); err != nil {
		return err
	}
	lv := sql.NullString{String: latestVersion, Valid: latestVersion != ""}
	_, err := db.bun.NewUpdate().
		Model((*ToolCache)(nil)).
		Set("outdated = ?", outdated).
		Set("outdated_unknown = FALSE").
		Set("latest_version = ?", lv).
		Where("name = ? AND provider = ? AND package = ?", name, provider, pkg).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("updating outdated for %s/%s: %w", provider, name, err)
	}
	return nil
}

// UpdateDescription — Never changes install/config state.
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

type DescriptionUpdate struct {
	Name        string
	Provider    string
	Package     string
	Description string
}

// UpdateDescriptionBatch — Never changes install/config state.
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
	if m.ArtifactKind != "" && t.Provider == "brew" {
		if t.Options == nil {
			t.Options = make(map[string]string)
		}
		t.Options["brew_kind"] = m.ArtifactKind
	}
	if m.SelfUpdates {
		if t.Options == nil {
			t.Options = make(map[string]string)
		}
		t.Options["self_updates"] = "true"
	}
}

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

// MarkInstalled — Also clears the outdated flag: the tool is now at the latest version.
func (db *DB) MarkInstalled(ctx context.Context, name, provider, pkg, version string) error {
	if err := requirePackage(name, provider, pkg); err != nil {
		return err
	}
	_, err := db.bun.NewUpdate().
		Model((*ToolCache)(nil)).
		Set("installed = TRUE").
		Set("version = ?", version).
		Set("outdated = FALSE").
		Set("outdated_unknown = FALSE").
		Set("latest_version = NULL").
		Set("last_checked = ?", time.Now()).
		Where("name = ? AND provider = ? AND package = ?", name, provider, pkg).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("marking tool %s/%s installed: %w", provider, name, err)
	}
	return nil
}

// MarkFailed — Creates a stub row if none exists yet.
func (db *DB) MarkFailed(ctx context.Context, name, prov, pkg, errMsg string) error {
	return db.markFailed(ctx, name, prov, pkg, errMsg, false)
}

// MarkUpgradeFailed — Unlike MarkFailed, leaves an existing installation marked present.
func (db *DB) MarkUpgradeFailed(ctx context.Context, name, prov, pkg, errMsg string) error {
	return db.markFailed(ctx, name, prov, pkg, errMsg, true)
}

func (db *DB) markFailed(ctx context.Context, name, prov, pkg, errMsg string, preserveInstalled bool) error {
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
		     installed     = CASE WHEN ? THEN tool_cache.installed ELSE FALSE END,
		     last_checked  = CURRENT_TIMESTAMP`,
		name, prov, pkg, errMsg, preserveInstalled)
	if err != nil {
		return fmt.Errorf("marking failed for %s/%s: %w", prov, name, err)
	}
	return nil
}

// MarkPrivilegeRequired — Creates a stub row if none exists yet.
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

// ClearFailure — For operations that were cancelled rather than failed; installed state is untouched.
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

func (db *DB) MarkUninstalled(ctx context.Context, name, provider, pkg string) error {
	if err := requirePackage(name, provider, pkg); err != nil {
		return err
	}
	_, err := db.bun.NewUpdate().
		Model((*ToolCache)(nil)).
		Set("installed = FALSE").
		Set("version = NULL").
		Set("outdated = FALSE").
		Set("outdated_unknown = FALSE").
		Set("latest_version = NULL").
		Set("last_checked = ?", time.Now()).
		Where("name = ? AND provider = ? AND package = ?", name, provider, pkg).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("marking tool %s/%s uninstalled: %w", provider, name, err)
	}
	return nil
}

// UpsertDiscovered — Records a locally-installed, unconfigured tool; never overwrites a config-tracked row.
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

type DiscoveredUpsert struct {
	Name          string
	Provider      string
	InstalledWith string
	Version       string
}

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

// MarkTracked — Best-effort promotion of a discovered row to config-tracked; a missing row is a no-op.
func (db *DB) MarkTracked(ctx context.Context, name, provider, pkg string) error {
	return db.MarkTrackedBatch(ctx, []TrackedTool{{Name: name, Provider: provider, Package: pkg}})
}

type TrackedTool struct {
	Name     string
	Provider string
	Package  string
}

// MarkTrackedBatch — Best-effort: rows that do not exist are ignored.
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

// ReconcileTracked marks desired keys tracked, applies explicit alias migrations, deletes stale missing rows,
// and demotes other installed rows to honest discoveries. Provider compatibility policy belongs to the app.
func (db *DB) ReconcileTracked(ctx context.Context, desired []*ToolCache, migrations ...TrackedAliasMigration) error {
	desiredKeys := make(map[ToolCacheKey]struct{}, len(desired))
	for _, t := range desired {
		if t == nil {
			continue
		}
		if err := requirePackage(t.Name, t.Provider, t.Package); err != nil {
			return err
		}
		desiredKeys[ToolCacheKey{Name: t.Name, Provider: t.Provider, Package: t.Package}] = struct{}{}
	}
	migrationBySource := make(map[ToolCacheKey]ToolCacheKey, len(migrations))
	for _, migration := range migrations {
		if err := requirePackage(migration.From.Name, migration.From.Provider, migration.From.Package); err != nil {
			return err
		}
		if err := requirePackage(migration.To.Name, migration.To.Provider, migration.To.Package); err != nil {
			return err
		}
		if _, ok := desiredKeys[migration.To]; !ok {
			return fmt.Errorf("tracked alias migration target %s/%s is not desired", migration.To.Provider, migration.To.Name)
		}
		migrationBySource[migration.From] = migration.To
	}

	// One transaction so the per-tool UPDATE loop costs one fsync rather than N.
	return db.bun.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var tracked []*ToolCache
		if err := tx.NewSelect().Model(&tracked).Where("tracked = TRUE").Scan(ctx); err != nil {
			return fmt.Errorf("listing tracked tools for reconciliation: %w", err)
		}
		for _, t := range tracked {
			key := ToolCacheKey{Name: t.Name, Provider: t.Provider, Package: t.Package}
			if _, keep := desiredKeys[key]; keep {
				continue
			}
			if target, migrate := migrationBySource[key]; migrate {
				targetExists, err := tx.NewSelect().Model((*ToolCache)(nil)).
					Where("name = ? AND provider = ? AND package = ?", target.Name, target.Provider, target.Package).
					Exists(ctx)
				if err != nil {
					return fmt.Errorf("checking tracked alias target %s/%s: %w", target.Provider, target.Name, err)
				}
				if !targetExists {
					migrated := *t
					migrated.ID = 0
					migrated.Name, migrated.Provider, migrated.Package = target.Name, target.Provider, target.Package
					migrated.Tracked = true
					if _, err := tx.NewInsert().Model(&migrated).Exec(ctx); err != nil {
						return fmt.Errorf("migrating tracked tool %s/%s to %s: %w", t.Provider, t.Name, target.Provider, err)
					}
				}
				if _, err := tx.NewDelete().Model((*ToolCache)(nil)).
					Where("name = ? AND provider = ? AND package = ?", t.Name, t.Provider, t.Package).
					Exec(ctx); err != nil {
					return fmt.Errorf("deleting migrated tracked tool %s/%s: %w", t.Provider, t.Name, err)
				}
				continue
			}
			if !t.Installed {
				if _, err := tx.NewDelete().Model((*ToolCache)(nil)).
					Where("name = ? AND provider = ? AND package = ?", t.Name, t.Provider, t.Package).
					Exec(ctx); err != nil {
					return fmt.Errorf("deleting stale tracked tool %s/%s: %w", t.Provider, t.Name, err)
				}
				continue
			}
			if _, err := tx.NewUpdate().Model((*ToolCache)(nil)).Set("tracked = FALSE").
				Where("name = ? AND provider = ? AND package = ?", t.Name, t.Provider, t.Package).
				Exec(ctx); err != nil {
				return fmt.Errorf("untracking stale tool %s/%s: %w", t.Provider, t.Name, err)
			}
		}
		for _, t := range desired {
			if t == nil {
				continue
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

// OutdatedUpdate — OutdatedUnknown records a provider that can upgrade the tool but cannot say whether it needs upgrading; it is never set together with Outdated.
type OutdatedUpdate struct {
	Name            string
	Provider        string
	Package         string
	Outdated        bool
	OutdatedUnknown bool
	LatestVersion   string
}

// UpdateOutdatedBatch — One transaction so SQLite batches the writes into a single fsync; rolls back on first error.
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
				Set("outdated_unknown = ?", u.OutdatedUnknown).
				Set("latest_version = ?", lv).
				Where("name = ? AND provider = ? AND package = ?", u.Name, u.Provider, u.Package).
				Exec(ctx); err != nil {
				return fmt.Errorf("updating outdated for %s/%s: %w", u.Provider, u.Name, err)
			}
		}
		return nil
	})
}

func (db *DB) PruneDiscovered(ctx context.Context, cutoff time.Time) error {
	_, err := db.bun.ExecContext(ctx,
		`DELETE FROM tool_cache WHERE tracked = 0 AND last_checked < ?`,
		cutoff)
	if err != nil {
		return fmt.Errorf("pruning discovered tools: %w", err)
	}
	return nil
}

type DiscoveredProviderScope struct {
	Provider      string
	InstalledWith string
}

func (db *DB) PruneDiscoveredProviders(ctx context.Context, cutoff time.Time, scopes []DiscoveredProviderScope) error {
	if len(scopes) == 0 {
		return nil
	}
	return db.bun.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for _, scope := range scopes {
			if scope.Provider == "" || scope.InstalledWith == "" {
				continue
			}
			if _, err := tx.NewDelete().Model((*ToolCache)(nil)).
				Where("tracked = FALSE").
				Where("last_checked < ?", cutoff).
				Where("provider = ?", scope.Provider).
				Where("installed_with = ?", scope.InstalledWith).
				Exec(ctx); err != nil {
				return fmt.Errorf("pruning discovered tools for %s/%s: %w", scope.Provider, scope.InstalledWith, err)
			}
		}
		return nil
	})
}
