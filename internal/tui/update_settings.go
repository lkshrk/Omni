package tui

import (
	"context"
	"slices"
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
)

func (m *Model) handleSettingsKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd

	if handled, subCmds := m.handleSettingsSubmodeKeyMsg(msg); handled {
		return subCmds
	}

	switch {
	case key.Matches(msg, m.keys.Up):
		m.setSettingsCursor((m.settingsCursor - 1 + numSettingRows) % numSettingRows)
	case key.Matches(msg, m.keys.Down):
		m.setSettingsCursor((m.settingsCursor + 1) % numSettingRows)
	case key.Matches(msg, m.keys.Top):
		if m.settingsDetailScroll > 0 && m.settingsCursor == settingsRowDoctor {
			m.settingsDetailScroll = 0
		} else {
			m.setSettingsCursor(0)
		}
	case key.Matches(msg, m.keys.Bottom):
		if maxScroll := m.settingsDetailScrollMax(); maxScroll > 0 && m.settingsCursor == settingsRowDoctor {
			m.settingsDetailScroll = maxScroll
		} else {
			m.setSettingsCursor(numSettingRows - 1)
		}
	case key.Matches(msg, m.keys.HalfPageDown):
		m.scrollSettingsBy(max(listAvailableHeight(*m)/2, 1))
	case key.Matches(msg, m.keys.HalfPageUp):
		m.scrollSettingsBy(-max(listAvailableHeight(*m)/2, 1))
	case key.Matches(msg, m.keys.PageDown):
		m.scrollSettingsBy(max(listAvailableHeight(*m), 1))
	case key.Matches(msg, m.keys.PageUp):
		m.scrollSettingsBy(-max(listAvailableHeight(*m), 1))
	case key.Matches(msg, m.keys.Toggle):
		m.handleSettingsRowAction(&cmds)
	case key.Matches(msg, m.keys.Confirm):
		m.handleSettingsConfirmAction(&cmds)
	case key.Matches(msg, m.keys.Back):
		m.mode = viewList
	}

	return cmds
}

func (m *Model) setSettingsCursor(row int) {
	next := clampIndex(row, numSettingRows)
	if next != m.settingsCursor {
		m.settingsDetailScroll = 0
	}
	m.settingsCursor = next
}

func (m *Model) handleSettingsConfirmAction(cmds *[]tea.Cmd) {
	switch m.settingsCursor {
	case settingsRowProviderPriority, settingsRowDotsRepo, settingsRowDotsSync, settingsRowDotsReminderInterval, settingsRowDotsWatchDebounce, settingsRowDoctor, settingsRowBootstrap, settingsRowResetSettings, settingsRowResetCache:
		m.handleSettingsEditAction(cmds)
	case settingsRowTraceLog:
		*cmds = append(*cmds, m.openTraceLog())
	}
}

func (m *Model) handleSettingsSubmodeKeyMsg(msg tea.KeyPressMsg) (bool, []tea.Cmd) {
	if m.editingPriority {
		return true, m.handleSettingsPriorityKeyMsg(msg)
	}
	if m.editingServiceDuration {
		return true, m.handleSettingsServiceDurationKeyMsg(msg)
	}
	if m.dangerConfirmRow >= 0 {
		return true, m.handleSettingsDangerConfirmKeyMsg(msg)
	}
	return false, nil
}

func (m *Model) handleSettingsPriorityKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd
	s := msg.String()
	up := s == "k" || key.Matches(msg, m.keys.Up)
	down := s == "j" || key.Matches(msg, m.keys.Down)
	switch {
	case up:
		// While holding, movement carries the grabbed provider; otherwise it moves the cursor.
		if m.priorityHolding {
			if m.priorityCursor > 0 {
				i := m.priorityCursor
				m.priorityDraft[i], m.priorityDraft[i-1] = m.priorityDraft[i-1], m.priorityDraft[i]
				m.priorityCursor--
			}
		} else if m.priorityCursor > 0 {
			m.priorityCursor--
		}
	case down:
		if m.priorityHolding {
			if m.priorityCursor < len(m.priorityDraft)-1 {
				i := m.priorityCursor
				m.priorityDraft[i], m.priorityDraft[i+1] = m.priorityDraft[i+1], m.priorityDraft[i]
				m.priorityCursor++
			}
		} else if m.priorityCursor < len(m.priorityDraft)-1 {
			m.priorityCursor++
		}
	case key.Matches(msg, m.keys.Toggle): // space: grab / drop the provider under the cursor
		if len(m.priorityDraft) > 0 {
			m.priorityHolding = !m.priorityHolding
		}
	case s == "x": // toggle the provider on/off (browse mode only)
		if !m.priorityHolding && m.priorityCursor >= 0 && m.priorityCursor < len(m.priorityDraft) {
			name := m.priorityDraft[m.priorityCursor]
			if m.priorityDisabled == nil {
				m.priorityDisabled = make(map[string]bool)
			}
			m.priorityDisabled[name] = !m.priorityDisabled[name]
		}
	case key.Matches(msg, m.keys.Confirm):
		priority := m.priorityDraft
		if slices.Equal(priority, m.providerPriorityDraft(m.settings.ProviderPriority)) {
			priority = m.settings.ProviderPriority
		}
		change := app.SetProviderLayout(priority, m.priorityDisabledList())
		if m.applySettingsChange(change) {
			m.editingPriority = false
			m.priorityHolding = false
			m.appendSaveSettingsChangeCmd(&cmds, change)
		}
	case key.Matches(msg, m.keys.Back):
		m.editingPriority = false
		m.priorityHolding = false
	}
	return cmds
}

// In the draft's own order, deterministic for persistence and tests.
func (m Model) priorityDisabledList() []string {
	out := make([]string, 0, len(m.priorityDisabled))
	seen := make(map[string]bool, len(m.priorityDraft))
	for _, name := range m.priorityDraft {
		seen[name] = true
		if m.priorityDisabled[name] {
			out = append(out, name)
		}
	}
	var extras []string
	for name, disabled := range m.priorityDisabled {
		if disabled && !seen[name] {
			extras = append(extras, name)
		}
	}
	sort.Strings(extras)
	out = append(out, extras...)
	return out
}

func (m *Model) handleSettingsServiceDurationKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd
	choices := settingsDurationChoicesForRow(m.serviceDurationRow, m.currentSettingsDurationValue(m.serviceDurationRow))
	switch msg.String() {
	case "h", "left":
		m.serviceDurationIdx = clampIndex(m.serviceDurationIdx-1, len(choices))
	case "l", "right":
		m.serviceDurationIdx = clampIndex(m.serviceDurationIdx+1, len(choices))
	default:
		switch {
		case key.Matches(msg, m.keys.Up):
			m.serviceDurationIdx = clampIndex(m.serviceDurationIdx-1, len(choices))
		case key.Matches(msg, m.keys.Down), key.Matches(msg, m.keys.Toggle):
			m.serviceDurationIdx = clampIndex(m.serviceDurationIdx+1, len(choices))
		case key.Matches(msg, m.keys.Confirm):
			cmds = append(cmds, m.applySettingsServiceDurationChoice(choices)...)
		case key.Matches(msg, m.keys.Back):
			m.editingServiceDuration = false
		}
	}
	return cmds
}

func (m *Model) handleSettingsDangerConfirmKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd
	if m.dangerConfirmRow == settingsRowDotsSync {
		switch strings.ToLower(msg.String()) {
		case "y":
			cmds = append(cmds, m.confirmSettingsDisableDots(true)...)
		case "n":
			cmds = append(cmds, m.confirmSettingsDisableDots(false)...)
		default:
			if key.Matches(msg, m.keys.Back) {
				m.cancelConfirmationTimeout()
				m.dangerConfirmRow = -1
			}
		}
		return cmds
	}
	switch {
	case key.Matches(msg, m.keys.Confirm):
		row := m.dangerConfirmRow
		m.cancelConfirmationTimeout()
		m.dangerConfirmRow = -1
		switch row {
		case settingsRowBootstrap:
			m.startBootstrapSetup()
		case settingsRowResetSettings:
			m.beginLoading(loadingOwnerLocalOp)
			startOp(m, "Resetting settings…")
			cmds = append(cmds, m.spinner.Tick, m.doResetSettings())
		case settingsRowResetCache:
			m.beginLoading(loadingOwnerLocalOp)
			startOp(m, "Resetting cache…")
			cmds = append(cmds, m.spinner.Tick, m.doResetCache())
		}
	case key.Matches(msg, m.keys.Back):
		m.cancelConfirmationTimeout()
		m.dangerConfirmRow = -1
	}
	return cmds
}

func (m *Model) confirmSettingsDisableDots(keepLocal bool) []tea.Cmd {
	m.cancelConfirmationTimeout()
	m.dangerConfirmRow = -1
	m.beginLoading(loadingOwnerLocalOp)
	startOp(m, "Disabling dots…")
	return []tea.Cmd{m.spinner.Tick, m.doDisableDots(keepLocal)}
}

func (m *Model) handleSettingsRowAction(cmds *[]tea.Cmd) {
	if action := settingsRows[m.settingsCursor].action; action != "" {
		if !app.SettingsActionAvailable(m.settings, action) {
			return
		}
		m.applySettingsActionAndSave(cmds, action)
		return
	}

	switch m.settingsCursor {
	case settingsRowDotsReminder:
		m.handleSettingsDotsReminderAction(cmds)
	case settingsRowDotsWatch:
		m.handleSettingsDotsWatchAction(cmds)
	}
}

func (m *Model) applySettingsActionAndSave(cmds *[]tea.Cmd, action app.SettingsActionID) {
	change, err := app.SettingsChangeForAction(action)
	if err != nil {
		m.err = err
		m.startupLoadErr = false
		return
	}
	m.applySettingsChangeAndSave(cmds, change)
}

func (m *Model) applySettingsChangeAndSave(cmds *[]tea.Cmd, change app.SettingsChange) {
	if m.applySettingsChange(change) {
		m.appendSaveSettingsChangeCmd(cmds, change)
	}
}

func (m *Model) applySettingsChange(change app.SettingsChange) bool {
	a := m.app
	if a == nil {
		a = app.New("")
	}
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	next, _, err := a.ApplySettingsChange(ctx, m.settings, change)
	if err != nil {
		m.err = err
		m.startupLoadErr = false
		return false
	}
	m.setSettings(next)
	return true
}

func (m *Model) handleSettingsEditAction(cmds *[]tea.Cmd) {
	switch m.settingsCursor {
	case settingsRowProviderPriority:
		m.startSettingsPriorityEdit()
	case settingsRowDotsRepo:
		*cmds = append(*cmds, m.openFilePicker("Dots repo path", dotsRepoPathForView(*m), false))
	case settingsRowDotsSync:
		m.handleSettingsDotsSyncAction(cmds)
	case settingsRowDotsReminderInterval, settingsRowDotsWatchDebounce:
		m.startSettingsServiceDurationEdit()
	case settingsRowDoctor:
		m.startDoctorRun("Running doctor…")
		*cmds = append(*cmds, m.spinner.Tick, m.doRunDoctor())
	case settingsRowBootstrap:
		m.dangerConfirmRow = settingsRowBootstrap
		*cmds = append(*cmds, m.armConfirmationTimeout())
	case settingsRowResetSettings:
		m.dangerConfirmRow = settingsRowResetSettings
		*cmds = append(*cmds, m.armConfirmationTimeout())
	case settingsRowResetCache:
		m.dangerConfirmRow = settingsRowResetCache
		*cmds = append(*cmds, m.armConfirmationTimeout())
	}
}

func (m *Model) startBootstrapSetup() {
	m.cancelConfirmationTimeout()
	m.dangerConfirmRow = -1
	m.mode = viewSetup
	m.setupBackgroundMode = viewSettings
	m.setupProviderIdx = 0
	m.setupCopyHostIdx = 0
	m.setupGroupIdx = 0
	m.setupGroupDraft = nil
	m.setupActivationIdx = 0
	m.setupComplete = false
	m.setupReloading = false
	if m.hostInfo == nil || m.hostInfo.Active == "" {
		m.setupStep = 2
		if len(m.setupCopyHostNames()) > 0 {
			m.setupStep = 7
		}
		return
	}
	m.setupStep = 10
}

func (m *Model) startSettingsPriorityEdit() {
	m.priorityDraft = m.providerPriorityDraft(m.settings.ProviderPriority)
	m.priorityDisabled = make(map[string]bool, len(m.settings.DisabledProviders))
	for _, name := range m.settings.DisabledProviders {
		m.priorityDisabled[name] = true
	}
	m.priorityAvailable = nil
	if m.app != nil {
		m.priorityAvailable = m.app.AvailableConcreteProviderSet(context.Background())
	}
	m.priorityCursor = 0
	m.priorityHolding = false
	m.editingPriority = true
}

func (m Model) providerPriorityDraft(priority []string) []string {
	if m.app == nil {
		return app.DefaultConcreteProviderPriorityDraft(priority)
	}
	return m.app.ConcreteProviderPriorityDraft(priority)
}

func (m Model) providerPriorityDisplay() []string {
	draft := m.providerPriorityDraft(m.settings.ProviderPriority)
	disabled := make(map[string]bool, len(m.settings.DisabledProviders))
	for _, name := range m.settings.DisabledProviders {
		disabled[name] = true
	}
	out := make([]string, 0, len(draft))
	for _, name := range draft {
		if !disabled[name] {
			out = append(out, name)
		}
	}
	return out
}

func (m *Model) startSettingsServiceDurationEdit() {
	row := m.settingsCursor
	current := m.currentSettingsDurationValue(row)
	choices := settingsDurationChoicesForRow(row, current)
	idx := settingsDurationChoiceIndex(choices, current)
	if idx < 0 {
		idx = 0
	}
	m.serviceDurationRow = row
	m.serviceDurationIdx = idx
	m.editingServiceDuration = true
}

func (m *Model) applySettingsServiceDurationChoice(choices []settingsDurationChoice) []tea.Cmd {
	if len(choices) == 0 {
		m.editingServiceDuration = false
		return nil
	}
	idx := clampRange(m.serviceDurationIdx, 0, len(choices)-1)
	choice := choices[idx]
	row := m.serviceDurationRow
	m.editingServiceDuration = false
	switch row {
	case settingsRowDotsReminderInterval:
		m.dotsReminderInterval = choice.value
		if m.dotsReminderService != nil && m.dotsReminderService.Installed {
			m.beginLoading(loadingOwnerLocalOp)
			startOp(m, "Updating dotfile reminders…")
			return []tea.Cmd{m.spinner.Tick, m.doToggleDotsReminderService(true)}
		}
		return []tea.Cmd{setStatus(m, "✓ reminder interval set to "+choice.label, false)}
	case settingsRowDotsWatchDebounce:
		if m.dotsWatchService != nil && m.dotsWatchService.Installed {
			m.dotsWatchDebounceNext = choice.value
			if m.promptForStowInstall(stowInstallDotsWatch) {
				return nil
			}
			m.beginLoading(loadingOwnerLocalOp)
			startOp(m, "Updating dotfile watch…")
			return []tea.Cmd{m.spinner.Tick, m.doToggleDotsWatchService(true)}
		}
		m.dotsWatchDebounce = choice.value
		return []tea.Cmd{setStatus(m, "✓ watch debounce set to "+choice.label, false)}
	default:
		return nil
	}
}

func (m *Model) handleSettingsDotsSyncAction(cmds *[]tea.Cmd) {
	switch m.dotsSyncAvailability().Reason {
	case app.DotsSyncAvailabilityDisabled:
		if m.promptForStowInstall(stowInstallEnableDots) {
			return
		}
		m.beginDotsOperation("Enabling dots…")
		*cmds = append(*cmds, m.spinner.Tick, m.doEnableDots())
	case app.DotsSyncAvailabilityReady:
		m.dangerConfirmRow = settingsRowDotsSync
		*cmds = append(*cmds, m.armConfirmationTimeout())
	case app.DotsSyncAvailabilityNoRepo, app.DotsSyncAvailabilityUnknown:
		*cmds = append(*cmds, setStatus(m, "Dots not configured.", false))
	}
}

func (m *Model) handleSettingsDotsReminderAction(cmds *[]tea.Cmd) {
	if !m.dotsConfigured() {
		*cmds = append(*cmds, setStatus(m, "Dots not configured.", false))
		return
	}
	if m.app == nil {
		*cmds = append(*cmds, setStatus(m, "Dots reminder service is unavailable.", true))
		return
	}
	enable := m.dotsReminderService == nil || !m.dotsReminderService.Installed
	m.beginLoading(loadingOwnerLocalOp)
	if enable {
		startOp(m, "Enabling dotfile reminders…")
	} else {
		startOp(m, "Disabling dotfile reminders…")
	}
	*cmds = append(*cmds, m.spinner.Tick, m.doToggleDotsReminderService(enable))
}

func (m *Model) handleSettingsDotsWatchAction(cmds *[]tea.Cmd) {
	if !m.dotsConfigured() {
		*cmds = append(*cmds, setStatus(m, "Dots not configured.", false))
		return
	}
	if m.app == nil {
		*cmds = append(*cmds, setStatus(m, "Dots watch service is unavailable.", true))
		return
	}
	enable := m.dotsWatchService == nil || !m.dotsWatchService.Installed
	if enable && m.promptForStowInstall(stowInstallDotsWatch) {
		return
	}
	m.beginLoading(loadingOwnerLocalOp)
	if enable {
		startOp(m, "Enabling dotfile watch…")
	} else {
		startOp(m, "Disabling dotfile watch…")
	}
	*cmds = append(*cmds, m.spinner.Tick, m.doToggleDotsWatchService(enable))
}
