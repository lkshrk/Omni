package tui

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/dots"
	"github.com/lkshrk/omni/internal/executor"
)

// Positioned on the settings tab with the cursor already on settingsRowTraceLog, ready for Enter.
func settingsTraceLogModel() Model {
	m := baseModel(nil)
	m.mode = viewSettings
	m.settingsCursor = settingsRowTraceLog
	m.dangerConfirmRow = -1
	return m
}

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

// Presses Enter on settingsRowTraceLog, then injects traceLogLoadedMsg with the given gen and traces.
func injectTraces(m Model, traces []app.CommandTraceView) Model {
	gen := m.traceLogGen + 1 // matches what handleSettingsConfirmAction will produce
	m = drive(m, pressEnter())
	return drive(m, traceLogLoadedMsg{gen: gen, traces: traces})
}

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

func TestTraceLog_EnterSetsLoadingState(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestTraceLog_ViewRendersTraceRows(t *testing.T) {
	t.Parallel()
	m := settingsTraceLogModel()
	m = injectTraces(m, fixtureTraces())

	view := m.View().Content

	if !strings.Contains(view, "brew install ripgrep") {
		t.Error("View() missing command 'brew install ripgrep'")
	}
	if !strings.Contains(view, "ok") {
		t.Error("View() missing status 'ok'")
	}
	if !strings.Contains(view, "error") {
		t.Error("View() missing status 'error'")
	}
	if !strings.Contains(view, "reason:") {
		t.Error("View() missing 'reason:' sub-line")
	}
	if !strings.Contains(view, "install missing tool") {
		t.Error("View() missing reason text 'install missing tool'")
	}
}

func TestTraceLog_RendersStructuredFullFailure(t *testing.T) {
	t.Parallel()
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

func TestTraceLog_LegacyControlsNeverReachRenderedPopup(t *testing.T) {
	invalidUTF8 := string([]byte{0xff})
	legacyText := "before\x00\x01\x08\r\x7f\u0080\u0085\u009f" +
		"\x1b[31mred\x1b[0m\x1b]0;title\x07" + invalidUTF8 + "界\tafter\nnext"
	m, _ := newDotsModelForCmds(t)
	if err := m.app.DB().RecordCommandTrace(context.Background(), &database.CommandTrace{
		StartedAt: time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC),
		Command:   legacyText,
		Status:    "failed\x08\r\u009f",
		Reason:    legacyText,
		Error:     legacyText,
		Stderr:    legacyText,
	}); err != nil {
		t.Fatalf("record legacy command trace: %v", err)
	}
	m.mode = viewSettings
	m.settingsCursor = settingsRowTraceLog
	m.dangerConfirmRow = -1
	m = drive(m, pressEnter())
	m = drive(m, m.doLoadTraces()())

	plain := stripANSIEscapeSequences(m.View().Content)
	if !utf8.ValidString(plain) {
		t.Fatalf("rendered command log is not valid UTF-8: %q", plain)
	}
	for _, r := range plain {
		if (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			t.Fatalf("rendered command log contains control U+%04X: %q", r, plain)
		}
	}
	for _, want := range []string{"before", "red", "界", "after", "next"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("rendered command log missing readable text %q: %q", want, plain)
		}
	}
	if strings.Contains(plain, "title") {
		t.Fatalf("rendered command log retained OSC payload: %q", plain)
	}
}

func TestTraceLog_SuccessfulStderrIsNotLabeledProblem(t *testing.T) {
	t.Parallel()
	trace := app.CommandTraceView{Status: "success", Stderr: "download progress"}
	if got := traceLogProblem(trace); got != "" {
		t.Fatalf("traceLogProblem() = %q for a successful command", got)
	}
}

func TestTraceLog_EFromFailedToolOpensPopup(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

func TestTraceLog_EFromDotsEntryRendersPopup(t *testing.T) {
	t.Parallel()
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

func TestTraceLog_EFromDotsEntryIsRepeatNoOp(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestTraceLog_ViewShowsPopupTitle(t *testing.T) {
	t.Parallel()
	m := settingsTraceLogModel()
	m = injectTraces(m, fixtureTraces())

	view := m.View().Content

	if !strings.Contains(view, "Command Log") {
		t.Error("View() should contain popup title 'Command Log'")
	}
}

func TestTraceLog_LoadingState(t *testing.T) {
	t.Parallel()
	m := settingsTraceLogModel()
	m = drive(m, pressEnter())

	view := m.View().Content

	if !strings.Contains(view, "Loading command log...") {
		t.Errorf("View() should show loading text, got:\n%s", view)
	}
}

func TestTraceLog_EmptyState(t *testing.T) {
	t.Parallel()
	m := settingsTraceLogModel()
	gen := m.traceLogGen + 1
	m = drive(m, pressEnter())
	m = drive(m, traceLogLoadedMsg{gen: gen, traces: nil})

	view := m.View().Content

	if !strings.Contains(view, "No commands recorded.") {
		t.Errorf("View() should show empty text, got:\n%s", view)
	}
}

func TestTraceLog_EmptySliceState(t *testing.T) {
	t.Parallel()
	m := settingsTraceLogModel()
	gen := m.traceLogGen + 1
	m = drive(m, pressEnter())
	m = drive(m, traceLogLoadedMsg{gen: gen, traces: []app.CommandTraceView{}})

	view := m.View().Content

	if !strings.Contains(view, "No commands recorded.") {
		t.Errorf("View() should show 'No commands recorded.' for empty slice, got:\n%s", view)
	}
}

func TestTraceLog_ScrollDown(t *testing.T) {
	t.Parallel()
	m := settingsTraceLogModel()
	m = injectTraces(m, manyTraces(50)) // enough rows to scroll

	if m.traceLog.scroll != 0 {
		t.Fatalf("initial scroll = %d, want 0", m.traceLog.scroll)
	}

	m = drive(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.traceLog.scroll != 1 {
		t.Errorf("scroll after Down = %d, want 1", m.traceLog.scroll)
	}
}

func TestTraceLog_ScrollUp_ClampsAtZero(t *testing.T) {
	t.Parallel()
	m := settingsTraceLogModel()
	m = injectTraces(m, manyTraces(50))

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
	t.Parallel()
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
	t.Parallel()
	m := settingsTraceLogModel()
	m = injectTraces(m, manyTraces(50))

	m = drive(m, pressRune('G'))
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyHome})
	if m.traceLog.scroll != 0 {
		t.Errorf("scroll after Home = %d, want 0", m.traceLog.scroll)
	}
}

func TestTraceLog_PageDown_AdvancesScroll(t *testing.T) {
	t.Parallel()
	m := settingsTraceLogModel()
	m = injectTraces(m, manyTraces(50))

	m = drive(m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	want := max(traceLogBodyHeight(m)-2, 1)
	if m.traceLog.scroll != want {
		t.Fatalf("scroll after PageDown = %d, want %d so ellipsis rows do not skip content", m.traceLog.scroll, want)
	}
}

func TestTraceLog_WrapsWideCharactersByCellWidth(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	m := settingsTraceLogModel()
	m = injectTraces(m, manyTraces(50))

	m = drive(m, pressRune('G'))
	scrollAtBottom := m.traceLog.scroll

	m = drive(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.traceLog.scroll != scrollAtBottom {
		t.Errorf("scroll past max = %d, want clamped %d", m.traceLog.scroll, scrollAtBottom)
	}
}

func TestTraceLog_EscClosesPopup(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestTraceLog_StaleGenIgnored(t *testing.T) {
	t.Parallel()
	m := settingsTraceLogModel()
	m = drive(m, pressEnter()) // gen is now traceLogGen (e.g. 1)

	currentGen := m.traceLogGen
	staleGen := currentGen - 1

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
	t.Parallel()
	m := settingsTraceLogModel()
	m = drive(m, pressEnter())

	currentGen := m.traceLogGen

	m = drive(m, traceLogLoadedMsg{
		gen:    currentGen,
		traces: fixtureTraces(),
	})

	if m.traceLog == nil {
		t.Error("correct-gen traceLogLoadedMsg should populate traceLog")
	}
}

func TestTraceLog_ErrorInLoadedMsg(t *testing.T) {
	t.Parallel()
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

func TestTraceLog_RenderGate_VisibleInSettings(t *testing.T) {
	t.Parallel()
	m := settingsTraceLogModel()
	m = injectTraces(m, fixtureTraces())

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
	t.Parallel()

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
	t.Parallel()
	m := settingsTraceLogModel()
	m = injectTraces(m, fixtureTraces())
	m.mode = viewGroups

	view := m.View().Content
	if strings.Contains(view, "brew install ripgrep") || strings.Contains(view, "Command Log") {
		t.Fatalf("command log should stay hidden outside settings/tools/dots/agents views:\n%s", view)
	}
}

func TestTraceLog_RenderGate_VisibleInDots(t *testing.T) {
	t.Parallel()
	m := settingsTraceLogModel()
	m = injectTraces(m, fixtureTraces())
	m.mode = viewDots

	view := m.View().Content
	if !strings.Contains(view, "brew install ripgrep") || !strings.Contains(view, "Command Log") {
		t.Fatalf("command log should be visible over the dots view:\n%s", view)
	}
}

func TestTraceLog_DisablesMainTabClicksWhileOpen(t *testing.T) {
	t.Parallel()
	m := settingsTraceLogModel()
	m = injectTraces(m, fixtureTraces())
	if m.mainTabsClickable() {
		t.Fatal("main tabs should not be clickable behind the command log")
	}
}

// On a non-nil msg.err the popup must render the failure text, must NOT render "No commands recorded.", and must set statusIsErr.
func TestTraceLog_ErrorPath_RendersFailureMessage(t *testing.T) {
	t.Parallel()
	m := settingsTraceLogModel()
	gen := m.traceLogGen + 1
	m = drive(m, pressEnter())

	sentinelErr := errSentinel("db unavailable")
	m = drive(m, traceLogLoadedMsg{gen: gen, err: sentinelErr})

	view := m.View().Content
	if !strings.Contains(view, "Failed to load command log") {
		t.Errorf("View() missing 'Failed to load command log'\nview:\n%s", view)
	}
	if !strings.Contains(view, "db unavailable") {
		t.Errorf("View() missing error text 'db unavailable'\nview:\n%s", view)
	}

	if strings.Contains(view, "No commands recorded.") {
		t.Error("View() must not show 'No commands recorded.' when err is set")
	}

	if m.statusMsg == "" {
		t.Error("statusMsg should be set after error traceLogLoadedMsg")
	}
	if !m.statusIsErr {
		t.Errorf("statusIsErr should be true after error traceLogLoadedMsg, statusMsg=%q", m.statusMsg)
	}
}

func TestTraceLog_HalfPageDown_AdvancesScroll(t *testing.T) {
	t.Parallel()
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

	m = drive(m, pressCtrlU())
	if m.traceLog.scroll >= scrollAfterDown {
		t.Errorf("HalfPageUp (ctrl+u) should reduce scroll; got %d, was %d", m.traceLog.scroll, scrollAfterDown)
	}
}

func TestTraceLog_HalfPageUp_ClampsAtZero(t *testing.T) {
	t.Parallel()
	m := settingsTraceLogModel()
	m = injectTraces(m, manyTraces(50))

	m = drive(m, pressCtrlU())
	if m.traceLog.scroll != 0 {
		t.Errorf("HalfPageUp at top should clamp to 0, got %d", m.traceLog.scroll)
	}
}

var errTraceLoadFailed = errSentinel("trace load failed")

type errSentinel string

func (e errSentinel) Error() string { return string(e) }

// Traces are seeded via App.RecordCommandTrace, which writes directly to the SQLite database, so no stubs are involved.
func TestTraceLog_Integration_RealAppRealDB(t *testing.T) {
	ctx := context.Background()

	m, _ := newDotsModelForCmds(t)
	a := m.app

	// Recorded through App.RecordCommandTrace (the executor.TraceSink interface) so rows land in the real SQLite table.
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

	m.mode = viewSettings
	m.settingsCursor = settingsRowTraceLog
	m.dangerConfirmRow = -1

	m = drive(m, pressEnter())

	if !m.traceLogLoading {
		t.Fatal("traceLogLoading should be true after Enter")
	}

	// Executes the real doLoadTraces cmd against the real SQLite DB, then feeds the message back through Update as the runtime would.
	cmd := m.doLoadTraces()
	msg := cmd()
	m = drive(m, msg)

	if m.traceLogLoading {
		t.Error("traceLogLoading should be false after load")
	}
	if m.traceLog == nil {
		t.Fatal("traceLog should not be nil after real load")
	}
	if len(m.traceLog.traces) < 2 {
		t.Fatalf("expected ≥2 traces from DB, got %d", len(m.traceLog.traces))
	}

	view := m.View().Content
	for _, want := range []string{
		"omni-integration-canary",
		"ok",
		"error",
		"integration test reason alpha",
		"integration test reason beta",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("View() missing %q\nfull view:\n%s", want, view)
		}
	}

	m = drive(m, pressEsc())
	if m.traceLog != nil {
		t.Error("traceLog should be nil after Esc")
	}
	if m.mode != viewSettings {
		t.Errorf("mode = %v after Esc, want viewSettings", m.mode)
	}

	_ = a
}

// Shadows the per-test App built by newDotsModelForCmds so callers can reach m.app for direct DB operations.
var _ *app.App // keep the app import used

func TestTraceLog_RendersStdoutFromTheDatabase(t *testing.T) {
	m, _ := newDotsModelForCmds(t)
	if err := m.app.DB().RecordCommandTrace(context.Background(), &database.CommandTrace{
		StartedAt: time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC),
		Command:   "apm install -g",
		Status:    "failed",
		Stdout:    "[i] Installing to user scope\n[x] Install failed after 0.0s.",
		Stderr:    "stderr detail",
	}); err != nil {
		t.Fatalf("record command trace: %v", err)
	}
	m.mode = viewSettings
	m.settingsCursor = settingsRowTraceLog
	m.dangerConfirmRow = -1
	m = drive(m, pressEnter())
	m = drive(m, m.doLoadTraces()())

	plain := stripANSIEscapeSequences(m.View().Content)
	for _, want := range []string{"stdout:", "[x] Install failed after 0.0s.", "stderr detail"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("trace log missing %q:\n%s", want, plain)
		}
	}
}
