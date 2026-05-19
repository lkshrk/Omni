package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
)

type setupActivationOption struct {
	label  string
	detail string
}

var setupActivationOptions = []setupActivationOption{
	{label: "Review first", detail: "Open Omni without changing tools or dotfiles."},
	{label: "Sync tools", detail: "Install configured missing tools for this host."},
	{label: "Sync dotfiles", detail: "Apply configured dotfile links for this host."},
}

const setupStepCreateConfig = 11

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
		m.setupBackgroundMode = viewStatus
		m.setupStep = 0
		return nil
	}
	if msg.err != nil {
		m.finishSetupReload()
		m.err = msg.err
		return nil
	}
	// Config was just created in setup — advance to provider/import step.
	if m.mode == viewSetup && m.setupStep == setupStepCreateConfig {
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
		m.groupNames = msg.groupNames
		m.toolMemberships = msg.toolMemberships
		m.dotMemberships = msg.dotMemberships
		m.effectivePythonManager = msg.effectivePythonManager
		m.effectiveNodeManager = msg.effectiveNodeManager
		m.effectiveSystemManager = msg.effectiveSystemManager
		m.dotsReminderService = msg.dotsReminderService
		m.dotsReminderServiceErr = msg.dotsReminderServiceErr
		m.dotsReminderInterval = dotsReminderIntervalFromService(msg.dotsReminderService)
		m.dotsWatchService = msg.dotsWatchService
		m.dotsWatchServiceErr = msg.dotsWatchServiceErr
		m.dotsWatchDebounce = dotsWatchDebounceFromService(msg.dotsWatchService)
		m.dotsHistory = append([]app.DotsHistoryEntry(nil), msg.dotsHistory...)
		m.dotsHistoryErr = msg.dotsHistoryErr
		m.hostInfo = msg.hostInfo
		m.setupProviders = msg.setupProviders
		m.setupProviderIdx = 0
		m.setupCopyHostIdx = 0
		m.setupGroupIdx = 0
		m.setupGroupDraft = nil
		m.setupActivationIdx = 0
		m.mode = viewSetup
		m.setupBackgroundMode = viewStatus
		m.setupStep = 2
		if len(m.setupCopyHostNames()) > 0 {
			m.setupStep = 7
		}
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
	m.dotsReminderService = msg.dotsReminderService
	m.dotsReminderServiceErr = msg.dotsReminderServiceErr
	m.dotsReminderInterval = dotsReminderIntervalFromService(msg.dotsReminderService)
	m.dotsWatchService = msg.dotsWatchService
	m.dotsWatchServiceErr = msg.dotsWatchServiceErr
	m.dotsWatchDebounce = dotsWatchDebounceFromService(msg.dotsWatchService)
	m.dotsHistory = append([]app.DotsHistoryEntry(nil), msg.dotsHistory...)
	m.dotsHistoryErr = msg.dotsHistoryErr
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
	if msg.bootstrapRequired && !m.setupComplete {
		m.mode = viewSetup
		m.setupBackgroundMode = viewStatus
		m.setupActivationIdx = 0
		m.setupStep = 10
		return cmds
	}
	if m.setupBackgroundMode == viewDots {
		m.mode = viewDots
		m.setupBackgroundMode = viewStatus
	} else {
		if !isMainTabMode(m.mode) || m.mode == viewSetup {
			m.mode = viewStatus
		}
		m.setupBackgroundMode = viewStatus
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
	// Only suppress the dashboard body during the post-bootstrap reload so
	// the user sees a complete first render after onboarding. On a normal
	// launch the dashboard renders immediately with per-row loading indicators.
	if wasSetupReloading {
		m.beginLaunchBatchIfPending()
	}
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

func (m *Model) handleSetupConfigImportDoneMsg(msg setupConfigImportDoneMsg) []tea.Cmd {
	var cmds []tea.Cmd

	m.loading = false
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
		return cmds
	}
	cmds = append(cmds, setStatus(m, "✓ imported settings", false))
	m.loading = true
	cmds = append(cmds, m.spinner.Tick, loadTools(m.app, m.ctx))
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

func (m *Model) handleSetupHostCopyDoneMsg(msg setupHostCopyDoneMsg) []tea.Cmd {
	var cmds []tea.Cmd

	m.loading = false
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
		return cmds
	}
	if msg.info != nil {
		m.hostInfo = msg.info
	}
	m.hostRequired = false
	cmds = append(cmds, setStatus(m, fmt.Sprintf("✓ copied %s to %s", msg.source, msg.target), false))
	m.finishSetupWithReload(&cmds)
	return cmds
}

func (m *Model) handleSetupHostGroupsDoneMsg(msg setupHostGroupsDoneMsg) []tea.Cmd {
	var cmds []tea.Cmd

	m.loading = false
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
		return cmds
	}
	if msg.info != nil {
		m.hostInfo = msg.info
	}
	m.hostRequired = false
	cmds = append(cmds, setStatus(m, setupGroupsSavedStatus(msg.groups), false))
	m.finishSetupWithReload(&cmds)
	return cmds
}

func (m *Model) handleSetupBootstrapDoneMsg(msg setupBootstrapDoneMsg) []tea.Cmd {
	var cmds []tea.Cmd

	m.loading = false
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
		return cmds
	}
	if msg.message != "" {
		cmds = append(cmds, setStatus(m, "✓ "+msg.message, false))
	}
	m.finishSetupWithReload(&cmds)
	return cmds
}

func (m *Model) handleSetupKeyMsg(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	var cmds []tea.Cmd

	if m.loading {
		return tea.Batch(cmds...), false
	}

	switch m.setupStep {
	case 0: // Import existing config or create a fresh one.
		switch {
		case key.Matches(msg, m.keys.Confirm) || strings.EqualFold(msg.String(), "y"):
			m.filePickerForConfig = true
			cmds = append(cmds, m.openFilePicker("Import settings.json", "", true))
		case strings.EqualFold(msg.String(), "n"):
			m.setupStep = setupStepCreateConfig
			m.loading = true
			startOp(m, "Creating settings.json…")
			cmds = append(cmds, m.spinner.Tick, m.doCreateConfig())
		case key.Matches(msg, m.keys.Back):
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
			cmds = append(cmds, m.spinner.Tick, m.doDisableDots(true, false))
		}
	case 6: // Dots repo path — handled by showFilePicker routing above.
		if key.Matches(msg, m.keys.Back) {
			m.startSetupGroupSelection(&cmds)
		}
	case 7: // Copy another host?
		switch {
		case key.Matches(msg, m.keys.Confirm) || strings.EqualFold(msg.String(), "y"):
			names := m.setupCopyHostNames()
			if len(names) == 0 {
				m.setupStep = 2
				break
			}
			if len(names) == 1 {
				m.loading = true
				startOp(m, "Copying host config…")
				cmds = append(cmds, m.spinner.Tick, m.doSetupCopyHostConfigFrom(names[0]))
				break
			}
			m.setupCopyHostIdx = clampRange(m.setupCopyHostIdx, 0, len(names)-1)
			m.setupStep = 8
		case strings.EqualFold(msg.String(), "n") || key.Matches(msg, m.keys.Back):
			m.setupStep = 2
		}
	case 8: // Host picker for copy.
		names := m.setupCopyHostNames()
		switch {
		case key.Matches(msg, m.keys.Up):
			if m.setupCopyHostIdx > 0 {
				m.setupCopyHostIdx--
			}
		case key.Matches(msg, m.keys.Down):
			if m.setupCopyHostIdx < len(names)-1 {
				m.setupCopyHostIdx++
			}
		case key.Matches(msg, m.keys.Confirm):
			if len(names) == 0 {
				m.setupStep = 2
				break
			}
			source := names[clampRange(m.setupCopyHostIdx, 0, len(names)-1)]
			m.loading = true
			startOp(m, "Copying host config…")
			cmds = append(cmds, m.spinner.Tick, m.doSetupCopyHostConfigFrom(source))
		case key.Matches(msg, m.keys.Back):
			m.setupStep = 2
		}
	case 9: // Reusable group selection.
		switch {
		case key.Matches(msg, m.keys.Up):
			if m.setupGroupIdx > 0 {
				m.setupGroupIdx--
			}
		case key.Matches(msg, m.keys.Down):
			if m.setupGroupIdx < len(m.groupNames)-1 {
				m.setupGroupIdx++
			}
		case key.Matches(msg, m.keys.Toggle):
			if m.setupGroupIdx >= 0 && m.setupGroupIdx < len(m.groupNames) {
				if m.setupGroupDraft == nil {
					m.initSetupGroupDraft()
				}
				group := m.groupNames[m.setupGroupIdx]
				m.setupGroupDraft[group] = !m.setupGroupDraft[group]
			}
		case key.Matches(msg, m.keys.Confirm):
			m.loading = true
			startOp(m, "Saving groups…")
			cmds = append(cmds, m.spinner.Tick, m.doSetupHostGroups(m.setupSelectedGroups()))
		case key.Matches(msg, m.keys.Back):
			m.loading = true
			startOp(m, "Saving groups…")
			cmds = append(cmds, m.spinner.Tick, m.doSetupHostGroups(nil))
		}
	case 10: // Existing-host activation.
		switch {
		case key.Matches(msg, m.keys.Up):
			if m.setupActivationIdx > 0 {
				m.setupActivationIdx--
			}
		case key.Matches(msg, m.keys.Down):
			if m.setupActivationIdx < len(setupActivationOptions)-1 {
				m.setupActivationIdx++
			}
		case key.Matches(msg, m.keys.Confirm):
			switch clampRange(m.setupActivationIdx, 0, len(setupActivationOptions)-1) {
			case 0:
				cmds = append(cmds, setStatus(m, "✓ bootstrap reviewed", false))
				m.finishSetupWithReload(&cmds)
			case 1:
				m.loading = true
				startOp(m, "Syncing tools…")
				cmds = append(cmds, m.spinner.Tick, m.doSetupBootstrapTools())
			case 2:
				if strings.TrimSpace(m.settings.DotsRepo) == "" || config.BoolVal(m.settings.DotsDisabled) {
					cmds = append(cmds, setStatus(m, "dotfile sync is not configured for this host", true))
					break
				}
				if m.promptForStowInstall(stowInstallLaunchSync) {
					break
				}
				m.loading = true
				startOp(m, "Syncing dotfiles…")
				cmds = append(cmds, m.spinner.Tick, m.doSetupBootstrapDots())
			}
		case key.Matches(msg, m.keys.Back):
			cmds = append(cmds, setStatus(m, "✓ bootstrap skipped", false))
			m.finishSetupWithReload(&cmds)
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

func (m Model) setupCopyHostNames() []string {
	return sortedHostNames(m.hostInfo)
}

func (m *Model) startSetupGroupSelection(cmds *[]tea.Cmd) {
	if len(m.groupNames) == 0 {
		m.finishSetupWithReload(cmds)
		return
	}
	m.loading = false
	m.setupStep = 9
	m.setupGroupIdx = clampRange(m.setupGroupIdx, 0, len(m.groupNames)-1)
	m.initSetupGroupDraft()
}

func (m *Model) initSetupGroupDraft() {
	draft := make(map[string]bool, len(m.groupNames))
	for _, group := range m.groupNames {
		draft[group] = false
	}
	if m.hostInfo != nil {
		host := m.hostInfo.Active
		if host == "" {
			host = shortHostname()
		}
		if assignment, ok := m.hostInfo.Hosts[host]; ok {
			for _, group := range assignment.Groups {
				if _, exists := draft[group]; exists {
					draft[group] = true
				}
			}
		}
	}
	m.setupGroupDraft = draft
}

func (m Model) setupSelectedGroups() []string {
	if len(m.groupNames) == 0 || len(m.setupGroupDraft) == 0 {
		return nil
	}
	groups := make([]string, 0, len(m.groupNames))
	for _, group := range m.groupNames {
		if m.setupGroupDraft[group] {
			groups = append(groups, group)
		}
	}
	return groups
}

func (m *Model) finishSetupWithReload(cmds *[]tea.Cmd) {
	m.markBootstrapComplete(cmds)
	targetMode := m.setupBackgroundMode
	if !isMainTabMode(targetMode) || targetMode == viewSetup {
		targetMode = viewStatus
	}
	m.mode = targetMode
	m.setupBackgroundMode = targetMode
	m.setupStep = 0
	m.setupCopyHostIdx = 0
	m.setupGroupIdx = 0
	m.setupGroupDraft = nil
	m.setupActivationIdx = 0
	m.hostRequired = false
	m.setupComplete = true
	m.setupReloading = true
	m.progressText = "Loading tools…"
	m.loading = true
	*cmds = append(*cmds, m.spinner.Tick, loadTools(m.app, m.ctx))
}

func (m *Model) markBootstrapComplete(cmds *[]tea.Cmd) {
	if m.app == nil {
		return
	}
	host := shortHostname()
	if m.hostInfo != nil && m.hostInfo.Active != "" {
		host = m.hostInfo.Active
	}
	if err := m.app.MarkHostBootstrapCompleted(m.ctx, host); err != nil {
		*cmds = append(*cmds, setStatus(m, "✗ bootstrap marker: "+err.Error(), true))
	}
}

func setupGroupsSavedStatus(groups []string) string {
	if len(groups) == 0 {
		return "✓ no reusable groups selected"
	}
	return fmt.Sprintf("✓ %d reusable groups selected", len(groups))
}
