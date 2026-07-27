package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/database"
)

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
	if _, err := db.Bun().ExecContext(ctx, `
		INSERT INTO tool_metadata (name, provider, package, updated_at)
		VALUES ('ripgrep', 'brew', 'ripgrep', CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("seed old row: %v", err)
	}

	// Full Migrate builds the other required tables; pre-seed the cleared marker so it does not wipe tool_metadata.
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

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate on old-schema DB: %v", err)
	}

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

func TestBrewKind_UpsertFormula(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

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

	if err := db.UpsertMetadataBatch(ctx, []database.MetadataUpdate{
		{Name: "iterm2", Provider: "brew", Package: "iterm2", ArtifactKind: "cask"},
	}); err != nil {
		t.Fatalf("UpsertMetadataBatch (first): %v", err)
	}

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

// Guards against corruption if another provider ever adopts ArtifactKind.
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
