package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

const statusLabelWidth = 18

const (
	statusSectionAttention = "Health Check"
	statusSectionOverview  = "Data"
	statusSectionQuiet     = "Quiet"
)

type statusActionKind int

const (
	statusActionNone statusActionKind = iota
	statusActionRunDoctor
	statusActionOpenTools
	statusActionOpenToolsSection
	statusActionOpenDots
	statusActionOpenDotsIssue
	statusActionOpenSettings
	statusActionSyncTools
	statusActionSyncDots
	statusActionCommitDots
	statusActionUpgradeTools
	statusActionFixIgnore
)

type statusAction struct {
	kind        statusActionKind
	toolSection section
	settingsRow int
	desc        string
}

type statusListRow struct {
	section   string
	icon      string
	iconStyle lipgloss.Style
	label     string
	value     string
	summary   string
	details   []string
	action    statusAction
	muted     bool
}

type dashboardReconcilePlanItem struct {
	kind   dashboardReconcilePlanKind
	label  string
	detail string
}

func renderStatus(m Model) string {
	rows := statusRows(m)
	m.statusCursor = clampIndex(m.statusCursor, len(rows))
	sections := statusSections(m, rows)
	return renderSectionedTab(m, sectionedTab{
		leadingBlank: true,
		sections:     sections,
	})
}

func renderDashboardReconcilePlanPopup(m Model) string {
	p := m.palette
	contentW := dashboardReconcilePlanContentWidth(m)
	items := dashboardReconcilePlanItems(m)
	if len(items) == 0 {
		return p.styleHelp.Render("no reconcile operations available")
	}

	labelW, detailW := dashboardReconcilePlanColumnWidths(items)
	labelW, detailW = fitPickerChoiceColumnWidths(contentW, true, labelW, detailW)
	rows := make([]pickerChoiceRow, 0, len(items))
	for i, item := range items {
		selected := i == m.dashboardReconcilePlanCursor
		checked := dashboardReconcilePlanItemSelected(m, item.kind)
		style := p.styleNormal
		if !checked {
			style = p.styleHelp
		}
		rows = append(rows, pickerChoiceRow{
			selected: selected,
			mark:     dashboardReconcilePlanMark(checked),
			label:    item.label,
			detail:   item.detail,
			style:    style,
		})
	}

	var sb strings.Builder
	sb.WriteString(renderPickerChoiceRows(p, rows, labelW, detailW))
	sb.WriteString("\n")
	sb.WriteString(renderPickerHintItems(m, contentW, []hintItem{
		hintFromBindingDesc(m.keys.Back, "cancel"),
		hintFromBindingDesc(m.keys.Toggle, "select"),
		hintFromBindingDesc(m.keys.Confirm, "reconcile selected"),
	}))
	return lipgloss.NewStyle().Width(contentW).Render(sb.String())
}

func dashboardReconcilePlanPopupFrame(m Model) popupFrame {
	paddingX := 2
	contentW := dashboardReconcilePlanContentWidth(m)
	return popupFrame{
		Title:          "Reconcile Plan",
		PaddingY:       1,
		PaddingX:       paddingX,
		Width:          popupFrameWidthForContent(contentW, paddingX),
		NoTitleDivider: true,
	}
}

func dashboardReconcilePlanContentWidth(m Model) int {
	labelW, detailW := dashboardReconcilePlanColumnWidths(dashboardReconcilePlanItems(m))
	preferred := max(pickerToggleRowWidth(labelW, detailW), 56)
	return popupContentWidth(m, preferred, 42, 72)
}

func dashboardReconcilePlanColumnWidths(items []dashboardReconcilePlanItem) (int, int) {
	labelW := 0
	detailW := 0
	for _, item := range items {
		labelW = max(labelW, lipgloss.Width(item.label))
		detailW = max(detailW, lipgloss.Width(item.detail))
	}
	return max(labelW, 12), detailW
}

func dashboardReconcilePlanItems(m Model) []dashboardReconcilePlanItem {
	items := make([]dashboardReconcilePlanItem, 0, 4)
	if statusDashboardToolSyncActionable(m) {
		items = append(items, dashboardReconcilePlanItem{
			kind:   dashboardReconcilePlanSyncTools,
			label:  "Sync tools",
			detail: dashboardToolSyncPlanDetail(m),
		})
	}
	if statusDashboardUpgradeActionable(m) {
		counts := statusToolCounts(m)
		items = append(items, dashboardReconcilePlanItem{
			kind:   dashboardReconcilePlanUpgradeTools,
			label:  "Upgrade tools",
			detail: pluralCount(counts.updates, "outdated tool", "outdated tools"),
		})
	}
	if statusDashboardDotsSyncActionable(m) {
		counts := dotHeaderCounts(m.dotsEntries)
		items = append(items, dashboardReconcilePlanItem{
			kind:   dashboardReconcilePlanSyncDots,
			label:  "Sync dotfiles",
			detail: pluralCount(counts.OutOfSync, "out-of-sync entry", "out-of-sync entries"),
		})
	}
	if statusDashboardDotsCommitActionable(m) {
		items = append(items, dashboardReconcilePlanItem{
			kind:   dashboardReconcilePlanCommitDots,
			label:  "Commit dotfiles",
			detail: statusDotsCommitPlanDetail(m),
		})
	}
	if statusDashboardFixIgnoreActionable(m) {
		items = append(items, dashboardReconcilePlanItem{
			kind:   dashboardReconcilePlanFixIgnore,
			label:  "Fix ignore patterns",
			detail: statusFixIgnorePlanDetail(m),
		})
	}
	return items
}

func dashboardToolSyncPlanDetail(m Model) string {
	missing, discovered := dashboardToolSyncPlanCounts(m)
	parts := make([]string, 0, 2)
	if missing > 0 {
		parts = append(parts, "install "+pluralCount(missing, "missing tool", "missing tools"))
	}
	if discovered > 0 {
		parts = append(parts, "add "+pluralCount(discovered, "local tool", "local tools"))
	}
	return strings.Join(parts, ", ")
}

func dashboardToolSyncPlanCounts(m Model) (int, int) {
	missing := 0
	for _, tool := range m.allTools {
		if tool != nil && tool.Tracked && !tool.Installed {
			missing++
		}
	}
	discovered := 0
	for _, tool := range m.discoveredTools {
		if tool != nil && tool.Name != "" && tool.Provider != "" {
			discovered++
		}
	}
	return missing, discovered
}

func statusDotsCommitPlanDetail(m Model) string {
	lines := strings.Split(strings.TrimSpace(m.dotsGitStatus), "\n")
	count := 0
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	if count == 0 {
		return "no repo changes"
	}
	return pluralCount(count, "repo change", "repo changes")
}

func dashboardReconcilePlanItemSelected(m Model, kind dashboardReconcilePlanKind) bool {
	if m.dashboardReconcilePlanSelected == nil {
		return true
	}
	selected, ok := m.dashboardReconcilePlanSelected[kind]
	return !ok || selected
}

func dashboardReconcilePlanMark(selected bool) string {
	if selected {
		return "[x]"
	}
	return "[ ]"
}

func statusRows(m Model) []statusListRow {
	counts := statusToolCounts(m)
	rows := make([]statusListRow, 0, 10)
	attention := statusAttentionRows(m, counts)
	if len(attention) == 0 {
		attention = append(attention, statusAllClearRow(m, counts))
	}
	rows = append(rows, attention...)
	rows = append(rows, statusOverviewRows(m, counts)...)
	rows = append(rows, statusQuietRows(m, counts)...)
	return rows
}

func statusSections(m Model, rows []statusListRow) []sectionedTabSection {
	sectionOrder := []string{statusSectionAttention, statusSectionOverview, statusSectionQuiet}
	bySection := make(map[string][]sectionedTabRow, len(sectionOrder))
	for i, row := range rows {
		selected := i == m.statusCursor && !m.cursorHidden
		details := []string(nil)
		if selected {
			details = statusSelectedDetails(m, row)
		}
		bySection[row.section] = append(bySection[row.section], sectionedTabRow{
			selected: selected,
			line:     statusRowLine(m, row, selected),
			details:  details,
		})
	}
	sections := make([]sectionedTabSection, 0, len(sectionOrder))
	for _, section := range sectionOrder {
		if len(bySection[section]) == 0 {
			continue
		}
		sections = append(sections, sectionedTabSection{title: section, rows: bySection[section]})
	}
	return sections
}

func statusAttentionRows(m Model, counts statusToolSummary) []statusListRow {
	rows := make([]statusListRow, 0, 5)
	if row, ok := statusUpdatesRow(m, counts); ok {
		rows = append(rows, row)
	}
	if counts.outOfSync > 0 {
		rows = append(rows, statusToolSyncAttentionRow(m, counts))
	}
	if row, ok := statusDotfilesAttentionRow(m); ok {
		rows = append(rows, row)
	}
	if row, ok := statusAutomationAttentionRow(m); ok {
		rows = append(rows, row)
	}
	hasDotfilesRow := len(rows) > 0 && rows[len(rows)-1].label == "Dotfiles"
	if row, ok := statusHealthAttentionRow(m, hasDotfilesRow); ok {
		rows = append(rows, row)
	}
	return rows
}

func statusAttentionCount(m Model) int {
	return len(statusAttentionRows(m, statusToolCounts(m)))
}

func statusHealthAttentionRow(m Model, hasDotfilesRow bool) (statusListRow, bool) {
	switch {
	case m.doctorRunning:
		icon, iconStyle := statusRowWorkingIcon(m, true)
		return statusListRow{
			section:   statusSectionAttention,
			icon:      icon,
			iconStyle: iconStyle,
			label:     "Doctor",
			value:     statusDoctorValue(m),
			summary:   "Running read-only checks.",
			details:   []string{statusActivityDetailLine(m, "Running read-only checks.", false)},
		}, true
	case m.doctorErr != "":
		summary := "Doctor could not finish: " + m.doctorErr
		return statusListRow{
			section:   statusSectionAttention,
			icon:      iconFailed,
			iconStyle: m.palette.styleErr,
			label:     "Doctor",
			value:     statusDoctorValue(m),
			summary:   summary,
			details:   statusDetailLines(m, summary),
			action:    statusAction{kind: statusActionRunDoctor, desc: "rerun doctor"},
		}, true
	case m.doctorResult == nil:
		return statusListRow{}, false
	case len(m.doctorResult.Checks) == 0:
		summary := "No individual checks were returned."
		return statusListRow{
			section:   statusSectionAttention,
			icon:      iconFailed,
			iconStyle: m.palette.styleOutdated,
			label:     "Doctor",
			value:     m.palette.styleOutdated.Render("[warn]"),
			summary:   "No individual checks were returned.",
			details:   []string{statusDetailLine(m, summary)},
			action:    statusAction{kind: statusActionRunDoctor, desc: "rerun doctor"},
		}, true
	}

	var nonOK []app.DoctorCheck
	for _, check := range m.doctorResult.Checks {
		if check.Status == app.DoctorStatusOK {
			continue
		}
		// Skip dots check when the dedicated Dotfiles attention row already
		// covers it — avoids duplicating dotfile issues across two rows.
		if hasDotfilesRow && check.ID == "dots" {
			continue
		}
		nonOK = append(nonOK, check)
	}
	if len(nonOK) == 0 {
		return statusListRow{}, false
	}
	return statusDoctorSummaryRow(m, nonOK), true
}

func statusDoctorSummaryRow(m Model, checks []app.DoctorCheck) statusListRow {
	details := make([]string, 0, min(len(checks), 5)+1)
	labels := make([]string, 0, len(checks))
	for _, check := range checks {
		labels = append(labels, check.Label)
		if len(details) < 5 {
			details = append(details, statusDoctorCheckRows(m, check)...)
		}
	}
	if len(checks) > len(details) {
		details = append(details, statusDetailLine(m, fmt.Sprintf("+%d more check(s)", len(checks)-len(details))))
	}
	action := statusAction{kind: statusActionRunDoctor, desc: "rerun doctor"}
	if len(checks) == 1 {
		action = statusDoctorCheckAction(checks[0])
	}
	return statusListRow{
		section:   statusSectionAttention,
		icon:      statusDoctorIcon(m),
		iconStyle: statusDoctorIconStyle(m),
		label:     "Doctor",
		value:     statusDoctorValue(m),
		summary:   statusDoctorAttentionSummary(m, labels),
		details:   details,
		action:    action,
	}
}

func statusDoctorCheckAction(check app.DoctorCheck) statusAction {
	switch check.ID {
	case "config", "host":
		return statusAction{kind: statusActionOpenSettings, settingsRow: settingsRowBootstrap, desc: "open bootstrap"}
	case "providers":
		return statusAction{kind: statusActionOpenSettings, settingsRow: settingsRowSystemProvider, desc: "open provider settings"}
	case "dots":
		if strings.Contains(check.Message, "disabled") {
			return statusAction{kind: statusActionOpenSettings, settingsRow: settingsRowDotsSync, desc: "open dotfile sync setting"}
		}
		if strings.Contains(check.Message, "dots_repo") {
			return statusAction{kind: statusActionOpenSettings, settingsRow: settingsRowDotsRepo, desc: "open dotfile settings"}
		}
		return doctorDotsAction(check)
	case "services":
		return statusAction{kind: statusActionOpenSettings, settingsRow: settingsRowDotsServices, desc: "open service settings"}
	case "cache":
		return statusAction{kind: statusActionOpenSettings, settingsRow: settingsRowResetCache, desc: "open cache reset"}
	default:
		return statusAction{kind: statusActionRunDoctor, desc: "rerun doctor"}
	}
}

// doctorDotsAction picks the most actionable enter-key action for a dots
// doctor warning based on the check details.
func doctorDotsAction(check app.DoctorCheck) statusAction {
	hasGit, hasSync := false, false
	for _, d := range check.Details {
		switch {
		case strings.Contains(d, "pending git"):
			hasGit = true
		case strings.Contains(d, "out-of-sync") || strings.Contains(d, "missing"):
			hasSync = true
		}
	}
	switch {
	case hasSync:
		return statusAction{kind: statusActionSyncDots, desc: "sync dotfiles"}
	case hasGit:
		return statusAction{kind: statusActionCommitDots, desc: "commit dotfiles"}
	default:
		return statusAction{kind: statusActionOpenDotsIssue, desc: "open dotfiles"}
	}
}

// doctorDetailHint returns a short inline fix suggestion appended to a doctor
// check detail line, based on the check ID and detail content.
func doctorDetailHint(checkID, detail string) string {
	if checkID != "dots" {
		return ""
	}
	switch {
	case strings.Contains(detail, "pending git"):
		return " — press C or 'omni dots commit'"
	case strings.Contains(detail, "out-of-sync"):
		return " — sync from Dots tab or 'omni dots sync'"
	case strings.Contains(detail, "conflict"):
		return " — resolve from Dots tab"
	case strings.Contains(detail, "stow missing"):
		return " — install stow ('brew install stow')"
	default:
		return ""
	}
}

func statusOverviewRows(m Model, counts statusToolSummary) []statusListRow {
	return []statusListRow{
		statusToolsOverviewRow(m, counts),
		statusToolSyncOverviewRow(m, counts),
		statusDotfilesOverviewRow(m),
		statusAutomationOverviewRow(m),
	}
}

func statusQuietRows(m Model, counts statusToolSummary) []statusListRow {
	var rows []statusListRow
	if counts.ignored > 0 {
		rows = append(rows, statusIgnoredRow(m, counts))
	}
	return rows
}

func statusAllClearRow(m Model, counts statusToolSummary) statusListRow {
	summary := "Tools current, dotfiles synced, automation ok."
	if counts.tracked == 0 {
		summary = "No attention items detected."
	}
	return statusListRow{
		section:   statusSectionAttention,
		icon:      iconInstalled,
		iconStyle: m.palette.styleInstalled,
		label:     "All Clear",
		value:     m.palette.styleInstalled.Render("[ok]"),
		summary:   summary,
		details:   statusDetailLines(m, "No attention items detected.", summary),
	}
}

func statusUpdatesRow(m Model, counts statusToolSummary) (statusListRow, bool) {
	active, waiting := statusUpgradeNames(m)
	if counts.updates == 0 && len(active) == 0 && len(waiting) == 0 && !m.upgradingKeys["*"] {
		return statusListRow{}, false
	}
	value := statusCountValue(m, counts.updates, "update", "updates", "current")
	summary := statusSampleSummary(counts.updateNames, "No outdated tools.")
	details := statusOverflowDetails(m, counts.updateNames)
	action := statusAction{kind: statusActionUpgradeTools, desc: "upgrade all tools"}
	icon, iconStyle := statusRowWarningIcon(m)
	if len(active) > 0 || len(waiting) > 0 || m.upgradingKeys["*"] {
		summary = statusUpgradeSummary(active, waiting, counts.updateNames)
		details = statusUpgradeDetails(m, active, waiting, counts.updateNames)
		value = statusUpgradeValue(m, active, waiting)
		action = statusAction{kind: statusActionOpenToolsSection, toolSection: sectionUpdates, desc: "open updates"}
		icon, iconStyle = statusRowWorkingIcon(m, len(active) > 0)
	}
	return statusListRow{
		section:   statusSectionAttention,
		icon:      icon,
		iconStyle: iconStyle,
		label:     "Tool Updates",
		value:     value,
		summary:   summary,
		details:   details,
		action:    action,
	}, true
}

func statusToolSyncAttentionRow(m Model, counts statusToolSummary) statusListRow {
	action := statusAction{kind: statusActionOpenToolsSection, toolSection: sectionOutOfSync, desc: "open sync issues"}
	value := statusCountValue(m, counts.outOfSync, "issue", "issues", "in sync")
	summary := statusSampleSummary(counts.outOfSyncNames, "All tracked tools match this host.")
	details := statusToolSyncDetails(m, counts)
	icon, iconStyle := statusRowWarningIcon(m)
	if statusDashboardToolSyncBusy(m) {
		value = statusLoadingValue(m, "syncing")
		summary = statusStaleSummary(statusToolsActivityText(m), summary, counts.outOfSync > 0)
		icon, iconStyle = statusRowWorkingIcon(m, m.rowOpKey != "" || strings.TrimSpace(m.progressText) != "")
	}
	if statusDashboardToolSyncActionable(m) && !statusToolsLoading(m) {
		action = statusAction{kind: statusActionSyncTools, desc: "sync tools"}
	}
	return statusListRow{
		section:   statusSectionAttention,
		icon:      icon,
		iconStyle: iconStyle,
		label:     "Tool Sync",
		value:     value,
		summary:   summary,
		details:   details,
		action:    action,
	}
}

func statusIgnoredRow(m Model, counts statusToolSummary) statusListRow {
	return statusListRow{
		section:   statusSectionQuiet,
		icon:      iconIgnored,
		iconStyle: m.palette.styleHelp,
		label:     "Ignored Tools",
		value:     m.palette.styleHelp.Render(pluralCount(counts.ignored, "ignored", "ignored")),
		summary:   statusSampleSummary(counts.ignoredNames, "No ignored tools."),
		details:   statusOverflowDetails(m, counts.ignoredNames),
		action:    statusAction{kind: statusActionOpenToolsSection, toolSection: sectionIgnored, desc: "open ignored tools"},
		muted:     true,
	}
}

func statusToolsOverviewRow(m Model, counts statusToolSummary) statusListRow {
	summary := statusToolsOverviewSummary(counts)
	value := statusToolsOverviewValue(m, counts)
	icon, iconStyle := statusToolsOverviewIcon(m, counts)
	if activity := statusToolsActivityText(m); activity != "" {
		value = statusLoadingValue(m, "loading")
		summary = statusStaleSummary(activity, summary, counts.tracked+counts.installed+counts.available > 0)
		icon, iconStyle = statusRowWorkingIcon(m, len(m.upgradingKeys) > 0 || m.rowOpKey != "" || strings.TrimSpace(m.progressText) != "")
	}
	return statusListRow{
		section:   statusSectionOverview,
		icon:      icon,
		iconStyle: iconStyle,
		label:     "Tools",
		value:     value,
		summary:   summary,
		details:   statusToolsOverviewDetails(m, counts),
		action:    statusAction{kind: statusActionOpenTools, desc: "open tools"},
	}
}

func statusToolSyncOverviewRow(m Model, counts statusToolSummary) statusListRow {
	summary := statusSampleSummary(counts.outOfSyncNames, "All tracked tools match this host.")
	value := statusCountValue(m, counts.outOfSync, "issue", "issues", "in sync")
	icon, iconStyle := statusToolSyncIcon(m, counts)
	if activity := statusToolsActivityText(m); activity != "" {
		value = statusLoadingValue(m, "checking")
		// Keep the fallback summary instead of repeating the scan activity
		// text that the Tools row already shows.
		if counts.outOfSync > 0 {
			summary = statusStaleSummary(activity, summary, true)
		}
		if statusDashboardToolSyncBusy(m) {
			icon, iconStyle = statusRowWorkingIcon(m, m.rowOpKey != "" || strings.TrimSpace(m.progressText) != "")
		}
	}
	action := statusAction{kind: statusActionOpenToolsSection, toolSection: sectionOutOfSync, desc: "open sync data"}
	if statusDashboardToolSyncActionable(m) && !statusToolsLoading(m) {
		action = statusAction{kind: statusActionSyncTools, desc: "sync tools"}
	}
	return statusListRow{
		section:   statusSectionOverview,
		icon:      icon,
		iconStyle: iconStyle,
		label:     "Tool Sync",
		value:     value,
		summary:   summary,
		details:   statusToolSyncDetails(m, counts),
		action:    action,
	}
}

func statusDotfilesAttentionRow(m Model) (statusListRow, bool) {
	p := m.palette
	repo := strings.TrimSpace(m.settings.DotsRepo)
	counts := dotHeaderCounts(m.dotsEntries)

	if config.BoolVal(m.settings.DotsDisabled) {
		icon, iconStyle := statusRowFailureIcon(m)
		return statusListRow{
			section:   statusSectionAttention,
			icon:      icon,
			iconStyle: iconStyle,
			label:     "Dotfiles",
			value:     p.styleMissing.Render("disabled"),
			summary:   "Dotfile sync is disabled for this host.",
			details:   statusDetailLines(m, "Dotfile sync is disabled for this host."),
			action:    statusAction{kind: statusActionOpenSettings, settingsRow: settingsRowDotsSync, desc: "open dotfile sync setting"},
		}, true
	}
	if repo == "" {
		return statusListRow{
			section:   statusSectionAttention,
			icon:      iconFailed,
			iconStyle: p.styleOutdated,
			label:     "Dotfiles",
			value:     p.styleOutdated.Render("not set"),
			summary:   "Set dots_repo before dotfiles can sync.",
			details:   statusDetailLines(m, "Set dots_repo before dotfiles can sync."),
			action:    statusAction{kind: statusActionOpenSettings, settingsRow: settingsRowDotsRepo, desc: "set repository"},
		}, true
	}

	if counts.OutOfSync == 0 && strings.TrimSpace(m.dotsGitStatus) == "" {
		return statusListRow{}, false
	}

	action := statusAction{kind: statusActionOpenDots, desc: "open dotfiles"}
	value := p.styleOutdated.Render("dirty")
	icon, iconStyle := statusRowWarningIcon(m)
	if counts.OutOfSync > 0 {
		value = p.styleOutdated.Render(pluralCount(counts.OutOfSync, "issue", "issues"))
		if m.dotsLoading || m.dotsPreparing {
			value = statusLoadingValue(m, "syncing")
			icon, iconStyle = statusRowWorkingIcon(m, m.dotsActiveName != "" || strings.TrimSpace(m.progressText) != "")
			action = statusAction{kind: statusActionOpenDotsIssue, desc: "open dotfiles"}
		} else {
			action = statusAction{kind: statusActionSyncDots, desc: "sync dotfiles"}
		}
	} else if statusDashboardDotsCommitActionable(m) {
		action = statusAction{kind: statusActionCommitDots, desc: "commit dotfiles"}
	}
	return statusListRow{
		section:   statusSectionAttention,
		icon:      icon,
		iconStyle: iconStyle,
		label:     "Dotfiles",
		value:     value,
		summary:   statusDotAttentionSummary(counts, m.dotsGitStatus),
		details:   statusDotAttentionDetails(m, counts),
		action:    action,
	}, true
}

func statusDotfilesOverviewRow(m Model) statusListRow {
	counts := dotHeaderCounts(m.dotsEntries)
	action := statusAction{kind: statusActionOpenDots, desc: "open dotfiles"}
	if config.BoolVal(m.settings.DotsDisabled) {
		action = statusAction{kind: statusActionOpenSettings, settingsRow: settingsRowDotsSync, desc: "open dotfile sync setting"}
	} else if strings.TrimSpace(m.settings.DotsRepo) == "" {
		action = statusAction{kind: statusActionOpenSettings, settingsRow: settingsRowDotsRepo, desc: "set repository"}
	}
	summary := statusDotOverviewSummary(m, counts)
	value := statusDotfilesOverviewValue(m, counts)
	icon, iconStyle := statusDotfilesOverviewIcon(m, counts)
	if activity := statusDotsActivityText(m); activity != "" {
		value = statusLoadingValue(m, "loading")
		summary = statusStaleSummary(activity, summary, counts.Synced+counts.OutOfSync+counts.Ignored > 0)
		icon, iconStyle = statusRowWorkingIcon(m, m.dotsActiveName != "" || strings.TrimSpace(m.progressText) != "")
	}
	return statusListRow{
		section:   statusSectionOverview,
		icon:      icon,
		iconStyle: iconStyle,
		label:     "Dotfiles",
		value:     value,
		summary:   summary,
		details:   statusDotOverviewDetails(m, counts),
		action:    action,
	}
}

func statusAutomationAttentionRow(m Model) (statusListRow, bool) {
	if !statusAutomationNeedsAttention(m) {
		return statusListRow{}, false
	}
	return statusAutomationRow(m, statusSectionAttention), true
}

func statusAutomationOverviewRow(m Model) statusListRow {
	return statusAutomationRow(m, statusSectionOverview)
}

func statusAutomationRow(m Model, section string) statusListRow {
	icon, iconStyle := statusAutomationIcon(m)
	return statusListRow{
		section:   section,
		icon:      icon,
		iconStyle: iconStyle,
		label:     "Services",
		value:     statusAutomationValue(m),
		summary:   statusAutomationSummary(m),
		details:   statusAutomationDetails(m),
		action:    statusAction{kind: statusActionOpenSettings, settingsRow: settingsRowDotsServices, desc: "open service settings"},
	}
}

func statusRowLine(m Model, row statusListRow, selected bool) string {
	p := m.palette
	icon := row.icon
	iconStyle := row.iconStyle
	if icon == "" {
		icon = iconIgnored
		iconStyle = p.styleHelp
	}
	iconText := renderCell(leftCell(listRowColumnStyle(selected, iconStyle).Render(fitCellText(icon, listIconWidth)), listIconWidth))
	labelW := max(statusLabelWidth-listIconWidth-listIconGapWidth, 1)
	label := fitCellText(row.label, labelW)
	style := p.styleNormal
	if selected {
		style = p.styleActiveText
	} else if row.muted {
		style = p.styleHelp
	}
	labelText := renderCell(leftCell(style.Render(label), labelW))
	labelCell := iconText + strings.Repeat(" ", listIconGapWidth) + labelText
	contentW := rowAvailableWidth(m.width)
	valueW := lipgloss.Width(row.value)
	summaryW := contentW - statusLabelWidth - valueW - settingsMinGap - listColumnGap
	left := []rowCell{leftCell(labelCell, statusLabelWidth)}
	if summary := strings.TrimSpace(row.summary); !selected && summary != "" && summaryW >= 16 {
		left = append(left, leftCell(p.styleHelp.Render(fitCellText(summary, summaryW)), summaryW))
	}
	return renderResponsiveGroupListRow(p, selected,
		left,
		[]rowCell{rightCell(row.value, 0)},
		contentW, settingsMinGap, listColumnGap,
	)
}

func statusRowOKIcon(m Model) (string, lipgloss.Style) {
	return iconInstalled, m.palette.styleInstalled
}

func statusRowWarningIcon(m Model) (string, lipgloss.Style) {
	return iconFailed, m.palette.styleOutdated
}

func statusRowFailureIcon(m Model) (string, lipgloss.Style) {
	return iconMissing, m.palette.styleMissing
}

func statusRowQuietIcon(m Model) (string, lipgloss.Style) {
	return iconIgnored, m.palette.styleHelp
}

func statusRowWorkingIcon(m Model, active bool) (string, lipgloss.Style) {
	if active {
		if spin := rowSpinnerIcon(m); spin != "" {
			return spin, lipgloss.NewStyle()
		}
	}
	return iconPending, m.palette.styleStatus
}

func statusToolsOverviewIcon(m Model, counts statusToolSummary) (string, lipgloss.Style) {
	switch {
	case counts.tracked == 0:
		return statusRowQuietIcon(m)
	case counts.updates > 0 || counts.outOfSync > 0:
		return statusRowWarningIcon(m)
	default:
		return statusRowOKIcon(m)
	}
}

func statusToolSyncIcon(m Model, counts statusToolSummary) (string, lipgloss.Style) {
	if counts.outOfSync > 0 {
		return statusRowWarningIcon(m)
	}
	return statusRowOKIcon(m)
}

func statusDotfilesOverviewIcon(m Model, counts app.DotFileCounts) (string, lipgloss.Style) {
	switch {
	case config.BoolVal(m.settings.DotsDisabled) || strings.TrimSpace(m.settings.DotsRepo) == "" || statusDotfilesNotLoaded(m, counts):
		return statusRowQuietIcon(m)
	case counts.OutOfSync > 0 || strings.TrimSpace(m.dotsGitStatus) != "":
		return statusRowWarningIcon(m)
	default:
		return statusRowOKIcon(m)
	}
}

func statusAutomationIcon(m Model) (string, lipgloss.Style) {
	switch {
	case m.dotsServicesRefreshing:
		return statusRowWorkingIcon(m, false)
	case strings.TrimSpace(m.dotsReminderServiceErr) != "" || strings.TrimSpace(m.dotsWatchServiceErr) != "":
		return statusRowWarningIcon(m)
	case statusAnyAutomationInstalled(m) && (strings.TrimSpace(m.settings.DotsRepo) == "" || config.BoolVal(m.settings.DotsDisabled)):
		return statusRowWarningIcon(m)
	case statusAnyAutomationInstalled(m):
		return statusRowOKIcon(m)
	default:
		return statusRowQuietIcon(m)
	}
}

func statusDoctorIcon(m Model) string {
	s := m.doctorResult.Summary
	switch {
	case s.Fail > 0:
		return iconMissing
	case s.Warn > 0:
		return iconFailed
	default:
		return iconInstalled
	}
}

func statusDoctorIconStyle(m Model) lipgloss.Style {
	s := m.doctorResult.Summary
	switch {
	case s.Fail > 0:
		return m.palette.styleMissing
	case s.Warn > 0:
		return m.palette.styleOutdated
	default:
		return m.palette.styleInstalled
	}
}

// statusDoctorCheckRows composes detail rows for a single doctor check.
// When the check carries DetailGroups, each group header renders at normal
// indent and its items are indented two spaces deeper. Falls back to flat
// Details when no groups are present.
func statusDoctorCheckRows(m Model, check app.DoctorCheck) []string {
	header := check.Label + ": " + check.Message
	rows := []string{statusDetailLine(m, header)}
	prefix := textRowContentPrefix()
	indent := "  "

	if len(check.Groups) > 0 {
		for _, g := range check.Groups {
			rows = append(rows, statusDetailLine(m, g.Header))
			for _, item := range g.Items {
				hint := doctorDetailHint(check.ID, item)
				text := strings.TrimSpace(item + hint)
				if text == "" {
					continue
				}
				width := max(m.width-lipgloss.Width(prefix)-len(indent)-screenEdgePadding, 12)
				rows = append(rows, prefix+indent+m.palette.styleHelp.Render(fitCellText(text, width)))
			}
		}
		return rows
	}

	for _, d := range check.Details {
		hint := doctorDetailHint(check.ID, d)
		text := strings.TrimSpace(d + hint)
		if text == "" {
			continue
		}
		rows = append(rows, statusDetailLine(m, text))
	}
	return rows
}

func statusDetailLine(m Model, text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	prefix := textRowContentPrefix()
	width := max(m.width-lipgloss.Width(prefix)-screenEdgePadding, 12)
	return prefix + m.palette.styleHelp.Render(fitCellText(text, width))
}

func statusActivityDetailLine(m Model, text string, cancellable bool) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	prefix := textRowContentPrefix()
	status := m.palette.styleStatus.Render(text)
	if !cancellable {
		return prefix + status
	}
	cancel := renderActionHintText(m.palette, []hintItem{rawHint("ctrl+c", "cancel")})
	return prefix + hintJoin(m.palette, status, cancel)
}

func statusDetailLines(m Model, lines ...string) []string {
	details := make([]string, 0, len(lines))
	for _, line := range lines {
		for _, part := range strings.Split(line, "\n") {
			if rendered := statusDetailLine(m, part); rendered != "" {
				details = append(details, rendered)
			}
		}
	}
	return details
}

func statusSelectedDetails(m Model, row statusListRow) []string {
	details := make([]string, 0, len(row.details)+2)
	seen := make(map[string]struct{}, len(row.details)+2)
	if summary := strings.TrimSpace(row.summary); summary != "" {
		if line := statusDetailLine(m, "Cause: "+summary); line != "" {
			details = append(details, line)
			statusRememberDetailKey(seen, line)
			statusRememberDetailKey(seen, summary)
		}
	}
	for _, detail := range row.details {
		key := statusDetailKey(detail)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		details = append(details, detail)
	}
	if hint := statusActionHintLine(m, row); hint != "" {
		details = append(details, hint)
	}
	return details
}

func statusRememberDetailKey(seen map[string]struct{}, text string) {
	if key := statusDetailKey(text); key != "" {
		seen[key] = struct{}{}
	}
}

func statusDetailKey(text string) string {
	text = strings.TrimSpace(stripANSIEscapeSequences(text))
	if text == "" {
		return ""
	}
	return strings.Join(strings.Fields(text), " ")
}

func statusActionHintLine(m Model, row statusListRow) string {
	hints := make([]hintItem, 0, 1)
	if hint, ok := statusActionHintItem(m.keys, row.action); ok {
		hints = append(hints, hint)
	}
	if len(hints) == 0 {
		return ""
	}
	return textRowHintPrefix() + m.palette.styleHelp.Render("Action: ") + renderActionHintText(m.palette, hints)
}

func statusActionHintItem(k KeyMap, action statusAction) (hintItem, bool) {
	binding, ok := statusActionHintBinding(k, action)
	if !ok {
		return hintItem{}, false
	}
	return hintFromBinding(binding), true
}

func selectedStatusActionHintItem(m Model) (hintItem, bool) {
	rows := statusRows(m)
	cursor := clampIndex(m.statusCursor, len(rows))
	return statusActionHintItem(m.keys, selectedStatusAction(rows, cursor))
}

func selectedStatusActionHintBinding(m Model) (key.Binding, bool) {
	rows := statusRows(m)
	cursor := clampIndex(m.statusCursor, len(rows))
	return statusActionHintBinding(m.keys, selectedStatusAction(rows, cursor))
}

func statusActionHintBinding(k KeyMap, action statusAction) (key.Binding, bool) {
	if action.kind == statusActionNone || strings.TrimSpace(action.desc) == "" {
		return key.Binding{}, false
	}
	binding := statusActionKeyBinding(k, action)
	help := binding.Help()
	return key.NewBinding(key.WithKeys(binding.Keys()...), key.WithHelp(help.Key, action.desc)), true
}

func statusActionKeyBinding(k KeyMap, action statusAction) key.Binding {
	switch action.kind {
	case statusActionSyncTools, statusActionSyncDots:
		return k.SyncAll
	case statusActionUpgradeTools:
		return k.UpgradeAll
	default:
		return k.Confirm
	}
}

func statusActionKeyMatches(msg tea.KeyPressMsg, k KeyMap, action statusAction) bool {
	binding, ok := statusActionHintBinding(k, action)
	return ok && key.Matches(msg, binding)
}

func statusDoctorValue(m Model) string {
	p := m.palette
	switch {
	case m.doctorRunning:
		return p.styleProvider.Render("[running]")
	case m.doctorErr != "":
		return p.styleMissing.Render("[failed]")
	case m.doctorResult != nil:
		s := m.doctorResult.Summary
		if s.Fail > 0 {
			return p.styleMissing.Render(fmt.Sprintf("[%d ok/%d warn/%d fail]", s.OK, s.Warn, s.Fail))
		}
		if s.Warn > 0 {
			return p.styleOutdated.Render(fmt.Sprintf("[%d ok/%d warn/%d fail]", s.OK, s.Warn, s.Fail))
		}
		return p.styleInstalled.Render(fmt.Sprintf("[%d ok/%d warn/%d fail]", s.OK, s.Warn, s.Fail))
	default:
		return p.styleHelp.Render("[not run]")
	}
}
