package tui

import (
	"errors"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/config"
)

func (m *Model) handleKeyPressMsg(msg tea.KeyPressMsg, cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	if handled, subCmds := m.handleFilePickerKeyMsg(msg); handled {
		cmds = append(cmds, subCmds...)
		return *m, tea.Batch(cmds...)
	}

	if m.adminTerminal != nil && m.adminTerminal.running {
		cmds = append(cmds, m.handleAdminTerminalKeyMsg(msg)...)
		return *m, tea.Batch(cmds...)
	}

	if m.dashboardReconcilePlanOpen {
		cmds = append(cmds, m.handleDashboardReconcilePlanKeyMsg(msg)...)
		return *m, tea.Batch(cmds...)
	}

	if key.Matches(msg, m.keys.Help) {
		m.help.ShowAll = !m.help.ShowAll
		return *m, nil
	}
	if m.help.ShowAll && key.Matches(msg, m.keys.Back) {
		m.help.ShowAll = false
		return *m, nil
	}
	if m.help.ShowAll {
		return *m, nil
	}

	if isCtrlC(msg) {
		if cmd := m.cancelActiveAction(); cmd != nil {
			cmds = append(cmds, cmd)
			return *m, tea.Batch(cmds...)
		}
	}

	if key.Matches(msg, m.keys.Quit) {
		if m.confirmQuit {
			m.shutdown()
			return *m, tea.Quit
		}
		m.confirmQuit = true
		m.quitConfirmKey = quitKeyLabel(msg)
		clearStatus(m)
		cmds = append(cmds, m.armConfirmationTimeout())
		return *m, tea.Batch(cmds...)
	}
	if m.confirmQuit {
		m.clearActiveConfirmation()
		m.cancelConfirmationTimeout()
		clearStatus(m)
	}

	if m.stowInstallPrompt {
		cmds = append(cmds, m.handleStowInstallKeyMsg(msg)...)
		return *m, tea.Batch(cmds...)
	}

	if m.mode == viewSetup {
		cmd, quit := m.handleSetupKeyMsg(msg)
		if quit {
			return *m, cmd
		}
		return *m, cmd
	}

	if m.mode == viewDots && (m.dotsPeek != nil || m.dotsPeekLoading) {
		cmds = append(cmds, m.handleDotsPeekKeyMsg(msg)...)
		return *m, tea.Batch(cmds...)
	}

	if m.mode == viewSettings && (m.traceLog != nil || m.traceLogLoading) {
		cmds = append(cmds, m.handleTraceLogKeyMsg(msg)...)
		return *m, tea.Batch(cmds...)
	}

	if m.handleTabKeyMsg(msg, &cmds) {
		return *m, tea.Batch(cmds...)
	}
	if m.handlePaletteOpenKeyMsg(msg, &cmds) {
		return *m, tea.Batch(cmds...)
	}

	if m.loading {
		return *m, tea.Batch(cmds...)
	}

	wasHidden := m.cursorHidden
	// Tab-global keys (bulk sync/update, refresh, commit, quit, help) act on
	// the whole tab, not the selected row — they must not turn the post-tab-
	// switch "no selection" state into a selected first row.
	if !isTabGlobalKey(msg, m.keys, m.mode) {
		m.cursorHidden = false
	}

	// First navigation keypress after a tab switch reveals the cursor without moving it.
	if wasHidden && isMainTabMode(m.mode) && isNavigationKey(msg, m.keys) {
		return *m, tea.Batch(cmds...)
	}

	// Global actions available from any main tab.
	if isMainTabMode(m.mode) && key.Matches(msg, m.keys.DotCommit) {
		m.startDashboardDotsCommit(&cmds)
		return *m, tea.Batch(cmds...)
	}

	switch m.mode {
	case viewSearch:
		cmds = append(cmds, m.handleSearchKeyMsg(msg)...)
	case viewGroups:
		cmds = append(cmds, m.handleGroupsKeyMsg(msg)...)
	case viewGroupPicker:
		cmds = append(cmds, m.handleGroupPickerKeyMsg(msg)...)
	case viewGroupMembership:
		cmds = append(cmds, m.handleGroupMembershipKeyMsg(msg)...)
	case viewGroupTools:
		cmds = append(cmds, m.handleGroupToolsKeyMsg(msg)...)
	case viewGroupDots:
		cmds = append(cmds, m.handleGroupDotsKeyMsg(msg)...)
	case viewIgnoreScope, viewProviderScope:
		cmds = append(cmds, m.handleScopePickerKeyMsg(msg)...)
	case viewFallbackEditor:
		cmds = append(cmds, m.handleFallbackEditorKeyMsg(msg)...)
	case viewSettings:
		cmds = append(cmds, m.handleSettingsKeyMsg(msg)...)
	case viewStatus:
		cmds = append(cmds, m.handleStatusKeyMsg(msg)...)
	case viewDots:
		if handled, subCmds := m.handleDotsSubmodeKeyMsg(msg); handled {
			cmds = append(cmds, subCmds...)
			return *m, tea.Batch(cmds...)
		}
		visible := dotsVisibleRows(*m)
		switch {
		case m.handleDotsNavigationKeyMsg(msg, visible, &cmds):
		default:
			cmds = append(cmds, m.handleDotsActionKeyMsg(msg, visible)...)
		}
	case viewCommand:
		cmds = append(cmds, m.handleCommandKeyMsg(msg)...)
	case viewAdminTerminal:
		cmds = append(cmds, m.handleAdminTerminalKeyMsg(msg)...)
	case viewSkills:
		if !m.agentsEnabled {
			break
		}
		if m.mcpFormOpen {
			cmds = append(cmds, m.handleMcpFormKeyMsg(msg)...)
			break
		}
		if m.pluginFormOpen {
			cmds = append(cmds, m.handlePluginFormKeyMsg(msg)...)
			break
		}
		if m.mcpAgentsPicker {
			cmds = append(cmds, m.handleMcpAgentsPickerKeyMsg(msg)...)
			break
		}
		if m.pluginAgentsPicker {
			cmds = append(cmds, m.handlePluginAgentsPickerKeyMsg(msg)...)
			break
		}
		if m.skillAgentsPicker {
			cmds = append(cmds, m.handleSkillAgentsPickerKeyMsg(msg)...)
			break
		}
		// Search mode intercepts keys while the input is focused.
		if m.skillsSearchActive && m.filter.Focused() {
			cmds = append(cmds, m.handleSkillsSearchKeyMsg(msg)...)
			break
		}
		if handled, subCmds := m.handleAgentsGlobalActionKeyMsg(msg); handled {
			cmds = append(cmds, subCmds...)
			break
		}
		switch m.skillTypeIdx {
		case agentsChipAll:
			cmds = append(cmds, m.handleAgentsAllKeyMsg(msg)...)
		case agentsChipMcp:
			cmds = append(cmds, m.handleMcpKeyMsg(msg)...)
		case agentsChipPlugin:
			cmds = append(cmds, m.handlePluginKeyMsg(msg)...)
		case agentsChipMarketplace:
			cmds = append(cmds, m.handleMarketplaceKeyMsg(msg)...)
		default:
			cmds = append(cmds, m.handleSkillsKeyMsg(msg)...)
		}
	default:
		switch {
		case m.handleListNavigationKeyMsg(msg):
		default:
			cmds = append(cmds, m.handleListActionKeyMsg(msg)...)
		}
	}

	return *m, tea.Batch(cmds...)
}

func quitKeyLabel(msg tea.KeyPressMsg) string {
	if isCtrlC(msg) {
		return "ctrl+c"
	}
	return "q"
}

func isCtrlC(msg tea.KeyPressMsg) bool {
	return msg.Mod == tea.ModCtrl && msg.Code == 'c'
}

// handleSkillsKeyMsg dispatches keys for the skills chip (skillTypeIdx ==
// agentsChipSkills): chip switching, agent filter cycling, cursor movement
// across local + find-result rows, and r/i/u/g/a/c actions. 'c' claims an
// orphan row; 'i' is the unrelated global import-from-CLI bulk action.
func (m *Model) handleSkillsKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd
	switch msg.String() {
	case "esc":
		if m.skillsSearchActive {
			m.skillsSearchActive = false
			m.searching = false
			m.filter.SetValue("")
			m.filter.Blur()
			m.skillFindResults = nil
			m.skillsCursor = 0
		}
	case "left", "h":
		m.agentsChipMove(-1)
	case "right", "l":
		m.agentsChipMove(1)
	case "[":
		m.agentsChipMove(-1)
	case "]":
		m.agentsChipMove(1)
	case "{":
		m.agentFilterMove(-1)
	case "}":
		m.agentFilterMove(1)
	case "up", "k":
		m.agentsChipMoveRow(agentsSectionSkills, -1)
	case "down", "j":
		m.agentsChipMoveRow(agentsSectionSkills, 1)
	case "enter":
		if m.skillAddRunning {
			break
		}
		_, findStart, _ := skillsVisibleRows(*m)
		switch {
		case findStart >= 0 && m.skillsCursor >= findStart:
			findIdx := m.skillsCursor - findStart
			if findIdx < len(m.skillFindResults) {
				src := m.skillFindResults[findIdx].Source
				m.skillAddRunning = true
				m.skillsErr = nil
				cmds = append(cmds, m.spinner.Tick, m.doAddSkillPackage(src))
			}
		}
	case "c":
		if m.skillAddRunning {
			break
		}
		_, _, unmanagedStart := skillsVisibleRows(*m)
		if unmanagedStart >= 0 && m.skillsCursor >= unmanagedStart {
			m.openAgentsClaimGroupPicker(agentsAllRow{feature: agentsSectionSkills, localIdx: m.skillsCursor})
		}
	case "/":
		m.skillsSearchActive = true
		m.filter.SetValue("")
		m.filter.Focus()
		m.skillsCursor = 0
		m.skillFindResults = nil
		m.skillsErr = nil
		cmds = append(cmds, textinput.Blink)
	case "g":
		visible, findStart, unmanagedStart := skillsVisibleRows(*m)
		if len(visible) > 0 && m.skillsCursor < skillsManagedRowsEnd(findStart, unmanagedStart, len(visible)) {
			m.openSkillGroupMembershipPicker()
		}
	case "a":
		visible, findStart, unmanagedStart := skillsVisibleRows(*m)
		if len(visible) > 0 && m.skillsCursor < skillsManagedRowsEnd(findStart, unmanagedStart, len(visible)) {
			cmds = append(cmds, m.openSkillAgentsPicker(visible[m.skillsCursor]))
		}
	case "r":
		m.skillsRunning = true
		m.skillsErr = nil
		m.skillsResult = nil
		cmds = append(cmds, m.spinner.Tick, m.doRestoreSkills())
	case "i":
		m.skillsRunning = true
		m.skillsErr = nil
		m.skillsImport = nil
		cmds = append(cmds, m.spinner.Tick, m.doImportSkills())
	case "u":
		m.skillsRunning = true
		m.skillsErr = nil
		cmds = append(cmds, m.spinner.Tick, m.doUpdateSkills())
	}
	return cmds
}

func (m *Model) handleTabKeyMsg(msg tea.KeyPressMsg, cmds *[]tea.Cmd) bool {
	if !key.Matches(msg, m.keys.Tab) || m.mode == viewSearch || m.mode == viewCommand || m.mode == viewGroupPicker || m.mode == viewGroupMembership || m.mode == viewGroupTools || m.mode == viewGroupDots || m.mode == viewIgnoreScope || m.mode == viewProviderScope || m.mode == viewAdminTerminal || m.hostRequired || m.mcpFormOpen || m.mcpAgentsPicker || m.skillAgentsPicker || m.pluginAgentsPicker || m.pluginFormOpen {
		return false
	}
	tabs := mainTabs()
	idx := -1
	for i, tab := range tabs {
		if tab.mode == m.mode {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false
	}
	delta := 1
	if msg.Mod.Contains(tea.ModShift) {
		delta = -1
	}
	target := tabs[(idx+delta+len(tabs))%len(tabs)].mode
	return m.switchMainTab(target, cmds)
}

func (m *Model) switchMainTab(target viewMode, cmds *[]tea.Cmd) bool {
	if m.hostRequired {
		return false
	}
	if !isMainTabMode(target) {
		return false
	}
	if m.mode == viewGroups && target != viewGroups {
		m.assignmentSection = 0
	}
	m.cancelConfirmationForGlobalNavigation()
	m.cursorHidden = true
	m.mode = target
	if target == viewDots && m.dotsConfigured() && !m.dotsLoaded && !m.dotsLoading && !m.dotsPreparing {
		m.beginDotsOperation("Loading dots…")
		*cmds = append(*cmds, m.spinner.Tick, m.doLoadDots())
	}
	if target == viewSkills {
		m.skillTypeIdx = agentsChipAll
		clampAgentsAllCursor(m)
	}
	if target == viewSkills && m.skillsSectionEnabled() && !m.skillsLoaded {
		m.skillsLoaded = true
		*cmds = append(*cmds, m.loadSkillsManifestCmd())
	}
	if target == viewSkills && m.mcpSectionEnabled() && !m.mcpLoaded {
		m.mcpLoaded = true
		m.mcpRunning = true
		*cmds = append(*cmds, m.spinner.Tick, m.doLoadMcpRows())
	}
	if target == viewSkills && m.pluginsSectionEnabled() && !m.pluginLoaded {
		m.pluginLoaded = true
		m.pluginRunning = true
		*cmds = append(*cmds, m.spinner.Tick, m.doLoadPluginRows())
	}
	if target == viewSkills && m.marketplacesSectionEnabled() && !m.marketplaceLoaded {
		m.marketplaceLoaded = true
		m.marketplaceRunning = true
		*cmds = append(*cmds, m.spinner.Tick, m.doLoadMarketplaceRows())
	}
	if target == viewStatus && m.shouldAutoRunStatusDoctor() {
		m.startDoctorRun("Running doctor…")
		*cmds = append(*cmds, m.spinner.Tick, m.doRunDoctor())
	}
	return true
}

func isMainTabMode(mode viewMode) bool {
	switch mode {
	case viewStatus, viewList, viewDots, viewGroups, viewSettings, viewSkills:
		return true
	default:
		return false
	}
}

func (m *Model) handlePaletteOpenKeyMsg(msg tea.KeyPressMsg, cmds *[]tea.Cmd) bool {
	if !key.Matches(msg, m.keys.Palette) {
		return false
	}
	if m.loading {
		return false
	}
	if m.mode != viewList && m.mode != viewSettings && m.mode != viewStatus && m.mode != viewGroups && m.mode != viewDots && m.mode != viewSkills {
		return false
	}
	m.cancelConfirmationForGlobalNavigation()
	m.mode = viewCommand
	m.commandInput.SetValue("")
	m.commandInput.Focus()
	m.commandSuggestions = filterPalette(buildPalette(*m), "")
	m.commandCursor = -1
	*cmds = append(*cmds, textinput.Blink)
	return true
}

func (m *Model) cancelConfirmationForGlobalNavigation() {
	if !m.hasActiveConfirmation() {
		return
	}
	m.clearActiveConfirmation()
	m.cancelConfirmationTimeout()
}

func isNavigationKey(msg tea.KeyPressMsg, k KeyMap) bool {
	return key.Matches(msg, k.Up, k.Down, k.Top, k.Bottom,
		k.HalfPageUp, k.HalfPageDown, k.PageUp, k.PageDown)
}

// isTabGlobalKey reports keys whose action is row-agnostic (bulk operations,
// refresh, dots commit, quit, help). The agents tab's capital U/S/R bulk keys
// share the same bindings as the tools tab's, so they are covered too.
// DotAdd ("a") is global only on the dots tab — the same key opens a
// row-scoped picker on the agents tab, which must keep revealing the cursor.
func isTabGlobalKey(msg tea.KeyPressMsg, k KeyMap, mode viewMode) bool {
	if key.Matches(msg, k.UpgradeAll, k.SyncAll, k.Refresh, k.DotRefresh, k.DotCommit,
		k.Reconcile, k.DotUseRepoAll, k.DotUseLocalAll, k.Quit, k.Help) {
		return true
	}
	return mode == viewDots && key.Matches(msg, k.DotAdd)
}

func (m *Model) handleSkillAgentsPickerKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd
	s := msg.String()
	up := s == "k" || key.Matches(msg, m.keys.Up)
	down := s == "j" || key.Matches(msg, m.keys.Down)
	switch {
	case up:
		if m.skillAgentsCursor > 0 {
			m.skillAgentsCursor--
		}
	case down:
		if m.skillAgentsCursor < len(m.skillAgentsRows)-1 {
			m.skillAgentsCursor++
		}
	case key.Matches(msg, m.keys.Toggle):
		if m.skillAgentsCursor >= 0 && m.skillAgentsCursor < len(m.skillAgentsRows) {
			m.skillAgentsRows[m.skillAgentsCursor].Targeted = !m.skillAgentsRows[m.skillAgentsCursor].Targeted
		}
	case key.Matches(msg, m.keys.Confirm):
		ids := make([]string, 0, len(m.skillAgentsRows))
		for _, r := range m.skillAgentsRows {
			if r.Targeted {
				ids = append(ids, r.ID)
			}
		}
		m.skillAgentsPicker = false
		cmds = append(cmds, m.doSetSkillAgents(m.skillAgentsSource, ids))
	case key.Matches(msg, m.keys.Back):
		m.skillAgentsPicker = false
	}
	return cmds
}

func (m *Model) handleMcpAgentsPickerKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd
	s := msg.String()
	up := s == "k" || key.Matches(msg, m.keys.Up)
	down := s == "j" || key.Matches(msg, m.keys.Down)
	switch {
	case up:
		if m.skillAgentsCursor > 0 {
			m.skillAgentsCursor--
		}
	case down:
		if m.skillAgentsCursor < len(m.skillAgentsRows)-1 {
			m.skillAgentsCursor++
		}
	case key.Matches(msg, m.keys.Toggle):
		if m.skillAgentsCursor >= 0 && m.skillAgentsCursor < len(m.skillAgentsRows) {
			m.skillAgentsRows[m.skillAgentsCursor].Targeted = !m.skillAgentsRows[m.skillAgentsCursor].Targeted
		}
	case key.Matches(msg, m.keys.Confirm):
		ids := make([]string, 0, len(m.skillAgentsRows))
		for _, r := range m.skillAgentsRows {
			if r.Targeted {
				ids = append(ids, r.ID)
			}
		}
		row := m.mcpAgentsRow
		m.mcpAgentsPicker = false
		m.mcpRunning = true
		m.mcpErr = nil
		cmds = append(cmds, m.spinner.Tick, m.doSetMcpAgents(row, ids))
	case key.Matches(msg, m.keys.Back):
		m.mcpAgentsPicker = false
	}
	return cmds
}

// handleMcpKeyMsg dispatches keys for the mcp chip (skillTypeIdx == 1): tab
// switching, cursor movement across managed+unmanaged rows, and r/c/a/d/g
// actions. 'c' claims (imports) an orphan/unmanaged row into the manifest.
func (m *Model) handleMcpKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd
	s := msg.String()

	if m.mcpDeleteConfirm {
		switch {
		case key.Matches(msg, m.keys.Confirm) || key.Matches(msg, m.keys.Delete):
			m.cancelConfirmationTimeout()
			name := m.mcpDeleteName
			m.mcpDeleteConfirm = false
			m.mcpDeleteName = ""
			m.mcpRunning = true
			m.mcpErr = nil
			cmds = append(cmds, m.spinner.Tick, m.doRemoveMcp(name))
		case key.Matches(msg, m.keys.Back):
			m.cancelConfirmationTimeout()
			m.mcpDeleteConfirm = false
			m.mcpDeleteName = ""
		}
		return cmds
	}

	switch s {
	case "left", "h":
		m.agentsChipMove(-1)
	case "right", "l":
		m.agentsChipMove(1)
	case "[":
		m.agentsChipMove(-1)
	case "]":
		m.agentsChipMove(1)
	case "{":
		m.agentFilterMove(-1)
	case "}":
		m.agentFilterMove(1)
	case "up", "k":
		m.agentsChipMoveRow(agentsSectionMcp, -1)
	case "down", "j":
		m.agentsChipMoveRow(agentsSectionMcp, 1)
	case "r":
		m.mcpRunning = true
		m.mcpErr = nil
		cmds = append(cmds, m.spinner.Tick, m.doRestoreMcp())
	case "c":
		if _, agentID, ok := mcpHighlightedUnmanaged(*m); ok {
			m.openAgentsClaimGroupPicker(agentsAllRow{feature: agentsSectionMcp, localIdx: m.mcpCursor, agentID: agentID})
		}
	case "d":
		if m.mcpCursor < len(m.mcpRows) {
			m.mcpDeleteConfirm = true
			m.mcpDeleteName = m.mcpRows[m.mcpCursor].Name
			cmds = append(cmds, m.armConfirmationTimeout())
		}
	case "a":
		if m.mcpCursor < len(m.mcpRows) {
			cmds = append(cmds, m.openMcpAgentsPicker(m.mcpRows[m.mcpCursor]))
		}
	case "g":
		if m.mcpCursor < len(m.mcpRows) {
			cmds = append(cmds, setStatus(m, mcpGroupsStatusText(m.mcpRows[m.mcpCursor]), false))
		}
	case "n":
		m.mcpFormOpen = true
		m.mcpFormErr = nil
		m.resetMcpForm()
		m.focusMcpFormField()
		cmds = append(cmds, textinput.Blink)
	}
	return cmds
}

// handlePluginKeyMsg dispatches keys for the plugin chip (skillTypeIdx == 2):
// tab switching, cursor movement across managed+unmanaged rows, and
// r/c/a/d/g actions. Mirrors handleMcpKeyMsg exactly minus the "n" add-form
// case, which Task 7 adds once the plugin form fields exist.
func (m *Model) handlePluginKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd
	s := msg.String()

	if m.pluginDeleteConfirm {
		switch {
		case key.Matches(msg, m.keys.Confirm) || key.Matches(msg, m.keys.Delete):
			m.cancelConfirmationTimeout()
			name := m.pluginDeleteName
			m.pluginDeleteConfirm = false
			m.pluginDeleteName = ""
			m.pluginRunning = true
			m.pluginErr = nil
			cmds = append(cmds, m.spinner.Tick, m.doRemovePlugin(name))
		case key.Matches(msg, m.keys.Back):
			m.cancelConfirmationTimeout()
			m.pluginDeleteConfirm = false
			m.pluginDeleteName = ""
		}
		return cmds
	}

	switch s {
	case "left", "h":
		m.agentsChipMove(-1)
	case "right", "l":
		m.agentsChipMove(1)
	case "[":
		m.agentsChipMove(-1)
	case "]":
		m.agentsChipMove(1)
	case "{":
		m.agentFilterMove(-1)
	case "}":
		m.agentFilterMove(1)
	case "up", "k":
		m.agentsChipMoveRow(agentsSectionPlugins, -1)
	case "down", "j":
		m.agentsChipMoveRow(agentsSectionPlugins, 1)
	case "r":
		m.pluginRunning = true
		m.pluginErr = nil
		cmds = append(cmds, m.spinner.Tick, m.doRestorePlugin())
	case "c":
		if _, agentID, ok := pluginHighlightedUnmanaged(*m); ok {
			m.openAgentsClaimGroupPicker(agentsAllRow{feature: agentsSectionPlugins, localIdx: m.pluginCursor, agentID: agentID})
		}
	case "d":
		if m.pluginCursor < len(m.pluginRows) {
			m.pluginDeleteConfirm = true
			m.pluginDeleteName = m.pluginRows[m.pluginCursor].Name
			cmds = append(cmds, m.armConfirmationTimeout())
		}
	case "a":
		if m.pluginCursor < len(m.pluginRows) {
			cmds = append(cmds, m.openPluginAgentsPicker(m.pluginRows[m.pluginCursor]))
		}
	case "g":
		if m.pluginCursor < len(m.pluginRows) {
			cmds = append(cmds, setStatus(m, pluginGroupsStatusText(m.pluginRows[m.pluginCursor]), false))
		}
	case "n":
		m.pluginFormOpen = true
		m.pluginFormErr = nil
		m.resetPluginForm()
		m.focusPluginFormField()
		cmds = append(cmds, textinput.Blink)
	}
	return cmds
}

// handleMarketplaceKeyMsg dispatches keys for the marketplace chip
// (skillTypeIdx == agentsChipMarketplace): tab switching, cursor movement
// across managed+unmanaged rows, and c/g/d actions. Mirrors handlePluginKeyMsg
// minus the a/n cases (marketplaces have no per-agent install picker or
// add-form): 'c' opens the group-membership picker in claim mode like
// plugins/mcp/skills, and 'g' opens the group-membership picker for a
// managed row.
func (m *Model) handleMarketplaceKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd
	s := msg.String()

	if m.marketplaceDeleteConfirm {
		switch {
		case key.Matches(msg, m.keys.Confirm) || key.Matches(msg, m.keys.Delete):
			m.cancelConfirmationTimeout()
			name := m.marketplaceDeleteName
			m.marketplaceDeleteConfirm = false
			m.marketplaceDeleteName = ""
			m.marketplaceRunning = true
			m.marketplaceErr = nil
			cmds = append(cmds, m.spinner.Tick, m.doRemoveMarketplace(name))
		case key.Matches(msg, m.keys.Back):
			m.cancelConfirmationTimeout()
			m.marketplaceDeleteConfirm = false
			m.marketplaceDeleteName = ""
		}
		return cmds
	}

	switch s {
	case "left", "h":
		m.agentsChipMove(-1)
	case "right", "l":
		m.agentsChipMove(1)
	case "[":
		m.agentsChipMove(-1)
	case "]":
		m.agentsChipMove(1)
	case "{":
		m.agentFilterMove(-1)
	case "}":
		m.agentFilterMove(1)
	case "up", "k":
		m.agentsChipMoveRow(agentsSectionMarketplaces, -1)
	case "down", "j":
		m.agentsChipMoveRow(agentsSectionMarketplaces, 1)
	case "c":
		if _, agentID, ok := marketplaceHighlightedUnmanaged(*m); ok {
			m.openAgentsClaimGroupPicker(agentsAllRow{feature: agentsSectionMarketplaces, localIdx: m.marketplaceCursor, agentID: agentID})
		}
	case "g":
		if m.marketplaceCursor < len(m.marketplaceRows) {
			m.openMarketplaceGroupMembershipPicker()
		}
	case "d":
		if m.marketplaceCursor < len(m.marketplaceRows) {
			m.marketplaceDeleteConfirm = true
			m.marketplaceDeleteName = m.marketplaceRows[m.marketplaceCursor].Name
			cmds = append(cmds, m.armConfirmationTimeout())
		}
	}
	return cmds
}

func (m *Model) handlePluginAgentsPickerKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd
	s := msg.String()
	up := s == "k" || key.Matches(msg, m.keys.Up)
	down := s == "j" || key.Matches(msg, m.keys.Down)
	switch {
	case up:
		if m.skillAgentsCursor > 0 {
			m.skillAgentsCursor--
		}
	case down:
		if m.skillAgentsCursor < len(m.skillAgentsRows)-1 {
			m.skillAgentsCursor++
		}
	case key.Matches(msg, m.keys.Toggle):
		if m.skillAgentsCursor >= 0 && m.skillAgentsCursor < len(m.skillAgentsRows) {
			m.skillAgentsRows[m.skillAgentsCursor].Targeted = !m.skillAgentsRows[m.skillAgentsCursor].Targeted
		}
	case key.Matches(msg, m.keys.Confirm):
		ids := make([]string, 0, len(m.skillAgentsRows))
		for _, r := range m.skillAgentsRows {
			if r.Targeted {
				ids = append(ids, r.ID)
			}
		}
		row := m.pluginAgentsRow
		m.pluginAgentsPicker = false
		m.pluginRunning = true
		m.pluginErr = nil
		cmds = append(cmds, m.spinner.Tick, m.doSetPluginAgents(row, ids))
	case key.Matches(msg, m.keys.Back):
		m.pluginAgentsPicker = false
	}
	return cmds
}

func (m *Model) handleMcpFormKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd
	switch {
	case key.Matches(msg, m.keys.Back):
		m.mcpFormOpen = false
		m.mcpFormErr = nil
		m.resetMcpForm()
	case key.Matches(msg, m.keys.Tab):
		if msg.Mod.Contains(tea.ModShift) {
			m.mcpFormField = (m.mcpFormField + 4) % 5
		} else {
			m.mcpFormField = (m.mcpFormField + 1) % 5
		}
		m.focusMcpFormField()
	case msg.String() == "left" || msg.String() == "h":
		if m.mcpFormField == 1 && m.mcpFormTransport > 0 {
			m.mcpFormTransport--
		}
	case msg.String() == "right" || msg.String() == "l":
		if m.mcpFormField == 1 && m.mcpFormTransport < 2 {
			m.mcpFormTransport++
		}
	case key.Matches(msg, m.keys.Confirm):
		s, err := m.buildMcpServerFromForm()
		if err != nil {
			m.mcpFormErr = err
			return cmds
		}
		m.mcpFormErr = nil
		m.mcpRunning = true
		cmds = append(cmds, m.spinner.Tick, m.doAddMcp(s))
	default:
		var cmd tea.Cmd
		switch m.mcpFormField {
		case 0:
			m.mcpFormName, cmd = m.mcpFormName.Update(msg)
		case 2:
			if m.mcpFormTransport == 0 {
				m.mcpFormCommand, cmd = m.mcpFormCommand.Update(msg)
			} else {
				m.mcpFormURL, cmd = m.mcpFormURL.Update(msg)
			}
		case 3:
			m.mcpFormEnv, cmd = m.mcpFormEnv.Update(msg)
		case 4:
			m.mcpFormEnvLit, cmd = m.mcpFormEnvLit.Update(msg)
		}
		cmds = append(cmds, cmd)
	}
	return cmds
}

func (m *Model) handlePluginFormKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd
	switch {
	case key.Matches(msg, m.keys.Back):
		m.pluginFormOpen = false
		m.pluginFormErr = nil
		m.resetPluginForm()
	case key.Matches(msg, m.keys.Tab):
		if msg.Mod.Contains(tea.ModShift) {
			m.pluginFormField = (m.pluginFormField + 2) % 3
		} else {
			m.pluginFormField = (m.pluginFormField + 1) % 3
		}
		m.focusPluginFormField()
	case key.Matches(msg, m.keys.Confirm):
		p, err := m.buildPluginFromForm()
		if err != nil {
			m.pluginFormErr = err
			return cmds
		}
		m.pluginFormErr = nil
		m.pluginRunning = true
		cmds = append(cmds, m.spinner.Tick, m.doAddPlugin(p))
	default:
		var cmd tea.Cmd
		switch m.pluginFormField {
		case 0:
			m.pluginFormName, cmd = m.pluginFormName.Update(msg)
		case 1:
			m.pluginFormMarketplace, cmd = m.pluginFormMarketplace.Update(msg)
		case 2:
			m.pluginFormAgents, cmd = m.pluginFormAgents.Update(msg)
		}
		cmds = append(cmds, cmd)
	}
	return cmds
}

// buildMcpServerFromForm assembles a config.McpServer from the add-server
// form fields, validating the name and the transport-specific required field.
func (m *Model) buildMcpServerFromForm() (config.McpServer, error) {
	name := strings.TrimSpace(m.mcpFormName.Value())
	if name == "" {
		return config.McpServer{}, errors.New("name is required")
	}

	transports := []string{"stdio", "http", "sse"}
	transport := transports[m.mcpFormTransport]

	s := config.McpServer{
		Name:      name,
		Transport: transport,
	}

	switch transport {
	case "stdio":
		command := strings.TrimSpace(m.mcpFormCommand.Value())
		if command == "" {
			return config.McpServer{}, errors.New("command is required for stdio transport")
		}
		s.Command = command
	default:
		url := strings.TrimSpace(m.mcpFormURL.Value())
		if url == "" {
			return config.McpServer{}, errors.New("URL is required for " + transport + " transport")
		}
		s.URL = url
	}

	if env := strings.TrimSpace(m.mcpFormEnv.Value()); env != "" {
		for _, name := range strings.Split(env, ",") {
			name = strings.TrimSpace(name)
			if name != "" {
				s.Env = append(s.Env, name)
			}
		}
	}

	if envLit := strings.TrimSpace(m.mcpFormEnvLit.Value()); envLit != "" {
		lit := make(map[string]string)
		for _, pair := range strings.Split(envLit, ",") {
			pair = strings.TrimSpace(pair)
			if pair == "" {
				continue
			}
			envKey, envValue, ok := strings.Cut(pair, "=")
			if !ok {
				return config.McpServer{}, errors.New("env literal entries must be KEY=VALUE: " + pair)
			}
			envKey = strings.TrimSpace(envKey)
			if envKey == "" {
				return config.McpServer{}, errors.New("env literal entries must be KEY=VALUE: " + pair)
			}
			lit[envKey] = strings.TrimSpace(envValue)
		}
		if len(lit) > 0 {
			s.EnvLiteral = lit
		}
	}

	return s, nil
}

func (m *Model) handleSkillsSearchKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd
	switch {
	case key.Matches(msg, m.keys.Back):
		m.skillsSearchActive = false
		m.searching = false
		m.filter.SetValue("")
		m.filter.Blur()
		m.skillsCursor = 0
		m.skillFindResults = nil
	case key.Matches(msg, m.keys.Confirm):
		// Submit: blur input so row keys work; also trigger find for the query.
		m.filter.Blur()
		q := strings.TrimSpace(m.filter.Value())
		if q != "" && !looksLikeSkillSource(q) {
			m.skillAddRunning = true
			m.searching = true
			m.skillFindResults = nil
			m.skillsErr = nil
			cmds = append(cmds, m.spinner.Tick, m.doFindSkills(q))
		} else if q != "" {
			m.skillAddRunning = true
			m.skillFindResults = nil
			m.skillsErr = nil
			cmds = append(cmds, m.spinner.Tick, m.doAddSkillPackage(q))
		}
	default:
		prev := m.filter.Value()
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		cmds = append(cmds, cmd)
		if m.filter.Value() != prev {
			m.skillsCursor = 0
			m.skillFindResults = nil
		}
	}
	return cmds
}
