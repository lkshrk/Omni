package tui

import (
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
)

// Update handles messages and key events.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	cmds = append(cmds, m.updateActiveFilePicker(msg)...)

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.help.SetWidth(msg.Width)
		if m.showFilePicker {
			m.dotsFilePicker.SetWidth(filePickerContentWidth(m))
			m.dotsFilePicker.SetHeight(filePickerListHeight(m))
		}

	case tea.BackgroundColorMsg:
		// Detect terminal light/dark theme once at startup.
		// RGBA() returns 0-65535; check perceived luminance.
		r, g, b, _ := msg.RGBA()
		lum := 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)
		m.isDark = lum < 32768
		m.applyTheme(m.isDark)

	case tea.FocusMsg:
		m.focused = true
		// Re-kick the spinner tick chain if any activity is still ongoing.
		if m.loading || len(m.scanningProviders) > 0 || m.dotsLoading || m.searching || len(m.upgradingKeys) > 0 {
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
		// Route bracketed-paste content directly into whichever text input is live.
		switch m.mode {
		case viewSearch:
			m.filter.SetValue(m.filter.Value() + msg.Content)
			m.applyFilter()
		case viewCommand:
			m.commandInput.SetValue(m.commandInput.Value() + msg.Content)
			m.commandSuggestions = filterPalette(buildPalette(m), m.commandInput.Value())
		}

	case spinner.TickMsg:
		// Only reschedule while focused — avoids burning CPU in the background.
		if m.focused && (m.loading || len(m.scanningProviders) > 0 || m.dotsLoading || m.searching || len(m.upgradingKeys) > 0) {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}

	case toolsLoadedMsg:
		cmds = append(cmds, m.handleToolsLoadedMsg(msg)...)

	case setupImportDoneMsg:
		cmds = append(cmds, m.handleSetupImportDoneMsg(msg)...)

	case setupProvidersDoneMsg:
		cmds = append(cmds, m.handleSetupProvidersDoneMsg(msg)...)

	case setupNodeMgrDoneMsg:
		cmds = append(cmds, m.handleSetupNodeMgrDoneMsg(msg)...)

	case setupProfileDoneMsg:
		cmds = append(cmds, m.handleSetupProfileDoneMsg(msg)...)

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

	case discoveredRefreshedMsg:
		if cmd := m.handleDiscoveredRefreshedMsg(msg); cmd != nil {
			return m, cmd
		}

	case descRefreshDoneMsg:
		m.handleDescRefreshDoneMsg(msg)

	case progressMsg:
		cmds = append(cmds, m.handleProgressMsg(msg)...)

	case progressStreamClosedMsg:
		m.handleProgressStreamClosedMsg(msg)

	case progressDoneMsg:
		cmds = append(cmds, m.handleProgressDoneMsg(msg)...)

	case opCompleteMsg:
		cmds = append(cmds, m.handleOpCompleteMsg(msg)...)

	case createGroupDoneMsg:
		cmds = append(cmds, m.handleCreateGroupDoneMsg(msg)...)

	case profileActivatedMsg:
		cmds = append(cmds, m.handleProfileActivatedMsg(msg)...)

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

	case dangerOpDoneMsg:
		cmds = append(cmds, m.handleDangerOpDoneMsg(msg)...)

	case profileGroupChangedMsg:
		cmds = append(cmds, m.handleProfileGroupChangedMsg(msg)...)

	case profileCreatedMsg:
		cmds = append(cmds, m.handleProfileCreatedMsg(msg)...)

	case debouncedSearchMsg:
		cmds = append(cmds, m.handleDebouncedSearchMsg(msg)...)

	case searchResultsMsg:
		cmds = append(cmds, m.handleSearchResultsMsg(msg)...)

	case dotsLoadedMsg:
		cmds = append(cmds, m.handleDotsLoadedMsg(msg)...)

	case dotsSyncedMsg:
		cmds = append(cmds, m.handleDotsSyncedMsg(msg)...)

	case dotsDiscoveredMsg:
		cmds = append(cmds, m.handleDotsDiscoveredMsg(msg)...)

	case dotsPulledMsg:
		cmds = append(cmds, m.handleDotsPulledMsg(msg)...)

	case dotsPushedMsg:
		cmds = append(cmds, m.handleDotsPushedMsg(msg)...)

	case dotsDeletedMsg:
		cmds = append(cmds, m.handleDotsDeletedMsg(msg)...)

	case dotsFixedMsg:
		cmds = append(cmds, m.handleDotsFixedMsg(msg)...)

	case dotsAddedMsg:
		cmds = append(cmds, m.handleDotsAddedMsg(msg)...)

	case claimDoneMsg:
		cmds = append(cmds, m.handleClaimDoneMsg(msg)...)

	case ignoreDoneMsg:
		cmds = append(cmds, m.handleIgnoreDoneMsg(msg)...)

	case migrateProviderDoneMsg:
		cmds = append(cmds, m.handleMigrateProviderDoneMsg(msg)...)

	case tea.KeyPressMsg:
		return m.handleKeyPressMsg(msg, cmds)
	}

	return m, tea.Batch(cmds...)
}

func (m *Model) handleMouseClickMsg(msg tea.MouseClickMsg, cmds *[]tea.Cmd) bool {
	mouse := msg.Mouse()
	if mouse.Button != tea.MouseLeft {
		return false
	}
	if m.handleToolFilterClick(mouse.X, mouse.Y) {
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
	for _, zone := range toolFilterHitZones(*m) {
		if y != zone.y || x < zone.start || x >= zone.end {
			continue
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
	return false
}

func (m Model) toolFiltersClickable() bool {
	if m.profileRequired || m.showFilePicker || m.stowInstallPrompt || m.help.ShowAll {
		return false
	}
	return m.mode == viewList || m.mode == viewSearch
}

func (m Model) mainTabsClickable() bool {
	if m.profileRequired || m.showFilePicker || m.stowInstallPrompt || m.help.ShowAll {
		return false
	}
	if m.mode != viewList && m.mode != viewDots && m.mode != viewProfiles && m.mode != viewSettings {
		return false
	}
	if m.profileCreating || m.profileRenameMode || m.groupCreating || m.groupRenameMode || m.groupDeleteConfirm {
		return false
	}
	if m.profileEditMode != 0 || m.editingPriority || m.dangerConfirmRow >= 0 {
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
	m.scrollBy(delta)
	return true
}

func (m *Model) scrollBy(delta int) {
	if delta == 0 {
		return
	}
	switch m.mode {
	case viewList, viewSearch:
		m.cursor = clampIndex(m.cursor+delta, len(m.visibleTools))
		m.clearListConfirmation()
	case viewCommand:
		m.commandCursor = clampRange(m.commandCursor+delta, -1, max(len(m.commandSuggestions)-1, -1))
	case viewDots:
		m.scrollDotsBy(delta)
	case viewSettings:
		m.scrollSettingsBy(delta)
	case viewProfiles:
		m.scrollProfilesBy(delta)
	case viewGroupPicker, viewGroupMembership:
		m.pickerCursor = clampIndex(m.pickerCursor+delta, len(m.pickerGroups))
	case viewProfileGroupTools:
		m.groupToolsEditor.cursor = clampIndex(m.groupToolsEditor.cursor+delta, len(profileGroupToolRows(*m)))
	case viewProfileGroupDots:
		m.groupDotsEditor.cursor = clampIndex(m.groupDotsEditor.cursor+delta, len(profileGroupDotRows(*m)))
	case viewIgnoreScope, viewProviderScope:
		m.scopeCursor = clampIndex(m.scopeCursor+delta, len(m.scopeOptions))
	case viewSetup:
		m.scrollSetupBy(delta)
	}
}

func (m *Model) scrollDotsBy(delta int) {
	visible := dotsVisibleRows(*m)
	m.dotsCursor = clampIndex(m.dotsCursor+delta, len(visible))
	m.syncDotsExpandedName(visible)
	m.clearDotsConfirmState()
}

func (m *Model) scrollSettingsBy(delta int) {
	if m.editingPriority {
		m.priorityCursor = clampIndex(m.priorityCursor+delta, len(m.priorityDraft))
		return
	}
	m.settingsCursor = clampIndex(m.settingsCursor+delta, numSettingRows)
}

func (m *Model) scrollProfilesBy(delta int) {
	switch {
	case m.profileEditMode == 1:
		m.profileGroupIdx = clampIndex(m.profileGroupIdx+delta, len(m.profileGroupPicker))
	case m.profileEditMode == 2:
		m.profileHostIdx = clampIndex(m.profileHostIdx+delta, len(m.profileHostPicker))
	case m.groupDeleteConfirm:
		m.groupDeleteChoice = clampIndex(m.groupDeleteChoice+delta, 2)
	default:
		if delta > 0 {
			m.moveProfilesCursorDown()
		} else {
			m.moveProfilesCursorUp()
		}
	}
}

func (m *Model) scrollSetupBy(delta int) {
	switch m.setupStep {
	case 1, 2:
		m.setupProviderIdx = clampIndex(m.setupProviderIdx+delta, len(m.setupProviders))
	case 3:
		m.setupNodeMgrIdx = clampIndex(m.setupNodeMgrIdx+delta, len(nodeMgrChoices))
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
