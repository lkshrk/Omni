package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
)

func (m *Model) handleToolsLoadedMsg(msg toolsLoadedMsg) []tea.Cmd {
	var cmds []tea.Cmd

	wasSetupReloading := m.setupReloading
	m.loading = false
	if !wasSetupReloading {
		m.setupReloading = false
	}
	if msg.noConfig {
		m.finishSetupReload()
		m.mode = viewSetup
		m.setupBackgroundMode = viewList
		m.setupStep = 0
		return nil
	}
	if msg.err != nil {
		m.finishSetupReload()
		m.err = msg.err
		return nil
	}
	// Config was just created in setup step 0 — advance to provider/import step.
	if m.mode == viewSetup && m.setupStep == 0 {
		m.setupStep = 1
		m.setupProviders = msg.setupProviders
		m.setupProviderIdx = 0
		return nil
	}

	if msg.noHost && !m.setupComplete {
		// Config exists but no host entry matches this machine. Keep the
		// background list as the pre-onboarding snapshot; fresh scans/reloads run
		// only after onboarding exits.
		m.settings = msg.settings
		m.taps = msg.taps
		m.effectivePythonManager = msg.effectivePythonManager
		m.effectiveNodeManager = msg.effectiveNodeManager
		m.effectiveSystemManager = msg.effectiveSystemManager
		m.hostInfo = msg.hostInfo
		m.setupProviders = msg.setupProviders
		m.setupProviderIdx = 0
		m.mode = viewSetup
		m.setupBackgroundMode = viewList
		m.setupStep = 2
		return cmds
	}
	m.setupComplete = false

	m.allTools = msg.tools
	m.discoveredTools = msg.discovered
	m.rebuildDiscoveredKeys()
	m.settings = msg.settings
	m.taps = msg.taps
	m.groupNames = msg.groupNames
	m.toolGroups = msg.toolGroups
	m.toolMemberships = msg.toolMemberships
	m.dotMemberships = msg.dotMemberships
	m.ignoreLabels = msg.ignoreLabels
	m.toolIgnoreSet = msg.toolIgnoreSet
	m.groupIgnoreSet = msg.groupIgnoreSet
	m.toolProviderPins = msg.toolProviderPins
	m.configuredProviders = append([]string(nil), msg.configuredProviders...)
	m.effectivePythonManager = msg.effectivePythonManager
	m.effectiveNodeManager = msg.effectiveNodeManager
	m.effectiveSystemManager = msg.effectiveSystemManager
	m.stowInstalled = msg.stowInstalled
	if len(msg.ecosystemProviders) > 0 {
		m.providerNames = append([]string(nil), msg.ecosystemProviders...)
	}
	m.hostInfo = msg.hostInfo
	m.ignoreSet = make(map[string]bool, len(msg.ignoreList))
	for _, name := range msg.ignoreList {
		m.ignoreSet[name] = true
	}
	if m.app != nil {
		m.consolidateOptions = m.app.ConsolidateOptions()
	}
	m.applyFilter()
	if m.setupBackgroundMode == viewDots {
		m.mode = viewDots
		m.setupBackgroundMode = viewList
	} else {
		m.mode = viewList
		m.setupBackgroundMode = viewList
	}
	m.hostRequired = false

	m.prepareDotsSnapshotOnLaunch(&cmds)

	if m.settings.DotsRepo != "" && !config.BoolVal(m.settings.DotsDisabled) && !m.dotsLoaded && !m.dotsLoading {
		if m.promptForStowInstall(stowInstallLaunchSync) {
			return cmds
		}
		m.beginDotsOperation("Syncing dots…")
		cmds = append(cmds, m.spinner.Tick, m.doDotsSyncOnly())
	}

	cmds = append(cmds, m.startPostLoadBackgroundTasks()...)
	m.beginLaunchBatchIfPending()
	m.finishSetupReloadIfIdle()
	return cmds
}

func (m *Model) handleSetupImportDoneMsg(msg setupImportDoneMsg) []tea.Cmd {
	var cmds []tea.Cmd

	m.loading = false
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
		if msg.hostInfo != nil {
			m.hostInfo = msg.hostInfo
			if msg.hostInfo.Active != "" {
				m.hostRequired = false
			}
		}
		return cmds
	} else if msg.added > 0 {
		cmds = append(cmds, setStatus(m, fmt.Sprintf("✓ %d imported", msg.added), false))
	} else {
		cmds = append(cmds, setStatus(m, "✓ nothing to import", false))
	}
	if msg.hostInfo != nil {
		m.hostInfo = msg.hostInfo
		if msg.hostInfo.Active != "" {
			m.hostRequired = false
		}
	}
	m.advanceSetupPastProviders(&cmds)
	return cmds
}

func (m *Model) handleSetupProvidersDoneMsg(msg setupProvidersDoneMsg) []tea.Cmd {
	var cmds []tea.Cmd

	m.loading = false
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
		return cmds
	}
	m.advanceSetupPastProviders(&cmds)
	return cmds
}

func (m *Model) handleSetupNodeMgrDoneMsg(msg setupNodeMgrDoneMsg) []tea.Cmd {
	var cmds []tea.Cmd

	m.loading = false
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
		return cmds
	}
	m.startSetupHostCreation(&cmds)
	return cmds
}

func (m *Model) handleSetupHostDoneMsg(msg setupHostDoneMsg) []tea.Cmd {
	var cmds []tea.Cmd

	m.loading = false
	if msg.err != nil {
		if m.setupHostReturnStep != 0 {
			m.setupStep = m.setupHostReturnStep
			m.setupHostReturnStep = 0
		}
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
		return cmds
	} else if msg.hostName != "" {
		m.setupHostReturnStep = 0
		m.hostInfo = msg.info
		m.hostRequired = false
		cmds = append(cmds, setStatus(m, "✓ host "+msg.hostName+" created", false))
	}
	m.settingsInput.Blur()
	m.setupStep = 5
	return cmds
}

func (m *Model) handleSetupKeyMsg(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	var cmds []tea.Cmd

	if m.loading {
		return tea.Batch(cmds...), false
	}

	switch m.setupStep {
	case 0: // Create config.
		switch {
		case key.Matches(msg, m.keys.Confirm) || strings.EqualFold(msg.String(), "y"):
			m.loading = true
			startOp(m, "Creating settings.json…")
			cmds = append(cmds, m.spinner.Tick, m.doCreateConfig())
		case strings.EqualFold(msg.String(), "n") || key.Matches(msg, m.keys.Back):
			m.shutdown()
			return tea.Quit, true
		}
	case 1, 2: // Provider selection (step 1: first-run + import; step 2: no-host re-run)
		switch {
		case key.Matches(msg, m.keys.Up):
			if m.setupProviderIdx > 0 {
				m.setupProviderIdx--
			}
		case key.Matches(msg, m.keys.Down):
			if m.setupProviderIdx < len(m.setupProviders)-1 {
				m.setupProviderIdx++
			}
		case key.Matches(msg, m.keys.Toggle):
			if m.setupProviderIdx >= 0 && m.setupProviderIdx < len(m.setupProviders) {
				m.setupProviders[m.setupProviderIdx].enabled = !m.setupProviders[m.setupProviderIdx].enabled
			}
		case key.Matches(msg, m.keys.Confirm):
			m.confirmSetupProviders(&cmds)
		}
	case 3: // Node manager selection
		switch {
		case key.Matches(msg, m.keys.Up):
			if m.setupNodeMgrIdx > 0 {
				m.setupNodeMgrIdx--
			}
		case key.Matches(msg, m.keys.Down):
			if m.setupNodeMgrIdx < len(nodeMgrChoices)-1 {
				m.setupNodeMgrIdx++
			}
		case key.Matches(msg, m.keys.Confirm):
			chosen := nodeMgrChoices[m.setupNodeMgrIdx].value
			if chosen == "" {
				m.startSetupHostCreation(&cmds)
			} else {
				m.loading = true
				startOp(m, "Saving…")
				cmds = append(cmds, m.spinner.Tick, m.doSaveNodeManager(chosen))
			}
		case key.Matches(msg, m.keys.Back):
			m.startSetupHostCreation(&cmds)
		}
	case 5: // Enable dotfile sync.
		switch {
		case key.Matches(msg, m.keys.Confirm) || strings.EqualFold(msg.String(), "y"):
			m.setupStep = 6
			cmds = append(cmds, m.openFilePicker("Dots repo path", "", false))
		case strings.EqualFold(msg.String(), "n") || key.Matches(msg, m.keys.Back):
			m.loading = true
			startOp(m, "Disabling dots…")
			cmds = append(cmds, m.spinner.Tick, m.doDisableDots(true, true))
		}
	case 6: // Dots repo path — handled by showFilePicker routing above.
		if key.Matches(msg, m.keys.Back) {
			m.loading = true
			startOp(m, "Loading…")
			cmds = append(cmds, m.spinner.Tick, loadTools(m.app, m.ctx))
		}
	}

	return tea.Batch(cmds...), false
}

func (m *Model) confirmSetupProviders(cmds *[]tea.Cmd) {
	var disabled []string
	for _, row := range m.setupProviders {
		if !row.enabled {
			disabled = append(disabled, row.name)
		}
	}
	if m.setupStep == 1 {
		m.loading = true
		startOp(m, "Importing tools…")
		*cmds = append(*cmds, m.spinner.Tick, m.doSetupImport(disabled))
		return
	}
	if len(disabled) > 0 {
		m.loading = true
		startOp(m, "Saving providers…")
		*cmds = append(*cmds, m.spinner.Tick, m.doSaveDisabledProviders(disabled))
		return
	}
	m.advanceSetupPastProviders(cmds)
}

func (m *Model) startCurrentProviderScans() []tea.Cmd {
	var cmds []tea.Cmd

	// Use the UNION of DB-row providers and config-declared providers so scans
	// run on first launch after import. Import() writes JSON config, not DB rows.
	m.scanningProviders = m.currentProviderScanSet()
	if len(m.scanningProviders) == 0 {
		return nil
	}
	setActivityStatus(m, providerRefreshStatus(m.scanningProviders))
	m.scanGen++
	gen := m.scanGen
	cmds = append(cmds, m.spinner.Tick)
	for prov := range m.scanningProviders {
		cmds = append(cmds, m.doScanProvider(prov, gen))
	}
	return cmds
}

func (m Model) currentProviderScanSet() map[string]bool {
	providers := make(map[string]bool)
	for _, p := range m.configuredProviders {
		if m.providerScanCoveredByConfiguredEcosystem(p) {
			continue
		}
		providers[p] = true
	}
	for _, t := range m.allTools {
		if m.providerScanCoveredByConfiguredEcosystem(t.Provider) {
			continue
		}
		providers[t.Provider] = true
	}
	return providers
}

func (m Model) providerScanCoveredByConfiguredEcosystem(prov string) bool {
	if prov == "" {
		return true
	}
	configured := make(map[string]bool, len(m.configuredProviders))
	for _, name := range m.configuredProviders {
		configured[name] = true
	}
	switch prov {
	case m.effectiveSystemManager:
		return configured[provider.EcosystemSystem]
	case m.effectiveNodeManager:
		return configured[provider.EcosystemNode]
	case m.effectivePythonManager:
		return configured[provider.EcosystemPython]
	default:
		return false
	}
}

func (m *Model) startPostLoadBackgroundTasks() []tea.Cmd {
	cmds := m.startCurrentProviderScans()
	if anyMissingDescription(m.allTools) {
		cmds = append(cmds, m.startDescriptionRefresh())
	}
	m.finishSetupReloadIfIdle()
	return cmds
}

func (m *Model) setupReloadPending() bool {
	return m.loading ||
		len(m.scanningProviders) > 0 ||
		m.providerSnapshotRefreshing ||
		m.discoveryRefreshing ||
		m.descRefreshing
}

func (m *Model) finishSetupReloadIfIdle() {
	if m.setupReloading && !m.setupReloadPending() {
		m.finishSetupReload()
	}
}

func (m *Model) finishSetupReload() {
	m.setupReloading = false
	m.progressText = ""
}

func (m *Model) advanceSetupPastProviders(cmds *[]tea.Cmd) {
	if isNodeProviderEnabled(m.setupProviders) {
		m.setupStep = 3
		return
	}
	m.startSetupHostCreation(cmds)
}

func (m *Model) startSetupHostCreation(cmds *[]tea.Cmd) {
	name := strings.TrimSpace(m.defaultSetupHostName())
	if m.setupStep != 5 {
		m.setupHostReturnStep = m.setupStep
	}
	m.settingsInput.Blur()
	m.setupStep = 5
	if m.hostInfo != nil && m.hostInfo.Active == name {
		if _, ok := m.hostInfo.Hosts[name]; ok {
			m.setupHostReturnStep = 0
			m.hostRequired = false
			return
		}
	}
	m.loading = true
	startOp(m, "Preparing this machine…")
	*cmds = append(*cmds, m.spinner.Tick, m.doSetupHost(name))
}

func (m *Model) defaultSetupHostName() string {
	return shortHostname()
}
