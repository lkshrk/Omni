package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func TestSkillPackages_GroupBadgeRendered(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = true
	m.width = 120
	m.enabledAgents = []string{"claude"}
	m.skillsRows = []app.SkillPackageRow{
		{Name: "caveman", Source: "github.com/foo/caveman", Groups: []string{"work", "home"}, Installed: true, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled}},
	}

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	if !strings.Contains(out, "[home] [work]") {
		t.Errorf("viewSkillsBody() missing group pills '[home] [work]', got:\n%s", out)
	}
}

func TestSkillPackages_MultiGroupBadgeAndFullDetail(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "beta.local")
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = true
	m.width = 120
	m.enabledAgents = []string{"claude"}
	m.hostInfo = &app.HostInfo{
		Active: "beta",
		Hosts: map[string]config.HostAssignment{
			"beta": {Groups: []string{"work"}},
		},
	}
	m.skillsRows = []app.SkillPackageRow{{
		Name:           "caveman",
		Source:         "github.com/foo/caveman",
		Groups:         []string{"alpha", "beta", "work"},
		Installed:      true,
		PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled},
	}}

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	if !strings.Contains(out, "[beta] [work]") {
		t.Fatalf("skill row missing active-host multi-group pills:\n%s", out)
	}
	if strings.Contains(out, "[alpha") {
		t.Fatalf("skill row exposed an inactive host group in its badge:\n%s", out)
	}
	if !strings.Contains(out, "groups: alpha, beta, work") {
		t.Fatalf("selected skill detail missing full memberships:\n%s", out)
	}
}

// A skills row in two reusable groups (no active host filtering to collapse them) must render both as separate pills, not a single compact badge.
func TestSkillPackages_TwoGroupsShowTwoPills(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = true
	m.width = 120
	m.enabledAgents = []string{"claude"}
	m.skillsRows = []app.SkillPackageRow{
		{Name: "caveman", Source: "github.com/foo/caveman", Groups: []string{"laptop", "work"}, Installed: true, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled}},
	}

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	if !strings.Contains(out, "[laptop]") || !strings.Contains(out, "[work]") {
		t.Fatalf("skills row lacks both group pills:\n%s", out)
	}
}

// When the group column cannot fit all three pills the row collapses to the host pill plus a "+N" count instead of dropping or truncating groups silently.
func TestSkillPackages_ThreeGroupsNarrowWidthCollapsesToHostPlusCount(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "laptop")
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = true
	m.width = 75
	m.enabledAgents = []string{"claude"}
	m.hostInfo = &app.HostInfo{
		Active: "laptop",
		Hosts: map[string]config.HostAssignment{
			"laptop": {Groups: []string{"laptop", "work", "base"}},
		},
	}
	m.skillsRows = []app.SkillPackageRow{
		{Name: "caveman", Source: "github.com/foo/caveman", Groups: []string{"laptop", "work", "base"}, Installed: true, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled}},
	}

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	if !strings.Contains(out, "[laptop] +2") {
		t.Fatalf("skills row missing collapsed host pill with count:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "caveman") && strings.Contains(line, "[work]") {
			t.Fatalf("skills row should collapse rather than show reusable pill in full:\n%s", out)
		}
	}
}

func TestSkillPackages_NoBadgeWhenNoGroups(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = true
	m.width = 120
	m.skillsRows = []app.SkillPackageRow{
		{Name: "review", Source: "github.com/foo/review", Groups: nil, Installed: true},
	}

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	lines := strings.Split(out, "\n")
	for _, l := range lines {
		if !strings.Contains(l, "review") {
			continue
		}
		after := l[strings.Index(l, "review"):]
		for i, c := range after {
			if c == '[' && i+1 < len(after) {
				next := rune(after[i+1])
				if (next >= 'a' && next <= 'z') || (next >= 'A' && next <= 'Z') {
					t.Errorf("row for 'review' (no groups) appears to contain a group badge: %q", l)
					break
				}
			}
		}
	}
}

func TestSkillPackages_StatusGroupingSections(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = true
	m.width = 120
	m.enabledAgents = []string{"claude"}
	m.skillsRows = []app.SkillPackageRow{
		{Name: "installed-pkg", Source: "github.com/a/installed", Installed: true, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled}},
		{Name: "missing-pkg", Source: "github.com/b/missing", Installed: false, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusMissing}},
	}

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	installedIdx := strings.Index(out, "Installed")
	outOfSyncIdx := strings.Index(out, "Out of Sync")
	if installedIdx < 0 {
		t.Fatalf("viewSkillsBody() missing 'Installed' section header, got:\n%s", out)
	}
	if outOfSyncIdx < 0 {
		t.Fatalf("viewSkillsBody() missing 'Out of Sync' section header, got:\n%s", out)
	}

	installedRowIdx := strings.Index(out, "installed-pkg")
	missingRowIdx := strings.Index(out, "missing-pkg")
	if installedRowIdx < 0 {
		t.Fatalf("viewSkillsBody() missing row 'installed-pkg', got:\n%s", out)
	}
	if missingRowIdx < 0 {
		t.Fatalf("viewSkillsBody() missing row 'missing-pkg', got:\n%s", out)
	}
	// Section order is Out of Sync before Installed, so missing-pkg renders before the Installed header and installed-pkg after it.
	if missingRowIdx >= installedIdx {
		t.Errorf("missing-pkg (idx %d) should render under Out of Sync, before the Installed header (idx %d)", missingRowIdx, installedIdx)
	}
	if installedRowIdx <= installedIdx {
		t.Errorf("installed-pkg (idx %d) should render under the Installed section header (idx %d)", installedRowIdx, installedIdx)
	}
}

func TestSkillPackages_FooterHasGroupKey(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = true
	m.width = 120
	m.enabledAgents = []string{"claude"}
	m.skillsRows = []app.SkillPackageRow{
		{Name: "caveman", Source: "github.com/foo/caveman", Installed: true, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled}},
	}
	m.skillsCursor = 0

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	// Hint format is key then desc with no brackets (e.g. "g group"), rendered under the selected row rather than in a static footer.
	if !strings.Contains(out, "g group") {
		t.Errorf("viewSkillsBody() missing per-row hint 'g group' for selected row, got:\n%s", out)
	}
}

func TestSkillPackages_GKeyOpensGroupMembershipPicker(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills
	m.agentsEnabled = true
	m.skillsRows = []app.SkillPackageRow{
		{Name: "caveman", Source: "github.com/foo/caveman", Groups: []string{"work"}, Installed: true},
	}
	m.skillsCursor = 0

	m = drive(m, pressRune('g'))

	if m.mode != viewGroupMembership {
		t.Fatalf("mode = %v, want viewGroupMembership after pressing 'g'", m.mode)
	}
	if m.pickerMembershipKind != pickerMembershipSkill {
		t.Errorf("pickerMembershipKind = %q, want %q", m.pickerMembershipKind, pickerMembershipSkill)
	}
	if m.pickerMembershipName != "github.com/foo/caveman" {
		t.Errorf("pickerMembershipName = %q, want %q", m.pickerMembershipName, "github.com/foo/caveman")
	}
}

func TestAgentsNav_RoundRobin(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills
	m.agentsEnabled = true
	m.cursorHidden = false
	m.skillsRows = []app.SkillPackageRow{
		{Name: "github.com/a/one", Source: "github.com/a/one", Installed: true},
		{Name: "github.com/b/two", Source: "github.com/b/two", Installed: true},
		{Name: "github.com/c/three", Source: "github.com/c/three", Installed: true},
	}
	m.skillsCursor = 0

	m = drive(m, tea.KeyPressMsg{Code: tea.KeyUp})
	if m.skillsCursor != 2 {
		t.Fatalf("up from 0: skillsCursor = %d, want 2", m.skillsCursor)
	}

	m = drive(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.skillsCursor != 0 {
		t.Fatalf("down from 2: skillsCursor = %d, want 0", m.skillsCursor)
	}

	m = drive(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.skillsCursor != 1 {
		t.Errorf("down from 0: skillsCursor = %d, want 1", m.skillsCursor)
	}
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.skillsCursor != 2 {
		t.Errorf("down from 1: skillsCursor = %d, want 2", m.skillsCursor)
	}
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.skillsCursor != 0 {
		t.Errorf("down from 2: skillsCursor = %d, want 0 (wrap)", m.skillsCursor)
	}
}

func TestSkillPackages_CursorMovesDown(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills
	m.agentsEnabled = true
	m.skillsRows = []app.SkillPackageRow{
		{Name: "a", Source: "github.com/a/a", Installed: true},
		{Name: "b", Source: "github.com/b/b", Installed: true},
		{Name: "c", Source: "github.com/c/c", Installed: true},
	}
	m.skillsCursor = 0

	m = drive(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.skillsCursor != 1 {
		t.Errorf("skillsCursor = %d, want 1 after down arrow", m.skillsCursor)
	}

	m = drive(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.skillsCursor != 2 {
		t.Errorf("skillsCursor = %d, want 2 after second down arrow", m.skillsCursor)
	}
}

func TestSkillPackages_CursorWrapsAtBottom(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills
	m.agentsEnabled = true
	m.cursorHidden = false
	m.skillsRows = []app.SkillPackageRow{
		{Name: "a", Source: "github.com/a/a", Installed: true},
		{Name: "b", Source: "github.com/b/b", Installed: true},
	}
	m.skillsCursor = 1

	m = drive(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.skillsCursor != 0 {
		t.Errorf("skillsCursor = %d, want 0 (wrapped) after down past last row", m.skillsCursor)
	}
}

func TestSkillPackages_CursorMovesUp(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills
	m.agentsEnabled = true
	m.skillsRows = []app.SkillPackageRow{
		{Name: "a", Source: "github.com/a/a", Installed: true},
		{Name: "b", Source: "github.com/b/b", Installed: true},
	}
	m.skillsCursor = 1

	m = drive(m, tea.KeyPressMsg{Code: tea.KeyUp})
	if m.skillsCursor != 0 {
		t.Errorf("skillsCursor = %d, want 0 after up arrow", m.skillsCursor)
	}
}

func TestSkillPackages_CursorWrapsAtTop(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills
	m.agentsEnabled = true
	m.cursorHidden = false
	m.skillsRows = []app.SkillPackageRow{
		{Name: "a", Source: "github.com/a/a", Installed: true},
		{Name: "b", Source: "github.com/b/b", Installed: true},
	}
	m.skillsCursor = 0

	m = drive(m, tea.KeyPressMsg{Code: tea.KeyUp})
	if m.skillsCursor != 1 {
		t.Errorf("skillsCursor = %d, want 1 (wrapped) after up past first row", m.skillsCursor)
	}
}

func TestSkillPackages_DriftedRowRendersDistinctlyFromMissing(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = true
	m.width = 120
	m.enabledAgents = []string{"claude"}
	m.skillsRows = []app.SkillPackageRow{
		{
			Name: "drifted-pkg", Source: "github.com/a/drifted", Installed: false,
			PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusDrifted},
		},
		{
			Name: "missing-pkg", Source: "github.com/b/missing", Installed: false,
			PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusMissing},
		},
	}

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	if !strings.Contains(out, "drifted") {
		t.Errorf("viewSkillsBody() missing the drifted marker, got:\n%s", out)
	}
	if !strings.Contains(out, "missing") {
		t.Errorf("viewSkillsBody() missing the missing marker, got:\n%s", out)
	}

	rows := agentsAllRowsList(m)
	byName := map[string]agentsAllRow{}
	for _, r := range rows {
		byName[r.sortName] = r
	}
	if got := byName["drifted-pkg"]; got.status != agentsStatusOutOfSync || got.mark != agentsMarkDrifted {
		t.Errorf("drifted row = (%v, %v), want (agentsStatusOutOfSync, agentsMarkDrifted)", got.status, got.mark)
	}
	if got := byName["missing-pkg"]; got.mark != agentsMarkMissing {
		t.Errorf("missing row mark = %v, want agentsMarkMissing", got.mark)
	}
}

func TestSkillPackages_DriftedAgentsListedInRowDetail(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = true
	m.width = 120
	m.enabledAgents = []string{"claude", "codex"}
	m.skillsRows = []app.SkillPackageRow{{
		Name: "mixed-pkg", Source: "github.com/a/mixed", Skills: []string{"demo"},
		PerAgentStatus: map[string]app.SkillStatus{
			"claude": app.SkillStatusInstalled,
			"codex":  app.SkillStatusDrifted,
		},
	}}

	rows := agentsAllRowsList(m)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	var detail string
	for _, line := range agentsRowDetailLines(m, rows[0]) {
		detail += stripANSIEscapeSequences(line) + "\n"
	}
	if !strings.Contains(detail, "linked: claude") {
		t.Errorf("detail lines missing the linked agent, got:\n%s", detail)
	}
	if !strings.Contains(detail, "drifted: codex") {
		t.Errorf("detail lines missing the drifted agent, got:\n%s", detail)
	}
}
