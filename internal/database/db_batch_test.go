package database_test

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/database"
)

func TestUpsertBatch_EmptyIsNoOp(t *testing.T) {
	db := newTestDB(t)
	if err := db.UpsertBatch(context.Background(), nil); err != nil {
		t.Errorf("UpsertBatch(nil) = %v, want nil", err)
	}
	if err := db.UpsertBatch(context.Background(), []*database.ToolCache{}); err != nil {
		t.Errorf("UpsertBatch([]) = %v, want nil", err)
	}
}

func TestUpsertBatch_InsertsAllAndPersists(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	batch := []*database.ToolCache{
		{Name: "ripgrep", Provider: "brew", Package: "ripgrep"},
		{Name: "fd", Provider: "brew", Package: "fd"},
		{Name: "bat", Provider: "brew"}, // Package empty → defaults to Name
	}
	if err := db.UpsertBatch(ctx, batch); err != nil {
		t.Fatalf("UpsertBatch: %v", err)
	}
	for _, want := range []struct{ name, prov, pkg string }{
		{"ripgrep", "brew", "ripgrep"},
		{"fd", "brew", "fd"},
		{"bat", "brew", "bat"},
	} {
		got, err := db.Get(ctx, want.name, want.prov, want.pkg)
		if err != nil {
			t.Errorf("Get(%s) after batch: %v", want.name, err)
			continue
		}
		if got.Name != want.name || got.Provider != want.prov || got.Package != want.pkg {
			t.Errorf("Get(%s) = %+v, want name=%s prov=%s pkg=%s", want.name, got, want.name, want.prov, want.pkg)
		}
	}
}

func TestUpsertBatch_SkipsNilEntries(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	batch := []*database.ToolCache{
		{Name: "ripgrep", Provider: "brew", Package: "ripgrep"},
		nil,
		{Name: "fd", Provider: "brew", Package: "fd"},
	}
	if err := db.UpsertBatch(ctx, batch); err != nil {
		t.Fatalf("UpsertBatch with nil entry: %v", err)
	}
	if _, err := db.Get(ctx, "ripgrep", "brew", "ripgrep"); err != nil {
		t.Errorf("ripgrep after nil-skip batch: %v", err)
	}
	if _, err := db.Get(ctx, "fd", "brew", "fd"); err != nil {
		t.Errorf("fd after nil-skip batch: %v", err)
	}
}

func TestUpsertBatch_UpdatesExistingRows(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	if err := db.Upsert(ctx, &database.ToolCache{
		Name: "ripgrep", Provider: "brew", Package: "ripgrep", Installed: false,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := db.UpsertBatch(ctx, []*database.ToolCache{
		{Name: "ripgrep", Provider: "brew", Package: "ripgrep", Installed: true},
	}); err != nil {
		t.Fatalf("UpsertBatch update: %v", err)
	}
	got, err := db.Get(ctx, "ripgrep", "brew", "ripgrep")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Installed {
		t.Error("Installed flag did not flip via UpsertBatch update")
	}
}

func TestUpsertMetadataBatch_StoresSourceHints(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	if err := db.UpsertMetadataBatch(ctx, []database.MetadataUpdate{{
		Name:        "ripgrep",
		Provider:    "system",
		Package:     "ripgrep",
		SourceType:  "github",
		SourceOwner: "BurntSushi",
		SourceRepo:  "ripgrep",
		SourceURL:   "https://github.com/BurntSushi/ripgrep",
	}}); err != nil {
		t.Fatalf("UpsertMetadataBatch: %v", err)
	}
	meta, err := db.GetMetadata(ctx, "ripgrep", "system", "ripgrep")
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
	if meta.SourceType != "github" || meta.SourceOwner != "BurntSushi" || meta.SourceRepo != "ripgrep" {
		t.Fatalf("source hint = %s/%s/%s, want github/BurntSushi/ripgrep", meta.SourceType, meta.SourceOwner, meta.SourceRepo)
	}
	if !meta.SourceURL.Valid || meta.SourceURL.String != "https://github.com/BurntSushi/ripgrep" {
		t.Fatalf("SourceURL = %+v, want GitHub URL", meta.SourceURL)
	}
}

func TestUpsertBatch_PreservesFailureStateWhenStillMissing(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	if err := db.MarkFailed(ctx, "ripgrep", "brew", "ripgrep", "install error"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	if err := db.UpsertBatch(ctx, []*database.ToolCache{
		{Name: "ripgrep", Provider: "brew", Package: "ripgrep", Installed: false},
	}); err != nil {
		t.Fatalf("UpsertBatch: %v", err)
	}

	got, err := db.Get(ctx, "ripgrep", "brew", "ripgrep")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.FailureCount != 1 {
		t.Errorf("FailureCount = %d after missing UpsertBatch, want 1", got.FailureCount)
	}
	if got.FailedAt == nil {
		t.Error("FailedAt should remain set after missing UpsertBatch")
	}
	if !got.LastError.Valid || got.LastError.String != "install error" {
		t.Errorf("LastError = %+v, want preserved install error", got.LastError)
	}
}

func TestUpdateOutdatedBatch_EmptyIsNoOp(t *testing.T) {
	db := newTestDB(t)
	if err := db.UpdateOutdatedBatch(context.Background(), nil); err != nil {
		t.Errorf("UpdateOutdatedBatch(nil) = %v", err)
	}
}

func TestUpdateOutdatedBatch_AppliesAllUpdates(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	for _, name := range []string{"ripgrep", "fd"} {
		if err := db.Upsert(ctx, &database.ToolCache{Name: name, Provider: "brew", Package: name}); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	updates := []database.OutdatedUpdate{
		{Name: "ripgrep", Provider: "brew", Package: "ripgrep", Outdated: true, LatestVersion: "14.1.0"},
		{Name: "fd", Provider: "brew", Package: "fd", Outdated: false, LatestVersion: ""},
	}
	if err := db.UpdateOutdatedBatch(ctx, updates); err != nil {
		t.Fatalf("UpdateOutdatedBatch: %v", err)
	}
	rg, _ := db.Get(ctx, "ripgrep", "brew", "ripgrep")
	if !rg.Outdated || rg.LatestVersion.String != "14.1.0" {
		t.Errorf("ripgrep not updated: outdated=%v latest=%v", rg.Outdated, rg.LatestVersion)
	}
	fd, _ := db.Get(ctx, "fd", "brew", "fd")
	if fd.Outdated || fd.LatestVersion.Valid {
		t.Errorf("fd should be up-to-date: outdated=%v latest=%v", fd.Outdated, fd.LatestVersion)
	}
}

func TestUpdateOutdatedBatch_RejectsInvalidEntry(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	updates := []database.OutdatedUpdate{
		{Name: "ripgrep", Provider: "brew", Package: ""}, // empty Package fails requirePackage
	}
	if err := db.UpdateOutdatedBatch(ctx, updates); err == nil {
		t.Error("expected error for empty Package in batch entry")
	}
}

func TestUpdateDescriptionBatch_EmptyIsNoOp(t *testing.T) {
	db := newTestDB(t)
	if err := db.UpdateDescriptionBatch(context.Background(), nil); err != nil {
		t.Errorf("UpdateDescriptionBatch(nil) = %v", err)
	}
}

func TestUpdateDescriptionBatch_UpdatesExistingAndCachesMissing(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	if err := db.Upsert(ctx, &database.ToolCache{Name: "ripgrep", Provider: "brew", Package: "ripgrep"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	updates := []database.DescriptionUpdate{
		{Name: "ripgrep", Provider: "brew", Package: "ripgrep", Description: "fast grep"},
		{Name: "fd", Provider: "brew", Package: "fd", Description: "fast find"},
	}
	if err := db.UpdateDescriptionBatch(ctx, updates); err != nil {
		t.Fatalf("UpdateDescriptionBatch: %v", err)
	}
	rg, _ := db.Get(ctx, "ripgrep", "brew", "ripgrep")
	if rg.Description.String != "fast grep" {
		t.Errorf("ripgrep description = %q, want 'fast grep'", rg.Description.String)
	}
	if _, err := db.Get(ctx, "fd", "brew", "fd"); err == nil {
		t.Fatal("fd metadata-only update created a tool_cache row")
	}
	fd, err := db.GetMetadata(ctx, "fd", "brew", "fd")
	if err != nil {
		t.Fatalf("fd metadata: %v", err)
	}
	if fd.Description.String != "fast find" {
		t.Errorf("fd metadata description = %q, want 'fast find'", fd.Description.String)
	}
	list, err := db.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Name != "ripgrep" {
		t.Fatalf("List = %+v, want only ripgrep state row", list)
	}
}

func TestUpdateDescriptionBatch_RejectsInvalidEntry(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	if err := db.UpdateDescriptionBatch(ctx, []database.DescriptionUpdate{
		{Name: "ripgrep", Provider: "brew", Package: "", Description: "d"}, // empty Package fails requirePackage
	}); err == nil {
		t.Error("expected error for empty Package in batch entry")
	}
}

func TestUpsertMetadataBatch_HydratesExistingRowsWithoutCreatingState(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	if err := db.Upsert(ctx, &database.ToolCache{
		Name:      "ripgrep",
		Provider:  "brew",
		Package:   "ripgrep",
		Installed: true,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := db.UpsertMetadataBatch(ctx, []database.MetadataUpdate{
		{
			Name:            "ripgrep",
			Provider:        "brew",
			Package:         "ripgrep",
			Version:         "14.1.0",
			Description:     "fast grep",
			Privilege:       "maybe",
			PrivilegeReason: "cask may run installer package",
		},
		{
			Name:        "fd",
			Provider:    "brew",
			Package:     "fd",
			Description: "fast find",
		},
	}); err != nil {
		t.Fatalf("UpsertMetadataBatch: %v", err)
	}

	rg, err := db.Get(ctx, "ripgrep", "brew", "ripgrep")
	if err != nil {
		t.Fatalf("Get ripgrep: %v", err)
	}
	if !rg.Description.Valid || rg.Description.String != "fast grep" {
		t.Fatalf("ripgrep description = %+v, want fast grep", rg.Description)
	}
	if rg.Privilege != "maybe" {
		t.Fatalf("ripgrep privilege = %q, want maybe", rg.Privilege)
	}
	if !rg.PrivilegeReason.Valid || rg.PrivilegeReason.String != "cask may run installer package" {
		t.Fatalf("ripgrep privilege reason = %+v, want cached reason", rg.PrivilegeReason)
	}

	if _, err := db.Get(ctx, "fd", "brew", "fd"); err == nil {
		t.Fatal("metadata-only fd created a tool_cache row")
	}
	list, err := db.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Name != "ripgrep" {
		t.Fatalf("List = %+v, want only ripgrep state row", list)
	}
}

func TestUpsertDiscoveredBatch_EmptyIsNoOp(t *testing.T) {
	db := newTestDB(t)
	if err := db.UpsertDiscoveredBatch(context.Background(), nil); err != nil {
		t.Errorf("UpsertDiscoveredBatch(nil) = %v", err)
	}
}

func TestUpsertDiscoveredBatch_InsertsAsUntracked(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	entries := []database.DiscoveredUpsert{
		{Name: "fd", Provider: "brew", InstalledWith: "brew", Version: "9.0.0"},
		{Name: "bat", Provider: "brew", InstalledWith: "brew", Version: ""},
	}
	if err := db.UpsertDiscoveredBatch(ctx, entries); err != nil {
		t.Fatalf("UpsertDiscoveredBatch: %v", err)
	}
	discovered, err := db.ListDiscovered(ctx)
	if err != nil {
		t.Fatalf("ListDiscovered: %v", err)
	}
	names := make(map[string]bool)
	for _, d := range discovered {
		names[d.Name] = true
	}
	if !names["fd"] || !names["bat"] {
		t.Errorf("ListDiscovered = %+v, want both fd and bat", names)
	}
}

func TestUpsertDiscoveredBatch_DoesNotOverwriteTrackedRows(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	// Seed a tracked (config-managed) row.
	if err := db.Upsert(ctx, &database.ToolCache{
		Name: "ripgrep", Provider: "brew", Package: "ripgrep", Installed: false,
	}); err != nil {
		t.Fatalf("seed tracked: %v", err)
	}
	// UpsertDiscoveredBatch's WHERE clause guards against touching tracked rows.
	if err := db.UpsertDiscoveredBatch(ctx, []database.DiscoveredUpsert{
		{Name: "ripgrep", Provider: "brew", InstalledWith: "brew", Version: "14.1.0"},
	}); err != nil {
		t.Fatalf("UpsertDiscoveredBatch on tracked: %v", err)
	}
	got, _ := db.Get(ctx, "ripgrep", "brew", "ripgrep")
	if got.Installed {
		t.Error("UpsertDiscoveredBatch should not flip a tracked row's Installed flag")
	}
}

func TestMarkTrackedBatch_PromotesDiscoveredRows(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	if err := db.UpsertDiscoveredBatch(ctx, []database.DiscoveredUpsert{
		{Name: "ripgrep", Provider: "system", InstalledWith: "brew", Version: "14.1.0"},
		{Name: "fd", Provider: "system", InstalledWith: "brew", Version: "9.0.0"},
	}); err != nil {
		t.Fatalf("UpsertDiscoveredBatch: %v", err)
	}
	if err := db.MarkTrackedBatch(ctx, []database.TrackedTool{
		{Name: "ripgrep", Provider: "system", Package: "ripgrep"},
		{Name: "fd", Provider: "system", Package: "fd"},
	}); err != nil {
		t.Fatalf("MarkTrackedBatch: %v", err)
	}
	discovered, err := db.ListDiscovered(ctx)
	if err != nil {
		t.Fatalf("ListDiscovered: %v", err)
	}
	if len(discovered) != 0 {
		t.Fatalf("ListDiscovered after MarkTrackedBatch = %+v, want none", discovered)
	}
}

func TestMarkTrackedBatch_EmptyIsNoOp(t *testing.T) {
	db := newTestDB(t)
	if err := db.MarkTrackedBatch(context.Background(), nil); err != nil {
		t.Fatalf("MarkTrackedBatch(nil): %v", err)
	}
}
