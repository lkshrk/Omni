package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorAcceptsManifestlessSkillEvidenceAndStillReportsExactRepair(t *testing.T) {
	_, _, a, _ := setupManifestlessOwnedFixTest(t)
	result := &DoctorResult{}
	a.doctorAgentsOwnedChildren(result, filepath.Join(os.Getenv("HOME"), ".apm"))
	if len(result.Checks) != 1 {
		t.Fatalf("checks = %#v", result.Checks)
	}
	check := result.Checks[0]
	all := check.Message + " " + strings.Join(check.Details, " ")
	if check.Status != DoctorStatusWarn || !strings.Contains(all, "provided identically") || strings.Contains(all, "unavailable") {
		t.Fatalf("check = %#v", check)
	}
}
