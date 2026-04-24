package tui

import (
	"time"

	"charm.land/bubbles/v2/textinput"
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
		m.setupExitConfirm ||
		m.profileDeleteConfirm ||
		m.groupDeleteConfirm ||
		m.stowInstallPrompt ||
		m.dangerConfirmRow >= 0 ||
		m.dotsConfirmIdx >= 0 ||
		m.dotsOverwriteIdx >= 0 ||
		m.dotsLocalIdx >= 0 ||
		m.dotsIgnoreIdx >= 0
}

func (m *Model) clearActiveConfirmation() {
	wipeStatus := m.confirmQuit || m.listConfirm.action != ""
	m.confirmQuit = false
	m.quitConfirmKey = ""
	m.listConfirm = listConfirmation{}
	m.setupExitConfirm = false
	m.profileDeleteConfirm = false
	m.profileDeleteName = ""
	m.groupDeleteConfirm = false
	m.groupDeleteName = ""
	m.stowInstallPrompt = false
	m.stowInstallAction = stowInstallNone
	m.dangerConfirmRow = -1
	m.dotsConfirmIdx = -1
	m.dotsOverwriteIdx = -1
	m.dotsLocalIdx = -1
	m.dotsIgnoreIdx = -1
	if wipeStatus {
		clearStatus(m)
	}
}

func (m *Model) handleConfirmTimeoutMsg(msg confirmTimeoutMsg) []tea.Cmd {
	if msg.gen != m.confirmGen || !m.hasActiveConfirmation() {
		return nil
	}
	refocusSetupInput := m.setupExitConfirm
	m.clearActiveConfirmation()
	var cmds []tea.Cmd
	if refocusSetupInput {
		m.settingsInput.Focus()
		cmds = append(cmds, textinput.Blink)
	}
	return cmds
}
