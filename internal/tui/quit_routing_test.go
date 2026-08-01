package tui

import (
	"context"
	"errors"
	"io"
	"os"
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

func TestCtrlCAlwaysConfirmsThenShutsDown(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*testing.T, *Model) func(*testing.T, Model, bool)
	}{
		{"default", func(*testing.T, *Model) func(*testing.T, Model, bool) { return nil }},
		{"file picker", func(t *testing.T, m *Model) func(*testing.T, Model, bool) {
			m.openFilePicker("Dots repo path", t.TempDir(), false)
			return nil
		}},
		{"focused input", func(_ *testing.T, m *Model) func(*testing.T, Model, bool) {
			m.mode = viewSearch
			m.filter.Focus()
			return nil
		}},
		{"help", func(_ *testing.T, m *Model) func(*testing.T, Model, bool) {
			m.help.ShowAll = true
			return nil
		}},
		{"drift modal", func(_ *testing.T, m *Model) func(*testing.T, Model, bool) {
			m.mode = viewSkills
			m.agentsDriftPromptOpen = true
			return nil
		}},
		{"setup overlay", func(_ *testing.T, m *Model) func(*testing.T, Model, bool) {
			m.setupReloading = true
			return nil
		}},
		{"fatal error", func(_ *testing.T, m *Model) func(*testing.T, Model, bool) {
			m.err = errors.New("startup failed")
			return nil
		}},
		{"active operation", func(_ *testing.T, m *Model) func(*testing.T, Model, bool) {
			cancelled := false
			m.loading = true
			m.activeActionCancel = func() { cancelled = true }
			return func(t *testing.T, got Model, first bool) {
				if first && (cancelled || got.activeActionCancel == nil || !got.loading) {
					t.Fatalf("first ctrl+c disturbed active work: cancelled=%v cancel=%v loading=%v", cancelled, got.activeActionCancel != nil, got.loading)
				}
				if !first && (!cancelled || got.activeActionCancel != nil || got.loading) {
					t.Fatalf("second ctrl+c did not shut down active work: cancelled=%v cancel=%v loading=%v", cancelled, got.activeActionCancel != nil, got.loading)
				}
			}
		}},
		{"running admin terminal", func(t *testing.T, m *Model) func(*testing.T, Model, bool) {
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reader.Close() })
			m.mode = viewAdminTerminal
			m.adminTerminal = &adminTerminalState{running: true, session: &adminTerminalSession{ptmx: writer}}
			return func(t *testing.T, got Model, first bool) {
				if first {
					if _, err := writer.Stat(); err != nil {
						t.Fatalf("first ctrl+c closed the admin PTY: %v", err)
					}
					return
				}
				if _, err := writer.Write([]byte("still open")); err == nil {
					t.Fatal("admin PTY remained open after second ctrl+c")
				}
				if forwarded, err := io.ReadAll(reader); err != nil || len(forwarded) != 0 {
					t.Fatalf("ctrl+c was forwarded to the admin PTY: bytes=%v err=%v", forwarded, err)
				}
				if got.adminTerminal.session != nil {
					t.Fatal("admin terminal retained its closed session")
				}
			}
		}},
		{"finished admin terminal", func(_ *testing.T, m *Model) func(*testing.T, Model, bool) {
			m.mode = viewAdminTerminal
			m.adminTerminal = &adminTerminalState{finished: true}
			return nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := baseModel(nil)
			m.ctx, m.cancel = context.WithCancel(context.Background())
			check := tc.setup(t, &m)
			model, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
			armed := model.(Model)
			if cmd == nil || reflect.ValueOf(cmd).Pointer() == reflect.ValueOf(tea.Quit).Pointer() {
				t.Fatal("first ctrl+c quit instead of arming confirmation")
			}
			if !armed.ctrlCConfirm {
				t.Fatal("first ctrl+c did not arm its confirmation")
			}
			if armed.ctx.Err() != nil {
				t.Fatal("first ctrl+c canceled the model context")
			}
			if out := stripANSIEscapeSequences(armed.viewString()); !strings.Contains(out, "ctrl+c") || !strings.Contains(out, "again to quit") {
				t.Fatalf("first ctrl+c confirmation is not visible:\n%s", out)
			}
			if check != nil {
				check(t, armed, true)
			}

			model, cmd = armed.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
			quit := model.(Model)
			if !cmdQuits(cmd) {
				t.Fatal("second ctrl+c did not quit")
			}
			if quit.ctx.Err() == nil {
				t.Fatal("second ctrl+c did not cancel the model context")
			}
			if check != nil {
				check(t, quit, false)
			}
		})
	}
}

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
