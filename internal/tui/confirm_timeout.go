package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// Has to outlast reading the hint that describes it and deciding: the previous 5s expired while users were still reading, and a silent re-arm on the second press reads as a frozen app.
const confirmTimeout = 8 * time.Second

type confirmTimeoutMsg struct {
	gen int
}

type ctrlCConfirmTimeoutMsg struct {
	gen int
}

func (m *Model) armConfirmationTimeout() tea.Cmd {
	m.confirmGen++
	gen := m.confirmGen
	return func() tea.Msg {
		time.Sleep(confirmTimeout)
		return confirmTimeoutMsg{gen: gen}
	}
}

func (m *Model) cancelConfirmationTimeout() {
	m.confirmGen++
}

func (m *Model) armCtrlCConfirmationTimeout() tea.Cmd {
	m.ctrlCConfirmGen++
	gen := m.ctrlCConfirmGen
	return func() tea.Msg {
		time.Sleep(confirmTimeout)
		return ctrlCConfirmTimeoutMsg{gen: gen}
	}
}

func (m *Model) clearCtrlCConfirmation() {
	m.ctrlCConfirm = false
	m.ctrlCConfirmGen++
}

func (m *Model) handleCtrlCConfirmTimeoutMsg(msg ctrlCConfirmTimeoutMsg) []tea.Cmd {
	if msg.gen != m.ctrlCConfirmGen || !m.ctrlCConfirm {
		return nil
	}
	m.clearCtrlCConfirmation()
	return []tea.Cmd{setStatus(m, "quit confirmation expired — press ctrl+c again", false)}
}

func (m *Model) hasActiveConfirmation() bool {
	return m.confirmQuit ||
		m.listConfirm.action != "" ||
		m.dashboardReconcilePlanOpen ||
		m.hostCopyConfirm ||
		m.hostDeleteConfirm ||
		m.groupDeleteConfirm ||
		m.stowInstallPrompt ||
		m.dangerConfirmRow >= 0 ||
		m.dotsConfirmIdx >= 0 ||
		m.agentsConfirmIdx >= 0 ||
		m.dotsOverwriteIdx >= 0 ||
		m.dotsLocalIdx >= 0 ||
		m.dotsIgnoreIdx >= 0 ||
		m.dotsVariantIdx >= 0 ||
		m.dotsForceResolve != ""
}

func (m *Model) clearActiveConfirmation() {
	wipeStatus := m.confirmQuit || m.listConfirm.action != "" || m.dotsOverwriteIdx >= 0 || m.dotsLocalIdx >= 0
	m.confirmQuit = false
	m.quitConfirmKey = ""
	m.listConfirm = listConfirmation{}
	m.clearDashboardReconcilePlan()
	m.hostCopyConfirm = false
	m.hostCopyName = ""
	m.hostDeleteConfirm = false
	m.hostDeleteName = ""
	m.groupDeleteConfirm = false
	m.groupDeleteName = ""
	m.stowInstallPrompt = false
	m.stowInstallAction = stowInstallNone
	m.dangerConfirmRow = -1
	m.dotsConfirmIdx = -1
	m.agentsConfirmIdx = -1
	m.dotsOverwriteIdx = -1
	m.dotsLocalIdx = -1
	m.dotsIgnoreIdx = -1
	m.dotsVariantIdx = -1
	m.dotsForceResolve = ""
	m.dotsVariantMode = dotsVariantNone
	m.stowInstallVariant = dotsVariantRequest{}
	if wipeStatus {
		clearStatus(m)
	}
}

func quitConfirmKeyLabel(key string) string {
	if key == "" {
		return "q"
	}
	return key
}

func (m *Model) handleConfirmTimeoutMsg(msg confirmTimeoutMsg) []tea.Cmd {
	if msg.gen != m.confirmGen || !m.hasActiveConfirmation() {
		return nil
	}
	// Row confirmations carry their own on-screen prompt; the quit hint lives only in the footer legend.
	expiredQuitKey := ""
	if m.confirmQuit {
		expiredQuitKey = quitConfirmKeyLabel(m.quitConfirmKey)
	}
	m.clearActiveConfirmation()
	switch {
	case expiredQuitKey != "":
		return []tea.Cmd{setStatus(m, "quit confirmation expired — press "+expiredQuitKey+" again", false)}
	}
	return nil
}
