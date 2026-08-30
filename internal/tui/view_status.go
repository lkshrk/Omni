package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/app"
	textutil "github.com/lkshrk/omni/internal/text"
)

const statusLabelWidth = 18

const (
	statusSectionAttention = "Health Check"
	statusSectionOverview  = "Data"
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
	statusActionFixConfig
	statusActionFixNvmManaged
	statusActionOpenAgents
	statusActionSyncAgents
	statusActionUpgradeAgents
)

type statusAction struct {
	kind        statusActionKind
	toolSection section
	settingsRow int
	desc        string
}

type statusListRow struct {
	section        string
	icon           string
	iconStyle      lipgloss.Style
	label          string
	value          string
	summary        string
	details        []string
	action         statusAction
	muted          bool
	needsAttention bool
}

type dashboardReconcilePlanItem = app.DashboardReconcilePlanStep

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
		checked := dashboardReconcilePlanItemSelected(m, item.ID)
		style := p.styleNormal
		if !checked {
			style = p.styleHelp
		}
		rows = append(rows, pickerChoiceRow{
			selected: selected,
			mark:     dashboardReconcilePlanMark(checked),
			label:    item.Label,
			detail:   item.Detail,
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
		labelW = max(labelW, lipgloss.Width(item.Label))
		detailW = max(detailW, lipgloss.Width(item.Detail))
	}
	return max(labelW, 12), detailW
}

func dashboardReconcilePlanItems(m Model) []dashboardReconcilePlanItem {
	return app.DashboardReconcilePlan(dashboardReconcilePlanInput(m))
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
	rows := make([]statusListRow, 0, 12)
	rows = append(rows, statusAttentionRows(m, counts)...)
	rows = append(rows, statusOverviewRows(m, counts)...)
	return applyDashboardRefreshPresentation(m, rows)
}

func statusSections(m Model, rows []statusListRow) []sectionedTabSection {
	sectionOrder := []string{statusSectionAttention, statusSectionOverview}
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

func statusAttentionRows(m Model, counts app.DashboardToolSummary) []statusListRow {
	hasDotfilesAttention := statusDotfilesAttentionNeedsAttention(m)
	rows := []statusListRow{
		statusToolUpdatesAttentionRow(m, counts),
		statusToolSyncAttentionRow(m, counts),
		statusDotfilesAttentionRow(m),
	}
	// The disabled case already says so once on the Agents row; a second muted "disabled" line adds nothing.
	if agentsDashboardEnabled(m) {
		rows = append(rows, statusAgentUpdatesAttentionRow(m))
	}
	return append(rows,
		statusAgentsAttentionRow(m),
		statusAutomationAttentionRow(m),
		statusDoctorAttentionRow(m, hasDotfilesAttention),
	)
}

func statusDoctorAttentionRow(m Model, hasDotfilesAttention bool) statusListRow {
	switch {
	case m.doctorRunning:
		icon, iconStyle := statusRowWorkingIcon(m, true)
		return statusListRow{
			section:        statusSectionAttention,
			icon:           icon,
			iconStyle:      iconStyle,
			label:          "Doctor",
			value:          statusDoctorValue(m),
			summary:        "Running read-only checks.",
			details:        []string{statusActivityDetailLine(m, "Running read-only checks.", false)},
			needsAttention: true,
		}
	case m.doctorErr != "":
		summary := "Doctor could not finish: " + m.doctorErr
		return statusListRow{
			section:        statusSectionAttention,
			icon:           iconFailed,
			iconStyle:      m.palette.styleErr,
			label:          "Doctor",
			value:          statusDoctorValue(m),
			summary:        summary,
			details:        statusDetailLines(m, summary),
			action:         statusAction{kind: statusActionRunDoctor, desc: "rerun doctor"},
			needsAttention: true,
		}
	case m.doctorResult == nil:
		icon, iconStyle := statusRowQuietIcon(m)
		return statusListRow{
			section:   statusSectionAttention,
			icon:      icon,
			iconStyle: iconStyle,
			label:     "Doctor",
			value:     statusDoctorValue(m),
			summary:   "Refresh dashboard to run checks.",
			details:   statusDetailLines(m, "Refresh dashboard to run checks."),
		}
	case len(m.doctorResult.Checks) == 0:
		summary := "No individual checks were returned."
		return statusListRow{
			section:        statusSectionAttention,
			icon:           iconFailed,
			iconStyle:      m.palette.styleOutdated,
			label:          "Doctor",
			value:          m.palette.styleOutdated.Render("[warn]"),
			summary:        summary,
			details:        []string{statusDetailLine(m, summary)},
			action:         statusAction{kind: statusActionRunDoctor, desc: "rerun doctor"},
			needsAttention: true,
		}
	}

	var nonOK []app.DoctorCheck
	for _, check := range m.doctorResult.Checks {
		if check.Status == app.DoctorStatusOK {
			continue
		}
		// The dedicated Dotfiles attention row already covers this, so skip it here rather than duplicating dotfile issues across two rows.
		if hasDotfilesAttention && check.ID == "dots" {
			continue
		}
		nonOK = append(nonOK, check)
	}
	if len(nonOK) == 0 {
		icon, iconStyle := statusRowOKIcon(m)
		return statusListRow{
			section:   statusSectionAttention,
			icon:      icon,
			iconStyle: iconStyle,
			label:     "Doctor",
			value:     statusHealthOKValue(m),
			summary:   "All checks passed.",
			details:   statusDetailLines(m, "All checks passed."),
		}
	}
	row := statusDoctorSummaryRow(m, nonOK)
	row.needsAttention = true
	return row
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
	action := statusDoctorSummaryAction(m, checks)
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

func statusDoctorSummaryAction(m Model, checks []app.DoctorCheck) statusAction {
	if app.DoctorHasNvmManagedDrift(m.doctorResult) {
		return statusAction{kind: statusActionFixNvmManaged, desc: "fix nvm-managed tools"}
	}
	for _, check := range checks {
		if action := statusDoctorCheckAction(check); action.kind == statusActionFixConfig {
			return action
		}
	}
	if len(checks) == 1 {
		return statusDoctorCheckAction(checks[0])
	}
	return statusAction{kind: statusActionRunDoctor, desc: "rerun doctor"}
}

func statusDoctorCheckAction(check app.DoctorCheck) statusAction {
	switch check.ID {
	case "drift":
		if app.DoctorCheckHasNvmManagedDrift(check) {
			return statusAction{kind: statusActionFixNvmManaged, desc: "fix nvm-managed tools"}
		}
		return statusAction{kind: statusActionRunDoctor, desc: "rerun doctor"}
	case "config":
		if check.Status == app.DoctorStatusWarn && strings.Contains(check.Message, "duplicate definitions across $include") {
			return statusAction{kind: statusActionFixConfig, desc: "fix issues"}
		}
		return statusAction{kind: statusActionOpenSettings, settingsRow: settingsRowBootstrap, desc: "open bootstrap"}
	case "host":
		return statusAction{kind: statusActionOpenSettings, settingsRow: settingsRowBootstrap, desc: "open bootstrap"}
	case "providers":
		return statusAction{kind: statusActionOpenSettings, settingsRow: settingsRowProviderPriority, desc: "open provider settings"}
	case "dots":
		if strings.Contains(check.Message, "disabled") {
			return statusAction{kind: statusActionOpenSettings, settingsRow: settingsRowDotsSync, desc: "open dotfile sync setting"}
		}
		if strings.Contains(check.Message, "dots_repo") {
			return statusAction{kind: statusActionOpenSettings, settingsRow: settingsRowDotsRepo, desc: "open dotfile settings"}
		}
		return doctorDotsAction(check)
	case "dots.ignore":
		return statusAction{kind: statusActionFixConfig, desc: "fix issues"}
	case "services":
		return statusAction{kind: statusActionOpenSettings, settingsRow: settingsRowDotsServices, desc: "open service settings"}
	case "cache":
		return statusAction{kind: statusActionOpenSettings, settingsRow: settingsRowResetCache, desc: "open cache reset"}
	default:
		return statusAction{kind: statusActionRunDoctor, desc: "rerun doctor"}
	}
}

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

func statusOverviewRows(m Model, counts app.DashboardToolSummary) []statusListRow {
	return []statusListRow{
		statusToolsOverviewRow(m, counts),
		statusDotfilesOverviewRow(m),
		statusAgentsOverviewRow(m),
		statusAutomationOverviewRow(m),
	}
}

func statusToolUpdatesAttentionRow(m Model, counts app.DashboardToolSummary) statusListRow {
	active, waiting := statusUpgradeNames(m)
	busy := len(active) > 0 || len(waiting) > 0 || m.upgradingKeys["*"]
	if counts.Updates == 0 && !busy {
		icon, iconStyle := statusRowOKIcon(m)
		return statusListRow{
			section:   statusSectionAttention,
			icon:      icon,
			iconStyle: iconStyle,
			label:     "Tool Updates",
			value:     statusHealthOKValue(m),
			summary:   "All tools up to date.",
			details:   statusDetailLines(m, "All tools up to date."),
		}
	}
	value := statusCountValue(m, counts.Updates, "update", "updates", "current")
	summary := statusSampleSummary(counts.UpdateNames, "No outdated tools.")
	details := statusOverflowDetails(m, counts.UpdateNames)
	action := statusAction{kind: statusActionUpgradeTools, desc: "upgrade all tools"}
	icon, iconStyle := statusRowWarningIcon(m)
	if busy {
		summary = statusUpgradeSummary(active, waiting, counts.UpdateNames)
		details = statusUpgradeDetails(m, active, waiting, counts.UpdateNames)
		value = statusUpgradeValue(m, active, waiting)
		action = statusAction{kind: statusActionOpenToolsSection, toolSection: sectionUpdates, desc: "open updates"}
		icon, iconStyle = statusRowWorkingIcon(m, len(active) > 0)
	}
	return statusListRow{
		section:        statusSectionAttention,
		icon:           icon,
		iconStyle:      iconStyle,
		label:          "Tool Updates",
		value:          value,
		summary:        summary,
		details:        details,
		action:         action,
		needsAttention: true,
	}
}

func statusToolSyncAttentionRow(m Model, counts app.DashboardToolSummary) statusListRow {
	busy := statusDashboardToolSyncBusy(m)
	if counts.OutOfSync == 0 && !busy {
		icon, iconStyle := statusRowOKIcon(m)
		return statusListRow{
			section:   statusSectionAttention,
			icon:      icon,
			iconStyle: iconStyle,
			label:     "Tool Sync",
			value:     statusHealthOKValue(m),
			summary:   "All tracked tools match this host.",
			details:   statusToolSyncDetails(m, counts),
		}
	}
	action := statusAction{kind: statusActionOpenToolsSection, toolSection: sectionOutOfSync, desc: "open sync issues"}
	value := statusCountValue(m, counts.OutOfSync, "issue", "issues", "in sync")
	summary := statusSampleSummary(counts.OutOfSyncNames, "All tracked tools match this host.")
	details := statusToolSyncDetails(m, counts)
	icon, iconStyle := statusRowWarningIcon(m)
	if busy {
		value = statusLoadingValue(m, "syncing")
		summary = statusStaleSummary(statusToolsActivityText(m), summary, counts.OutOfSync > 0)
		icon, iconStyle = statusRowWorkingIcon(m, m.rowOpKey != "" || strings.TrimSpace(m.progressText) != "")
	}
	if !statusToolsLoading(m) {
		switch {
		case statusDashboardToolSyncActionable(m):
			action = statusAction{kind: statusActionSyncTools, desc: "sync tools"}
		case counts.OutOfSync > 0 && statusDashboardNvmManagedActionable(m):
			action = statusAction{kind: statusActionFixNvmManaged, desc: "fix nvm-managed tools"}
		}
	}
	return statusListRow{
		section:        statusSectionAttention,
		icon:           icon,
		iconStyle:      iconStyle,
		label:          "Tool Sync",
		value:          value,
		summary:        summary,
		details:        details,
		action:         action,
		needsAttention: true,
	}
}

func statusToolsOverviewRow(m Model, counts app.DashboardToolSummary) statusListRow {
	summary := statusToolsOverviewBreakdown(counts)
	value := statusToolsOverviewDataValue(m, counts)
	icon, iconStyle := statusToolsOverviewIcon(m, counts)
	value, summary, icon, iconStyle = applyStatusRowActivity(m, value, summary, icon, iconStyle, statusRowActivity{
		text:    statusToolsActivityText(m),
		hasData: counts.Tracked+counts.Installed+counts.Available > 0,
		working: len(m.upgradingKeys) > 0 || m.rowOpKey != "" || strings.TrimSpace(m.progressText) != "",
	})
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

func statusDotfilesAttentionNeedsAttention(m Model) bool {
	if dotsViewDisabled(m) || dotsViewUnconfigured(m) {
		return true
	}
	counts := app.DotStatusesFileCounts(m.dotsEntries)
	return counts.OutOfSync > 0 || strings.TrimSpace(m.dotsGitStatus) != ""
}

func statusDotfilesAttentionRow(m Model) statusListRow {
	p := m.palette
	counts := app.DotStatusesFileCounts(m.dotsEntries)

	if dotsViewDisabled(m) {
		icon, iconStyle := statusRowFailureIcon(m)
		return statusListRow{
			section:        statusSectionAttention,
			icon:           icon,
			iconStyle:      iconStyle,
			label:          "Dotfiles",
			value:          p.styleMissing.Render("disabled"),
			summary:        "Dotfile sync is disabled for this host.",
			details:        statusDetailLines(m, "Dotfile sync is disabled for this host."),
			action:         statusAction{kind: statusActionOpenSettings, settingsRow: settingsRowDotsSync, desc: "open dotfile sync setting"},
			needsAttention: true,
		}
	}
	if dotsViewUnconfigured(m) {
		return statusListRow{
			section:        statusSectionAttention,
			icon:           iconFailed,
			iconStyle:      p.styleOutdated,
			label:          "Dotfiles",
			value:          p.styleOutdated.Render("not set"),
			summary:        "Set dots_repo before dotfiles can sync.",
			details:        statusDetailLines(m, "Set dots_repo before dotfiles can sync."),
			action:         statusAction{kind: statusActionOpenSettings, settingsRow: settingsRowDotsRepo, desc: "set repository"},
			needsAttention: true,
		}
	}

	if counts.OutOfSync == 0 && strings.TrimSpace(m.dotsGitStatus) == "" {
		icon, iconStyle := statusRowOKIcon(m)
		return statusListRow{
			section:   statusSectionAttention,
			icon:      icon,
			iconStyle: iconStyle,
			label:     "Dotfiles",
			value:     statusHealthOKValue(m),
			summary:   "All dotfile entries synced.",
			details:   statusDotAttentionDetails(m, counts),
		}
	}

	action := statusAction{kind: statusActionOpenDots, desc: "open dotfiles"}
	value := p.styleOutdated.Render("dirty")
	icon, iconStyle := statusRowWarningIcon(m)
	if counts.OutOfSync > 0 {
		value = p.styleOutdated.Render(textutil.PluralCount(counts.OutOfSync, "issue", "issues"))
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
		section:        statusSectionAttention,
		icon:           icon,
		iconStyle:      iconStyle,
		label:          "Dotfiles",
		value:          value,
		summary:        statusDotAttentionSummary(counts, m.dotsGitStatus),
		details:        statusDotAttentionDetails(m, counts),
		action:         action,
		needsAttention: true,
	}
}

func statusDotfilesOverviewRow(m Model) statusListRow {
	counts := app.DotStatusesFileCounts(m.dotsEntries)
	action := statusAction{kind: statusActionOpenDots, desc: "open dotfiles"}
	if dotsViewDisabled(m) {
		action = statusAction{kind: statusActionOpenSettings, settingsRow: settingsRowDotsSync, desc: "open dotfile sync setting"}
	} else if dotsViewUnconfigured(m) {
		action = statusAction{kind: statusActionOpenSettings, settingsRow: settingsRowDotsRepo, desc: "set repository"}
	}
	summary := statusDotOverviewSummary(m, counts)
	value := statusDotfilesOverviewDataValue(m, counts)
	icon, iconStyle := statusDotfilesOverviewIcon(m, counts)
	value, summary, icon, iconStyle = applyStatusRowActivity(m, value, summary, icon, iconStyle, statusRowActivity{
		text:    statusDotsActivityText(m),
		hasData: counts.Synced+counts.OutOfSync+counts.Ignored > 0,
		working: m.dotsActiveName != "" || strings.TrimSpace(m.progressText) != "",
	})
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

func statusAutomationAttentionRow(m Model) statusListRow {
	if !statusAutomationNeedsAttention(m) {
		icon, iconStyle := statusRowOKIcon(m)
		return statusListRow{
			section:   statusSectionAttention,
			icon:      icon,
			iconStyle: iconStyle,
			label:     "Services",
			value:     statusHealthOKValue(m),
			summary:   "Automation services healthy.",
			details:   statusAutomationDetails(m),
		}
	}
	row := statusAutomationRow(m, statusSectionAttention)
	row.needsAttention = true
	return row
}

func statusAutomationOverviewRow(m Model) statusListRow {
	return statusAutomationRow(m, statusSectionOverview)
}

func statusAutomationRow(m Model, section string) statusListRow {
	icon, iconStyle := statusAutomationIcon(m)
	summary := statusAutomationSummary(m)
	value := statusAutomationValue(m)
	if section == statusSectionOverview {
		summary = statusAutomationBreakdown(m)
		value = statusAutomationOverviewDataValue(m)
	}
	activityText := ""
	if m.dotsServicesRefreshing {
		activityText = "Refreshing service status…"
	}
	value, summary, icon, iconStyle = applyStatusRowActivity(m, value, summary, icon, iconStyle, statusRowActivity{
		text:    activityText,
		hasData: dashboardDotsAutomationStatus(m).Known,
		working: m.dotsServicesRefreshing,
	})
	return statusListRow{
		section:   section,
		icon:      icon,
		iconStyle: iconStyle,
		label:     "Services",
		value:     value,
		summary:   summary,
		details:   statusAutomationDetails(m),
		action:    statusAction{kind: statusActionOpenSettings, settingsRow: settingsRowDotsServices, desc: "open service settings"},
	}
}

func statusAgentsOverviewRow(m Model) statusListRow {
	view := agentsDashboardViewFor(m)
	summary := statusAgentsOverviewSummary(view, statusAgentsCounts(m))
	value := statusAgentsOverviewDataValue(m, view)
	icon, iconStyle := statusAgentsOverviewIcon(m)
	working := view.enabled && statusAgentsLoading(m)
	activityText := ""
	if working {
		activityText = "Loading agents…"
	}
	value, summary, icon, iconStyle = applyStatusRowActivity(m, value, summary, icon, iconStyle, statusRowActivity{
		text:      activityText,
		hasData:   view.managed() > 0,
		working:   working,
		keepValue: true,
	})
	return statusListRow{
		section:   statusSectionOverview,
		icon:      icon,
		iconStyle: iconStyle,
		label:     "Agents",
		value:     value,
		summary:   summary,
		details:   statusAgentsOverviewDetails(m, view),
		action:    statusAction{kind: statusActionOpenAgents, desc: "open agents"},
		muted:     !view.enabled,
	}
}

func statusAgentsOverviewIcon(m Model) (string, lipgloss.Style) {
	if !agentsDashboardEnabled(m) {
		return statusRowQuietIcon(m)
	}
	if statusAgentsLoading(m) {
		return statusRowWorkingIcon(m, true)
	}
	if m.agentsRowsErr != nil {
		return statusRowWarningIcon(m)
	}
	counts := statusAgentsCounts(m)
	if counts.OutOfSync() > 0 || counts.Outdated() > 0 {
		return statusRowWarningIcon(m)
	}
	return statusRowOKIcon(m)
}

// Outdated is disjoint from out-of-sync (see agentsDashCounts), so without its own row the Agents row's "in sync" verdict silently hides every pending upgrade.
func statusAgentUpdatesAttentionRow(m Model) statusListRow {
	if row, ok := statusAgentsReadinessRow(m, "Agent Updates"); ok {
		return row
	}
	counts := statusAgentsCounts(m)
	if counts.Outdated() == 0 {
		icon, iconStyle := statusRowOKIcon(m)
		if statusAgentsLoading(m) {
			icon, iconStyle = statusRowWorkingIcon(m, true)
		}
		return statusListRow{
			section:   statusSectionAttention,
			icon:      icon,
			iconStyle: iconStyle,
			label:     "Agent Updates",
			value:     statusHealthOKValue(m),
			summary:   "All agent resources up to date.",
			details:   statusDetailLines(m, "All agent resources up to date."),
		}
	}
	icon, iconStyle := statusRowWarningIcon(m)
	if statusAgentsLoading(m) {
		icon, iconStyle = statusRowWorkingIcon(m, true)
	}
	names := statusAgentsOutdatedNames(m)
	return statusListRow{
		section:        statusSectionAttention,
		icon:           icon,
		iconStyle:      iconStyle,
		label:          "Agent Updates",
		value:          statusCountValue(m, counts.Outdated(), "upgrade", "upgrades", "current"),
		summary:        statusSampleSummary(names, "No outdated agent resources."),
		details:        statusAgentUpdatesDetails(m, counts, names),
		action:         statusAction{kind: statusActionUpgradeAgents, desc: "upgrade all agents"},
		needsAttention: true,
	}
}

func statusAgentsAttentionRow(m Model) statusListRow {
	view := agentsDashboardViewFor(m)
	counts := statusAgentsCounts(m)
	if !view.enabled {
		icon, iconStyle := statusRowQuietIcon(m)
		return statusListRow{
			section:   statusSectionAttention,
			icon:      icon,
			iconStyle: iconStyle,
			label:     "Agents",
			value:     m.palette.styleHelp.Render("disabled"),
			summary:   "Agents disabled for this host.",
			details:   statusDetailLines(m, "Agents disabled for this host."),
			muted:     true,
		}
	}
	if row, ok := statusAgentsReadinessRow(m, "Agents"); ok {
		return row
	}
	if m.agentsRowsErr != nil {
		icon, iconStyle := statusRowWarningIcon(m)
		return statusListRow{
			section: statusSectionAttention, icon: icon, iconStyle: iconStyle, label: "Agents",
			value: statusCountValue(m, 1, "issue", "issues", "in sync"), summary: m.agentsRowsErr.Error(),
			details: statusAgentsAttentionDetails(m, counts, view), action: statusAction{kind: statusActionOpenAgents, desc: "open agents"}, needsAttention: true,
		}
	}
	if counts.OutOfSync() == 0 {
		icon, iconStyle := statusRowOKIcon(m)
		if m.apmRunning {
			icon, iconStyle = statusRowWorkingIcon(m, true)
		}
		return statusListRow{
			section:   statusSectionAttention,
			icon:      icon,
			iconStyle: iconStyle,
			label:     "Agents",
			value:     statusHealthOKValue(m),
			summary:   "All managed agents items in sync.",
			details:   statusAgentsAttentionDetails(m, counts, view),
		}
	}
	icon, iconStyle := statusRowWarningIcon(m)
	value := statusCountValue(m, counts.OutOfSync(), "issue", "issues", "in sync")
	action := statusAction{kind: statusActionOpenAgents, desc: "open agents"}
	if m.apmRunning {
		icon, iconStyle = statusRowWorkingIcon(m, true)
	}
	return statusListRow{
		section:        statusSectionAttention,
		icon:           icon,
		iconStyle:      iconStyle,
		label:          "Agents",
		value:          value,
		summary:        statusAgentsAttentionSummary(m, counts),
		details:        statusAgentsAttentionDetails(m, counts, view),
		action:         action,
		needsAttention: true,
	}
}

func statusAgentsReadinessRow(m Model, label string) (statusListRow, bool) {
	guidance := agentsReadinessGuidance(m)
	if guidance == "" {
		return statusListRow{}, false
	}
	icon, iconStyle := statusRowWarningIcon(m)
	value := statusCountValue(m, 1, "issue", "issues", "ready")
	if m.agentsReadinessPending {
		icon, iconStyle = statusRowWorkingIcon(m, true)
		value = statusLoadingValue(m, "checking")
	}
	return statusListRow{
		section: statusSectionAttention, icon: icon, iconStyle: iconStyle, label: label,
		value: value, summary: guidance, details: statusDetailLines(m, guidance), needsAttention: true,
	}, true
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
	// The badge is right-aligned against contentW, so one longer than the space the label column leaves would push the row past the terminal width.
	value := fitStyledText(row.value, max(contentW-statusLabelWidth-settingsMinGap, 1))
	valueW := lipgloss.Width(value)
	summaryW := contentW - statusLabelWidth - valueW - settingsMinGap - listColumnGap
	left := []rowCell{leftCell(labelCell, statusLabelWidth)}
	if summary := strings.TrimSpace(row.summary); !selected && summary != "" && summaryW >= 16 {
		left = append(left, leftCell(p.styleHelp.Render(fitCellText(summary, summaryW)), summaryW))
	}
	return renderResponsiveGroupListRow(p, selected,
		left,
		[]rowCell{rightCell(value, 0)},
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

func statusToolsOverviewIcon(m Model, counts app.DashboardToolSummary) (string, lipgloss.Style) {
	switch {
	case counts.Tracked == 0:
		return statusRowQuietIcon(m)
	case counts.Updates > 0 || counts.OutOfSync > 0:
		return statusRowWarningIcon(m)
	default:
		return statusRowOKIcon(m)
	}
}

func statusDotfilesOverviewIcon(m Model, counts app.DotFileCounts) (string, lipgloss.Style) {
	switch {
	case dotsViewBlocked(m) || statusDotfilesNotLoaded(m, counts):
		return statusRowQuietIcon(m)
	case counts.OutOfSync > 0 || strings.TrimSpace(m.dotsGitStatus) != "":
		return statusRowWarningIcon(m)
	default:
		return statusRowOKIcon(m)
	}
}

func statusAutomationIcon(m Model) (string, lipgloss.Style) {
	switch {
	case strings.TrimSpace(m.dotsReminderServiceErr) != "" || strings.TrimSpace(m.dotsWatchServiceErr) != "":
		return statusRowWarningIcon(m)
	case statusAnyAutomationInstalled(m) && dotsViewBlocked(m):
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

// With DetailGroups each group header renders at normal indent and its items two spaces deeper; falls back to flat Details when no groups are present.
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
	return statusDetailLineIndented(m, text)
}

// Preserves leading spaces so wrapped detail lines keep their hanging indent (TrimSpace would strip it).
func statusDetailLineIndented(m Model, text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	text = strings.TrimRight(text, " ")
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
	quit := renderActionHintText(m.palette, []hintItem{rawHint("ctrl+c", "quit")})
	return prefix + hintJoin(m.palette, status, quit)
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
	// Data rows carry stat breakdowns, not problem statements — no Cause framing.
	if summary := strings.TrimSpace(row.summary); summary != "" && row.section != statusSectionOverview {
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
	case statusActionUpgradeTools, statusActionUpgradeAgents:
		return k.UpgradeAll
	case statusActionFixConfig:
		return k.Fallback
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
