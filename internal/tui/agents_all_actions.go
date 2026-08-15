package tui

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

func (m *Model) openAgentsDriftPrompt(drift []string) {
	if len(drift) == 0 {
		return
	}
	m.agentsDriftPromptOpen = true
	m.agentsDriftPromptLines = drift
	clearStatus(m)
}

func (m *Model) closeAgentsDriftPrompt() {
	m.agentsDriftPromptOpen = false
	m.agentsDriftPromptLines = nil
}

func (m *Model) handleAgentsDriftPromptKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	if msg.IsRepeat {
		return nil
	}
	if m.agentsBulkResolveConfirm {
		return m.handleAgentsBulkResolveConfirmKeyMsg(msg)
	}
	useManaged := key.Matches(msg, m.keys.AgentsUseManagedAll)
	if !useManaged && !key.Matches(msg, m.keys.AgentsUseLocalAll) {
		m.dismissAgentsDriftPromptOn(msg)
		return nil
	}
	m.agentsBulkResolveConfirm = true
	m.agentsBulkResolveUseManaged = useManaged
	return []tea.Cmd{m.armConfirmationTimeout()}
}

func (m *Model) handleAgentsBulkResolveConfirmKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	armed := m.keys.AgentsUseLocalAll
	if m.agentsBulkResolveUseManaged {
		armed = m.keys.AgentsUseManagedAll
	}
	useManaged := m.agentsBulkResolveUseManaged
	m.cancelConfirmationTimeout()
	m.agentsBulkResolveConfirm = false
	m.agentsBulkResolveUseManaged = false
	if !key.Matches(msg, armed) {
		m.dismissAgentsDriftPromptOn(msg)
		return nil
	}
	m.closeAgentsDriftPrompt()
	if m.agentsOpInFlight() {
		return []tea.Cmd{setStatus(m, agentsBusyStatus, true)}
	}
	m.skillsRunning, m.mcpRunning, m.pluginRunning = true, true, true
	return []tea.Cmd{m.spinner.Tick, m.doAgentsBulkResolve(useManaged)}
}

func (m *Model) dismissAgentsDriftPromptOn(msg tea.KeyPressMsg) {
	if key.Matches(msg, m.keys.Back) || msg.String() == "q" {
		m.closeAgentsDriftPrompt()
	}
}
