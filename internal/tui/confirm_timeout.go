package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
)

// Has to outlast reading the hint that describes it and deciding: the previous 5s expired while users were still reading, and a silent re-arm on the second press reads as a frozen app.
const confirmTimeout = 8 * time.Second

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
		m.agentsResolveConfirm ||
		m.agentsBulkResolveConfirm ||
		m.agentsSyncAllConfirm ||
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
	m.agentsResolveConfirm = false
	m.agentsResolveUseLocal = false
	m.agentsResolveSource = ""
	m.agentsResolveOpKey = ""
	m.agentsBulkResolveConfirm = false
	m.agentsBulkResolveUseManaged = false
	m.agentsSyncAllConfirm = false
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
	// Row confirmations carry their own on-screen prompt; the quit and agents sync-all hints live only in the footer legend, where a silent disarm is indistinguishable from a dead key.
	expiredSyncAll := m.agentsSyncAllConfirm
	expiredQuitKey := ""
	if m.confirmQuit {
		expiredQuitKey = quitConfirmKeyLabel(m.quitConfirmKey)
	}
	m.clearActiveConfirmation()
	switch {
	case expiredSyncAll:
		return []tea.Cmd{setStatus(m, "sync all confirmation expired — press S again", false)}
	case expiredQuitKey != "":
		return []tea.Cmd{setStatus(m, "quit confirmation expired — press "+expiredQuitKey+" again", false)}
	}
	return nil
}
