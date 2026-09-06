package database_test

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/database"
)

func agentUpdatePackages(updates []database.AgentUpdate) []string {
	packages := make([]string, 0, len(updates))
	for _, update := range updates {
		packages = append(packages, update.Package)
	}
	return packages
}

func TestReplaceAgentUpdates_RoundTripOrderedByPackage(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	if err := db.ReplaceAgentUpdates(ctx, []database.AgentUpdate{
		{Package: "zeta/tool", Current: "1.0.0", Latest: "1.1.0", Source: "git tags"},
		{Package: "acme/tool", Current: "2.0.0", Latest: "2.5.0", Source: "registry"},
	}); err != nil {
		t.Fatalf("ReplaceAgentUpdates: %v", err)
	}

	got, err := db.ListAgentUpdates(ctx)
	if err != nil {
		t.Fatalf("ListAgentUpdates: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("cached updates = %#v, want 2 rows", got)
	}
	if got[0].Package != "acme/tool" || got[1].Package != "zeta/tool" {
		t.Fatalf("packages = %v, want acme/tool before zeta/tool", agentUpdatePackages(got))
	}
	if got[0].Current != "2.0.0" || got[0].Latest != "2.5.0" || got[0].Source != "registry" {
		t.Fatalf("acme/tool = %#v, want current/latest/source preserved", got[0])
	}
	if got[1].Current != "1.0.0" || got[1].Latest != "1.1.0" || got[1].Source != "git tags" {
		t.Fatalf("zeta/tool = %#v, want current/latest/source preserved", got[1])
	}
}

func TestReplaceAgentUpdates_ReplacesRatherThanAppends(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	if err := db.ReplaceAgentUpdates(ctx, []database.AgentUpdate{
		{Package: "acme/first", Latest: "1.0.0"},
		{Package: "acme/second", Latest: "2.0.0"},
	}); err != nil {
		t.Fatalf("ReplaceAgentUpdates first set: %v", err)
	}
	if err := db.ReplaceAgentUpdates(ctx, []database.AgentUpdate{
		{Package: "acme/third", Latest: "3.0.0"},
	}); err != nil {
		t.Fatalf("ReplaceAgentUpdates second set: %v", err)
	}

	got, err := db.ListAgentUpdates(ctx)
	if err != nil {
		t.Fatalf("ListAgentUpdates: %v", err)
	}
	if len(got) != 1 || got[0].Package != "acme/third" || got[0].Latest != "3.0.0" {
		t.Fatalf("cached updates = %v, want only the second set; stale rows would keep offering finished upgrades", agentUpdatePackages(got))
	}
}

func TestReplaceAgentUpdates_NilClearsCache(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	if err := db.ReplaceAgentUpdates(ctx, []database.AgentUpdate{{Package: "acme/tool", Latest: "1.0.0"}}); err != nil {
		t.Fatalf("ReplaceAgentUpdates: %v", err)
	}
	if err := db.ReplaceAgentUpdates(ctx, nil); err != nil {
		t.Fatalf("ReplaceAgentUpdates nil: %v", err)
	}

	got, err := db.ListAgentUpdates(ctx)
	if err != nil {
		t.Fatalf("ListAgentUpdates: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("cached updates = %v, want empty", agentUpdatePackages(got))
	}
}

func TestReplaceAgentUpdates_StampsCheckedAt(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	if err := db.ReplaceAgentUpdates(ctx, []database.AgentUpdate{{Package: "acme/tool", Latest: "1.0.0"}}); err != nil {
		t.Fatalf("ReplaceAgentUpdates: %v", err)
	}

	got, err := db.ListAgentUpdates(ctx)
	if err != nil {
		t.Fatalf("ListAgentUpdates: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("cached updates = %#v, want 1 row", got)
	}
	if got[0].CheckedAt.IsZero() {
		t.Fatal("CheckedAt is zero; the cache cannot tell how stale its answer is")
	}
}

func TestReplaceAgentUpdates_DuplicatePackageFailsAndKeepsPreviousRows(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	if err := db.ReplaceAgentUpdates(ctx, []database.AgentUpdate{{Package: "acme/kept", Latest: "1.0.0"}}); err != nil {
		t.Fatalf("ReplaceAgentUpdates: %v", err)
	}
	err := db.ReplaceAgentUpdates(ctx, []database.AgentUpdate{
		{Package: "acme/dupe", Latest: "1.0.0"},
		{Package: "acme/dupe", Latest: "2.0.0"},
	})
	if err == nil {
		t.Fatal("ReplaceAgentUpdates with a duplicate package succeeded; the unique index is not enforced")
	}

	got, listErr := db.ListAgentUpdates(ctx)
	if listErr != nil {
		t.Fatalf("ListAgentUpdates: %v", listErr)
	}
	if len(got) != 1 || got[0].Package != "acme/kept" {
		t.Fatalf("cached updates = %v, want the failed write rolled back to acme/kept", agentUpdatePackages(got))
	}
}
