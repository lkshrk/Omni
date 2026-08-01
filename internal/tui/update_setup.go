package tui

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
)

// Bounds how long the post-onboarding overlay may block the UI while background scans are still pending.
const setupReloadTimeout = 25 * time.Second

type setupReloadTimeoutMsg struct {
	gen int
}

type setupActivationOption struct {
	label  string
	detail string
}

var setupActivationOptions = []setupActivationOption{
	{label: "Review first", detail: "Open Omni without changing tools or dotfiles."},
	{label: "Sync tools", detail: "Install configured missing tools for this host."},
	{label: "Sync dotfiles", detail: "Apply configured dotfile links for this host."},
}

const (
	setupStepCreateConfig     = 11
	setupStepImportAdvisories = 12
	setupStepMax              = setupStepImportAdvisories
)

func (m *Model) handleToolsLoadedMsg(msg toolsLoadedMsg) []tea.Cmd {
	var cmds []tea.Cmd

	wasSetupReloading := m.setupReloading
	m.loading = false
	if !wasSetupReloading {
		m.setupReloading = false
	}
	if msg.err == nil && m.startupLoadErr {
		m.err = nil
		m.startupLoadErr = false
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
		m.startupLoadErr = true
		return nil
	}
	if m.mode == viewSetup && m.setupStep == setupStepCreateConfig {
		m.setupStep = 1
		m.setupProviders = msg.setupProviders
		m.setupProviderIdx = 0
		return nil
	}

	if msg.noHost && !m.setupComplete {
		// Keep the background list as the pre-onboarding snapshot; fresh scans and reloads run only after onboarding exits.
		m.setSettings(msg.settings)
		m.agentsEnabled = msg.agentsEnabled
		m.skillsEnabled = msg.skillsEnabled
		m.mcpEnabled = msg.mcpEnabled
		m.pluginsEnabled = msg.pluginsEnabled
		m.enabledAgents = msg.enabledAgents
		m.agentsIgnore = msg.agentsIgnore
		if msg.dotsConfiguredKnown {
			m.dotsConfiguredCached = msg.dotsConfigured
		}
		if msg.dotsSyncAvailKnown {
			m.dotsSyncAvailCached = msg.dotsSyncAvail
		}
		m.taps = msg.taps
		m.groupNames = msg.groupNames
		m.toolMemberships = msg.toolMemberships
		m.dotMemberships = msg.dotMemberships
		m.effectivePythonManager = msg.effectivePythonManager
		m.effectiveNodeManager = msg.effectiveNodeManager
		m.effectiveSystemManager = msg.effectiveSystemManager
		m.nvmManaged = msg.nvmManaged
		m.dotsReminderService = msg.dotsReminderService
		m.dotsReminderServiceErr = msg.dotsReminderServiceErr
		m.dotsReminderInterval = app.DotsReminderInterval(msg.dotsReminderService)
		m.dotsWatchService = msg.dotsWatchService
		m.dotsWatchServiceErr = msg.dotsWatchServiceErr
		m.dotsWatchDebounce = app.DotsWatchDebounce(msg.dotsWatchService)
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
	m.setSettings(msg.settings)
	m.agentsEnabled = msg.agentsEnabled
	m.skillsEnabled = msg.skillsEnabled
	m.mcpEnabled = msg.mcpEnabled
	m.pluginsEnabled = msg.pluginsEnabled
	m.enabledAgents = msg.enabledAgents
	m.agentsIgnore = msg.agentsIgnore
	m.seedAgentsRowsFromCache(msg.agentsRows)
	if m.skillsSectionEnabled() && !m.skillsLoaded {
		m.skillsLoaded = true
		cmds = append(cmds, m.loadSkillsManifestCmd())
	}
	if m.mcpSectionEnabled() && !m.mcpLoaded {
		m.mcpLoaded = true
		m.mcpRunning = true
		cmds = append(cmds, m.spinner.Tick, m.doLoadMcpRows())
	}
	if m.pluginsSectionEnabled() && !m.pluginLoaded {
		m.pluginLoaded = true
		m.pluginRunning = true
		cmds = append(cmds, m.spinner.Tick, m.doLoadPluginRows())
	}
	if m.marketplacesSectionEnabled() && !m.marketplaceLoaded {
		m.marketplaceLoaded = true
		m.marketplaceRunning = true
		cmds = append(cmds, m.spinner.Tick, m.doLoadMarketplaceRows())
	}
	if msg.dotsConfiguredKnown {
		m.dotsConfiguredCached = msg.dotsConfigured
	}
	if msg.dotsSyncAvailKnown {
		m.dotsSyncAvailCached = msg.dotsSyncAvail
	}
	m.taps = msg.taps
	m.groupNames = msg.groupNames
	m.toolGroups = msg.toolGroups
	m.toolMemberships = msg.toolMemberships
	m.dotMemberships = msg.dotMemberships
	m.ignoreLabels = msg.ignoreLabels
	m.toolIgnoreSet = msg.toolIgnoreSet
	m.groupIgnoreSet = msg.groupIgnoreSet
	m.toolProviderPins = msg.toolProviderPins
	m.toolProviderCandidates = msg.toolProviderCandidates
	m.toolFallbacks = msg.toolFallbacks
	m.toolGit = msg.toolGit
	m.effectivePythonManager = msg.effectivePythonManager
	m.effectiveNodeManager = msg.effectiveNodeManager
	m.effectiveSystemManager = msg.effectiveSystemManager
	m.nvmManaged = msg.nvmManaged
	m.stowInstalled = msg.stowInstalled
	m.dotsReminderService = msg.dotsReminderService
	m.dotsReminderServiceErr = msg.dotsReminderServiceErr
	m.dotsReminderInterval = app.DotsReminderInterval(msg.dotsReminderService)
	m.dotsWatchService = msg.dotsWatchService
	m.dotsWatchServiceErr = msg.dotsWatchServiceErr
	m.dotsWatchDebounce = app.DotsWatchDebounce(msg.dotsWatchService)
	m.dotsHistory = append([]app.DotsHistoryEntry(nil), msg.dotsHistory...)
	m.dotsHistoryErr = msg.dotsHistoryErr
	if msg.dotsState != nil && msg.dotsState.Loaded {
		m.applyDotsSnapshot(append([]app.DotStatus(nil), msg.dotsState.Entries...), msg.dotsState.GitStatus, msg.dotMemberships)
	}
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
		m.setupBackgroundMode = viewStatus
	} else {
		if !isMainTabMode(m.mode) || m.mode == viewSetup {
			m.mode = viewStatus
		}
		m.setupBackgroundMode = viewStatus
	}
	m.hostRequired = false

	m.prepareDotsSnapshotOnLaunch(&cmds)

	skipLaunchDotsSync := m.skipLaunchDotsSyncOnce
	m.skipLaunchDotsSyncOnce = false
	if m.dotsSyncConfigured() && !m.dotsLoading && !skipLaunchDotsSync {
		if m.promptForStowInstall(stowInstallLaunchSync) {
			return cmds
		}
		m.beginDotsOperation("Syncing dots…")
		cmds = append(cmds, m.spinner.Tick, m.doLaunchDotsSyncOnly())
	}

	cmds = append(cmds, m.doLoadNvmManaged())
	cmds = append(cmds, m.startPostLoadBackgroundTasks()...)
	// Only suppress the dashboard body during the post-bootstrap reload; a normal launch renders immediately with per-row loading indicators.
	if wasSetupReloading {
		m.beginLaunchBatchIfPending()
	}
	m.finishSetupReloadIfIdle()
	return cmds
}

func (m *Model) dotsSyncConfigured() bool {
	return m.dotsSyncAvailability().Configured
}

func (m *Model) dotsConfigured() bool {
	return m.dotsConfiguredCached
}

func (m *Model) dotsSyncAvailability() app.DotsSyncAvailability {
	if m.dotsSyncAvailCached.Reason == "" && !m.dotsSyncAvailCached.Configured {
		return app.DotsSyncAvailability{Reason: app.DotsSyncAvailabilityUnknown}
	}
	return m.dotsSyncAvailCached
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
	m.beginLoading(loadingOwnerLocalOp)
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
	if msg.action == "sync-dots" {
		m.skipLaunchDotsSyncOnce = true
	}
	m.finishSetupWithReload(&cmds)
	return cmds
}

func (m *Model) handleSetupAgentsDiffMsg(msg setupAgentsDiffMsg) []tea.Cmd {
	m.setupAgentsDiffLoading = false
	if msg.err != nil {
		return []tea.Cmd{setStatus(m, "✗ "+msg.err.Error(), true)}
	}
	m.setupAgentsDiffLoaded = true
	m.setupAgentsUnmanagedSkills = msg.unmanagedSkills
	m.setupAgentsUnmanagedMcp = msg.unmanagedMcp
	m.setupAgentsUnmanagedPlugins = msg.unmanagedPlugins
	return nil
}

func (m *Model) handleSetupAgentsImportDoneMsg(msg setupAgentsImportDoneMsg) []tea.Cmd {
	var cmds []tea.Cmd

	m.loading = false
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
		return cmds
	}
	total := msg.skills + msg.mcp + msg.plugins
	if total > 0 {
		cmds = append(cmds, setStatus(m, fmt.Sprintf("✓ imported %d skills · %d mcp servers · %d plugins", msg.skills, msg.mcp, msg.plugins), false))
	} else {
		cmds = append(cmds, setStatus(m, "✓ nothing to import", false))
	}
	if len(msg.advisories) > 0 {
		m.startSetupImportAdvisories(msg.advisories)
		return cmds
	}
	m.startSetupGroupSelection(&cmds)
	return cmds
}

// Keep credential-adoption disclosure on a dedicated step because status truncation and reloads can hide it.
func (m *Model) startSetupImportAdvisories(advisories []string) {
	m.loading = false
	m.setupImportNotices = advisories
	m.setupStep = setupStepImportAdvisories
}

func (m *Model) handleSetupKeyMsg(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	var cmds []tea.Cmd

	if m.loading {
		return tea.Batch(cmds...), false
	}

	// When the priority editor closes, saved or cancelled, the wizard continues to host creation.
	if m.editingPriority {
		pcmds := m.handleSettingsPriorityKeyMsg(msg)
		if !m.editingPriority {
			m.startSetupHostCreation(&pcmds)
		}
		return tea.Batch(pcmds...), true
	}

	switch m.setupStep {
	case 0: // Import existing config or create a fresh one.
		switch {
		case key.Matches(msg, m.keys.Confirm) || strings.EqualFold(msg.String(), "y"):
			m.filePickerForConfig = true
			cmds = append(cmds, m.openFilePicker("Import settings.json", "", true))
		case strings.EqualFold(msg.String(), "n"):
			m.setupStep = setupStepCreateConfig
			m.beginLoading(loadingOwnerLocalOp)
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
				m.setupProviders[m.setupProviderIdx].Enabled = !m.setupProviders[m.setupProviderIdx].Enabled
			}
		case key.Matches(msg, m.keys.Confirm):
			m.confirmSetupProviders(&cmds)
		}
	case 4: // Agents onboarding.
		if m.setupAgentsDiffLoading {
			break
		}
		switch {
		case strings.EqualFold(msg.String(), "i"):
			m.beginLoading(loadingOwnerLocalOp)
			startOp(m, "Importing agents state…")
			cmds = append(cmds, m.spinner.Tick, m.doSetupAgentsImportAll())
		case strings.EqualFold(msg.String(), "s") || key.Matches(msg, m.keys.Back):
			cmds = append(cmds, setStatus(m, "✓ agents onboarding skipped", false))
			m.startSetupGroupSelection(&cmds)
		}
	case 5: // Enable dotfile sync.
		switch {
		case key.Matches(msg, m.keys.Confirm) || strings.EqualFold(msg.String(), "y"):
			m.setupStep = 6
			cmds = append(cmds, m.openFilePicker("Dots repo path", "", false))
		case strings.EqualFold(msg.String(), "n") || key.Matches(msg, m.keys.Back):
			m.beginLoading(loadingOwnerLocalOp)
			startOp(m, "Disabling dots…")
			cmds = append(cmds, m.spinner.Tick, m.doDisableDots(true, false))
		}
	case 6: // Dots repo path — handled by showFilePicker routing above.
		if key.Matches(msg, m.keys.Back) {
			m.startSetupAgentsStep(&cmds)
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
				m.beginLoading(loadingOwnerLocalOp)
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
			m.beginLoading(loadingOwnerLocalOp)
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
			m.beginLoading(loadingOwnerLocalOp)
			startOp(m, "Saving groups…")
			cmds = append(cmds, m.spinner.Tick, m.doSetupHostGroups(m.setupSelectedGroups()))
		case key.Matches(msg, m.keys.Back):
			m.beginLoading(loadingOwnerLocalOp)
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
				m.beginLoading(loadingOwnerLocalOp)
				startOp(m, "Syncing tools…")
				cmds = append(cmds, m.spinner.Tick, m.doSetupBootstrapTools())
			case 2:
				if !m.dotsSyncConfigured() {
					cmds = append(cmds, setStatus(m, "dotfile sync is not configured for this host", true))
					break
				}
				if m.promptForStowInstall(stowInstallLaunchSync) {
					break
				}
				m.beginLoading(loadingOwnerLocalOp)
				startOp(m, "Syncing dotfiles…")
				cmds = append(cmds, m.spinner.Tick, m.doSetupBootstrapDots())
			}
		case key.Matches(msg, m.keys.Back):
			cmds = append(cmds, setStatus(m, "✓ bootstrap skipped", false))
			m.finishSetupWithReload(&cmds)
		}
	case setupStepImportAdvisories:
		if key.Matches(msg, m.keys.Confirm) || key.Matches(msg, m.keys.Back) {
			m.setupImportNotices = nil
			m.startSetupGroupSelection(&cmds)
		}
	}

	return tea.Batch(cmds...), false
}

func (m *Model) confirmSetupProviders(cmds *[]tea.Cmd) {
	disabled := app.SetupDisabledProviders(m.setupProviders)
	if m.setupStep == 1 {
		m.beginLoading(loadingOwnerLocalOp)
		startOp(m, "Importing tools…")
		*cmds = append(*cmds, m.spinner.Tick, m.doSetupImport(disabled))
		return
	}
	if len(disabled) > 0 {
		m.beginLoading(loadingOwnerLocalOp)
		startOp(m, "Saving providers…")
		*cmds = append(*cmds, m.spinner.Tick, m.doSaveDisabledProviders(disabled))
		return
	}
	m.advanceSetupPastProviders(cmds)
}

func (m *Model) startCurrentProviderScans() []tea.Cmd {
	if m.app == nil {
		return nil
	}
	plan, err := m.app.CurrentRefreshProviderScanPlan(m.ctx)
	if err != nil {
		return []tea.Cmd{setStatus(m, "✗ "+err.Error(), true)}
	}
	m.scanningProviders = plan.ProviderSet()
	m.refreshScanErrors = nil
	m.providerScanToolCounts = plan.CountsByProvider()
	m.providerScanLabels = plan.LabelsByProvider()
	m.providerScanToolDone = make(map[string]int, len(m.providerScanToolCounts))
	m.refreshToolDone = 0
	m.refreshToolTotal = plan.Total()
	if len(m.scanningProviders) == 0 {
		return nil
	}
	setActivityStatus(m, m.toolRefreshStatus(m.refreshToolDone, m.refreshToolTotal))
	m.scanGen++
	gen := m.scanGen
	cmds := []tea.Cmd{m.spinner.Tick}
	// Take over the shared stream only when no other operation owns it. Beginning a stream here would
	// bump progressGen and strand the active operation: an agents run's gen-guarded done message is
	// dropped on mismatch, leaving its section spinners running for the rest of the session.
	var ch chan progressUpdate
	var progressGen int
	if m.progressCh == nil {
		ch, progressGen = m.beginProgressStream()
		m.scanProgressCh = ch
		cmds = append(cmds, waitForProgress(ch, progressGen))
	}
	for _, prov := range plan.ProviderNames() {
		cmds = append(cmds, m.doScanProvider(prov, gen, ch, progressGen))
	}
	return cmds
}

func (m *Model) startPostLoadBackgroundTasks() []tea.Cmd {
	cmds := m.startCurrentProviderScans()
	if len(m.scanningProviders) == 0 && anyMissingDescription(m.allTools) {
		cmds = append(cmds, m.startDescriptionRefresh())
	}
	m.finishSetupReloadIfIdle()
	return cmds
}

func (m *Model) setupReloadPending() bool {
	return m.loading ||
		m.dotsLoading ||
		m.dotsPreparing ||
		len(m.scanningProviders) > 0 ||
		m.providerSnapshotRefreshing ||
		m.discoveryRefreshing ||
		m.descRefreshing
}

// Names the work the overlay is waiting on, for the status line shown when it is dismissed before that work finishes.
func (m *Model) setupReloadPendingLabels() []string {
	var pending []string
	if m.loading {
		pending = append(pending, "tools")
	}
	if m.dotsLoading || m.dotsPreparing {
		pending = append(pending, "dots")
	}
	if names := app.RefreshProviderScanLabels(m.scanningProviders, m.providerScanLabels); len(names) > 0 {
		slices.Sort(names)
		pending = append(pending, names...)
	}
	if m.providerSnapshotRefreshing || m.discoveryRefreshing {
		pending = append(pending, "local tools")
	}
	if m.descRefreshing {
		pending = append(pending, "descriptions")
	}
	return pending
}

func (m *Model) finishSetupReloadIfIdle() {
	if m.setupReloading && !m.setupReloadPending() {
		m.finishSetupReload()
	}
}

func (m *Model) finishSetupReload() {
	m.setupReloading = false
	m.setupReloadGen++
	m.progressText = ""
}

// A provider scan on an offline or unreachable host never completes, so without this timeout the overlay blocks the UI forever; the scans keep running and still apply their results.
func (m *Model) beginSetupReload() tea.Cmd {
	m.setupReloading = true
	m.setupReloadGen++
	gen := m.setupReloadGen
	return func() tea.Msg {
		time.Sleep(setupReloadTimeout)
		return setupReloadTimeoutMsg{gen: gen}
	}
}

func (m *Model) handleSetupReloadTimeoutMsg(msg setupReloadTimeoutMsg) []tea.Cmd {
	if msg.gen != m.setupReloadGen || !m.setupReloading {
		return nil
	}
	return m.dismissSetupReload()
}

// Names whatever is still running so the footer explains why the UI came back before the scans did.
func (m *Model) dismissSetupReload() []tea.Cmd {
	pending := m.setupReloadPendingLabels()
	m.finishSetupReload()
	if len(pending) == 0 {
		return nil
	}
	text := "background scans still running: " + strings.Join(pending, ", ") + " — results will land when ready"
	return []tea.Cmd{setStatusFor(m, text, false, statusDurationErr)}
}

func (m *Model) advanceSetupPastProviders(cmds *[]tea.Cmd) {
	// Saving derives the node/python effective managers.
	m.setupStep = 3
	m.startSettingsPriorityEdit()
}

func (m *Model) startSetupHostCreation(cmds *[]tea.Cmd) {
	name := strings.TrimSpace(m.defaultSetupHostName())
	if m.setupStep != 5 {
		m.setupHostReturnStep = m.setupStep
	}
	m.settingsInput.Blur()
	m.setupStep = 5
	if app.SetupHostAlreadyConfigured(m.hostInfo, name) {
		m.setupHostReturnStep = 0
		m.hostRequired = false
		return
	}
	m.beginLoading(loadingOwnerLocalOp)
	startOp(m, "Preparing this machine…")
	*cmds = append(*cmds, m.spinner.Tick, m.doSetupHost(name))
}

func (m *Model) defaultSetupHostName() string {
	return app.DefaultSetupHostName()
}

func (m Model) setupCopyHostNames() []string {
	return app.SetupCopyHostNames(m.hostInfo)
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

// Skips straight to group selection when agents are disabled for this host or no supported agent CLI is detected.
func (m *Model) startSetupAgentsStep(cmds *[]tea.Cmd) {
	if !m.agentsEnabled {
		m.startSetupGroupSelection(cmds)
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		m.startSetupGroupSelection(cmds)
		return
	}
	agents := app.InstalledAgents(home)
	if len(agents) == 0 {
		m.startSetupGroupSelection(cmds)
		return
	}
	m.loading = false
	m.setupStep = 4
	m.setupAgentsList = agents
	m.setupAgentsDiffLoading = true
	m.setupAgentsDiffLoaded = false
	*cmds = append(*cmds, m.spinner.Tick, m.doSetupAgentsDiff())
}

func (m *Model) initSetupGroupDraft() {
	m.setupGroupDraft = app.SetupHostGroupDraft(m.groupNames, m.hostInfo)
}

func (m Model) setupSelectedGroups() []string {
	return app.SetupSelectedHostGroups(m.groupNames, m.setupGroupDraft)
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
	m.setupImportNotices = nil
	m.setupCopyHostIdx = 0
	m.setupGroupIdx = 0
	m.setupGroupDraft = nil
	m.setupActivationIdx = 0
	m.hostRequired = false
	m.setupComplete = true
	reloadTimeout := m.beginSetupReload()
	m.progressText = "Loading tools…"
	m.beginLoading(loadingOwnerLocalOp)
	*cmds = append(*cmds, reloadTimeout, m.spinner.Tick, loadTools(m.app, m.ctx))
}

func (m *Model) markBootstrapComplete(cmds *[]tea.Cmd) {
	if m.app == nil {
		return
	}
	if err := m.app.MarkCurrentHostBootstrapCompleted(m.ctx); err != nil {
		*cmds = append(*cmds, setStatus(m, "✗ bootstrap marker: "+err.Error(), true))
	}
}

func setupGroupsSavedStatus(groups []string) string {
	if len(groups) == 0 {
		return "✓ no reusable groups selected"
	}
	return fmt.Sprintf("✓ %d reusable groups selected", len(groups))
}
