package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func TestAgentsUse_RowMeta(t *testing.T) {
	t.Parallel()
	row := settingsRows[settingsRowAgentsUse]
	if row.label != "Agents" {
		t.Errorf("label = %q, want %q", row.label, "Agents")
	}
	if row.section != "Agents" {
		t.Errorf("section = %q, want %q", row.section, "Agents")
	}
}

func TestAgentsUse_EditorRenderWithRows(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.editingAgents = true
	m.agentsEditRows = []app.AgentPickerRow{
		{ID: "claude-code", Display: "Claude Code", Enabled: true},
		{ID: "cursor", Display: "Cursor", Enabled: false},
		{ID: "codex", Display: "Codex", Enabled: true},
	}
	m.agentsEditCursor = 0
	m.width = 120

	out := stripANSIEscapeSequences(renderSettingsAgentsEditor(m))

	for _, display := range []string{"Claude Code", "Cursor", "Codex"} {
		if !strings.Contains(out, display) {
			t.Errorf("render missing %q", display)
		}
	}
	// Enabled rows show [x], disabled rows show [ ]
	lines := strings.Split(out, "\n")
	checkMark := func(display, wantMark string) {
		for _, line := range lines {
			if strings.Contains(line, display) {
				if !strings.Contains(line, wantMark) {
					t.Errorf("line for %q = %q, want mark %q", display, line, wantMark)
				}
				return
			}
		}
		t.Errorf("no line found containing %q", display)
	}
	checkMark("Claude Code", "[x]")
	checkMark("Cursor", "[ ]")
	checkMark("Codex", "[x]")
}

func TestAgentsUse_EditorEmptyState(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.editingAgents = true
	m.agentsEditRows = nil
	m.width = 120

	out := stripANSIEscapeSequences(renderSettingsAgentsEditor(m))
	if !strings.Contains(out, "No supported agents detected") {
		t.Errorf("empty-state render = %q, want 'No supported agents detected'", out)
	}
}

func TestAgentsUse_SpaceToggles(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSettings
	m.settingsCursor = settingsRowAgentsUse
	m.editingAgents = true
	m.agentsEditRows = []app.AgentPickerRow{
		{ID: "claude-code", Display: "Claude Code", Enabled: false},
		{ID: "cursor", Display: "Cursor", Enabled: false},
	}
	m.agentsEditCursor = 0

	got := drive(m, pressRune(' '))
	if !got.agentsEditRows[0].Enabled {
		t.Error("agentsEditRows[0].Enabled should be true after space")
	}
	if got.agentsEditRows[1].Enabled {
		t.Error("agentsEditRows[1].Enabled should remain false")
	}
}

func TestAgentsUse_CursorMove(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSettings
	m.settingsCursor = settingsRowAgentsUse
	m.editingAgents = true
	m.agentsEditRows = []app.AgentPickerRow{
		{ID: "a", Display: "A"},
		{ID: "b", Display: "B"},
		{ID: "c", Display: "C"},
	}
	m.agentsEditCursor = 0

	// j moves down
	got := drive(m, pressRune('j'))
	if got.agentsEditCursor != 1 {
		t.Errorf("agentsEditCursor after j = %d, want 1", got.agentsEditCursor)
	}

	// k moves up
	got = drive(got, pressRune('k'))
	if got.agentsEditCursor != 0 {
		t.Errorf("agentsEditCursor after k = %d, want 0", got.agentsEditCursor)
	}

	// up at 0 clamps
	got = drive(got, tea.KeyPressMsg{Code: tea.KeyUp})
	if got.agentsEditCursor != 0 {
		t.Errorf("agentsEditCursor after up at 0 = %d, want 0 (clamped)", got.agentsEditCursor)
	}

	// down arrow also works
	got = drive(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if got.agentsEditCursor != 1 {
		t.Errorf("agentsEditCursor after down arrow = %d, want 1", got.agentsEditCursor)
	}
}

func TestAgentsUse_EscCancels(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSettings
	m.settingsCursor = settingsRowAgentsUse
	m.editingAgents = true
	m.agentsEditRows = []app.AgentPickerRow{
		{ID: "claude-code", Display: "Claude Code", Enabled: true},
	}

	got := drive(m, pressEsc())
	if got.editingAgents {
		t.Error("editingAgents should be false after esc")
	}
}

func TestAgentsUse_EnterExitsEditor(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSettings
	m.settingsCursor = settingsRowAgentsUse
	m.editingAgents = true
	m.agentsEditRows = []app.AgentPickerRow{
		{ID: "claude-code", Display: "Claude Code", Enabled: true},
	}

	got := drive(m, pressEnter())
	if got.editingAgents {
		t.Error("editingAgents should be false after enter")
	}
}

func TestAgentsUse_SavedMsgSetsSettings(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	got := drive(m, agentsUseSavedMsg{ids: []string{"codex"}})
	if len(got.settings.AgentsUse) != 1 || got.settings.AgentsUse[0] != "codex" {
		t.Errorf("settings.AgentsUse = %v, want [codex]", got.settings.AgentsUse)
	}
}

func TestAgentsUse_ValFn(t *testing.T) {
	t.Parallel()
	t.Run("nil AgentsUse contains 'all detected'", func(t *testing.T) {
		m := baseModel(nil)
		m.settings.AgentsUse = nil
		out := stripANSIEscapeSequences(settingsAgentsUseVal(m))
		if !strings.Contains(out, "all detected") {
			t.Errorf("val = %q, want 'all detected'", out)
		}
	})

	t.Run("empty AgentsUse contains 'none'", func(t *testing.T) {
		m := baseModel(nil)
		m.settings.AgentsUse = []string{}
		out := stripANSIEscapeSequences(settingsAgentsUseVal(m))
		if !strings.Contains(out, "none") {
			t.Errorf("val = %q, want 'none'", out)
		}
	})

	t.Run("non-empty AgentsUse contains IDs", func(t *testing.T) {
		m := baseModel(nil)
		m.settings.AgentsUse = []string{"codex", "cursor"}
		out := stripANSIEscapeSequences(settingsAgentsUseVal(m))
		if !strings.Contains(out, "codex") {
			t.Errorf("val = %q, want 'codex'", out)
		}
		if !strings.Contains(out, "cursor") {
			t.Errorf("val = %q, want 'cursor'", out)
		}
	})
}

// TestDoToggleAgents_NilAppReturnsNil verifies the nil-app guard.
func TestDoToggleAgents_NilAppReturnsNil(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.app = nil
	if cmd := m.doToggleAgents(); cmd != nil {
		t.Error("doToggleAgents with nil app should return nil cmd")
	}
}

// TestDoToggleAgents_SuccessFlipsEnabled verifies the toggle persists the
// per-host agents_disabled flag and reports the flipped enabled state.
func TestDoToggleAgents_SuccessFlipsEnabled(t *testing.T) {
	t.Parallel()
	a := newScanPlanTestApp(t)
	m := baseModel(nil)
	m.app = a

	// enabled → disable
	m.agentsEnabled = true
	msg := m.doToggleAgents()()
	got, ok := msg.(agentsToggledMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want agentsToggledMsg", msg)
	}
	if got.err != nil {
		t.Fatalf("err = %v, want nil", got.err)
	}
	if got.enabled {
		t.Error("enabled = true, want false after toggling an enabled model")
	}
	cfg, err := a.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !config.BoolVal(cfg.HostSettings[shortHostname()].AgentsDisabled) {
		t.Error("expected agents_disabled=true persisted to host settings")
	}

	// disabled → enable
	m.agentsEnabled = false
	got2 := m.doToggleAgents()().(agentsToggledMsg)
	if got2.err != nil {
		t.Fatalf("err = %v, want nil", got2.err)
	}
	if !got2.enabled {
		t.Error("enabled = false, want true after toggling a disabled model")
	}
}

// TestDoToggleAgents_ErrorPropagates verifies a config save failure lands in
// agentsToggledMsg.err.
func TestDoToggleAgents_ErrorPropagates(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.app = newBrokenConfigApp(t)
	m.agentsEnabled = true

	got, ok := m.doToggleAgents()().(agentsToggledMsg)
	if !ok {
		t.Fatal("cmd() should return agentsToggledMsg")
	}
	if got.err == nil {
		t.Error("expected an error when the config path is unreadable")
	}
}
