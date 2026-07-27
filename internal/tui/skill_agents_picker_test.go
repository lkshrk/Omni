package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
)

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

// openSkillAgentsPicker guards a nil app for the AgentPickerRows call but still sets skillAgentsPicker=true.
func TestSkillAgents_OpenPickerNilApp(t *testing.T) {
	t.Parallel()
	m := skillsModelWithRows([]app.SkillPackageRow{
		{Source: "github.com/foo/caveman", Name: "caveman", Installed: true, Agents: []string{"codex"}},
	})
	m.skillsCursor = 0

	m = drive(m, pressRune('a'))

	if !m.skillAgentsPicker {
		t.Error("skillAgentsPicker should be true after pressing 'a' on a local row")
	}
}

func TestSkillAgents_PopupRenderChecks(t *testing.T) {
	t.Parallel()
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

func TestSkillAgents_SpaceToggles(t *testing.T) {
	t.Parallel()
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

func TestSkillAgents_UpDownMoveCursor(t *testing.T) {
	t.Parallel()
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

	m = drive(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.skillAgentsCursor != 2 {
		t.Errorf("cursor after clamped down = %d, want 2", m.skillAgentsCursor)
	}

	m = drive(m, tea.KeyPressMsg{Code: tea.KeyUp})
	if m.skillAgentsCursor != 1 {
		t.Errorf("cursor after up = %d, want 1", m.skillAgentsCursor)
	}

	m = drive(m, tea.KeyPressMsg{Code: tea.KeyUp})
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyUp})
	if m.skillAgentsCursor != 0 {
		t.Errorf("cursor after clamped up = %d, want 0", m.skillAgentsCursor)
	}
}

func TestSkillAgents_EscCancels(t *testing.T) {
	t.Parallel()
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

// With a nil app the save command is a no-op, but the picker state must close.
func TestSkillAgents_EnterSavesAndExits(t *testing.T) {
	t.Parallel()
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

// The picker is already closed by the enter key handler before the async cmd fires; the saved msg only updates row state.
func TestSkillAgents_SavedMsgUpdatesRows(t *testing.T) {
	t.Parallel()
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

func TestSkillAgents_SavedMsgWithErrorPreservesRows(t *testing.T) {
	t.Parallel()
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

// The inline hint line must reflect agentsRowHints' eligibility rather than a static set, and list-level actions must stay out of it since those live only in the tab footer.
func TestSkillAgents_PerRowHintsEligibilityDriven(t *testing.T) {
	t.Parallel()
	m := skillsModelWithRows([]app.SkillPackageRow{
		{Source: "github.com/foo/caveman", Name: "caveman", Installed: true, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled}},
	})
	m.skillsCursor = 0

	out := stripANSIEscapeSequences(m.viewSkillsBody())

	for _, want := range []string{"u upgrade", "g group", "x ignore"} {
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

// The list-level bulk-action and refresh actions must surface in the skills-chip footer, not a stray body line.
func TestSkillAgents_FooterShowsListLevelActions(t *testing.T) {
	t.Parallel()
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

	for _, want := range []string{"U upgrade all", "S sync all", "R refresh"} {
		if !strings.Contains(joined, want) {
			t.Errorf("footer missing %q, got %q", want, joined)
		}
	}

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	if strings.Contains(out, "U upgrade all") {
		t.Errorf("viewSkillsBody must not contain a stray bulk-action line, got:\n%s", out)
	}
}

// Its config path is a directory, so every LoadConfig/withConfig call fails deterministically without touching the network or the real config.
func newBrokenConfigApp(t *testing.T) *app.App {
	t.Helper()
	return app.New(t.TempDir())
}

// When SkillAgentRows fails, openSkillAgentsPicker must return an error status cmd and leave picker state untouched.
func TestOpenSkillAgentsPicker_ErrorSetsStatusAndKeepsPickerClosed(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.app = newBrokenConfigApp(t)

	cmd := m.openSkillAgentsPicker(app.SkillPackageRow{Source: "github.com/foo/pkg", Name: "pkg"})

	if cmd == nil {
		t.Fatal("expected a non-nil status cmd when SkillAgentRows fails")
	}
	if !strings.HasPrefix(m.statusMsg, "✗ ") {
		t.Errorf("statusMsg = %q, want it to start with %q", m.statusMsg, "✗ ")
	}
	if m.skillAgentsPicker {
		t.Error("skillAgentsPicker should stay false when SkillAgentRows fails")
	}
	if m.skillAgentsSource != "" {
		t.Errorf("skillAgentsSource = %q, want empty (untouched on error)", m.skillAgentsSource)
	}
	if m.skillAgentsRows != nil {
		t.Errorf("skillAgentsRows = %v, want nil (untouched on error)", m.skillAgentsRows)
	}
}

func TestOpenSkillAgentsPicker_SuccessOpensPicker(t *testing.T) {
	t.Parallel()
	a := newScanPlanTestApp(t)
	m := baseModel(nil)
	m.app = a
	m.skillAgentsCursor = 3

	cmd := m.openSkillAgentsPicker(app.SkillPackageRow{Source: "github.com/foo/pkg", Name: "pkg"})

	if cmd != nil {
		t.Errorf("expected nil cmd on success, got %v", cmd)
	}
	if !m.skillAgentsPicker {
		t.Error("skillAgentsPicker should be true after a successful open")
	}
	if m.skillAgentsSource != "github.com/foo/pkg" {
		t.Errorf("skillAgentsSource = %q, want %q", m.skillAgentsSource, "github.com/foo/pkg")
	}
	if m.skillAgentsCursor != 0 {
		t.Errorf("skillAgentsCursor = %d, want 0", m.skillAgentsCursor)
	}
}

// An unknown package source propagates its error into skillsGroupsUpdatedMsg with no rows.
func TestDoSetSkillGroupMemberships_ErrorCarriedInMsg(t *testing.T) {
	t.Parallel()
	a := newScanPlanTestApp(t)
	m := baseModel(nil)
	m.app = a
	m.ctx = context.Background()

	msg := m.doSetSkillGroupMemberships("github.com/foo/absent", []string{"dev"}, nil, shortHostname())()
	got, ok := msg.(skillsGroupsUpdatedMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want skillsGroupsUpdatedMsg", msg)
	}
	if got.err == nil {
		t.Fatal("expected an error for an unknown package source")
	}
	if !strings.Contains(got.err.Error(), "not found") {
		t.Errorf("err = %q, want it to mention 'not found'", got.err.Error())
	}
	if got.rows != nil {
		t.Errorf("rows = %v, want nil on error", got.rows)
	}
}

func TestDoSetSkillGroupMemberships_SuccessReturnsRows(t *testing.T) {
	t.Parallel()
	a := newScanPlanTestApp(t)
	const source = "github.com/foo/pkg"
	if _, err := a.AdoptSkillPackage(source); err != nil {
		t.Fatalf("AdoptSkillPackage: %v", err)
	}
	m := baseModel(nil)
	m.app = a
	m.ctx = context.Background()

	msg := m.doSetSkillGroupMemberships(source, []string{"dev"}, nil, shortHostname())()
	got, ok := msg.(skillsGroupsUpdatedMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want skillsGroupsUpdatedMsg", msg)
	}
	if got.err != nil {
		t.Fatalf("err = %v, want nil", got.err)
	}
	var row app.SkillPackageRow
	for _, r := range got.rows {
		if r.Source == source {
			row = r
		}
	}
	if row.Source == "" {
		t.Fatalf("rows = %v, want a row for %q", got.rows, source)
	}
	if len(row.Groups) != 1 || row.Groups[0] != "dev" {
		t.Errorf("row.Groups = %v, want [dev]", row.Groups)
	}
}
