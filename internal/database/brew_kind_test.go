package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/database"
)

// TestBrewKind_MigrationIdempotent verifies that running Migrate twice on an
// existing database that already has the artifact_kind column does not fail.
func TestBrewKind_MigrationIdempotent(t *testing.T) {
	db := newTestDB(t)
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("second Migrate: %v", err)
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
