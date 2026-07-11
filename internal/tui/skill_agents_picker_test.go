package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
)

// pressSpace returns a space key press (the Toggle binding).
func pressSpace() tea.Msg { return tea.KeyPressMsg{Code: ' ', Text: " "} }

func skillsModelWithRows(rows []app.SkillPackageRow) Model {
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills
	m.agentsEnabled = true
	m.width = 120
	m.enabledAgents = []string{"claude"}
	m.skillsRows = rows
	m.cursorHidden = false
	return m
}

// TestSkillAgents_OpenPickerNilApp verifies pressing 'a' on a local installed
// row with a nil app does not panic and does open the picker (openSkillAgentsPicker
// guards nil app for the AgentPickerRows call, but still sets skillAgentsPicker=true).
func TestSkillAgents_OpenPickerNilApp(t *testing.T) {
	m := skillsModelWithRows([]app.SkillPackageRow{
		{Source: "github.com/foo/caveman", Name: "caveman", Installed: true, Agents: []string{"codex"}},
	})
	m.skillsCursor = 0

	m = drive(m, pressRune('a'))

	// openSkillAgentsPicker always sets skillAgentsPicker=true regardless of nil app.
	if !m.skillAgentsPicker {
		t.Error("skillAgentsPicker should be true after pressing 'a' on a local row")
	}
}

// TestSkillAgents_PopupRenderChecks verifies renderSkillAgentsPicker shows
// [x]/[ ] marks and the toggle/save/cancel footer hints.
func TestSkillAgents_PopupRenderChecks(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = true
	m.width = 120
	m.skillAgentsPicker = true
	m.skillAgentsSource = "github.com/foo/pkg"
	m.skillAgentsRows = []app.SkillAgentRow{
		{ID: "codex", Display: "Codex", Targeted: true},
		{ID: "cursor", Display: "Cursor", Targeted: false},
	}
	m.skillAgentsCursor = 0

	out := stripANSIEscapeSequences(renderSkillAgentsPicker(m))

	if !strings.Contains(out, "[x]") {
		t.Errorf("renderSkillAgentsPicker: expected '[x]' for enabled row, got:\n%s", out)
	}
	if !strings.Contains(out, "[ ]") {
		t.Errorf("renderSkillAgentsPicker: expected '[ ]' for disabled row, got:\n%s", out)
	}
	if !strings.Contains(out, "Codex") {
		t.Errorf("renderSkillAgentsPicker: expected 'Codex', got:\n%s", out)
	}
	if !strings.Contains(out, "Cursor") {
		t.Errorf("renderSkillAgentsPicker: expected 'Cursor', got:\n%s", out)
	}
	if !strings.Contains(out, "toggle") {
		t.Errorf("renderSkillAgentsPicker: expected 'toggle' in footer, got:\n%s", out)
	}
	if !strings.Contains(out, "save") {
		t.Errorf("renderSkillAgentsPicker: expected 'save' in footer, got:\n%s", out)
	}
	if !strings.Contains(out, "cancel") {
		t.Errorf("renderSkillAgentsPicker: expected 'cancel' in footer, got:\n%s", out)
	}
}

// TestSkillAgents_SpaceToggles verifies space toggles the Targeted flag on the
// cursor row.
func TestSkillAgents_SpaceToggles(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = true
	m.skillAgentsPicker = true
	m.skillAgentsRows = []app.SkillAgentRow{
		{ID: "codex", Display: "Codex", Targeted: false},
		{ID: "cursor", Display: "Cursor", Targeted: false},
	}
	m.skillAgentsCursor = 0

	m = drive(m, pressSpace())

	if !m.skillAgentsRows[0].Targeted {
		t.Error("skillAgentsRows[0].Targeted should be true after space")
	}
	if m.skillAgentsRows[1].Targeted {
		t.Error("skillAgentsRows[1].Targeted should remain false")
	}
}

// TestSkillAgents_UpDownMoveCursor verifies up/down move the cursor and clamp.
func TestSkillAgents_UpDownMoveCursor(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = true
	m.skillAgentsPicker = true
	m.skillAgentsRows = []app.SkillAgentRow{
		{ID: "codex", Display: "Codex"},
		{ID: "cursor", Display: "Cursor"},
		{ID: "claude", Display: "Claude Code"},
	}
	m.skillAgentsCursor = 0

	m = drive(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.skillAgentsCursor != 1 {
		t.Errorf("cursor after down = %d, want 1", m.skillAgentsCursor)
	}

	m = drive(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.skillAgentsCursor != 2 {
		t.Errorf("cursor after 2nd down = %d, want 2", m.skillAgentsCursor)
	}

	// Clamped at last row.
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.skillAgentsCursor != 2 {
		t.Errorf("cursor after clamped down = %d, want 2", m.skillAgentsCursor)
	}

	m = drive(m, tea.KeyPressMsg{Code: tea.KeyUp})
	if m.skillAgentsCursor != 1 {
		t.Errorf("cursor after up = %d, want 1", m.skillAgentsCursor)
	}

	// Clamp at 0.
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyUp})
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyUp})
	if m.skillAgentsCursor != 0 {
		t.Errorf("cursor after clamped up = %d, want 0", m.skillAgentsCursor)
	}
}

// TestSkillAgents_EscCancels verifies esc closes the picker.
func TestSkillAgents_EscCancels(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = true
	m.skillAgentsPicker = true
	m.skillAgentsRows = []app.SkillAgentRow{
		{ID: "codex", Display: "Codex", Targeted: true},
	}

	m = drive(m, pressEsc())

	if m.skillAgentsPicker {
		t.Error("skillAgentsPicker should be false after esc")
	}
}

// TestSkillAgents_EnterSavesAndExits verifies enter closes the picker.
// With nil app the save command is a no-op, but the picker state must close.
func TestSkillAgents_EnterSavesAndExits(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = true
	m.skillAgentsPicker = true
	m.skillAgentsSource = "github.com/foo/pkg"
	m.skillAgentsRows = []app.SkillAgentRow{
		{ID: "codex", Display: "Codex", Targeted: true},
	}

	m = drive(m, pressEnter())

	if m.skillAgentsPicker {
		t.Error("skillAgentsPicker should be false after enter")
	}
}

// TestSkillAgents_SavedMsgUpdatesRows verifies skillAgentsSavedMsg replaces
// skillsRows. The picker is already closed by the enter key handler before the
// async cmd fires; the saved msg only updates row state.
func TestSkillAgents_SavedMsgUpdatesRows(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = true
	m.skillAgentsPicker = false // already closed by enter

	newRows := []app.SkillPackageRow{
		{Source: "github.com/o/r", Name: "github.com/o/r", Agents: []string{"codex"}, Installed: true},
	}
	m = drive(m, skillAgentsSavedMsg{rows: newRows})

	if len(m.skillsRows) != 1 {
		t.Fatalf("skillsRows len = %d, want 1", len(m.skillsRows))
	}
	if m.skillsRows[0].Source != "github.com/o/r" {
		t.Errorf("skillsRows[0].Source = %q, want %q", m.skillsRows[0].Source, "github.com/o/r")
	}
	if len(m.skillsRows[0].Agents) != 1 || m.skillsRows[0].Agents[0] != "codex" {
		t.Errorf("skillsRows[0].Agents = %v, want [codex]", m.skillsRows[0].Agents)
	}
}

// TestSkillAgents_SavedMsgWithErrorPreservesRows verifies a failed save does
// not replace skillsRows and sets skillsErr.
func TestSkillAgents_SavedMsgWithErrorPreservesRows(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = true
	existing := []app.SkillPackageRow{
		{Source: "github.com/existing/pkg", Name: "pkg", Installed: true},
	}
	m.skillsRows = existing

	m = drive(m, skillAgentsSavedMsg{err: errors.New("save failed")})

	if len(m.skillsRows) != 1 || m.skillsRows[0].Source != "github.com/existing/pkg" {
		t.Errorf("skillsRows should be unchanged on error, got %v", m.skillsRows)
	}
	if m.skillsErr == nil {
		t.Error("skillsErr should be set after failed skillAgentsSavedMsg")
	}
}

// TestSkillAgents_PerRowHintsEligibilityDriven verifies that the inline hint
// line rendered under the selected installed row reflects agentsRowHints'
// eligibility checks ("u update", "g group", "x ignore" for an installed,
// non-ignored row) rather than a static per-context hint set, and that
// list-level actions (restore/import/update) are not mixed into it — those
// now live only in the tab footer.
func TestSkillAgents_PerRowHintsEligibilityDriven(t *testing.T) {
	m := skillsModelWithRows([]app.SkillPackageRow{
		{Source: "github.com/foo/caveman", Name: "caveman", Installed: true, PerAgentStatus: map[string]bool{"claude": true}},
	})
	m.skillsCursor = 0

	out := stripANSIEscapeSequences(m.viewSkillsBody())

	for _, want := range []string{"u update", "g group", "x ignore"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected per-row hint %q in viewSkillsBody, got:\n%s", want, out)
		}
	}

	var hintLine string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "g group") {
			hintLine = l
			break
		}
	}
	if hintLine == "" {
		t.Fatal("could not find hint line containing 'g group'")
	}
	for _, unwanted := range []string{"restore", "import"} {
		if strings.Contains(hintLine, unwanted) {
			t.Errorf("per-row hint line must not contain %q, got: %q", unwanted, hintLine)
		}
	}
}

// TestSkillAgents_FooterShowsListLevelActions verifies the skills-chip footer
// (not a stray body line) surfaces the list-level bulk-action and refresh
// actions.
func TestSkillAgents_FooterShowsListLevelActions(t *testing.T) {
	m := skillsModelWithRows([]app.SkillPackageRow{
		{Source: "github.com/foo/caveman", Name: "caveman", Installed: true},
	})
	m.cursorHidden = true

	bindings := tabShortHelpBindings(&m)
	var got []string
	for _, b := range bindings {
		h := b.Help()
		got = append(got, h.Key+" "+h.Desc)
	}
	joined := strings.Join(got, " · ")

	for _, want := range []string{"U update all", "S sync all", "R refresh"} {
		if !strings.Contains(joined, want) {
			t.Errorf("footer missing %q, got %q", want, joined)
		}
	}

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	if strings.Contains(out, "U update all") {
		t.Errorf("viewSkillsBody must not contain a stray bulk-action line, got:\n%s", out)
	}
}
