package tui

import tea "charm.land/bubbletea/v2"

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
		cmds = append(cmds, setStatus(m, "scan failed for "+msg.provider+": "+msg.err.Error(), true))
	}
	// When the last provider finishes: fetch one consistent snapshot now that
	// all upserts are done, and kick off the orphan scan in parallel.
	if len(m.scanningProviders) == 0 {
		m.discoveryGen++
		cmds = append(cmds, m.doFetchFinalTools(msg.gen), m.doRefreshDiscovered(m.discoveryGen))
	}

	return cmds
}

func (m *Model) handleAllProvidersDoneMsg(msg allProvidersDoneMsg) tea.Cmd {
	if msg.gen != m.scanGen {
		return nil
	}
	if msg.err != nil {
		return setStatus(m, "refresh failed: "+msg.err.Error(), true)
	}
	if msg.tools != nil {
		m.allTools = msg.tools
		m.applyFilter()
	}
	if msg.effectiveSystemManager != "" {
		m.effectiveSystemManager = msg.effectiveSystemManager
	}

	return nil
}

func (m *Model) handleDiscoveredRefreshedMsg(msg discoveredRefreshedMsg) tea.Cmd {
	if msg.gen != m.discoveryGen {
		return nil
	}
	if msg.err != nil {
		return setStatus(m, "orphan scan failed: "+msg.err.Error(), true)
	}
	if msg.discovered != nil {
		m.discoveredTools = msg.discovered
		m.rebuildDiscoveredKeys()
		m.applyFilter()
		if anyMissingDescription(msg.discovered) || anyMissingDescription(m.allTools) {
			return m.startDescriptionRefresh()
		}
	}

	return nil
}
