package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func cmdQuits(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	switch msg := cmd().(type) {
	case tea.QuitMsg:
		return true
	case tea.BatchMsg:
		for _, c := range msg {
			if cmdQuits(c) {
				return true
			}
		}
	}
	return false
}

// Background work must not block either step of the two-step quit, and the armed state has to show in the footer — a silent first press is indistinguishable from a dead key.
func TestQuitFromTopLevelViewsWhileBackgroundWorkRuns(t *testing.T) {
	t.Parallel()
	for _, mode := range []viewMode{viewStatus, viewList, viewSettings, viewSkills, viewDots, viewGroups} {
		m := baseModel(nil)
		m.mode = mode
		m.loading = true
		m.doctorRunning = true
		m.skillsRunning = true
		m.pluginRunning = true

		model, cmd := m.Update(pressRune('q'))
		armed := model.(Model)
		if !armed.confirmQuit {
			t.Fatalf("mode %v: first q should arm the quit confirmation", mode)
		}
		if cmdQuits(cmd) {
			t.Fatalf("mode %v: first q should not quit outright", mode)
		}
		footer := stripANSIEscapeSequences(renderStatusBar(armed))
		if !strings.Contains(footer, "press q again to quit") {
			t.Fatalf("mode %v: armed footer = %q, want the press-again hint", mode, footer)
		}

		_, cmd = armed.Update(pressRune('q'))
		if !cmdQuits(cmd) {
			t.Fatalf("mode %v: second q should quit even with background loads in flight", mode)
		}
	}
}

// A modal that opens on its own must not be able to trap ctrl+c: q may belong to the modal, but the interrupt key still reaches quit.
func TestQuitFromModalStates(t *testing.T) {
	t.Parallel()
	ctrlC := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	for _, tc := range []struct {
		name string
		open func(*Model)
	}{
		{"drift prompt", func(m *Model) { m.agentsDriftPromptOpen = true }},
		{"armed batch resolve", func(m *Model) { m.agentsDriftPromptOpen = true; m.agentsBulkResolveConfirm = true }},
	} {
		m := baseModel(nil)
		m.mode = viewSkills
		tc.open(&m)

		model, cmd := m.Update(ctrlC)
		armed := model.(Model)
		if !armed.confirmQuit {
			t.Fatalf("%s: ctrl+c should arm the quit confirmation", tc.name)
		}
		if cmdQuits(cmd) {
			t.Fatalf("%s: the first ctrl+c should not quit outright", tc.name)
		}
		if _, cmd = armed.Update(ctrlC); !cmdQuits(cmd) {
			t.Fatalf("%s: the second ctrl+c should quit", tc.name)
		}
	}
}

// A focused text input owns the key: q types instead of quitting.
func TestQuitIsTypedIntoAFocusedInput(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSearch
	m.filter.Focus()

	model, cmd := m.Update(pressRune('q'))
	got := model.(Model)
	if got.confirmQuit || cmdQuits(cmd) {
		t.Fatal("q inside a focused search input should not touch the quit confirmation")
	}
	if got.filter.Value() != "q" {
		t.Fatalf("filter value = %q, want the typed rune", got.filter.Value())
	}
}
