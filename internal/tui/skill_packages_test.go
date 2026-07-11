package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
)

func TestSkillPackages_GroupBadgeRendered(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = true
	m.width = 120
	m.enabledAgents = []string{"claude"}
	m.skillsRows = []app.SkillPackageRow{
		{Name: "caveman", Source: "github.com/foo/caveman", Groups: []string{"work", "home"}, Installed: true, PerAgentStatus: map[string]bool{"claude": true}},
	}

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	if !strings.Contains(out, "[work,home]") {
		t.Errorf("viewSkillsBody() missing group badge '[work,home]', got:\n%s", out)
	}
}

func TestSkillPackages_NoBadgeWhenNoGroups(t *testing.T) {
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
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = true
	m.width = 120
	m.enabledAgents = []string{"claude"}
	m.skillsRows = []app.SkillPackageRow{
		{Name: "installed-pkg", Source: "github.com/a/installed", Installed: true, PerAgentStatus: map[string]bool{"claude": true}},
		{Name: "missing-pkg", Source: "github.com/b/missing", Installed: false, PerAgentStatus: map[string]bool{"claude": false}},
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
	// Section order is Out of Sync < Installed, so missing-pkg (out of sync)
	// renders before the Installed header, and installed-pkg renders after it.
	if missingRowIdx >= installedIdx {
		t.Errorf("missing-pkg (idx %d) should render under Out of Sync, before the Installed header (idx %d)", missingRowIdx, installedIdx)
	}
	if installedRowIdx <= installedIdx {
		t.Errorf("installed-pkg (idx %d) should render under the Installed section header (idx %d)", installedRowIdx, installedIdx)
	}
}

func TestSkillPackages_FooterHasGroupKey(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = true
	m.width = 120
	m.enabledAgents = []string{"claude"}
	m.skillsRows = []app.SkillPackageRow{
		{Name: "caveman", Source: "github.com/foo/caveman", Installed: true, PerAgentStatus: map[string]bool{"claude": true}},
	}
	m.skillsCursor = 0

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	// Per-row hints render under the selected row (not a static footer).
	// Hint format: key then desc, no brackets (e.g. "g group").
	if !strings.Contains(out, "g group") {
		t.Errorf("viewSkillsBody() missing per-row hint 'g group' for selected row, got:\n%s", out)
	}
}

func TestSkillPackages_GKeyOpensGroupMembershipPicker(t *testing.T) {
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

	// Up from 0 wraps to last (2).
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyUp})
	if m.skillsCursor != 2 {
		t.Fatalf("up from 0: skillsCursor = %d, want 2", m.skillsCursor)
	}

	// Down from 2 wraps back to 0.
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.skillsCursor != 0 {
		t.Fatalf("down from 2: skillsCursor = %d, want 0", m.skillsCursor)
	}

	// Sequential down: 0 → 1 → 2 → 0.
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
