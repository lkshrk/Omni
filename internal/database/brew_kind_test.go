package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/database"
)

// TestBrewKind_MigrationAddsColumnToOldSchema verifies that Migrate adds the
// artifact_kind column to a tool_metadata table that was created before the
// column existed, and that any pre-existing rows survive with an empty kind.
func TestBrewKind_MigrationAddsColumnToOldSchema(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Create tool_metadata without artifact_kind to simulate an old-schema DB.
	if _, err := db.Bun().ExecContext(ctx, `
		CREATE TABLE tool_metadata (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			name            TEXT NOT NULL,
			provider        TEXT NOT NULL,
			package         TEXT NOT NULL,
			version         TEXT,
			description     TEXT,
			source_type     TEXT NOT NULL DEFAULT '',
			source_owner    TEXT NOT NULL DEFAULT '',
			source_repo     TEXT NOT NULL DEFAULT '',
			source_url      TEXT,
			privilege       TEXT NOT NULL DEFAULT '',
			privilege_reason TEXT,
			updated_at      DATETIME NOT NULL
		)
	`); err != nil {
		t.Fatalf("create old-schema tool_metadata: %v", err)
	}
	// Insert a row that predates artifact_kind.
	if _, err := db.Bun().ExecContext(ctx, `
		INSERT INTO tool_metadata (name, provider, package, updated_at)
		VALUES ('ripgrep', 'brew', 'ripgrep', CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("seed old row: %v", err)
	}

	// Migrate requires several other tables to exist first (local_state for the
	// provider-list-cache migration marker, tool_cache for migrateExistingToolMetadata,
	// etc.). Rather than recreating every table manually, run a full Migrate which
	// creates the missing tables and then adds the artifact_kind column via ALTER TABLE.
	// Pre-seed the provider-list-cache-cleared marker so Migrate does not wipe
	// tool_metadata before we can verify the old row survives.
	if _, err := db.Bun().ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS local_state (
			key        TEXT PRIMARY KEY NOT NULL,
			value      TEXT NOT NULL,
			updated_at DATETIME NOT NULL
		)
	`); err != nil {
		t.Fatalf("create local_state: %v", err)
	}
	if _, err := db.Bun().ExecContext(ctx, `
		INSERT INTO local_state (key, value, updated_at)
		VALUES ('migration.provider_list_cache_cleared', '1', CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("seed migration marker: %v", err)
	}

	// Migrate must succeed and add the missing column.
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate on old-schema DB: %v", err)
	}

	// The pre-existing row must still be readable with an empty artifact_kind.
	var kind string
	if err := db.Bun().QueryRowContext(ctx,
		`SELECT artifact_kind FROM tool_metadata WHERE name='ripgrep'`,
	).Scan(&kind); err != nil {
		t.Fatalf("reading artifact_kind after migration: %v", err)
	}
	if kind != "" {
		t.Errorf("pre-existing row artifact_kind = %q, want empty string", kind)
	}
}

// TestBrewKind_UpsertFormula verifies that a MetadataUpdate with ArtifactKind
// "formula" is persisted and survives a List round-trip.
func TestBrewKind_UpsertFormula(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	// Seed a tool_cache row so the List/hydrateMetadata path has something to join.
	if err := db.Upsert(ctx, &database.ToolCache{
		Name:        "ripgrep",
		Provider:    "brew",
		Package:     "ripgrep",
		Installed:   true,
		LastChecked: time.Now(),
	}); err != nil {
		t.Fatalf("Upsert tool: %v", err)
	}

	if err := db.UpsertMetadataBatch(ctx, []database.MetadataUpdate{
		{
			Name:         "ripgrep",
			Provider:     "brew",
			Package:      "ripgrep",
			ArtifactKind: "formula",
		},
	}); err != nil {
		t.Fatalf("UpsertMetadataBatch: %v", err)
	}

	tools, err := db.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("expected at least one tool")
	}
	got := tools[0].Options["brew_kind"]
	if got != "formula" {
		t.Errorf("Options[brew_kind] = %q, want %q", got, "formula")
	}
}

// TestBrewKind_UpsertCask verifies that a MetadataUpdate with ArtifactKind
// "cask" is persisted and hydrated back with brew_kind=cask.
func TestBrewKind_UpsertCask(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	if err := db.Upsert(ctx, &database.ToolCache{
		Name:        "iterm2",
		Provider:    "brew",
		Package:     "iterm2",
		Installed:   true,
		LastChecked: time.Now(),
	}); err != nil {
		t.Fatalf("Upsert tool: %v", err)
	}

	if err := db.UpsertMetadataBatch(ctx, []database.MetadataUpdate{
		{
			Name:         "iterm2",
			Provider:     "brew",
			Package:      "iterm2",
			ArtifactKind: "cask",
		},
	}); err != nil {
		t.Fatalf("UpsertMetadataBatch: %v", err)
	}

	tools, err := db.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("expected at least one tool")
	}
	got := tools[0].Options["brew_kind"]
	if got != "cask" {
		t.Errorf("Options[brew_kind] = %q, want %q", got, "cask")
	}
}

// TestBrewKind_UpsertPreservesExistingKind verifies that upserting with an
// empty ArtifactKind does not overwrite a previously stored value (idempotent
// upsert preservation, A2).
func TestBrewKind_UpsertPreservesExistingKind(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	if err := db.Upsert(ctx, &database.ToolCache{
		Name:        "iterm2",
		Provider:    "brew",
		Package:     "iterm2",
		Installed:   true,
		LastChecked: time.Now(),
	}); err != nil {
		t.Fatalf("Upsert tool: %v", err)
	}

	// First upsert stores "cask".
	if err := db.UpsertMetadataBatch(ctx, []database.MetadataUpdate{
		{Name: "iterm2", Provider: "brew", Package: "iterm2", ArtifactKind: "cask"},
	}); err != nil {
		t.Fatalf("UpsertMetadataBatch (first): %v", err)
	}

	// Second upsert has no ArtifactKind — existing value must be preserved.
	if err := db.UpsertMetadataBatch(ctx, []database.MetadataUpdate{
		{Name: "iterm2", Provider: "brew", Package: "iterm2", ArtifactKind: ""},
	}); err != nil {
		t.Fatalf("UpsertMetadataBatch (second): %v", err)
	}

	tools, err := db.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("expected at least one tool")
	}
	got := tools[0].Options["brew_kind"]
	if got != "cask" {
		t.Errorf("Options[brew_kind] after empty re-upsert = %q, want %q", got, "cask")
	}
}

// TestBrewKind_ProviderGuard verifies that applyToolMetadata does NOT inject
// brew_kind into a non-brew tool even when the stored artifact_kind is non-empty.
// This guards against future corruption if another provider adopts ArtifactKind.
func TestBrewKind_ProviderGuard(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	if err := db.Upsert(ctx, &database.ToolCache{
		Name:        "some-tool",
		Provider:    "apt",
		Package:     "some-tool",
		Installed:   true,
		LastChecked: time.Now(),
	}); err != nil {
		t.Fatalf("Upsert tool: %v", err)
	}

	// Store an artifact_kind for a non-brew provider row (hypothetical future use).
	if err := db.UpsertMetadataBatch(ctx, []database.MetadataUpdate{
		{Name: "some-tool", Provider: "apt", Package: "some-tool", ArtifactKind: "deb"},
	}); err != nil {
		t.Fatalf("UpsertMetadataBatch: %v", err)
	}

	tools, err := db.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("expected at least one tool")
	}
	if kind, ok := tools[0].Options["brew_kind"]; ok && kind != "" {
		t.Errorf("non-brew tool has Options[brew_kind] = %q after provider guard, want absent", kind)
	}
}

// TestBrewKind_NonBrewUnaffected verifies that a non-brew tool row does not
// receive a brew_kind option when there is no artifact_kind in its metadata (A5).
func TestBrewKind_NonBrewUnaffected(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	if err := db.Upsert(ctx, &database.ToolCache{
		Name:        "typescript",
		Provider:    "node",
		Package:     "typescript",
		Installed:   true,
		LastChecked: time.Now(),
	}); err != nil {
		t.Fatalf("Upsert tool: %v", err)
	}

	if err := db.UpsertMetadataBatch(ctx, []database.MetadataUpdate{
		{Name: "typescript", Provider: "node", Package: "typescript", Description: "TypeScript compiler"},
	}); err != nil {
		t.Fatalf("UpsertMetadataBatch: %v", err)
	}

	tools, err := db.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("expected at least one tool")
	}
	if kind, ok := tools[0].Options["brew_kind"]; ok && kind != "" {
		t.Errorf("non-brew tool has Options[brew_kind] = %q, want empty/absent", kind)
	}
}

// TestBrewKind_UnknownKindNoOptions verifies that rows without artifact_kind
// metadata produce a nil/empty Options map (no brew_kind key injected), so
// existing formula behavior is unchanged (A4).
func TestBrewKind_UnknownKindNoOptions(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	if err := db.Upsert(ctx, &database.ToolCache{
		Name:        "git",
		Provider:    "brew",
		Package:     "git",
		Installed:   true,
		LastChecked: time.Now(),
	}); err != nil {
		t.Fatalf("Upsert tool: %v", err)
	}
	// No UpsertMetadataBatch call — simulates pre-existing DB row without kind.

	tools, err := db.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("expected at least one tool")
	}
	if kind := tools[0].Options["brew_kind"]; kind != "" {
		t.Errorf("tool without metadata has Options[brew_kind] = %q, want empty", kind)
	}
}
