package tui

import (
	"reflect"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
)

// TestSkillsSearch_SlashOpensSearch verifies that pressing / in viewSkills
// activates search mode and focuses the filter input.
func TestSkillsSearch_SlashOpensSearch(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills

	m = drive(m, pressRune('/'))

	if !m.skillsSearchActive {
		t.Error("skillsSearchActive should be true after pressing '/'")
	}
	if !m.filter.Focused() {
		t.Error("filter should be focused after pressing '/'")
	}
}

// TestSkillsSearch_EscClosesSearch verifies that Esc while search is active
// clears the search state.
func TestSkillsSearch_EscClosesSearch(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills

	m = drive(m, pressRune('/'))
	m = drive(m, pressEsc())

	if m.skillsSearchActive {
		t.Error("skillsSearchActive should be false after Esc")
	}
	if m.filter.Value() != "" {
		t.Errorf("filter should be cleared after Esc, got %q", m.filter.Value())
	}
}

// TestSkillsSearch_TypeFiltersLocalRows verifies that typing a query while
// search is focused filters the visible local rows.
func TestSkillsSearch_TypeFiltersLocalRows(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills
	m.skillsRows = []app.SkillPackageRow{
		{Name: "caveman", Source: "github.com/foo/caveman", Installed: true},
		{Name: "review", Source: "github.com/bar/review", Installed: true},
	}

	m = drive(m, pressRune('/'))
	// Simulate typing "cave" into the focused filter by setting the value directly,
	// then sending a key that triggers re-filter (a no-op rune that the textinput processes).
	m.filter.SetValue("cave")

	visible, _, _ := skillsVisibleRows(m)
	if len(visible) != 1 {
		t.Fatalf("expected 1 visible row after filtering 'cave', got %d: %v", len(visible), visible)
	}
	if visible[0].Name != "caveman" {
		t.Errorf("visible[0].Name = %q, want %q", visible[0].Name, "caveman")
	}
}

// TestSkillsSearch_EnterWithFreeTextDispatchesFind verifies that pressing Enter
// on a free-text query (no slash, no https) sets skillAddRunning (triggering find).
func TestSkillsSearch_EnterWithFreeTextDispatchesFind(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills

	m = drive(m, pressRune('/'))
	m.filter.SetValue("my query")
	m = drive(m, pressEnter())

	if !m.skillAddRunning {
		t.Error("skillAddRunning should be true after Enter with free-text query (triggers find)")
	}
	if m.filter.Focused() {
		t.Error("filter should be blurred after Enter")
	}
}

// TestSkillsSearch_EnterWithSourceDispatchesAdd verifies that pressing Enter
// on a source-like query (owner/repo) sets skillAddRunning (triggering add).
func TestSkillsSearch_EnterWithSourceDispatchesAdd(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills

	m = drive(m, pressRune('/'))
	m.filter.SetValue("owner/repo")
	m = drive(m, pressEnter())

	if !m.skillAddRunning {
		t.Error("skillAddRunning should be true after Enter with source-like query (triggers add)")
	}
}

// TestSkillsSearch_FoundMsgPopulatesFindResults verifies that skillsFoundMsg
// stores results and clears skillAddRunning.
func TestSkillsSearch_FoundMsgPopulatesFindResults(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillAddRunning = true

	results := []app.FindResult{
		{Source: "owner/pkg-a", Skill: "pkg-a", Installs: "1000"},
		{Source: "owner/pkg-b", Skill: "pkg-b", Installs: "500"},
	}
	m = drive(m, skillsFoundMsg{results: results})

	if m.skillAddRunning {
		t.Error("skillAddRunning should be false after skillsFoundMsg")
	}
	if len(m.skillFindResults) != 2 {
		t.Fatalf("skillFindResults len = %d, want 2", len(m.skillFindResults))
	}
	if m.skillFindResults[0].Source != "owner/pkg-a" {
		t.Errorf("skillFindResults[0].Source = %q, want %q", m.skillFindResults[0].Source, "owner/pkg-a")
	}
}

// TestSkillsSearch_FindResultsRenderSection verifies that find results are rendered
// under the shared "Available" status section.
func TestSkillsSearch_FindResultsRenderSection(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills
	m.width = 120
	m.skillFindResults = []app.FindResult{
		{Source: "owner/cool-skill", Skill: "cool-skill", Installs: "42"},
	}

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	availableIdx := strings.Index(out, "Available")
	if availableIdx < 0 {
		t.Fatalf("viewSkillsBody() missing 'Available' section, got:\n%s", out)
	}
	sourceIdx := strings.Index(out, "cool-skill")
	if sourceIdx < 0 {
		t.Fatalf("viewSkillsBody() missing find result name, got:\n%s", out)
	}
	if sourceIdx < availableIdx {
		t.Errorf("find result (idx %d) should appear under the Available section (idx %d)", sourceIdx, availableIdx)
	}
}

// TestSkillsSearch_CursorOnFindRowEnterTriggersAdd verifies that pressing Enter
// when the cursor is on a find row (cursor >= findStart) sets skillAddRunning.
func TestSkillsSearch_CursorOnFindRowEnterTriggersAdd(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills
	m.skillsRows = []app.SkillPackageRow{
		{Name: "existing", Source: "owner/existing", Installed: true},
	}
	m.skillFindResults = []app.FindResult{
		{Source: "owner/new-skill", Skill: "new-skill", Installs: "100"},
	}

	// visible rows: [existing(idx=0), find-row(idx=1)]; findStart=1
	_, findStart, _ := skillsVisibleRows(m)
	if findStart != 1 {
		t.Fatalf("findStart = %d, want 1", findStart)
	}

	m.skillsCursor = findStart
	m = drive(m, pressEnter())

	if !m.skillAddRunning {
		t.Error("skillAddRunning should be true after Enter on a find row")
	}
}

// TestSkillsSearch_FoundMsgWithErrorSetsErr verifies error handling on skillsFoundMsg.
func TestSkillsSearch_FoundMsgWithErrorSetsErr(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillAddRunning = true

	m = drive(m, skillsFoundMsg{err: errTest("find failed")})

	if m.skillAddRunning {
		t.Error("skillAddRunning should be false after error skillsFoundMsg")
	}
	if m.skillsErr == nil || m.skillsErr.Error() != "find failed" {
		t.Errorf("skillsErr = %v, want 'find failed'", m.skillsErr)
	}
}

// TestSkillsSearch_AddedMsgClearsState verifies that skillAddedMsg clears
// search state and find results.
func TestSkillsSearch_AddedMsgClearsState(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillAddRunning = true
	m.skillsSearchActive = true
	m.skillFindResults = []app.FindResult{
		{Source: "owner/pkg", Skill: "pkg"},
	}

	m = drive(m, skillAddedMsg{})

	if m.skillAddRunning {
		t.Error("skillAddRunning should be false after skillAddedMsg")
	}
	if m.skillsSearchActive {
		t.Error("skillsSearchActive should be false after skillAddedMsg")
	}
	if len(m.skillFindResults) != 0 {
		t.Errorf("skillFindResults should be cleared after skillAddedMsg, got %d entries", len(m.skillFindResults))
	}
}

// TestLooksLikeSkillSource_OwnerRepo verifies that owner/repo is recognized as a source.
func TestLooksLikeSkillSource_OwnerRepo(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"owner/repo", true},
		{"https://github.com/owner/repo", true},
		{"find me a skill", false},
		{"caveman", false},
		{"owner/repo/subpath", true},
		{"just text", false},
		{"with spaces/path", false},
	}
	for _, c := range cases {
		got := looksLikeSkillSource(c.input)
		if got != c.want {
			t.Errorf("looksLikeSkillSource(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

func TestSkillAdd_FindRowEnterQueuesSpinnerTick(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills
	m.focused = true
	m.skillsRows = []app.SkillPackageRow{
		{Name: "existing", Source: "owner/existing", Installed: true},
	}
	m.skillFindResults = []app.FindResult{
		{Source: "owner/new-skill", Skill: "new-skill", Installs: "100"},
	}
	_, findStart, _ := skillsVisibleRows(m)
	m.skillsCursor = findStart

	newModel, cmd := m.Update(pressEnter())
	m = newModel.(Model)

	if !m.skillAddRunning {
		t.Error("skillAddRunning should be true after Enter on find row")
	}
	if cmd == nil {
		t.Fatal("no cmd returned after Enter on find row")
	}
	if !batchHasSpinnerTick(m.spinner.Tick, cmd) {
		t.Error("command batch missing spinner tick after Enter on find row")
	}
}

func TestSkillAdd_DirectSourceSubmitQueuesSpinnerTick(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills
	m.focused = true
	m = drive(m, pressRune('/'))
	m.filter.SetValue("owner/repo")

	newModel, cmd := m.Update(pressEnter())
	m = newModel.(Model)

	if !m.skillAddRunning {
		t.Error("skillAddRunning should be true after direct-source submit")
	}
	if cmd == nil {
		t.Fatal("no cmd returned after direct-source submit")
	}
	if !batchHasSpinnerTick(m.spinner.Tick, cmd) {
		t.Error("command batch missing spinner tick after direct-source submit")
	}
}

func TestSkillAdd_ActivityLabelAddingSkill(t *testing.T) {
	m := baseModel(nil)
	m.skillAddRunning = true
	m.searching = false

	got := activityLabel(m)
	if got != "Adding skill…" {
		t.Errorf("activityLabel = %q, want %q", got, "Adding skill…")
	}
}

func TestSkillAdd_ActivityLabelSearchingWins(t *testing.T) {
	m := baseModel(nil)
	m.skillAddRunning = true
	m.searching = true

	got := activityLabel(m)
	if got != "Searching…" {
		t.Errorf("activityLabel with both flags = %q, want %q", got, "Searching…")
	}
}

func TestSkillAdd_StatusbarSpinnerVisibleWhenRunning(t *testing.T) {
	m := baseModel(nil)
	m.skillAddRunning = true
	m.searching = false
	m.width = 80

	out := renderFooterStatusLayer(m, 78)
	spinnerView := m.spinner.View()
	if !strings.Contains(out, spinnerView) {
		t.Errorf("statusbar missing spinner when skillAddRunning=true; got %q", out)
	}
	if !strings.Contains(out, "Adding skill") {
		t.Errorf("statusbar missing 'Adding skill' text when skillAddRunning=true; got %q", out)
	}
}

func TestSkillAdd_SpinnerTickContinuesWhileRunning(t *testing.T) {
	m := baseModel(nil)
	m.focused = true
	m.skillAddRunning = true

	_, cmd := m.Update(spinner.TickMsg{})
	if cmd == nil {
		t.Error("spinner.TickMsg should produce a follow-up command while skillAddRunning=true")
	}
}

func TestSkillAdd_SpinnerTickStopsWhenDone(t *testing.T) {
	m := baseModel(nil)
	m.focused = true
	m.skillAddRunning = false

	_, cmd := m.Update(spinner.TickMsg{})
	if cmd != nil {
		t.Error("spinner.TickMsg should not re-schedule when skillAddRunning=false and no other activity")
	}
}

// batchHasSpinnerTick checks whether spinnerTick appears in cmd's batch by
// comparing function pointers — it never executes any sub-command.
func batchHasSpinnerTick(spinnerTick tea.Cmd, cmd tea.Cmd) bool {
	if cmd == nil || spinnerTick == nil {
		return false
	}
	tickPtr := reflect.ValueOf(spinnerTick).Pointer()
	isTickPtr := func(c tea.Cmd) bool {
		return c != nil && reflect.ValueOf(c).Pointer() == tickPtr
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return isTickPtr(cmd)
	}
	for _, c := range batch {
		if isTickPtr(c) {
			return true
		}
	}
	return false
}

// errTest is a minimal error value for tests.
type errTest string

func (e errTest) Error() string { return string(e) }
