package app

import (
	"testing"

	"github.com/lkshrk/omni/internal/database"
)

// Node managers (bun/pnpm/npm) share a global store, so each reports the same
// globally-installed package. Discovery iterates each manager-provider, yielding
// one upsert per manager. collapseSharedStoreDuplicates must reduce these to a
// single row attributed to the ecosystem's effective manager.
func TestCollapseSharedStoreDuplicates_NodeCollapsesToEffective(t *testing.T) {
	effective := map[string]string{"node": "pnpm", "python": "uv"}
	in := []database.DiscoveredUpsert{
		{Name: "@aisuite/chub", Provider: "bun", InstalledWith: "bun", Version: "1.0.0"},
		{Name: "@aisuite/chub", Provider: "npm", InstalledWith: "npm", Version: "1.0.0"},
		{Name: "@aisuite/chub", Provider: "pnpm", InstalledWith: "pnpm", Version: "1.0.0"},
	}

	got := collapseSharedStoreDuplicates(in, effective)

	if len(got) != 1 {
		t.Fatalf("collapseSharedStoreDuplicates returned %d rows, want 1: %+v", len(got), got)
	}
	if got[0].InstalledWith != "pnpm" {
		t.Errorf("InstalledWith = %q, want pnpm (effective node manager)", got[0].InstalledWith)
	}
}

// Python managers (uv/pip) also share PyPI's global store.
func TestCollapseSharedStoreDuplicates_PythonCollapsesToEffective(t *testing.T) {
	effective := map[string]string{"python": "uv"}
	in := []database.DiscoveredUpsert{
		{Name: "black", Provider: "pip", InstalledWith: "pip", Version: "24.0.0"},
		{Name: "black", Provider: "uv", InstalledWith: "uv", Version: "24.0.0"},
	}

	got := collapseSharedStoreDuplicates(in, effective)

	if len(got) != 1 {
		t.Fatalf("returned %d rows, want 1: %+v", len(got), got)
	}
	if got[0].InstalledWith != "uv" {
		t.Errorf("InstalledWith = %q, want uv", got[0].InstalledWith)
	}
}

// System package managers (brew/apt) do NOT share a store — a package installed
// under both is two genuinely separate installs and must not be collapsed.
func TestCollapseSharedStoreDuplicates_SystemNotCollapsed(t *testing.T) {
	effective := map[string]string{}
	in := []database.DiscoveredUpsert{
		{Name: "ripgrep", Provider: "brew", InstalledWith: "brew"},
		{Name: "ripgrep", Provider: "apt", InstalledWith: "apt"},
	}

	got := collapseSharedStoreDuplicates(in, effective)

	if len(got) != 2 {
		t.Fatalf("returned %d rows, want 2 (system not collapsed): %+v", len(got), got)
	}
}

// When the effective manager is not among the reported duplicates, the first
// entry survives rather than dropping the package entirely.
func TestCollapseSharedStoreDuplicates_FallsBackToFirstWhenNoEffective(t *testing.T) {
	effective := map[string]string{"node": "yarn"} // not installed locally
	in := []database.DiscoveredUpsert{
		{Name: "typescript", Provider: "npm", InstalledWith: "npm"},
		{Name: "typescript", Provider: "bun", InstalledWith: "bun"},
	}

	got := collapseSharedStoreDuplicates(in, effective)

	if len(got) != 1 {
		t.Fatalf("returned %d rows, want 1: %+v", len(got), got)
	}
	if got[0].InstalledWith != "npm" {
		t.Errorf("InstalledWith = %q, want npm (first entry)", got[0].InstalledWith)
	}
}

// Distinct packages within the same ecosystem are preserved.
func TestCollapseSharedStoreDuplicates_KeepsDistinctNames(t *testing.T) {
	effective := map[string]string{"node": "pnpm"}
	in := []database.DiscoveredUpsert{
		{Name: "prettier", Provider: "pnpm", InstalledWith: "pnpm"},
		{Name: "eslint", Provider: "pnpm", InstalledWith: "pnpm"},
	}

	got := collapseSharedStoreDuplicates(in, effective)

	if len(got) != 2 {
		t.Fatalf("returned %d rows, want 2 distinct: %+v", len(got), got)
	}
}
