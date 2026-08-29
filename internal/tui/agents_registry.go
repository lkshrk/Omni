package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
)

type agentsRegistryMsg struct {
	gen     int
	entries []app.AgentsRegistryEntry
	notices []string
	err     error
}

func (m *Model) doLoadAgentsRegistry() tea.Cmd {
	if m.app == nil {
		return nil
	}
	m.agentsRegistryGen++
	gen, a := m.agentsRegistryGen, m.app
	return func() tea.Msg {
		entries, notices, err := a.AgentsRegistry()
		return agentsRegistryMsg{gen: gen, entries: entries, notices: notices, err: err}
	}
}

// The registry list is cursor-selected while the query keeps focus, so the cursor must be visible from the start.
func (m *Model) openAgentsRegistry() []tea.Cmd {
	m.agentsRegistryMode = true
	m.agentsSearchActive = true
	m.agentsConfirmIdx = -1
	m.filter.SetValue("")
	m.filter.Focus()
	m.agentsCursor = 0
	m.cursorHidden = false
	return []tea.Cmd{m.doLoadAgentsRegistry()}
}

func (m *Model) closeAgentsRegistry() {
	m.agentsRegistryMode = false
	m.agentsRegistry = nil
	m.agentsConfirmIdx = -1
	m.closeAgentsFilter()
}

func (m Model) agentsVisibleRegistry() []app.AgentsRegistryEntry {
	query := m.agentsFilterText()
	if query == "" {
		return m.agentsRegistry
	}
	out := make([]app.AgentsRegistryEntry, 0, len(m.agentsRegistry))
	for _, entry := range m.agentsRegistry {
		if agentsRowMatches(query, entry.Name+" "+entry.Marketplace, entry.Description) {
			out = append(out, entry)
		}
	}
	return out
}

func (m *Model) doAgentsRegistryInstall(entry app.AgentsRegistryEntry) []tea.Cmd {
	cmds := m.runAPMRowOp("apm install -g "+entry.Spec(), entry.Spec(), "install", "-g", entry.Spec())
	m.agentsRemovalHint = app.AgentsTemplateHintLines(entry.Spec(), false)
	return cmds
}

func agentsRegistryHintItems(m Model) []hintItem {
	entries := m.agentsVisibleRegistry()
	if m.agentsCursor >= len(entries) || entries[m.agentsCursor].Installed {
		return nil
	}
	return []hintItem{hintFromBindingDesc(m.keys.Confirm, "install"), hintFromBindingDesc(m.keys.Back, "back")}
}

func (m *Model) handleAgentsRegistryEnter() []tea.Cmd {
	entries := m.agentsVisibleRegistry()
	if m.agentsCursor >= len(entries) {
		return nil
	}
	entry := entries[m.agentsCursor]
	if entry.Installed {
		return []tea.Cmd{setStatus(m, "⚠ "+entry.Name+" is already installed", false)}
	}
	if m.apmRunning {
		return []tea.Cmd{setStatus(m, agentsBusyStatus, true)}
	}
	if m.agentsConfirmIdx == m.agentsCursor {
		m.agentsConfirmIdx = -1
		return m.doAgentsRegistryInstall(entry)
	}
	m.agentsConfirmIdx = m.agentsCursor
	return []tea.Cmd{
		setStatus(m, "press enter again to install "+entry.Spec(), false),
		m.armConfirmationTimeout(),
	}
}
