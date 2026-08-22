package tui

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
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

func TestCtrlCIgnoresLockModifiers(t *testing.T) {
	for _, mod := range []tea.KeyMod{
		tea.ModCtrl | tea.ModCapsLock,
		tea.ModCtrl | tea.ModNumLock,
		tea.ModCtrl | tea.ModScrollLock,
		tea.ModCtrl | lockMods,
	} {
		if !isCtrlC(tea.KeyPressMsg{Code: 'c', Mod: mod}) {
			t.Errorf("ctrl+c with lock modifiers %v was not recognized", mod)
		}
	}
}

func TestCtrlCConfirmationIsDisarmedByInterveningPickerInput(t *testing.T) {
	m := baseModel(nil)
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.openFilePicker("Dots repo path", t.TempDir(), false)

	armed := drive(m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	typed := drive(armed, tea.KeyPressMsg{Code: 'x', Text: "x"})
	if typed.ctrlCConfirm || typed.dotsFilePicker.input.Value() != armed.dotsFilePicker.input.Value()+"x" {
		t.Fatalf("intervening picker input did not disarm and route normally: confirm=%v value=%q", typed.ctrlCConfirm, typed.dotsFilePicker.input.Value())
	}

	fresh := drive(typed, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !fresh.ctrlCConfirm || fresh.ctx.Err() != nil {
		t.Fatalf("next ctrl+c was not a fresh first press: confirm=%v canceled=%v", fresh.ctrlCConfirm, fresh.ctx.Err() != nil)
	}
}

func TestCtrlCConfirmationDoesNotDisturbDashboardConfirmation(t *testing.T) {
	m := baseModel([]*app.ToolView{
		{Name: "git", Provider: "brew", Installed: true, Outdated: true, Tracked: true},
		{Name: "fd", Provider: "brew", Installed: false, Tracked: true},
	})
	m.mode = viewStatus
	m.dotsGitStatus = " M dotfiles/zsh/.zshrc"
	m.openDashboardReconcilePlan()
	if items := dashboardReconcilePlanItems(m); len(items) < 2 {
		t.Fatalf("test needs at least two dashboard plan items, got %d", len(items))
	}
	m.confirmGen = 17

	armed := drive(m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !armed.dashboardReconcilePlanOpen || armed.confirmGen != 17 {
		t.Fatalf("ctrl+c disturbed dashboard confirmation: open=%v gen=%d", armed.dashboardReconcilePlanOpen, armed.confirmGen)
	}
	routed := drive(armed, tea.KeyPressMsg{Code: tea.KeyDown})
	if routed.ctrlCConfirm || !routed.dashboardReconcilePlanOpen || routed.dashboardReconcilePlanCursor != 1 || routed.confirmGen != 17 {
		t.Fatalf("intervening key did not preserve and route to dashboard modal: ctrl=%v open=%v cursor=%d gen=%d", routed.ctrlCConfirm, routed.dashboardReconcilePlanOpen, routed.dashboardReconcilePlanCursor, routed.confirmGen)
	}
}

func TestCtrlCConfirmationExpiryPreservesUnderlyingConfirmation(t *testing.T) {
	m := baseModel(nil)
	m.confirmQuit = true
	m.quitConfirmKey = "q"
	m.confirmGen = 23
	armed := drive(m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})

	got := drive(armed, ctrlCConfirmTimeoutMsg{gen: armed.ctrlCConfirmGen})
	if got.ctrlCConfirm || !got.confirmQuit || got.quitConfirmKey != "q" || got.confirmGen != 23 {
		t.Fatalf("ctrl+c expiry disturbed q confirmation: ctrl=%v quit=%v key=%q gen=%d", got.ctrlCConfirm, got.confirmQuit, got.quitConfirmKey, got.confirmGen)
	}
	if got.statusMsg != "quit confirmation expired — press ctrl+c again" {
		t.Fatalf("expiry status = %q", got.statusMsg)
	}
}

func TestFatalErrorShowsQuitExpiryAndCtrlCRearms(t *testing.T) {
	m := baseModel(nil)
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.err = errors.New("startup failed")

	armed := drive(m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	expired := drive(armed, ctrlCConfirmTimeoutMsg{gen: armed.ctrlCConfirmGen})
	if out := stripANSIEscapeSequences(expired.viewString()); !strings.Contains(out, "quit confirmation expired — press ctrl+c again") {
		t.Fatalf("fatal error hid ctrl+c expiry guidance:\n%s", out)
	}

	model, cmd := expired.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	rearmed := model.(Model)
	if !rearmed.ctrlCConfirm || rearmed.ctx.Err() != nil || cmd == nil || reflect.ValueOf(cmd).Pointer() == reflect.ValueOf(tea.Quit).Pointer() {
		t.Fatalf("next ctrl+c did not re-arm: confirm=%v canceled=%v", rearmed.ctrlCConfirm, rearmed.ctx.Err() != nil)
	}

	qArmed := drive(m, pressRune('q'))
	qExpired := drive(qArmed, confirmTimeoutMsg{gen: qArmed.confirmGen})
	if out := stripANSIEscapeSequences(qExpired.viewString()); !strings.Contains(out, "quit confirmation expired — press q again") {
		t.Fatalf("fatal error hid q expiry guidance:\n%s", out)
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
