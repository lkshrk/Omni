package app

import (
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

func TestDiscoveryScopeInstallSpecs_HostOverrideBeatsProviders(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "topaz.local")
	spec := config.ToolSpec{
		Providers: []config.ToolInstallSpec{{Provider: "pnpm"}, {Provider: "npm"}},
		Hosts:     map[string]config.ToolInstallSpec{"topaz": {Provider: "bun"}},
	}

	got := discoveryScopeInstallSpecs(spec)
	if len(got) != 1 || got[0].Provider != "bun" {
		t.Fatalf("discovery specs = %+v, want host-pinned bun", got)
	}
}

func TestCollapseSharedStoreDuplicates_NodeCollapsesToEffective(t *testing.T) {
	t.Parallel()
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

func TestCollapseSharedStoreDuplicates_PythonCollapsesToEffective(t *testing.T) {
	t.Parallel()
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

func TestCollapseSharedStoreDuplicates_SystemNotCollapsed(t *testing.T) {
	t.Parallel()
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

func TestCollapseSharedStoreDuplicates_FallsBackToFirstWhenNoEffective(t *testing.T) {
	t.Parallel()
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

func TestCollapseSharedStoreDuplicates_KeepsDistinctNames(t *testing.T) {
	t.Parallel()
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
