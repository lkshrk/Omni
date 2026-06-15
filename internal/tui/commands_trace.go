package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"
)

func (m *Model) doLoadTraces() tea.Cmd {
	a := m.app
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	gen := m.traceLogGen
	return func() tea.Msg {
		traces, err := a.CommandTraces(ctx, 100)
		return traceLogLoadedMsg{gen: gen, traces: traces, err: err}
	}
}
