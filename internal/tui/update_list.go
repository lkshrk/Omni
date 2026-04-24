package tui

import (
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/database"
)

const (
	listConfirmSyncAll          = "sync-all"
	listConfirmDelete           = "delete"
	listConfirmReinstallDefault = "reinstall-default"
)

func (m *Model) handleListNavigationKeyMsg(msg tea.KeyPressMsg) bool {
	switch {
	case key.Matches(msg, m.keys.Up):
		if m.cursor > 0 {
			m.cursor--
		}
	case key.Matches(msg, m.keys.Down):
		if m.cursor < len(m.visibleTools)-1 {
			m.cursor++
		}
	case key.Matches(msg, m.keys.Top):
		m.cursor = 0
	case key.Matches(msg, m.keys.Bottom):
		if n := len(m.visibleTools); n > 0 {
			m.cursor = n - 1
		}
	case key.Matches(msg, m.keys.HalfPageDown):
		half := max(listAvailableHeight(*m)/2, 1)
		m.cursor = min(m.cursor+half, max(len(m.visibleTools)-1, 0))
	case key.Matches(msg, m.keys.HalfPageUp):
		half := max(listAvailableHeight(*m)/2, 1)
		m.cursor = max(m.cursor-half, 0)
	case key.Matches(msg, m.keys.PageDown):
		page := max(listAvailableHeight(*m), 1)
		m.cursor = min(m.cursor+page, max(len(m.visibleTools)-1, 0))
	case key.Matches(msg, m.keys.PageUp):
		page := max(listAvailableHeight(*m), 1)
		m.cursor = max(m.cursor-page, 0)
	case key.Matches(msg, m.keys.PrevTab):
		if len(m.providerNames) > 0 {
			if m.providerTabIdx > 0 {
				m.providerTabIdx--
			} else {
				m.providerTabIdx = len(m.providerNames)
			}
			m.applyFilter()
			m.cursor = 0
		}
	case key.Matches(msg, m.keys.NextTab):
		if len(m.providerNames) > 0 {
			if m.providerTabIdx < len(m.providerNames) {
				m.providerTabIdx++
			} else {
				m.providerTabIdx = 0
			}
			m.applyFilter()
			m.cursor = 0
		}
	case key.Matches(msg, m.keys.GroupPrev):
		groupNames := visibleGroupNames(*m)
		if len(groupNames) > 0 {
			allGroups := buildAllGroupNames(groupNames)
			if m.groupTabIdx > 0 {
				m.groupTabIdx--
			} else {
				m.groupTabIdx = len(allGroups)
			}
			m.setGroupFilterFromIdx(allGroups)
			m.applyFilter()
			m.cursor = 0
		}
	case key.Matches(msg, m.keys.GroupNext):
		groupNames := visibleGroupNames(*m)
		if len(groupNames) > 0 {
			allGroups := buildAllGroupNames(groupNames)
			if m.groupTabIdx < len(allGroups) {
				m.groupTabIdx++
			} else {
				m.groupTabIdx = 0
			}
			m.setGroupFilterFromIdx(allGroups)
			m.applyFilter()
			m.cursor = 0
		}
	case key.Matches(msg, m.keys.Back):
		if m.groupFilter != "" {
			m.groupFilter = ""
			m.groupTabIdx = 0
			m.applyFilter()
		} else if m.providerTabIdx != 0 {
			m.providerTabIdx = 0
			m.applyFilter()
		}
	default:
		return false
	}

	m.clearListConfirmation()
	return true
}

func (m *Model) handleListActionKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd

	if handled, confirmCmds := m.handleListConfirmationKeyMsg(msg); handled {
		return confirmCmds
	}

	switch {
	case key.Matches(msg, m.keys.MoveGroup):
		if m.selectedTool() != nil {
			m.openGroupMembershipPicker()
		}
	case key.Matches(msg, m.keys.Claim):
		if msg.IsRepeat {
			break
		}
		if t := m.selectedTool(); t != nil && m.syncStatusOf(t) == syncOrphan {
			m.openGroupPicker(true)
		}
	case key.Matches(msg, m.keys.Ignore):
		if msg.IsRepeat {
			break
		}
		if t := m.selectedTool(); t != nil {
			m.openIgnoreScopePicker(t)
		}
	case key.Matches(msg, m.keys.Confirm):
		if t := m.selectedTool(); t != nil && !t.Installed {
			m.loading = true
			startOp(m, "Installing "+t.Name+"…")
			m.startRowOperation(t.Name, t.Provider, m.statusMsg)
			cmds = append(cmds, m.spinner.Tick, m.doInstall(t.Name, t.Provider))
		}
	case key.Matches(msg, m.keys.Search):
		m.mode = viewSearch
		m.filter.SetValue("")
		m.filter.Focus()
		cmds = append(cmds, textinput.Blink)
	case key.Matches(msg, m.keys.SyncAll):
		if msg.IsRepeat {
			break
		}
		cmds = append(cmds, m.armListConfirmation(listConfirmSyncAll, nil))
	case key.Matches(msg, m.keys.Install):
		if msg.IsRepeat {
			break
		}
		if t := m.selectedTool(); t != nil && !t.Installed {
			if !t.Tracked {
				m.openInstallGroupPicker()
			} else {
				m.loading = true
				startOp(m, "Installing "+t.Name+"…")
				m.startRowOperation(t.Name, t.Provider, m.statusMsg)
				cmds = append(cmds, m.spinner.Tick, m.doInstall(t.Name, t.Provider))
			}
		}
	case key.Matches(msg, m.keys.Delete):
		if msg.IsRepeat {
			break
		}
		if t := m.selectedTool(); t != nil && (t.Installed || t.Tracked) {
			cmds = append(cmds, m.armListConfirmation(listConfirmDelete, t))
		}
	case key.Matches(msg, m.keys.Upgrade):
		if msg.IsRepeat {
			break
		}
		if t := m.selectedTool(); t != nil && t.Installed && t.Outdated {
			uk := toolKey(t.Name, t.Provider)
			if !m.upgradingKeys["*"] && !m.upgradingKeys[uk] {
				m.upgradingKeys[uk] = true
				startOp(m, "Upgrading "+t.Name+"…")
				cmds = append(cmds, m.spinner.Tick, m.doUpgrade(t.Name, t.Provider))
			}
		}
	case key.Matches(msg, m.keys.UpgradeAll):
		if msg.IsRepeat {
			break
		}
		if !m.upgradingKeys["*"] && m.sectionCounts[sectionUpdates] > 0 {
			m.upgradingKeys["*"] = true
			m.loading = true
			m.progressText = ""
			ch, gen := m.beginProgressStream()
			m.markBulkPendingUpdates()
			cmds = append(cmds, m.spinner.Tick, m.doUpgradeAll(ch, gen), waitForProgress(ch, gen))
		}
	case key.Matches(msg, m.keys.Refresh):
		if msg.IsRepeat {
			break
		}
		cmds = append(cmds, m.refreshInstalledProviders()...)
	case key.Matches(msg, m.keys.PinProvider):
		if msg.IsRepeat {
			break
		}
		if t := m.selectedTool(); t != nil && m.syncStatusOf(t) == syncWrongProv {
			m.openProviderScopePicker(t)
		}
	case key.Matches(msg, m.keys.MigrateProvider):
		if msg.IsRepeat {
			break
		}
		if t := m.selectedTool(); t != nil && m.syncStatusOf(t) == syncWrongProv {
			cmds = append(cmds, m.armListConfirmation(listConfirmReinstallDefault, t))
		}
	}

	return cmds
}

func (m *Model) handleListConfirmationKeyMsg(msg tea.KeyPressMsg) (bool, []tea.Cmd) {
	if m.listConfirm.action == "" {
		return false, nil
	}
	if key.Matches(msg, m.keys.Back) {
		m.clearListConfirmation()
		return true, nil
	}
	if !m.matchesListConfirmationAction(msg) {
		m.clearListConfirmation()
		return false, nil
	}

	var cmds []tea.Cmd
	c := m.listConfirm
	m.clearListConfirmation()
	switch c.action {
	case listConfirmSyncAll:
		m.loading = true
		m.progressText = ""
		ch, gen := m.beginProgressStream()
		discovered := append([]*database.ToolCache(nil), m.discoveredTools...)
		m.markBulkPendingSyncAll(discovered)
		cmds = append(cmds, m.spinner.Tick, m.doSyncAllWithProgress(ch, gen, discovered), waitForProgress(ch, gen))
	case listConfirmDelete:
		m.loading = true
		if c.installed {
			startOp(m, "Deleting "+c.name+"…")
			cmds = append(cmds, m.spinner.Tick, m.doDelete(c.name, c.provider))
		} else {
			startOp(m, "Deleting "+c.name+" from config…")
			cmds = append(cmds, m.spinner.Tick, m.doDeleteFromConfig(c.name, c.provider))
		}
		m.startRowOperation(c.name, c.provider, m.statusMsg)
	case listConfirmReinstallDefault:
		m.loading = true
		m.migrating = true
		startOp(m, "Reinstalling "+c.name+" with default ("+c.provider+")…")
		m.startRowOperation(c.name, c.provider, m.statusMsg)
		cmds = append(cmds, m.spinner.Tick, m.doMigrateProvider(c.name, c.provider, c.installedWith))
	}
	return true, cmds
}

func (m *Model) startRowOperation(name, provider, status string) {
	m.rowOpKey = toolKey(name, provider)
	m.rowOpStatus = status
	m.clearToolActionError(m.rowOpKey)
}

func (m *Model) clearRowOperation() {
	m.rowOpKey = ""
	m.rowOpStatus = ""
}

func (m *Model) setToolActionError(key, message string) {
	if key == "" || message == "" {
		return
	}
	if m.rowErrors == nil {
		m.rowErrors = make(map[string]string)
	}
	m.rowErrors[key] = message
}

func (m *Model) clearToolActionError(key string) {
	if key == "" || len(m.rowErrors) == 0 {
		return
	}
	delete(m.rowErrors, key)
}

func (m *Model) clearRowActionError() {
	clear(m.rowErrors)
}

func (m *Model) markBulkPendingUpdates() {
	m.bulkPendingKeys = make(map[string]bool)
	for _, t := range m.visibleTools {
		if t != nil && t.Installed && t.Outdated {
			m.bulkPendingKeys[toolKey(t.Name, t.Provider)] = true
		}
	}
}

func (m *Model) markBulkPendingSync() {
	m.bulkPendingKeys = make(map[string]bool)
	for _, t := range m.visibleTools {
		if t != nil && t.Tracked && !t.Installed {
			m.bulkPendingKeys[toolKey(t.Name, t.Provider)] = true
		}
	}
}

func (m *Model) markBulkPendingSyncAll(discovered []*database.ToolCache) {
	m.markBulkPendingSync()
	for _, t := range discovered {
		if t != nil && t.Name != "" && t.Provider != "" {
			m.bulkPendingKeys[toolKey(t.Name, t.Provider)] = true
		}
	}
}

func (m *Model) clearBulkPending() {
	clear(m.bulkPendingKeys)
}

func (m *Model) armListConfirmation(action string, t *database.ToolCache) tea.Cmd {
	m.listConfirm = listConfirmation{action: action}
	if t != nil {
		m.listConfirm.name = t.Name
		m.listConfirm.provider = t.Provider
		m.listConfirm.installed = t.Installed
		m.listConfirm.installedWith = t.InstalledWith
	}
	switch action {
	case listConfirmSyncAll:
		m.statusMsg = ""
	}
	m.statusIsErr = false
	return m.armConfirmationTimeout()
}

func (m *Model) matchesListConfirmationAction(msg tea.KeyPressMsg) bool {
	switch m.listConfirm.action {
	case listConfirmSyncAll:
		return key.Matches(msg, m.keys.SyncAll)
	case listConfirmDelete:
		return key.Matches(msg, m.keys.Delete)
	case listConfirmReinstallDefault:
		return key.Matches(msg, m.keys.MigrateProvider)
	default:
		return false
	}
}

func (m *Model) clearListConfirmation() {
	if m.listConfirm.action != "" {
		m.cancelConfirmationTimeout()
		clearStatus(m)
	}
	m.listConfirm = listConfirmation{}
}

func (m *Model) openGroupPicker(claim bool) {
	m.mode = viewGroupPicker
	m.pickerGroups = append(prioritizedPickerGroups(*m), groupPickerNewSentinel)
	m.pickerCursor = 0
	m.pickerCreatingGroup = false
	m.pickerPurposeClaim = claim
	m.pickerPurposeInstall = false
	m.pickerCreatedGroups = nil
	if t := m.selectedTool(); t != nil {
		m.pickerActionTool = *t
		m.pickerActionToolSet = true
	} else {
		m.pickerActionTool = database.ToolCache{}
		m.pickerActionToolSet = false
	}
}

func (m *Model) openGroupMembershipPicker() {
	m.mode = viewGroupMembership
	m.pickerGroups = append(prioritizedPickerGroups(*m), groupPickerNewSentinel)
	m.pickerCursor = 0
	m.pickerCreatingGroup = false
	m.pickerCreatedGroups = nil
	m.pickerMembershipKind = pickerMembershipTool
	m.pickerMembershipName = ""
	if t := m.selectedTool(); t != nil {
		m.pickerMembershipName = t.Name
		m.pickerMembershipKey = toolMembershipKey(t)
		m.pickerOriginalGroups = append([]string(nil), m.toolMemberships[m.pickerMembershipKey]...)
	}
}

func (m *Model) openInstallGroupPicker() {
	m.openGroupPicker(false)
	m.pickerPurposeInstall = true
}

func (m *Model) openIgnoreScopePicker(t *database.ToolCache) {
	m.mode = viewIgnoreScope
	m.scopeCursor = 0
	m.scopeOptions = ignoreScopeOptions(*m, t)
	m.scopeTarget = *t
	m.scopeTargetSet = true
}

func (m *Model) openProviderScopePicker(t *database.ToolCache) {
	m.mode = viewProviderScope
	m.scopeCursor = 0
	m.scopeOptions = providerScopeOptions(t)
	m.scopeTarget = *t
	m.scopeTargetSet = true
}

func (m *Model) refreshInstalledProviders() []tea.Cmd {
	var cmds []tea.Cmd
	if len(m.scanningProviders) > 0 {
		return cmds
	}
	clearStatus(m)
	m.scanningProviders = make(map[string]bool)
	for _, t := range m.allTools {
		m.scanningProviders[t.Provider] = true
	}
	if len(m.scanningProviders) == 0 {
		return cmds
	}
	m.scanGen++
	gen := m.scanGen
	cmds = append(cmds, m.spinner.Tick)
	for prov := range m.scanningProviders {
		cmds = append(cmds, m.doScanProvider(prov, gen))
	}
	return cmds
}
