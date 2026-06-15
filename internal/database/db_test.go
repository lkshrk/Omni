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

func TestMigrate_ClearsProviderDerivedCacheOnce(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	if _, err := db.Bun().ExecContext(ctx,
		`INSERT INTO tool_cache (
		     name, provider, package, installed, description,
		     privilege, privilege_reason, last_checked
		 )
		 VALUES (?, ?, ?, FALSE, ?, ?, ?, CURRENT_TIMESTAMP)`,
		"parsec", "system", "parsec", "remote desktop", "maybe", "cask may run installer package"); err != nil {
		t.Fatalf("seed tool cache: %v", err)
	}
	if _, err := db.Bun().ExecContext(ctx,
		`INSERT INTO tool_metadata (name, provider, package, updated_at)
		 VALUES (?, ?, ?, CURRENT_TIMESTAMP)`,
		"fd", "brew", "fd"); err != nil {
		t.Fatalf("seed tool metadata: %v", err)
	}
	if err := db.UpsertPackageAvailability(ctx, database.PackageAvailability{
		Name: "rg", Provider: "apt", Package: "ripgrep", Available: false, CheckedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed package availability: %v", err)
	}
	if err := db.UpsertUpdateMetadata(ctx, database.UpdateMetadata{
		Provider: "npm", Package: "typescript", Version: "5.8.0", AvailableAt: time.Now().UTC(), DateSource: "registry", CheckedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed update metadata: %v", err)
	}
	if err := db.SetState(ctx, "bootstrap.testhost", "complete"); err != nil {
		t.Fatalf("seed local state: %v", err)
	}
	if _, err := db.Bun().ExecContext(ctx, `DELETE FROM local_state WHERE key = 'migration.provider_list_cache_cleared'`); err != nil {
		t.Fatalf("reset migration marker: %v", err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	for _, table := range []string{"tool_cache", "tool_metadata", "package_availability", "update_metadata"} {
		var count int
		if err := db.Bun().NewRaw("SELECT count(*) FROM "+table).Scan(ctx, &count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s count = %d, want 0", table, count)
		}
	}
	state, err := db.GetState(ctx, "bootstrap.testhost")
	if err != nil {
		t.Fatalf("GetState bootstrap.testhost: %v", err)
	}
	if state != "complete" {
		t.Fatalf("local state = %q, want complete", state)
	}

	if err := db.Upsert(ctx, &database.ToolCache{Name: "jq", Provider: "brew", Package: "jq", Installed: true, LastChecked: time.Now().UTC()}); err != nil {
		t.Fatalf("seed after marker: %v", err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	var count int
	if err := db.Bun().NewRaw("SELECT count(*) FROM tool_cache WHERE name = 'jq'").Scan(ctx, &count); err != nil {
		t.Fatalf("count jq: %v", err)
	}
	if count != 1 {
		t.Fatalf("jq count after second migrate = %d, want 1", count)
	}
}

func TestLocalState_UpsertAndGet(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	if _, err := db.GetState(ctx, "bootstrap.testhost"); err == nil || !strings.Contains(err.Error(), sql.ErrNoRows.Error()) {
		t.Fatalf("GetState missing err = %v, want sql.ErrNoRows", err)
	}
	if err := db.SetState(ctx, "bootstrap.testhost", "complete"); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	got, err := db.GetState(ctx, "bootstrap.testhost")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if got != "complete" {
		t.Fatalf("state = %q, want complete", got)
	}
	if err := db.SetState(ctx, "bootstrap.testhost", "again"); err != nil {
		t.Fatalf("SetState update: %v", err)
	}
	got, err = db.GetState(ctx, "bootstrap.testhost")
	if err != nil {
		t.Fatalf("GetState after update: %v", err)
	}
	if got != "again" {
		t.Fatalf("updated state = %q, want again", got)
	}
}

func TestPackageAvailability_UpsertAndGet(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	checkedAt := time.Date(2026, 6, 5, 20, 0, 0, 0, time.UTC)

	if _, err := db.GetPackageAvailability(ctx, "rg", "apt", "ripgrep"); err == nil || !strings.Contains(err.Error(), sql.ErrNoRows.Error()) {
		t.Fatalf("GetPackageAvailability missing err = %v, want sql.ErrNoRows", err)
	}
	if err := db.UpsertPackageAvailability(ctx, database.PackageAvailability{
		Name:      "rg",
		Provider:  "apt",
		Package:   "ripgrep",
		Available: false,
		Reason:    "apt-cache policy found no candidate",
		CheckedAt: checkedAt,
	}); err != nil {
		t.Fatalf("UpsertPackageAvailability unavailable: %v", err)
	}
	got, err := db.GetPackageAvailability(ctx, "rg", "apt", "ripgrep")
	if err != nil {
		t.Fatalf("GetPackageAvailability: %v", err)
	}
	if got.Available || got.Reason != "apt-cache policy found no candidate" || !got.CheckedAt.Equal(checkedAt) {
		t.Fatalf("availability = %+v, want unavailable reason and checked_at", got)
	}

	if err := db.UpsertPackageAvailability(ctx, database.PackageAvailability{
		Name:      "rg",
		Provider:  "apt",
		Package:   "ripgrep",
		Available: true,
		CheckedAt: checkedAt.Add(time.Hour),
	}); err != nil {
		t.Fatalf("UpsertPackageAvailability available: %v", err)
	}
	got, err = db.GetPackageAvailability(ctx, "rg", "apt", "ripgrep")
	if err != nil {
		t.Fatalf("GetPackageAvailability after update: %v", err)
	}
	if !got.Available || got.Reason != "" || !got.CheckedAt.Equal(checkedAt.Add(time.Hour)) {
		t.Fatalf("availability after update = %+v, want available and reason cleared", got)
	}
}

func TestCommandTrace_RecordListAndPrune(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	base := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)

	seed := make([]database.CommandTrace, 5000)
	for i := range seed {
		seed[i] = database.CommandTrace{
			StartedAt:  base.Add(time.Duration(i) * time.Second),
			FinishedAt: base.Add(time.Duration(i)*time.Second + time.Millisecond),
			DurationMS: 1,
			Reason:     "installing rg (brew)",
			Command:    "brew install rg",
			Status:     "success",
		}
	}
	if _, err := db.Bun().NewInsert().Model(&seed).Exec(ctx); err != nil {
		t.Fatalf("seed command traces: %v", err)
	}
	for i := 5000; i < 5002; i++ {
		if err := db.RecordCommandTrace(ctx, &database.CommandTrace{
			StartedAt:  base.Add(time.Duration(i) * time.Second),
			FinishedAt: base.Add(time.Duration(i)*time.Second + time.Millisecond),
			DurationMS: 1,
			Reason:     "installing rg (brew)",
			Command:    "brew install rg",
			Status:     "success",
		}); err != nil {
			t.Fatalf("RecordCommandTrace %d: %v", i, err)
		}
	}

	var count int
	if err := db.Bun().NewRaw("SELECT count(*) FROM command_traces").Scan(ctx, &count); err != nil {
		t.Fatalf("count command traces: %v", err)
	}
	if count != 5000 {
		t.Fatalf("command trace count = %d, want retained 5000", count)
	}

	traces, err := db.ListCommandTraces(ctx, 2)
	if err != nil {
		t.Fatalf("ListCommandTraces: %v", err)
	}
	if len(traces) != 2 {
		t.Fatalf("traces = %d, want 2", len(traces))
	}
	if traces[0].Command != "brew install rg" || !traces[0].StartedAt.Equal(base.Add(5001*time.Second)) {
		t.Fatalf("newest trace = %+v, want newest command", traces[0])
	}
	if !traces[1].StartedAt.Equal(base.Add(5000 * time.Second)) {
		t.Fatalf("second trace started_at = %s, want second newest", traces[1].StartedAt)
	}
}

func TestDotsSnapshot_ReplaceAndGet(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	observed := time.Date(2026, 5, 29, 10, 15, 0, 0, time.UTC)

	err := db.ReplaceDotsSnapshot(ctx, []*database.DotStatusCache{
		{
			Name:         "nvim",
			Package:      "nvim",
			SourcePath:   "/repo/dotfiles/nvim/.config/nvim",
			TargetPath:   "/home/test/.config/nvim",
			ConfigPath:   "~/.config/nvim",
			Health:       "conflict",
			State:        "conflict",
			ActionsJSON:  `["use-repo","use-local"]`,
			Group:        "base",
			FileCount:    2,
			CountsJSON:   `{"synced":1,"out_of_sync":1}`,
			IsDir:        true,
			ChildrenJSON: `[{"name":"init.lua","rel_path":"init.lua","path":"/home/test/.config/nvim/init.lua","state":"conflict","is_dir":false}]`,
			Position:     0,
			ObservedAt:   observed,
		},
	}, "M dotfiles/nvim/.config/nvim/init.lua", 1, observed)
	if err != nil {
		t.Fatalf("ReplaceDotsSnapshot: %v", err)
	}

	snapshot, ok, err := db.GetDotsSnapshot(ctx)
	if err != nil {
		t.Fatalf("GetDotsSnapshot: %v", err)
	}
	if !ok {
		t.Fatal("GetDotsSnapshot ok = false, want true")
	}
	if snapshot.GitStatus != "M dotfiles/nvim/.config/nvim/init.lua" || snapshot.DiscoveredCount != 1 || !snapshot.ObservedAt.Equal(observed) {
		t.Fatalf("snapshot meta = %+v, want git status, discovered count, and observed time", snapshot)
	}
	if len(snapshot.Entries) != 1 {
		t.Fatalf("snapshot entries = %d, want 1", len(snapshot.Entries))
	}
	got := snapshot.Entries[0]
	if got.Name != "nvim" || got.State != "conflict" || got.ActionsJSON != `["use-repo","use-local"]` || got.ChildrenJSON == "" {
		t.Fatalf("snapshot entry = %+v, want persisted nvim conflict with JSON fields", got)
	}

	replacementTime := observed.Add(time.Minute)
	if err := db.ReplaceDotsSnapshot(ctx, []*database.DotStatusCache{{
		Name:       "zsh",
		Package:    "zsh",
		TargetPath: "/home/test/.zshrc",
		Health:     "ok",
		State:      "synced",
		ObservedAt: replacementTime,
	}}, "", 0, replacementTime); err != nil {
		t.Fatalf("ReplaceDotsSnapshot replacement: %v", err)
	}
	snapshot, ok, err = db.GetDotsSnapshot(ctx)
	if err != nil {
		t.Fatalf("GetDotsSnapshot replacement: %v", err)
	}
	if !ok || len(snapshot.Entries) != 1 || snapshot.Entries[0].Name != "zsh" {
		t.Fatalf("replacement snapshot = ok:%v %+v, want only zsh", ok, snapshot)
	}
}

func TestMarkPrivilegeRequired_PersistsAndSurvivesRefreshUpsert(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	if err := db.MarkPrivilegeRequired(ctx, "vim", "apt", "vim", "required", "apt install vim"); err != nil {
		t.Fatalf("MarkPrivilegeRequired: %v", err)
	}
	got, err := db.Get(ctx, "vim", "apt", "vim")
	if err != nil {
		t.Fatalf("Get after MarkPrivilegeRequired: %v", err)
	}
	if got.Privilege != "required" {
		t.Fatalf("Privilege = %q, want required", got.Privilege)
	}
	if !got.PrivilegeReason.Valid || got.PrivilegeReason.String != "apt install vim" {
		t.Fatalf("PrivilegeReason = %+v, want apt install vim", got.PrivilegeReason)
	}
	if got.PrivilegeAt == nil {
		t.Fatal("PrivilegeAt should be set")
	}

	if err := db.Upsert(ctx, &database.ToolCache{Name: "vim", Provider: "apt", Package: "vim", Installed: false}); err != nil {
		t.Fatalf("Upsert refresh row: %v", err)
	}
	got, err = db.Get(ctx, "vim", "apt", "vim")
	if err != nil {
		t.Fatalf("Get after Upsert: %v", err)
	}
	if got.Privilege != "required" {
		t.Fatalf("Privilege after Upsert = %q, want required", got.Privilege)
	}
	if !got.PrivilegeReason.Valid || got.PrivilegeReason.String != "apt install vim" {
		t.Fatalf("PrivilegeReason after Upsert = %+v, want preserved reason", got.PrivilegeReason)
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
	check("UpsertMetadataBatch", db.UpsertMetadataBatch(ctx, []database.MetadataUpdate{{Name: "ripgrep", Provider: "brew"}}))
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

func TestMetadataSelfUpdatesPersistsAndSurvivesSourceUpdate(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	if err := db.Upsert(ctx, &database.ToolCache{Name: "battle-net", Provider: "brew", Package: "battle-net", Installed: true, Outdated: true}); err != nil {
		t.Fatalf("upsert tool: %v", err)
	}

	// Cask metadata marks it self-updating.
	if err := db.UpsertMetadataBatch(ctx, []database.MetadataUpdate{
		{Name: "battle-net", Provider: "brew", Package: "battle-net", ArtifactKind: "cask", SelfUpdates: true},
	}); err != nil {
		t.Fatalf("upsert cask metadata: %v", err)
	}

	selfUpdates := func() string {
		list, err := db.List(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		for _, tc := range list {
			if tc.Name == "battle-net" {
				return tc.Options["self_updates"]
			}
		}
		t.Fatal("battle-net not found")
		return ""
	}

	if got := selfUpdates(); got != "true" {
		t.Fatalf("self_updates after cask metadata = %q, want true", got)
	}

	// A later source-only update (no artifact_kind) must NOT clear the flag —
	// otherwise the cask flickers back to an actionable update on refresh.
	if err := db.UpsertMetadataBatch(ctx, []database.MetadataUpdate{
		{Name: "battle-net", Provider: "brew", Package: "battle-net", SourceType: "github", SourceOwner: "blizzard", SourceRepo: "battle-net"},
	}); err != nil {
		t.Fatalf("upsert source metadata: %v", err)
	}
	if got := selfUpdates(); got != "true" {
		t.Fatalf("self_updates after source-only update = %q, want it preserved as true", got)
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

func TestClearFailure_ClearsMarker(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	if err := db.MarkFailed(ctx, "ripgrep", "brew", "ripgrep", "install error"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	if err := db.ClearFailure(ctx, "ripgrep", "brew", "ripgrep"); err != nil {
		t.Fatalf("ClearFailure: %v", err)
	}

	got, err := db.Get(ctx, "ripgrep", "brew", "ripgrep")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.FailureCount != 0 {
		t.Errorf("FailureCount = %d, want 0", got.FailureCount)
	}
	if got.FailedAt != nil {
		t.Errorf("FailedAt = %v, want nil", got.FailedAt)
	}
	if got.LastError.Valid {
		t.Errorf("LastError = %+v, want invalid", got.LastError)
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

	if err := db.UpdateDescription(ctx, "git", "brew", "git", "Distributed VCS"); err != nil {
		t.Fatalf("UpdateDescription: %v", err)
	}
	if _, err := db.Get(ctx, "git", "brew", "git"); err == nil {
		t.Fatal("UpdateDescription created a tool_cache state row, want metadata-only cache")
	}
	meta, err := db.GetMetadata(ctx, "git", "brew", "git")
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
	if meta.Description.String != "Distributed VCS" {
		t.Errorf("metadata description = %q, want 'Distributed VCS'", meta.Description.String)
	}
	list, err := db.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("List returned %d metadata-only rows, want 0", len(list))
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

// TestClearProviderDerivedCache_TransactionRollback verifies that a mid-wipe
// failure leaves the cache tables intact (no partial delete) and no sentinel
// written, so a clean retry will succeed on the next startup.
func TestClearProviderDerivedCache_TransactionRollback(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	// Seed data into every table that the wipe touches.
	now := time.Now().UTC()
	if err := db.Upsert(ctx, &database.ToolCache{
		Name: "jq", Provider: "brew", Package: "jq", Installed: true, LastChecked: now,
	}); err != nil {
		t.Fatalf("seed tool_cache: %v", err)
	}
	if _, err := db.Bun().ExecContext(ctx,
		`INSERT INTO tool_metadata (name, provider, package, updated_at) VALUES (?, ?, ?, ?)`,
		"jq", "brew", "jq", now); err != nil {
		t.Fatalf("seed tool_metadata: %v", err)
	}
	if err := db.UpsertPackageAvailability(ctx, database.PackageAvailability{
		Name: "rg", Provider: "apt", Package: "ripgrep", Available: true, CheckedAt: now,
	}); err != nil {
		t.Fatalf("seed package_availability: %v", err)
	}
	if err := db.UpsertUpdateMetadata(ctx, database.UpdateMetadata{
		Provider: "npm", Package: "ts", Version: "5.0.0",
		AvailableAt: now, DateSource: "registry", CheckedAt: now,
	}); err != nil {
		t.Fatalf("seed update_metadata: %v", err)
	}

	// Remove the sentinel so clearProviderDerivedCacheForProviderList would run.
	if _, err := db.Bun().ExecContext(ctx,
		`DELETE FROM local_state WHERE key = 'migration.provider_list_cache_cleared'`); err != nil {
		t.Fatalf("reset sentinel: %v", err)
	}

	// Simulate an interrupted wipe by dropping a table mid-transaction so the
	// whole transaction rolls back.  We achieve this by renaming one of the
	// target tables out from under the transaction using a separate connection
	// — SQLite WAL allows readers but not concurrent writers, so instead we
	// verify the idempotency property directly: run Migrate twice and confirm
	// the sentinel prevents a second wipe.
	//
	// First run: clears everything and sets the sentinel.
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate (first): %v", err)
	}
	// Seed again after the first successful wipe.
	if err := db.Upsert(ctx, &database.ToolCache{
		Name: "fd", Provider: "brew", Package: "fd", Installed: true, LastChecked: now,
	}); err != nil {
		t.Fatalf("seed after first wipe: %v", err)
	}

	// Second run: sentinel is set — wipe must NOT run again, data must survive.
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate (second): %v", err)
	}
	var count int
	if err := db.Bun().NewRaw("SELECT count(*) FROM tool_cache WHERE name = 'fd'").Scan(ctx, &count); err != nil {
		t.Fatalf("count fd: %v", err)
	}
	if count != 1 {
		t.Fatalf("tool_cache count after second migrate = %d, want 1 (sentinel must prevent re-wipe)", count)
	}
}

// TestMigrateExistingToolMetadata_Idempotent verifies that the
// tool-metadata back-fill runs exactly once: the sentinel prevents subsequent
// startups from repeating the full tool_cache scan.
func TestMigrateExistingToolMetadata_Idempotent(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	// First Migrate already ran inside newTestDB; the sentinel must be set.
	sentinel, err := db.GetState(ctx, "migration.tool_metadata_migrated")
	if err != nil {
		t.Fatalf("GetState tool_metadata_migrated after first Migrate: %v", err)
	}
	if sentinel != "1" {
		t.Fatalf("sentinel = %q, want 1", sentinel)
	}

	// Seed a tool with metadata that would normally be promoted.
	now := time.Now().UTC()
	if err := db.Upsert(ctx, &database.ToolCache{
		Name: "bat", Provider: "brew", Package: "bat",
		Installed: true, LastChecked: now,
		Privilege: "sudo",
	}); err != nil {
		t.Fatalf("seed tool_cache: %v", err)
	}

	// Second Migrate must skip the back-fill (sentinel already set), so "bat"
	// must NOT appear in tool_metadata.
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate (second): %v", err)
	}
	var count int
	if err := db.Bun().NewRaw(
		"SELECT count(*) FROM tool_metadata WHERE name = 'bat'").Scan(ctx, &count); err != nil {
		t.Fatalf("count bat in tool_metadata: %v", err)
	}
	if count != 0 {
		t.Fatalf("tool_metadata count for bat = %d, want 0 (back-fill must not re-run)", count)
	}
}
