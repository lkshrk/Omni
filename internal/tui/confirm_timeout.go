package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
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
		m.dashboardReconcilePlanOpen ||
		m.hostCopyConfirm ||
		m.hostDeleteConfirm ||
		m.groupDeleteConfirm ||
		m.stowInstallPrompt ||
		m.dangerConfirmRow >= 0 ||
		m.dotsConfirmIdx >= 0 ||
		m.dotsOverwriteIdx >= 0 ||
		m.dotsLocalIdx >= 0 ||
		m.dotsIgnoreIdx >= 0 ||
		m.dotsVariantIdx >= 0 ||
		m.dotsForceResolve != "" ||
		m.mcpDeleteConfirm ||
		m.pluginDeleteConfirm ||
		m.marketplaceDeleteConfirm ||
		m.agentsDeleteConfirm ||
		m.agentsIgnoreConfirm ||
		m.pluginMarketplaceOfferConfirm
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
	m.dotsOverwriteIdx = -1
	m.dotsLocalIdx = -1
	m.dotsIgnoreIdx = -1
	m.dotsVariantIdx = -1
	m.dotsForceResolve = ""
	m.dotsVariantMode = dotsVariantNone
	m.stowInstallVariant = dotsVariantRequest{}
	m.mcpDeleteConfirm = false
	m.mcpDeleteName = ""
	m.pluginDeleteConfirm = false
	m.pluginDeleteName = ""
	m.marketplaceDeleteConfirm = false
	m.marketplaceDeleteName = ""
	m.agentsDeleteConfirm = false
	m.agentsDeleteUninstall = false
	m.agentsDeleteName = ""
	m.agentsIgnoreConfirm = false
	m.agentsIgnoreName = ""
	m.pluginMarketplaceOfferConfirm = false
	m.pluginMarketplaceOfferAgentID = ""
	m.pluginMarketplaceOfferPlugin = app.InstalledPlugin{}
	m.pluginMarketplaceOfferGroup = ""
	m.pluginMarketplaceOfferMarket = ""
	m.pluginMarketplaceOfferSource = ""
	m.pluginMarketplaceOfferOpKey = ""
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
