package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/database"
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
func fixtureTraces() []database.CommandTrace {
	t0 := time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC)
	return []database.CommandTrace{
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
func injectTraces(m Model, traces []database.CommandTrace) Model {
	gen := m.traceLogGen + 1 // matches what handleSettingsConfirmAction will produce
	m = drive(m, pressEnter())
	return drive(m, traceLogLoadedMsg{gen: gen, traces: traces})
}

// manyTraces returns n identical trace rows for scroll testing.
func manyTraces(n int) []database.CommandTrace {
	out := make([]database.CommandTrace, n)
	t0 := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	for i := range out {
		out[i] = database.CommandTrace{
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
	m = drive(m, traceLogLoadedMsg{gen: gen, traces: []database.CommandTrace{}})

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
	if m.traceLog.scroll == 0 {
		t.Error("scroll should advance after PageDown")
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

// errTraceLoadFailed is a sentinel error for testing the error-path.
var errTraceLoadFailed = errSentinel("trace load failed")

type errSentinel string

func (e errSentinel) Error() string { return string(e) }
