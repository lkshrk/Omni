package tui

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/app"
	textutil "github.com/lkshrk/omni/internal/text"
)

func statusToolCounts(m Model) app.DashboardToolSummary {
	return app.BuildDashboardToolSummary(app.DashboardToolSummaryInput{
		Tools:                  m.allTools,
		DiscoveredTools:        m.discoveredTools,
		IgnoredTools:           dashboardIgnoredTools(m),
		ToolProviderPins:       m.toolProviderPins,
		EffectiveSystemManager: m.effectiveSystemManager,
		EffectivePythonManager: m.effectivePythonManager,
		EffectiveNodeManager:   m.effectiveNodeManager,
		NvmManaged:             m.nvmManaged,
	})
}

func statusCountValue(m Model, count int, singular, plural, empty string) string {
	if count == 0 {
		return m.palette.styleInstalled.Render(empty)
	}
	return m.palette.styleOutdated.Render(textutil.PluralCount(count, singular, plural))
}

func statusLoadingValue(m Model, label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		label = "loading"
	}
	return m.palette.styleStatus.Render(iconPending + " " + label)
}

func statusStaleSummary(activity, fallback string, hasData bool) string {
	activity = strings.TrimSpace(activity)
	if activity == "" {
		return fallback
	}
	fallback = strings.TrimSpace(fallback)
	if hasData && fallback != "" {
		return activity + " · " + fallback
	}
	return activity
}

// keepValue preserves the row's real value instead of a loading placeholder while active.
type statusRowActivity struct {
	text      string
	hasData   bool
	working   bool
	keepValue bool
}

func applyStatusRowActivity(m Model, value, summary, icon string, iconStyle lipgloss.Style, a statusRowActivity) (string, string, string, lipgloss.Style) {
	if a.text == "" {
		return value, summary, icon, iconStyle
	}
	summary = statusStaleSummary(a.text, summary, a.hasData)
	if !a.keepValue {
		value = statusLoadingValue(m, "loading")
	}
	if a.working {
		icon, iconStyle = statusRowWorkingIcon(m, true)
	}
	return value, summary, icon, iconStyle
}

func applyDashboardRefreshPresentation(m Model, rows []statusListRow) []statusListRow {
	for i := range rows {
		state := dashboardRefreshStateForRow(m, rows[i].label)
		switch state {
		case dashboardRowWaiting:
			rows[i].icon = iconPending
			rows[i].iconStyle = m.palette.styleStatus
			rows[i].value = m.palette.styleHelp.Render("-")
			rows[i].details = []string{statusDetailLine(m, "waiting...")}
		case dashboardRowRefreshing:
			rows[i].icon, rows[i].iconStyle = statusRowWorkingIcon(m, true)
			rows[i].value = m.palette.styleStatus.Render(iconPending)
			text := strings.TrimSpace(m.progressText)
			if text == "" {
				text = "loading..."
			}
			rows[i].details = []string{statusDetailLine(m, text)}
		}
	}
	return rows
}

type dashboardRowRefreshState uint8

const (
	dashboardRowReady dashboardRowRefreshState = iota
	dashboardRowWaiting
	dashboardRowRefreshing
)

func dashboardRefreshStateForRow(m Model, label string) dashboardRowRefreshState {
	if m.loading && len(m.allTools) == 0 {
		if label == "Doctor" && m.doctorRunning {
			return dashboardRowRefreshing
		}
		return dashboardRowWaiting
	}
	active := false
	switch label {
	case "Tool Sync", "Tools":
		active = statusToolsLoading(m)
	case "Tool Updates":
		// The Tool Updates row renders its own upgrade progress in statusToolUpdatesAttentionRow; the generic refresh presentation must not clobber it with a bare "loading…".
		active = false
	case "Dotfiles":
		active = m.dotsLoading || m.dotsPreparing
	case "Agents":
		active = statusAgentsLoading(m)
	case "Services":
		active = m.dotsServicesRefreshing
	case "Doctor":
		active = m.doctorRunning
	}
	if active {
		return dashboardRowRefreshing
	}
	return dashboardRowReady
}

func statusToolsLoading(m Model) bool {
	return m.loading ||
		len(m.upgradingKeys) > 0 ||
		len(m.scanningProviders) > 0 ||
		m.providerSnapshotRefreshing ||
		m.discoveryRefreshing ||
		m.descRefreshing
}

// statusToolsLoading covers foreground mutations and the main refresh phases; outdated checks are separate background snapshot owners.
func statusReconcileToolPlanBusy(m Model) bool {
	return statusToolsLoading(m) ||
		len(m.outdatedProviders) > 0 ||
		m.outdatedSnapshotRefreshing
}

type agentsDashCounts struct {
	outOfSync int
	outdated  int
}

func (c agentsDashCounts) OutOfSync() int { return c.outOfSync }
func (c agentsDashCounts) Outdated() int  { return c.outdated }

type agentsDashboardView struct {
	enabled      bool
	managedCount int
}

func (v agentsDashboardView) managed() int { return v.managedCount }
func agentsDashboardEnabled(Model) bool    { return true }
func agentsDashboardViewFor(m Model) agentsDashboardView {
	return agentsDashboardView{enabled: true, managedCount: len(m.agentsRows) + len(m.agentsMCPRows) + len(m.agentsLSPRows)}
}
func statusAgentsCounts(m Model) agentsDashCounts {
	counts := agentsDashCounts{}
	for _, row := range m.agentsRows {
		if agentsStatusNeedsSync(row.Status) {
			counts.outOfSync++
		}
		if row.UpdateAvailable {
			counts.outdated++
		}
	}
	for _, rows := range [][]app.AgentsServiceRow{m.agentsMCPRows, m.agentsLSPRows} {
		for _, row := range rows {
			if agentsStatusNeedsSync(row.Status) {
				counts.outOfSync++
			}
		}
	}
	return counts
}
func agentsStatusNeedsSync(status app.AgentsPackageStatus) bool {
	return status == app.AgentsPackageMissing || status == app.AgentsPackageDrifted || status == app.AgentsPackageUnavailable
}
func statusAgentsOutdatedNames(m Model) []string {
	var names []string
	for _, row := range m.agentsRows {
		if row.UpdateAvailable {
			names = append(names, row.Name)
		}
	}
	return names
}
func statusAgentsAttentionSummary(Model, agentsDashCounts) string { return "APM workspace" }
func statusAgentsAttentionDetails(m Model, _ agentsDashCounts, _ agentsDashboardView) []string {
	if m.apmErr != nil {
		return []string{statusDetailLine(m, m.apmErr.Error())}
	}
	if output := strings.TrimSpace(m.apmOutput); output != "" {
		return []string{statusDetailLine(m, output)}
	}
	return []string{statusDetailLine(m, "~/.apm/apm.yml")}
}
func statusAgentsLoading(m Model) bool { return m.apmRunning }

func statusToolsActivityText(m Model) string {
	if !statusToolsLoading(m) {
		return ""
	}
	if text := strings.TrimSpace(m.progressText); text != "" && !m.dotsLoading {
		return text
	}
	if len(m.upgradingKeys) > 0 {
		return "Upgrading tools…"
	}
	if m.loading {
		return "Syncing tools…"
	}
	return activityLabel(m)
}

func statusHealthOKValue(m Model) string {
	return m.palette.styleInstalled.Render("[ok]")
}

func statusToolsOverviewDataValue(m Model, counts app.DashboardToolSummary) string {
	if counts.Tracked == 0 {
		return m.palette.styleHelp.Render("0 tracked")
	}
	return m.palette.styleProvider.Render(textutil.PluralCount(counts.Tracked, "tracked", "tracked"))
}

func statusToolsOverviewValue(m Model, counts app.DashboardToolSummary) string {
	if counts.Tracked == 0 {
		return m.palette.styleHelp.Render("no tools")
	}
	if counts.Updates > 0 {
		return m.palette.styleOutdated.Render(textutil.PluralCount(counts.Updates, "update", "updates"))
	}
	return m.palette.styleProvider.Render(textutil.PluralCount(counts.Tracked, "tracked", "tracked"))
}

func statusToolsOverviewBreakdown(counts app.DashboardToolSummary) string {
	return fmt.Sprintf("%d installed · %d available · %d ignored", counts.Installed, counts.Available, counts.Ignored)
}

func statusToolsOverviewDetails(m Model, counts app.DashboardToolSummary) []string {
	details := []string{
		statusDetailLine(m, textutil.PluralCount(counts.Tracked, "tracked tool", "tracked tools")),
		statusDetailLine(m, textutil.PluralCount(counts.Installed, "installed locally", "installed locally")),
	}
	if counts.Available > 0 {
		details = append(details, statusDetailLine(m, textutil.PluralCount(counts.Available, "available tool", "available tools")))
	}
	if counts.Updates > 0 {
		details = append(details, statusDetailLine(m, textutil.PluralCount(counts.Updates, "pending update", "pending updates")))
	}
	if counts.OutOfSync > 0 {
		details = append(details, statusDetailLine(m, textutil.PluralCount(counts.OutOfSync, "sync issue", "sync issues")))
	}
	return details
}

func statusToolSyncDetails(m Model, counts app.DashboardToolSummary) []string {
	var details []string
	if statusDashboardToolSyncBusy(m) {
		details = append(details, statusActivityDetailLine(m, statusToolsActivityText(m), true))
		if len(m.bulkPendingKeys) > 0 {
			details = append(details, statusDetailLine(m, "queued: "+statusInlineNames(statusToolSyncQueuedNames(m), 5)))
		}
	}
	if counts.OutOfSync == 0 {
		if len(details) == 0 {
			return statusDetailLines(m, "All tracked tools match this host.")
		}
		return details
	}
	details = append(details, statusDetailLine(m, "Issues: "+statusInlineNames(counts.OutOfSyncNames, 5)))
	if len(counts.OutOfSyncNames) > 5 {
		details = append(details, statusDetailLine(m, fmt.Sprintf("+%d more", len(counts.OutOfSyncNames)-5)))
	}
	return details
}

func statusToolSyncQueuedNames(m Model) []string {
	return app.DashboardToolSyncQueuedNames(dashboardToolActivityInput(m))
}

func statusUpgradeNames(m Model) ([]string, []string) {
	return app.DashboardUpgradeNames(dashboardToolActivityInput(m))
}

func dashboardToolActivityInput(m Model) app.DashboardToolActivityInput {
	return app.DashboardToolActivityInput{
		Tools:                  m.allTools,
		DiscoveredTools:        m.discoveredTools,
		IgnoredTools:           dashboardIgnoredTools(m),
		PendingKeys:            m.bulkPendingKeys,
		ActiveKeys:             m.upgradingKeys,
		RowKey:                 m.rowOpKey,
		Loading:                m.loading,
		UpgradeBusy:            len(m.upgradingKeys) > 0,
		ProgressText:           m.progressText,
		ToolProviderPins:       m.toolProviderPins,
		EffectiveSystemManager: m.effectiveSystemManager,
		EffectivePythonManager: m.effectivePythonManager,
		EffectiveNodeManager:   m.effectiveNodeManager,
		NvmManaged:             m.nvmManaged,
	}
}

func statusDashboardNvmManagedActionable(m Model) bool {
	return app.DashboardReconcilePlanHasStep(dashboardReconcilePlanItems(m), app.ReconcileStepFixNvmManaged)
}

func statusUpgradeSummary(active, waiting, updates []string) string {
	if len(active) > 0 {
		name := strings.Join(limitedNames(active, 2), ", ")
		if len(waiting) > 0 {
			return fmt.Sprintf("upgrading %s, %d queued", name, len(waiting))
		}
		return "upgrading " + name
	}
	if len(waiting) > 0 {
		return fmt.Sprintf("%d queued to upgrade", len(waiting))
	}
	if len(updates) > 0 {
		return "upgrading outdated tools"
	}
	return "upgrading tools"
}

func statusUpgradeValue(m Model, active, waiting []string) string {
	switch {
	case len(active) > 0:
		return m.palette.styleStatus.Render(fmt.Sprintf("%s %d active / %d queued", iconPending, len(active), len(waiting)))
	case len(waiting) > 0:
		return m.palette.styleStatus.Render(iconPending + " " + textutil.PluralCount(len(waiting), "queued", "queued"))
	default:
		return m.palette.styleStatus.Render(iconPending + " updating")
	}
}

func statusUpgradeDetails(m Model, active, waiting, updates []string) []string {
	var details []string
	if len(active) > 0 || len(waiting) > 0 {
		details = append(details, statusActivityDetailLine(m, statusToolsActivityText(m), true))
	}
	if len(active) > 0 {
		details = append(details, statusDetailLine(m, "active: "+statusInlineNames(active, 3)))
	}
	if len(waiting) > 0 {
		details = append(details, statusDetailLine(m, "queued: "+statusInlineNames(waiting, 5)))
	}
	if len(details) == 0 && len(updates) > 0 {
		details = append(details, statusDetailLine(m, "queued: "+statusInlineNames(updates, 5)))
	}
	return details
}

func statusInlineNames(names []string, limit int) string {
	limited := limitedNames(names, limit)
	text := strings.Join(limited, ", ")
	if len(names) > len(limited) {
		text += fmt.Sprintf(", +%d more", len(names)-len(limited))
	}
	return text
}

func statusOverflowDetails(m Model, names []string) []string {
	if len(names) <= 3 {
		return nil
	}
	overflow := limitedNames(names[3:], 5)
	details := []string{statusDetailLine(m, "More: "+strings.Join(overflow, ", "))}
	if extra := len(names) - 3 - len(overflow); extra > 0 {
		details = append(details, statusDetailLine(m, fmt.Sprintf("+%d more", extra)))
	}
	return details
}

func statusSampleSummary(names []string, empty string) string {
	if len(names) == 0 {
		return empty
	}
	limited := limitedNames(names, 3)
	summary := strings.Join(limited, ", ")
	if len(names) > len(limited) {
		summary += fmt.Sprintf(", +%d more", len(names)-len(limited))
	}
	return summary
}

func limitedNames(names []string, limit int) []string {
	if len(names) <= limit {
		return names
	}
	return names[:limit]
}

func truncatedGitStatus(status string, maxLines int) (lines []string, overflow string) {
	for _, line := range strings.Split(status, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) > maxLines {
		overflow = fmt.Sprintf("+%d more repo change(s)", len(lines)-maxLines)
		lines = lines[:maxLines]
	}
	return
}

func statusDotfilesNotLoaded(m Model, counts app.DotFileCounts) bool {
	if m.dotsLoading || m.dotsPreparing {
		return false
	}
	return counts.Synced == 0 && counts.OutOfSync == 0 && counts.Ignored == 0
}

func statusDotsActivityText(m Model) string {
	switch {
	case m.dotsPreparing:
		return "Loading dotfiles…"
	case m.dotsLoading:
		if text := strings.TrimSpace(m.progressText); text != "" {
			return text
		}
		return "Syncing dotfiles…"
	default:
		return ""
	}
}

func statusDashboardDotsSyncActionable(m Model) bool {
	return statusDashboardPlanHasStep(m, app.ReconcileStepSyncDots)
}

func statusDashboardToolSyncActionable(m Model) bool {
	return statusDashboardPlanHasStep(m, app.ReconcileStepSyncTools)
}

func statusDashboardToolSyncBusy(m Model) bool {
	return app.DashboardToolSyncBusy(dashboardToolActivityInput(m))
}

func statusDashboardUpgradeActionable(m Model) bool {
	return statusDashboardPlanHasStep(m, app.ReconcileStepUpgradeTools)
}

func statusDashboardDotsCommitActionable(m Model) bool {
	return statusDashboardPlanHasStep(m, app.ReconcileStepCommitDots)
}

func statusDashboardReconcileActionable(m Model) bool {
	return len(dashboardReconcilePlanItems(m)) > 0
}

func statusDashboardFixIgnoreActionable(m Model) bool {
	return statusDashboardPlanHasStep(m, app.ReconcileStepFixIgnore)
}

func statusDashboardPlanHasStep(m Model, id app.DashboardReconcileStepID) bool {
	return app.DashboardReconcilePlanHasStep(dashboardReconcilePlanItems(m), id)
}

func doctorHasIgnoreFindings(m Model) bool {
	_, ok := app.DotsIgnoreFinding(m.doctorResult)
	return ok
}

func dashboardReconcilePlanInput(m Model) app.DashboardReconcilePlanInput {
	return app.DashboardReconcilePlanInput{
		Tools:           m.allTools,
		DiscoveredTools: m.discoveredTools,
		IgnoredTools:    dashboardIgnoredTools(m),
		ToolsBusy:       statusReconcileToolPlanBusy(m),
		UpgradeBusy:     len(m.upgradingKeys) > 0,
		AgentsOutOfSync: statusAgentsCounts(m).OutOfSync(),
		AgentsBusy:      m.apmRunning,
		DotsConfigured:  m.dotsSyncAvailCached.Configured,
		DotsDisabled:    m.dotsSyncAvailCached.Reason == app.DotsSyncAvailabilityDisabled,
		DotsBusy:        m.dotsLoading || m.dotsPreparing,
		DotsEntries:     m.dotsEntries,
		DotsGitStatus:   m.dotsGitStatus,
		Doctor:          m.doctorResult,
		NvmManaged:      m.nvmManaged,
	}
}

func dashboardIgnoredTools(m Model) map[string]bool {
	if len(m.ignoreSet) == 0 && len(m.ignoreLabels) == 0 {
		return nil
	}
	ignored := make(map[string]bool, len(m.ignoreSet)+len(m.ignoreLabels))
	for name, ok := range m.ignoreSet {
		if ok {
			ignored[name] = true
		}
	}
	for name, label := range m.ignoreLabels {
		if strings.TrimSpace(label) != "" {
			ignored[name] = true
		}
	}
	return ignored
}

func dotsViewAvailability(m Model) app.DotsSyncAvailability {
	availability := m.dotsSyncAvailCached
	switch availability.Reason {
	case app.DotsSyncAvailabilityReady, app.DotsSyncAvailabilityNoRepo, app.DotsSyncAvailabilityDisabled, app.DotsSyncAvailabilityUnknown:
		return availability
	default:
		return app.DotsSyncAvailability{Reason: app.DotsSyncAvailabilityNoRepo}
	}
}

func dotsViewDisabled(m Model) bool {
	return dotsViewAvailability(m).Reason == app.DotsSyncAvailabilityDisabled
}

func dotsViewUnconfigured(m Model) bool {
	return dotsViewAvailability(m).Reason == app.DotsSyncAvailabilityNoRepo
}

func dotsViewBlocked(m Model) bool {
	reason := dotsViewAvailability(m).Reason
	return reason == app.DotsSyncAvailabilityNoRepo || reason == app.DotsSyncAvailabilityDisabled
}

func statusDotfilesOverviewDataValue(m Model, counts app.DotFileCounts) string {
	switch {
	case dotsViewDisabled(m), dotsViewUnconfigured(m), statusDotfilesNotLoaded(m, counts):
		return m.palette.styleHelp.Render("—")
	default:
		total := counts.Synced + counts.OutOfSync + counts.Ignored
		if total == 0 {
			return m.palette.styleHelp.Render("0 entries")
		}
		return m.palette.styleProvider.Render(textutil.PluralCount(total, "entry", "entries"))
	}
}

func statusAutomationOverviewDataValue(m Model) string {
	status := dashboardDotsAutomationStatus(m)
	return m.palette.styleProvider.Render(fmt.Sprintf("%d/2", status.Installed))
}

func statusAgentsOverviewDataValue(m Model, view agentsDashboardView) string {
	return m.palette.styleProvider.Render("APM")
}

func statusDotOverviewSummary(m Model, counts app.DotFileCounts) string {
	switch {
	case dotsViewDisabled(m):
		return "Dotfile sync disabled for this host."
	case dotsViewUnconfigured(m):
		return "Set dots_repo to use dotfiles."
	default:
		return statusDotOverviewBreakdown(counts)
	}
}

func statusDotOverviewBreakdown(counts app.DotFileCounts) string {
	parts := []string{textutil.PluralCount(counts.Synced, "synced", "synced")}
	if counts.OutOfSync > 0 {
		parts = append(parts, textutil.PluralCount(counts.OutOfSync, "out-of-sync", "out-of-sync"))
	}
	parts = append(parts, textutil.PluralCount(counts.Ignored, "ignored", "ignored"))
	return strings.Join(parts, " · ")
}

func statusDotOverviewDetails(m Model, counts app.DotFileCounts) []string {
	switch {
	case dotsViewDisabled(m):
		return statusDetailLines(m, "Dotfile sync disabled for this host.")
	case dotsViewUnconfigured(m):
		return statusDetailLines(m, "Set dots_repo to use dotfiles.")
	default:
		return statusDotDetails(m, counts)
	}
}

func statusDotDetails(m Model, counts app.DotFileCounts) []string {
	var details []string
	if activity := statusDotsActivityText(m); activity != "" {
		details = append(details, statusActivityDetailLine(m, activity, true))
		active, queued := statusDotsActiveQueuedNames(m)
		if len(active) > 0 {
			details = append(details, statusDetailLine(m, "active: "+statusInlineNames(active, 3)))
		}
		if len(queued) > 0 {
			details = append(details, statusDetailLine(m, "queued: "+statusInlineNames(queued, 5)))
		}
	}
	if strings.TrimSpace(m.dotsGitStatus) != "" {
		lines, overflow := truncatedGitStatus(m.dotsGitStatus, 3)
		if len(lines) > 0 {
			details = append(details, statusDetailLine(m, "changed: "+lines[0]))
			for _, line := range lines[1:] {
				details = append(details, statusDetailLine(m, line))
			}
		}
		if overflow != "" {
			details = append(details, statusDetailLine(m, overflow))
		}
	}
	details = append(details, statusDotsHistoryDetails(m)...)
	if repo := strings.TrimSpace(dotsRepoPathForView(m)); repo != "" {
		details = append(details, statusDetailLine(m, "repo "+repo))
	}
	if len(details) == 0 {
		details = append(details, statusDetailLine(m, "No dotfile entries loaded."))
	}
	return details
}

func statusDotsHistoryDetails(m Model) []string {
	if errText := strings.TrimSpace(m.dotsHistoryErr); errText != "" {
		return statusDetailLines(m, "history unavailable: "+errText)
	}
	if len(m.dotsHistory) == 0 {
		return nil
	}
	return statusDetailLines(m, dotsHistoryDashboardLine(m.dotsHistory[0]))
}

func statusDotsActiveQueuedNames(m Model) ([]string, []string) {
	active := []string(nil)
	if strings.TrimSpace(m.dotsActiveName) != "" {
		active = append(active, m.dotsActiveName)
	}
	queued := make([]string, 0, len(m.dotsPendingNames))
	for name := range m.dotsPendingNames {
		if name == m.dotsActiveName {
			continue
		}
		queued = append(queued, name)
	}
	sort.Strings(queued)
	return active, queued
}

// Omits the managed/ignored counts the overview row already shows.
func statusDotAttentionSummary(counts app.DotFileCounts, gitStatus string) string {
	var parts []string
	if counts.OutOfSync > 0 {
		parts = append(parts, textutil.PluralCount(counts.OutOfSync, "out-of-sync entry", "out-of-sync entries"))
	}
	if strings.TrimSpace(gitStatus) != "" {
		parts = append(parts, "repo dirty")
	}
	if len(parts) == 0 {
		return "Dotfiles need attention."
	}
	return strings.Join(parts, ", ") + "."
}

// Omits managed/ignored counts, shown in the overview row below.
func statusDotAttentionDetails(m Model, counts app.DotFileCounts) []string {
	var details []string
	if activity := statusDotsActivityText(m); activity != "" {
		details = append(details, statusActivityDetailLine(m, activity, true))
		active, queued := statusDotsActiveQueuedNames(m)
		if len(active) > 0 {
			details = append(details, statusDetailLine(m, "active: "+statusInlineNames(active, 3)))
		}
		if len(queued) > 0 {
			details = append(details, statusDetailLine(m, "queued: "+statusInlineNames(queued, 5)))
		}
	}
	if counts.OutOfSync > 0 {
		details = append(details, statusDetailLine(m, textutil.PluralCount(counts.OutOfSync, "entry out of sync", "entries out of sync")))
	}
	if strings.TrimSpace(m.dotsGitStatus) != "" {
		lines, overflow := truncatedGitStatus(m.dotsGitStatus, 3)
		if len(lines) > 0 {
			details = append(details, statusDetailLine(m, "changed: "+lines[0]))
			for _, line := range lines[1:] {
				details = append(details, statusDetailLine(m, line))
			}
		}
		if overflow != "" {
			details = append(details, statusDetailLine(m, overflow))
		}
	}
	details = append(details, statusDotsHistoryDetails(m)...)
	if len(details) == 0 {
		details = append(details, statusDetailLine(m, "Dotfiles need attention."))
	}
	return details
}

func statusAutomationNeedsAttention(m Model) bool {
	return dashboardDotsAutomationStatus(m).NeedsAttention
}

func statusAnyAutomationInstalled(m Model) bool {
	return dashboardDotsAutomationStatus(m).Installed > 0
}

func statusAutomationValue(m Model) string {
	status := dashboardDotsAutomationStatus(m)
	if status.HasError {
		return m.palette.styleOutdated.Render("[warn]")
	}
	if status.Blocked {
		return m.palette.styleOutdated.Render("[blocked]")
	}
	text := fmt.Sprintf("[%d/2 ON]", status.Installed)
	switch status.Installed {
	case 2:
		return m.palette.styleInstalled.Render(text)
	case 1:
		return m.palette.styleProvider.Render(text)
	default:
		return m.palette.styleHelp.Render(text)
	}
}

func statusAutomationSummary(m Model) string {
	parts := []string{statusReminderAutomationSummary(m), statusWatchAutomationSummary(m)}
	return strings.Join(parts, " · ")
}

func statusReminderAutomationSummary(m Model) string {
	if errText := strings.TrimSpace(m.dotsReminderServiceErr); errText != "" {
		return "Reminder [WARN] " + errText
	}
	service := m.dotsReminderService
	if service == nil || !service.Installed {
		return "Reminder [OFF]"
	}
	return fmt.Sprintf("Reminder [ON] %s notify %s", formatSettingsDuration(app.DotsReminderInterval(service)), onOffText(service.Notify))
}

func statusWatchAutomationSummary(m Model) string {
	if errText := strings.TrimSpace(m.dotsWatchServiceErr); errText != "" {
		return "Watch [WARN] " + errText
	}
	service := m.dotsWatchService
	if service == nil || !service.Installed {
		return "Watch [OFF]"
	}
	return "Watch [ON] debounce " + formatSettingsDuration(app.DotsWatchDebounce(service))
}

func statusAutomationBreakdown(m Model) string {
	reminder := "reminder off"
	if errText := strings.TrimSpace(m.dotsReminderServiceErr); errText != "" {
		reminder = "reminder warn"
	} else if service := m.dotsReminderService; service != nil && service.Installed {
		reminder = "reminder " + formatSettingsDuration(app.DotsReminderInterval(service))
	}
	watch := "watch off"
	if errText := strings.TrimSpace(m.dotsWatchServiceErr); errText != "" {
		watch = "watch warn"
	} else if service := m.dotsWatchService; service != nil && service.Installed {
		watch = "watch " + formatSettingsDuration(app.DotsWatchDebounce(service))
	}
	return reminder + "/" + watch
}

func statusAutomationDetails(m Model) []string {
	var details []string
	if m.dotsServicesRefreshing {
		details = append(details, statusActivityDetailLine(m, "Refreshing service status…", false))
	}
	details = append(details, statusDetailLines(m,
		statusReminderAutomationDetail(m),
		statusWatchAutomationDetail(m),
	)...)
	details = append(details, statusDotsServiceReadinessWarnings(m)...)
	return details
}

func statusReminderAutomationDetail(m Model) string {
	if errText := strings.TrimSpace(m.dotsReminderServiceErr); errText != "" {
		return "Reminder [WARN] status unavailable: " + errText
	}
	service := m.dotsReminderService
	if service == nil || !service.Installed {
		return "Reminder [OFF]"
	}
	return fmt.Sprintf("Reminder [ON] %s every %s notify %s",
		statusServicePlatform(service.Platform),
		formatSettingsDuration(app.DotsReminderInterval(service)),
		onOffText(service.Notify),
	)
}

func statusWatchAutomationDetail(m Model) string {
	if errText := strings.TrimSpace(m.dotsWatchServiceErr); errText != "" {
		return "Watch [WARN] status unavailable: " + errText
	}
	service := m.dotsWatchService
	if service == nil || !service.Installed {
		return "Watch [OFF]"
	}
	return fmt.Sprintf("Watch [ON] %s debounce %s",
		statusServicePlatform(service.Platform),
		formatSettingsDuration(app.DotsWatchDebounce(service)),
	)
}

func statusServicePlatform(platform string) string {
	platform = strings.TrimSpace(platform)
	if platform == "" {
		return "native"
	}
	return platform
}

func statusDoctorAttentionSummary(m Model, labels []string) string {
	s := m.doctorResult.Summary
	var parts []string
	if s.Fail > 0 {
		parts = append(parts, textutil.PluralCount(s.Fail, "fail", "fail"))
	}
	if s.Warn > 0 {
		parts = append(parts, textutil.PluralCount(s.Warn, "warn", "warn"))
	}
	if len(labels) > 0 {
		parts = append(parts, statusInlineNames(labels, 3))
	}
	return strings.Join(parts, ": ")
}

func statusDotsServiceReadinessWarnings(m Model) []string {
	warnings := dashboardDotsAutomationStatus(m).ReadinessWarnings
	details := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		details = append(details, statusDetailLine(m, warning))
	}
	return details
}

func dashboardDotsAutomationStatus(m Model) app.DashboardDotsAutomationStatus {
	return app.BuildDashboardDotsAutomationStatus(app.DashboardDotsAutomationInput{
		Services: app.DotsServicesStatus{
			Reminder:      m.dotsReminderService,
			ReminderError: m.dotsReminderServiceErr,
			Watch:         m.dotsWatchService,
			WatchError:    m.dotsWatchServiceErr,
		},
		DotsConfigured: m.dotsConfiguredCached,
		DotsDisabled:   m.dotsSyncAvailCached.Reason == app.DotsSyncAvailabilityDisabled,
	})
}

func statusAgentsOverviewSummary(view agentsDashboardView, counts agentsDashCounts) string {
	return "Managed by ~/.apm/apm.yml."
}

func statusAgentsOverviewDetails(m Model, view agentsDashboardView) []string {
	return statusAgentsAttentionDetails(m, agentsDashCounts{}, view)
}

func statusAgentUpdatesDetails(m Model, counts agentsDashCounts, names []string) []string {
	return statusDetailLines(m, "Run APM outdated or update from the Agents tab.")
}

func onOffText(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}
