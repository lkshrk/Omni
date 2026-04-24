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

	if key.Matches(msg, m.keys.Help) {
		m.help.ShowAll = !m.help.ShowAll
		return *m, nil
	}
	if m.help.ShowAll && key.Matches(msg, m.keys.Back) {
		m.help.ShowAll = false
		return *m, nil
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

	switch m.mode {
	case viewSearch:
		cmds = append(cmds, m.handleSearchKeyMsg(msg)...)
	case viewProfiles:
		cmds = append(cmds, m.handleProfilesKeyMsg(msg)...)
	case viewGroupPicker:
		cmds = append(cmds, m.handleGroupPickerKeyMsg(msg)...)
	case viewGroupMembership:
		cmds = append(cmds, m.handleGroupMembershipKeyMsg(msg)...)
	case viewProfileGroupTools:
		cmds = append(cmds, m.handleProfileGroupToolsKeyMsg(msg)...)
	case viewProfileGroupDots:
		cmds = append(cmds, m.handleProfileGroupDotsKeyMsg(msg)...)
	case viewIgnoreScope, viewProviderScope:
		cmds = append(cmds, m.handleScopePickerKeyMsg(msg)...)
	case viewSettings:
		cmds = append(cmds, m.handleSettingsKeyMsg(msg)...)
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
	if msg.Mod == tea.ModCtrl && msg.Code == 'c' {
		return "ctrl+c"
	}
	return "q"
}

func (m *Model) handleTabKeyMsg(msg tea.KeyPressMsg, cmds *[]tea.Cmd) bool {
	if !key.Matches(msg, m.keys.Tab) || m.mode == viewSearch || m.mode == viewCommand || m.mode == viewGroupPicker || m.mode == viewGroupMembership || m.mode == viewProfileGroupTools || m.mode == viewProfileGroupDots || m.mode == viewIgnoreScope || m.mode == viewProviderScope || m.profileRequired {
		return false
	}
	target := viewList
	if msg.Mod.Contains(tea.ModShift) {
		switch m.mode {
		case viewList:
			target = viewSettings
		case viewDots:
			target = viewList
		case viewProfiles:
			target = viewDots
		case viewSettings:
			target = viewProfiles
		}
	} else {
		switch m.mode {
		case viewList:
			target = viewDots
		case viewDots:
			target = viewProfiles
		case viewProfiles:
			target = viewSettings
		case viewSettings:
			target = viewList
		}
	}
	return m.switchMainTab(target, cmds)
}

func (m *Model) switchMainTab(target viewMode, cmds *[]tea.Cmd) bool {
	if m.profileRequired {
		return false
	}
	switch target {
	case viewList, viewDots, viewProfiles, viewSettings:
	default:
		return false
	}
	if m.mode == viewProfiles && target != viewProfiles {
		m.profileSection = 0
	}
	m.cancelConfirmationForGlobalNavigation()
	m.mode = target
	if target == viewDots && m.settings.DotsRepo != "" && !m.dotsLoaded && !m.dotsLoading {
		m.beginDotsOperation("Loading dots…")
		*cmds = append(*cmds, m.spinner.Tick, m.doLoadDots())
	}
	return true
}

func (m *Model) handlePaletteOpenKeyMsg(msg tea.KeyPressMsg, cmds *[]tea.Cmd) bool {
	if !key.Matches(msg, m.keys.Palette) {
		return false
	}
	if m.loading {
		return false
	}
	if m.mode != viewList && m.mode != viewSettings && m.mode != viewProfiles && m.mode != viewDots {
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
