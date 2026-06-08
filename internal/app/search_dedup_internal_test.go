package app

import (
	"testing"

	"github.com/lkshrk/omni/internal/provider"
)

func TestDedupSearchResults_CollapsesNodeEcosystem(t *testing.T) {
	rank := map[string]int{"brew": 0, "bun": 1, "pnpm": 2, "npm": 3, "uv": 4, "pip": 5}
	in := []provider.SearchResult{
		{Name: "typescript", Provider: "npm"},
		{Name: "typescript", Provider: "node"},
		{Name: "typescript", Provider: "pnpm"},
		{Name: "typescript", Provider: "bun"},
	}
	got := dedupSearchResults(in, rank, "")
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(got), got)
	}
	if got[0].Provider != "bun" {
		t.Errorf("provider = %q, want bun (highest-priority node concrete)", got[0].Provider)
	}
}

func TestDedupSearchResults_KeepsDifferentEcosystems(t *testing.T) {
	rank := map[string]int{"brew": 0, "npm": 3}
	in := []provider.SearchResult{
		{Name: "prettier", Provider: "npm"},
		{Name: "prettier", Provider: "brew"},
	}
	got := dedupSearchResults(in, rank, "")
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (distinct ecosystems): %+v", len(got), got)
	}
	if got[0].Provider != "brew" {
		t.Errorf("first = %q, want brew (sorted by rank)", got[0].Provider)
	}
}

func TestDedupSearchResults_RelevanceFirst(t *testing.T) {
	rank := map[string]int{"brew": 0, "npm": 1}
	in := []provider.SearchResult{
		{Name: "ripgrep-all", Provider: "brew"}, // prefix, high-priority provider
		{Name: "ripgrep", Provider: "npm"},      // exact, lower-priority provider
		{Name: "x-ripgrep", Provider: "brew"},   // substring
	}
	got := dedupSearchResults(in, rank, "ripgrep")
	if got[0].Name != "ripgrep" {
		t.Fatalf("first = %q, want exact match ripgrep despite lower provider rank", got[0].Name)
	}
	if got[1].Name != "ripgrep-all" {
		t.Errorf("second = %q, want prefix match ripgrep-all", got[1].Name)
	}
	if got[2].Name != "x-ripgrep" {
		t.Errorf("third = %q, want substring match x-ripgrep", got[2].Name)
	}
}

func TestDedupSearchResults_SortsByRankThenName(t *testing.T) {
	rank := map[string]int{"brew": 0, "npm": 1}
	in := []provider.SearchResult{
		{Name: "zlib", Provider: "npm"},
		{Name: "atool", Provider: "brew"},
		{Name: "btool", Provider: "brew"},
	}
	got := dedupSearchResults(in, rank, "")
	order := make([]string, len(got))
	for i, r := range got {
		order[i] = r.Provider + ":" + r.Name
	}
	want := []string{"brew:atool", "brew:btool", "npm:zlib"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}
