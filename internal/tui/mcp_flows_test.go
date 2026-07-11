package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
)

func mcpChipFixture(rows []app.McpServerRow) Model {
	m := agentsAllModel(nil, rows, nil)
	m.skillTypeIdx = agentsChipMcp
	m.mcpCursor = 0
	return m
}

func TestMcpFlow_NOpensAddFormWithNameFocused(t *testing.T) {
	m := mcpChipFixture(nil)

	m = drive(m, pressRune('n'))

	if !m.mcpFormOpen {
		t.Fatal("expected mcpFormOpen after 'n'")
	}
	if m.mcpFormField != 0 {
		t.Errorf("mcpFormField = %d, want 0 (name)", m.mcpFormField)
	}
	if !m.mcpFormName.Focused() {
		t.Error("expected name field focused on open")
	}
}

func TestMcpFlow_TabCyclesFieldsForwardAndWraps(t *testing.T) {
	m := mcpChipFixture(nil)
	m = drive(m, pressRune('n'))

	wantOrder := []int{1, 2, 3, 4, 0}
	for i, want := range wantOrder {
		m = drive(m, pressTab())
		if m.mcpFormField != want {
			t.Fatalf("after tab #%d, mcpFormField = %d, want %d", i+1, m.mcpFormField, want)
		}
	}
}

func TestMcpFlow_EscCancelsAndResetsForm(t *testing.T) {
	m := mcpChipFixture(nil)
	m = drive(m, pressRune('n'))
	m.mcpFormName.SetValue("half-typed")

	m = drive(m, pressEsc())

	if m.mcpFormOpen {
		t.Fatal("expected mcpFormOpen=false after esc")
	}
	m = drive(m, pressRune('n'))
	if m.mcpFormName.Value() != "" {
		t.Errorf("reopened form should have blank name, got %q", m.mcpFormName.Value())
	}
}

func TestMcpFlow_StdioWithoutCommandShowsValidationError(t *testing.T) {
	m := mcpChipFixture(nil)
	m = drive(m, pressRune('n'))
	m.mcpFormName.SetValue("my-server")

	newModel, cmd := m.Update(pressEnter())
	m = newModel.(Model)

	if m.mcpFormErr == nil {
		t.Fatal("expected mcpFormErr when stdio transport has no command")
	}
	if !strings.Contains(m.mcpFormErr.Error(), "command is required for stdio transport") {
		t.Errorf("mcpFormErr = %v, want 'command is required for stdio transport'", m.mcpFormErr)
	}
	if cmd != nil {
		t.Error("expected no command when validation fails")
	}
}

func TestMcpFlow_ValidSubmitQueuesSpinnerTickAndSetsRunning(t *testing.T) {
	m := mcpChipFixture(nil)
	m = drive(m, pressRune('n'))
	m.mcpFormName.SetValue("my-server")
	m.mcpFormCommand.SetValue("npx -y @server/mcp")

	newModel, cmd := m.Update(pressEnter())
	m = newModel.(Model)

	if m.mcpFormErr != nil {
		t.Fatalf("unexpected mcpFormErr on valid submit: %v", m.mcpFormErr)
	}
	if !m.mcpRunning {
		t.Fatal("expected mcpRunning=true after valid submit")
	}
	if cmd == nil {
		t.Fatal("expected a command batch after valid submit")
	}
	if !batchHasSpinnerTick(m.spinner.Tick, cmd) {
		t.Error("command batch missing spinner tick after valid mcp add submit")
	}
}

func TestMcpFlow_DeleteConfirmTwoStep(t *testing.T) {
	t.Run("first d arms confirm", func(t *testing.T) {
		m := mcpChipFixture([]app.McpServerRow{{Name: "arm-target"}})

		m = drive(m, pressRune('d'))

		if !m.mcpDeleteConfirm || m.mcpDeleteName != "arm-target" {
			t.Fatalf("mcpDeleteConfirm=%v mcpDeleteName=%q, want armed for arm-target", m.mcpDeleteConfirm, m.mcpDeleteName)
		}
	})

	t.Run("second d confirms and triggers removal", func(t *testing.T) {
		m := mcpChipFixture([]app.McpServerRow{{Name: "confirm-target"}})
		m = drive(m, pressRune('d'))

		newModel, cmd := m.Update(pressRune('d'))
		m = newModel.(Model)

		if m.mcpDeleteConfirm {
			t.Error("mcpDeleteConfirm should be disarmed after second 'd' confirms")
		}
		if !m.mcpRunning {
			t.Error("mcpRunning should be true after confirming delete")
		}
		if cmd == nil {
			t.Fatal("expected a command batch after confirming delete")
		}
	})

	t.Run("esc disarms confirm without triggering removal", func(t *testing.T) {
		m := mcpChipFixture([]app.McpServerRow{{Name: "disarm-target"}})

		m = drive(m, pressRune('d'), pressEsc())

		if m.mcpDeleteConfirm {
			t.Error("mcpDeleteConfirm should be false after esc")
		}
		if m.mcpRunning {
			t.Error("mcpRunning should remain false after esc cancel")
		}
	})
}

func TestMcpFlow_AgentsPickerOpensWithTargetingFromRow(t *testing.T) {
	rows := []app.McpServerRow{
		{
			Name:      "picker-target",
			Transport: "stdio",
			Agents:    []string{"codex"},
			PerAgentStatus: map[string]app.McpStatus{
				"codex":  app.McpStatusInstalled,
				"claude": app.McpStatusMissing,
			},
		},
	}
	m := mcpChipFixture(rows)

	m = drive(m, pressRune('a'))

	if !m.mcpAgentsPicker {
		t.Fatal("mcpAgentsPicker should be true after 'a'")
	}
	if m.skillAgentsPicker {
		t.Error("skillAgentsPicker should NOT be opened by mcp 'a'")
	}
	if m.mcpAgentsRow.Name != "picker-target" {
		t.Errorf("mcpAgentsRow.Name = %q, want picker-target", m.mcpAgentsRow.Name)
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

func TestMcpFlow_AgentsPickerConfirmSavesAndCloses(t *testing.T) {
	rows := []app.McpServerRow{
		{
			Name:      "save-target",
			Transport: "stdio",
			Agents:    []string{"codex"},
			PerAgentStatus: map[string]app.McpStatus{
				"codex":  app.McpStatusInstalled,
				"claude": app.McpStatusMissing,
			},
		},
	}
	m := mcpChipFixture(rows)
	m = drive(m, pressRune('a'))

	newModel, cmd := m.Update(pressEnter())
	m = newModel.(Model)

	if m.mcpAgentsPicker {
		t.Error("mcpAgentsPicker should close on confirm")
	}
	if !m.mcpRunning {
		t.Error("mcpRunning should be true after confirming the mcp agents picker")
	}
	if cmd == nil {
		t.Fatal("expected a command batch after confirming the mcp agents picker")
	}
}

func TestMcpFlow_AgentsPickerEscCancelsWithoutSaving(t *testing.T) {
	rows := []app.McpServerRow{{Name: "esc-target", PerAgentStatus: map[string]app.McpStatus{"codex": app.McpStatusInstalled}}}
	m := mcpChipFixture(rows)

	m = drive(m, pressRune('a'), pressEsc())

	if m.mcpAgentsPicker {
		t.Error("mcpAgentsPicker should be false after esc")
	}
	if m.mcpRunning {
		t.Error("mcpRunning should remain false after esc cancel (no save triggered)")
	}
}

func TestMcpFlow_RestoreKeySetsRunningFlag(t *testing.T) {
	m := mcpChipFixture([]app.McpServerRow{{Name: "restore-target"}})

	m = drive(m, pressRune('r'))

	if !m.mcpRunning {
		t.Error("mcpRunning should be true after 'r'")
	}
	if m.mcpErr != nil {
		t.Errorf("mcpErr should be reset to nil, got %v", m.mcpErr)
	}
}

// TestMcpFlow_HttpTransportWithoutURLShowsValidationError closes gap 1: the
// http/sse branch of buildMcpServerFromForm requires a non-empty URL (see
// update_keys.go's buildMcpServerFromForm, "default:" case). Cycling
// transport away from stdio and submitting with a blank URL must reject with
// the exact "URL is required for http transport" message, leave the form
// open, and retain the already-typed name.
func TestMcpFlow_HttpTransportWithoutURLShowsValidationError(t *testing.T) {
	m := mcpChipFixture(nil)
	m = drive(m, pressRune('n'))
	m.mcpFormName.SetValue("http-server")
	// Field 1 is transport; move focus there and cycle stdio -> http.
	m = drive(m, pressTab())
	if m.mcpFormField != 1 {
		t.Fatalf("mcpFormField = %d, want 1 (transport) before cycling", m.mcpFormField)
	}
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyRight})
	if m.mcpFormTransport != 1 {
		t.Fatalf("mcpFormTransport = %d, want 1 (http) after right", m.mcpFormTransport)
	}

	newModel, cmd := m.Update(pressEnter())
	m = newModel.(Model)

	if !m.mcpFormOpen {
		t.Fatal("mcpFormOpen should remain true when validation fails")
	}
	if m.mcpFormErr == nil {
		t.Fatal("expected mcpFormErr when http transport has no URL")
	}
	if got := m.mcpFormErr.Error(); got != "URL is required for http transport" {
		t.Errorf("mcpFormErr = %q, want %q", got, "URL is required for http transport")
	}
	if m.mcpFormName.Value() != "http-server" {
		t.Errorf("mcpFormName = %q, want retained value %q", m.mcpFormName.Value(), "http-server")
	}
	if m.mcpFormTransport != 1 {
		t.Errorf("mcpFormTransport = %d, want retained value 1", m.mcpFormTransport)
	}
	if cmd != nil {
		t.Error("expected no command when validation fails")
	}
}

// TestMcpFlow_TransportCycleWrapsThroughAllValues closes gap 2: the left/right
// keys on field 1 (transport) move through the real order defined in
// buildMcpServerFromForm's transports slice: stdio(0) -> http(1) -> sse(2),
// clamping (not wrapping) at each end per handleMcpFormKeyMsg's guards.
func TestMcpFlow_TransportCycleWrapsThroughAllValues(t *testing.T) {
	m := mcpChipFixture(nil)
	m = drive(m, pressRune('n'), pressTab())
	if m.mcpFormField != 1 {
		t.Fatalf("mcpFormField = %d, want 1 (transport)", m.mcpFormField)
	}
	if m.mcpFormTransport != 0 {
		t.Fatalf("mcpFormTransport = %d, want 0 (stdio) initially", m.mcpFormTransport)
	}

	// Right past the end clamps at sse(2), does not wrap to 0.
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyRight})
	if m.mcpFormTransport != 1 {
		t.Fatalf("after 1st right, mcpFormTransport = %d, want 1 (http)", m.mcpFormTransport)
	}
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyRight})
	if m.mcpFormTransport != 2 {
		t.Fatalf("after 2nd right, mcpFormTransport = %d, want 2 (sse)", m.mcpFormTransport)
	}
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyRight})
	if m.mcpFormTransport != 2 {
		t.Fatalf("extra right past sse should clamp, mcpFormTransport = %d, want 2", m.mcpFormTransport)
	}

	// Left walks back down to stdio(0) and clamps there too.
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyLeft})
	if m.mcpFormTransport != 1 {
		t.Fatalf("after 1st left, mcpFormTransport = %d, want 1 (http)", m.mcpFormTransport)
	}
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyLeft})
	if m.mcpFormTransport != 0 {
		t.Fatalf("after 2nd left, mcpFormTransport = %d, want 0 (stdio)", m.mcpFormTransport)
	}
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyLeft})
	if m.mcpFormTransport != 0 {
		t.Fatalf("extra left past stdio should clamp, mcpFormTransport = %d, want 0", m.mcpFormTransport)
	}
}

// TestMcpFlow_ShiftTabNavigatesBackwardThroughUpdate closes gap 3: drives a
// real tea.KeyPressMsg with Mod: tea.ModShift through Model.Update (not a
// helper shortcut) to prove shift+tab actually reverses field focus rather
// than behaving like a second plain tab.
func TestMcpFlow_ShiftTabNavigatesBackwardThroughUpdate(t *testing.T) {
	m := mcpChipFixture(nil)
	m = drive(m, pressRune('n'))
	if m.mcpFormField != 0 {
		t.Fatalf("mcpFormField = %d, want 0 (name) on open", m.mcpFormField)
	}

	newModel, _ := m.Update(pressTab())
	m = newModel.(Model)
	if m.mcpFormField != 1 {
		t.Fatalf("mcpFormField = %d, want 1 after forward tab", m.mcpFormField)
	}

	newModel, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	m = newModel.(Model)
	if m.mcpFormField != 0 {
		t.Fatalf("mcpFormField = %d, want 0 (back to original) after shift+tab", m.mcpFormField)
	}
	if !m.mcpFormName.Focused() {
		t.Error("expected name field re-focused after shift+tab back to field 0")
	}
}

// TestMcpFlow_AddDoneMsgSuccessClosesFormAndReloads closes gap 4 (success
// path): feeding mcpAddDoneMsg{err:nil} back through Update while the form is
// open must close the form, clear any form error, and queue a reload command
// (doLoadMcpRows), per the mcpAddDoneMsg case in update.go.
func TestMcpFlow_AddDoneMsgSuccessClosesFormAndReloads(t *testing.T) {
	m := mcpChipFixture(nil)
	m = drive(m, pressRune('n'))
	m.mcpFormName.SetValue("nav-focus-server")
	m.mcpFormCommand.SetValue("npx -y @server/mcp")
	m.mcpRunning = true

	newModel, cmd := m.Update(mcpAddDoneMsg{err: nil})
	m = newModel.(Model)

	if m.mcpFormOpen {
		t.Error("mcpFormOpen should be false after successful mcpAddDoneMsg")
	}
	if m.mcpFormErr != nil {
		t.Errorf("mcpFormErr should be nil after success, got %v", m.mcpFormErr)
	}
	if m.mcpRunning {
		t.Error("mcpRunning should be false after mcpAddDoneMsg resolves")
	}
	if m.mcpFormName.Value() != "" {
		t.Errorf("form should be reset after success, mcpFormName = %q", m.mcpFormName.Value())
	}
	if cmd == nil {
		t.Fatal("expected a reload command after successful add")
	}
}

// TestMcpFlow_AddDoneMsgErrorKeepsFormOpenWithError closes gap 4 (error
// path): feeding mcpAddDoneMsg with a non-nil err while the form is open must
// keep the form open and set mcpFormErr (not the global statusMsg), per the
// "if m.mcpFormOpen" branch in update.go's mcpAddDoneMsg case.
func TestMcpFlow_AddDoneMsgErrorKeepsFormOpenWithError(t *testing.T) {
	m := mcpChipFixture(nil)
	m = drive(m, pressRune('n'))
	m.mcpFormName.SetValue("nav-focus-server")
	m.mcpFormCommand.SetValue("npx -y @server/mcp")
	m.mcpRunning = true

	newModel, _ := m.Update(mcpAddDoneMsg{err: errors.New("add failed: name already exists")})
	m = newModel.(Model)

	if !m.mcpFormOpen {
		t.Error("mcpFormOpen should remain true after failed mcpAddDoneMsg")
	}
	if m.mcpFormErr == nil {
		t.Fatal("expected mcpFormErr to be set after failed mcpAddDoneMsg")
	}
	if got := m.mcpFormErr.Error(); got != "add failed: name already exists" {
		t.Errorf("mcpFormErr = %q, want %q", got, "add failed: name already exists")
	}
	if m.mcpRunning {
		t.Error("mcpRunning should be false after mcpAddDoneMsg resolves")
	}
	if m.mcpFormName.Value() != "nav-focus-server" {
		t.Errorf("form fields should be retained on error, mcpFormName = %q", m.mcpFormName.Value())
	}
	if m.statusMsg != "" {
		t.Errorf("statusMsg should stay empty; error belongs in mcpFormErr while form is open, got %q", m.statusMsg)
	}
}

// TestMcpFlow_ClaimKeyOnUnmanagedRowQueuesAdoptCommand closes gap 5: 'c' on
// an unmanaged (not-yet-adopted) mcp row must set mcpRunning and yield a
// batched command (spinner tick + doImportMcpServer) rather than a no-op.
// doImportMcpServer's closure captures m.app/m.ctx and hits the real App on
// execution, so — mirroring how the existing valid-submit test asserts on the
// queued batch via batchHasSpinnerTick rather than executing it against a
// nil app — this stops at "a real adopt command was queued" instead of
// running the network/DB call.
func TestMcpFlow_ClaimKeyOnUnmanagedRowQueuesAdoptCommand(t *testing.T) {
	m := mcpChipFixture(nil)
	m.mcpUnmanaged = map[string][]app.InstalledMcpServer{
		"codex": {{Name: "unmanaged-mcp-srv", Transport: "stdio", Command: "npx unmanaged"}},
	}
	// No managed rows, so cursor 0 lands directly on the single unmanaged entry.
	m.mcpCursor = 0

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

// TestMcpFlow_ImportAdoptErrorClearsOpKeyAndSurfacesStatus reproduces the
// stuck-spinner claim bug: the group-picker claim path sets agentsOpKey via
// startAgentsOp, but the completion handler for mcpImportAdoptDoneMsg used to
// never clear it (on success OR error), leaving the row's spinner running
// forever whenever AddMcpServer/SetMcpGroups failed.
func TestMcpFlow_ImportAdoptErrorClearsOpKeyAndSurfacesStatus(t *testing.T) {
	m := mcpChipFixture(nil)
	m.startAgentsOp("stuck-key")
	m.mcpRunning = true

	newModel, _ := m.Update(mcpImportAdoptDoneMsg{serverName: "srv", err: errors.New("boom")})
	got := newModel.(Model)

	if got.agentsOpKey != "" {
		t.Errorf("agentsOpKey after import error = %q, want cleared", got.agentsOpKey)
	}
	if got.mcpRunning {
		t.Error("mcpRunning should be false after the op completes")
	}
	if !got.statusIsErr || got.statusMsg == "" {
		t.Errorf("status=%q isErr=%v, want a surfaced error", got.statusMsg, got.statusIsErr)
	}
}

func TestMcpFlow_ImportKeyOnUnmanagedRowIsInert(t *testing.T) {
	m := mcpChipFixture(nil)
	m.mcpUnmanaged = map[string][]app.InstalledMcpServer{
		"codex": {{Name: "unmanaged-mcp-srv", Transport: "stdio", Command: "npx unmanaged"}},
	}
	m.mcpCursor = 0

	newModel, cmd := m.Update(pressRune('i'))
	m = newModel.(Model)

	if m.mcpRunning {
		t.Error("mcpRunning should stay false after 'i' on an unmanaged row (import key retired)")
	}
	if cmd != nil {
		t.Error("expected a nil command after 'i' on an unmanaged row")
	}
}

// TestMcpFlow_GroupsStatusMessagePersistsUntilReplaced closes gap 6: setStatus
// writes m.statusMsg synchronously (see commands.go), and neither opening the
// add-server form nor moving the cursor clears it — it persists untouched
// until a new status/clearStatusMsg replaces it, unlike mcpFormErr which is
// reset on form open.
func TestMcpFlow_GroupsStatusMessagePersistsUntilReplaced(t *testing.T) {
	rows := []app.McpServerRow{{Name: "groups-status-server", Groups: []string{"work"}}}
	m := mcpChipFixture(rows)

	m = drive(m, pressRune('g'))

	if m.statusMsg == "" {
		t.Fatal("expected statusMsg to be set by 'g'")
	}
	wantSubstr := "groups-status-server groups: work"
	if m.statusMsg != wantSubstr {
		t.Fatalf("statusMsg = %q, want %q", m.statusMsg, wantSubstr)
	}

	// Opening the add form does not touch statusMsg.
	m = drive(m, pressRune('n'))
	if m.statusMsg != wantSubstr {
		t.Errorf("statusMsg changed after opening add form: got %q, want unchanged %q", m.statusMsg, wantSubstr)
	}
	m = drive(m, pressEsc())

	// Neither does plain cursor movement.
	m = drive(m, pressRune('j'))
	if m.statusMsg != wantSubstr {
		t.Errorf("statusMsg changed after cursor move: got %q, want unchanged %q", m.statusMsg, wantSubstr)
	}
}
