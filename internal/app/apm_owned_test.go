package app

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lkshrk/omni/internal/apm"
)

func ownedSidecarPath(t *testing.T) string {
	t.Helper()
	manifestPath := filepath.Join(t.TempDir(), ".apm", "apm.yml")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o700); err != nil {
		t.Fatal(err)
	}
	return manifestPath
}

// A record written before the split lists one set per surface, and every entry in it is one an install applied.
func TestReadAPMOwnedIdentitiesReadsThePreSplitRecordAsApplied(t *testing.T) {
	manifestPath := ownedSidecarPath(t)
	if err := os.WriteFile(apmOwnedSidecarPath(manifestPath),
		[]byte(`{"packages":["acme/one"],"mcp":["linear"]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	owned := readAPMOwnedIdentities(manifestPath)
	want := apmSurfaceIdentities{Packages: []string{"acme/one"}, Mcp: []string{"linear"}}
	if !reflect.DeepEqual(owned.Applied, want) {
		t.Fatalf("applied = %+v, want the pre-split record", owned.Applied)
	}
	if !reflect.DeepEqual(owned.Rendered, want) {
		t.Fatalf("rendered = %+v, want the pre-split record seeded into it too", owned.Rendered)
	}
}

// The halves are advanced by different steps, so writing one must leave the other where the other step put it.
func TestAdvanceAPMOwnedKeepsTheRenderedAndAppliedHalvesApart(t *testing.T) {
	manifestPath := ownedSidecarPath(t)
	if err := advanceAPMRendered(manifestPath,
		apmSurfaceIdentities{Packages: []string{"acme/rendered"}, Mcp: []string{"linear"}}, true, true); err != nil {
		t.Fatal(err)
	}
	if err := advanceAPMApplied(manifestPath, apm.SurfacePackages, []string{"acme/applied"}); err != nil {
		t.Fatal(err)
	}

	owned := readAPMOwnedIdentities(manifestPath)
	if !reflect.DeepEqual(owned.Rendered.Packages, []string{"acme/rendered"}) {
		t.Fatalf("rendered packages = %v, want the manifest write's record kept", owned.Rendered.Packages)
	}
	if !reflect.DeepEqual(owned.Applied.Packages, []string{"acme/applied"}) {
		t.Fatalf("applied packages = %v, want the install's record kept", owned.Applied.Packages)
	}
	if len(owned.Applied.Mcp) != 0 {
		t.Fatalf("applied mcp = %v, want the surface whose install never ran left empty", owned.Applied.Mcp)
	}
	if !reflect.DeepEqual(owned.ownedPackages(), []string{"acme/rendered", "acme/applied"}) {
		t.Fatalf("owned packages = %v, want both halves classified as omni's", owned.ownedPackages())
	}
}
