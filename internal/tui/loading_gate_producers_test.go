package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
)

// opCompleteOwnsGate deliberately fails open on generation zero so an unconverted producer cannot
// wedge the TUI behind a gate nobody may lower. That escape hatch is also what let a cancelled
// operation release the next operation's gate, so the only thing keeping it unreachable is that every
// producer stamps. Asserting the stamp on the constructed message would not notice a producer losing
// its wrapper, so this drives the real commands.
func TestEveryOpCompleteProducerStampsItsGate(t *testing.T) {
	producers := []struct {
		name string
		cmd  func(m *Model) tea.Cmd
	}{
		{"doInstall", func(m *Model) tea.Cmd { return m.doInstall("nope", "brew", nil, 0) }},
		{"doDelete", func(m *Model) tea.Cmd { return m.doDelete("nope", "brew") }},
		{"doDeleteFromConfig", func(m *Model) tea.Cmd { return m.doDeleteFromConfig("nope", "brew") }},
		{"doUpgrade", func(m *Model) tea.Cmd { return m.doUpgrade("nope", "brew", nil, 0) }},
		{"doConsolidate", func(m *Model) tea.Cmd { return m.doConsolidate("node", "npm") }},
		{"doInstallAndAddTool", func(m *Model) tea.Cmd { return m.doInstallAndAddTool(nil) }},
		{"doSetProviderScope", func(m *Model) tea.Cmd { return m.doSetProviderScope("nope", scopeOption{}, nil) }},
		{"doApplyProviderSolution", func(m *Model) tea.Cmd {
			return m.doApplyProviderSolution("nope", "brew", app.ErrorSolution{})
		}},
		{"doCompleteAdminTerminalAction", func(m *Model) tea.Cmd {
			return m.doCompleteAdminTerminalAction(adminTerminalState{name: "nope", providerName: "brew"})
		}},
	}

	for _, p := range producers {
		t.Run(p.name, func(t *testing.T) {
			m, _ := newDotsModelForCmds(t)
			m.beginLoading(loadingOwnerLocalOp)
			gen := m.loadingGen
			if gen == 0 {
				t.Fatal("a raised gate must have a non-zero generation")
			}

			msg, ok := p.cmd(&m)().(opCompleteMsg)
			if !ok {
				t.Skipf("%s did not produce an opCompleteMsg for this input", p.name)
			}
			if msg.loadingGen != gen {
				t.Fatalf("%s emitted loadingGen %d, want %d: an unstamped completion falls through opCompleteOwnsGate's fail-open and can release a later operation's gate",
					p.name, msg.loadingGen, gen)
			}

			m.beginLoading(loadingOwnerLocalOp)
			if m.opCompleteOwnsGate(msg.loadingGen) {
				t.Fatalf("%s's completion still owns the gate after the user cancelled and started something else", p.name)
			}
		})
	}
}
