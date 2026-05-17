package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

type statusToolSummary struct {
	tracked        int
	updates        int
	outOfSync      int
	installed      int
	available      int
	ignored        int
	updateNames    []string
	outOfSyncNames []string
	installedNames []string
	availableNames []string
	ignoredNames   []string
}

func statusToolCounts(m Model) statusToolSummary {
	var counts statusToolSummary
	seen := make(map[string]bool, len(m.allTools)+len(m.discoveredTools))
	for _, tool := range m.allTools {
		if tool == nil {
			continue
		}
		statusAccumulateTool(&counts, m, tool)
		seen[toolKey(tool.Name, tool.Provider)] = true
	}
	for _, tool := range m.discoveredTools {
		if tool == nil || seen[toolKey(tool.Name, tool.Provider)] {
			continue
		}
		statusAccumulateTool(&counts, m, tool)
	}
	sort.Strings(counts.updateNames)
	sort.Strings(counts.outOfSyncNames)
	sort.Strings(counts.installedNames)
	sort.Strings(counts.availableNames)
	sort.Strings(counts.ignoredNames)
	return counts
}

func statusAccumulateTool(counts *statusToolSummary, m Model, tool *database.ToolCache) {
	if tool.Tracked {
		counts.tracked++
	}
	if tool.Installed {
		counts.installed++
		counts.installedNames = append(counts.installedNames, statusToolName(tool))
	}
	switch m.displaySection(tool) {
	case sectionUpdates:
		counts.updates++
		counts.updateNames = append(counts.updateNames, statusToolUpdateName(tool))
	case sectionOutOfSync:
		counts.outOfSync++
		counts.outOfSyncNames = append(counts.outOfSyncNames, statusToolSyncIssueName(m, tool))
	case sectionAvailable:
		counts.available++
		counts.availableNames = append(counts.availableNames, statusToolName(tool))
	case sectionIgnored:
		counts.ignored++
		counts.ignoredNames = append(counts.ignoredNames, statusToolName(tool))
	}
}

func statusToolName(tool *database.ToolCache) string {
	if tool == nil {
		return ""
	}
	return tool.Name
}

func statusToolUpdateName(tool *database.ToolCache) string {
	if tool == nil {
		return ""
	}
	latest := strings.TrimSpace(tool.LatestVersion.String)
	if !tool.LatestVersion.Valid || latest == "" {
		latest = "?"
	}
	if latest == "?" {
		return tool.Name
	}
	return fmt.Sprintf("%s (%s)", tool.Name, latest)
}

func statusToolSyncIssueName(m Model, tool *database.ToolCache) string {
	name := statusToolName(tool)
	switch m.syncStatusOf(tool) {
	case syncMissing:
		return name + " missing"
	case syncOrphan:
		return name + " local only"
	case syncWrongProv:
		return name + " provider mismatch"
	default:
		return name
	}
}

func statusCountValue(m Model, count int, singular, plural, empty string) string {
	if count == 0 {
		return m.palette.styleInstalled.Render(empty)
	}
	return m.palette.styleOutdated.Render(pluralCount(count, singular, plural))
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

func pluralCount(count int, singular, plural string) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", singular)
	}
	return fmt.Sprintf("%d %s", count, plural)
}

func statusToolsLoading(m Model) bool {
	return m.loading ||
		len(m.upgradingKeys) > 0 ||
		len(m.scanningProviders) > 0 ||
		m.providerSnapshotRefreshing ||
		m.discoveryRefreshing ||
		m.descRefreshing
}

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

func statusToolsOverviewValue(m Model, counts statusToolSummary) string {
	if counts.tracked == 0 {
		return m.palette.styleHelp.Render("no tools")
	}
	if counts.updates > 0 {
		return m.palette.styleOutdated.Render(pluralCount(counts.updates, "update", "updates"))
	}
	return m.palette.styleProvider.Render(pluralCount(counts.tracked, "tracked", "tracked"))
}

func statusToolsOverviewSummary(counts statusToolSummary) string {
	if counts.tracked == 0 {
		return "No tools configured for this host."
	}
	parts := []string{pluralCount(counts.installed, "installed locally", "installed locally")}
	if counts.available > 0 {
		parts = append(parts, pluralCount(counts.available, "available", "available"))
	}
	if counts.outOfSync > 0 {
		parts = append(parts, pluralCount(counts.outOfSync, "sync issue", "sync issues"))
	}
	return strings.Join(parts, ", ")
}

func statusToolsOverviewDetails(m Model, counts statusToolSummary) []string {
	details := []string{
		statusDetailLine(m, pluralCount(counts.tracked, "tracked tool", "tracked tools")),
		statusDetailLine(m, pluralCount(counts.installed, "installed locally", "installed locally")),
	}
	if counts.available > 0 {
		details = append(details, statusDetailLine(m, pluralCount(counts.available, "available tool", "available tools")))
	}
	if counts.updates > 0 {
		details = append(details, statusDetailLine(m, pluralCount(counts.updates, "pending update", "pending updates")))
	}
	if counts.outOfSync > 0 {
		details = append(details, statusDetailLine(m, pluralCount(counts.outOfSync, "sync issue", "sync issues")))
	}
	return details
}

func statusToolSyncDetails(m Model, counts statusToolSummary) []string {
	var details []string
	if statusDashboardToolSyncBusy(m) {
		details = append(details, statusActivityDetailLine(m, statusToolsActivityText(m), true))
		if len(m.bulkPendingKeys) > 0 {
			details = append(details, statusDetailLine(m, "queued: "+statusInlineNames(statusToolSyncQueuedNames(m), 5)))
		}
	}
	if counts.outOfSync == 0 {
		if len(details) == 0 {
			return statusDetailLines(m, "All tracked tools match this host.")
		}
		return details
	}
	details = append(details, statusDetailLine(m, "Issues: "+statusInlineNames(counts.outOfSyncNames, 5)))
	if len(counts.outOfSyncNames) > 5 {
		details = append(details, statusDetailLine(m, fmt.Sprintf("+%d more", len(counts.outOfSyncNames)-5)))
	}
	return details
}

func statusToolSyncQueuedNames(m Model) []string {
	names := make([]string, 0, len(m.bulkPendingKeys))
	seen := make(map[string]bool, len(m.allTools)+len(m.discoveredTools))
	visit := func(tool *database.ToolCache) {
		if tool == nil {
			return
		}
		key := toolKey(tool.Name, tool.Provider)
		if seen[key] || !m.bulkPendingKeys[key] {
			return
		}
		seen[key] = true
		if m.displaySection(tool) == sectionOutOfSync || !tool.Installed {
			names = append(names, statusToolSyncIssueName(m, tool))
		}
	}
	for _, tool := range m.allTools {
		visit(tool)
	}
	for _, tool := range m.discoveredTools {
		visit(tool)
	}
	sort.Strings(names)
	return names
}

func statusUpgradeNames(m Model) ([]string, []string) {
	active := make([]string, 0, len(m.upgradingKeys))
	waiting := make([]string, 0, len(m.bulkPendingKeys))
	seen := make(map[string]bool, len(m.allTools)+len(m.discoveredTools))
	visit := func(tool *database.ToolCache) {
		if tool == nil {
			return
		}
		key := toolKey(tool.Name, tool.Provider)
		if seen[key] {
			return
		}
		seen[key] = true
		if m.displaySection(tool) != sectionUpdates {
			return
		}
		name := statusToolUpdateName(tool)
		switch {
		case m.upgradingKeys[key] || m.rowOpKey == key:
			active = append(active, name)
		case m.bulkPendingKeys[key]:
			waiting = append(waiting, name)
		}
	}
	for _, tool := range m.allTools {
		visit(tool)
	}
	for _, tool := range m.discoveredTools {
		visit(tool)
	}
	sort.Strings(active)
	sort.Strings(waiting)
	return active, waiting
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
		return m.palette.styleStatus.Render(iconPending + " " + pluralCount(len(waiting), "queued", "queued"))
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

// truncatedGitStatus splits a git status string into non-empty lines,
// caps at maxLines, and returns an overflow summary if truncated.
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

func statusDotSummary(counts app.DotFileCounts, gitStatus string) string {
	if counts.Synced > 0 || counts.OutOfSync > 0 || counts.Ignored > 0 {
		summary := dotRatioText(counts) + " managed" + statusDotIgnoredSuffix(counts)
		if strings.TrimSpace(gitStatus) != "" {
			summary += ", repo dirty"
		}
		return summary
	}
	return "No dotfile entries loaded."
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
	if config.BoolVal(m.settings.DotsDisabled) || strings.TrimSpace(m.settings.DotsRepo) == "" {
		return false
	}
	if m.dotsLoading || m.dotsPreparing {
		return false
	}
	return dotHeaderCounts(m.dotsEntries).OutOfSync > 0
}

func statusDashboardToolSyncActionable(m Model) bool {
	return countSyncAllProgressItems(m.allTools, m.discoveredTools) > 0
}

func statusDashboardToolSyncBusy(m Model) bool {
	if !m.loading || len(m.upgradingKeys) > 0 {
		return false
	}
	if len(statusToolSyncQueuedNames(m)) > 0 {
		return true
	}
	text := strings.ToLower(strings.TrimSpace(m.progressText))
	return strings.Contains(text, "sync") || strings.Contains(text, "install") || strings.Contains(text, "add")
}

func statusDashboardUpgradeActionable(m Model) bool {
	return statusToolCounts(m).updates > 0 && len(m.upgradingKeys) == 0
}

func statusDashboardDotsCommitActionable(m Model) bool {
	return !config.BoolVal(m.settings.DotsDisabled) &&
		strings.TrimSpace(m.settings.DotsRepo) != "" &&
		!m.dotsLoading &&
		!m.dotsPreparing &&
		strings.TrimSpace(m.dotsGitStatus) != ""
}

func statusDashboardReconcileActionable(m Model) bool {
	return statusDashboardToolSyncActionable(m) ||
		statusDashboardUpgradeActionable(m) ||
		statusDashboardDotsSyncActionable(m) ||
		statusDashboardDotsCommitActionable(m) ||
		statusDashboardFixIgnoreActionable(m)
}

func statusDashboardFixIgnoreActionable(m Model) bool {
	return doctorHasIgnoreFindings(m)
}

func doctorHasIgnoreFindings(m Model) bool {
	if m.doctorResult == nil {
		return false
	}
	for _, check := range m.doctorResult.Checks {
		if check.ID == "dots.ignore" && check.Status == app.DoctorStatusWarn {
			return true
		}
	}
	return false
}

func statusFixIgnorePlanDetail(m Model) string {
	if m.doctorResult == nil {
		return ""
	}
	for _, check := range m.doctorResult.Checks {
		if check.ID == "dots.ignore" && check.Status == app.DoctorStatusWarn {
			return check.Message
		}
	}
	return ""
}

func statusDotfilesOverviewValue(m Model, counts app.DotFileCounts) string {
	switch {
	case config.BoolVal(m.settings.DotsDisabled):
		return m.palette.styleHelp.Render("disabled")
	case strings.TrimSpace(m.settings.DotsRepo) == "":
		return m.palette.styleHelp.Render("not set")
	case counts.OutOfSync > 0:
		return m.palette.styleOutdated.Render(dotRatioText(counts))
	case statusDotfilesNotLoaded(m, counts):
		return m.palette.styleHelp.Render("not loaded")
	case strings.TrimSpace(m.dotsGitStatus) != "":
		return m.palette.styleOutdated.Render("dirty")
	default:
		return m.palette.styleInstalled.Render(dotRatioText(counts))
	}
}

func statusDotOverviewSummary(m Model, counts app.DotFileCounts) string {
	switch {
	case config.BoolVal(m.settings.DotsDisabled):
		return "Dotfile sync disabled for this host."
	case strings.TrimSpace(m.settings.DotsRepo) == "":
		return "Set dots_repo to use dotfiles."
	default:
		return statusDotSummary(counts, m.dotsGitStatus)
	}
}

func statusDotOverviewDetails(m Model, counts app.DotFileCounts) []string {
	switch {
	case config.BoolVal(m.settings.DotsDisabled):
		return statusDetailLines(m, "Dotfile sync disabled for this host.")
	case strings.TrimSpace(m.settings.DotsRepo) == "":
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
	if counts.Synced > 0 || counts.OutOfSync > 0 || counts.Ignored > 0 {
		details = append(details, statusDetailLine(m, dotRatioText(counts)+" managed"+statusDotIgnoredSuffix(counts)))
	}
	if strings.TrimSpace(m.dotsGitStatus) != "" {
		lines, overflow := truncatedGitStatus(m.dotsGitStatus, 3)
		if len(lines) > 0 {
			details = append(details, statusDetailLine(m, "repo dirty: "+lines[0]))
			for _, line := range lines[1:] {
				details = append(details, statusDetailLine(m, line))
			}
		}
		if overflow != "" {
			details = append(details, statusDetailLine(m, overflow))
		}
	}
	details = append(details, statusDotsHistoryDetails(m)...)
	if repo := strings.TrimSpace(m.settings.DotsRepo); repo != "" {
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

// statusDotAttentionSummary returns a summary focused on what's wrong
// (out-of-sync, dirty repo) without repeating managed/ignored counts
// that the overview row already shows.
func statusDotAttentionSummary(counts app.DotFileCounts, gitStatus string) string {
	var parts []string
	if counts.OutOfSync > 0 {
		parts = append(parts, pluralCount(counts.OutOfSync, "out-of-sync entry", "out-of-sync entries"))
	}
	if strings.TrimSpace(gitStatus) != "" {
		parts = append(parts, "repo dirty")
	}
	if len(parts) == 0 {
		return "Dotfiles need attention."
	}
	return strings.Join(parts, ", ") + "."
}

// statusDotAttentionDetails returns detail lines for the attention row,
// omitting managed/ignored counts (shown in the overview row below).
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
		details = append(details, statusDetailLine(m, pluralCount(counts.OutOfSync, "entry out of sync", "entries out of sync")))
	}
	if strings.TrimSpace(m.dotsGitStatus) != "" {
		lines, overflow := truncatedGitStatus(m.dotsGitStatus, 3)
		if len(lines) > 0 {
			details = append(details, statusDetailLine(m, "repo dirty: "+lines[0]))
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

func statusDotIgnoredSuffix(counts app.DotFileCounts) string {
	if counts.Ignored == 0 {
		return ""
	}
	return fmt.Sprintf(", %d ignored", counts.Ignored)
}

func statusAutomationNeedsAttention(m Model) bool {
	if strings.TrimSpace(m.dotsReminderServiceErr) != "" || strings.TrimSpace(m.dotsWatchServiceErr) != "" {
		return true
	}
	if !statusAnyAutomationInstalled(m) {
		return false
	}
	return strings.TrimSpace(m.settings.DotsRepo) == "" || config.BoolVal(m.settings.DotsDisabled)
}

func statusAnyAutomationInstalled(m Model) bool {
	return (m.dotsReminderService != nil && m.dotsReminderService.Installed) ||
		(m.dotsWatchService != nil && m.dotsWatchService.Installed)
}

func statusAutomationValue(m Model) string {
	if m.dotsServicesRefreshing {
		return statusLoadingValue(m, "loading")
	}
	if strings.TrimSpace(m.dotsReminderServiceErr) != "" || strings.TrimSpace(m.dotsWatchServiceErr) != "" {
		return m.palette.styleOutdated.Render("[warn]")
	}
	if statusAnyAutomationInstalled(m) && (strings.TrimSpace(m.settings.DotsRepo) == "" || config.BoolVal(m.settings.DotsDisabled)) {
		return m.palette.styleOutdated.Render("[blocked]")
	}
	installed := 0
	if m.dotsReminderService != nil && m.dotsReminderService.Installed {
		installed++
	}
	if m.dotsWatchService != nil && m.dotsWatchService.Installed {
		installed++
	}
	text := fmt.Sprintf("[%d/2 ON]", installed)
	switch installed {
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
	summary := strings.Join(parts, " · ")
	if m.dotsServicesRefreshing {
		return statusStaleSummary("Refreshing service status…", summary, statusAnyAutomationKnown(m))
	}
	return summary
}

func statusAnyAutomationKnown(m Model) bool {
	return m.dotsReminderService != nil ||
		m.dotsWatchService != nil ||
		strings.TrimSpace(m.dotsReminderServiceErr) != "" ||
		strings.TrimSpace(m.dotsWatchServiceErr) != ""
}

func statusReminderAutomationSummary(m Model) string {
	if errText := strings.TrimSpace(m.dotsReminderServiceErr); errText != "" {
		return "Reminder [WARN] " + errText
	}
	service := m.dotsReminderService
	if service == nil || !service.Installed {
		return "Reminder [OFF]"
	}
	return fmt.Sprintf("Reminder [ON] %s notify %s", formatSettingsDuration(dotsReminderIntervalFromService(service)), onOffText(service.Notify))
}

func statusWatchAutomationSummary(m Model) string {
	if errText := strings.TrimSpace(m.dotsWatchServiceErr); errText != "" {
		return "Watch [WARN] " + errText
	}
	service := m.dotsWatchService
	if service == nil || !service.Installed {
		return "Watch [OFF]"
	}
	return "Watch [ON] debounce " + formatSettingsDuration(dotsWatchDebounceFromService(service))
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
		formatSettingsDuration(dotsReminderIntervalFromService(service)),
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
		formatSettingsDuration(dotsWatchDebounceFromService(service)),
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
		parts = append(parts, pluralCount(s.Fail, "fail", "fail"))
	}
	if s.Warn > 0 {
		parts = append(parts, pluralCount(s.Warn, "warn", "warn"))
	}
	if len(labels) > 0 {
		parts = append(parts, statusInlineNames(labels, 3))
	}
	return strings.Join(parts, ": ")
}

func statusDotsServiceReadinessWarnings(m Model) []string {
	var details []string
	if strings.TrimSpace(m.settings.DotsRepo) == "" {
		details = append(details, statusDetailLine(m, "Blocked: dots_repo is not configured."))
	}
	if config.BoolVal(m.settings.DotsDisabled) {
		details = append(details, statusDetailLine(m, "Blocked: dotfile sync is disabled for this host."))
	}
	return details
}

func onOffText(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}
