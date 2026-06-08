package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/dots"
)

func (m *Model) handleStatusKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd
	if handled, confirmCmds := m.handleListConfirmationKeyMsg(msg); handled {
		return confirmCmds
	}
	rows := statusRows(*m)
	m.clampStatusCursor(len(rows))

	switch {
	case key.Matches(msg, m.keys.Back):
		m.mode = viewList
		return nil
	case key.Matches(msg, m.keys.Up):
		if n := len(rows); n > 0 {
			m.statusCursor = (m.statusCursor - 1 + n) % n
		}
	case key.Matches(msg, m.keys.Down):
		if n := len(rows); n > 0 {
			m.statusCursor = (m.statusCursor + 1) % n
		}
	case key.Matches(msg, m.keys.Top):
		m.statusCursor = 0
	case key.Matches(msg, m.keys.Bottom):
		if len(rows) > 0 {
			m.statusCursor = len(rows) - 1
		}
	case key.Matches(msg, m.keys.HalfPageDown):
		half := max(listAvailableHeight(*m)/2, 1)
		m.statusCursor = min(m.statusCursor+half, max(len(rows)-1, 0))
	case key.Matches(msg, m.keys.HalfPageUp):
		half := max(listAvailableHeight(*m)/2, 1)
		m.statusCursor = max(m.statusCursor-half, 0)
	case key.Matches(msg, m.keys.PageDown):
		page := max(listAvailableHeight(*m), 1)
		m.statusCursor = min(m.statusCursor+page, max(len(rows)-1, 0))
	case key.Matches(msg, m.keys.PageUp):
		page := max(listAvailableHeight(*m), 1)
		m.statusCursor = max(m.statusCursor-page, 0)
	case key.Matches(msg, m.keys.Refresh):
		if msg.IsRepeat {
			return nil
		}
		m.startDashboardRefresh(&cmds)
	case statusActionKeyMatches(msg, m.keys, selectedStatusAction(rows, m.statusCursor)):
		if msg.IsRepeat {
			return nil
		}
		m.handleStatusAction(selectedStatusAction(rows, m.statusCursor), &cmds)
	case key.Matches(msg, m.keys.Reconcile):
		if msg.IsRepeat {
			return nil
		}
		m.startDashboardReconcile(&cmds)
	default:
		return nil
	}
	return cmds
}

func (m *Model) shouldAutoRunStatusDoctor() bool {
	return !m.loading && !m.dotsLoading && !m.doctorRunning && m.doctorResult == nil && m.doctorErr == ""
}

func (m *Model) startDoctorRun(message string) {
	m.doctorRunning = true
	startOp(m, message)
}

// refreshDoctorAfterFix re-runs doctor so its cached findings (and the action
// hints derived from them, e.g. "commit dotfiles") drop once a dashboard fix
// resolved the underlying issue. The Doctor row reads a doctorResult snapshot,
// unlike the live Dotfiles row, so without this it keeps showing a stale warn.
// No-op when doctor has not been run or is already running.
func (m *Model) refreshDoctorAfterFix(cmds *[]tea.Cmd) {
	if m.doctorResult == nil || m.doctorRunning {
		return
	}
	m.startDoctorRun("Refreshing doctor…")
	*cmds = append(*cmds, m.spinner.Tick, m.doRunDoctor())
}

func (m *Model) startDashboardRefresh(cmds *[]tea.Cmd) {
	*cmds = append(*cmds, m.refreshInstalledProviders()...)
	if !m.doctorRunning {
		m.startDoctorRun("Refreshing dashboard…")
		*cmds = append(*cmds, m.spinner.Tick, m.doRunDoctor())
	}
	if m.app != nil {
		m.dotsServicesRefreshing = true
		*cmds = append(*cmds, m.doRefreshDotsServices(), m.doRefreshDotsHistory())
	}
	if m.dotsSyncConfigured() && !m.dotsLoading && !m.dotsPreparing {
		m.beginDotsOperation("Refreshing dashboard…")
		*cmds = append(*cmds, m.spinner.Tick, m.doLoadDots())
	}
}

func (m *Model) startDashboardReconcile(cmds *[]tea.Cmd) {
	if !statusDashboardReconcileActionable(*m) {
		*cmds = append(*cmds, setStatus(m, "nothing to reconcile", false))
		return
	}
	m.openDashboardReconcilePlan()
}

func (m *Model) handleDashboardReconcilePlanKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd
	items := dashboardReconcilePlanItems(*m)
	m.dashboardReconcilePlanCursor = clampIndex(m.dashboardReconcilePlanCursor, len(items))
	switch {
	case key.Matches(msg, m.keys.Back):
		m.clearDashboardReconcilePlan()
	case key.Matches(msg, m.keys.Up):
		if m.dashboardReconcilePlanCursor > 0 {
			m.dashboardReconcilePlanCursor--
		}
	case key.Matches(msg, m.keys.Down):
		if m.dashboardReconcilePlanCursor < len(items)-1 {
			m.dashboardReconcilePlanCursor++
		}
	case key.Matches(msg, m.keys.Top):
		m.dashboardReconcilePlanCursor = 0
	case key.Matches(msg, m.keys.Bottom):
		if len(items) > 0 {
			m.dashboardReconcilePlanCursor = len(items) - 1
		}
	case key.Matches(msg, m.keys.Toggle):
		m.toggleDashboardReconcilePlanItem(items)
	case key.Matches(msg, m.keys.Confirm):
		if msg.IsRepeat {
			return nil
		}
		m.runDashboardReconcilePlan(items, &cmds)
	}
	return cmds
}

func (m *Model) openDashboardReconcilePlan() {
	items := dashboardReconcilePlanItems(*m)
	if len(items) == 0 {
		return
	}
	m.dashboardReconcilePlanOpen = true
	m.dashboardReconcilePlanCursor = clampIndex(m.dashboardReconcilePlanCursor, len(items))
	m.dashboardReconcilePlanSelected = make(map[dashboardReconcilePlanKind]bool, len(items))
	for _, item := range items {
		m.dashboardReconcilePlanSelected[item.ID] = true
	}
	clearStatus(m)
}

func (m *Model) clearDashboardReconcilePlan() {
	m.dashboardReconcilePlanOpen = false
	m.dashboardReconcilePlanCursor = 0
	m.dashboardReconcilePlanSelected = nil
}

func (m *Model) toggleDashboardReconcilePlanItem(items []dashboardReconcilePlanItem) {
	if len(items) == 0 {
		return
	}
	m.dashboardReconcilePlanCursor = clampIndex(m.dashboardReconcilePlanCursor, len(items))
	item := items[m.dashboardReconcilePlanCursor]
	if m.dashboardReconcilePlanSelected == nil {
		m.dashboardReconcilePlanSelected = make(map[dashboardReconcilePlanKind]bool, len(items))
		for _, item := range items {
			m.dashboardReconcilePlanSelected[item.ID] = true
		}
	}
	m.dashboardReconcilePlanSelected[item.ID] = !m.dashboardReconcilePlanSelected[item.ID]
}

func (m *Model) runDashboardReconcilePlan(items []dashboardReconcilePlanItem, cmds *[]tea.Cmd) {
	if len(items) == 0 {
		m.clearDashboardReconcilePlan()
		*cmds = append(*cmds, setStatus(m, "nothing to reconcile", false))
		return
	}
	queue := make([]dashboardReconcilePlanKind, 0, len(items))
	for _, item := range items {
		if m.dashboardReconcilePlanSelected[item.ID] {
			queue = append(queue, item.ID)
		}
	}
	if len(queue) == 0 {
		*cmds = append(*cmds, setStatus(m, "nothing selected", false))
		return
	}
	m.clearDashboardReconcilePlan()
	m.dashboardReconcileRunning = true
	m.dashboardReconcileCurrent = ""
	m.dashboardReconcileQueue = queue
	m.dashboardReconcileErrors = nil
	m.startNextDashboardReconcileStep(cmds)
}

func (m *Model) startNextDashboardReconcileStep(cmds *[]tea.Cmd) {
	for len(m.dashboardReconcileQueue) > 0 {
		kind := m.dashboardReconcileQueue[0]
		m.dashboardReconcileQueue = m.dashboardReconcileQueue[1:]
		if !m.dashboardReconcileStepActionable(kind) {
			continue
		}
		m.dashboardReconcileCurrent = kind
		before := len(*cmds)
		switch kind {
		case dashboardReconcilePlanSyncTools:
			m.startToolSyncAllConfirmed(cmds)
		case dashboardReconcilePlanUpgradeTools:
			m.startDashboardUpgradeAll(cmds)
		case dashboardReconcilePlanSyncDots:
			m.startDashboardDotsSync(cmds)
		case dashboardReconcilePlanCommitDots:
			m.startDashboardDotsCommit(cmds)
		case dashboardReconcilePlanFixIgnore:
			m.startDashboardFixIgnore(cmds)
		}
		if len(*cmds) > before {
			return
		}
	}
	m.finishDashboardReconcile(cmds)
}

func (m *Model) dashboardReconcileStepActionable(kind dashboardReconcilePlanKind) bool {
	switch kind {
	case dashboardReconcilePlanSyncTools:
		return statusDashboardToolSyncActionable(*m)
	case dashboardReconcilePlanUpgradeTools:
		return statusDashboardUpgradeActionable(*m)
	case dashboardReconcilePlanSyncDots:
		return statusDashboardDotsSyncActionable(*m)
	case dashboardReconcilePlanCommitDots:
		return statusDashboardDotsCommitActionable(*m)
	case dashboardReconcilePlanFixIgnore:
		return statusDashboardFixIgnoreActionable(*m)
	default:
		return false
	}
}

func (m *Model) finishDashboardReconcile(cmds *[]tea.Cmd) {
	m.dashboardReconcileRunning = false
	m.dashboardReconcileCurrent = ""
	m.dashboardReconcileQueue = nil
	if len(m.dashboardReconcileErrors) > 0 {
		msg := "✗ reconcile finished with issue: " + m.dashboardReconcileErrors[0]
		if len(m.dashboardReconcileErrors) > 1 {
			msg = fmt.Sprintf("✗ reconcile finished with %d issues: %s", len(m.dashboardReconcileErrors), m.dashboardReconcileErrors[0])
		}
		m.dashboardReconcileErrors = nil
		*cmds = append(*cmds, setStatus(m, msg, true))
		m.refreshDoctorAfterFix(cmds)
		return
	}
	m.dashboardReconcileErrors = nil
	*cmds = append(*cmds, setStatus(m, "✓ reconciled", false))
	m.refreshDoctorAfterFix(cmds)
}

func (m *Model) continueDashboardReconcile(kind dashboardReconcilePlanKind, err error, cmds *[]tea.Cmd) {
	if !m.dashboardReconcileRunning || m.dashboardReconcileCurrent != kind {
		return
	}
	if isContextCanceled(err) {
		m.cancelDashboardReconcile()
		return
	}
	if err != nil {
		m.dashboardReconcileErrors = append(m.dashboardReconcileErrors, err.Error())
	}
	m.startNextDashboardReconcileStep(cmds)
}

func (m *Model) cancelDashboardReconcile() {
	m.dashboardReconcileRunning = false
	m.dashboardReconcileCurrent = ""
	m.dashboardReconcileQueue = nil
	m.dashboardReconcileErrors = nil
}

func (m *Model) startDashboardDotsSync(cmds *[]tea.Cmd) {
	availability := m.dotsSyncAvailability()
	switch {
	case m.dotsLoading:
		return
	case !availability.Configured:
		*cmds = append(*cmds, setStatus(m, dashboardDotsUnavailableMessage("syncing", availability), true))
		return
	}
	m.beginDotsOperation("Syncing dots…")
	total := m.markDotsPendingSyncAll()
	setActivityStatus(m, app.DotsSyncActivityProgressText(dots.SyncProgressEvent{Total: total}))
	order := dotsSyncAllEntryOrder(*m)
	ch := m.beginDotsProgressStream()
	*cmds = append(*cmds, m.spinner.Tick, m.doDotsSyncOnlyWithProgress(ch, order), waitForDotsProgress(ch, m.dotsOpGen))
}

func (m *Model) startDashboardDotsCommit(cmds *[]tea.Cmd) {
	availability := m.dotsSyncAvailability()
	switch {
	case m.dotsLoading:
		return
	case !availability.Configured:
		*cmds = append(*cmds, setStatus(m, dashboardDotsUnavailableMessage("committing", availability), true))
		return
	case strings.TrimSpace(m.dotsGitStatus) == "":
		return
	}
	m.beginDotsOperation("Committing dots…")
	*cmds = append(*cmds, m.spinner.Tick, m.doDotsCommit())
}

func dashboardDotsUnavailableMessage(action string, availability app.DotsSyncAvailability) string {
	switch availability.Reason {
	case app.DotsSyncAvailabilityDisabled:
		return "dotfile sync is disabled for this host"
	case app.DotsSyncAvailabilityNoRepo:
		return fmt.Sprintf("set dots_repo before %s dotfiles", action)
	default:
		return "dotfile sync is not configured for this host"
	}
}

func (m *Model) startDashboardFixIgnore(cmds *[]tea.Cmd) {
	if !doctorHasIgnoreFindings(*m) {
		return
	}
	a := m.app
	*cmds = append(*cmds, func() tea.Msg {
		modified, err := a.DotsFixIgnorePatterns()
		return fixIgnoreDoneMsg{modified: modified, err: err}
	})
}

func (m *Model) startDashboardUpgradeAll(cmds *[]tea.Cmd) {
	if len(m.upgradingKeys) > 0 || statusToolCounts(*m).Updates == 0 {
		return
	}
	if m.upgradingKeys == nil {
		m.upgradingKeys = make(map[string]bool)
	}
	m.upgradingKeys["*"] = true
	m.loading = true
	m.progressText = ""
	ch, gen := m.beginProgressStream()
	m.markBulkPendingUpdates()
	*cmds = append(*cmds, m.spinner.Tick, m.doUpgradeAll(ch, gen), waitForProgress(ch, gen))
}

func (m *Model) handleDotsServicesStatusMsg(msg dotsServicesStatusMsg) {
	m.dotsServicesRefreshing = false
	m.dotsReminderService = msg.reminder
	m.dotsReminderServiceErr = msg.reminderErr
	m.dotsReminderInterval = app.DotsReminderInterval(msg.reminder)
	m.dotsWatchService = msg.watch
	m.dotsWatchServiceErr = msg.watchErr
	m.dotsWatchDebounce = app.DotsWatchDebounce(msg.watch)
	m.dotsWatchDebounceNext = 0
}

func (m *Model) doRefreshDotsServices() tea.Cmd {
	a := m.app
	return func() tea.Msg {
		reminder, reminderErr := a.DotsReminderServiceStatus()
		watch, watchErr := a.DotsWatchServiceStatus()
		return dotsServicesStatusMsg{
			reminder:    reminder,
			reminderErr: errorString(reminderErr),
			watch:       watch,
			watchErr:    errorString(watchErr),
		}
	}
}

func (m *Model) clampStatusCursor(rowCount int) {
	m.statusCursor = clampIndex(m.statusCursor, rowCount)
}

func (m *Model) scrollStatusBy(delta int) {
	if delta == 0 {
		return
	}
	rows := statusRows(*m)
	m.statusCursor = clampIndex(m.statusCursor+delta, len(rows))
}

func (m *Model) handleStatusAction(action statusAction, cmds *[]tea.Cmd) {
	switch action.kind {
	case statusActionRunDoctor:
		if m.doctorRunning {
			return
		}
		m.startDoctorRun("Running doctor…")
		*cmds = append(*cmds, m.spinner.Tick, m.doRunDoctor())
	case statusActionOpenTools:
		m.openStatusTools(false, sectionAvailable)
	case statusActionOpenToolsSection:
		m.openStatusTools(true, action.toolSection)
	case statusActionOpenDots:
		m.openStatusDots(false, cmds)
	case statusActionOpenDotsIssue:
		m.openStatusDots(true, cmds)
	case statusActionOpenSettings:
		m.openStatusSettings(action.settingsRow, cmds)
	case statusActionSyncTools:
		if !m.loading && statusDashboardToolSyncActionable(*m) {
			m.startToolSyncAllConfirmed(cmds)
		}
	case statusActionSyncDots:
		m.startDashboardDotsSync(cmds)
	case statusActionCommitDots:
		m.startDashboardDotsCommit(cmds)
	case statusActionUpgradeTools:
		m.startDashboardUpgradeAll(cmds)
	case statusActionFixIgnore:
		m.startDashboardFixIgnore(cmds)
	}
}

func (m *Model) handleFixIgnoreDoneMsg(msg fixIgnoreDoneMsg) []tea.Cmd {
	var cmds []tea.Cmd
	if msg.err != nil {
		cmds = append(cmds, setStatus(m, "fix ignore patterns: "+msg.err.Error(), true))
		m.continueDashboardReconcile(dashboardReconcilePlanFixIgnore, msg.err, &cmds)
		return cmds
	}
	if len(msg.modified) > 0 {
		cmds = append(cmds, setStatus(m, "✓ fixed ignore patterns for "+strings.Join(msg.modified, ", "), false))
		// Re-run doctor to refresh findings.
		m.startDoctorRun("Refreshing doctor…")
		cmds = append(cmds, m.spinner.Tick, m.doRunDoctor())
	}
	m.continueDashboardReconcile(dashboardReconcilePlanFixIgnore, nil, &cmds)
	return cmds
}

func selectedStatusAction(rows []statusListRow, cursor int) statusAction {
	if len(rows) == 0 || cursor < 0 || cursor >= len(rows) {
		return statusAction{}
	}
	return rows[cursor].action
}

func (m *Model) openStatusTools(clearFilters bool, target section) {
	m.cancelConfirmationForGlobalNavigation()
	m.mode = viewList
	if clearFilters {
		m.clearToolFiltersAndSearch()
	} else {
		m.applyFilter()
	}
	if target != sectionAvailable || clearFilters {
		m.selectFirstToolSection(target)
	}
}

func (m *Model) selectFirstToolSection(target section) {
	for i, tool := range m.visibleTools {
		if m.displaySection(tool) == target {
			m.cursor = i
			return
		}
	}
	m.clampToolCursor()
}

func (m *Model) openStatusDots(selectIssue bool, cmds *[]tea.Cmd) {
	m.switchMainTab(viewDots, cmds)
	if selectIssue {
		m.selectFirstDotsIssue()
	}
}

func (m *Model) selectFirstDotsIssue() {
	visible := dotsVisibleRows(*m)
	for i, row := range visible {
		if row.isChild {
			continue
		}
		if app.DotStatusNeedsAttention(row.entry) {
			m.dotsCursor = i
			m.syncDotsExpandedName(visible)
			return
		}
	}
	m.dotsCursor = clampIndex(m.dotsCursor, len(visible))
	m.syncDotsExpandedName(visible)
}

func (m *Model) openStatusSettings(row int, cmds *[]tea.Cmd) {
	m.switchMainTab(viewSettings, cmds)
	m.setSettingsCursor(row)
}
