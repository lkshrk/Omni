package tui

import (
	"errors"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
)

func (m *Model) handleDescRefreshDoneMsg(msg descRefreshDoneMsg) tea.Cmd {
	if msg.gen != m.descRefreshGen {
		return nil
	}
	m.descRefreshing = false
	m.releaseDescriptionProgressStream()
	if msg.err != nil && (m.ctx == nil || !errors.Is(msg.err, m.ctx.Err())) {
		m.finishSetupReloadIfIdle()
		status := "description refresh failed: " + msg.err.Error()
		if m.collectLaunchBatchError(status) {
			m.settleToolRefresh()
			cmd := m.finishLaunchBatchIfIdle()
			if cmd != nil {
				m.clearRefreshIssue()
			}
			return cmd
		}
		m.rememberRefreshIssue(status, refreshIssueSnapshotError)
	}
	m.settleToolRefresh()
	changed := false
	if msg.tools != nil {
		m.allTools = msg.tools
		changed = true
	}
	if msg.discovered != nil {
		m.discoveredTools = msg.discovered
		m.rebuildDiscoveredKeys()
		changed = true
	}
	if changed {
		m.applyFilter()
	}
	m.finishSetupReloadIfIdle()
	if cmd := m.finishLaunchBatchIfIdle(); cmd != nil {
		m.clearRefreshIssue()
		return cmd
	}
	if cmd := m.finishRefreshIssueIfIdle(); cmd != nil {
		return cmd
	}
	m.clearActivityStatusIfIdle()
	return nil
}

func (m *Model) releaseDescriptionProgressStream() {
	if m.descriptionProgressCh == nil {
		return
	}
	if m.progressCh == m.descriptionProgressCh {
		m.progressCh = nil
		m.progressGen++
	}
	m.descriptionProgressCh = nil
}

func (m *Model) handleProgressMsg(msg progressMsg) []tea.Cmd {
	if msg.gen != m.progressGen {
		return nil
	}
	if msg.refreshProvider != "" {
		m.handleRefreshToolProgress(msg)
	} else {
		m.progressText = msg.text
	}
	progressChangedList := false
	if msg.tools != nil {
		m.allTools = msg.tools
		progressChangedList = true
	}
	if len(msg.claimedNames) > 0 {
		if m.removeDiscoveredByName(msg.claimedNames) {
			progressChangedList = true
		}
	}
	if msg.toolGroups != nil {
		m.toolGroups = msg.toolGroups
		progressChangedList = true
	}
	if msg.toolMemberships != nil {
		m.toolMemberships = msg.toolMemberships
		progressChangedList = true
	}
	if msg.groupNames != nil {
		m.groupNames = msg.groupNames
		progressChangedList = true
	}
	if msg.rowKey != "" {
		delete(m.bulkPendingKeys, msg.rowKey)
		switch {
		case msg.rowErr != "":
			m.clearRowOperation()
			m.setToolActionError(msg.rowKey, msg.rowErr)
		case msg.rowDone:
			if m.rowOpKey == msg.rowKey {
				m.clearRowOperation()
			}
			m.clearToolActionError(msg.rowKey)
			if m.markToolProgressDone(msg.rowKey) {
				progressChangedList = true
			}
		case msg.rowStatus != "":
			parts := strings.SplitN(msg.rowKey, "\x00", 2)
			if len(parts) == 2 {
				m.startRowOperation(parts[0], parts[1], msg.rowStatus)
			}
		}
	}
	if progressChangedList {
		m.applyFilter()
	}
	if m.progressCh != nil {
		return []tea.Cmd{waitForProgress(m.progressCh, m.progressGen)}
	}
	return nil
}

func (m *Model) markToolProgressDone(rowKey string) bool {
	if rowKey == "" {
		return false
	}
	changed := false
	for _, t := range m.allTools {
		if t == nil || toolKey(t.Name, t.Provider) != rowKey {
			continue
		}
		if !t.Installed || !t.Tracked || t.Outdated {
			changed = true
		}
		t.Installed = true
		t.Tracked = true
		t.Outdated = false
	}
	return changed
}

func (m *Model) handleRefreshToolProgress(msg progressMsg) {
	providerName := msg.refreshProvider
	if providerName == "" {
		return
	}
	if len(m.scanningProviders) > 0 && !m.scanningProviders[providerName] {
		return
	}
	if m.providerScanToolDone == nil {
		m.providerScanToolDone = make(map[string]int)
	}
	expected := m.providerScanToolCounts[providerName]
	if expected <= 0 {
		expected = 1
	}
	if m.providerScanToolDone[providerName] >= expected {
		return
	}
	m.providerScanToolDone[providerName]++
	if m.refreshToolTotal == 0 {
		m.refreshToolTotal = app.RefreshProviderScanCountTotal(m.providerScanToolCounts)
	}
	m.refreshToolDone++
	if m.refreshToolTotal > 0 && m.refreshToolDone > m.refreshToolTotal {
		m.refreshToolDone = m.refreshToolTotal
	}
	label := app.RefreshProviderScanLabel(providerName, m.providerScanLabels)
	if eventLabel := strings.TrimSpace(msg.refreshProviderLabel); eventLabel != "" && (eventLabel != providerName || label == providerName) {
		label = eventLabel
	}
	m.progressText = app.RefreshToolProgressStatus(label, msg.refreshToolName, m.refreshToolDone, m.refreshToolTotal)
}

func (m *Model) handleProgressStreamClosedMsg(msg progressStreamClosedMsg) {
	if msg.gen != m.progressGen {
		return
	}
	m.progressCh = nil
}

func (m *Model) handleProgressDoneMsg(msg progressDoneMsg) []tea.Cmd {
	var cmds []tea.Cmd
	if msg.gen != m.progressGen {
		return nil
	}
	m.finishCancellableAction()
	m.progressGen++
	if !m.migrating {
		m.endLoading(loadingOwnerProgressOp)
	}
	delete(m.upgradingKeys, msg.key)
	m.progressText = ""
	m.progressCh = nil
	m.clearRowOperation()
	m.clearBulkPending()
	if msg.tools != nil {
		m.allTools = msg.tools
	}
	if len(msg.claimedNames) > 0 {
		m.removeDiscoveredByName(msg.claimedNames)
		m.startGroupReassignQueue(msg.claimedNames)
	}
	if msg.toolGroups != nil {
		m.toolGroups = msg.toolGroups
	}
	if msg.toolMemberships != nil {
		m.toolMemberships = msg.toolMemberships
	}
	if msg.groupNames != nil {
		m.groupNames = msg.groupNames
	}
	m.applyFilter()
	if msg.rowErrors != nil || msg.rowActionErrors != nil {
		m.clearRowActionError()
		for key, message := range msg.rowErrors {
			m.setToolActionError(key, message)
		}
		for key, actionErr := range msg.rowActionErrors {
			if actionErr == nil {
				continue
			}
			m.setToolActionError(key, actionErr.Error(), actionErr)
		}
	}
	promptingPrivilegedAction := false
	if msg.err == nil && len(msg.rowErrors) > 0 && len(msg.promptPrivilegedActions) > 0 {
		promptingPrivilegedAction = m.queuePrivilegedToolPrompts(msg.rowErrors, msg.promptPrivilegedActions)
	}
	progressErr := firstToolProgressError(msg)
	if isContextCanceled(msg.err) {
		m.adminTerminalQueue = nil
		cmds = append(cmds, setStatus(m, "cancelled", false))
	} else if msg.err != nil {
		m.adminTerminalQueue = nil
		cmds = append(cmds, setStatus(m, "✗ "+rowErrorSummary(msg.err.Error()), true))
	} else if promptingPrivilegedAction {
		m.cancelDashboardReconcile()
		return cmds
	} else if progressErr != nil {
		cmds = append(cmds, setStatus(m, "✗ "+rowErrorSummary(progressErr.Error()), true))
	} else if msg.message != "" {
		if msg.rowErrors == nil {
			m.clearRowActionError()
		}
		cmds = append(cmds, setStatus(m, "✓ "+msg.message, false))
	}
	switch m.dashboardReconcileCurrent {
	case dashboardReconcilePlanSyncTools, dashboardReconcilePlanUpgradeTools:
		m.continueDashboardReconcile(m.dashboardReconcileCurrent, progressErr, &cmds)
	}
	return cmds
}

func firstToolProgressError(msg progressDoneMsg) error {
	if msg.err != nil {
		return msg.err
	}
	keys := make([]string, 0, len(msg.rowActionErrors)+len(msg.rowErrors))
	for key := range msg.rowActionErrors {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := msg.rowActionErrors[key]; err != nil {
			return err
		}
	}
	keys = keys[:0]
	for key := range msg.rowErrors {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		return errors.New(msg.rowErrors[keys[0]])
	}
	return nil
}

func (m *Model) removeDiscoveredByName(names []string) bool {
	if len(names) == 0 || len(m.discoveredTools) == 0 {
		return false
	}
	claimed := make(map[string]struct{}, len(names))
	for _, name := range names {
		claimed[name] = struct{}{}
	}
	remaining := m.discoveredTools[:0]
	changed := false
	for _, t := range m.discoveredTools {
		if _, ok := claimed[t.Name]; ok {
			changed = true
			continue
		}
		remaining = append(remaining, t)
	}
	if !changed {
		return false
	}
	m.discoveredTools = remaining
	m.rebuildDiscoveredKeys()
	return true
}

func (m *Model) removeDiscoveredByKeys(keys []string) bool {
	if len(keys) == 0 || len(m.discoveredTools) == 0 {
		return false
	}
	remove := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		remove[key] = struct{}{}
	}
	remaining := m.discoveredTools[:0]
	changed := false
	for _, t := range m.discoveredTools {
		if _, ok := remove[toolKey(t.Name, t.Provider)]; ok {
			changed = true
			continue
		}
		remaining = append(remaining, t)
	}
	if !changed {
		return false
	}
	m.discoveredTools = remaining
	m.rebuildDiscoveredKeys()
	return true
}

func (m *Model) removeSearchResultsByKeys(keys []string) bool {
	if len(keys) == 0 {
		return false
	}
	remove := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		remove[key] = struct{}{}
	}

	changed := false
	if len(m.searchTools) > 0 {
		remaining := m.searchTools[:0]
		for _, t := range m.searchTools {
			if _, ok := remove[toolKey(t.Name, t.Provider)]; ok {
				changed = true
				continue
			}
			remaining = append(remaining, t)
		}
		m.searchTools = remaining
	}
	for cacheKey, entry := range m.searchCache {
		if len(entry.tools) == 0 {
			continue
		}
		remaining := entry.tools[:0]
		entryChanged := false
		for _, t := range entry.tools {
			if _, ok := remove[toolKey(t.Name, t.Provider)]; ok {
				entryChanged = true
				continue
			}
			remaining = append(remaining, t)
		}
		if entryChanged {
			entry.tools = remaining
			m.searchCache[cacheKey] = entry
			changed = true
		}
	}
	return changed
}

func (m *Model) handleOpCompleteMsg(msg opCompleteMsg) []tea.Cmd {
	var cmds []tea.Cmd
	m.finishCancellableAction()
	if m.opCompleteOwnsGate(msg.loadingGen) {
		m.releaseLoading()
	}
	rowErrKey := msg.key
	if rowErrKey == "" {
		rowErrKey = m.rowOpKey
	}
	m.clearRowOperation()
	delete(m.upgradingKeys, msg.key)
	m.applyOpCompleteRefresh(msg)
	if isContextCanceled(msg.err) {
		m.adminTerminalQueue = nil
		cmds = append(cmds, setStatus(m, "cancelled", false))
	} else if msg.err != nil {
		m.setToolActionError(rowErrKey, msg.err.Error(), msg.err)
		if msg.preserveOtherRowErrors && m.openNextQueuedAdminTerminalAction() {
			return cmds
		}
		m.adminTerminalQueue = nil
		cmds = append(cmds, setStatus(m, "✗ "+rowErrorSummary(msg.err.Error()), true))
	} else {
		if msg.preserveOtherRowErrors && rowErrKey != "" {
			m.clearToolActionError(rowErrKey)
		} else {
			m.clearRowActionError()
		}
		if msg.preserveOtherRowErrors && m.openNextQueuedAdminTerminalAction() {
			return cmds
		}
		cmds = append(cmds, setStatus(m, "✓ "+msg.message, false))
	}
	return cmds
}

func (m *Model) applyOpCompleteRefresh(msg opCompleteMsg) {
	changed := m.removeDiscoveredByKeys(msg.removeDiscoveredKeys)
	if m.removeSearchResultsByKeys(msg.removeDiscoveredKeys) {
		changed = true
	}
	if msg.tools != nil {
		m.allTools = msg.tools
		changed = true
	}
	if msg.toolGroups != nil {
		m.toolGroups = msg.toolGroups
		changed = true
	}
	if msg.toolMemberships != nil {
		m.toolMemberships = msg.toolMemberships
		changed = true
	}
	if msg.groupNames != nil {
		m.groupNames = msg.groupNames
		changed = true
	}
	if msg.hostInfo != nil {
		m.hostInfo = msg.hostInfo
		changed = true
	}
	if msg.toolProviderPins != nil {
		m.toolProviderPins = msg.toolProviderPins
		changed = true
	}
	if changed {
		m.applyFilter()
	}
}

func (m *Model) handleCreateGroupDoneMsg(msg createGroupDoneMsg) []tea.Cmd {
	var cmds []tea.Cmd
	m.loading = false
	m.groupCreating = false
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
		return cmds
	}
	cmds = append(cmds, setStatus(m, "✓ group "+msg.name+" created and assigned to this host", false))
	refreshed := false
	if msg.groupNames != nil {
		m.groupNames = msg.groupNames
		refreshed = true
		allGroupNames := buildAllGroupNames(m.groupNames)
		for i, name := range allGroupNames {
			if name == msg.name {
				m.groupCursor = i
				break
			}
		}
	}
	if msg.toolMemberships != nil {
		m.toolMemberships = msg.toolMemberships
		refreshed = true
	}
	if msg.toolGroups != nil {
		m.toolGroups = msg.toolGroups
		refreshed = true
	}
	if msg.hostInfo != nil {
		m.hostInfo = msg.hostInfo
		refreshed = true
	}
	if refreshed {
		m.applyFilter()
	}
	return cmds
}

func (m *Model) handleHostCopiedMsg(msg hostCopiedMsg) []tea.Cmd {
	if msg.err != nil {
		return []tea.Cmd{setStatus(m, "✗ "+msg.err.Error(), true)}
	}
	if msg.info != nil {
		m.hostInfo = msg.info
	}
	m.applyCopiedHost(msg.host)
	return []tea.Cmd{setStatus(m, "✓ copied groups from "+msg.host, false)}
}

func (m *Model) handleGroupChangedMsg(msg groupChangedMsg) []tea.Cmd {
	var cmds []tea.Cmd
	m.loading = false
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
		return cmds
	}
	m.clearRowActionError()
	cmds = append(cmds, setStatus(m, msg.detail, false))
	if msg.tools != nil {
		m.allTools = msg.tools
	}
	if msg.toolGroups != nil {
		m.toolGroups = msg.toolGroups
	}
	if msg.groupNames != nil {
		m.groupNames = msg.groupNames
		allGroupNames := buildAllGroupNames(m.groupNames)
		if m.groupCursor >= len(allGroupNames) {
			m.groupCursor = max(len(allGroupNames)-1, 0)
		}
	}
	if msg.info != nil {
		m.hostInfo = msg.info
	}
	m.applyFilter()
	return cmds
}

func (m *Model) handleGroupToolsChangedMsg(msg groupToolsChangedMsg) []tea.Cmd {
	var cmds []tea.Cmd
	m.loading = false
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
		return cmds
	}
	m.clearRowActionError()
	cmds = append(cmds, setStatus(m, msg.detail, false))
	if msg.tools != nil {
		m.allTools = msg.tools
	}
	if msg.toolGroups != nil {
		m.toolGroups = msg.toolGroups
	}
	if msg.toolMemberships != nil {
		m.toolMemberships = msg.toolMemberships
	}
	if msg.groupNames != nil {
		m.groupNames = msg.groupNames
	}
	if msg.ignoreLabels != nil {
		m.ignoreLabels = msg.ignoreLabels
	}
	if msg.toolIgnoreSet != nil {
		m.toolIgnoreSet = msg.toolIgnoreSet
	}
	if msg.groupIgnoreSet != nil {
		m.groupIgnoreSet = msg.groupIgnoreSet
	}
	m.applyFilter()
	return cmds
}

func (m *Model) handleGroupDotsChangedMsg(msg groupDotsChangedMsg) []tea.Cmd {
	var cmds []tea.Cmd
	m.loading = false
	if msg.err != nil {
		if msg.dotMemberships != nil {
			m.dotMemberships = msg.dotMemberships
		}
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
		return cmds
	}
	cmds = append(cmds, setStatus(m, msg.detail, false))
	if msg.dotMemberships != nil {
		m.dotMemberships = msg.dotMemberships
	}
	if msg.entries != nil {
		m.applyDotsSnapshot(msg.entries, msg.gitStatus, msg.dotMemberships)
	}
	return cmds
}

func (m *Model) handleSettingsSavedMsg(msg settingsSavedMsg) []tea.Cmd {
	if msg.gen != 0 && (!m.settingsSaveRunning || msg.gen != m.settingsSaveInFlightGen) {
		return nil
	}
	if msg.gen != 0 && m.settingsSaveQueued {
		changes := append([]app.SettingsChange(nil), m.settingsSaveQueuedChanges...)
		gen := m.settingsSaveQueuedGen
		m.settingsSaveQueued = false
		m.settingsSaveQueuedChanges = nil
		m.settingsSaveQueuedGen = 0
		if len(changes) > 0 {
			return []tea.Cmd{m.startSettingsChangeSave(changes, gen)}
		}
	}
	if msg.gen != 0 {
		m.settingsSaveRunning = false
		m.settingsSaveInFlightGen = 0
	}
	if msg.err != nil {
		return []tea.Cmd{setStatus(m, "✗ "+msg.err.Error(), true)}
	}
	if msg.hasSettings {
		m.setSettings(msg.settings)
	}
	return []tea.Cmd{setStatus(m, "✓ settings saved", false)}
}

func (m *Model) handleDotsServiceChangedMsg(msg dotsServiceChangedMsg) []tea.Cmd {
	m.loading = false
	name := "service"
	switch msg.kind {
	case dotsReminderServiceKind:
		name = "reminder"
		if msg.err != nil {
			m.dotsReminderInterval = app.DotsReminderInterval(m.dotsReminderService)
			break
		}
		if msg.reminder != nil {
			m.dotsReminderService = msg.reminder
			if msg.enabled && msg.reminder.Interval > 0 {
				m.dotsReminderInterval = msg.reminder.Interval
			}
		}
		m.dotsReminderServiceErr = ""
	case dotsWatchServiceKind:
		name = "watch"
		if msg.err != nil {
			m.dotsWatchDebounce = app.DotsWatchDebounce(m.dotsWatchService)
			m.dotsWatchDebounceNext = 0
			break
		}
		if msg.watch != nil {
			m.dotsWatchService = msg.watch
			if msg.enabled && msg.watch.Debounce > 0 {
				m.dotsWatchDebounce = msg.watch.Debounce
			}
		}
		m.dotsWatchDebounceNext = 0
		m.dotsWatchServiceErr = ""
	}
	if msg.err != nil {
		return []tea.Cmd{setStatus(m, "✗ dotfile "+name+" service: "+msg.err.Error(), true)}
	}
	action := "enabled"
	if !msg.enabled {
		action = "disabled"
	}
	return []tea.Cmd{setStatus(m, "✓ dotfile "+name+" service "+action, false)}
}

func (m *Model) handleDoctorDoneMsg(msg doctorDoneMsg) []tea.Cmd {
	var cmds []tea.Cmd
	m.doctorRunning = false
	preserveStatus := m.doctorPreserveStatus
	m.doctorPreserveStatus = false
	if msg.err != nil {
		m.doctorErr = msg.err.Error()
		if !preserveStatus {
			cmds = append(cmds, setStatus(m, "✗ doctor: "+msg.err.Error(), true))
		}
	} else {
		m.doctorResult = msg.result
		m.doctorErr = ""
		if !preserveStatus {
			if msg.result != nil && msg.result.HasFailures() {
				cmds = append(cmds, setStatus(m, "✗ doctor found failing checks", true))
			} else {
				cmds = append(cmds, setStatus(m, "✓ doctor complete", false))
			}
		}
	}
	if m.doctorRefreshPending {
		m.doctorRefreshPending = false
		m.refreshDoctorAfterFixWithStatus(&cmds, preserveStatus)
	}
	return cmds
}

func (m *Model) handleDangerOpDoneMsg(msg dangerOpDoneMsg) []tea.Cmd {
	var cmds []tea.Cmd
	m.loading = false
	m.setupReloading = false
	if msg.action == "enable-dots" {
		if !m.finishDotsOperation(msg.dotsGen) {
			return cmds
		}
	}
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.action+": "+msg.err.Error(), true))
	} else {
		text := "✓ " + msg.action
		if msg.detail != "" {
			text = "✓ " + msg.detail
		}
		cmds = append(cmds, setStatus(m, text, false))
	}
	if m.mode == viewSetup && msg.err == nil && (msg.action == "setup-dots" || msg.action == "disable-dots") && !msg.setupComplete {
		if msg.action == "disable-dots" {
			m.dotsEntries = nil
			m.dotsGitStatus = ""
			m.dotsLoaded = false
		}
		m.startSetupAgentsStep(&cmds)
		return cmds
	}
	if msg.setupComplete && msg.err == nil {
		m.mode = viewStatus
		m.setupBackgroundMode = viewStatus
		m.setupStep = 0
		m.hostRequired = false
		m.setupComplete = true
	}
	if msg.reload {
		if msg.mode != viewStatus {
			m.setupBackgroundMode = msg.mode
		}
		m.beginLoading(loadingOwnerLocalOp)
		if msg.setupComplete && msg.err == nil {
			cmds = append(cmds, m.beginSetupReload())
			m.progressText = "Loading tools…"
		}
		cmds = append(cmds, m.spinner.Tick, loadTools(m.app, m.ctx))
	} else if len(msg.tools) > 0 {
		m.allTools = msg.tools
		m.applyFilter()
	}
	if msg.action == "disable-dots" && msg.err == nil {
		m.dotsEntries = nil
		m.dotsGitStatus = ""
		m.dotsLoaded = false
	} else if msg.action == "enable-dots" && msg.err == nil {
		m.mode = viewDots
		m.dotsLoaded = false
	}
	return cmds
}

func (m *Model) handleHostGroupChangedMsg(msg hostGroupChangedMsg) []tea.Cmd {
	var cmds []tea.Cmd
	m.loading = false
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
		return cmds
	}
	if msg.info != nil {
		m.hostInfo = msg.info
		if msg.detail != "" && msg.host != "" {
			m.placeHostCursor(msg.host)
		}
	}
	refreshed := false
	if msg.toolMemberships != nil {
		m.toolMemberships = msg.toolMemberships
		refreshed = true
	}
	if msg.groupNames != nil {
		m.groupNames = msg.groupNames
		refreshed = true
	}
	if msg.toolGroups != nil {
		m.toolGroups = msg.toolGroups
		refreshed = true
	} else if msg.info != nil && m.toolMemberships != nil {
		m.toolGroups = app.ToolGroupLabels(m.toolMemberships, msg.info)
		refreshed = true
	}
	statusText := msg.detail
	if statusText == "" {
		statusText = "✓ host " + msg.host + " deleted"
	}
	if msg.group != "" {
		verb := "added to"
		if !msg.added {
			verb = "removed from"
		}
		statusText = "✓ " + msg.group + " " + verb + " host " + msg.host
	} else if m.hostInfo != nil {
		if n := len(m.hostInfo.Hosts); m.hostCursor >= n {
			m.hostCursor = max(n-1, 0)
		}
	}
	if refreshed {
		m.applyFilter()
	}
	cmds = append(cmds, setStatus(m, statusText, false))
	return cmds
}

func (m *Model) placeHostCursor(host string) {
	if m.hostInfo == nil {
		return
	}
	names := make([]string, 0, len(m.hostInfo.Hosts))
	for name := range m.hostInfo.Hosts {
		names = append(names, name)
	}
	sort.Strings(names)
	for i, name := range names {
		if name == host {
			m.hostCursor = i
			return
		}
	}
}

func (m *Model) handleClaimDoneMsg(msg claimDoneMsg) []tea.Cmd {
	var cmds []tea.Cmd
	m.loading = false
	hasRefresh := msg.tools != nil || msg.toolGroups != nil || msg.toolMemberships != nil || msg.groupNames != nil || msg.hostInfo != nil
	discoveredChanged := false
	if msg.err == nil || hasRefresh {
		discoveredChanged = m.removeDiscoveredByName([]string{msg.name})
	}
	refreshed := m.applyClaimRefresh(msg)
	if discoveredChanged && !refreshed {
		m.applyFilter()
	}
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
		return cmds
	}
	cmds = append(cmds, setStatusFor(m, claimSuccessStatus(msg), false, statusDurationErr))
	return cmds
}

func (m *Model) applyClaimRefresh(msg claimDoneMsg) bool {
	changed := false
	if msg.tools != nil {
		m.allTools = msg.tools
		changed = true
	}
	if msg.toolGroups != nil {
		m.toolGroups = msg.toolGroups
		changed = true
	}
	if msg.toolMemberships != nil {
		m.toolMemberships = msg.toolMemberships
		changed = true
	}
	if msg.groupNames != nil {
		m.groupNames = msg.groupNames
		changed = true
	}
	if msg.hostInfo != nil {
		m.hostInfo = msg.hostInfo
		changed = true
	}
	if changed {
		m.applyFilter()
	}
	return changed
}

func (m *Model) handleIgnoreDoneMsg(msg ignoreDoneMsg) []tea.Cmd {
	var cmds []tea.Cmd
	m.loading = false
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
		return cmds
	}
	m.clearRowActionError()
	verb := "ignored"
	if !msg.ignored {
		verb = "un-ignored"
		if msg.hostScope || msg.ignoreLabels == nil {
			delete(m.ignoreSet, msg.name)
		}
	} else {
		if msg.hostScope || msg.ignoreLabels == nil {
			m.ignoreSet[msg.name] = true
		}
	}
	cmds = append(cmds, setStatus(m, "✓ "+msg.name+" "+verb, false))
	if msg.ignoreLabels != nil {
		m.ignoreLabels = msg.ignoreLabels
	}
	if msg.toolIgnoreSet != nil {
		m.toolIgnoreSet = msg.toolIgnoreSet
	}
	if msg.groupIgnoreSet != nil {
		m.groupIgnoreSet = msg.groupIgnoreSet
	}
	if msg.tools != nil {
		m.allTools = msg.tools
	}
	m.applyFilter()
	m.repositionCursorToTool(msg.name)
	return cmds
}

func (m *Model) handleMigrateProviderDoneMsg(msg migrateProviderDoneMsg) []tea.Cmd {
	var cmds []tea.Cmd
	m.finishCancellableAction()
	m.loading = false
	m.migrating = false
	m.clearRowOperation()
	if isContextCanceled(msg.err) {
		cmds = append(cmds, setStatus(m, "cancelled", false))
		return cmds
	}
	if msg.err != nil {
		m.setToolActionError(toolKey(msg.name, msg.toProvider), msg.err.Error())
		cmds = append(cmds, setStatus(m, "✗ "+msg.err.Error(), true))
		return cmds
	}
	m.clearRowActionError()
	cmds = append(cmds, setStatusFor(m, migrateProviderSuccessStatus(msg), false, statusDurationErr))
	if msg.tools != nil {
		m.allTools = msg.tools
		m.applyFilter()
	}
	if msg.nvmManaged != nil {
		m.nvmManaged = msg.nvmManaged
		m.applyFilter()
	}
	if msg.toolProviderPins != nil {
		m.toolProviderPins = msg.toolProviderPins
		m.applyFilter()
	}
	return cmds
}

func claimSuccessStatus(msg claimDoneMsg) string {
	return app.ClaimSuccessStatusText(msg.name, msg.groupName)
}

func migrateProviderSuccessStatus(msg migrateProviderDoneMsg) string {
	if msg.removedFromConfig {
		return "✓ removed " + msg.name + " from omni config (nvm owns the Node runtime)"
	}
	if msg.clearedProviderOverride {
		switch {
		case msg.fromProvider != "" && msg.toProvider != "":
			return "✓ removed provider override for " + msg.name + " and reinstalled with default (" + msg.toProvider + "), removed " + msg.fromProvider
		case msg.toProvider != "":
			return "✓ removed provider override for " + msg.name + " and reinstalled with default (" + msg.toProvider + ")"
		default:
			return "✓ removed provider override for " + msg.name
		}
	}
	switch {
	case msg.fromProvider != "" && msg.toProvider != "":
		return "✓ reinstalled " + msg.name + " with default (" + msg.toProvider + "), removed " + msg.fromProvider
	case msg.toProvider != "":
		return "✓ reinstalled " + msg.name + " with default (" + msg.toProvider + ")"
	default:
		return "✓ reinstalled " + msg.name + " with default provider"
	}
}
