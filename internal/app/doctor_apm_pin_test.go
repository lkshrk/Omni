package app

import (
	"path/filepath"
	"strings"
	"testing"
)

func apmPinCheck(t *testing.T, receipt string) DoctorCheck {
	t.Helper()
	path := filepath.Join(t.TempDir(), "direct_url.json")
	writeFile(t, path, receipt)
	result := &DoctorResult{}
	checkAPMInstallReceipt(result, path)
	if len(result.Checks) != 1 {
		t.Fatalf("checks = %+v", result.Checks)
	}
	return result.Checks[0]
}

func TestDoctorAPMPinAcceptsThePinnedReceipt(t *testing.T) {
	url, ref := parseAPMPackagePin(apmPackagePin)
	check := apmPinCheck(t, `{"url":"`+url+`","vcs_info":{"vcs":"git","commit_id":"`+ref+`","requested_revision":"`+ref+`"}}`)
	if check.ID != "apm-pin" || check.Status != DoctorStatusOK {
		t.Fatalf("check = %+v", check)
	}
}

func TestDoctorAPMPinWarnsOnADifferentCommit(t *testing.T) {
	url, ref := parseAPMPackagePin(apmPackagePin)
	check := apmPinCheck(t, `{"url":"`+url+`","vcs_info":{"vcs":"git","commit_id":"0ff1ce0ff1ce","requested_revision":"main"}}`)
	if check.Status != DoctorStatusWarn {
		t.Fatalf("check = %+v, want warn", check)
	}
	details := strings.Join(check.Details, "\n")
	if !strings.Contains(details, "0ff1ce0ff1ce") || !strings.Contains(details, ref) {
		t.Fatalf("details name neither side: %q", details)
	}
}

func TestDoctorAPMPinWarnsOnADifferentSource(t *testing.T) {
	_, ref := parseAPMPackagePin(apmPackagePin)
	check := apmPinCheck(t, `{"url":"https://github.com/fork/apm.git","vcs_info":{"vcs":"git","commit_id":"`+ref+`"}}`)
	if check.Status != DoctorStatusWarn {
		t.Fatalf("check = %+v, want warn", check)
	}
	if !strings.Contains(strings.Join(check.Details, "\n"), "fork/apm") {
		t.Fatalf("details = %+v", check.Details)
	}
}

func TestDoctorAPMPinWarnsOnAnUnparseableReceipt(t *testing.T) {
	if check := apmPinCheck(t, "not json"); check.Status != DoctorStatusWarn {
		t.Fatalf("check = %+v, want warn", check)
	}
	if check := apmPinCheck(t, `{"url":"https://pypi.org/simple/apm-cli"}`); check.Status != DoctorStatusWarn {
		t.Fatalf("non-VCS receipt = %+v, want warn", check)
	}
}

func TestDoctorAPMPinIsSilentWithoutAReceipt(t *testing.T) {
	result := &DoctorResult{}
	checkAPMInstallReceipt(result, filepath.Join(t.TempDir(), "missing.json"))
	if len(result.Checks) != 0 {
		t.Fatalf("checks = %+v, want none", result.Checks)
	}
}

func TestParseAPMPackagePin(t *testing.T) {
	url, ref := parseAPMPackagePin(apmPackagePin)
	if url != "https://github.com/microsoft/apm.git" || len(ref) != 40 {
		t.Fatalf("url = %q, ref = %q", url, ref)
	}
}
