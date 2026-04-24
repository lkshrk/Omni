package database_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/testguard"
)

func newTestDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestMigrate_Idempotent(t *testing.T) {
	db := newTestDB(t)
	// Second call must not fail.
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
}

func TestOpen_RejectsLivePathInLocalTests(t *testing.T) {
	if testguard.Isolated() {
		t.Skip("Docker-isolated tests do not enforce local live-path rejection")
	}
	_, err := database.Open(filepath.Join(string(filepath.Separator), "var", "tmp", "omni.db"))
	if err == nil {
		t.Fatal("Open accepted live database path in local test")
	}
	if !strings.Contains(err.Error(), "outside a temp directory") {
		t.Fatalf("Open err = %v, want outside-temp message", err)
	}
}

func TestUpsertAndGet(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	tool := &database.ToolCache{
		Name:     "ripgrep",
		Provider: "brew",
		Package:  "ripgrep",
	}
	if err := db.Upsert(ctx, tool); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := db.Get(ctx, "ripgrep", "brew", "ripgrep")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "ripgrep" || got.Provider != "brew" {
		t.Errorf("got %+v, want ripgrep/brew", got)
	}
}

func TestUpsert_Updates(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	tool := &database.ToolCache{Name: "ripgrep", Provider: "brew", Package: "ripgrep"}
	_ = db.Upsert(ctx, tool)

	// Upsert again with the same resolved key.
	tool.Installed = true
	if err := db.Upsert(ctx, tool); err != nil {
		t.Fatalf("second Upsert: %v", err)
	}
	got, _ := db.Get(ctx, "ripgrep", "brew", "ripgrep")
	if !got.Installed {
		t.Error("installed not updated")
	}
}

func TestUpsert_DifferentPackageIsDistinctKey(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	if err := db.Upsert(ctx, &database.ToolCache{Name: "ripgrep", Provider: "brew", Package: "ripgrep"}); err != nil {
		t.Fatalf("first Upsert: %v", err)
	}
	if err := db.Upsert(ctx, &database.ToolCache{Name: "ripgrep", Provider: "brew", Package: "ripgrep-all"}); err != nil {
		t.Fatalf("second Upsert: %v", err)
	}
	list, err := db.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("len(list) = %d, want 2", len(list))
	}
}

func TestDelete_OnlyRemovesMatchingPackage(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	_ = db.Upsert(ctx, &database.ToolCache{Name: "ripgrep", Provider: "brew", Package: "ripgrep"})
	_ = db.Upsert(ctx, &database.ToolCache{Name: "ripgrep", Provider: "brew", Package: "ripgrep-all"})

	if err := db.Delete(ctx, "ripgrep", "brew", "ripgrep"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := db.Get(ctx, "ripgrep", "brew", "ripgrep"); err == nil {
		t.Fatal("expected ripgrep package row to be deleted")
	}
	if _, err := db.Get(ctx, "ripgrep", "brew", "ripgrep-all"); err != nil {
		t.Fatalf("alternate package row should remain: %v", err)
	}
}

func TestGet_NotFound(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	_, err := db.Get(ctx, "nonexistent", "brew", "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing tool")
	}
}

func TestPackageAwareMethodsRequirePackage(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	check := func(name string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s returned nil error", name)
		}
		if !strings.Contains(err.Error(), "missing package") {
			t.Fatalf("%s error = %q, want missing package", name, err)
		}
	}

	_, err := db.Get(ctx, "ripgrep", "brew", "")
	check("Get", err)
	check("UpdateOutdated", db.UpdateOutdated(ctx, "ripgrep", "brew", "", true, "15.0.0"))
	check("UpdateDescription", db.UpdateDescription(ctx, "ripgrep", "brew", "", "Fast text search"))
	check("Delete", db.Delete(ctx, "ripgrep", "brew", ""))
	check("MarkInstalled", db.MarkInstalled(ctx, "ripgrep", "brew", "", "14.1.0"))
	check("MarkFailed", db.MarkFailed(ctx, "ripgrep", "brew", "", "install failed"))
	check("MarkUninstalled", db.MarkUninstalled(ctx, "ripgrep", "brew", ""))
	check("MarkTracked", db.MarkTracked(ctx, "ripgrep", "brew", ""))
	check("ReconcileTracked", db.ReconcileTracked(ctx, []*database.ToolCache{{Name: "ripgrep", Provider: "brew"}}))
}

func TestList(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	tools := []*database.ToolCache{
		{Name: "ripgrep", Provider: "brew", Package: "ripgrep"},
		{Name: "black", Provider: "pip", Package: "black"},
		{Name: "typescript", Provider: "npm", Package: "typescript"},
	}
	for _, tool := range tools {
		_ = db.Upsert(ctx, tool)
	}

	list, err := db.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("got %d tools, want 3", len(list))
	}
}

func TestListByProvider(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	_ = db.Upsert(ctx, &database.ToolCache{Name: "ripgrep", Provider: "brew", Package: "ripgrep"})
	_ = db.Upsert(ctx, &database.ToolCache{Name: "node", Provider: "brew", Package: "node"})
	_ = db.Upsert(ctx, &database.ToolCache{Name: "black", Provider: "pip", Package: "black"})

	list, err := db.ListByProvider(ctx, "brew")
	if err != nil {
		t.Fatalf("ListByProvider: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("got %d tools for brew, want 2", len(list))
	}
}

func TestDelete(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	_ = db.Upsert(ctx, &database.ToolCache{Name: "ripgrep", Provider: "brew", Package: "ripgrep"})
	if err := db.Delete(ctx, "ripgrep", "brew", "ripgrep"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := db.Get(ctx, "ripgrep", "brew", "ripgrep")
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestMarkInstalled(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	_ = db.Upsert(ctx, &database.ToolCache{Name: "ripgrep", Provider: "brew", Package: "ripgrep"})
	if err := db.MarkInstalled(ctx, "ripgrep", "brew", "ripgrep", "14.1.0"); err != nil {
		t.Fatalf("MarkInstalled: %v", err)
	}
	got, _ := db.Get(ctx, "ripgrep", "brew", "ripgrep")
	if !got.Installed {
		t.Error("expected installed=true")
	}
	if !got.Version.Valid || got.Version.String != "14.1.0" {
		t.Errorf("version: got %+v, want 14.1.0", got.Version)
	}
}

func TestMarkUninstalled(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	_ = db.Upsert(ctx, &database.ToolCache{Name: "ripgrep", Provider: "brew", Package: "ripgrep"})
	_ = db.MarkInstalled(ctx, "ripgrep", "brew", "ripgrep", "14.1.0")
	if err := db.MarkUninstalled(ctx, "ripgrep", "brew", "ripgrep"); err != nil {
		t.Fatalf("MarkUninstalled: %v", err)
	}
	got, _ := db.Get(ctx, "ripgrep", "brew", "ripgrep")
	if got.Installed {
		t.Error("expected installed=false")
	}
	if got.Version != (sql.NullString{}) {
		t.Errorf("expected version=NULL, got %+v", got.Version)
	}
}

func TestMarkUninstalled_ClearsOutdatedState(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	_ = db.Upsert(ctx, &database.ToolCache{Name: "ripgrep", Provider: "brew", Package: "ripgrep", Installed: true})
	if err := db.UpdateOutdated(ctx, "ripgrep", "brew", "ripgrep", true, "15.0.0"); err != nil {
		t.Fatalf("UpdateOutdated: %v", err)
	}
	if err := db.MarkUninstalled(ctx, "ripgrep", "brew", "ripgrep"); err != nil {
		t.Fatalf("MarkUninstalled: %v", err)
	}
	got, err := db.Get(ctx, "ripgrep", "brew", "ripgrep")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Outdated || got.LatestVersion.Valid {
		t.Fatalf("outdated/latest = %v/%v, want cleared", got.Outdated, got.LatestVersion)
	}
}

// ─── UpdateOutdated ───────────────────────────────────────────────────────────

func TestUpdateOutdated_SetsFlag(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	_ = db.Upsert(ctx, &database.ToolCache{Name: "ripgrep", Provider: "brew", Package: "ripgrep"})
	if err := db.UpdateOutdated(ctx, "ripgrep", "brew", "ripgrep", true, "15.0.0"); err != nil {
		t.Fatalf("UpdateOutdated: %v", err)
	}
	got, err := db.Get(ctx, "ripgrep", "brew", "ripgrep")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Outdated {
		t.Error("Outdated should be true")
	}
	if got.LatestVersion.String != "15.0.0" {
		t.Errorf("LatestVersion = %q, want 15.0.0", got.LatestVersion.String)
	}
}

func TestUpdateOutdated_ClearsFlag(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	_ = db.Upsert(ctx, &database.ToolCache{Name: "ripgrep", Provider: "brew", Package: "ripgrep"})
	_ = db.UpdateOutdated(ctx, "ripgrep", "brew", "ripgrep", true, "15.0.0")
	if err := db.UpdateOutdated(ctx, "ripgrep", "brew", "ripgrep", false, ""); err != nil {
		t.Fatalf("UpdateOutdated (clear): %v", err)
	}
	got, _ := db.Get(ctx, "ripgrep", "brew", "ripgrep")
	if got.Outdated {
		t.Error("Outdated should be false after clearing")
	}
}

// TestUpsert_PreservesOutdatedFlag is a regression test for the bug where
// Upsert (called by RefreshInstalled) included "outdated" in its ON CONFLICT
// SET clause, causing it to race with RefreshOutdated and wipe the ↑ update
// flags — making tools with available updates vanish from the UI after ~1 min.
func TestUpsert_PreservesOutdatedFlag(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	_ = db.Upsert(ctx, &database.ToolCache{Name: "ripgrep", Provider: "brew", Package: "ripgrep"})
	// Simulate RefreshOutdated marking the tool as outdated.
	_ = db.UpdateOutdated(ctx, "ripgrep", "brew", "ripgrep", true, "15.0.0")

	// Simulate RefreshInstalled re-upsetting the same tool (with Outdated unset/false).
	if err := db.Upsert(ctx, &database.ToolCache{
		Name:      "ripgrep",
		Provider:  "brew",
		Package:   "ripgrep",
		Installed: true,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := db.Get(ctx, "ripgrep", "brew", "ripgrep")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Outdated {
		t.Error("Upsert must not clear outdated=true — regression: was clobbering the flag")
	}
	if got.LatestVersion.String != "15.0.0" {
		t.Errorf("LatestVersion = %q, want 15.0.0 (Upsert must not clear latest_version)", got.LatestVersion.String)
	}
}

// ─── UpdateDescription ────────────────────────────────────────────────────────

func TestUpdateDescription_ExistingRow(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	_ = db.Upsert(ctx, &database.ToolCache{Name: "ripgrep", Provider: "brew", Package: "ripgrep"})
	if err := db.UpdateDescription(ctx, "ripgrep", "brew", "ripgrep", "Fast text search"); err != nil {
		t.Fatalf("UpdateDescription: %v", err)
	}
	got, err := db.Get(ctx, "ripgrep", "brew", "ripgrep")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Description.String != "Fast text search" {
		t.Errorf("Description = %q, want 'Fast text search'", got.Description.String)
	}
}

func TestUpdateDescription_OnlyUpdatesMatchingPackage(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	_ = db.Upsert(ctx, &database.ToolCache{Name: "editor", Provider: "apt", Package: "vim"})
	_ = db.Upsert(ctx, &database.ToolCache{Name: "editor", Provider: "apt", Package: "neovim"})

	if err := db.UpdateDescription(ctx, "editor", "apt", "neovim", "Modern Vim fork"); err != nil {
		t.Fatalf("UpdateDescription: %v", err)
	}
	vim, err := db.Get(ctx, "editor", "apt", "vim")
	if err != nil {
		t.Fatalf("Get vim: %v", err)
	}
	if vim.Description.Valid {
		t.Fatalf("vim description = %q, want empty", vim.Description.String)
	}
	neovim, err := db.Get(ctx, "editor", "apt", "neovim")
	if err != nil {
		t.Fatalf("Get neovim: %v", err)
	}
	if !neovim.Description.Valid || neovim.Description.String != "Modern Vim fork" {
		t.Fatalf("neovim description = %#v, want Modern Vim fork", neovim.Description)
	}
}

// ─── Bun ─────────────────────────────────────────────────────────────────────

func TestBun_ReturnsNonNil(t *testing.T) {
	db := newTestDB(t)
	if b := db.Bun(); b == nil {
		t.Error("Bun() should return non-nil")
	}
}

// ─── MarkFailed / ListFailed ──────────────────────────────────────────────────

func TestMarkFailed_CreatesRow(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	if err := db.MarkFailed(ctx, "ripgrep", "brew", "ripgrep", "brew: command failed"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	got, err := db.Get(ctx, "ripgrep", "brew", "ripgrep")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.FailureCount != 1 {
		t.Errorf("FailureCount = %d, want 1", got.FailureCount)
	}
	if !got.LastError.Valid || got.LastError.String != "brew: command failed" {
		t.Errorf("LastError = %+v, want 'brew: command failed'", got.LastError)
	}
	if got.FailedAt == nil {
		t.Error("FailedAt should be non-nil")
	}
	if got.Installed {
		t.Error("Installed should be false after failure")
	}
}

func TestMarkFailed_IncrementsCount(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	_ = db.MarkFailed(ctx, "ripgrep", "brew", "ripgrep", "first failure")
	_ = db.MarkFailed(ctx, "ripgrep", "brew", "ripgrep", "second failure")

	got, _ := db.Get(ctx, "ripgrep", "brew", "ripgrep")
	if got.FailureCount != 2 {
		t.Errorf("FailureCount = %d, want 2", got.FailureCount)
	}
	if got.LastError.String != "second failure" {
		t.Errorf("LastError = %q, want 'second failure'", got.LastError.String)
	}
}

func TestListFailed_ReturnsOnlyFailed(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	_ = db.Upsert(ctx, &database.ToolCache{Name: "ripgrep", Provider: "brew", Package: "ripgrep", Installed: true})
	_ = db.MarkFailed(ctx, "jq", "brew", "jq", "install error")
	_ = db.MarkFailed(ctx, "black", "pip", "black", "pip error")

	failed, err := db.ListFailed(ctx)
	if err != nil {
		t.Fatalf("ListFailed: %v", err)
	}
	if len(failed) != 2 {
		t.Errorf("ListFailed() = %d tools, want 2", len(failed))
	}
}

func TestListFailed_EmptyWhenNoFailures(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	_ = db.Upsert(ctx, &database.ToolCache{Name: "ripgrep", Provider: "brew", Package: "ripgrep", Installed: true})

	failed, err := db.ListFailed(ctx)
	if err != nil {
		t.Fatalf("ListFailed: %v", err)
	}
	if len(failed) != 0 {
		t.Errorf("ListFailed() = %d tools, want 0", len(failed))
	}
}

func TestUpsert_PreservesFailureStateWhenStillMissing(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	if err := db.MarkFailed(ctx, "ripgrep", "brew", "ripgrep", "install error"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	if err := db.Upsert(ctx, &database.ToolCache{
		Name:      "ripgrep",
		Provider:  "brew",
		Package:   "ripgrep",
		Installed: false,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := db.Get(ctx, "ripgrep", "brew", "ripgrep")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.FailureCount != 1 {
		t.Errorf("FailureCount = %d after missing Upsert, want 1", got.FailureCount)
	}
	if got.FailedAt == nil {
		t.Error("FailedAt should remain set after missing Upsert")
	}
	if !got.LastError.Valid || got.LastError.String != "install error" {
		t.Errorf("LastError = %+v, want preserved install error", got.LastError)
	}
	failed, err := db.ListFailed(ctx)
	if err != nil {
		t.Fatalf("ListFailed: %v", err)
	}
	if len(failed) != 1 {
		t.Fatalf("ListFailed() = %d, want 1", len(failed))
	}
}

func TestUpsert_ClearsFailureStateWhenInstalled(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	if err := db.MarkFailed(ctx, "ripgrep", "brew", "ripgrep", "install error"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	if err := db.Upsert(ctx, &database.ToolCache{
		Name:      "ripgrep",
		Provider:  "brew",
		Package:   "ripgrep",
		Installed: true,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := db.Get(ctx, "ripgrep", "brew", "ripgrep")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.FailureCount != 0 {
		t.Errorf("FailureCount = %d after Upsert, want 0", got.FailureCount)
	}
	if got.FailedAt != nil {
		t.Error("FailedAt should be nil after Upsert")
	}
	if got.LastError.Valid {
		t.Errorf("LastError should be NULL after Upsert, got %q", got.LastError.String)
	}
}

func TestUpdateDescription_NoExistingRow(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	// Inserting description without a prior Upsert should create a stub row.
	if err := db.UpdateDescription(ctx, "git", "brew", "git", "Distributed VCS"); err != nil {
		t.Fatalf("UpdateDescription: %v", err)
	}
	got, err := db.Get(ctx, "git", "brew", "git")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Description.String != "Distributed VCS" {
		t.Errorf("Description = %q, want 'Distributed VCS'", got.Description.String)
	}
}

// ─── UpsertDiscovered / MarkTracked / ListDiscovered / PruneDiscovered ────────

func TestUpsertDiscovered_InsertsRow(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	if err := db.UpsertDiscovered(ctx, "ripgrep", "brew", "brew", "14.1.0"); err != nil {
		t.Fatalf("UpsertDiscovered: %v", err)
	}

	discovered, err := db.ListDiscovered(ctx)
	if err != nil {
		t.Fatalf("ListDiscovered: %v", err)
	}
	if len(discovered) != 1 {
		t.Fatalf("got %d discovered tools, want 1", len(discovered))
	}
	if discovered[0].Name != "ripgrep" || discovered[0].Provider != "brew" {
		t.Errorf("got %+v, want ripgrep/brew", discovered[0])
	}
	if discovered[0].Tracked {
		t.Error("UpsertDiscovered should set tracked=false")
	}
}

func TestUpsertDiscovered_UpdatesVersion(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	if err := db.UpsertDiscovered(ctx, "ripgrep", "brew", "brew", "14.0.0"); err != nil {
		t.Fatalf("first UpsertDiscovered: %v", err)
	}
	if err := db.UpsertDiscovered(ctx, "ripgrep", "brew", "brew", "14.1.0"); err != nil {
		t.Fatalf("second UpsertDiscovered: %v", err)
	}

	discovered, err := db.ListDiscovered(ctx)
	if err != nil {
		t.Fatalf("ListDiscovered: %v", err)
	}
	if len(discovered) != 1 {
		t.Fatalf("got %d discovered tools, want 1", len(discovered))
	}
	if !discovered[0].Version.Valid || discovered[0].Version.String != "14.1.0" {
		t.Errorf("version = %+v, want 14.1.0", discovered[0].Version)
	}
}

func TestUpsertDiscovered_DoesNotOverwriteConfigTrackedRow(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	// Insert a config-tracked row via Upsert (tracked=true by default).
	if err := db.Upsert(ctx, &database.ToolCache{
		Name:     "ripgrep",
		Provider: "brew",
		Package:  "ripgrep",
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// UpsertDiscovered must not overwrite the tracked=true row.
	if err := db.UpsertDiscovered(ctx, "ripgrep", "brew", "brew", "99.0.0"); err != nil {
		t.Fatalf("UpsertDiscovered: %v", err)
	}

	// The row should still be tracked and not appear in ListDiscovered.
	discovered, err := db.ListDiscovered(ctx)
	if err != nil {
		t.Fatalf("ListDiscovered: %v", err)
	}
	if len(discovered) != 0 {
		t.Errorf("config-tracked row appeared in ListDiscovered: %v", discovered)
	}

	// The original row should still exist and be tracked.
	got, err := db.Get(ctx, "ripgrep", "brew", "ripgrep")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Tracked {
		t.Error("Tracked should still be true after UpsertDiscovered on a config-tracked row")
	}
}

func TestMarkTracked_PromotesDiscoveredRow(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	if err := db.UpsertDiscovered(ctx, "ripgrep", "brew", "brew", "14.1.0"); err != nil {
		t.Fatalf("UpsertDiscovered: %v", err)
	}

	// Before MarkTracked, it should appear in ListDiscovered.
	before, err := db.ListDiscovered(ctx)
	if err != nil {
		t.Fatalf("ListDiscovered before: %v", err)
	}
	if len(before) != 1 {
		t.Fatalf("expected 1 discovered tool before MarkTracked, got %d", len(before))
	}

	if err := db.MarkTracked(ctx, "ripgrep", "brew", "ripgrep"); err != nil {
		t.Fatalf("MarkTracked: %v", err)
	}

	// After MarkTracked, it should not appear in ListDiscovered.
	after, err := db.ListDiscovered(ctx)
	if err != nil {
		t.Fatalf("ListDiscovered after: %v", err)
	}
	if len(after) != 0 {
		t.Errorf("expected 0 discovered tools after MarkTracked, got %d", len(after))
	}

	// The row should now be tracked.
	got, err := db.Get(ctx, "ripgrep", "brew", "ripgrep")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Tracked {
		t.Error("expected tracked=true after MarkTracked")
	}
}

func TestListDiscovered_ExcludesConfigTrackedRows(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	// Config-tracked row.
	if err := db.Upsert(ctx, &database.ToolCache{Name: "git", Provider: "brew", Package: "git"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	// Discovered (tracked=false) row.
	if err := db.UpsertDiscovered(ctx, "ripgrep", "brew", "brew", "14.1.0"); err != nil {
		t.Fatalf("UpsertDiscovered: %v", err)
	}

	discovered, err := db.ListDiscovered(ctx)
	if err != nil {
		t.Fatalf("ListDiscovered: %v", err)
	}
	if len(discovered) != 1 {
		t.Fatalf("got %d discovered tools, want 1", len(discovered))
	}
	if discovered[0].Name != "ripgrep" {
		t.Errorf("got %q, want ripgrep", discovered[0].Name)
	}
}

func TestPruneDiscovered_RemovesOldRows(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	// Insert a discovered tool.
	if err := db.UpsertDiscovered(ctx, "ripgrep", "brew", "brew", "14.1.0"); err != nil {
		t.Fatalf("UpsertDiscovered: %v", err)
	}

	// Prune with a cutoff in the future — all discovered rows should be removed.
	cutoff := time.Now().Add(time.Minute)
	if err := db.PruneDiscovered(ctx, cutoff); err != nil {
		t.Fatalf("PruneDiscovered: %v", err)
	}

	discovered, err := db.ListDiscovered(ctx)
	if err != nil {
		t.Fatalf("ListDiscovered: %v", err)
	}
	if len(discovered) != 0 {
		t.Errorf("expected 0 discovered tools after prune, got %d", len(discovered))
	}
}

func TestPruneDiscovered_KeepsNewRows(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	// Insert a discovered tool.
	if err := db.UpsertDiscovered(ctx, "ripgrep", "brew", "brew", "14.1.0"); err != nil {
		t.Fatalf("UpsertDiscovered: %v", err)
	}

	// Prune with a cutoff in the past — no rows should be removed.
	cutoff := time.Now().Add(-time.Minute)
	if err := db.PruneDiscovered(ctx, cutoff); err != nil {
		t.Fatalf("PruneDiscovered: %v", err)
	}

	discovered, err := db.ListDiscovered(ctx)
	if err != nil {
		t.Fatalf("ListDiscovered: %v", err)
	}
	if len(discovered) != 1 {
		t.Errorf("expected 1 discovered tool after prune with past cutoff, got %d", len(discovered))
	}
}

func TestPruneDiscovered_DoesNotRemoveConfigTrackedRows(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	// Config-tracked row.
	if err := db.Upsert(ctx, &database.ToolCache{Name: "git", Provider: "brew", Package: "git"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Prune with a future cutoff.
	cutoff := time.Now().Add(time.Minute)
	if err := db.PruneDiscovered(ctx, cutoff); err != nil {
		t.Fatalf("PruneDiscovered: %v", err)
	}

	// Config-tracked row must still exist.
	got, err := db.Get(ctx, "git", "brew", "git")
	if err != nil {
		t.Fatalf("Get after prune: %v", err)
	}
	if got.Name != "git" {
		t.Errorf("config-tracked row was pruned: got %+v", got)
	}
}

// TestPruneDiscovered_SameSecondNotPruned is a regression test for the
// CURRENT_TIMESTAMP precision race: when upsert and cutoff land in the same
// wall-clock second, SQLite's CURRENT_TIMESTAMP (no sub-seconds) produced a
// timestamp that compared as older than the Go time.Time cutoff, causing the
// row to be pruned immediately. The fix passes time.Now() as a bound parameter
// so both sides carry identical precision and the row is correctly kept.
func TestPruneDiscovered_SameSecondNotPruned(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	// Record cutoff BEFORE the upsert so it is ≤ last_checked.
	cutoff := time.Now()

	if err := db.UpsertDiscovered(ctx, "ripgrep", "brew", "brew", "14.1.0"); err != nil {
		t.Fatalf("UpsertDiscovered: %v", err)
	}

	// Prune with the cutoff captured before the upsert.
	// last_checked >= cutoff → row must survive.
	if err := db.PruneDiscovered(ctx, cutoff); err != nil {
		t.Fatalf("PruneDiscovered: %v", err)
	}

	discovered, err := db.ListDiscovered(ctx)
	if err != nil {
		t.Fatalf("ListDiscovered: %v", err)
	}
	if len(discovered) != 1 {
		t.Errorf("same-second prune race: expected 1 row to survive, got %d", len(discovered))
	}
}

func TestReconcileTracked_UntracksStalePackageRows(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	_ = db.Upsert(ctx, &database.ToolCache{Name: "ripgrep", Provider: "brew", Package: "ripgrep"})
	_ = db.Upsert(ctx, &database.ToolCache{Name: "ripgrep", Provider: "brew", Package: "ripgrep-all"})

	if err := db.ReconcileTracked(ctx, []*database.ToolCache{{
		Name:     "ripgrep",
		Provider: "brew",
		Package:  "ripgrep-all",
	}}); err != nil {
		t.Fatalf("ReconcileTracked: %v", err)
	}
	oldRow, err := db.Get(ctx, "ripgrep", "brew", "ripgrep")
	if err != nil {
		t.Fatalf("Get stale package row: %v", err)
	}
	if oldRow.Tracked {
		t.Fatal("stale package row should be untracked")
	}
	currentRow, err := db.Get(ctx, "ripgrep", "brew", "ripgrep-all")
	if err != nil {
		t.Fatalf("Get desired package row: %v", err)
	}
	if !currentRow.Tracked {
		t.Fatal("desired package row should stay tracked")
	}
}
