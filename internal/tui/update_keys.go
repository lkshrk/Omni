package tui

import (
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
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

	if m.hostCreateStep != 0 {
		cmds = append(cmds, m.handleHostCreateChoiceKeyMsg(msg)...)
		return *m, tea.Batch(cmds...)
	}

	if !m.focusedTextInputActive() && key.Matches(msg, m.keys.Help) {
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

	if key.Matches(msg, m.keys.Quit) && !m.focusedTextInputActive() {
		if m.confirmQuit && (m.quitConfirmKey == "" || m.quitConfirmKey == "q") {
			m.shutdown()
			return *m, tea.Quit
		}
		m.confirmQuit = true
		m.quitConfirmKey = "q"
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

	// The post-setup overlay owns the screen, so esc dismisses it instead of reaching the tab underneath.
	if m.setupReloading && key.Matches(msg, m.keys.Back) {
		cmds = append(cmds, m.dismissSetupReload()...)
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

	if (m.mode == viewSettings || m.mode == viewList || m.mode == viewSearch) && (m.traceLog != nil || m.traceLogLoading) {
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
	// Tab-global keys act on the whole tab, so they must not turn the post-tab-switch "no selection" state into a selected first row.
	if !isTabGlobalKey(msg, m.keys, m.mode) {
		m.cursorHidden = false
	}

	// First navigation keypress after a tab switch reveals the cursor without moving it.
	if wasHidden && isMainTabMode(m.mode) && isNavigationKey(msg, m.keys) {
		return *m, tea.Batch(cmds...)
	}

	if isMainTabMode(m.mode) && !m.focusedTextInputActive() && key.Matches(msg, m.keys.DotCommit) {
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
		if handled, subCmds := m.handleAgentsGlobalActionKeyMsg(msg); handled {
			cmds = append(cmds, subCmds...)
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

func isCtrlC(msg tea.KeyPressMsg) bool {
	return msg.Mod&^lockMods == tea.ModCtrl && msg.Code == 'c'
}

func (m *Model) focusedTextInputActive() bool {
	if m.showFilePicker {
		return m.dotsFilePicker.input.Focused()
	}
	switch m.mode {
	case viewSearch:
		return m.filter.Focused()
	case viewDots:
		return m.dotsSearchActive && m.filter.Focused()
	case viewSkills:
		return false
	case viewCommand:
		return m.commandInput.Focused()
	case viewFallbackEditor:
		return m.settingsInput.Focused()
	case viewGroups:
		editing := m.hostRenameMode || m.groupRenameMode || m.groupCreating || (m.hostEditMode == 1 && m.pickerCreatingGroup)
		return editing && m.settingsInput.Focused()
	case viewGroupPicker, viewGroupMembership:
		return m.pickerCreatingGroup && m.settingsInput.Focused()
	case viewGroupTools:
		return m.groupToolsEditor.searchActive && m.settingsInput.Focused()
	case viewGroupDots:
		return m.groupDotsEditor.searchActive && m.settingsInput.Focused()
	default:
		return false
	}
}

func (m *Model) handleTabKeyMsg(msg tea.KeyPressMsg, cmds *[]tea.Cmd) bool {
	if m.focusedTextInputActive() || !key.Matches(msg, m.keys.Tab) || m.mode == viewSearch || m.mode == viewCommand || m.mode == viewGroupPicker || m.mode == viewGroupMembership || m.mode == viewGroupTools || m.mode == viewGroupDots || m.mode == viewIgnoreScope || m.mode == viewProviderScope || m.mode == viewAdminTerminal || m.hostRequired {
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
	if m.focusedTextInputActive() || !key.Matches(msg, m.keys.Palette) {
		return false
	}
	if m.loading {
		return false
	}
	if m.mode != viewList && m.mode != viewSettings && m.mode != viewStatus && m.mode != viewGroups && m.mode != viewDots && m.mode != viewSkills {
		return false
	}
	m.cancelConfirmationForGlobalNavigation()
	m.commandOrigin = m.mode
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

// DotAdd ("a") is global only on the dots tab — the same key opens a row-scoped picker on the agents tab, which must keep revealing the cursor.
func isTabGlobalKey(msg tea.KeyPressMsg, k KeyMap, mode viewMode) bool {
	if key.Matches(msg, k.UpgradeAll, k.SyncAll, k.Refresh, k.DotRefresh, k.DotCommit,
		k.Reconcile, k.DotUseRepoAll, k.DotUseLocalAll, k.Quit, k.Help) {
		return true
	}
	return mode == viewDots && key.Matches(msg, k.DotAdd)
}
