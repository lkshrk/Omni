package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func pluginChipFixture(rows []app.PluginRow) Model {
	m := agentsAllModel(nil, nil, rows)
	m.skillTypeIdx = agentsChipPlugin
	m.pluginCursor = 0
	return m
}

func TestPluginFlow_NOpensAddFormWithNameFocused(t *testing.T) {
	t.Parallel()
	m := pluginChipFixture(nil)

	m = drive(m, pressRune('n'))

	if !m.pluginFormOpen {
		t.Fatal("expected pluginFormOpen after 'n'")
	}
	if m.pluginFormField != 0 {
		t.Errorf("pluginFormField = %d, want 0 (name)", m.pluginFormField)
	}
	if !m.pluginFormName.Focused() {
		t.Error("expected name field focused on open")
	}
}

func TestPluginFlow_NResetsFieldsFromPreviousSession(t *testing.T) {
	t.Parallel()
	m := pluginChipFixture(nil)
	m = drive(m, pressRune('n'))
	m.pluginFormName.SetValue("half-typed")
	m = drive(m, pressEsc())

	m = drive(m, pressRune('n'))

	if m.pluginFormName.Value() != "" {
		t.Errorf("reopened form should have blank name, got %q", m.pluginFormName.Value())
	}
	if m.pluginFormField != 0 {
		t.Errorf("reopened form field = %d, want 0", m.pluginFormField)
	}
}

func TestPluginFlow_TabCyclesFieldsForwardAndWraps(t *testing.T) {
	t.Parallel()
	m := pluginChipFixture(nil)
	m = drive(m, pressRune('n'))

	wantOrder := []int{1, 2, 3, 0}
	for i, want := range wantOrder {
		m = drive(m, pressTab())
		if m.pluginFormField != want {
			t.Fatalf("after tab #%d, pluginFormField = %d, want %d", i+1, m.pluginFormField, want)
		}
	}
}

func TestPluginFlow_EmptyNameShowsValidationError(t *testing.T) {
	t.Parallel()
	m := pluginChipFixture(nil)
	m = drive(m, pressRune('n'))

	newModel, cmd := m.Update(pressEnter())
	m = newModel.(Model)

	if m.pluginFormErr == nil {
		t.Fatal("expected pluginFormErr when name is empty")
	}
	if !strings.Contains(m.pluginFormErr.Error(), "name is required") {
		t.Errorf("pluginFormErr = %v, want 'name is required'", m.pluginFormErr)
	}
	if cmd != nil {
		t.Error("expected no command when validation fails")
	}
}

func TestPluginFlow_EmptyOriginShowsValidationError(t *testing.T) {
	t.Parallel()
	m := pluginChipFixture(nil)
	m = drive(m, pressRune('n'))
	m.pluginFormName.SetValue("my-plugin")

	newModel, cmd := m.Update(pressEnter())
	m = newModel.(Model)

	if m.pluginFormErr == nil {
		t.Fatal("expected pluginFormErr when origin is empty")
	}
	if !strings.Contains(m.pluginFormErr.Error(), "exactly one of marketplace or source") {
		t.Errorf("pluginFormErr = %v, want origin validation", m.pluginFormErr)
	}
	if m.pluginRunning {
		t.Error("pluginRunning should remain false when validation fails")
	}
	if cmd != nil {
		t.Error("expected no command when validation fails")
	}
}

func TestPluginFlow_DirectSourceBuildsExpectedPlugin(t *testing.T) {
	t.Parallel()
	m := pluginChipFixture(nil)
	m.pluginFormName.SetValue("hermes-plugin")
	m.pluginFormSource.SetValue("owner/repo")
	m.pluginFormAgents.SetValue("hermes-agent")

	got, err := m.buildPluginFromForm()
	if err != nil {
		t.Fatal(err)
	}
	want := config.Plugin{Name: "hermes-plugin", Source: "owner/repo", Agents: []string{"hermes-agent"}}
	if got.Name != want.Name || got.Source != want.Source || len(got.Agents) != 1 || got.Agents[0] != "hermes-agent" {
		t.Fatalf("buildPluginFromForm = %+v, want %+v", got, want)
	}
}

func TestPluginFlow_ValidSubmitBuildsExpectedPluginAndQueuesSpinnerTick(t *testing.T) {
	t.Parallel()
	m := pluginChipFixture(nil)
	m = drive(m, pressRune('n'))
	m.pluginFormName.SetValue("my-plugin")
	m.pluginFormMarketplace.SetValue("acme-market")
	m.pluginFormAgents.SetValue("codex, claude")

	newModel, cmd := m.Update(pressEnter())
	m = newModel.(Model)

	if m.pluginFormErr != nil {
		t.Fatalf("unexpected pluginFormErr on valid submit: %v", m.pluginFormErr)
	}
	if !m.pluginRunning {
		t.Fatal("expected pluginRunning=true after valid submit")
	}
	if cmd == nil {
		t.Fatal("expected a command batch after valid submit")
	}
	if !batchHasSpinnerTick(m.spinner.Tick, cmd) {
		t.Error("command batch missing spinner tick after valid plugin add submit")
	}

	got, err := m.buildPluginFromForm()
	if err != nil {
		t.Fatalf("buildPluginFromForm error: %v", err)
	}
	want := config.Plugin{Name: "my-plugin", Marketplace: "acme-market", Agents: []string{"codex", "claude"}}
	if got.Name != want.Name || got.Marketplace != want.Marketplace || len(got.Agents) != len(want.Agents) {
		t.Fatalf("buildPluginFromForm = %+v, want %+v", got, want)
	}
	for i := range want.Agents {
		if got.Agents[i] != want.Agents[i] {
			t.Fatalf("buildPluginFromForm.Agents = %v, want %v", got.Agents, want.Agents)
		}
	}
}

func TestPluginFlow_DeleteConfirmTwoStep(t *testing.T) {
	t.Parallel()
	t.Run("first d arms confirm", func(t *testing.T) {
		m := pluginChipFixture([]app.PluginRow{{Name: "arm-plugin"}})

		m = drive(m, pressRune('d'))

		if !m.pluginDeleteConfirm || m.pluginDeleteName != "arm-plugin" {
			t.Fatalf("pluginDeleteConfirm=%v pluginDeleteName=%q, want armed for arm-plugin", m.pluginDeleteConfirm, m.pluginDeleteName)
		}
	})

	t.Run("second d confirms and triggers removal", func(t *testing.T) {
		m := pluginChipFixture([]app.PluginRow{{Name: "confirm-plugin"}})
		m = drive(m, pressRune('d'))

		newModel, cmd := m.Update(pressRune('d'))
		m = newModel.(Model)

		if m.pluginDeleteConfirm {
			t.Error("pluginDeleteConfirm should be disarmed after second 'd' confirms")
		}
		if !m.pluginRunning {
			t.Error("pluginRunning should be true after confirming delete")
		}
		if cmd == nil {
			t.Fatal("expected a command batch after confirming delete")
		}
	})

	t.Run("esc disarms confirm without triggering removal", func(t *testing.T) {
		m := pluginChipFixture([]app.PluginRow{{Name: "disarm-plugin"}})

		m = drive(m, pressRune('d'), pressEsc())

		if m.pluginDeleteConfirm {
			t.Error("pluginDeleteConfirm should be false after esc")
		}
		if m.pluginRunning {
			t.Error("pluginRunning should remain false after esc cancel")
		}
	})
}

func TestPluginFlow_AgentsPickerOpensWithTargetingFromRow(t *testing.T) {
	t.Parallel()
	rows := []app.PluginRow{
		{
			Name:        "picker-plugin",
			Marketplace: "acme",
			Agents:      []string{"codex"},
			PerAgentStatus: map[string]app.PluginStatus{
				"codex":  app.PluginStatusInstalled,
				"claude": app.PluginStatusMissing,
			},
		},
	}
	m := pluginChipFixture(rows)

	m = drive(m, pressRune('a'))

	if !m.pluginAgentsPicker {
		t.Fatal("pluginAgentsPicker should be true after 'a'")
	}
	if m.skillAgentsPicker {
		t.Error("skillAgentsPicker should NOT be opened by plugin 'a'")
	}
	if m.pluginAgentsRow.Name != "picker-plugin" {
		t.Errorf("pluginAgentsRow.Name = %q, want picker-plugin", m.pluginAgentsRow.Name)
	}

	var codexTargeted, claudeTargeted bool
	for _, r := range m.skillAgentsRows {
		if r.ID == "codex" {
			codexTargeted = r.Targeted
		}
		if r.ID == "claude" {
			claudeTargeted = r.Targeted
		}
	}
	if !codexTargeted {
		t.Error("codex should be targeted (present in row.Agents)")
	}
	if claudeTargeted {
		t.Error("claude should not be targeted (absent from row.Agents)")
	}
}

func TestPluginFlow_AgentsPickerConfirmSavesAndCloses(t *testing.T) {
	t.Parallel()
	rows := []app.PluginRow{
		{
			Name:        "save-plugin",
			Marketplace: "acme",
			Agents:      []string{"codex"},
			PerAgentStatus: map[string]app.PluginStatus{
				"codex":  app.PluginStatusInstalled,
				"claude": app.PluginStatusMissing,
			},
		},
	}
	m := pluginChipFixture(rows)
	m = drive(m, pressRune('a'))

	newModel, cmd := m.Update(pressEnter())
	m = newModel.(Model)

	if m.pluginAgentsPicker {
		t.Error("pluginAgentsPicker should close on confirm")
	}
	if !m.pluginRunning {
		t.Error("pluginRunning should be true after confirming the plugin agents picker")
	}
	if cmd == nil {
		t.Fatal("expected a command batch after confirming the plugin agents picker")
	}
}

func TestPluginFlow_AgentsPickerEscCancelsWithoutSaving(t *testing.T) {
	t.Parallel()
	rows := []app.PluginRow{
		{Name: "esc-plugin", PerAgentStatus: map[string]app.PluginStatus{"codex": app.PluginStatusInstalled}},
	}
	m := pluginChipFixture(rows)

	m = drive(m, pressRune('a'), pressEsc())

	if m.pluginAgentsPicker {
		t.Error("pluginAgentsPicker should be false after esc")
	}
	if m.pluginRunning {
		t.Error("pluginRunning should remain false after esc cancel (no save triggered)")
	}
}

func TestPluginFlow_RestoreKeySetsRunningFlag(t *testing.T) {
	t.Parallel()
	m := pluginChipFixture([]app.PluginRow{{Name: "restore-plugin"}})

	m = drive(m, pressRune('r'))

	if !m.pluginRunning {
		t.Error("pluginRunning should be true after 'r'")
	}
	if m.pluginErr != nil {
		t.Errorf("pluginErr should be reset to nil, got %v", m.pluginErr)
	}
}

// The plugin add form has only name/marketplace/agents: no transport concept, no left/right handling, and no URL-vs-command branch.

// Drives a real tea.KeyPressMsg with Mod: tea.ModShift through Model.Update, not a helper shortcut, to prove shift+tab actually reverses field focus.
func TestPluginFlow_ShiftTabNavigatesBackwardThroughUpdate(t *testing.T) {
	t.Parallel()
	m := pluginChipFixture(nil)
	m = drive(m, pressRune('n'))
	if m.pluginFormField != 0 {
		t.Fatalf("pluginFormField = %d, want 0 (name) on open", m.pluginFormField)
	}

	newModel, _ := m.Update(pressTab())
	m = newModel.(Model)
	if m.pluginFormField != 1 {
		t.Fatalf("pluginFormField = %d, want 1 after forward tab", m.pluginFormField)
	}

	newModel, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	m = newModel.(Model)
	if m.pluginFormField != 0 {
		t.Fatalf("pluginFormField = %d, want 0 (back to original) after shift+tab", m.pluginFormField)
	}
	if !m.pluginFormName.Focused() {
		t.Error("expected name field re-focused after shift+tab back to field 0")
	}
}

// pluginAddDoneMsg with a nil err while the form is open must close the form, clear any form error, and queue doLoadPluginRows.
func TestPluginFlow_AddDoneMsgSuccessClosesFormAndReloads(t *testing.T) {
	t.Parallel()
	m := pluginChipFixture(nil)
	m = drive(m, pressRune('n'))
	m.pluginFormName.SetValue("nav-focus-plugin")
	m.pluginFormMarketplace.SetValue("acme-market")
	m.pluginRunning = true

	newModel, cmd := m.Update(pluginAddDoneMsg{err: nil})
	m = newModel.(Model)

	if m.pluginFormOpen {
		t.Error("pluginFormOpen should be false after successful pluginAddDoneMsg")
	}
	if m.pluginFormErr != nil {
		t.Errorf("pluginFormErr should be nil after success, got %v", m.pluginFormErr)
	}
	if !m.pluginRunning {
		t.Error("pluginRunning should stay true after successful pluginAddDoneMsg until the row reload lands")
	}
	if m.pluginFormName.Value() != "" {
		t.Errorf("form should be reset after success, pluginFormName = %q", m.pluginFormName.Value())
	}
	if cmd == nil {
		t.Fatal("expected a reload command after successful add")
	}

	m = drive(m, pluginRowsMsg{})
	if m.pluginRunning {
		t.Error("pluginRunning should be false once pluginRowsMsg lands")
	}
}

// A non-nil err while the form is open must keep the form open and set pluginFormErr, not the global statusMsg.
func TestPluginFlow_AddDoneMsgErrorKeepsFormOpenWithError(t *testing.T) {
	t.Parallel()
	m := pluginChipFixture(nil)
	m = drive(m, pressRune('n'))
	m.pluginFormName.SetValue("nav-focus-plugin")
	m.pluginFormMarketplace.SetValue("acme-market")
	m.pluginRunning = true

	newModel, _ := m.Update(pluginAddDoneMsg{err: errors.New("add failed: name already exists")})
	m = newModel.(Model)

	if !m.pluginFormOpen {
		t.Error("pluginFormOpen should remain true after failed pluginAddDoneMsg")
	}
	if m.pluginFormErr == nil {
		t.Fatal("expected pluginFormErr to be set after failed pluginAddDoneMsg")
	}
	if got := m.pluginFormErr.Error(); got != "add failed: name already exists" {
		t.Errorf("pluginFormErr = %q, want %q", got, "add failed: name already exists")
	}
	if m.pluginRunning {
		t.Error("pluginRunning should be false after pluginAddDoneMsg resolves")
	}
	if m.pluginFormName.Value() != "nav-focus-plugin" {
		t.Errorf("form fields should be retained on error, pluginFormName = %q", m.pluginFormName.Value())
	}
	if m.statusMsg != "" {
		t.Errorf("statusMsg should stay empty; error belongs in pluginFormErr while form is open, got %q", m.statusMsg)
	}
}

// Asserts on the queued batch rather than executing doImportPlugin against a nil app, mirroring the mcp import test.
func TestPluginFlow_ClaimKeyOnUnmanagedRowQueuesAdoptCommand(t *testing.T) {
	t.Parallel()
	m := pluginChipFixture(nil)
	m.pluginUnmanaged = map[string][]app.InstalledPlugin{
		"codex": {{Name: "unmanaged-plugin-pkg", Marketplace: "acme"}},
	}
	// No managed rows, so cursor 0 lands directly on the single unmanaged entry.
	m.pluginCursor = 0

	newModel, cmd := m.Update(pressRune('c'))
	m = newModel.(Model)

	if m.mode != viewGroupPicker {
		t.Errorf("expected 'c' on an unmanaged row to open the group picker, mode = %v", m.mode)
	}
	if !m.pickerPurposeClaim {
		t.Error("expected the opened picker to be in claim mode")
	}
	if cmd != nil {
		t.Fatal("expected opening the group picker to dispatch no command")
	}
}

// pluginImportAdoptDoneMsg's handler used to never clear agentsOpKey, leaving the claimed row's spinner stuck whenever AddPlugin/SetPluginGroups failed.
func TestPluginFlow_ImportAdoptErrorClearsOpKeyAndSurfacesStatus(t *testing.T) {
	t.Parallel()
	m := pluginChipFixture(nil)
	m.startAgentsOp("stuck-key")
	m.pluginRunning = true

	newModel, _ := m.Update(pluginImportAdoptDoneMsg{pluginName: "plg", err: errors.New("boom")})
	got := newModel.(Model)

	if got.agentsOpKey != "" {
		t.Errorf("agentsOpKey after import error = %q, want cleared", got.agentsOpKey)
	}
	if got.pluginRunning {
		t.Error("pluginRunning should be false after the op completes")
	}
	if !got.statusIsErr || got.statusMsg == "" {
		t.Errorf("status=%q isErr=%v, want a surfaced error", got.statusMsg, got.statusIsErr)
	}
}

func TestPluginFlow_ImportKeyOnUnmanagedRowIsInert(t *testing.T) {
	t.Parallel()
	m := pluginChipFixture(nil)
	m.pluginUnmanaged = map[string][]app.InstalledPlugin{
		"codex": {{Name: "unmanaged-plugin-pkg", Marketplace: "acme"}},
	}
	m.pluginCursor = 0

	newModel, cmd := m.Update(pressRune('i'))
	m = newModel.(Model)

	if m.pluginRunning {
		t.Error("pluginRunning should stay false after 'i' on an unmanaged row (import key retired)")
	}
	if cmd != nil {
		t.Error("expected a nil command after 'i' on an unmanaged row")
	}
}

// setStatus writes statusMsg synchronously and neither opening the add form nor moving the cursor clears it, unlike pluginFormErr which resets on form open.
func TestPluginFlow_GroupsStatusMessagePersistsUntilReplaced(t *testing.T) {
	t.Parallel()
	rows := []app.PluginRow{{Name: "groups-status-plugin", Groups: []string{"work"}}}
	m := pluginChipFixture(rows)

	m = drive(m, pressRune('g'))

	if m.statusMsg == "" {
		t.Fatal("expected statusMsg to be set by 'g'")
	}
	wantSubstr := "groups-status-plugin groups: work"
	if m.statusMsg != wantSubstr {
		t.Fatalf("statusMsg = %q, want %q", m.statusMsg, wantSubstr)
	}

	m = drive(m, pressRune('n'))
	if m.statusMsg != wantSubstr {
		t.Errorf("statusMsg changed after opening add form: got %q, want unchanged %q", m.statusMsg, wantSubstr)
	}
	m = drive(m, pressEsc())

	m = drive(m, pressRune('j'))
	if m.statusMsg != wantSubstr {
		t.Errorf("statusMsg changed after cursor move: got %q, want unchanged %q", m.statusMsg, wantSubstr)
	}
}
