package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

const confirmTimeout = 5 * time.Second

type confirmTimeoutMsg struct {
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

func (m *Model) hasActiveConfirmation() bool {
	return m.confirmQuit ||
		m.listConfirm.action != "" ||
		m.hostCopyConfirm ||
		m.hostDeleteConfirm ||
		m.groupDeleteConfirm ||
		m.stowInstallPrompt ||
		m.dangerConfirmRow >= 0 ||
		m.dotsConfirmIdx >= 0 ||
		m.dotsOverwriteIdx >= 0 ||
		m.dotsLocalIdx >= 0 ||
		m.dotsIgnoreIdx >= 0 ||
		m.dotsVariantIdx >= 0
}

func (m *Model) clearActiveConfirmation() {
	wipeStatus := m.confirmQuit || m.listConfirm.action != ""
	m.confirmQuit = false
	m.quitConfirmKey = ""
	m.listConfirm = listConfirmation{}
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
	m.dotsOverwriteIdx = -1
	m.dotsLocalIdx = -1
	m.dotsIgnoreIdx = -1
	m.dotsVariantIdx = -1
	m.dotsVariantMode = dotsVariantNone
	m.stowInstallVariant = dotsVariantRequest{}
	if wipeStatus {
		clearStatus(m)
	}
}

func (m *Model) handleConfirmTimeoutMsg(msg confirmTimeoutMsg) []tea.Cmd {
	if msg.gen != m.confirmGen || !m.hasActiveConfirmation() {
		return nil
	}
	m.clearActiveConfirmation()
	return nil
}
