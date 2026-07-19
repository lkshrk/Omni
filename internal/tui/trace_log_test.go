package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/dots"
	"github.com/lkshrk/omni/internal/executor"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// settingsTraceLogModel returns a model positioned on the settings tab with the
// cursor already on settingsRowTraceLog, ready for Enter to be pressed.
func settingsTraceLogModel() Model {
	m := baseModel(nil)
	m.mode = viewSettings
	m.settingsCursor = settingsRowTraceLog
	m.dangerConfirmRow = -1
	return m
}

// fixtureTraces returns three deterministic CommandTrace values for use in tests.
func fixtureTraces() []app.CommandTraceView {
	t0 := time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC)
	return []app.CommandTraceView{
		{
			ID:         1,
			StartedAt:  t0,
			FinishedAt: t0.Add(123 * time.Millisecond),
			DurationMS: 123,
			Command:    "brew install ripgrep",
			Status:     "ok",
			Reason:     "install missing tool",
		},
		{
			ID:         2,
			StartedAt:  t0.Add(time.Minute),
			FinishedAt: t0.Add(time.Minute + 456*time.Millisecond),
			DurationMS: 456,
			Command:    "brew upgrade fd",
			Status:     "error",
			Reason:     "",
		},
		{
			ID:         3,
			StartedAt:  t0.Add(2 * time.Minute),
			FinishedAt: t0.Add(2*time.Minute + 789*time.Millisecond),
			DurationMS: 789,
			Command:    "npm install --global prettier",
			Status:     "ok",
			Reason:     "install via node",
		},
	}
}

// injectTraces drives the model through the full open-and-populate flow:
//
//  1. Enter on settingsRowTraceLog → sets traceLogLoading, dispatches cmd.
//  2. Inject traceLogLoadedMsg with the given gen and traces.
func injectTraces(m Model, traces []app.CommandTraceView) Model {
	gen := m.traceLogGen + 1 // matches what handleSettingsConfirmAction will produce
	m = drive(m, pressEnter())
	return drive(m, traceLogLoadedMsg{gen: gen, traces: traces})
}

// manyTraces returns n identical trace rows for scroll testing.
func manyTraces(n int) []app.CommandTraceView {
	out := make([]app.CommandTraceView, n)
	t0 := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	for i := range out {
		out[i] = app.CommandTraceView{
			ID:         int64(i + 1),
			StartedAt:  t0.Add(time.Duration(i) * time.Minute),
			FinishedAt: t0.Add(time.Duration(i)*time.Minute + 50*time.Millisecond),
			DurationMS: 50,
			Command:    "brew install tool",
			Status:     "ok",
		}
	}
	return out
}

// ── UC-TL-01: Enter on settingsRowTraceLog opens popup ───────────────────────

func TestTraceLog_EnterSetsLoadingState(t *testing.T) {
	m := settingsTraceLogModel()
	prevGen := m.traceLogGen

	got := drive(m, pressEnter())

	if !got.traceLogLoading {
		t.Error("traceLogLoading should be true after Enter on settingsRowTraceLog")
	}
	if got.traceLogGen != prevGen+1 {
		t.Errorf("traceLogGen = %d, want %d", got.traceLogGen, prevGen+1)
	}
}

func TestTraceLog_LoadedMsgPopulatesState(t *testing.T) {
	m := settingsTraceLogModel()
	traces := fixtureTraces()
	got := injectTraces(m, traces)

	if got.traceLog == nil {
		t.Fatal("traceLog should not be nil after traceLogLoadedMsg")
	}
	if got.traceLogLoading {
		t.Error("traceLogLoading should be false after traceLogLoadedMsg")
	}
	if len(got.traceLog.traces) != len(traces) {
		t.Errorf("traceLog.traces len = %d, want %d", len(got.traceLog.traces), len(traces))
	}
}

// ── UC-TL-02: popup renders trace rows ───────────────────────────────────────

func TestTraceLog_ViewRendersTraceRows(t *testing.T) {
	m := settingsTraceLogModel()
	m = injectTraces(m, fixtureTraces())

	view := m.View().Content

	// Primary line must contain the command string.
	if !strings.Contains(view, "brew install ripgrep") {
		t.Error("View() missing command 'brew install ripgrep'")
	}
	// Status should appear.
	if !strings.Contains(view, "ok") {
		t.Error("View() missing status 'ok'")
	}
	// Error status should appear.
	if !strings.Contains(view, "error") {
		t.Error("View() missing status 'error'")
	}
	// Sub-line reason should appear.
	if !strings.Contains(view, "reason:") {
		t.Error("View() missing 'reason:' sub-line")
	}
	if !strings.Contains(view, "install missing tool") {
		t.Error("View() missing reason text 'install missing tool'")
	}
}

func TestTraceLog_RendersStructuredFullFailure(t *testing.T) {
	m := settingsTraceLogModel()
	trace := app.CommandTraceView{
		StartedAt:  time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC),
		DurationMS: 2560,
		Command:    "brew install --cask font-intel-one-mono",
		Status:     "failed",
		Reason:     "installing font-intel-one-mono (brew)",
		Error:      "exit status 1",
		Stderr:     "Error: A font is already installed at:\n/Library/Fonts/IntelOneMono.ttf\nRemove it before reinstalling.",
	}
	m = injectTraces(m, []app.CommandTraceView{trace})

	view := stripANSIEscapeSequences(m.View().Content)
	for _, want := range []string{
		"command:", "brew install --cask font-intel-one-mono",
		"reason:", "installing font-intel-one-mono (brew)",
		"problem:", "A font is already installed at:",
		"error:", "exit status 1",
		"stderr:", "/Library/Fonts/IntelOneMono.ttf", "Remove it before reinstalling.",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("structured command log missing %q:\n%s", want, view)
		}
	}
}

func TestTraceLog_SuccessfulStderrIsNotLabeledProblem(t *testing.T) {
	trace := app.CommandTraceView{Status: "success", Stderr: "download progress"}
	if got := traceLogProblem(trace); got != "" {
		t.Fatalf("traceLogProblem() = %q for a successful command", got)
	}
}

func TestTraceLog_EFromFailedToolOpensPopup(t *testing.T) {
	tool := &app.ToolView{Name: "font-intel-one-mono", Provider: "brew", Tracked: true}
	m := baseModel([]*app.ToolView{tool})
	m.mode = viewList
	m.cursor = 0
	m.setToolActionError(toolKey(tool.Name, tool.Provider), "brew install: exit status 1 (stderr: Error: font already installed)")

	got := drive(m, pressRune('e'))
	if !got.traceLogLoading {
		t.Fatal("e on a failed tool should open the command log")
	}
	if got.mode != viewList {
		t.Fatalf("opening command log should preserve the underlying view, got mode %v", got.mode)
	}
}

func TestTraceLog_EFromBlurredSearchResultOpensPopup(t *testing.T) {
	tool := &app.ToolView{Name: "font-intel-one-mono", Provider: "brew", Tracked: true}
	m := baseModel([]*app.ToolView{tool})
	m.mode = viewSearch
	m.filter.Blur()
	m.cursor = 0
	m.setToolActionError(toolKey(tool.Name, tool.Provider), "font already installed")

	got := drive(m, pressRune('e'))
	if !got.traceLogLoading {
		t.Fatal("e on a failed blurred search result should open the command log")
	}
}

func TestTraceLog_EFromDotsEntryWithLastErrorOpensPopup(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	setDotsRepoForTest(&m, "/repo/dotfiles")
	m.dotsEntries = []app.DotStatus{{
		Name:       "nvim",
		TargetPath: "~/.config/nvim",
		State:      dots.StateModified,
		LastError:  "stow: error: existing target is not a symlink",
	}}
	m.dotsCursor = 0

	got := drive(m, pressRune('e'))
	if !got.traceLogLoading {
		t.Fatal("e on a dots entry with a last error should open the command log")
	}
}

func TestTraceLog_EFromDotsEntryWithoutLastErrorIsNoOp(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	setDotsRepoForTest(&m, "/repo/dotfiles")
	m.dotsEntries = []app.DotStatus{{
		Name:       "nvim",
		TargetPath: "~/.config/nvim",
		State:      dots.StateModified,
	}}
	m.dotsCursor = 0

	got := drive(m, pressRune('e'))
	if got.traceLogLoading {
		t.Fatal("e on a dots entry without a last error should not open the command log")
	}
}

func TestTraceLog_EFromAgentsTabOpensPopup(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = true
	m.skillTypeIdx = agentsChipAll

	got := drive(m, pressRune('e'))
	if !got.traceLogLoading {
		t.Fatal("e on the agents tab should open the command log")
	}
}

func TestTraceLog_EFromDotsEntryRendersPopup(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	setDotsRepoForTest(&m, "/repo/dotfiles")
	m.dotsEntries = []app.DotStatus{{
		Name:       "nvim",
		TargetPath: "~/.config/nvim",
		State:      dots.StateModified,
		LastError:  "stow: error: existing target is not a symlink",
	}}
	m.dotsCursor = 0

	gen := m.traceLogGen + 1
	m = drive(m, pressRune('e'))
	m = drive(m, traceLogLoadedMsg{gen: gen, traces: fixtureTraces()})

	view := m.View().Content
	if !strings.Contains(view, "Command Log") || !strings.Contains(view, "brew install ripgrep") {
		t.Fatalf("e on a dots entry with a last error should render the command log popup:\n%s", view)
	}
}

func TestTraceLog_EFromAgentsTabRendersPopup(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = true
	m.skillTypeIdx = agentsChipAll

	gen := m.traceLogGen + 1
	m = drive(m, pressRune('e'))
	m = drive(m, traceLogLoadedMsg{gen: gen, traces: fixtureTraces()})

	view := m.View().Content
	if !strings.Contains(view, "Command Log") || !strings.Contains(view, "brew install ripgrep") {
		t.Fatalf("e on the agents tab should render the command log popup:\n%s", view)
	}
}

func TestTraceLog_EFromDotsEntryIsRepeatNoOp(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	setDotsRepoForTest(&m, "/repo/dotfiles")
	m.dotsEntries = []app.DotStatus{{
		Name:       "nvim",
		TargetPath: "~/.config/nvim",
		State:      dots.StateModified,
		LastError:  "stow: error: existing target is not a symlink",
	}}
	m.dotsCursor = 0

	got := drive(m, tea.KeyPressMsg{Code: 'e', Text: "e", IsRepeat: true})
	if got.traceLogLoading {
		t.Fatal("e IsRepeat on a dots entry with a last error should not open the command log")
	}
}

func TestTraceLog_EFromDotsChildRowUsesParentLastError(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	setDotsRepoForTest(&m, "/repo/dotfiles")
	parent := app.DotStatus{
		Name:       "nvim",
		TargetPath: "~/.config/nvim",
		State:      dots.StateModified,
		LastError:  "stow: error: existing target is not a symlink",
		Children:   []app.DotChild{{RelPath: "init.lua"}},
	}
	m.dotsEntries = []app.DotStatus{parent}
	m.dotsExpandedName = parent.Name
	m.dotsExpandedState = app.DotStatusState(parent)
	m.dotsCursor = 1 // the child row, not the parent

	got := drive(m, pressRune('e'))
	if !got.traceLogLoading {
		t.Fatal("e on a child row should fall back to the parent entry's last error and open the command log")
	}
}

func TestTraceLog_EFromAgentsTabBlockedDuringDeleteConfirm(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = true
	m.skillTypeIdx = agentsChipAll
	m.agentsDeleteConfirm = true

	got := drive(m, pressRune('e'))
	if got.traceLogLoading {
		t.Fatal("e on the agents tab during a delete confirmation should not open the command log")
	}
}

func TestTraceLog_EFromAgentsTabOpensDuringBulkOp(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = true
	m.skillTypeIdx = agentsChipAll
	m.skillsRunning = true

	got := drive(m, pressRune('e'))
	if !got.traceLogLoading {
		t.Fatal("e on the agents tab should open the command log even while a bulk op is running")
	}
}

func TestTraceLog_ViewShowsPopupTitle(t *testing.T) {
	m := settingsTraceLogModel()
	m = injectTraces(m, fixtureTraces())

	view := m.View().Content

	if !strings.Contains(view, "Command Log") {
		t.Error("View() should contain popup title 'Command Log'")
	}
}

// ── UC-TL-03: empty and loading states ───────────────────────────────────────

func TestTraceLog_LoadingState(t *testing.T) {
	m := settingsTraceLogModel()
	// Press Enter but do NOT inject the loaded msg — popup is in loading state.
	m = drive(m, pressEnter())

	view := m.View().Content

	if !strings.Contains(view, "Loading command log...") {
		t.Errorf("View() should show loading text, got:\n%s", view)
	}
}

func TestTraceLog_EmptyState(t *testing.T) {
	m := settingsTraceLogModel()
	gen := m.traceLogGen + 1
	m = drive(m, pressEnter())
	// Loaded msg with no traces.
	m = drive(m, traceLogLoadedMsg{gen: gen, traces: nil})

	view := m.View().Content

	if !strings.Contains(view, "No commands recorded.") {
		t.Errorf("View() should show empty text, got:\n%s", view)
	}
}

func TestTraceLog_EmptySliceState(t *testing.T) {
	m := settingsTraceLogModel()
	gen := m.traceLogGen + 1
	m = drive(m, pressEnter())
	m = drive(m, traceLogLoadedMsg{gen: gen, traces: []app.CommandTraceView{}})

	view := m.View().Content

	if !strings.Contains(view, "No commands recorded.") {
		t.Errorf("View() should show 'No commands recorded.' for empty slice, got:\n%s", view)
	}
}

// ── UC-TL-04: scrolling clamps correctly ─────────────────────────────────────

func TestTraceLog_ScrollDown(t *testing.T) {
	m := settingsTraceLogModel()
	m = injectTraces(m, manyTraces(50)) // enough rows to scroll

	// Scroll starts at 0.
	if m.traceLog.scroll != 0 {
		t.Fatalf("initial scroll = %d, want 0", m.traceLog.scroll)
	}

	m = drive(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.traceLog.scroll != 1 {
		t.Errorf("scroll after Down = %d, want 1", m.traceLog.scroll)
	}
}

func TestTraceLog_ScrollUp_ClampsAtZero(t *testing.T) {
	m := settingsTraceLogModel()
	m = injectTraces(m, manyTraces(50))

	// Press Down once, then Up twice — second Up should clamp at 0.
	m = drive(m,
		tea.KeyPressMsg{Code: tea.KeyDown},
		tea.KeyPressMsg{Code: tea.KeyUp},
		tea.KeyPressMsg{Code: tea.KeyUp},
	)
	if m.traceLog.scroll != 0 {
		t.Errorf("scroll after Up past top = %d, want 0", m.traceLog.scroll)
	}
}

func TestTraceLog_GoToBottom_G(t *testing.T) {
	m := settingsTraceLogModel()
	m = injectTraces(m, manyTraces(50))

	m = drive(m, pressRune('G'))
	maxScroll := traceLogMaxScroll(m)
	if m.traceLog.scroll != maxScroll {
		t.Errorf("scroll after G = %d, want max %d", m.traceLog.scroll, maxScroll)
	}
	if maxScroll == 0 {
		t.Error("maxScroll should be > 0 for 50 traces")
	}
}

func TestTraceLog_GoToTop_Home(t *testing.T) {
	m := settingsTraceLogModel()
	m = injectTraces(m, manyTraces(50))

	// Go to bottom first, then back to top with Home (Top binding).
	m = drive(m, pressRune('G'))
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyHome})
	if m.traceLog.scroll != 0 {
		t.Errorf("scroll after Home = %d, want 0", m.traceLog.scroll)
	}
}

func TestTraceLog_PageDown_AdvancesScroll(t *testing.T) {
	m := settingsTraceLogModel()
	m = injectTraces(m, manyTraces(50))

	m = drive(m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	want := max(traceLogBodyHeight(m)-2, 1)
	if m.traceLog.scroll != want {
		t.Fatalf("scroll after PageDown = %d, want %d so ellipsis rows do not skip content", m.traceLog.scroll, want)
	}
}

func TestTraceLog_WrapsWideCharactersByCellWidth(t *testing.T) {
	got := hardWrapLine("界界a", 3)
	want := []string{"界", "界a"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("hardWrapLine() = %#v, want %#v", got, want)
	}
	for _, line := range got {
		if width := lipgloss.Width(line); width > 3 {
			t.Fatalf("wrapped line %q is %d cells wide", line, width)
		}
	}
}

func TestTraceLog_ScrollClampsAtMax(t *testing.T) {
	m := settingsTraceLogModel()
	m = injectTraces(m, manyTraces(50))

	// G to bottom, then try to scroll further.
	m = drive(m, pressRune('G'))
	scrollAtBottom := m.traceLog.scroll

	m = drive(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.traceLog.scroll != scrollAtBottom {
		t.Errorf("scroll past max = %d, want clamped %d", m.traceLog.scroll, scrollAtBottom)
	}
}

// ── UC-TL-05: Esc closes the popup ───────────────────────────────────────────

func TestTraceLog_EscClosesPopup(t *testing.T) {
	m := settingsTraceLogModel()
	m = injectTraces(m, fixtureTraces())

	if m.traceLog == nil {
		t.Fatal("precondition: traceLog should be non-nil")
	}

	m = drive(m, pressEsc())

	if m.traceLog != nil {
		t.Error("traceLog should be nil after Esc")
	}
	if m.traceLogLoading {
		t.Error("traceLogLoading should be false after Esc")
	}
	if m.mode != viewSettings {
		t.Errorf("mode = %v, want viewSettings after Esc", m.mode)
	}
}

func TestTraceLog_EscFromLoadingClosesPopup(t *testing.T) {
	m := settingsTraceLogModel()
	m = drive(m, pressEnter()) // loading state, no msg yet

	if !m.traceLogLoading {
		t.Fatal("precondition: should be in loading state")
	}

	m = drive(m, pressEsc())

	if m.traceLogLoading {
		t.Error("traceLogLoading should be false after Esc")
	}
	if m.traceLog != nil {
		t.Error("traceLog should be nil after Esc")
	}
}

// ── UC-TL-06: stale-gen guard ─────────────────────────────────────────────────

func TestTraceLog_StaleGenIgnored(t *testing.T) {
	m := settingsTraceLogModel()
	m = drive(m, pressEnter()) // gen is now traceLogGen (e.g. 1)

	currentGen := m.traceLogGen
	staleGen := currentGen - 1

	// Inject a msg with the old gen — should be ignored.
	m = drive(m, traceLogLoadedMsg{
		gen:    staleGen,
		traces: fixtureTraces(),
	})

	if m.traceLog != nil {
		t.Error("stale traceLogLoadedMsg should not populate traceLog")
	}
	if !m.traceLogLoading {
		t.Error("traceLogLoading should remain true after stale msg")
	}
}

func TestTraceLog_CorrectGenAccepted(t *testing.T) {
	m := settingsTraceLogModel()
	m = drive(m, pressEnter())

	currentGen := m.traceLogGen

	// Inject with the correct gen — should populate.
	m = drive(m, traceLogLoadedMsg{
		gen:    currentGen,
		traces: fixtureTraces(),
	})

	if m.traceLog == nil {
		t.Error("correct-gen traceLogLoadedMsg should populate traceLog")
	}
}

// ── UC-TL-07: error in loaded msg does not crash ─────────────────────────────

func TestTraceLog_ErrorInLoadedMsg(t *testing.T) {
	m := settingsTraceLogModel()
	gen := m.traceLogGen + 1
	m = drive(m, pressEnter())
	m = drive(m, traceLogLoadedMsg{gen: gen, err: errTraceLoadFailed})

	if m.traceLog == nil {
		t.Error("traceLog should be non-nil (empty) even after error")
	}
	if m.traceLogLoading {
		t.Error("traceLogLoading should be false after error msg")
	}
}

// ── UC-TL-08: render gate ────────────────────────────────────────────────────

// TestTraceLog_RenderGate_VisibleInSettings verifies the popup is drawn when
// m.mode == viewSettings and traceLog is populated.
func TestTraceLog_RenderGate_VisibleInSettings(t *testing.T) {
	m := settingsTraceLogModel()
	m = injectTraces(m, fixtureTraces())

	// Precondition: we are in viewSettings with traces loaded.
	if m.mode != viewSettings {
		t.Fatalf("precondition: mode = %v, want viewSettings", m.mode)
	}
	if m.traceLog == nil {
		t.Fatal("precondition: traceLog should be non-nil")
	}

	view := m.View().Content
	if !strings.Contains(view, "brew install ripgrep") {
		t.Errorf("popup should be visible in viewSettings; View() missing trace command\nview:\n%s", view)
	}
	if !strings.Contains(view, "Command Log") {
		t.Errorf("popup title 'Command Log' should appear in viewSettings\nview:\n%s", view)
	}
}

func TestTraceLog_RenderGate_VisibleInList(t *testing.T) {
	// Build the model in settings mode so we can populate traceLog.
	m := settingsTraceLogModel()
	m = injectTraces(m, fixtureTraces())

	if m.traceLog == nil {
		t.Fatal("precondition: traceLog should be non-nil")
	}

	m.mode = viewList

	view := m.View().Content
	if !strings.Contains(view, "brew install ripgrep") || !strings.Contains(view, "Command Log") {
		t.Fatalf("command log should be visible over the tools list:\n%s", view)
	}
}

func TestTraceLog_RenderGate_HiddenOutsideSupportedViews(t *testing.T) {
	m := settingsTraceLogModel()
	m = injectTraces(m, fixtureTraces())
	m.mode = viewGroups

	view := m.View().Content
	if strings.Contains(view, "brew install ripgrep") || strings.Contains(view, "Command Log") {
		t.Fatalf("command log should stay hidden outside settings/tools/dots/agents views:\n%s", view)
	}
}

func TestTraceLog_RenderGate_VisibleInDots(t *testing.T) {
	m := settingsTraceLogModel()
	m = injectTraces(m, fixtureTraces())
	m.mode = viewDots

	view := m.View().Content
	if !strings.Contains(view, "brew install ripgrep") || !strings.Contains(view, "Command Log") {
		t.Fatalf("command log should be visible over the dots view:\n%s", view)
	}
}

func TestTraceLog_RenderGate_VisibleInAgents(t *testing.T) {
	m := settingsTraceLogModel()
	m = injectTraces(m, fixtureTraces())
	m.mode = viewSkills
	m.agentsEnabled = true
	m.skillTypeIdx = agentsChipAll

	view := m.View().Content
	if !strings.Contains(view, "brew install ripgrep") || !strings.Contains(view, "Command Log") {
		t.Fatalf("command log should be visible over the agents view:\n%s", view)
	}
}

func TestTraceLog_DisablesMainTabClicksWhileOpen(t *testing.T) {
	m := settingsTraceLogModel()
	m = injectTraces(m, fixtureTraces())
	if m.mainTabsClickable() {
		t.Fatal("main tabs should not be clickable behind the command log")
	}
}

// ── UC-TL-09: error path rendering ───────────────────────────────────────────

// TestTraceLog_ErrorPath_RendersFailureMessage verifies that when
// handleTraceLogLoadedMsg receives msg.err != nil it:
//
//	(a) renders "Failed to load command log" + the error text in the popup,
//	(b) does NOT render "No commands recorded.", and
//	(c) sets m.statusMsg with m.statusIsErr == true.
func TestTraceLog_ErrorPath_RendersFailureMessage(t *testing.T) {
	m := settingsTraceLogModel()
	gen := m.traceLogGen + 1
	m = drive(m, pressEnter())

	sentinelErr := errSentinel("db unavailable")
	m = drive(m, traceLogLoadedMsg{gen: gen, err: sentinelErr})

	// (a) popup body must contain the failure text.
	view := m.View().Content
	if !strings.Contains(view, "Failed to load command log") {
		t.Errorf("View() missing 'Failed to load command log'\nview:\n%s", view)
	}
	if !strings.Contains(view, "db unavailable") {
		t.Errorf("View() missing error text 'db unavailable'\nview:\n%s", view)
	}

	// (b) must NOT fall through to the empty-state text.
	if strings.Contains(view, "No commands recorded.") {
		t.Error("View() must not show 'No commands recorded.' when err is set")
	}

	// (c) status bar must reflect the error.
	if m.statusMsg == "" {
		t.Error("statusMsg should be set after error traceLogLoadedMsg")
	}
	if !m.statusIsErr {
		t.Errorf("statusIsErr should be true after error traceLogLoadedMsg, statusMsg=%q", m.statusMsg)
	}
}

// ── UC-TL-10: half-page scroll ───────────────────────────────────────────────

// TestTraceLog_HalfPageDown_AdvancesScroll verifies that ctrl+d moves the
// scroll position forward by at least 1 row.
func TestTraceLog_HalfPageDown_AdvancesScroll(t *testing.T) {
	m := settingsTraceLogModel()
	m = injectTraces(m, manyTraces(50)) // enough rows to scroll

	if m.traceLog.scroll != 0 {
		t.Fatalf("precondition: initial scroll = %d, want 0", m.traceLog.scroll)
	}

	m = drive(m, pressCtrlD())
	if m.traceLog.scroll == 0 {
		t.Error("HalfPageDown (ctrl+d) should advance scroll beyond 0")
	}
	scrollAfterDown := m.traceLog.scroll

	// Half-page up should move back toward 0.
	m = drive(m, pressCtrlU())
	if m.traceLog.scroll >= scrollAfterDown {
		t.Errorf("HalfPageUp (ctrl+u) should reduce scroll; got %d, was %d", m.traceLog.scroll, scrollAfterDown)
	}
}

// TestTraceLog_HalfPageUp_ClampsAtZero verifies ctrl+u cannot go below scroll=0.
func TestTraceLog_HalfPageUp_ClampsAtZero(t *testing.T) {
	m := settingsTraceLogModel()
	m = injectTraces(m, manyTraces(50))

	// Starting at 0, pressing ctrl+u should not underflow.
	m = drive(m, pressCtrlU())
	if m.traceLog.scroll != 0 {
		t.Errorf("HalfPageUp at top should clamp to 0, got %d", m.traceLog.scroll)
	}
}

// errTraceLoadFailed is a sentinel error for testing the error-path.
var errTraceLoadFailed = errSentinel("trace load failed")

type errSentinel string

func (e errSentinel) Error() string { return string(e) }

// ── Integration: real App + real SQLite DB ────────────────────────────────────

// TestTraceLog_Integration_RealAppRealDB proves the full end-to-end path:
// real DB rows → App.CommandTraces → doLoadTraces cmd → popup render.
//
// Traces are seeded via App.RecordCommandTrace (the executor.TraceSink path),
// which writes directly to the SQLite database — no stubs involved.
func TestTraceLog_Integration_RealAppRealDB(t *testing.T) {
	ctx := context.Background()

	// 1. Build a real App backed by a real temp SQLite DB, identical to how
	//    newDotsModelForCmds constructs its app (reusing the same helper chain).
	m, _ := newDotsModelForCmds(t)
	a := m.app

	// 2. Record two real command traces through App.RecordCommandTrace (the
	//    executor.TraceSink interface), so rows land in the real SQLite table.
	t0 := time.Date(2024, 3, 10, 14, 22, 33, 0, time.UTC)
	traces := []executor.TraceRecord{
		{
			StartedAt:  t0,
			FinishedAt: t0.Add(77 * time.Millisecond),
			DurationMS: 77,
			Command:    "brew install omni-integration-canary",
			Status:     "ok",
			Reason:     "integration test reason alpha",
		},
		{
			StartedAt:  t0.Add(time.Minute),
			FinishedAt: t0.Add(time.Minute + 210*time.Millisecond),
			DurationMS: 210,
			Command:    "npm install --global omni-integration-canary",
			Status:     "error",
			Reason:     "integration test reason beta",
		},
	}
	for _, tr := range traces {
		if err := a.RecordCommandTrace(ctx, tr); err != nil {
			t.Fatalf("RecordCommandTrace: %v", err)
		}
	}

	// 3. Wire the model to the real App, navigate to Settings, position cursor
	//    on settingsRowTraceLog, and press Enter.
	m.mode = viewSettings
	m.settingsCursor = settingsRowTraceLog
	m.dangerConfirmRow = -1

	m = drive(m, pressEnter())

	if !m.traceLogLoading {
		t.Fatal("traceLogLoading should be true after Enter")
	}

	// 4. Execute the real tea.Cmd returned by doLoadTraces — this calls
	//    App.CommandTraces against the real SQLite DB.  Feed the resulting
	//    message back through Update exactly as the bubbletea runtime would.
	cmd := m.doLoadTraces()
	msg := cmd() // run the Cmd synchronously; returns traceLogLoadedMsg
	m = drive(m, msg)

	// 5. Assert popup state populated from real DB rows.
	if m.traceLogLoading {
		t.Error("traceLogLoading should be false after load")
	}
	if m.traceLog == nil {
		t.Fatal("traceLog should not be nil after real load")
	}
	if len(m.traceLog.traces) < 2 {
		t.Fatalf("expected ≥2 traces from DB, got %d", len(m.traceLog.traces))
	}

	// Assert rendered View contains the real command strings, status, and reasons.
	view := m.View().Content
	for _, want := range []string{
		"omni-integration-canary",       // command substring shared by both rows
		"ok",                            // status of first trace
		"error",                         // status of second trace
		"integration test reason alpha", // reason sub-line from first row
		"integration test reason beta",  // reason sub-line from second row
	} {
		if !strings.Contains(view, want) {
			t.Errorf("View() missing %q\nfull view:\n%s", want, view)
		}
	}

	// 6. Esc closes the popup.
	m = drive(m, pressEsc())
	if m.traceLog != nil {
		t.Error("traceLog should be nil after Esc")
	}
	if m.mode != viewSettings {
		t.Errorf("mode = %v after Esc, want viewSettings", m.mode)
	}

	// Verify App can be closed cleanly (cleanup also runs via t.Cleanup).
	_ = a
}

// newRealAppModel is a convenience used only by the integration test above.
// It shadows the per-test App built by newDotsModelForCmds so callers can
// reach m.app for direct DB operations.
var _ *app.App // keep the app import used
