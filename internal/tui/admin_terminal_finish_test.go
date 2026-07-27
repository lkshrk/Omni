package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
)

func adminTerminalRunning(returnMode viewMode) *adminTerminalState {
	return &adminTerminalState{
		id:         1,
		name:       "vim",
		command:    "brew",
		display:    "brew install vim",
		running:    true,
		returnMode: returnMode,
		output:     "installing...\ndone\n",
	}
}

func TestHandleAdminTerminalDoneMsg_NonNilTerminal_ErrorKeepsViewOpen(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewAdminTerminal
	m.adminTerminal = adminTerminalRunning(viewList)
	m.loading = true
	wantErr := errors.New("brew failed")

	m = drive(m, adminTerminalDoneMsg{id: 1, state: m.adminTerminal.completionState(), err: wantErr})

	if m.mode != viewAdminTerminal {
		t.Fatalf("mode = %v, want viewAdminTerminal", m.mode)
	}
	if m.adminTerminal == nil {
		t.Fatal("adminTerminal should remain non-nil after done msg")
	}
	if !m.adminTerminal.finished {
		t.Fatal("finished should be true")
	}
	if m.adminTerminal.finishErr != wantErr {
		t.Fatalf("finishErr = %v, want %v", m.adminTerminal.finishErr, wantErr)
	}
	if m.loading {
		t.Fatal("loading should be cleared")
	}
}

func TestHandleAdminTerminalDoneMsg_NonNilTerminal_SuccessKeepsViewOpen(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewAdminTerminal
	m.adminTerminal = adminTerminalRunning(viewList)
	m.loading = true

	m = drive(m, adminTerminalDoneMsg{id: 1, state: m.adminTerminal.completionState(), err: nil})

	if m.mode != viewAdminTerminal {
		t.Fatalf("mode = %v, want viewAdminTerminal", m.mode)
	}
	if m.adminTerminal == nil {
		t.Fatal("adminTerminal should remain non-nil")
	}
	if !m.adminTerminal.finished {
		t.Fatal("finished should be true")
	}
	if m.adminTerminal.finishErr != nil {
		t.Fatalf("finishErr = %v, want nil", m.adminTerminal.finishErr)
	}
	if m.loading {
		t.Fatal("loading should be cleared")
	}
}

func TestHandleAdminTerminalKeyMsg_FinishedState_DismissesOnAnyKey(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewAdminTerminal
	m.adminTerminal = adminTerminalRunning(viewList)
	m.adminTerminal.running = false
	m.adminTerminal.finished = true
	m.adminTerminal.finishErr = errors.New("exit status 1")

	m = drive(m, tea.KeyPressMsg{Code: tea.KeyRight})

	if m.adminTerminal != nil {
		t.Fatal("adminTerminal should be nil after dismiss keypress")
	}
	if m.mode == viewAdminTerminal {
		t.Fatal("mode should no longer be viewAdminTerminal after dismiss")
	}
}

func TestHandleAdminTerminalKeyMsg_FailedBulkActionContinuesQueue(t *testing.T) {
	t.Parallel()
	currentKey := toolKey("bat", "apt")
	nextKey := toolKey("vim", "apt")
	m := baseModel(nil)
	m.mode = viewAdminTerminal
	m.adminTerminal = adminTerminalRunning(viewList)
	m.adminTerminal.action = app.PrivilegeActionUpgrade
	m.adminTerminal.name = "bat"
	m.adminTerminal.providerName = "apt"
	m.adminTerminal.rowKey = currentKey
	m.adminTerminal.preserveOtherRowErrors = true
	m.adminTerminal.running = false
	m.adminTerminal.finished = true
	m.adminTerminal.finishErr = errors.New("exit status 1")
	m.adminTerminalQueue = []adminTerminalState{{
		action:                 app.PrivilegeActionUpgrade,
		name:                   "vim",
		providerName:           "apt",
		command:                "sudo",
		args:                   []string{"apt-get", "install", "--only-upgrade", "-y", "vim"},
		display:                "sudo apt-get install --only-upgrade -y vim",
		returnMode:             viewList,
		rowKey:                 nextKey,
		preserveOtherRowErrors: true,
	}}
	m.rowErrors = map[string]string{
		currentKey: "admin approval required to upgrade",
		nextKey:    "admin approval required to upgrade",
	}

	m = drive(m, pressEnter())

	if m.mode != viewAdminTerminal || m.adminTerminal == nil {
		t.Fatalf("mode=%v adminTerminal=%v, want next queued admin terminal after first failure", m.mode, m.adminTerminal != nil)
	}
	if m.adminTerminal.name != "vim" {
		t.Fatalf("next admin terminal target = %q, want vim", m.adminTerminal.name)
	}
	if got := m.rowErrors[currentKey]; !strings.Contains(got, "admin terminal: exit status 1") {
		t.Fatalf("current row error = %q, want failed sudo action retained", got)
	}
	if got := m.rowErrors[nextKey]; got != "admin approval required to upgrade" {
		t.Fatalf("queued row error = %q, want untouched approval state", got)
	}
	if m.loading {
		t.Fatal("failed item should release loading before the next approval prompt")
	}
}

func TestRenderAdminTerminalFinishedPopup_SuccessShowsDone(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.width = 90
	m.height = 24
	m.adminTerminal = &adminTerminalState{
		name:     "vim",
		command:  "brew",
		display:  "brew install vim",
		finished: true,
		output:   "installing vim...\n",
	}

	out := renderAdminTerminalPopup(m)

	if !strings.Contains(out, "done") {
		t.Fatalf("finished success popup missing 'done':\n%s", out)
	}
	if !strings.Contains(out, "press any key to close") {
		t.Fatalf("finished popup missing 'press any key to close':\n%s", out)
	}
	if strings.Contains(out, "failed") {
		t.Fatalf("finished success popup should not contain 'failed':\n%s", out)
	}
}

func TestRenderAdminTerminalFinishedPopup_ErrorShowsFailed(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.width = 90
	m.height = 24
	m.adminTerminal = &adminTerminalState{
		name:      "vim",
		command:   "brew",
		display:   "brew install vim",
		finished:  true,
		finishErr: errors.New("exit status 1"),
		output:    "Error: brew failed\n",
	}

	out := renderAdminTerminalPopup(m)

	if !strings.Contains(out, "failed:") {
		t.Fatalf("finished error popup missing 'failed:':\n%s", out)
	}
	if !strings.Contains(out, "exit status 1") {
		t.Fatalf("finished error popup missing error message:\n%s", out)
	}
	if !strings.Contains(out, "press any key to close") {
		t.Fatalf("finished popup missing 'press any key to close':\n%s", out)
	}
}

func TestHandleAdminTerminalDoneMsg_StaleID_IsIgnored(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewAdminTerminal
	m.adminTerminal = adminTerminalRunning(viewList)

	m = drive(m, adminTerminalDoneMsg{id: 99, state: adminTerminalState{}, err: nil})

	if m.mode != viewAdminTerminal {
		t.Fatalf("mode = %v, want viewAdminTerminal (stale msg should be ignored)", m.mode)
	}
	if m.adminTerminal == nil || m.adminTerminal.finished {
		t.Fatal("stale done msg should not modify adminTerminal state")
	}
}
