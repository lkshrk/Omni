package tui

import (
	"fmt"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
)

// Update handles messages and key events.
func (m Model) Update(msg tea.Msg) (next tea.Model, cmd tea.Cmd) {
	activityBefore := m.spinnerActivityState()
	defer func() {
		updated, ok := next.(Model)
		if !ok || cmd == nil || !updated.spinnerActivityState().startedSince(activityBefore) {
			return
		}
		cmd = runAfterRender(cmd)
	}()

	var cmds []tea.Cmd

	cmds = append(cmds, m.updateActiveFilePicker(msg)...)

	switch msg := msg.(type) {
	case runAfterRenderMsg:
		return m, msg.cmd

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.help.SetWidth(msg.Width)
		if m.showFilePicker {
			m.dotsFilePicker.SetWidth(filePickerContentWidth(m))
			m.dotsFilePicker.SetHeight(filePickerListHeight(m))
		}
		m.resizeAdminTerminalSession()

	case tea.BackgroundColorMsg:
		// Detect terminal light/dark theme for foreground tokens. Backgrounds
		// stay on the terminal default instead of being repainted by Omni.
		m.isDark = backgroundIsDark(msg.Color)
		m.applyTheme(m.isDark)

	case tea.FocusMsg:
		m.focused = true
		// Re-kick the spinner tick chain if any activity is still ongoing.
		if m.spinnerActivityActive() {
			cmds = append(cmds, m.spinner.Tick)
		}

	case tea.BlurMsg:
		m.focused = false

	case tea.MouseWheelMsg:
		if m.handleMouseWheelMsg(msg) {
			return m, tea.Batch(cmds...)
		}

	case tea.MouseClickMsg:
		if m.handleMouseClickMsg(msg, &cmds) {
			return m, tea.Batch(cmds...)
		}

	case tea.PasteMsg:
		if m.showFilePicker {
			return m, tea.Batch(cmds...)
		}
		if m.adminTerminal != nil && m.adminTerminal.running {
			m.writeAdminTerminalInput([]byte(msg.Content))
			return m, tea.Batch(cmds...)
		}
		// Route bracketed-paste content directly into whichever text input is live.
		switch m.mode {
		case viewSearch:
			m.filter.SetValue(m.filter.Value() + msg.Content)
			m.applyFilter()
		case viewCommand:
			m.commandInput.SetValue(m.commandInput.Value() + msg.Content)
			m.commandSuggestions = filterPalette(buildPalette(m), m.commandInput.Value())
		case viewFallbackEditor:
			m.settingsInput.SetValue(m.settingsInput.Value() + msg.Content)
			m.saveFallbackEditorInput()
		}

	case spinner.TickMsg:
		// Only reschedule while focused — avoids burning CPU in the background.
		if m.focused && m.spinnerActivityActive() {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}

	case toolsLoadedMsg:
		cmds = append(cmds, m.handleToolsLoadedMsg(msg)...)

	case setupImportDoneMsg:
		cmds = append(cmds, m.handleSetupImportDoneMsg(msg)...)

	case setupConfigImportDoneMsg:
		cmds = append(cmds, m.handleSetupConfigImportDoneMsg(msg)...)

	case setupProvidersDoneMsg:
		cmds = append(cmds, m.handleSetupProvidersDoneMsg(msg)...)

	case setupHostDoneMsg:
		cmds = append(cmds, m.handleSetupHostDoneMsg(msg)...)

	case setupHostCopyDoneMsg:
		cmds = append(cmds, m.handleSetupHostCopyDoneMsg(msg)...)

	case setupHostGroupsDoneMsg:
		cmds = append(cmds, m.handleSetupHostGroupsDoneMsg(msg)...)

	case setupBootstrapDoneMsg:
		cmds = append(cmds, m.handleSetupBootstrapDoneMsg(msg)...)

	case setupAgentsDiffMsg:
		cmds = append(cmds, m.handleSetupAgentsDiffMsg(msg)...)

	case setupAgentsImportDoneMsg:
		cmds = append(cmds, m.handleSetupAgentsImportDoneMsg(msg)...)

	case stowInstallDoneMsg:
		cmds = append(cmds, m.handleStowInstallDoneMsg(msg)...)

	case dotsIgnoredMsg:
		if !m.finishDotsOperation(msg.gen) {
			return m, tea.Batch(cmds...)
		}
		m.dotsIgnoreIdx = -1
		if msg.entries != nil {
			m.applyDotsSnapshot(msg.entries, msg.gitStatus, msg.dotMemberships)
		}
		if msg.err != nil {
			cmds = append(cmds, setStatus(&m, "✗ "+msg.err.Error(), true))
		} else {
			action := "ignored"
			if !msg.ignored {
				action = "included"
			}
			cmds = append(cmds, setStatus(&m, "✓ "+msg.pattern+" "+action+" for "+msg.name, false))
		}

	case providerScannedMsg:
		cmds = append(cmds, m.handleProviderScannedMsg(msg)...)

	case allProvidersDoneMsg:
		if cmd := m.handleAllProvidersDoneMsg(msg); cmd != nil {
			return m, cmd
		}

	case providerOutdatedCheckedMsg:
		cmds = append(cmds, m.handleProviderOutdatedCheckedMsg(msg)...)

	case outdatedProvidersDoneMsg:
		if cmd := m.handleOutdatedProvidersDoneMsg(msg); cmd != nil {
			return m, cmd
		}

	case discoveredRefreshedMsg:
		if cmd := m.handleDiscoveredRefreshedMsg(msg); cmd != nil {
			return m, cmd
		}

	case descRefreshDoneMsg:
		if cmd := m.handleDescRefreshDoneMsg(msg); cmd != nil {
			return m, cmd
		}

	case progressMsg:
		cmds = append(cmds, m.handleProgressMsg(msg)...)

	case progressStreamClosedMsg:
		m.handleProgressStreamClosedMsg(msg)

	case dotsProgressMsg:
		cmds = append(cmds, m.handleDotsProgressMsg(msg)...)

	case dotsProgressStreamClosedMsg:
		m.handleDotsProgressStreamClosedMsg(msg)

	case progressDoneMsg:
		cmds = append(cmds, m.handleProgressDoneMsg(msg)...)

	case opCompleteMsg:
		cmds = append(cmds, m.handleOpCompleteMsg(msg)...)

	case adminTerminalStartedMsg:
		cmds = append(cmds, m.handleAdminTerminalStartedMsg(msg)...)

	case adminTerminalDoneMsg:
		cmds = append(cmds, m.handleAdminTerminalDoneMsg(msg)...)

	case adminTerminalOutputMsg:
		cmds = append(cmds, m.handleAdminTerminalOutputMsg(msg)...)

	case createGroupDoneMsg:
		cmds = append(cmds, m.handleCreateGroupDoneMsg(msg)...)

	case hostCopiedMsg:
		cmds = append(cmds, m.handleHostCopiedMsg(msg)...)

	case groupChangedMsg:
		cmds = append(cmds, m.handleGroupChangedMsg(msg)...)

	case groupToolsChangedMsg:
		cmds = append(cmds, m.handleGroupToolsChangedMsg(msg)...)

	case groupDotsChangedMsg:
		cmds = append(cmds, m.handleGroupDotsChangedMsg(msg)...)

	case clearStatusMsg:
		if msg.gen == m.statusGen {
			m.statusMsg = ""
			m.statusIsErr = false
		}

	case confirmTimeoutMsg:
		cmds = append(cmds, m.handleConfirmTimeoutMsg(msg)...)

	case settingsSavedMsg:
		cmds = append(cmds, m.handleSettingsSavedMsg(msg)...)

	case doctorDoneMsg:
		cmds = append(cmds, m.handleDoctorDoneMsg(msg)...)

	case fixIgnoreDoneMsg:
		cmds = append(cmds, m.handleFixIgnoreDoneMsg(msg)...)

	case configOptimizeDoneMsg:
		cmds = append(cmds, m.handleConfigOptimizeDoneMsg(msg)...)

	case fixNvmDoneMsg:
		cmds = append(cmds, m.handleFixNvmDoneMsg(msg)...)

	case dotsServiceChangedMsg:
		cmds = append(cmds, m.handleDotsServiceChangedMsg(msg)...)

	case dotsServicesStatusMsg:
		m.handleDotsServicesStatusMsg(msg)

	case dotsHistoryLoadedMsg:
		m.handleDotsHistoryLoadedMsg(msg)

	case dangerOpDoneMsg:
		cmds = append(cmds, m.handleDangerOpDoneMsg(msg)...)

	case hostGroupChangedMsg:
		cmds = append(cmds, m.handleHostGroupChangedMsg(msg)...)

	case debouncedSearchMsg:
		cmds = append(cmds, m.handleDebouncedSearchMsg(msg)...)

	case searchResultsMsg:
		cmds = append(cmds, m.handleSearchResultsMsg(msg)...)

	case dotsLoadedMsg:
		cmds = append(cmds, m.handleDotsLoadedMsg(msg)...)

	case dotsPeekLoadedMsg:
		cmds = append(cmds, m.handleDotsPeekLoadedMsg(msg)...)

	case traceLogLoadedMsg:
		cmds = append(cmds, m.handleTraceLogLoadedMsg(msg)...)

	case dotsPreparedMsg:
		cmds = append(cmds, m.handleDotsPreparedMsg(msg)...)

	case dotsSyncedMsg:
		cmds = append(cmds, m.handleDotsSyncedMsg(msg)...)

	case dotsDiscoveredMsg:
		cmds = append(cmds, m.handleDotsDiscoveredMsg(msg)...)

	case dotsPulledMsg:
		cmds = append(cmds, m.handleDotsPulledMsg(msg)...)

	case dotsPushedMsg:
		cmds = append(cmds, m.handleDotsPushedMsg(msg)...)

	case dotsCommittedMsg:
		cmds = append(cmds, m.handleDotsCommittedMsg(msg)...)

	case dotsDeletedMsg:
		cmds = append(cmds, m.handleDotsDeletedMsg(msg)...)

	case dotsFixedMsg:
		cmds = append(cmds, m.handleDotsFixedMsg(msg)...)

	case dotsAddedMsg:
		cmds = append(cmds, m.handleDotsAddedMsg(msg)...)

	case dotsVariantChangedMsg:
		cmds = append(cmds, m.handleDotsVariantChangedMsg(msg)...)

	case claimDoneMsg:
		cmds = append(cmds, m.handleClaimDoneMsg(msg)...)

	case ignoreDoneMsg:
		cmds = append(cmds, m.handleIgnoreDoneMsg(msg)...)

	case migrateProviderDoneMsg:
		cmds = append(cmds, m.handleMigrateProviderDoneMsg(msg)...)

	case fallbackSavedMsg:
		cmds = append(cmds, m.handleFallbackSavedMsg(msg)...)

	case agentsSummaryLoadedMsg:
		if msg.err == nil {
			m.agentsSummary = msg.summary
		}

	case nvmManagedLoadedMsg:
		if msg.err == nil {
			m.nvmManaged = msg.nvmManaged
			m.applyFilter()
		}

	case agentsIgnoreReloadedMsg:
		if msg.err == nil {
			m.agentsIgnore = msg.ignore
			clampAgentsAllCursor(&m)
		}

	case agentsIgnoreToggledMsg:
		m.clearAgentsOp()
		if msg.err != nil {
			cmds = append(cmds, setStatus(&m, "✗ "+msg.err.Error(), true))
		} else {
			desc := "ignored"
			if !msg.nowIgnored {
				desc = "unignored"
			}
			cmds = append(cmds, setStatus(&m, "✓ "+msg.name+" "+desc, false), m.doReloadAgentsIgnore(), m.doLoadAgentsSummary())
		}
		return m, tea.Batch(cmds...)

	case skillsManifestLoadedMsg:
		m.skillsRunning = false
		m.skillAddRunning = false
		m.clearAgentsOpFor(agentsSectionSkills)
		// On error keep the seeded/previous rows visible instead of wiping
		// the table with nil; the error surfaces via skillsErr.
		if msg.err == nil {
			m.skillsRows = msg.rows
			m.skillsUnmanagedRows = msg.unmanaged
			m.skillsRowsKnown = true
		}
		m.skillsErr = msg.err
		m.skillsLoaded = true
		if m.skillAgentIdx > len(skillAgentIDs(m.skillsRows, m.enabledAgents)) {
			m.skillAgentIdx = 0
		}
		clampSkillsCursor(&m)
		clampAgentsAllCursor(&m)
		cmds = append(cmds, m.doLoadAgentsSummary())

	case skillsGroupsUpdatedMsg:
		if msg.err != nil {
			m.skillsErr = msg.err
		} else {
			m.skillsRows = msg.rows
			if m.skillAgentIdx > len(skillAgentIDs(m.skillsRows, m.enabledAgents)) {
				m.skillAgentIdx = 0
			}
			clampSkillsCursor(&m)
			clampAgentsAllCursor(&m)
		}

	case skillsRestoredMsg:
		if msg.err != nil {
			m.skillsRunning = false
			m.skillsErr = msg.err
		} else {
			r := msg.res
			m.skillsResult = &r
			m.skillsLoaded = false
			cmds = append(cmds, setStatus(&m, app.RestoreSkillsSummaryText(r), false), m.loadSkillsManifestCmd(), m.doLoadAgentsSummary())
		}
		return m, tea.Batch(cmds...)

	case skillsImportedMsg:
		if msg.err != nil {
			m.skillsRunning = false
			m.skillsErr = msg.err
		} else {
			d := msg.diff
			m.skillsImport = &d
			m.skillsLoaded = false
			cmds = append(cmds, setStatus(&m, app.ImportDiffSummaryText(d), false), m.loadSkillsManifestCmd())
		}
		return m, tea.Batch(cmds...)

	case skillsUpdatedMsg:
		if msg.err != nil {
			m.skillsRunning = false
			m.clearAgentsOp()
			m.skillsErr = msg.err
		} else {
			m.skillsLoaded = false
			cmds = append(cmds, setStatus(&m, "✓ skills updated", false), m.loadSkillsManifestCmd())
		}
		return m, tea.Batch(cmds...)

	case skillsFoundMsg:
		m.skillAddRunning = false
		m.searching = false
		if msg.err != nil {
			m.skillsErr = msg.err
			m.skillsSearchActive = false
			m.filter.SetValue("")
			m.filter.Blur()
			clampSkillsCursor(&m)
			clampAgentsAllCursor(&m)
		} else {
			m.skillFindResults = msg.results
			m.skillFindCursor = 0
			clampSkillsCursor(&m)
			clampAgentsAllCursor(&m)
			cmds = append(cmds, setStatus(&m, fmt.Sprintf("found %d", len(msg.results)), false))
		}

	case skillAddedMsg:
		m.searching = false
		m.skillsSearchActive = false
		m.skillFindResults = nil
		m.filter.SetValue("")
		m.filter.Blur()
		clampSkillsCursor(&m)
		clampAgentsAllCursor(&m)
		if msg.err != nil {
			m.skillAddRunning = false
			m.clearAgentsOp()
			m.skillsErr = msg.err
		} else {
			m.skillsLoaded = false
			cmds = append(cmds, m.loadSkillsManifestCmd(), m.doLoadAgentsSummary())
		}
		return m, tea.Batch(cmds...)

	case skillRemovedMsg:
		if msg.err != nil {
			m.skillsRunning = false
			m.clearAgentsOp()
			cmds = append(cmds, setStatus(&m, "✗ "+msg.err.Error(), true))
		} else {
			m.skillsLoaded = false
			cmds = append(cmds, m.loadSkillsManifestCmd(), m.doLoadAgentsSummary())
		}
		return m, tea.Batch(cmds...)

	case agentsToggledMsg:
		if msg.err != nil {
			m.skillsErr = msg.err
		} else {
			m.agentsEnabled = msg.enabled
			m.skillsErr = nil
			if m.agentsEnabled {
				m.skillsLoaded = false
				cmds = append(cmds, m.loadSkillsManifestCmd())
			}
			cmds = append(cmds, m.doLoadAgentsSummary())
		}
		return m, tea.Batch(cmds...)

	case skillsFeatureToggledMsg:
		if msg.err != nil {
			m.skillsErr = msg.err
		} else {
			m.skillsEnabled = msg.enabled
			m.skillsErr = nil
			if msg.enabled && m.agentsEnabled {
				m.skillsLoaded = false
				cmds = append(cmds, m.loadSkillsManifestCmd())
			}
			cmds = append(cmds, m.doLoadAgentsSummary())
		}
		return m, tea.Batch(cmds...)

	case mcpFeatureToggledMsg:
		if msg.err != nil {
			m.mcpErr = msg.err
		} else {
			m.mcpEnabled = msg.enabled
			m.mcpErr = nil
			if msg.enabled && m.agentsEnabled {
				m.mcpLoaded = false
				m.mcpRunning = true
				cmds = append(cmds, m.spinner.Tick, m.doLoadMcpRows())
			}
			cmds = append(cmds, m.doLoadAgentsSummary())
		}
		return m, tea.Batch(cmds...)

	case pluginsFeatureToggledMsg:
		if msg.err != nil {
			m.pluginErr = msg.err
		} else {
			m.pluginsEnabled = msg.enabled
			m.pluginErr = nil
			if msg.enabled && m.agentsEnabled {
				m.pluginLoaded = false
				m.pluginRunning = true
				cmds = append(cmds, m.spinner.Tick, m.doLoadPluginRows())
				m.marketplaceLoaded = false
				m.marketplaceRunning = true
				cmds = append(cmds, m.spinner.Tick, m.doLoadMarketplaceRows())
			}
			cmds = append(cmds, m.doLoadAgentsSummary())
		}
		return m, tea.Batch(cmds...)

	case agentsUseSavedMsg:
		if msg.err != nil {
			cmds = append(cmds, setStatus(&m, "✗ "+msg.err.Error(), true))
		} else {
			m.settings.AgentsUse = msg.ids
			cmds = append(cmds, setStatus(&m, "✓ agents saved", false))
		}
		return m, tea.Batch(cmds...)

	case skillAgentsSavedMsg:
		if msg.err != nil {
			m.skillsErr = msg.err
		} else {
			m.skillsRows = msg.rows
			if m.skillAgentIdx > len(skillAgentIDs(m.skillsRows, m.enabledAgents)) {
				m.skillAgentIdx = 0
			}
			clampSkillsCursor(&m)
			clampAgentsAllCursor(&m)
			cmds = append(cmds, setStatus(&m, "✓ agents updated", false))
		}
		return m, tea.Batch(cmds...)

	case mcpRowsMsg:
		m.mcpRunning = false
		m.clearAgentsOpFor(agentsSectionMcp)
		if msg.err == nil {
			m.mcpRows = msg.rows
			m.mcpUnmanaged = msg.unmanaged
			m.mcpRowsKnown = true
		}
		m.mcpErr = msg.err
		clampMcpCursor(&m)
		clampAgentsAllCursor(&m)

	case mcpRestoreDoneMsg:
		if msg.err != nil {
			m.mcpRunning = false
			m.mcpErr = msg.err
		} else {
			m.mcpErr = nil
			cmds = append(cmds, m.doLoadMcpRows())
		}
		return m, tea.Batch(cmds...)

	case mcpRemoveDoneMsg:
		if msg.err != nil {
			m.mcpRunning = false
			m.clearAgentsOp()
			m.mcpErr = msg.err
		} else {
			m.mcpErr = nil
			cmds = append(cmds, m.doLoadMcpRows())
		}
		return m, tea.Batch(cmds...)

	case mcpImportAdoptDoneMsg:
		if msg.err != nil {
			m.mcpRunning = false
			m.clearAgentsOp()
			m.mcpErr = msg.err
			cmds = append(cmds, setStatus(&m, "✗ "+msg.err.Error(), true))
		} else {
			m.mcpErr = nil
			cmds = append(cmds, m.doLoadMcpRows())
		}
		return m, tea.Batch(cmds...)

	case mcpAgentsSavedMsg:
		if msg.err != nil {
			m.mcpRunning = false
			m.clearAgentsOp()
			m.mcpErr = msg.err
		} else {
			m.mcpErr = nil
			cmds = append(cmds, m.doLoadMcpRows())
		}
		return m, tea.Batch(cmds...)

	case mcpGroupsSavedMsg:
		if msg.err != nil {
			cmds = append(cmds, setStatus(&m, "✗ "+msg.err.Error(), true))
		} else {
			cmds = append(cmds, m.doLoadMcpRows())
		}
		return m, tea.Batch(cmds...)

	case pluginRowsMsg:
		m.pluginRunning = false
		m.clearAgentsOpFor(agentsSectionPlugins)
		if msg.err == nil {
			m.pluginRows = msg.rows
			m.pluginUnmanaged = msg.unmanaged
			m.pluginRowsKnown = true
		}
		m.pluginErr = msg.err
		clampPluginCursor(&m)
		clampAgentsAllCursor(&m)

	case pluginRestoreDoneMsg:
		// On success the running flag (and any row-op spinner) stays set until
		// the reloaded rows land (pluginRowsMsg) — clearing it here would show
		// the stale pre-op row without its spinner for the whole reload,
		// which reads as a failed operation.
		if msg.err != nil {
			m.pluginRunning = false
			m.pluginErr = msg.err
		} else {
			m.pluginErr = nil
			cmds = append(cmds, m.doLoadPluginRows(), m.doLoadMarketplaceRows())
		}
		return m, tea.Batch(cmds...)

	case pluginRemoveDoneMsg:
		if msg.err != nil {
			m.pluginRunning = false
			m.clearAgentsOp()
			m.pluginErr = msg.err
			// The manifest delete may have succeeded even when an adapter
			// errored — reload so the removed row doesn't linger as stale.
			cmds = append(cmds, m.doLoadPluginRows())
		} else {
			m.pluginErr = nil
			cmds = append(cmds, m.doLoadPluginRows())
		}
		if msg.warning != "" {
			cmds = append(cmds, setStatus(&m, "⚠ "+msg.warning, true))
		}
		return m, tea.Batch(cmds...)

	case pluginImportAdoptDoneMsg:
		if msg.err != nil {
			m.pluginRunning = false
			m.clearAgentsOp()
			m.pluginErr = msg.err
			cmds = append(cmds, setStatus(&m, "✗ "+msg.err.Error(), true))
		} else {
			m.pluginErr = nil
			cmds = append(cmds, m.doLoadPluginRows())
			if msg.reloadMarketplaces {
				cmds = append(cmds, m.doLoadMarketplaceRows())
			}
		}
		return m, tea.Batch(cmds...)

	case pluginNeedsMarketplaceMsg:
		// Loading stays true (set by runAgentsClaimGroupPickerAction) until the
		// user answers the offer, so the spinner keeps showing on the row.
		cmds = append(cmds, m.armPluginMarketplaceOffer(msg))
		return m, tea.Batch(cmds...)

	case pluginAgentsSavedMsg:
		if msg.err != nil {
			m.pluginRunning = false
			m.clearAgentsOp()
			m.pluginErr = msg.err
		} else {
			m.pluginErr = nil
			// Installing a plugin also installs its marketplace when the
			// adapter didn't have it yet (App.SetPluginAgents →
			// ensureMarketplace), so the marketplace section must reload too.
			cmds = append(cmds, m.doLoadPluginRows(), m.doLoadMarketplaceRows())
		}
		return m, tea.Batch(cmds...)

	case pluginGroupsSavedMsg:
		if msg.err != nil {
			cmds = append(cmds, setStatus(&m, "✗ "+msg.err.Error(), true))
		} else {
			cmds = append(cmds, m.doLoadPluginRows())
		}
		return m, tea.Batch(cmds...)

	case pluginUpdateDoneMsg:
		if msg.err != nil {
			m.pluginRunning = false
			m.clearAgentsOp()
			m.pluginErr = msg.err
		} else {
			m.pluginErr = nil
			cmds = append(cmds, m.doLoadPluginRows())
		}
		return m, tea.Batch(cmds...)

	case marketplaceRowsMsg:
		m.marketplaceRunning = false
		m.clearAgentsOpFor(agentsSectionMarketplaces)
		if msg.err == nil {
			m.marketplaceRows = msg.rows
			m.marketplaceUnmanaged = msg.unmanaged
			m.marketplaceRowsKnown = true
		}
		m.marketplaceErr = msg.err
		clampMarketplaceCursor(&m)
		clampAgentsAllCursor(&m)

	case marketplaceRemoveDoneMsg:
		if msg.err != nil {
			m.marketplaceRunning = false
			m.clearAgentsOp()
			m.marketplaceErr = msg.err
		} else {
			m.marketplaceErr = nil
			cmds = append(cmds, m.doLoadMarketplaceRows())
		}
		return m, tea.Batch(cmds...)

	case marketplaceImportAdoptDoneMsg:
		if msg.err != nil {
			m.marketplaceRunning = false
			m.clearAgentsOp()
			m.marketplaceErr = msg.err
			cmds = append(cmds, setStatus(&m, "✗ "+msg.err.Error(), true))
		} else {
			m.marketplaceErr = nil
			cmds = append(cmds, m.doLoadMarketplaceRows())
		}
		return m, tea.Batch(cmds...)

	case marketplaceGroupsSavedMsg:
		if msg.err != nil {
			cmds = append(cmds, setStatus(&m, "✗ "+msg.err.Error(), true))
		} else {
			cmds = append(cmds, m.doLoadMarketplaceRows())
		}
		return m, tea.Batch(cmds...)

	case agentsProgressDoneMsg:
		if msg.gen != m.progressGen {
			return m, tea.Batch(cmds...)
		}
		m.progressGen++
		m.progressText = ""
		m.progressCh = nil
		// On success each section's running flag stays set until its reloaded
		// rows land (rows msg handlers clear it) so completed rows keep their
		// spinner instead of flashing the stale pre-op state.
		if msg.skills {
			m.skillsErr = msg.skillsErr
			if msg.skillsErr == nil {
				m.skillsLoaded = false
				cmds = append(cmds, m.loadSkillsManifestCmd())
			} else {
				m.skillsRunning = false
			}
		}
		if msg.mcp {
			m.mcpErr = msg.mcpErr
			if msg.mcpErr == nil {
				cmds = append(cmds, m.doLoadMcpRows())
			} else {
				m.mcpRunning = false
			}
		}
		if msg.plugin {
			m.pluginErr = msg.pluginErr
			if msg.pluginErr == nil {
				cmds = append(cmds, m.doLoadPluginRows())
			} else {
				m.pluginRunning = false
			}
		}
		if msg.marketplace {
			m.marketplaceErr = msg.marketplaceErr
			if msg.marketplaceErr == nil {
				cmds = append(cmds, m.doLoadMarketplaceRows())
			} else {
				m.marketplaceRunning = false
			}
		}
		// A plugin update also refreshes marketplaces internally (see
		// App.UpdatePlugins), so reload marketplace rows on that path too
		// even though msg.marketplace is false — otherwise UpdatedAt stays
		// stale in the TUI's cached rows until the next manual refresh.
		if msg.plugin && !msg.marketplace && msg.pluginErr == nil && m.marketplacesSectionEnabled() {
			cmds = append(cmds, m.doLoadMarketplaceRows())
		}
		if err := firstAgentsProgressError(msg); err != nil {
			cmds = append(cmds, setStatus(&m, "✗ "+err.Error(), true))
		}
		cmds = append(cmds, m.doLoadAgentsSummary())
		if m.dashboardReconcileCurrent == dashboardReconcilePlanSyncAgents {
			m.continueDashboardReconcile(dashboardReconcilePlanSyncAgents, firstAgentsProgressError(msg), &cmds)
		}
		return m, tea.Batch(cmds...)

	case mcpAddDoneMsg:
		if msg.err != nil {
			m.mcpRunning = false
			if m.mcpFormOpen {
				m.mcpFormErr = msg.err
			} else {
				cmds = append(cmds, setStatus(&m, "✗ "+msg.err.Error(), true))
			}
		} else {
			m.mcpFormOpen = false
			m.mcpFormErr = nil
			m.resetMcpForm()
			cmds = append(cmds, m.doLoadMcpRows())
		}
		return m, tea.Batch(cmds...)

	case pluginAddDoneMsg:
		if msg.err != nil {
			m.pluginRunning = false
			if m.pluginFormOpen {
				m.pluginFormErr = msg.err
			} else {
				cmds = append(cmds, setStatus(&m, "✗ "+msg.err.Error(), true))
			}
		} else {
			m.pluginFormOpen = false
			m.pluginFormErr = nil
			m.resetPluginForm()
			cmds = append(cmds, m.doLoadPluginRows())
		}
		return m, tea.Batch(cmds...)

	case tea.KeyPressMsg:
		return m.handleKeyPressMsg(msg, cmds)
	}

	return m, tea.Batch(cmds...)
}

func firstAgentsProgressError(msg agentsProgressDoneMsg) error {
	for _, err := range []error{msg.skillsErr, msg.mcpErr, msg.pluginErr, msg.marketplaceErr} {
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *Model) handleMouseClickMsg(msg tea.MouseClickMsg, cmds *[]tea.Cmd) bool {
	mouse := msg.Mouse()
	if mouse.Button != tea.MouseLeft {
		return false
	}
	if m.handleToolFilterClick(mouse.X, mouse.Y) {
		return true
	}
	if m.handleAgentsFilterClick(mouse.X, mouse.Y) {
		return true
	}
	if !m.mainTabsClickable() {
		return false
	}
	if target, ok := mainTabAtPosition(*m, mouse.X, mouse.Y); ok {
		return m.switchMainTab(target, cmds)
	}
	return false
}

func (m *Model) handleToolFilterClick(x, y int) bool {
	if !m.toolFiltersClickable() {
		return false
	}
	zone, ok := matchHitZone(toolFilterHitZones, *m, x, y)
	if !ok {
		return false
	}
	switch zone.kind {
	case toolFilterProvider:
		m.providerTabIdx = zone.index
	case toolFilterGroup:
		m.groupTabIdx = zone.index
		m.setGroupFilterFromIdx(buildAllGroupNames(visibleGroupNames(*m)))
	}
	m.applyFilter()
	m.cursor = 0
	m.clearListConfirmation()
	return true
}

func (m *Model) handleAgentsFilterClick(x, y int) bool {
	if m.mode != viewSkills {
		return false
	}
	zone, ok := matchHitZone(agentsFilterHitZones, *m, x, y)
	if !ok {
		return false
	}
	switch zone.kind {
	case agentsFilterType:
		m.setAgentsChip(zone.index)
	case agentsFilterAgent:
		m.skillAgentIdx = zone.index
		clampSkillsCursor(m)
	}
	return true
}

func (m Model) toolFiltersClickable() bool {
	if m.hostRequired || m.showFilePicker || m.stowInstallPrompt || m.help.ShowAll {
		return false
	}
	return m.mode == viewList || m.mode == viewSearch
}

func (m Model) mainTabsClickable() bool {
	if m.hostRequired || m.showFilePicker || m.stowInstallPrompt || m.dashboardReconcilePlanOpen || m.help.ShowAll || m.traceLog != nil || m.traceLogLoading {
		return false
	}
	if !isMainTabMode(m.mode) {
		return false
	}
	if m.hostRenameMode || m.groupCreating || m.groupRenameMode || m.groupDeleteConfirm {
		return false
	}
	if m.hostEditMode != 0 || m.editingPriority || m.editingServiceDuration || m.dangerConfirmRow >= 0 {
		return false
	}
	return true
}

func (m *Model) handleMouseWheelMsg(msg tea.MouseWheelMsg) bool {
	if m.showFilePicker || m.help.ShowAll {
		return true
	}
	delta := 0
	switch msg.Button {
	case tea.MouseWheelDown:
		delta = 1
	case tea.MouseWheelUp:
		delta = -1
	default:
		return false
	}
	if m.dotsPeek != nil {
		m.scrollDotsPeekBy(delta)
		return true
	}
	if (m.mode == viewSettings || m.mode == viewList || m.mode == viewSearch) && m.traceLog != nil {
		m.scrollTraceLogBy(delta)
		return true
	}
	m.scrollBy(delta)
	return true
}

func (m *Model) scrollBy(delta int) {
	if delta == 0 {
		return
	}
	switch m.mode {
	case viewList, viewSearch:
		m.cursor = cursorMove(m.cursor, delta, len(m.visibleTools), true)
		m.clearListConfirmation()
	case viewCommand:
		m.commandCursor = clampRange(m.commandCursor+delta, -1, max(len(m.commandSuggestions)-1, -1))
	case viewDots:
		m.scrollDotsBy(delta)
	case viewStatus:
		m.scrollStatusBy(delta)
	case viewSettings:
		m.scrollSettingsBy(delta)
	case viewGroups:
		m.scrollGroupsBy(delta)
	case viewGroupPicker, viewGroupMembership:
		m.pickerCursor = clampIndex(m.pickerCursor+delta, len(m.pickerGroups))
	case viewGroupTools:
		m.groupToolsEditor.cursor = clampIndex(m.groupToolsEditor.cursor+delta, len(groupToolRows(*m)))
	case viewGroupDots:
		m.groupDotsEditor.cursor = clampIndex(m.groupDotsEditor.cursor+delta, len(groupDotRows(*m)))
	case viewIgnoreScope, viewProviderScope:
		m.scopeCursor = clampIndex(m.scopeCursor+delta, len(m.scopeOptions))
	case viewSetup:
		m.scrollSetupBy(delta)
	case viewSkills:
		m.scrollSkillsTabBy(delta)
	}
}

// scrollSkillsTabBy routes a mouse wheel scroll on the agents tab to the
// same cursor-move logic the active chip's key handler uses, so wheel and
// keyboard navigation stay in lockstep.
func (m *Model) scrollSkillsTabBy(delta int) {
	switch m.skillTypeIdx {
	case agentsChipAll:
		agentsAllCursorMove(m, delta)
	case agentsChipSkills:
		m.agentsChipMoveRow(agentsSectionSkills, delta)
	case agentsChipMcp:
		m.agentsChipMoveRow(agentsSectionMcp, delta)
	case agentsChipPlugin:
		m.agentsChipMoveRow(agentsSectionPlugins, delta)
	}
}

func (m *Model) scrollDotsBy(delta int) {
	visible := dotsVisibleRows(*m)
	m.dotsCursor = cursorMove(m.dotsCursor, delta, len(visible), true)
	m.syncDotsExpandedName(visible)
	m.clearDotsConfirmState()
}

func (m *Model) scrollDotsPeekBy(delta int) {
	if m.dotsPeek == nil || delta == 0 {
		return
	}
	m.dotsPeek.scroll = clampRange(m.dotsPeek.scroll+delta, 0, dotsPeekMaxScroll(*m))
}

func (m *Model) scrollSettingsBy(delta int) {
	if m.editingPriority {
		m.priorityCursor = clampIndex(m.priorityCursor+delta, len(m.priorityDraft))
		return
	}
	if m.editingServiceDuration {
		choices := settingsDurationChoicesForRow(m.serviceDurationRow, m.currentSettingsDurationValue(m.serviceDurationRow))
		m.serviceDurationIdx = clampIndex(m.serviceDurationIdx+delta, len(choices))
		return
	}
	if maxScroll := m.settingsDetailScrollMax(); maxScroll > 0 && m.settingsCursor == settingsRowDoctor {
		m.settingsDetailScroll = clampRange(m.settingsDetailScroll+delta, 0, maxScroll)
		return
	}
	m.setSettingsCursor(m.settingsCursor + delta)
}

func (m *Model) scrollGroupsBy(delta int) {
	switch {
	case m.hostEditMode == 1:
		m.hostGroupIdx = clampIndex(m.hostGroupIdx+delta, len(m.hostGroupPicker))
	case m.groupDeleteConfirm:
		m.groupDeleteChoice = clampIndex(m.groupDeleteChoice+delta, 2)
	default:
		if delta > 0 {
			m.moveGroupsCursorDown()
		} else {
			m.moveGroupsCursorUp()
		}
	}
}

func (m *Model) scrollSetupBy(delta int) {
	switch m.setupStep {
	case 1, 2:
		m.setupProviderIdx = clampIndex(m.setupProviderIdx+delta, len(m.setupProviders))
	case 8:
		m.setupCopyHostIdx = clampIndex(m.setupCopyHostIdx+delta, len(m.setupCopyHostNames()))
	case 9:
		m.setupGroupIdx = clampIndex(m.setupGroupIdx+delta, len(m.groupNames))
	case 10:
		m.setupActivationIdx = clampIndex(m.setupActivationIdx+delta, len(setupActivationOptions))
	}
}

func clampIndex(idx, n int) int {
	if n <= 0 {
		return 0
	}
	return clampRange(idx, 0, n-1)
}

func clampRange(idx, low, high int) int {
	if high < low {
		return low
	}
	if idx < low {
		return low
	}
	if idx > high {
		return high
	}
	return idx
}
