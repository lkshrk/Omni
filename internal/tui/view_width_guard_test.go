package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/dots"
)

// The dashboard state the live sweep hit: doctor still running, agents still loading, and every badge at its longest.
func worstCaseDashboardModel(width int) Model {
	m := baseModel(nil)
	m.mode = viewStatus
	m.width = width
	m.height = 40
	m.doctorRunning = true
	m.apmRunning = true
	m.setSettings(config.Settings{DotsRepo: ""})
	m.dotsEntries = []app.DotStatus{{Name: "zsh", State: dots.StateSynced, Health: app.HealthOK}}
	m.doctorResult = &app.DoctorResult{
		Checks: []app.DoctorCheck{
			{ID: "dots", Label: "Dotfiles", Status: app.DoctorStatusWarn, Message: "dotfiles need attention"},
			{ID: "agents", Label: "Agent features", Status: app.DoctorStatusWarn, Message: "agents need attention"},
			{ID: "config", Label: "Config", Status: app.DoctorStatusFail, Message: "settings.json is unreadable"},
		},
		Summary: app.DoctorSummary{OK: 7, Warn: 2, Fail: 1},
	}
	return m
}

func assertNoLineExceedsWidth(t *testing.T, out string, width int) {
	t.Helper()
	for i, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > width {
			t.Fatalf("line %d is %d cells wide, terminal is %d: %q", i, w, width, stripANSIEscapeSequences(line))
		}
	}
}

func TestDashboardRowsNeverExceedTerminalWidth(t *testing.T) {
	t.Parallel()
	for _, width := range []int{24, 32, 40, 60, 80, 120, 200} {
		m := worstCaseDashboardModel(width)
		assertNoLineExceedsWidth(t, renderStatus(m), width)
		for i := range statusRows(m) {
			m.statusCursor = i
			assertNoLineExceedsWidth(t, renderStatus(m), width)
		}
	}
}

func TestViewNeverEmitsLinesWiderThanTerminal(t *testing.T) {
	t.Parallel()
	for _, width := range []int{24, 40, 80, 120} {
		m := worstCaseDashboardModel(width)
		for _, mode := range []viewMode{viewStatus, viewList, viewDots, viewSettings, viewSkills, viewGroups} {
			m.mode = mode
			assertNoLineExceedsWidth(t, m.View().Content, width)
		}
	}
}

// First-run onboarding composes its popup over a live dashboard background, so the worst-case doctor badge reaches the frame through a different path than the plain tabs.
func TestSetupViewNeverEmitsLinesWiderThanTerminal(t *testing.T) {
	t.Parallel()
	for _, width := range []int{24, 32, 40, 60, 80, 120} {
		for _, background := range []viewMode{viewStatus, viewList, viewDots, viewSkills} {
			for step := range setupStepMax + 1 {
				m := worstCaseDashboardModel(width)
				m.mode = viewSetup
				m.setupBackgroundMode = background
				m.setupStep = step
				assertNoLineExceedsWidth(t, m.View().Content, width)
			}
		}
	}
}

// The first async doctor/agents results can land before the first WindowSizeMsg; without a fallback size those frames compose against width 0.
func TestNewModelStartsWithUsableTerminalSize(t *testing.T) {
	t.Parallel()
	m := New(t.Context(), nil)
	if m.width != defaultTerminalWidth || m.height != defaultTerminalHeight {
		t.Fatalf("new model size = %dx%d, want %dx%d", m.width, m.height, defaultTerminalWidth, defaultTerminalHeight)
	}
}
