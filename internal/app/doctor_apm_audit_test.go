package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
)

const apmAuditFixture = `[>] Replaying install (cache-only)...
[!] Drift detected: 2 file(s)
{
  "passed": false,
  "checks": [
    {"name": "lockfile-exists", "passed": true, "message": "Lockfile present", "details": []},
    {"name": "ref-consistency", "passed": false, "message": "1 dependency ref differs from lockfile", "details": ["owner/repo: main != 0ff1ce"]},
    {"name": "deployment-ledger-owners", "passed": true, "message": "All deployment ledger owners are valid", "details": []},
    {"name": "deployed-files-present", "passed": false, "message": "2 deployed files missing -- run 'apm install' to restore", "details": [
      ".claude/skills/present/SKILL.md",
      ".claude/skills/gone/SKILL.md"
    ]},
    {"name": "drift", "passed": false, "message": "drift detected: 2 file(s)", "details": [
      "unintegrated: .agents/skills/deploy-root/SKILL.md",
      "unintegrated: .claude/skills/hand-edited/SKILL.md"
    ]}
  ]
}
`

func newAPMAuditApp(t *testing.T, stdout string, runErr error) (*App, string, *availExecutor) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".apm")
	writeFile(t, filepath.Join(dir, "apm.yml"), "name: test\n")
	writeFile(t, filepath.Join(dir, "apm.lock.yaml"), "dependencies: []\n")
	mock := &availExecutor{available: map[string]bool{"apm": true}}
	mock.Responses = []executor.MockCall{
		{Stdout: "Agent Package Manager (APM) CLI version " + apmVersionPin + "\n"},
		{Stdout: stdout, Err: runErr},
	}
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	a.SetFallbackExecutor(mock)
	return a, home, mock
}

func apmAuditCheck(t *testing.T, result *DoctorResult) DoctorCheck {
	t.Helper()
	for _, check := range result.Checks {
		if check.ID == "apm-audit" {
			return check
		}
	}
	t.Fatalf("no apm-audit check in %+v", result.Checks)
	return DoctorCheck{}
}

func TestDoctorAPMAuditCorrectsVanillaFindings(t *testing.T) {
	a, home, mock := newAPMAuditApp(t, apmAuditFixture, errors.New("exit status 1"))
	writeFile(t, filepath.Join(home, ".claude/skills/present/SKILL.md"), "present\n")

	result := &DoctorResult{}
	a.doctorAPMAudit(context.Background(), result)

	audit := mock.Calls[len(mock.Calls)-1]
	if got := strings.Join(audit.Args, " "); got != "audit --ci --format json" {
		t.Fatalf("audit args = %q", got)
	}
	if audit.Dir != filepath.Join(home, ".apm") {
		t.Fatalf("audit ran in %q, want the global workspace", audit.Dir)
	}

	check := apmAuditCheck(t, result)
	if check.Status != DoctorStatusFail {
		t.Fatalf("status = %q, want fail: %+v", check.Status, check)
	}
	details := strings.Join(append([]string{check.Message}, check.Details...), "\n")
	if !strings.Contains(details, "ref-consistency") {
		t.Fatalf("integrity failure not surfaced: %q", details)
	}
	if !strings.Contains(details, ".claude/skills/hand-edited/SKILL.md") {
		t.Fatalf("real drift not surfaced: %q", details)
	}
	if !strings.Contains(details, ".claude/skills/gone/SKILL.md") {
		t.Fatalf("genuinely missing file not surfaced: %q", details)
	}
	if strings.Contains(details, ".agents/skills/deploy-root/SKILL.md") {
		t.Fatalf("deploy-root drift reported as real: %q", details)
	}
	if strings.Contains(details, ".claude/skills/present/SKILL.md") {
		t.Fatalf("file present under HOME reported as missing: %q", details)
	}
}

func TestDoctorAPMAuditPassesWhenOnlyVanillaQuirksRemain(t *testing.T) {
	fixture := strings.NewReplacer(
		`"name": "ref-consistency", "passed": false, "message": "1 dependency ref differs from lockfile", "details": ["owner/repo: main != 0ff1ce"]`,
		`"name": "ref-consistency", "passed": true, "message": "All dependency refs match lockfile", "details": []`,
		`      "unintegrated: .claude/skills/hand-edited/SKILL.md"`, `      "unintegrated: .agents/skills/other/SKILL.md"`,
		`      ".claude/skills/gone/SKILL.md"`, `      ".claude/skills/also-present/SKILL.md"`,
	).Replace(apmAuditFixture)

	a, home, _ := newAPMAuditApp(t, fixture, errors.New("exit status 1"))
	writeFile(t, filepath.Join(home, ".claude/skills/present/SKILL.md"), "present\n")
	writeFile(t, filepath.Join(home, ".claude/skills/also-present/SKILL.md"), "present\n")

	result := &DoctorResult{}
	a.doctorAPMAudit(context.Background(), result)
	if check := apmAuditCheck(t, result); check.Status != DoctorStatusOK {
		t.Fatalf("check = %+v, want ok", check)
	}
}

func TestDoctorAPMAuditWarnsOnRecoverableFindings(t *testing.T) {
	fixture := strings.Replace(apmAuditFixture,
		`"name": "ref-consistency", "passed": false, "message": "1 dependency ref differs from lockfile", "details": ["owner/repo: main != 0ff1ce"]`,
		`"name": "ref-consistency", "passed": true, "message": "All dependency refs match lockfile", "details": []`, 1)

	a, home, _ := newAPMAuditApp(t, fixture, errors.New("exit status 1"))
	writeFile(t, filepath.Join(home, ".claude/skills/present/SKILL.md"), "present\n")

	result := &DoctorResult{}
	a.doctorAPMAudit(context.Background(), result)
	if check := apmAuditCheck(t, result); check.Status != DoctorStatusWarn {
		t.Fatalf("check = %+v, want warn", check)
	}
}

func TestDoctorAPMAuditSkipsWithoutManifest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	mock := &availExecutor{available: map[string]bool{"apm": true}}
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	a.SetFallbackExecutor(mock)

	result := &DoctorResult{}
	a.doctorAPMAudit(context.Background(), result)
	if check := apmAuditCheck(t, result); check.Status != DoctorStatusOK || !strings.Contains(check.Message, "nothing to audit") {
		t.Fatalf("check = %+v", check)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("apm invoked without a manifest: %+v", mock.Calls)
	}
}

func TestDoctorAPMAuditSkipsWithoutLockfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeFile(t, filepath.Join(home, ".apm", "apm.yml"), "name: live\nversion: 1.0.0\n")
	mock := &availExecutor{available: map[string]bool{"apm": true}}
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	a.SetFallbackExecutor(mock)

	result := &DoctorResult{}
	a.doctorAPMAudit(context.Background(), result)
	if check := apmAuditCheck(t, result); check.Status != DoctorStatusOK || !strings.Contains(check.Message, "nothing to audit") {
		t.Fatalf("check = %+v", check)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("apm invoked without a lockfile: %+v", mock.Calls)
	}
}

func TestDoctorAPMAuditFailsOnUnparseableReport(t *testing.T) {
	a, _, _ := newAPMAuditApp(t, "[>] Replaying install (cache-only)...\n", errors.New("exit status 2"))
	result := &DoctorResult{}
	a.doctorAPMAudit(context.Background(), result)
	if check := apmAuditCheck(t, result); check.Status != DoctorStatusFail {
		t.Fatalf("check = %+v, want fail", check)
	}
}
