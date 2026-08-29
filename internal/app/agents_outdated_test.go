package app

import (
	"testing"

	"github.com/lkshrk/omni/internal/apm"
)

func TestApplyAgentsOutdatedMatchesSourceBeforeSafeNameFallback(t *testing.T) {
	rows := []AgentsPackageRow{
		{Name: "tool", Source: "https://github.com/acme/tool", Version: "1.0.0", Status: AgentsPackageInstalled},
		{Name: "named", Source: "elsewhere/repo", Version: "2.0.0", Status: AgentsPackageInstalled},
		{Name: "wrapper", Source: "./generated/wrapper", LocalPath: "/tmp/wrapper", Version: "local", Status: AgentsPackageInstalled},
	}
	ApplyAgentsOutdated(rows, apm.OutdatedResult{Rows: []apm.OutdatedRow{
		{Package: "acme/tool", Source: "git tags", Current: "1.0.0", Latest: "1.1.0"},
		{Package: "named", Current: "2.0.0", Latest: "2.1.0"},
		{Package: "generated/wrapper", Current: "local", Latest: "remote"},
	}})
	if !rows[0].UpdateAvailable || rows[0].LatestVersion != "1.1.0" || !rows[1].UpdateAvailable || rows[1].LatestVersion != "2.1.0" {
		t.Fatalf("remote rows = %#v", rows[:2])
	}
	if rows[2].UpdateAvailable || rows[2].LatestVersion != "" {
		t.Fatalf("local wrapper decorated: %#v", rows[2])
	}
}

func TestApplyAgentsOutdatedRejectsNonInstalledAndAmbiguousRows(t *testing.T) {
	rows := []AgentsPackageRow{
		{Name: "duplicate", Source: "acme/shared", Status: AgentsPackageInstalled},
		{Name: "duplicate", Source: "acme/shared", Status: AgentsPackageInstalled},
		{Name: "missing", Source: "acme/missing", Status: AgentsPackageMissing},
		{Name: "drifted", Source: "acme/drifted", Status: AgentsPackageDrifted},
		{Name: "orphan", Source: "acme/orphan", Status: AgentsPackageOrphaned},
	}
	ApplyAgentsOutdated(rows, apm.OutdatedResult{Rows: []apm.OutdatedRow{
		{Package: "acme/shared", Latest: "2.0.0"},
		{Package: "duplicate", Latest: "2.0.0"},
		{Package: "acme/missing", Latest: "2.0.0"},
		{Package: "acme/drifted", Latest: "2.0.0"},
		{Package: "acme/orphan", Latest: "2.0.0"},
	}})
	for _, row := range rows {
		if row.UpdateAvailable || row.LatestVersion != "" {
			t.Fatalf("ineligible or ambiguous row decorated: %#v", row)
		}
	}
}
