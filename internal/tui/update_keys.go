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
	m.cursorHidden = false

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

func (m *Model) handleTabKeyMsg(msg tea.KeyPressMsg, cmds *[]tea.Cmd) bool {
	if !key.Matches(msg, m.keys.Tab) || m.mode == viewSearch || m.mode == viewCommand || m.mode == viewGroupPicker || m.mode == viewGroupMembership || m.mode == viewGroupTools || m.mode == viewGroupDots || m.mode == viewIgnoreScope || m.mode == viewProviderScope || m.mode == viewAdminTerminal || m.hostRequired {
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
	if target == viewDots && m.settings.DotsRepo != "" && !m.dotsLoaded && !m.dotsLoading && !m.dotsPreparing {
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
	case viewStatus, viewList, viewDots, viewGroups, viewSettings:
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
	if m.mode != viewList && m.mode != viewSettings && m.mode != viewStatus && m.mode != viewGroups && m.mode != viewDots {
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
