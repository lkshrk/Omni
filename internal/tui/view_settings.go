package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/app"
)

const (
	settingLabelWidth = 28
	firstColumnGap    = listColumnGap * 2
	settingsMinGap    = listColumnGap * 3
	groupsMinGap      = listColumnGap * 3
)

const (
	settingsRowAutoImport = iota
	settingsRowProviderPriority
	settingsRowDotsRepo
	settingsRowDotsSync
	settingsRowDotsReminder
	settingsRowDotsReminderInterval
	settingsRowDotsWatch
	settingsRowDotsWatchDebounce
	settingsRowDotsServices
	settingsRowDotsCommit
	settingsRowDotsPush
	settingsRowAgentsEnabled
	settingsRowSkillsEnabled
	settingsRowMcpEnabled
	settingsRowPluginsEnabled
	settingsRowAgentsUse
	settingsRowDoctor
	settingsRowTraceLog
	settingsRowBootstrap
	settingsRowResetSettings
	settingsRowResetCache
	settingsRowCount
)

const numSettingRows = settingsRowCount

type settingsRowMeta struct {
	label   string
	section string
	hint    hintContext
	danger  bool
	action  app.SettingsActionID
	valueFn func(m Model) string
	helpFn  func(m Model) string
}

var settingsRows = []settingsRowMeta{
	settingsRowAutoImport: {
		label:   "Import Installed Tools",
		section: "Tools",
		hint:    hintCtxSettingsToggle,
		action:  app.SettingsActionToggleAutoImport,
		valueFn: func(m Model) string { return settingsOnOff(m.palette, m.settings.AutoImport) },
		helpFn: func(m Model) string {
			return m.palette.styleHelp.Render("Add newly installed tools to the config on every sync.")
		},
	},
	settingsRowProviderPriority: {
		label:   "Provider Priority",
		section: "Tools",
		hint:    hintCtxSettingsEdit,
		valueFn: func(m Model) string { return settingsPriorityVal(m.palette, m.providerPriorityDisplay()) },
		helpFn: func(m Model) string {
			return m.palette.styleHelp.Render("Per-machine order of concrete package managers. Reorder to set priority; space toggles a provider off.")
		},
	},
	settingsRowDotsRepo: {
		label:   "Repository",
		section: "Dotfiles",
		hint:    hintCtxSettingsEdit,
		valueFn: func(m Model) string { return settingsDotsRepoVal(m) },
		helpFn:  func(m Model) string { return m.palette.styleHelp.Render("Path to your dotfiles git repository.") },
	},
	settingsRowDotsSync: {
		label:   "Dotfile Sync",
		section: "Dotfiles",
		hint:    hintCtxSettingsDotsSync,
		valueFn: func(m Model) string { return settingsOnOff(m.palette, !dotsViewDisabled(m)) },
		helpFn: func(m Model) string {
			if dotsViewDisabled(m) {
				return m.palette.styleHelp.Render("Re-enable dotfile sync and restore managed symlinks.")
			}
			return m.palette.styleHelp.Render("Disable sync: remove managed symlinks and copy files back locally.")
		},
	},
	settingsRowDotsReminder: {
		label:   "Reminder Notifications",
		section: "Dotfiles",
		hint:    hintCtxSettingsToggle,
		valueFn: func(m Model) string {
			return settingsServiceVal(m.palette, m.dotsReminderService != nil && m.dotsReminderService.Installed, m.dotsReminderServiceErr)
		},
		helpFn: func(m Model) string {
			return settingsServiceHelp(m.palette, "reminder", m.dotsReminderService != nil && m.dotsReminderService.Installed, m.dotsReminderServiceErr, "Install a native reminder timer with desktop notifications.", dotsViewUnconfigured(m))
		},
	},
	settingsRowDotsReminderInterval: {
		label:   "Reminder Interval",
		section: "Dotfiles",
		hint:    hintCtxSettingsDuration,
		valueFn: func(m Model) string {
			return settingsDurationVal(m.palette, m.currentDotsReminderInterval(), m.dotsReminderServiceErr)
		},
		helpFn: func(m Model) string {
			return settingsDurationHelp(m.palette, "reminder", m.dotsReminderService != nil && m.dotsReminderService.Installed, m.dotsReminderServiceErr)
		},
	},
	settingsRowDotsWatch: {
		label:   "Watch Sync",
		section: "Dotfiles",
		hint:    hintCtxSettingsToggle,
		valueFn: func(m Model) string {
			return settingsServiceVal(m.palette, m.dotsWatchService != nil && m.dotsWatchService.Installed, m.dotsWatchServiceErr)
		},
		helpFn: func(m Model) string {
			return settingsServiceHelp(m.palette, "watch", m.dotsWatchService != nil && m.dotsWatchService.Installed, m.dotsWatchServiceErr, "Install a native watcher that syncs links after changes; it never commits the current branch or pushes.", dotsViewUnconfigured(m))
		},
	},
	settingsRowDotsWatchDebounce: {
		label:   "Watch Debounce",
		section: "Dotfiles",
		hint:    hintCtxSettingsDuration,
		valueFn: func(m Model) string {
			return settingsDurationVal(m.palette, m.currentDotsWatchDebounce(), m.dotsWatchServiceErr)
		},
		helpFn: func(m Model) string {
			return settingsDurationHelp(m.palette, "watch", m.dotsWatchService != nil && m.dotsWatchService.Installed, m.dotsWatchServiceErr)
		},
	},
	settingsRowDotsServices: {
		label:   "Service Status",
		section: "Dotfiles",
		hint:    hintCtxSettingsStatus,
		valueFn: func(m Model) string { return settingsServicesVal(m) },
		helpFn: func(m Model) string {
			return m.palette.styleHelp.Render("Native service file status for reminders and watch sync.")
		},
	},
	settingsRowDotsCommit: {
		label:   "Commit Changes",
		section: "Dotfiles",
		hint:    hintCtxSettingsToggle,
		action:  app.SettingsActionToggleDotsCommit,
		valueFn: func(m Model) string {
			if m.settings.DotsGit.AutoPush {
				return m.palette.styleHelp.Render("[──]")
			}
			return settingsOnOff(m.palette, m.settings.DotsGit.AutoCommit)
		},
		helpFn: func(m Model) string {
			if m.settings.DotsGit.AutoPush {
				return m.palette.styleHelp.Render("Implied by Push Changes.")
			}
			return m.palette.styleHelp.Render("Commit automatically after dots add/remove/variant operations; does not affect Watch Sync.")
		},
	},
	settingsRowDotsPush: {
		label:   "Push Changes",
		section: "Dotfiles",
		hint:    hintCtxSettingsToggle,
		action:  app.SettingsActionToggleDotsPush,
		valueFn: func(m Model) string { return settingsOnOff(m.palette, m.settings.DotsGit.AutoPush) },
		helpFn: func(m Model) string {
			return m.palette.styleHelp.Render("Push (and commit) automatically after dots add/remove/variant operations; does not affect Watch Sync.")
		},
	},
	settingsRowAgentsEnabled: {
		label:   "Agent Skills",
		section: "Agents",
		hint:    hintCtxSettingsAgents,
		valueFn: func(m Model) string { return settingsOnOff(m.palette, m.agentsEnabled) },
		helpFn: func(m Model) string {
			if m.agentsEnabled {
				return m.palette.styleHelp.Render("Disable agent skill sync/import/upgrade for this machine.")
			}
			return m.palette.styleHelp.Render("Re-enable agent skill sync/import/upgrade.")
		},
	},
	settingsRowSkillsEnabled: {
		label:   "Skills",
		section: "Agents",
		hint:    hintCtxSettingsAgents,
		valueFn: func(m Model) string { return settingsOnOff(m.palette, m.skillsEnabled) },
		helpFn: func(m Model) string {
			if m.skillsEnabled {
				return m.palette.styleHelp.Render("Disable skill-package management for this machine.")
			}
			return m.palette.styleHelp.Render("Re-enable skill-package management.")
		},
	},
	settingsRowMcpEnabled: {
		label:   "MCP Servers",
		section: "Agents",
		hint:    hintCtxSettingsAgents,
		valueFn: func(m Model) string { return settingsOnOff(m.palette, m.mcpEnabled) },
		helpFn: func(m Model) string {
			if m.mcpEnabled {
				return m.palette.styleHelp.Render("Disable MCP server management for this machine.")
			}
			return m.palette.styleHelp.Render("Re-enable MCP server management.")
		},
	},
	settingsRowPluginsEnabled: {
		label:   "Plugins",
		section: "Agents",
		hint:    hintCtxSettingsAgents,
		valueFn: func(m Model) string { return settingsOnOff(m.palette, m.pluginsEnabled) },
		helpFn: func(m Model) string {
			if m.pluginsEnabled {
				return m.palette.styleHelp.Render("Disable plugin management for this machine.")
			}
			return m.palette.styleHelp.Render("Re-enable plugin management.")
		},
	},
	settingsRowAgentsUse: {
		label:   "Agents",
		section: "Agents",
		hint:    hintCtxSettingsEdit,
		valueFn: func(m Model) string { return settingsAgentsUseVal(m) },
		helpFn: func(m Model) string {
			return m.palette.styleHelp.Render("Which installed agents skills are installed to on this machine.")
		},
	},
	settingsRowDoctor: {
		label:   "Run Doctor",
		section: "Maintenance",
		hint:    hintCtxSettingsEdit,
		valueFn: func(m Model) string { return settingsDoctorVal(m) },
		helpFn: func(m Model) string {
			return m.palette.styleHelp.Render("Run read-only checks for config, host setup, providers, dotfiles, services, and cache.")
		},
	},
	settingsRowTraceLog: {
		label:   "Command Log",
		section: "Maintenance",
		hint:    hintCtxSettingsEdit,
		valueFn: func(m Model) string { return m.palette.styleHelp.Render("[view]") },
		helpFn:  func(m Model) string { return m.palette.styleHelp.Render("View recent commands Omni ran and why.") },
	},
	settingsRowBootstrap: {
		label:   "Run Bootstrap Again",
		section: "Maintenance",
		hint:    hintCtxSettingsDanger,
		danger:  true,
		valueFn: func(m Model) string { return m.palette.styleHelp.Render("[run]") },
		helpFn: func(m Model) string {
			return m.palette.styleHelp.Render("Run the guided bootstrap flow for this host again.")
		},
	},
	settingsRowResetSettings: {
		label:   "Reset Settings",
		section: "Maintenance",
		hint:    hintCtxSettingsDanger,
		danger:  true,
		valueFn: func(m Model) string { return m.palette.styleHelp.Render("[reset]") },
		helpFn: func(m Model) string {
			return m.palette.styleHelp.Render("Restore all settings to defaults (tools, hosts, and groups preserved).")
		},
	},
	settingsRowResetCache: {
		label:   "Reset Cache",
		section: "Maintenance",
		hint:    hintCtxSettingsDanger,
		danger:  true,
		valueFn: func(m Model) string { return m.palette.styleHelp.Render("[reset]") },
		helpFn: func(m Model) string {
			return m.palette.styleHelp.Render("Delete and reinitialise the tool cache database.")
		},
	},
}

type settingsDurationChoice struct {
	label string
	value time.Duration
}

func formatSettingLabel(label string) string {
	return fmt.Sprintf("%-*s", settingLabelWidth, label)
}

func settingsOnOff(p palette, on bool) string {
	if on {
		return p.styleInstalled.Render("[ON]")
	}
	return p.styleMissing.Render("[OFF]")
}

func settingsPriorityVal(p palette, pv []string) string {
	if len(pv) == 0 {
		return p.styleProvider.Render("[default]")
	}
	return p.styleProvider.Render("[" + strings.Join(pv, " › ") + "]")
}

func settingsDotsRepoVal(m Model) string {
	v := dotsRepoPathForView(m)
	if v == "" {
		return m.palette.styleHelp.Render("[not set]")
	}
	contentW := rowAvailableWidth(m.width)
	avail := max(contentW-settingLabelWidth-settingsMinGap, 12)
	return m.palette.styleProvider.Render(truncatePath(v, avail))
}

func settingsServiceVal(p palette, installed bool, statusErr string) string {
	if statusErr != "" {
		return p.styleHelp.Render("[n/a]")
	}
	return settingsOnOff(p, installed)
}

func settingsDurationVal(p palette, duration time.Duration, statusErr string) string {
	if statusErr != "" {
		return p.styleHelp.Render("[n/a]")
	}
	return p.styleProvider.Render("[" + formatSettingsDuration(duration) + "]")
}

func settingsServicesVal(m Model) string {
	p := m.palette
	if m.dotsReminderServiceErr != "" || m.dotsWatchServiceErr != "" {
		return p.styleHelp.Render("[n/a]")
	}
	installed := 0
	if m.dotsReminderService != nil && m.dotsReminderService.Installed {
		installed++
	}
	if m.dotsWatchService != nil && m.dotsWatchService.Installed {
		installed++
	}
	return p.styleProvider.Render(fmt.Sprintf("[%d/2 on]", installed))
}

func settingsDoctorVal(m Model) string {
	p := m.palette
	switch {
	case m.doctorRunning:
		return p.styleProvider.Render("[running]")
	case m.doctorErr != "":
		return p.styleMissing.Render("[failed]")
	case m.doctorResult != nil:
		return p.styleProvider.Render(fmt.Sprintf("[%d ok/%d warn/%d fail]", m.doctorResult.Summary.OK, m.doctorResult.Summary.Warn, m.doctorResult.Summary.Fail))
	default:
		return p.styleHelp.Render("[run]")
	}
}

func settingsServiceHelp(p palette, name string, installed bool, statusErr string, enableCopy string, unconfigured bool) string {
	if unconfigured {
		return p.styleHelp.Render("Set a dotfiles repository before enabling " + name + ".")
	}
	if statusErr != "" {
		return p.styleHelp.Render(statusErr)
	}
	if installed {
		return p.styleHelp.Render("Native " + name + " service is installed; space disables it.")
	}
	return p.styleHelp.Render(enableCopy)
}

func settingsDurationHelp(p palette, name string, installed bool, statusErr string) string {
	if statusErr != "" {
		return p.styleHelp.Render(statusErr)
	}
	if installed {
		return p.styleHelp.Render("Update the installed " + name + " service interval.")
	}
	return p.styleHelp.Render("Choose the " + name + " service value used on the next enable.")
}

func renderSettings(m Model) string {
	p := m.palette
	var buf scrollBuf
	write := buf.write
	rowInset := ""
	detailPrefix := textRowContentPrefix()
	hintPrefix := textRowHintPrefix()
	contentW := rowAvailableWidth(m.width)

	write("\n")

	for i, row := range settingsRows {
		value := row.valueFn(m)
		help := row.helpFn(m)
		if i == 0 || row.section != settingsRows[i-1].section {
			if i > 0 {
				write("\n")
			}
			if row.danger {
				write(renderSectionHeaderDanger(p, row.section, m.width) + "\n")
			} else {
				write(renderSectionHeader(p, row.section, m.width) + "\n")
			}
		}

		isSettingsCursor := i == m.settingsCursor && !m.cursorHidden
		if isSettingsCursor {
			buf.markCursor()
		}

		lbl := p.styleNormal
		if row.danger {
			lbl = p.styleDangerLabel
		}

		if i == m.serviceDurationRow && m.editingServiceDuration {
			write(renderResponsiveGroupListRow(p, true,
				[]rowCell{leftCell(p.styleActiveText.Render(rowInset+formatSettingLabel(row.label)), settingLabelWidth+lipgloss.Width(rowInset))},
				[]rowCell{rightCell(p.styleProvider.Render("[editing]"), 0)},
				contentW, settingsMinGap, listColumnGap,
			) + "\n")
			write(renderSettingsDurationPicker(m, detailPrefix) + "\n")
			write(renderContextHints(m, hintCtxSettingsDurationEdit, hintPrefix) + "\n")
			continue
		}

		if m.dangerConfirmRow == i {
			var confirmLabel string
			if row.danger {
				confirmLabel = lipgloss.NewStyle().Bold(true).Foreground(p.colDanger).Render(rowInset + formatSettingLabel(row.label))
			} else {
				confirmLabel = p.styleActiveText.Render(rowInset + formatSettingLabel(row.label))
			}
			write(renderFixedGroupListRow(p, true,
				[]rowCell{leftCell(confirmLabel, settingLabelWidth+lipgloss.Width(rowInset))},
				nil,
				firstColumnGap, listColumnGap,
			) + "\n")
			if i == settingsRowDotsSync {
				write(renderSettingsDotsDisableKeepLocalPrompt(m, detailPrefix) + "\n")
			} else {
				write(renderContextHints(m, hintCtxSettingsDanger, hintPrefix) + "\n")
			}
			continue
		}

		if isSettingsCursor {
			labelStyle := p.styleActiveText
			if row.danger {
				labelStyle = p.styleDangerSection
			}
			write(renderResponsiveGroupListRow(p, true,
				[]rowCell{leftCell(labelStyle.Render(rowInset+formatSettingLabel(row.label)), settingLabelWidth+lipgloss.Width(rowInset))},
				[]rowCell{rightCell(value, 0)},
				contentW, settingsMinGap, listColumnGap,
			) + "\n")
		} else {
			write(renderResponsiveGroupListRow(p, false,
				[]rowCell{leftCell(lbl.Render(rowInset+formatSettingLabel(row.label)), settingLabelWidth+lipgloss.Width(rowInset))},
				[]rowCell{rightCell(value, 0)},
				contentW, settingsMinGap, listColumnGap,
			) + "\n")
		}
		if isSettingsCursor {
			if i == settingsRowDotsServices {
				write(renderDotsServiceDashboard(m, detailPrefix) + "\n")
			} else if i == settingsRowDoctor && (m.doctorResult != nil || m.doctorErr != "") {
				write(renderScrollableSettingsDoctorDashboard(m, detailPrefix) + "\n")
			} else {
				write(detailPrefix + help + "\n")
			}
			if hints := renderContextHints(m, settingsRowHintContext(i), hintPrefix); hints != "" {
				write(hints + "\n")
			}
			buf.markCursorEnd()
		}
	}

	return buf.render(listAvailableHeight(m))
}

func renderDotsServiceDashboard(m Model, prefix string) string {
	return strings.Join([]string{
		renderDotsReminderServiceDashboardLine(m, prefix),
		renderDotsWatchServiceDashboardLine(m, prefix),
	}, "\n")
}

func renderDotsReminderServiceDashboardLine(m Model, prefix string) string {
	p := m.palette
	if m.dotsReminderServiceErr != "" {
		return prefix + p.styleMissing.Render("Reminder") + p.styleHelp.Render("  unavailable  "+m.dotsReminderServiceErr)
	}
	service := m.dotsReminderService
	if service == nil {
		service = &app.DotsReminderService{Interval: app.DefaultDotsReminderInterval()}
	}
	state := p.styleMissing.Render("[OFF]")
	if service.Installed {
		state = p.styleInstalled.Render("[ON]")
	}
	platform := service.Platform
	if platform == "" {
		platform = "native"
	}
	notify := "notify off"
	if service.Notify {
		notify = "notify on"
	}
	return prefix + p.styleNormal.Render("Reminder") +
		"  " + state +
		"  " + p.styleProvider.Render(platform) +
		"  " + p.styleHelp.Render("interval "+formatSettingsDuration(service.Interval)) +
		"  " + p.styleHelp.Render(notify)
}

func renderDotsWatchServiceDashboardLine(m Model, prefix string) string {
	p := m.palette
	if m.dotsWatchServiceErr != "" {
		return prefix + p.styleMissing.Render("Watch") + p.styleHelp.Render("     unavailable  "+m.dotsWatchServiceErr)
	}
	service := m.dotsWatchService
	if service == nil {
		service = &app.DotsWatchService{Debounce: app.DefaultDotsWatchDebounce()}
	}
	state := p.styleMissing.Render("[OFF]")
	if service.Installed {
		state = p.styleInstalled.Render("[ON]")
	}
	platform := service.Platform
	if platform == "" {
		platform = "native"
	}
	return prefix + p.styleNormal.Render("Watch") +
		"     " + state +
		"  " + p.styleProvider.Render(platform) +
		"  " + p.styleHelp.Render("debounce "+formatSettingsDuration(service.Debounce))
}

func renderScrollableSettingsDoctorDashboard(m Model, prefix string) string {
	lines := renderDoctorDashboardLines(m, prefix)
	lines = settingsDetailWindow(m, lines, prefix, m.settingsDetailScroll, settingsDetailWindowHeight(m))
	return strings.Join(lines, "\n")
}

func renderDoctorDashboardLines(m Model, prefix string) []string {
	p := m.palette
	if m.doctorErr != "" {
		return []string{prefix + p.styleMissing.Render("Doctor failed") + p.styleHelp.Render("  "+m.doctorErr)}
	}
	if m.doctorResult == nil {
		return []string{prefix + p.styleHelp.Render("Run doctor to collect diagnostics.")}
	}
	lines := []string{
		prefix + p.styleHelp.Render(fmt.Sprintf("Summary  %d ok  %d warn  %d fail", m.doctorResult.Summary.OK, m.doctorResult.Summary.Warn, m.doctorResult.Summary.Fail)),
	}
	for _, check := range m.doctorResult.Checks {
		lines = append(lines, renderDoctorCheckLine(m, prefix, check))
		lines = append(lines, renderDoctorCheckDetails(m, prefix, check)...)
	}
	return lines
}

func settingsDetailWindow(m Model, lines []string, prefix string, offset int, maxLines int) []string {
	if maxLines <= 0 || len(lines) <= maxLines {
		return lines
	}
	offset = clampRange(offset, 0, len(lines)-maxLines)
	out := append([]string(nil), lines[offset:offset+maxLines]...)
	if maxLines >= 3 {
		if offset > 0 {
			out[0] = prefix + m.palette.styleHelp.Render("…")
		}
		if offset+maxLines < len(lines) {
			out[len(out)-1] = prefix + m.palette.styleHelp.Render("…")
		}
	}
	return out
}

func (m Model) settingsDetailScrollMax() int {
	if m.settingsCursor != settingsRowDoctor || (m.doctorResult == nil && m.doctorErr == "") {
		return 0
	}
	return max(len(renderDoctorDashboardLines(m, textRowContentPrefix()))-settingsDetailWindowHeight(m), 0)
}

func settingsDetailWindowHeight(m Model) int {
	return max(listAvailableHeight(m)-4, 1)
}

func renderDoctorCheckLine(m Model, prefix string, check app.DoctorCheck) string {
	p := m.palette
	status := p.styleHelp.Render("[?]")
	switch check.Status {
	case app.DoctorStatusOK:
		status = p.styleInstalled.Render("[ok]")
	case app.DoctorStatusWarn:
		status = p.styleOutdated.Render("[warn]")
	case app.DoctorStatusFail:
		status = p.styleMissing.Render("[fail]")
	}
	return prefix + status + " " + p.styleNormal.Render(check.Label) + p.styleHelp.Render("  "+check.Message)
}

func renderDoctorCheckDetails(m Model, prefix string, check app.DoctorCheck) []string {
	if len(check.Details) == 0 {
		return nil
	}
	p := m.palette
	maxLines := min(len(check.Details), 4)
	detailW := max(rowAvailableWidth(m.width)-lipgloss.Width(prefix)-4, 20)
	lines := make([]string, 0, maxLines+1)
	for _, detail := range check.Details[:maxLines] {
		detail = strings.TrimSpace(detail)
		if detail == "" {
			continue
		}
		lines = append(lines, prefix+p.styleHelp.Render("  - "+truncatePath(detail, detailW)))
	}
	if remaining := len(check.Details) - maxLines; remaining > 0 {
		lines = append(lines, prefix+p.styleHelp.Render(fmt.Sprintf("  - +%d more", remaining)))
	}
	return lines
}

func renderSettingsDotsDisableKeepLocalPrompt(m Model, prefix string) string {
	p := m.palette
	prompt := p.styleMissing.Render("disable dotfile sync") +
		p.styleHelp.Render(", keep local? ")
	return prefix + prompt + renderActionHintText(p, contextHintItems(m, hintCtxDotsDeleteConfirm))
}

func settingsRowHintContext(row int) hintContext {
	if row >= 0 && row < len(settingsRows) {
		return settingsRows[row].hint
	}
	return hintCtxSettingsToggle
}

func (m Model) currentDotsReminderInterval() time.Duration {
	if m.dotsReminderInterval > 0 {
		return m.dotsReminderInterval
	}
	return app.DotsReminderInterval(m.dotsReminderService)
}

func (m Model) currentDotsWatchDebounce() time.Duration {
	if m.dotsWatchDebounce > 0 {
		return m.dotsWatchDebounce
	}
	return app.DotsWatchDebounce(m.dotsWatchService)
}

func (m Model) dotsWatchDebounceForServiceInstall() time.Duration {
	if m.dotsWatchDebounceNext > 0 {
		return m.dotsWatchDebounceNext
	}
	return m.currentDotsWatchDebounce()
}

func settingsDurationChoicesForRow(row int, current time.Duration) []settingsDurationChoice {
	var base []time.Duration
	switch row {
	case settingsRowDotsReminderInterval:
		base = app.DotsReminderIntervalChoices()
	case settingsRowDotsWatchDebounce:
		base = app.DotsWatchDebounceChoices()
	default:
		return nil
	}
	choices := make([]settingsDurationChoice, 0, len(base)+1)
	for _, value := range base {
		choices = append(choices, settingsDurationChoice{label: formatSettingsDuration(value), value: value})
	}
	if current > 0 && !settingsDurationChoicesContain(choices, current) {
		choices = append(choices, settingsDurationChoice{label: formatSettingsDuration(current), value: current})
		sort.Slice(choices, func(i, j int) bool { return choices[i].value < choices[j].value })
	}
	return choices
}

func settingsDurationChoicesContain(choices []settingsDurationChoice, value time.Duration) bool {
	return settingsDurationChoiceIndex(choices, value) >= 0
}

func settingsDurationChoiceIndex(choices []settingsDurationChoice, value time.Duration) int {
	for i, choice := range choices {
		if choice.value == value {
			return i
		}
	}
	return -1
}

func formatSettingsDuration(duration time.Duration) string {
	switch {
	case duration%(24*time.Hour) == 0:
		days := duration / (24 * time.Hour)
		return fmt.Sprintf("%dd", days)
	case duration%time.Hour == 0:
		hours := duration / time.Hour
		return fmt.Sprintf("%dh", hours)
	case duration%time.Minute == 0:
		minutes := duration / time.Minute
		return fmt.Sprintf("%dm", minutes)
	default:
		return duration.String()
	}
}

func renderSettingsDurationPicker(m Model, prefix string) string {
	p := m.palette
	current := m.currentSettingsDurationValue(m.serviceDurationRow)
	choices := settingsDurationChoicesForRow(m.serviceDurationRow, current)
	if len(choices) == 0 {
		return prefix + p.styleHelp.Render("No duration choices available.")
	}
	idx := clampRange(m.serviceDurationIdx, 0, len(choices)-1)
	parts := make([]string, 0, len(choices))
	for i, choice := range choices {
		if i == idx {
			parts = append(parts, p.styleInstalled.Render("["+choice.label+"]"))
			continue
		}
		parts = append(parts, p.styleHelp.Render(choice.label))
	}
	return prefix + strings.Join(parts, "  ")
}

func agentsEditorPopupFrame(m Model) popupFrame {
	contentH := len(m.agentsEditRows) + popupFooterHeight
	if len(m.agentsEditRows) == 0 {
		contentH = 1 + popupFooterHeight
	}
	return popupFrame{
		Title:          "Agents",
		PaddingY:       1,
		PaddingX:       2,
		Width:          popupFrameWidthForContent(popupContentWidth(m, 40, 32, 56), 2),
		ContentHeight:  contentH,
		NoTitleDivider: true,
	}
}

func settingsAgentsUseVal(m Model) string {
	p := m.palette
	ids := m.settings.AgentsUse
	if ids == nil {
		return p.styleHelp.Render("[all detected]")
	}
	if len(ids) == 0 {
		return p.styleHelp.Render("[none]")
	}
	return p.styleProvider.Render("[" + strings.Join(ids, ", ") + "]")
}

func renderSettingsAgentsEditor(m Model) string {
	p := m.palette
	rows := m.agentsEditRows
	if len(rows) == 0 {
		prefix := textRowContentPrefix()
		var sb strings.Builder
		sb.WriteString(prefix + p.styleHelp.Render("No supported agents detected on this machine.") + "\n")
		sb.WriteString(renderPickerHintItems(m, rowAvailableWidth(m.width), []hintItem{
			hintFromBindingDesc(m.keys.Back, "cancel"),
		}))
		return sb.String()
	}
	contentW := rowAvailableWidth(m.width)
	labelW := 0
	for _, row := range rows {
		if w := lipgloss.Width(row.Display); w > labelW {
			labelW = w
		}
	}
	pickerRows := make([]pickerChoiceRow, 0, len(rows))
	for i, r := range rows {
		selected := i == m.agentsEditCursor
		style := p.styleNormal
		if selected {
			style = p.styleActiveText
		}
		mark := "[ ]"
		if r.Enabled {
			mark = "[x]"
		}
		pickerRows = append(pickerRows, pickerChoiceRow{selected: selected, label: r.Display, mark: mark, style: style})
	}
	var sb strings.Builder
	sb.WriteString(renderPickerChoiceRows(p, pickerRows, labelW, 0))
	sb.WriteString("\n")
	sb.WriteString(renderPickerHintItems(m, contentW, toggleSaveCancelActionItems(m)))
	return sb.String()
}

func (m Model) currentSettingsDurationValue(row int) time.Duration {
	switch row {
	case settingsRowDotsReminderInterval:
		return m.currentDotsReminderInterval()
	case settingsRowDotsWatchDebounce:
		return m.currentDotsWatchDebounce()
	default:
		return 0
	}
}
