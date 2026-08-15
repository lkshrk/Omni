package tui

import (
	"sort"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
)

type mcpRowsMsg struct {
	rows      []app.McpServerRow
	unmanaged map[string][]app.InstalledMcpServer
	err       error
}

type mcpRestoreDoneMsg struct{ err error }

type mcpRemoveDoneMsg struct {
	err error
}

type mcpImportAdoptDoneMsg struct {
	err error
}

type mcpAgentsSavedMsg struct{ err error }

type mcpAddDoneMsg struct{ err error }

func (m *Model) doLoadMcpRows() tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		rows, unmanaged, err := a.McpServerRows(ctx)
		return mcpRowsMsg{rows: rows, unmanaged: unmanaged, err: err}
	}
}

type mcpGroupsSavedMsg struct{ err error }

func (m *Model) doSetMcpGroupMemberships(name string, groups []string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		err := a.SetMcpGroups(ctx, name, groups)
		return mcpGroupsSavedMsg{err: err}
	}
}

// Ordered deterministically (by agent ID then server name) for cursor indexing.
type mcpUnmanagedEntry struct {
	agentID string
	srv     app.InstalledMcpServer
}

func mcpUnmanagedFlat(unmanaged map[string][]app.InstalledMcpServer) []mcpUnmanagedEntry {
	agentIDs := make([]string, 0, len(unmanaged))
	for id := range unmanaged {
		agentIDs = append(agentIDs, id)
	}
	sort.Strings(agentIDs)
	var out []mcpUnmanagedEntry
	for _, id := range agentIDs {
		srvs := append([]app.InstalledMcpServer(nil), unmanaged[id]...)
		sort.Slice(srvs, func(i, j int) bool { return srvs[i].Name < srvs[j].Name })
		for _, s := range srvs {
			out = append(out, mcpUnmanagedEntry{agentID: id, srv: s})
		}
	}
	return out
}

func mcpTotalRows(m Model) int {
	return len(m.mcpRows) + len(mcpUnmanagedFlat(m.mcpUnmanaged))
}

func clampMcpCursor(m *Model) {
	n := mcpTotalRows(*m)
	if n == 0 {
		m.mcpCursor = 0
		return
	}
	if m.mcpCursor >= n {
		m.mcpCursor = n - 1
	}
	if m.mcpCursor < 0 {
		m.mcpCursor = 0
	}
}

func (m *Model) resetMcpForm() {
	m.mcpFormField = 0
	m.mcpFormTransport = 0
	m.mcpFormName.SetValue("")
	m.mcpFormCommand.SetValue("")
	m.mcpFormURL.SetValue("")
	m.mcpFormEnv.SetValue("")
	m.mcpFormEnvLit.SetValue("")
	m.mcpFormName.Blur()
	m.mcpFormCommand.Blur()
	m.mcpFormURL.Blur()
	m.mcpFormEnv.Blur()
	m.mcpFormEnvLit.Blur()
}
