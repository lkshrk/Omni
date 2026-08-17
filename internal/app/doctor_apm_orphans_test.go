package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

const orphanTestManifest = "name: omni-host\nversion: 0.0.0\n"

func orphanTestConfig(agents ...string) *config.RootConfig {
	return &config.RootConfig{Settings: config.Settings{AgentsUse: agents}}
}

func TestDoctorAPMOrphansReportsUnaccountedDeployedPaths(t *testing.T) {
	a, home := newLedgerApp(t)
	writeLedgerFile(t, filepath.Join(home, ".apm", "apm.yml"), orphanTestManifest)
	writeLedgerFile(t, filepath.Join(home, ".apm", "apm.lock.yaml"), strings.Join([]string{
		"lockfile_version: '2'",
		"dependencies:",
		"  - repo_url: acme/tracked",
		"    deployed_files:",
		"      - .claude/skills/tracked/SKILL.md",
		"deployments:",
		"  - kind: file",
		"    value: .agents/tracked.md",
		"local_deployed_files:",
		"  - .claude/commands/local.md",
		"",
	}, "\n"))
	writeLedgerFile(t, filepath.Join(home, ".claude", "skills", "tracked", "SKILL.md"), "tracked\n")
	writeLedgerFile(t, filepath.Join(home, ".agents", "tracked.md"), "tracked\n")
	writeLedgerFile(t, filepath.Join(home, ".claude", "commands", "local.md"), "tracked\n")
	orphan := filepath.Join(home, ".claude", "skills", "stray")
	writeLedgerFile(t, filepath.Join(orphan, "SKILL.md"), "orphaned\n")

	result := &DoctorResult{}
	a.doctorAPMOrphans(result, orphanTestConfig("claude-code"))
	check := result.Checks[0]
	if check.Status != DoctorStatusWarn {
		t.Fatalf("check = %+v", check)
	}
	details := strings.Join(check.Details, "\n")
	if !strings.Contains(details, orphan) {
		t.Fatalf("details %q lack the orphan path", details)
	}
	for _, tracked := range []string{"tracked", "local.md"} {
		if strings.Contains(details, filepath.Join(home, ".claude", "skills", tracked)) {
			t.Fatalf("details %q report an accounted path", details)
		}
	}
}

func TestDoctorAPMOrphansPassesWhenEverythingIsAccounted(t *testing.T) {
	a, home := newLedgerApp(t)
	writeLedgerFile(t, filepath.Join(home, ".apm", "apm.yml"), orphanTestManifest)
	writeLedgerFile(t, filepath.Join(home, ".apm", "apm.lock.yaml"),
		"dependencies:\n  - deployed_files:\n      - .claude/skills/tracked/\n")
	writeLedgerFile(t, filepath.Join(home, ".claude", "skills", "tracked", "SKILL.md"), "tracked\n")

	result := &DoctorResult{}
	a.doctorAPMOrphans(result, orphanTestConfig("claude-code"))
	if check := result.Checks[0]; check.Status != DoctorStatusOK {
		t.Fatalf("check = %+v", check)
	}
}

func TestDoctorAPMOrphansReportsUnlockedClaudeMcpServers(t *testing.T) {
	a, home := newLedgerApp(t)
	writeLedgerFile(t, filepath.Join(home, ".apm", "apm.yml"), orphanTestManifest)
	writeLedgerFile(t, filepath.Join(home, ".apm", "apm.lock.yaml"),
		"mcp_target_servers:\n  claude:\n    - tracked\n")
	writeLedgerFile(t, filepath.Join(home, ".claude.json"),
		`{"mcpServers":{"tracked":{"url":"https://a"},"stray":{"url":"https://b"}}}`)

	result := &DoctorResult{}
	a.doctorAPMOrphans(result, orphanTestConfig("claude-code"))
	check := result.Checks[0]
	details := strings.Join(check.Details, "\n")
	if check.Status != DoctorStatusWarn || !strings.Contains(details, "stray") {
		t.Fatalf("check = %+v", check)
	}
	if strings.Contains(details, "tracked") {
		t.Fatalf("details %q report a locked server", details)
	}
}

// Claude's config only describes APM state when APM drives claude; on a host that selects something else the
// servers there belong to whatever owns that target, and reporting them is a finding no omni verb can clear.
func TestDoctorAPMOrphansIgnoresClaudeMcpWhenClaudeIsNotAnAPMTarget(t *testing.T) {
	a, home := newLedgerApp(t)
	writeLedgerFile(t, filepath.Join(home, ".apm", "apm.yml"), orphanTestManifest)
	writeLedgerFile(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"stray":{"url":"https://b"}}}`)

	result := &DoctorResult{}
	a.doctorAPMOrphans(result, orphanTestConfig("codex"))
	if check := result.Checks[0]; strings.Contains(strings.Join(check.Details, "\n"), "stray") {
		t.Fatalf("check = %+v, want claude's servers left out", check)
	}
}

func TestDoctorAPMOrphansInspectsANonClaudeTargetsRoots(t *testing.T) {
	a, home := newLedgerApp(t)
	writeLedgerFile(t, filepath.Join(home, ".apm", "apm.yml"), orphanTestManifest)
	writeLedgerFile(t, filepath.Join(home, ".apm", "apm.lock.yaml"),
		"dependencies:\n  - deployed_files:\n      - .agents/tracked.md\n")
	writeLedgerFile(t, filepath.Join(home, ".agents", "tracked.md"), "tracked\n")
	orphan := filepath.Join(home, ".agents", "skills", "stray")
	writeLedgerFile(t, filepath.Join(orphan, "SKILL.md"), "orphaned\n")
	writeLedgerFile(t, filepath.Join(home, ".claude", "skills", "elsewhere", "SKILL.md"), "not codex's\n")

	result := &DoctorResult{}
	a.doctorAPMOrphans(result, orphanTestConfig("codex"))
	details := strings.Join(result.Checks[0].Details, "\n")
	if !strings.Contains(details, filepath.Join(home, ".agents", "skills")) {
		t.Fatalf("details %q lack codex's shared skills root", details)
	}
	if strings.Contains(details, "elsewhere") {
		t.Fatalf("details %q report a root no selected target deploys into", details)
	}
}

// A root APM never deployed into is hand-maintained, and every file in it would be reported as an orphan forever.
func TestDoctorAPMOrphansSkipsRootsWithNoAPMState(t *testing.T) {
	a, home := newLedgerApp(t)
	writeLedgerFile(t, filepath.Join(home, ".apm", "apm.yml"), orphanTestManifest)
	writeLedgerFile(t, filepath.Join(home, ".apm", "apm.lock.yaml"),
		"dependencies:\n  - deployed_files:\n      - .claude/skills/tracked/SKILL.md\n")
	writeLedgerFile(t, filepath.Join(home, ".claude", "skills", "tracked", "SKILL.md"), "tracked\n")
	writeLedgerFile(t, filepath.Join(home, ".claude", "agents", "handwritten.md"), "mine\n")

	result := &DoctorResult{}
	a.doctorAPMOrphans(result, orphanTestConfig("claude-code"))
	if check := result.Checks[0]; check.Status != DoctorStatusOK {
		t.Fatalf("check = %+v, want the hand-maintained root left out", check)
	}
}

func TestDoctorAPMOrphansSkippedWithoutAManifest(t *testing.T) {
	a, home := newLedgerApp(t)
	writeLedgerFile(t, filepath.Join(home, ".claude", "skills", "stray", "SKILL.md"), "orphaned\n")

	result := &DoctorResult{}
	a.doctorAPMOrphans(result, orphanTestConfig("claude-code"))
	if len(result.Checks) != 0 {
		t.Fatalf("checks = %+v", result.Checks)
	}
}

func TestDoctorRunsTheAPMOrphanCheck(t *testing.T) {
	a, _, _ := newSyncApp(t, &config.RootConfig{Version: config.CurrentVersion}, emptySyncManifest)
	result, err := a.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var seen bool
	for _, check := range result.Checks {
		if check.ID == "apm-orphans" {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("checks = %+v", result.Checks)
	}
}
