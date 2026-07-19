package tui

import (
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
)

// TestAgentsHarden_NoSelectionWhenCursorHidden verifies that viewSkillsBody does
// not render any selected-row marker when cursorHidden is true, and that it does
// render the marker after cursorHidden is cleared.
func TestAgentsHarden_NoSelectionWhenCursorHidden(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.width = 120
	m.enabledAgents = []string{"claude"}
	m.skillsRows = []app.SkillPackageRow{
		{Name: "caveman", Source: "github.com/foo/caveman", Installed: true, PerAgentStatus: map[string]bool{"claude": true}},
		{Name: "review", Source: "github.com/bar/review", Installed: true, PerAgentStatus: map[string]bool{"claude": true}},
	}
	m.cursorHidden = true
	m.skillsCursor = 0

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " "), "> ") || line == "> " {
			t.Errorf("cursorHidden=true but found selected marker on line: %q", line)
		}
	}

	m.cursorHidden = false
	outVisible := stripANSIEscapeSequences(m.viewSkillsBody())
	found := false
	for _, line := range strings.Split(outVisible, "\n") {
		if strings.HasPrefix(line, "> ") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("cursorHidden=false but no line starts with '> ' (selected marker) in:\n%s", outVisible)
	}
}

// TestAgentsHarden_SearchBoxAbovePills verifies that when skillsSearchActive is
// true the search control line ("/ ...") appears above the pill bar line that
// contains "skills".
func TestAgentsHarden_SearchBoxAbovePills(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.width = 120
	m.skillsRows = []app.SkillPackageRow{
		{Name: "caveman", Source: "github.com/foo/caveman", Installed: true},
	}
	m.skillsSearchActive = true
	m.filter.Focus()

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	lines := strings.Split(out, "\n")

	searchIdx := -1
	pillIdx := -1
	for i, line := range lines {
		if searchIdx < 0 && strings.Contains(line, "/") && !strings.Contains(line, "skills") {
			searchIdx = i
		}
		if pillIdx < 0 && strings.Contains(line, "skills") && strings.Contains(line, "mcp") {
			pillIdx = i
		}
	}

	if searchIdx < 0 {
		t.Fatalf("search control line ('/ ...') not found in:\n%s", out)
	}
	if pillIdx < 0 {
		t.Fatalf("pill bar line ('skills'/'mcp') not found in:\n%s", out)
	}
	if searchIdx >= pillIdx {
		t.Errorf("search line (idx %d) should appear before pill bar line (idx %d)", searchIdx, pillIdx)
	}
}

// TestAgentsHarden_FindSetsSearching verifies that submitting a free-text query
// via Enter sets m.searching=true, and that receiving skillsFoundMsg clears it.
func TestAgentsHarden_FindSetsSearching(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills

	// Open search and set a free-text query (not a skill source).
	m = drive(m, pressRune('/'))
	m.filter.SetValue("typescript")

	// Press Enter to submit — routes through handleSkillsSearchKeyMsg because
	// filter is focused and skillsSearchActive is true.
	m = drive(m, pressEnter())

	if !m.searching {
		t.Error("m.searching should be true after submitting a free-text query")
	}

	// Receiving skillsFoundMsg should clear searching.
	m = drive(m, skillsFoundMsg{results: []app.FindResult{
		{Source: "owner/ts-skill", Skill: "ts-skill", Installs: "10 installs"},
	}})

	if m.searching {
		t.Error("m.searching should be false after skillsFoundMsg")
	}
	if len(m.skillFindResults) != 1 {
		t.Errorf("skillFindResults len = %d, want 1", len(m.skillFindResults))
	}
}
