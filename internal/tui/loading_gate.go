package tui

import tea "charm.land/bubbletea/v2"

// Record gate ownership because shared clearers cannot infer it from progress streams, which omit some refresh legs and gate-raising operations.
type loadingOwner uint8

const (
	// Unowned gates remain clearable by anyone so unconverted callers cannot wedge the TUI.
	loadingUnowned loadingOwner = iota
	loadingOwnerToolRefresh
	loadingOwnerProgressOp
	loadingOwnerLocalOp
)

func (m *Model) beginLoading(owner loadingOwner) {
	m.loading = true
	m.loadingOwner = owner
	m.loadingGen++
}

func (m *Model) releaseLoading() {
	m.loading = false
	m.loadingOwner = loadingUnowned
}

// Gate generations keep stale completions from clearing newer same-owner work; zero remains permissive for unstamped producers.
func (m Model) opCompleteOwnsGate(msgGen int) bool {
	return msgGen == 0 || msgGen == m.loadingGen
}

// stampGate records dispatch generation so a completion outliving cancellation cannot lower the next operation's gate.
func (m *Model) stampGate(cmd tea.Cmd) tea.Cmd {
	gen := m.loadingGen
	return func() tea.Msg {
		msg := cmd()
		if done, ok := msg.(opCompleteMsg); ok {
			done.loadingGen = gen
			return done
		}
		return msg
	}
}

func (m Model) loadingHeldByOther(owner loadingOwner) bool {
	return m.loading && m.loadingOwner != loadingUnowned && m.loadingOwner != owner
}

func (m *Model) endLoading(owner loadingOwner) {
	if m.loadingHeldByOther(owner) {
		return
	}
	m.releaseLoading()
}

// Ownership only means anything while the gate is up, and many operations lower m.loading directly on
// their own result message; without this the next operation to raise it would inherit a stale owner.
func (m *Model) scrubLoadingOwner() {
	if !m.loading {
		m.loadingOwner = loadingUnowned
	}
}
