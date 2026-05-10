package tui

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
)

func (m *Model) handleSettingsKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd

	if handled, subCmds := m.handleSettingsSubmodeKeyMsg(msg); handled {
		return subCmds
	}

	switch {
	case key.Matches(msg, m.keys.Up):
		m.settingsCursor = (m.settingsCursor - 1 + numSettingRows) % numSettingRows
	case key.Matches(msg, m.keys.Down):
		m.settingsCursor = (m.settingsCursor + 1) % numSettingRows
	case key.Matches(msg, m.keys.Toggle):
		m.handleSettingsRowAction(&cmds)
	case key.Matches(msg, m.keys.Confirm):
		m.handleSettingsConfirmAction(&cmds)
	case key.Matches(msg, m.keys.Back):
		m.mode = viewList
	}

	return cmds
}

func (m *Model) handleSettingsConfirmAction(cmds *[]tea.Cmd) {
	switch m.settingsCursor {
	case settingsRowSystemPriority, settingsRowDotsRepo, settingsRowDotsSync, settingsRowDotsReminderInterval, settingsRowDotsWatchDebounce, settingsRowDoctor, settingsRowBootstrap, settingsRowResetSettings, settingsRowResetCache:
		m.handleSettingsEditAction(cmds)
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
	switch msg.String() {
	case "j":
		if m.priorityCursor < len(m.priorityDraft)-1 {
			m.priorityCursor++
		}
	case "k":
		if m.priorityCursor > 0 {
			m.priorityCursor--
		}
	case "J":
		if m.priorityCursor < len(m.priorityDraft)-1 {
			i := m.priorityCursor
			m.priorityDraft[i], m.priorityDraft[i+1] = m.priorityDraft[i+1], m.priorityDraft[i]
			m.priorityCursor++
		}
	case "K":
		if m.priorityCursor > 0 {
			i := m.priorityCursor
			m.priorityDraft[i], m.priorityDraft[i-1] = m.priorityDraft[i-1], m.priorityDraft[i]
			m.priorityCursor--
		}
	default:
		if key.Matches(msg, m.keys.Confirm) {
			m.settings.SetEcosystemPriority(provider.EcosystemSystem, m.filterSystemPriority(m.priorityDraft))
			m.editingPriority = false
			m.appendSaveSettingsCmd(&cmds)
		} else if key.Matches(msg, m.keys.Back) {
			m.editingPriority = false
		}
	}
	return cmds
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
			m.loading = true
			startOp(m, "Resetting settings…")
			cmds = append(cmds, m.spinner.Tick, m.doResetSettings())
		case settingsRowResetCache:
			m.loading = true
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
	m.loading = true
	startOp(m, "Disabling dots…")
	return []tea.Cmd{m.spinner.Tick, m.doDisableDots(keepLocal)}
}

func (m *Model) handleSettingsRowAction(cmds *[]tea.Cmd) {
	switch m.settingsCursor {
	case settingsRowAutoImport:
		m.settings.AutoImport = !m.settings.AutoImport
		m.appendSaveSettingsCmd(cmds)
	case settingsRowSystemProvider:
		m.settings.DisabledProviders = toggleProvider(m.settings.DisabledProviders, provider.EcosystemSystem)
		m.appendSaveSettingsCmd(cmds)
	case settingsRowNodeProvider:
		m.settings.DisabledProviders = toggleProvider(m.settings.DisabledProviders, provider.EcosystemNode)
		m.appendSaveSettingsCmd(cmds)
	case settingsRowPythonProvider:
		m.settings.DisabledProviders = toggleProvider(m.settings.DisabledProviders, provider.EcosystemPython)
		m.appendSaveSettingsCmd(cmds)
	case settingsRowNodeManager:
		m.settings.SetEcosystemManager(provider.EcosystemNode, cycleNodeManager(m.settings.EcosystemManager(provider.EcosystemNode)))
		m.appendSaveSettingsCmd(cmds)
	case settingsRowPythonManager:
		m.settings.SetEcosystemManager(provider.EcosystemPython, cyclePythonManager(m.settings.EcosystemManager(provider.EcosystemPython)))
		m.appendSaveSettingsCmd(cmds)
	case settingsRowDotsCommit:
		if !m.settings.DotsGit.AutoPush {
			m.settings.DotsGit.AutoCommit = !m.settings.DotsGit.AutoCommit
			m.appendSaveSettingsCmd(cmds)
		}
	case settingsRowDotsPush:
		m.settings.DotsGit.AutoPush = !m.settings.DotsGit.AutoPush
		m.appendSaveSettingsCmd(cmds)
	case settingsRowDotsReminder:
		m.handleSettingsDotsReminderAction(cmds)
	case settingsRowDotsWatch:
		m.handleSettingsDotsWatchAction(cmds)
	}
}

func (m *Model) handleSettingsEditAction(cmds *[]tea.Cmd) {
	switch m.settingsCursor {
	case settingsRowSystemPriority:
		m.startSettingsPriorityEdit()
	case settingsRowDotsRepo:
		*cmds = append(*cmds, m.openFilePicker("Dots repo path", m.settings.DotsRepo, false))
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
	m.priorityDraft = m.systemPriorityDraft(m.settings.EcosystemPriority(provider.EcosystemSystem))
	m.priorityCursor = 0
	m.editingPriority = true
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
			m.loading = true
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
			m.loading = true
			startOp(m, "Updating dotfile watch…")
			return []tea.Cmd{m.spinner.Tick, m.doToggleDotsWatchService(true)}
		}
		m.dotsWatchDebounce = choice.value
		return []tea.Cmd{setStatus(m, "✓ watch debounce set to "+choice.label, false)}
	default:
		return nil
	}
}

func (m Model) systemPriorityDraft(priority []string) []string {
	defaults := m.systemPriorityDefaults()
	if len(priority) == 0 {
		return defaults
	}
	draft := m.filterSystemPriority(priority)
	if len(draft) == 0 {
		return defaults
	}
	for _, name := range defaults {
		if !slices.Contains(draft, name) {
			draft = append(draft, name)
		}
	}
	return draft
}

func (m Model) systemPriorityDisplay(priority []string) []string {
	return m.filterSystemPriority(priority)
}

func (m Model) systemPriorityDefaults() []string {
	defaults := provider.BuiltinSystemProviderPriorityNames()
	if m.app == nil {
		return defaults
	}
	for _, name := range m.app.ConcreteProviderNamesForEcosystem(provider.EcosystemSystem) {
		if !slices.Contains(defaults, name) {
			defaults = append(defaults, name)
		}
	}
	return defaults
}

func (m Model) filterSystemPriority(priority []string) []string {
	out := make([]string, 0, len(priority))
	seen := make(map[string]struct{}, len(priority))
	for _, name := range priority {
		if _, ok := seen[name]; ok {
			continue
		}
		if !m.isSystemPriorityProvider(name) {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func (m Model) isSystemPriorityProvider(name string) bool {
	if m.app != nil {
		return m.app.IsConcreteProviderForEcosystem(provider.EcosystemSystem, name)
	}
	ecosystem, ok := provider.BuiltinEcosystemFor(name)
	return ok && ecosystem == provider.EcosystemSystem && !provider.BuiltinIsEcosystem(name)
}

func (m *Model) handleSettingsDotsSyncAction(cmds *[]tea.Cmd) {
	if config.BoolVal(m.settings.DotsDisabled) {
		if m.settings.DotsRepo == "" {
			*cmds = append(*cmds, setStatus(m, "Dots not configured.", false))
			return
		}
		if m.promptForStowInstall(stowInstallEnableDots) {
			return
		}
		m.beginDotsOperation("Enabling dots…")
		*cmds = append(*cmds, m.spinner.Tick, m.doEnableDots())
		return
	}
	if m.settings.DotsRepo != "" {
		m.dangerConfirmRow = settingsRowDotsSync
		*cmds = append(*cmds, m.armConfirmationTimeout())
	} else {
		*cmds = append(*cmds, setStatus(m, "Dots not configured.", false))
	}
}

func (m *Model) handleSettingsDotsReminderAction(cmds *[]tea.Cmd) {
	if strings.TrimSpace(m.settings.DotsRepo) == "" {
		*cmds = append(*cmds, setStatus(m, "Dots not configured.", false))
		return
	}
	if m.app == nil {
		*cmds = append(*cmds, setStatus(m, "Dots reminder service is unavailable.", true))
		return
	}
	enable := m.dotsReminderService == nil || !m.dotsReminderService.Installed
	m.loading = true
	if enable {
		startOp(m, "Enabling dotfile reminders…")
	} else {
		startOp(m, "Disabling dotfile reminders…")
	}
	*cmds = append(*cmds, m.spinner.Tick, m.doToggleDotsReminderService(enable))
}

func (m *Model) handleSettingsDotsWatchAction(cmds *[]tea.Cmd) {
	if strings.TrimSpace(m.settings.DotsRepo) == "" {
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
	m.loading = true
	if enable {
		startOp(m, "Enabling dotfile watch…")
	} else {
		startOp(m, "Disabling dotfile watch…")
	}
	*cmds = append(*cmds, m.spinner.Tick, m.doToggleDotsWatchService(enable))
}
