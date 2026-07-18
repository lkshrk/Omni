package tui

import (
	"context"
	"slices"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

const (
	listConfirmSyncAll               = "sync-all"
	listConfirmDelete                = "delete"
	listConfirmReinstallDefault      = "reinstall-default"
	listConfirmClearProviderOverride = "clear-provider-override"
	listConfirmMigrateNvm            = "migrate-nvm"
	listConfirmRemoveNvmRuntime      = "remove-nvm-runtime"
)

func (m *Model) handleListNavigationKeyMsg(msg tea.KeyPressMsg) bool {
	switch {
	case key.Matches(msg, m.keys.Up):
		if n := len(m.visibleTools); n > 0 {
			m.cursor = cursorMove(m.cursor, -1, n, true)
			m.providerCandidateCursor = 0
		}
	case key.Matches(msg, m.keys.Down):
		if n := len(m.visibleTools); n > 0 {
			m.cursor = cursorMove(m.cursor, 1, n, true)
			m.providerCandidateCursor = 0
		}
	case key.Matches(msg, m.keys.ProviderPrev):
		if candidates := providerCandidateOptions(*m, m.selectedTool()); len(candidates) > 0 && m.providerCandidateCursor > 0 {
			m.providerCandidateCursor--
		}
	case key.Matches(msg, m.keys.ProviderNext):
		if candidates := providerCandidateOptions(*m, m.selectedTool()); len(candidates) > 0 && m.providerCandidateCursor < len(candidates)-1 {
			m.providerCandidateCursor++
		}
	case key.Matches(msg, m.keys.Top):
		m.cursor = 0
		m.providerCandidateCursor = 0
	case key.Matches(msg, m.keys.Bottom):
		if n := len(m.visibleTools); n > 0 {
			m.cursor = n - 1
			m.providerCandidateCursor = 0
		}
	case key.Matches(msg, m.keys.HalfPageDown):
		half := max(listAvailableHeight(*m)/2, 1)
		m.cursor = min(m.cursor+half, max(len(m.visibleTools)-1, 0))
		m.providerCandidateCursor = 0
	case key.Matches(msg, m.keys.HalfPageUp):
		half := max(listAvailableHeight(*m)/2, 1)
		m.cursor = max(m.cursor-half, 0)
		m.providerCandidateCursor = 0
	case key.Matches(msg, m.keys.PageDown):
		page := max(listAvailableHeight(*m), 1)
		m.cursor = min(m.cursor+page, max(len(m.visibleTools)-1, 0))
		m.providerCandidateCursor = 0
	case key.Matches(msg, m.keys.PageUp):
		page := max(listAvailableHeight(*m), 1)
		m.cursor = max(m.cursor-page, 0)
		m.providerCandidateCursor = 0
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
		m.clearToolFiltersAndSearch()
	default:
		return false
	}

	m.clearListConfirmation()
	return true
}

func (m *Model) handleListActionKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd
	selected := m.selectedTool()

	if handled, confirmCmds := m.handleListConfirmationKeyMsg(msg); handled {
		return confirmCmds
	}

	switch {
	case key.Matches(msg, m.keys.ErrorLog) && rowActionErrorStatus(*m, selected) != "":
		if msg.IsRepeat {
			break
		}
		cmds = append(cmds, m.openTraceLog())
	case key.Matches(msg, m.keys.EditIgnore):
		if msg.IsRepeat {
			break
		}
		if selected != nil && m.displaySection(selected) == sectionIgnored {
			m.openIgnoreScopePicker(selected)
		}
	case key.Matches(msg, m.keys.ApplySolution):
		if msg.IsRepeat {
			break
		}
		if t := m.selectedTool(); t != nil {
			solution, ok := m.selectedRowApplicableSolution()
			if !ok {
				break
			}
			m.loading = true
			startOp(m, "Applying fix: "+solution.Label+"…")
			m.startRowOperation(t.Name, t.Provider, m.statusMsg)
			cmds = append(cmds, m.spinner.Tick, m.doApplyProviderSolution(t.Name, t.Provider, solution))
		}
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
			if m.displaySection(t) == sectionIgnored {
				options := ignoreScopeOptions(*m, t)
				for i := range options {
					options[i].checked = false
				}
				m.loading = true
				startOp(m, "Including "+t.Name+"…")
				cmds = append(cmds, m.spinner.Tick, m.doSaveIgnoreScopes(t.Name, options))
			} else {
				m.openIgnoreScopePicker(t)
			}
		}
	case key.Matches(msg, m.keys.Confirm):
		if t := m.selectedTool(); t != nil && !t.Installed {
			cmds = append(cmds, m.startSelectedToolInstall(t)...)
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
		if t := m.selectedTool(); t != nil {
			switch m.syncStatusOf(t) {
			case syncWrongProv:
				cmds = append(cmds, m.armListConfirmation(listConfirmReinstallDefault, t))
			default:
				if !t.Installed {
					cmds = append(cmds, m.startSelectedToolInstall(t)...)
				}
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
			if m.blockPrivilegedToolAction(t, provider.PrivilegeActionUpgrade) {
				break
			}
			uk := toolKey(t.Name, t.Provider)
			if !m.upgradingKeys["*"] && !m.upgradingKeys[uk] {
				m.upgradingKeys[uk] = true
				m.loading = true
				startOp(m, "Upgrading "+t.Name+"…")
				m.startRowOperation(t.Name, t.Provider, m.statusMsg)
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
		if t := m.selectedTool(); t != nil {
			if providerPinForTool(t, m.toolProviderPins) != "" {
				cmds = append(cmds, m.armListConfirmation(listConfirmClearProviderOverride, t))
			} else if isProviderRepairSync(m.syncStatusOf(t)) {
				m.openProviderScopePicker(t)
			}
		}
	case key.Matches(msg, m.keys.Fallback):
		if msg.IsRepeat {
			break
		}
		if t := m.selectedTool(); t != nil {
			if cmd := m.openFallbackEditor(t); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	case key.Matches(msg, m.keys.MigrateProvider):
		if msg.IsRepeat {
			break
		}
		if t := m.selectedTool(); t != nil {
			switch m.syncStatusOf(t) {
			case syncWrongProv:
				cmds = append(cmds, m.armListConfirmation(listConfirmReinstallDefault, t))
			case syncNvmManaged:
				if t.Name == "node" {
					cmds = append(cmds, m.armListConfirmation(listConfirmRemoveNvmRuntime, t))
				} else {
					cmds = append(cmds, m.armListConfirmation(listConfirmMigrateNvm, t))
				}
			}
		}
	}

	return cmds
}

func (m *Model) startSelectedToolInstall(t *database.ToolCache) []tea.Cmd {
	if t == nil || t.Installed {
		return nil
	}
	if m.isInstallAndAddCandidate(t) {
		m.openInstallGroupPicker()
		return nil
	}
	installTool := m.selectedProviderCandidateTool(t)
	if m.blockPrivilegedToolAction(installTool, provider.PrivilegeActionInstall) {
		return nil
	}
	m.loading = true
	startOp(m, "Installing "+t.Name+"…")
	m.startRowOperation(t.Name, installTool.Provider, m.statusMsg)
	return []tea.Cmd{m.spinner.Tick, m.doInstall(t.Name, installTool.Provider)}
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
		if m.mode == viewStatus {
			m.startDashboardReconcile(&cmds)
		} else {
			m.startToolSyncAllConfirmed(&cmds)
		}
	case listConfirmDelete:
		m.loading = true
		if c.installed {
			if m.blockPrivilegedToolAction(&database.ToolCache{Name: c.name, Provider: c.provider, Installed: true, InstalledWith: c.installedWith}, provider.PrivilegeActionUninstall) {
				m.loading = false
				break
			}
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
	case listConfirmMigrateNvm:
		m.loading = true
		m.migrating = true
		startOp(m, "Migrating "+c.name+" off Homebrew to "+m.effectiveNodeManagerLabel()+"…")
		m.startRowOperation(c.name, c.provider, m.statusMsg)
		cmds = append(cmds, m.spinner.Tick, m.doMigrateNvmTool(c.name))
	case listConfirmRemoveNvmRuntime:
		m.loading = true
		startOp(m, "Removing "+c.name+" from omni config…")
		m.startRowOperation(c.name, c.provider, m.statusMsg)
		cmds = append(cmds, m.spinner.Tick, m.doMigrateNvmTool(c.name))
	case listConfirmClearProviderOverride:
		m.loading = true
		m.migrating = c.installed && c.installedWith != ""
		startOp(m, "Removing provider override for "+c.name+"…")
		m.startRowOperation(c.name, c.provider, m.statusMsg)
		clearProv := c.pinnedProvider
		if clearProv == "" {
			clearProv = c.provider
		}
		cmds = append(cmds, m.spinner.Tick, m.doClearProviderOverride(c.name, clearProv, c.installedWith))
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

func (m *Model) beginCancellableAction() context.Context {
	if m.activeActionCancel != nil {
		m.activeActionCancel()
	}
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	m.activeActionCancel = cancel
	return ctx
}

func (m *Model) finishCancellableAction() {
	m.activeActionCancel = nil
}

func (m *Model) cancelActiveAction() tea.Cmd {
	if m.activeActionCancel == nil {
		return nil
	}
	m.activeActionCancel()
	m.activeActionCancel = nil
	m.progressGen++
	m.progressCh = nil
	m.progressText = ""
	m.loading = false
	m.migrating = false
	m.clearRowOperation()
	m.clearBulkPending()
	clear(m.upgradingKeys)
	m.adminTerminalQueue = nil
	return setStatus(m, "cancelled", false)
}

func (m *Model) setToolActionError(key, message string, errValues ...error) {
	if key == "" || message == "" {
		return
	}
	if m.rowErrors == nil {
		m.rowErrors = make(map[string]string)
	}
	m.rowErrors[key] = message
	if len(errValues) > 0 {
		if actionErr, ok := provider.ActionErrorFrom(errValues[0]); ok {
			if m.rowActionErrors == nil {
				m.rowActionErrors = make(map[string]*provider.ActionError)
			}
			m.rowActionErrors[key] = actionErr
		}
	}
}

func (m *Model) isInstallAndAddCandidate(t *database.ToolCache) bool {
	if t == nil {
		return false
	}
	for _, searchTool := range m.searchTools {
		if searchTool == t {
			return true
		}
	}
	for _, discoveredTool := range m.discoveredTools {
		if discoveredTool == t {
			return true
		}
	}
	return false
}

func (m *Model) clearToolActionError(key string) {
	if key == "" || len(m.rowErrors) == 0 {
		return
	}
	delete(m.rowErrors, key)
	delete(m.rowActionErrors, key)
}

func (m *Model) clearRowActionError() {
	clear(m.rowErrors)
	clear(m.rowActionErrors)
}

func (m Model) selectedRowApplicableSolution() (provider.ErrorSolution, bool) {
	t := m.selectedTool()
	if t == nil || len(m.rowActionErrors) == 0 {
		return provider.ErrorSolution{}, false
	}
	actionErr := m.rowActionErrors[toolKey(t.Name, t.Provider)]
	if actionErr == nil {
		return provider.ErrorSolution{}, false
	}
	return app.FirstApplicableProviderSolution(actionErr)
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
		if action == listConfirmClearProviderOverride {
			m.listConfirm.pinnedProvider = providerPinForTool(t, m.toolProviderPins)
		}
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
		return key.Matches(msg, m.keys.Install) || key.Matches(msg, m.keys.MigrateProvider)
	case listConfirmMigrateNvm, listConfirmRemoveNvmRuntime:
		return key.Matches(msg, m.keys.MigrateProvider)
	case listConfirmClearProviderOverride:
		return key.Matches(msg, m.keys.PinProvider)
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
	t := m.selectedTool()
	if t == nil {
		return
	}
	m.openToolGroupMembershipPicker(t)
}

func (m *Model) openToolGroupMembershipPicker(t *database.ToolCache) {
	m.mode = viewGroupMembership
	groups := prioritizedPickerGroups(*m)
	if t != nil && m.hostInventoryTools[t.Name] && !slices.Contains(groups, config.SystemInventoryGroup) {
		groups = append(groups, config.SystemInventoryGroup)
	}
	m.pickerGroups = append(groups, groupPickerNewSentinel)
	m.pickerCursor = 0
	m.pickerCreatingGroup = false
	m.pickerCreatedGroups = nil
	m.pickerMembershipKind = pickerMembershipTool
	m.pickerMembershipName = ""
	if t != nil {
		m.pickerMembershipName = t.Name
		m.pickerMembershipKey = toolMembershipKey(t)
		m.pickerOriginalGroups = append([]string(nil), m.toolMemberships[m.pickerMembershipKey]...)
		if m.hostInventoryTools[t.Name] && !slices.Contains(m.pickerOriginalGroups, config.SystemInventoryGroup) {
			m.pickerOriginalGroups = append(m.pickerOriginalGroups, config.SystemInventoryGroup)
			if m.toolMemberships == nil {
				m.toolMemberships = make(map[string][]string)
			}
			m.toolMemberships[m.pickerMembershipKey] = append([]string(nil), m.pickerOriginalGroups...)
		}
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
	m.scopeOptions = m.providerScopeOptions(t)
	m.scopeTarget = *t
	m.scopeTargetSet = true
}

func (m *Model) refreshInstalledProviders() []tea.Cmd {
	if len(m.scanningProviders) > 0 || len(m.outdatedProviders) > 0 || m.providerSnapshotRefreshing || m.outdatedSnapshotRefreshing {
		return nil
	}
	clearStatus(m)
	m.loading = true
	setActivityStatus(m, "Refreshing tools…")
	cmds := m.startCurrentProviderScans()
	if len(m.scanningProviders) == 0 {
		m.loading = false
		m.progressText = ""
	}
	return cmds
}

func (m *Model) startToolSyncAllConfirmed(cmds *[]tea.Cmd) {
	m.loading = true
	m.progressText = ""
	ch, gen := m.beginProgressStream()
	discovered := append([]*database.ToolCache(nil), m.discoveredTools...)
	m.markBulkPendingSyncAll(discovered)
	*cmds = append(*cmds, m.spinner.Tick, m.doSyncAllWithProgress(ch, gen, discovered), waitForProgress(ch, gen))
}
