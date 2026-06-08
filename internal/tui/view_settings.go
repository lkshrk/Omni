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
	settingsRowDoctor
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
}

var settingsRows = []settingsRowMeta{
	settingsRowAutoImport: {
		label:   "Import Installed Tools",
		section: "Tools",
		hint:    hintCtxSettingsToggle,
		action:  app.SettingsActionToggleAutoImport,
	},
	settingsRowProviderPriority: {
		label:   "Provider Priority",
		section: "Tools",
		hint:    hintCtxSettingsEdit,
	},
	settingsRowDotsRepo: {
		label:   "Repository",
		section: "Dotfiles",
		hint:    hintCtxSettingsEdit,
	},
	settingsRowDotsSync: {
		label:   "Dotfile Sync",
		section: "Dotfiles",
		hint:    hintCtxSettingsDotsSync,
	},
	settingsRowDotsReminder: {
		label:   "Reminder Notifications",
		section: "Dotfiles",
		hint:    hintCtxSettingsToggle,
	},
	settingsRowDotsReminderInterval: {
		label:   "Reminder Interval",
		section: "Dotfiles",
		hint:    hintCtxSettingsDuration,
	},
	settingsRowDotsWatch: {
		label:   "Watch Sync",
		section: "Dotfiles",
		hint:    hintCtxSettingsToggle,
	},
	settingsRowDotsWatchDebounce: {
		label:   "Watch Debounce",
		section: "Dotfiles",
		hint:    hintCtxSettingsDuration,
	},
	settingsRowDotsServices: {
		label:   "Service Status",
		section: "Dotfiles",
		hint:    hintCtxSettingsStatus,
	},
	settingsRowDotsCommit: {
		label:   "Commit Changes",
		section: "Dotfiles",
		hint:    hintCtxSettingsToggle,
		action:  app.SettingsActionToggleDotsCommit,
	},
	settingsRowDotsPush: {
		label:   "Push Changes",
		section: "Dotfiles",
		hint:    hintCtxSettingsToggle,
		action:  app.SettingsActionToggleDotsPush,
	},
	settingsRowDoctor: {
		label:   "Run Doctor",
		section: "Maintenance",
		hint:    hintCtxSettingsEdit,
	},
	settingsRowBootstrap: {
		label:   "Run Bootstrap Again",
		section: "Maintenance",
		hint:    hintCtxSettingsDanger,
		danger:  true,
	},
	settingsRowResetSettings: {
		label:   "Reset Settings",
		section: "Maintenance",
		hint:    hintCtxSettingsDanger,
		danger:  true,
	},
	settingsRowResetCache: {
		label:   "Reset Cache",
		section: "Maintenance",
		hint:    hintCtxSettingsDanger,
		danger:  true,
	},
}

type settingRow struct {
	settingsRowMeta
	value string
	help  string // pre-rendered with styling
}

type settingsDurationChoice struct {
	label string
	value time.Duration
}

func formatSettingLabel(label string) string {
	return fmt.Sprintf("%-*s", settingLabelWidth, label)
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

	onOff := func(on bool) string {
		if on {
			return p.styleInstalled.Render("[ON]")
		}
		return p.styleMissing.Render("[OFF]")
	}

	priorityVal := func(pv []string) string {
		if len(pv) == 0 {
			return p.styleProvider.Render("[default]")
		}
		return p.styleProvider.Render("[" + strings.Join(pv, " › ") + "]")
	}

	dotsRepoVal := func(v string) string {
		if v == "" {
			return p.styleHelp.Render("[not set]")
		}
		avail := max(contentW-lipgloss.Width(rowInset)-settingLabelWidth-settingsMinGap, 12)
		return p.styleProvider.Render(truncatePath(v, avail))
	}

	serviceVal := func(installed bool, statusErr string) string {
		if statusErr != "" {
			return p.styleHelp.Render("[n/a]")
		}
		return onOff(installed)
	}

	durationVal := func(duration time.Duration, statusErr string) string {
		if statusErr != "" {
			return p.styleHelp.Render("[n/a]")
		}
		return p.styleProvider.Render("[" + formatSettingsDuration(duration) + "]")
	}

	servicesVal := func() string {
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

	doctorVal := func() string {
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

	serviceHelp := func(name string, installed bool, statusErr string, enableCopy string) string {
		if dotsViewUnconfigured(m) {
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

	durationHelp := func(name string, installed bool, statusErr string) string {
		if statusErr != "" {
			return p.styleHelp.Render(statusErr)
		}
		if installed {
			return p.styleHelp.Render("Update the installed " + name + " service interval.")
		}
		return p.styleHelp.Render("Choose the " + name + " service value used on the next enable.")
	}

	rows := []settingRow{
		settingsRowAutoImport: {
			settingsRowMeta: settingsRows[settingsRowAutoImport],
			value:           onOff(m.settings.AutoImport),
			help:            p.styleHelp.Render("Add newly installed tools to the config on every sync."),
		},
		settingsRowProviderPriority: {
			settingsRowMeta: settingsRows[settingsRowProviderPriority],
			value:           priorityVal(m.providerPriorityDisplay()),
			help:            p.styleHelp.Render("Per-machine order of concrete package managers. Reorder to set priority; space toggles a provider off."),
		},
		settingsRowDotsRepo: {
			settingsRowMeta: settingsRows[settingsRowDotsRepo],
			value:           dotsRepoVal(dotsRepoPathForView(m)),
			help:            p.styleHelp.Render("Path to your dotfiles git repository."),
		},
		settingsRowDotsSync: {
			settingsRowMeta: settingsRows[settingsRowDotsSync],
			value:           onOff(!dotsViewDisabled(m)),
			help: func() string {
				if dotsViewDisabled(m) {
					return p.styleHelp.Render("Re-enable dotfile sync and restore managed symlinks.")
				}
				return p.styleHelp.Render("Disable sync: remove managed symlinks and copy files back locally.")
			}(),
		},
		settingsRowDotsReminder: {
			settingsRowMeta: settingsRows[settingsRowDotsReminder],
			value:           serviceVal(m.dotsReminderService != nil && m.dotsReminderService.Installed, m.dotsReminderServiceErr),
			help: serviceHelp(
				"reminder",
				m.dotsReminderService != nil && m.dotsReminderService.Installed,
				m.dotsReminderServiceErr,
				"Install a native reminder timer with desktop notifications.",
			),
		},
		settingsRowDotsReminderInterval: {
			settingsRowMeta: settingsRows[settingsRowDotsReminderInterval],
			value:           durationVal(m.currentDotsReminderInterval(), m.dotsReminderServiceErr),
			help:            durationHelp("reminder", m.dotsReminderService != nil && m.dotsReminderService.Installed, m.dotsReminderServiceErr),
		},
		settingsRowDotsWatch: {
			settingsRowMeta: settingsRows[settingsRowDotsWatch],
			value:           serviceVal(m.dotsWatchService != nil && m.dotsWatchService.Installed, m.dotsWatchServiceErr),
			help: serviceHelp(
				"watch",
				m.dotsWatchService != nil && m.dotsWatchService.Installed,
				m.dotsWatchServiceErr,
				"Install a native watcher that syncs links after changes; it does not commit or push.",
			),
		},
		settingsRowDotsWatchDebounce: {
			settingsRowMeta: settingsRows[settingsRowDotsWatchDebounce],
			value:           durationVal(m.currentDotsWatchDebounce(), m.dotsWatchServiceErr),
			help:            durationHelp("watch", m.dotsWatchService != nil && m.dotsWatchService.Installed, m.dotsWatchServiceErr),
		},
		settingsRowDotsServices: {
			settingsRowMeta: settingsRows[settingsRowDotsServices],
			value:           servicesVal(),
			help:            p.styleHelp.Render("Native service file status for reminders and watch sync."),
		},
		settingsRowDotsCommit: {
			settingsRowMeta: settingsRows[settingsRowDotsCommit],
			value: func() string {
				if m.settings.DotsGit.AutoPush {
					return p.styleHelp.Render("[──]")
				}
				return onOff(m.settings.DotsGit.AutoCommit)
			}(),
			help: func() string {
				if m.settings.DotsGit.AutoPush {
					return p.styleHelp.Render("Implied by Push Changes.")
				}
				return p.styleHelp.Render("Commit automatically after dots add/remove/variant operations; does not affect Watch Sync.")
			}(),
		},
		settingsRowDotsPush: {
			settingsRowMeta: settingsRows[settingsRowDotsPush],
			value:           onOff(m.settings.DotsGit.AutoPush),
			help:            p.styleHelp.Render("Push (and commit) automatically after dots add/remove/variant operations; does not affect Watch Sync."),
		},
		settingsRowDoctor: {
			settingsRowMeta: settingsRows[settingsRowDoctor],
			value:           doctorVal(),
			help:            p.styleHelp.Render("Run read-only checks for config, host setup, providers, dotfiles, services, and cache."),
		},
		settingsRowBootstrap: {
			settingsRowMeta: settingsRows[settingsRowBootstrap],
			value:           p.styleHelp.Render("[run]"),
			help:            p.styleHelp.Render("Run the guided bootstrap flow for this host again."),
		},
		settingsRowResetSettings: {
			settingsRowMeta: settingsRows[settingsRowResetSettings],
			value:           p.styleHelp.Render("[reset]"),
			help:            p.styleHelp.Render("Restore all settings to defaults (tools, hosts, and groups preserved)."),
		},
		settingsRowResetCache: {
			settingsRowMeta: settingsRows[settingsRowResetCache],
			value:           p.styleHelp.Render("[reset]"),
			help:            p.styleHelp.Render("Delete and reinitialise the tool cache database."),
		},
	}

	for i, row := range rows {
		if i == 0 || row.section != rows[i-1].section {
			if i > 0 {
				write("\n")
			}
			if row.danger {
				write(renderSectionHeaderDanger(p, row.section, m.width) + "\n")
			} else {
				write(renderSectionHeader(p, row.section, m.width) + "\n")
			}
		}

		// Record cursor line after the section header but before the row content.
		isSettingsCursor := i == m.settingsCursor && !m.cursorHidden
		if isSettingsCursor {
			buf.markCursor()
		}

		lbl := p.styleNormal
		if row.danger {
			lbl = p.styleDangerLabel
		}

		// Provider Priority row: expand into an inline reorder list when editing.
		if i == settingsRowProviderPriority && m.editingPriority {
			write(renderResponsiveGroupListRow(p, true,
				[]rowCell{leftCell(p.styleActiveText.Render(rowInset+formatSettingLabel(row.label)), settingLabelWidth+lipgloss.Width(rowInset))},
				[]rowCell{rightCell(p.styleProvider.Render("[editing]"), 0)},
				contentW, settingsMinGap, listColumnGap,
			) + "\n")
			for j, name := range m.priorityDraft {
				cursorOn := j == m.priorityCursor
				enum := " "
				if cursorOn {
					if m.priorityHolding {
						enum = "⇅"
					} else {
						enum = "‣"
					}
				}
				dot := "●"
				if m.priorityDisabled[name] {
					dot = "○"
				}
				unavailable := m.priorityAvailable != nil && !m.priorityAvailable[name]
				style := p.styleNormal
				switch {
				case cursorOn:
					style = p.styleActiveText
				case unavailable || m.priorityDisabled[name]:
					style = p.styleHelp // dim: unavailable on this host or toggled off
				}
				write("    " + style.Render(enum+" "+dot+" "+name) + "\n")
			}
			hintCtx := hintCtxSettingsPriorityEdit
			if m.priorityHolding {
				hintCtx = hintCtxSettingsPriorityHold
			}
			write(renderContextHints(m, hintCtx, hintPrefix) + "\n")
			continue
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

		// Any row awaiting second-enter confirmation.
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
				[]rowCell{rightCell(row.value, 0)},
				contentW, settingsMinGap, listColumnGap,
			) + "\n")
		} else {
			write(renderResponsiveGroupListRow(p, false,
				[]rowCell{leftCell(lbl.Render(rowInset+formatSettingLabel(row.label)), settingLabelWidth+lipgloss.Width(rowInset))},
				[]rowCell{rightCell(row.value, 0)},
				contentW, settingsMinGap, listColumnGap,
			) + "\n")
		}
		if isSettingsCursor {
			if i == settingsRowDotsServices {
				write(renderDotsServiceDashboard(m, detailPrefix) + "\n")
			} else if i == settingsRowDoctor && (m.doctorResult != nil || m.doctorErr != "") {
				write(renderScrollableSettingsDoctorDashboard(m, detailPrefix) + "\n")
			} else {
				write(detailPrefix + row.help + "\n")
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
