package tui

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"
)

func (m *Model) handleProviderScannedMsg(msg providerScannedMsg) []tea.Cmd {
	var cmds []tea.Cmd

	if msg.gen != m.scanGen {
		return cmds
	}
	if !m.scanningProviders[msg.provider] {
		return cmds
	}
	delete(m.scanningProviders, msg.provider)
	if msg.err != nil {
		status := providerScanFailureStatus(msg.provider, msg.err)
		if !m.collectLaunchBatchError(status) {
			cmds = append(cmds, setStatus(m, status, true))
		}
	}
	if len(m.scanningProviders) > 0 && (msg.err == nil || m.launchBatchActive) {
		setActivityStatus(m, providerRefreshStatus(m.scanningProviders))
	}
	// When the last provider finishes: fetch one consistent snapshot now that
	// all upserts are done, and kick off the orphan scan in parallel.
	if len(m.scanningProviders) == 0 {
		m.discoveryGen++
		m.providerSnapshotRefreshing = true
		m.discoveryRefreshing = true
		setActivityStatus(m, "Finding local tools…")
		cmds = append(cmds, m.doFetchFinalTools(msg.gen), m.doRefreshDiscovered(m.discoveryGen))
	}
	if cmd := m.finishLaunchBatchIfIdle(); cmd != nil {
		cmds = append(cmds, cmd)
	}

	return cmds
}

func providerScanFailureStatus(provider string, err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "scan timed out for " + provider
	}
	return "scan failed for " + provider + ": " + err.Error()
}

func (m *Model) handleAllProvidersDoneMsg(msg allProvidersDoneMsg) tea.Cmd {
	if msg.gen != m.scanGen {
		return nil
	}
	m.providerSnapshotRefreshing = false
	if msg.err != nil {
		m.finishSetupReloadIfIdle()
		status := "refresh failed: " + msg.err.Error()
		if m.collectLaunchBatchError(status) {
			return m.finishLaunchBatchIfIdle()
		}
		return setStatus(m, status, true)
	}
	if msg.tools != nil {
		m.allTools = msg.tools
		m.applyFilter()
	}
	if msg.effectiveSystemManager != "" {
		m.effectiveSystemManager = msg.effectiveSystemManager
	}

	m.finishSetupReloadIfIdle()
	if cmd := m.finishLaunchBatchIfIdle(); cmd != nil {
		return cmd
	}
	m.clearActivityStatusIfIdle()
	return nil
}

func (m *Model) handleDiscoveredRefreshedMsg(msg discoveredRefreshedMsg) tea.Cmd {
	if msg.gen != m.discoveryGen {
		return nil
	}
	m.discoveryRefreshing = false
	if msg.err != nil {
		m.finishSetupReloadIfIdle()
		status := "orphan scan failed: " + msg.err.Error()
		if m.collectLaunchBatchError(status) {
			return m.finishLaunchBatchIfIdle()
		}
		return setStatus(m, status, true)
	}
	if msg.discovered != nil {
		m.discoveredTools = msg.discovered
		m.rebuildDiscoveredKeys()
		m.applyFilter()
		if anyMissingDescription(msg.discovered) || anyMissingDescription(m.allTools) {
			return m.startDescriptionRefresh()
		}
	}

	m.finishSetupReloadIfIdle()
	if cmd := m.finishLaunchBatchIfIdle(); cmd != nil {
		return cmd
	}
	m.clearActivityStatusIfIdle()
	return nil
}

func (m *Model) clearActivityStatusIfIdle() {
	if m.launchBatchActive || m.setupReloading || m.launchBatchPending() {
		return
	}
	m.progressText = ""
}
