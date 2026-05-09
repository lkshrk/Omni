package tui

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

const (
	settingLabelWidth = 28
	firstColumnGap    = listColumnGap * 2
	settingsMinGap    = listColumnGap * 3
	groupsMinGap      = listColumnGap * 3
)

const (
	settingsRowAutoImport = iota
	settingsRowSystemProvider
	settingsRowSystemPriority
	settingsRowNodeProvider
	settingsRowPythonProvider
	settingsRowNodeManager
	settingsRowPythonManager
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
}

var settingsRows = []settingsRowMeta{
	settingsRowAutoImport: {
		label:   "Import Installed Tools",
		section: "Tools",
		hint:    hintCtxSettingsToggle,
	},
	settingsRowSystemPriority: {
		label:   "System Provider Order",
		section: "Tools",
		hint:    hintCtxSettingsEdit,
	},
	settingsRowSystemProvider: {
		label:   "Track System",
		section: "Tools",
		hint:    hintCtxSettingsToggle,
	},
	settingsRowNodeProvider: {
		label:   "Track Node",
		section: "Tools",
		hint:    hintCtxSettingsToggle,
	},
	settingsRowPythonProvider: {
		label:   "Track Python",
		section: "Tools",
		hint:    hintCtxSettingsToggle,
	},
	settingsRowNodeManager: {
		label:   "Node Manager",
		section: "Managers",
		hint:    hintCtxSettingsToggle,
	},
	settingsRowPythonManager: {
		label:   "Python Manager",
		section: "Managers",
		hint:    hintCtxSettingsToggle,
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
	},
	settingsRowDotsPush: {
		label:   "Push Changes",
		section: "Dotfiles",
		hint:    hintCtxSettingsToggle,
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

var reminderIntervalChoices = []settingsDurationChoice{
	{label: "15m", value: 15 * time.Minute},
	{label: "30m", value: 30 * time.Minute},
	{label: "1h", value: time.Hour},
	{label: "4h", value: 4 * time.Hour},
	{label: "12h", value: 12 * time.Hour},
	{label: "24h", value: 24 * time.Hour},
	{label: "48h", value: 48 * time.Hour},
	{label: "7d", value: 7 * 24 * time.Hour},
}

var watchDebounceChoices = []settingsDurationChoice{
	{label: "500ms", value: 500 * time.Millisecond},
	{label: "1s", value: time.Second},
	{label: "2s", value: 2 * time.Second},
	{label: "5s", value: 5 * time.Second},
	{label: "10s", value: 10 * time.Second},
	{label: "30s", value: 30 * time.Second},
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

	nodeVal := func(v string) string {
		if v == "" {
			return p.styleProvider.Render("[auto]")
		}
		return p.styleProvider.Render("[" + v + "]")
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
		if strings.TrimSpace(m.settings.DotsRepo) == "" {
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

	providerEnabled := func(name string) bool {
		for _, d := range m.settings.DisabledProviders {
			if d == name {
				return false
			}
		}
		return true
	}

	rows := []settingRow{
		settingsRowAutoImport: {
			settingsRowMeta: settingsRows[settingsRowAutoImport],
			value:           onOff(m.settings.AutoImport),
			help:            p.styleHelp.Render("Add newly installed tools to the config on every sync."),
		},
		settingsRowSystemPriority: {
			settingsRowMeta: settingsRows[settingsRowSystemPriority],
			value:           priorityVal(m.systemPriorityDisplay(m.settings.EcosystemPriority(provider.EcosystemSystem))),
			help:            p.styleHelp.Render("Concrete system managers tried for system tools without an install_with override."),
		},
		settingsRowSystemProvider: {
			settingsRowMeta: settingsRows[settingsRowSystemProvider],
			value:           onOff(providerEnabled(provider.EcosystemSystem)),
			help:            p.styleHelp.Render("Track system tools on this machine (brew/apt/dnf/...)."),
		},
		settingsRowNodeProvider: {
			settingsRowMeta: settingsRows[settingsRowNodeProvider],
			value:           onOff(providerEnabled(provider.EcosystemNode)),
			help:            p.styleHelp.Render("Track node tools on this machine (bun/pnpm/npm)."),
		},
		settingsRowPythonProvider: {
			settingsRowMeta: settingsRows[settingsRowPythonProvider],
			value:           onOff(providerEnabled(provider.EcosystemPython)),
			help:            p.styleHelp.Render("Track python tools on this machine (uv/pip3)."),
		},
		settingsRowNodeManager: {
			settingsRowMeta: settingsRows[settingsRowNodeManager],
			value:           nodeVal(m.settings.EcosystemManager(provider.EcosystemNode)),
			help:            p.styleHelp.Render("JS package manager (auto = bun preferred, then pnpm, then npm)."),
		},
		settingsRowPythonManager: {
			settingsRowMeta: settingsRows[settingsRowPythonManager],
			value:           nodeVal(m.settings.EcosystemManager(provider.EcosystemPython)),
			help:            p.styleHelp.Render("Python tool manager (auto = uv preferred, then pip3)."),
		},
		settingsRowDotsRepo: {
			settingsRowMeta: settingsRows[settingsRowDotsRepo],
			value:           dotsRepoVal(m.settings.DotsRepo),
			help:            p.styleHelp.Render("Path to your dotfiles git repository."),
		},
		settingsRowDotsSync: {
			settingsRowMeta: settingsRows[settingsRowDotsSync],
			value:           onOff(!config.BoolVal(m.settings.DotsDisabled)),
			help: func() string {
				if config.BoolVal(m.settings.DotsDisabled) {
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
		if i == m.settingsCursor {
			buf.markCursor()
		}

		lbl := p.styleNormal
		if row.danger {
			lbl = p.styleDangerLabel
		}

		// System Provider Order row: expand into an inline reorder list when editing.
		if i == settingsRowSystemPriority && m.editingPriority {
			write(renderResponsiveGroupListRow(p, true,
				[]rowCell{leftCell(p.styleActiveText.Render(rowInset+formatSettingLabel(row.label)), settingLabelWidth+lipgloss.Width(rowInset))},
				[]rowCell{rightCell(p.styleProvider.Render("[editing]"), 0)},
				contentW, settingsMinGap, listColumnGap,
			) + "\n")
			prCursor := m.priorityCursor
			prItems := make([]any, len(m.priorityDraft))
			for j, pd := range m.priorityDraft {
				prItems[j] = pd
			}
			prl := newCursorList(p, prItems, prCursor, 4)
			write(prl.String() + "\n")
			write(renderContextHints(m, hintCtxSettingsPriorityEdit, hintPrefix) + "\n")
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

		if i == m.settingsCursor {
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
		if i == m.settingsCursor {
			if i == settingsRowDotsServices {
				write(renderDotsServiceDashboard(m, detailPrefix) + "\n")
			} else if i == settingsRowDoctor && (m.doctorResult != nil || m.doctorErr != "") {
				write(renderDoctorDashboard(m, detailPrefix) + "\n")
			} else {
				write(detailPrefix + row.help + "\n")
			}
			if hints := renderContextHints(m, settingsRowHintContext(i), hintPrefix); hints != "" {
				write(hints + "\n")
			}
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

func renderDoctorDashboard(m Model, prefix string) string {
	p := m.palette
	if m.doctorErr != "" {
		return prefix + p.styleMissing.Render("Doctor failed") + p.styleHelp.Render("  "+m.doctorErr)
	}
	if m.doctorResult == nil {
		return prefix + p.styleHelp.Render("Run doctor to collect diagnostics.")
	}
	lines := []string{
		prefix + p.styleHelp.Render(fmt.Sprintf("Summary  %d ok  %d warn  %d fail", m.doctorResult.Summary.OK, m.doctorResult.Summary.Warn, m.doctorResult.Summary.Fail)),
	}
	for _, check := range m.doctorResult.Checks {
		lines = append(lines, renderDoctorCheckLine(m, prefix, check))
		lines = append(lines, renderDoctorCheckDetails(m, prefix, check)...)
	}
	return strings.Join(lines, "\n")
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
	return dotsReminderIntervalFromService(m.dotsReminderService)
}

func (m Model) currentDotsWatchDebounce() time.Duration {
	if m.dotsWatchDebounce > 0 {
		return m.dotsWatchDebounce
	}
	return dotsWatchDebounceFromService(m.dotsWatchService)
}

func (m Model) dotsWatchDebounceForServiceInstall() time.Duration {
	if m.dotsWatchDebounceNext > 0 {
		return m.dotsWatchDebounceNext
	}
	return m.currentDotsWatchDebounce()
}

func dotsReminderIntervalFromService(service *app.DotsReminderService) time.Duration {
	if service != nil && service.Interval > 0 {
		return service.Interval
	}
	return app.DefaultDotsReminderInterval()
}

func dotsWatchDebounceFromService(service *app.DotsWatchService) time.Duration {
	if service != nil && service.Debounce > 0 {
		return service.Debounce
	}
	return app.DefaultDotsWatchDebounce()
}

func settingsDurationChoicesForRow(row int, current time.Duration) []settingsDurationChoice {
	var base []settingsDurationChoice
	switch row {
	case settingsRowDotsReminderInterval:
		base = reminderIntervalChoices
	case settingsRowDotsWatchDebounce:
		base = watchDebounceChoices
	default:
		return nil
	}
	choices := append([]settingsDurationChoice(nil), base...)
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

func renderGroupDeletePopup(m Model) string {
	p := m.palette
	groupName := m.groupDeleteName
	if groupName == "" {
		groupName = m.selectedHostGroupName()
	}
	if groupName == "" {
		groupName = "group"
	}
	var sb strings.Builder
	sb.WriteString(p.styleMissing.Render(groupName))
	sb.WriteString("\n\n")
	if m.groupHasContent(groupName) {
		choices := []string{
			"Move last-membership tools to this host",
			"Delete last-membership logical tools",
		}
		for i, choice := range choices {
			prefix := "  "
			style := p.styleNormal
			if i == m.groupDeleteChoice {
				prefix = "› "
				style = p.styleActiveText
			}
			sb.WriteString(prefix)
			sb.WriteString(style.Render(choice))
			sb.WriteString("\n")
		}
	} else {
		sb.WriteString(p.styleHelp.Render("No tools or dotfiles belong to this group."))
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
	sb.WriteString(renderPickerHintItems(m, groupDeletePopupContentWidth, confirmActionItems(m.keys.Confirm, "delete", m.keys.Back)))
	return sb.String()
}

const groupDeletePopupContentWidth = 44

func renderGroupCreatePopup(m Model) string {
	return renderNameCreatePopup(m, "group name")
}

func renderNameCreatePopup(m Model, label string) string {
	p := m.palette
	contentW := groupCreatePopupWidth(m)
	input := m.settingsInput
	input.Prompt = ""
	fieldLabel := "name"
	inputWidth := max(contentW-lipgloss.Width(fieldLabel)-8, 1)
	inputView := renderCreateNameInputView(p, input, label+"...", inputWidth)

	var sb strings.Builder
	sb.WriteString(renderCreateNameField(p, fieldLabel, inputView, contentW))
	sb.WriteString("\n\n")
	sb.WriteString(renderPickerHintItems(m, contentW, confirmActionItems(m.keys.Confirm, "create", m.keys.Back)))
	return lipgloss.NewStyle().Width(contentW).Render(sb.String())
}

func groupCreatePopupWidth(m Model) int {
	return popupContentWidth(m, 42, 28, 42)
}

func renderCreateNameField(p palette, label, input string, width int) string {
	prefix := p.styleHelp.Render(label + " ")
	field := p.styleHelp.Render("[ ") + input + p.styleHelp.Render(" ]")
	return lipgloss.NewStyle().Width(width).Render(prefix + field)
}

func renderCreateNameInputView(p palette, input textinput.Model, placeholder string, width int) string {
	return renderEmptyAwareTextInputView(p, input, placeholder, width)
}

func renderNewGroupInputView(m Model, width int) string {
	placeholder := m.settingsInput.Placeholder
	if placeholder == "" {
		placeholder = "new group name…"
	}
	return renderCreateNameInputView(m.palette, m.settingsInput, placeholder, width)
}

func renderGroups(m Model) string {
	p := m.palette
	rowInset := rowContentInset()
	detailPrefix := textRowContentPrefix()
	hintPrefix := textRowHintPrefix()
	var top []string

	if m.hostRequired {
		top = append(top,
			p.styleMissing.Render("  ⚠  No host configuration for this machine."),
			p.styleHelp.Render("  Create this host or copy groups from an existing host."),
			p.styleHelp.Render("  Navigation is locked until this host is configured. Press q to quit."),
			"",
		)
	}

	names := sortedHostNames(m.hostInfo)
	allGroupNames := buildAllGroupNames(m.groupNames)
	groupCounts := toolCountsByGroup(m)
	groupDots := dotCountsByGroup(m)
	cols := groupAssignmentTableColumnWidths(names, m.hostInfo, allGroupNames, groupCounts, groupDots)

	assignmentSection := sectionedTabSection{
		title:            "Group Assignments",
		blankAfterHeader: false,
	}
	if len(names) == 0 {
		assignmentSection.empty = []string{
			p.styleHelp.Render("  No host assignments configured."),
			p.styleHelp.Render("  Onboarding creates this machine's host assignment."),
		}
	} else {
		for i, name := range names {
			prof := m.hostInfo.Hosts[name]
			hostGroups := append([]string(nil), prof.Groups...)
			sort.Strings(hostGroups)
			groupBadge := compactHostAssignmentList(name, hostGroups)
			hostBadge := hostStatusLabel(m.hostInfo, name)
			nameLabel := name
			hostSelected := m.assignmentSection == 0 && i == m.hostCursor
			if hostSelected {
				if m.hostRenameMode {
					inputWidth := max(m.width-lipgloss.Width("    Rename: [ ")-4, 20)
					m.settingsInput.SetWidth(inputWidth)
					inputView := renderEmptyAwareTextInputView(p, m.settingsInput, m.settingsInput.Placeholder, inputWidth)
					assignmentSection.rows = append(assignmentSection.rows, sectionedTabRow{
						selected: true,
						line: renderFixedGroupListRow(p, true,
							[]rowCell{leftCell(p.styleActiveText.Render(nameLabel), cols.name)},
							nil,
							firstColumnGap, listColumnGap,
						),
						details: []string{
							p.styleHelp.Render(detailPrefix+"Rename: ") + "[ " + inputView + " ]",
							confirmCancelHintWithPrefix(m, "save", hintPrefix),
						},
					})
					continue
				}

				hostTools, hostDots := hostAssignmentCounts(m, name, prof.Groups)
				localStats := fmt.Sprintf("current host: %s, %s",
					compactCount(hostTools, "tool"),
					compactCount(hostDots, "dotfile"),
				)
				details := []string{p.styleHelp.Render(detailPrefix + localStats)}

				// Inline delete confirmation.
				if m.hostDeleteConfirm {
					details = append(details, renderPressAgainActionHint(p, detailPrefix, "d", "delete"))
				}

				if m.assignmentSection == 0 && !m.hostRenameMode && m.hostEditMode == 0 && !m.hostDeleteConfirm {
					details = append(details, renderContextHints(m, hintCtxHostDefault, hintPrefix))
				}
				assignmentSection.rows = append(assignmentSection.rows, sectionedTabRow{
					selected: true,
					line: renderResponsiveGroupListRow(p, true,
						[]rowCell{leftCell(p.styleActiveText.Render(nameLabel), cols.name)},
						[]rowCell{
							leftCell(listRowColumnStyle(true, p.styleHelp).Render(groupBadge), cols.mid),
							leftCell(listRowColumnStyle(true, p.styleProvider).Render(hostBadge), cols.tail),
						},
						rowAvailableWidth(m.width), groupsMinGap, listColumnGap,
					),
					details: details,
				})
			} else {
				assignmentSection.rows = append(assignmentSection.rows, sectionedTabRow{
					line: renderResponsiveGroupListRow(p, false,
						[]rowCell{leftCell(p.styleNormal.Render(nameLabel), cols.name)},
						[]rowCell{
							leftCell(p.styleHelp.Render(groupBadge), cols.mid),
							leftCell(p.styleHelp.Render(hostBadge), cols.tail),
						},
						rowAvailableWidth(m.width), groupsMinGap, listColumnGap,
					),
				})
			}
		}
	}

	// ── Groups section ──────────────────────────────────────────────────────
	groupsFocused := m.assignmentSection == 1
	groupSection := sectionedTabSection{
		title:            "Groups",
		blankAfterHeader: false,
	}

	for i, gn := range allGroupNames {
		count := groupCounts[gn]
		displayName := groupDisplayName(gn)
		label := rowInset + displayName
		toolCount := compactCount(count, "tool")
		dotCount := compactCount(groupDots[gn], "dotfile")
		isSelected := groupsFocused && i == m.groupCursor

		if isSelected {
			switch {
			case m.groupRenameMode:
				inputWidth := max(m.width-lipgloss.Width("    Rename: [ ")-4, 20)
				m.settingsInput.SetWidth(inputWidth)
				inputView := renderEmptyAwareTextInputView(p, m.settingsInput, m.settingsInput.Placeholder, inputWidth)
				groupSection.rows = append(groupSection.rows, sectionedTabRow{
					selected: true,
					line: renderFixedGroupListRow(p, true,
						[]rowCell{leftCell(p.styleActiveText.Render(label), cols.name)},
						nil,
						firstColumnGap, listColumnGap,
					),
					details: []string{
						p.styleHelp.Render(detailPrefix+"Rename: ") + "[ " + inputView + " ]",
						confirmCancelHintWithPrefix(m, "confirm", hintPrefix),
					},
				})
			case m.groupDeleteConfirm:
				groupSection.rows = append(groupSection.rows, sectionedTabRow{
					selected: true,
					line: renderResponsiveGroupListRow(p, true,
						[]rowCell{leftCell(p.styleMissing.Render(label), cols.name)},
						[]rowCell{
							rightCell(listRowColumnStyle(true, p.styleHelp).Render(toolCount), cols.mid),
							rightCell(listRowColumnStyle(true, p.styleProvider).Render(dotCount), cols.tail),
						},
						rowAvailableWidth(m.width), groupsMinGap, listColumnGap,
					),
					details: []string{confirmCancelHintWithPrefix(m, "confirm delete", hintPrefix)},
				})
			default:
				details := []string{}
				if isProtectedGroupName(gn) {
					details = append(details, p.styleProvider.Render(detailPrefix+"host bound group"))
				} else if hosts := hostsForGroup(m.hostInfo, gn); len(hosts) > 0 {
					details = append(details, p.styleHelp.Render(detailPrefix+"hosts: "+strings.Join(hosts, ", ")))
				}
				details = append(details, renderContextHints(m, hintCtxGroupDefault, hintPrefix))
				groupSection.rows = append(groupSection.rows, sectionedTabRow{
					selected: true,
					line: renderResponsiveGroupListRow(p, true,
						[]rowCell{leftCell(p.styleActiveText.Render(label), cols.name)},
						[]rowCell{
							rightCell(listRowColumnStyle(true, p.styleHelp).Render(toolCount), cols.mid),
							rightCell(listRowColumnStyle(true, p.styleProvider).Render(dotCount), cols.tail),
						},
						rowAvailableWidth(m.width), groupsMinGap, listColumnGap,
					),
					details: details,
				})
			}
		} else {
			groupSection.rows = append(groupSection.rows, sectionedTabRow{
				line: renderResponsiveGroupListRow(p, false,
					[]rowCell{leftCell(p.styleNormal.Render(label), cols.name)},
					[]rowCell{
						rightCell(p.styleHelp.Render(toolCount), cols.mid),
						rightCell(p.styleHelp.Render(dotCount), cols.tail),
					},
					rowAvailableWidth(m.width), groupsMinGap, listColumnGap,
				),
			})
		}
	}

	return renderSectionedTab(m, sectionedTab{
		leadingBlank: true,
		top:          top,
		sections:     []sectionedTabSection{assignmentSection, groupSection},
	})
}

type groupAssignmentTableColumns struct {
	name int
	mid  int
	tail int
}

func groupAssignmentTableColumnWidths(hostNames []string, info *app.HostInfo, groupNames []string, groupCounts, groupDots map[string]int) groupAssignmentTableColumns {
	cols := groupAssignmentTableColumns{name: 20, mid: len("assigned groups"), tail: len("status")}
	for _, name := range hostNames {
		host := info.Hosts[name]
		cols.name = max(cols.name, lipgloss.Width(name))
		cols.mid = max(cols.mid, lipgloss.Width(compactHostAssignmentList(name, host.Groups)))
		cols.tail = max(cols.tail, lipgloss.Width(hostStatusLabel(info, name)))
	}
	for _, name := range groupNames {
		cols.name = max(cols.name, lipgloss.Width(rowContentInset()+groupDisplayName(name)))
		cols.mid = max(cols.mid, lipgloss.Width(compactCount(groupCounts[name], "tool")))
		cols.tail = max(cols.tail, lipgloss.Width(compactCount(groupDots[name], "dotfile")))
	}
	return cols
}

func groupDisplayName(group string) string {
	if isProtectedGroupName(group) {
		return group + " (local)"
	}
	return group
}

func compactHostAssignmentList(host string, groups []string) string {
	items := []string{}
	if host != "" {
		items = append(items, host+" (local)")
	}
	for _, group := range groups {
		if group == "" || group == host {
			continue
		}
		items = append(items, group)
	}
	return compactGroupList(items)
}

func hostAssignmentCounts(m Model, host string, groups []string) (int, int) {
	groupSet := hostAssignmentGroupSet(host, groups)
	toolCount := 0
	if len(m.toolMemberships) > 0 {
		for _, memberships := range m.toolMemberships {
			if containsAnyGroup(memberships, groupSet) {
				toolCount++
			}
		}
	} else {
		for _, group := range m.toolGroups {
			if groupSet[group] {
				toolCount++
			}
		}
	}

	dotCount := 0
	for _, memberships := range m.dotMemberships {
		if containsAnyGroup(memberships, groupSet) {
			dotCount++
		}
	}
	return toolCount, dotCount
}

func hostAssignmentGroupSet(host string, groups []string) map[string]bool {
	set := make(map[string]bool, len(groups)+1)
	if host != "" {
		set[host] = true
	}
	for _, group := range groups {
		if group == "" {
			continue
		}
		set[group] = true
	}
	return set
}

func toolCountsByGroup(m Model) map[string]int {
	counts := make(map[string]int)
	if len(m.toolMemberships) > 0 {
		for _, memberships := range m.toolMemberships {
			for _, group := range uniqueGroups(memberships) {
				counts[group]++
			}
		}
		return counts
	}
	for _, group := range m.toolGroups {
		if group == "" {
			continue
		}
		counts[group]++
	}
	return counts
}

func dotCountsByGroup(m Model) map[string]int {
	counts := make(map[string]int)
	for _, memberships := range m.dotMemberships {
		for _, group := range uniqueGroups(memberships) {
			counts[group]++
		}
	}
	return counts
}

func uniqueGroups(groups []string) []string {
	seen := make(map[string]bool, len(groups))
	out := make([]string, 0, len(groups))
	for _, group := range groups {
		if group == "" || seen[group] {
			continue
		}
		seen[group] = true
		out = append(out, group)
	}
	return out
}

func containsAnyGroup(groups []string, set map[string]bool) bool {
	for _, group := range groups {
		if set[group] {
			return true
		}
	}
	return false
}

func compactGroupList(groups []string) string {
	if len(groups) == 0 {
		return "no groups"
	}
	groups = append([]string(nil), groups...)
	sort.Strings(groups)
	if len(groups) <= 3 {
		return strings.Join(groups, ", ")
	}
	return fmt.Sprintf("%s, %s, %s +%d", groups[0], groups[1], groups[2], len(groups)-3)
}

func hostStatusLabel(info *app.HostInfo, name string) string {
	if info != nil && name == info.Active {
		return "this host"
	}
	return ""
}

func sortedHostNames(info *app.HostInfo) []string {
	if info == nil || len(info.Hosts) == 0 {
		return nil
	}
	names := make([]string, 0, len(info.Hosts))
	for n := range info.Hosts {
		names = append(names, n)
	}
	sort.Strings(names)
	if info.Active != "" {
		if idx := slices.Index(names, info.Active); idx > 0 {
			copy(names[1:idx+1], names[:idx])
			names[0] = info.Active
		}
	}
	return names
}

func compactCount(n int, label string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", label)
	}
	return fmt.Sprintf("%d %ss", n, label)
}

func hostsForGroup(info *app.HostInfo, group string) []string {
	if info == nil {
		return nil
	}
	var hosts []string
	for name, host := range info.Hosts {
		if slices.Contains(host.Groups, group) {
			hosts = append(hosts, name)
		}
	}
	sort.Strings(hosts)
	return hosts
}

// toggleProvider toggles name in the DisabledProviders slice. If name is in
// the slice it is removed; otherwise it is appended. Returns the updated slice.
func toggleProvider(disabled []string, name string) []string {
	for i, d := range disabled {
		if d == name {
			// Remove: swap with last and trim.
			out := make([]string, len(disabled)-1)
			copy(out, disabled[:i])
			copy(out[i:], disabled[i+1:])
			return out
		}
	}
	return append(append([]string(nil), disabled...), name)
}

func cycleNodeManager(current string) string {
	return cycleManager(current, provider.BuiltinSettingsManagerNames(provider.EcosystemNode))
}

func cyclePythonManager(current string) string {
	return cycleManager(current, provider.BuiltinSettingsManagerNames(provider.EcosystemPython))
}

func cycleManager(current string, options []string) string {
	if len(options) == 0 {
		return ""
	}
	if current == "" {
		return options[0]
	}
	for i, option := range options {
		if current == option {
			if i+1 < len(options) {
				return options[i+1]
			}
			return ""
		}
	}
	return ""
}

const groupPickerNewSentinel = "+ new group…"

func renderGroupPicker(m Model) string {
	p := m.palette
	t := m.selectedTool()
	if t == nil {
		return p.styleHelp.Render("no tool selected")
	}
	var sb strings.Builder
	group := m.toolGroups[toolKey(t.Name, t.Provider)]
	contentW := groupPickerContentWidth(m)

	// Render group list. The "+ new group…" sentinel is styled differently;
	// when pickerCreatingGroup is true it is replaced by an inline text input
	// of the same visual width so the popup never changes size.
	m.settingsInput.SetWidth(min(groupPickerInputWidth(m), max(contentW-2, 1)))

	labelW, detailW := groupPickerColumnWidths(m, group)
	labelW, detailW = fitPickerChoiceColumnWidths(contentW, false, labelW, detailW)
	lastSection := ""
	for i, g := range m.pickerGroups {
		isSelected := i == m.pickerCursor
		if section := groupPickerSection(m, g); section != "" && section != lastSection {
			if lastSection != "" {
				sb.WriteString("\n")
			}
			sb.WriteString(renderPickerSectionLabel(p, section) + "\n")
			lastSection = section
		}

		// Replace sentinel with input field — same position, same width.
		if m.pickerCreatingGroup && isNewGroupSentinel(g) {
			sb.WriteString(pickerCursor(p, isSelected) + renderNewGroupInputView(m, max(contentW-2, 1)) + "\n")
			continue
		}

		style := p.styleNormal
		switch {
		case isNewGroupSentinel(g):
			style = p.styleProvider
		case groupHasActiveHostContext(m) && !groupInActiveHost(m, g):
			style = p.styleHelp
		case isSelected:
			style = p.styleActiveText
		}
		sb.WriteString(renderChoiceRow(p, isSelected, "", g, groupPickerDetail(m, g, group), labelW, detailW, style) + "\n")
	}
	sb.WriteString("\n")
	if m.pickerCreatingGroup {
		sb.WriteString(renderPickerHintItems(m, contentW, confirmActionItems(m.keys.Confirm, "create", m.keys.Back)))
	} else {
		sb.WriteString(renderPickerHintItems(m, contentW, confirmActionItems(m.keys.Confirm, "confirm", m.keys.Back)))
	}
	return lipgloss.NewStyle().Width(contentW).Render(sb.String())
}

func renderGroupMembershipPicker(m Model) string {
	p := m.palette
	targetName, memberships, ok := m.selectedMembershipTarget()
	if !ok || targetName == "" {
		return p.styleHelp.Render("no entry selected")
	}
	selectedGroup := primaryMembershipGroup(memberships)
	contentW := groupMembershipContentWidth(m)
	labelW, detailW := groupMembershipColumnWidths(m)
	labelW, detailW = fitPickerChoiceColumnWidths(contentW, true, labelW, detailW)
	rows := make([]pickerChoiceRow, 0, len(m.pickerGroups))
	for i, group := range m.pickerGroups {
		selected := i == m.pickerCursor
		row := pickerChoiceRow{section: groupPickerSection(m, group), selected: selected, label: group}
		if m.pickerCreatingGroup && isNewGroupSentinel(group) {
			row.inputView = renderNewGroupInputView(m, max(contentW-2, 1))
			rows = append(rows, row)
			continue
		}
		if isNewGroupSentinel(group) {
			style := p.styleProvider
			if selected {
				style = p.styleActiveText
			}
			row.style = style
			rows = append(rows, row)
			continue
		}
		row.mark = "[ ]"
		if group == selectedGroup {
			row.mark = "[x]"
		}
		row.style = p.styleNormal
		if selected {
			row.style = p.styleActiveText
		} else if groupHasActiveHostContext(m) && !groupInActiveHost(m, group) {
			row.style = p.styleHelp
		}
		rows = append(rows, row)
	}
	var sb strings.Builder
	sb.WriteString(renderPickerChoiceRows(p, rows, labelW, detailW))
	sb.WriteString("\n")
	hints := selectCancelActionItems(m)
	if m.pickerCreatingGroup {
		hints = confirmActionItems(m.keys.Confirm, "create", m.keys.Back)
	}
	sb.WriteString(renderPickerHintItems(m, contentW, hints))
	return lipgloss.NewStyle().Width(contentW).Render(sb.String())
}

func primaryMembershipGroup(memberships []string) string {
	if len(memberships) == 0 {
		return ""
	}
	return memberships[0]
}

func renderHostGroupEditor(m Model) string {
	p := m.palette
	contentW := groupEditorContentWidth(m)
	labelW := 0
	for _, group := range m.hostGroupPicker {
		labelW = max(labelW, lipgloss.Width(hostAssignmentPickerLabel(m, group)))
	}
	labelW, _ = fitPickerChoiceColumnWidths(contentW, true, labelW, 0)
	rows := make([]pickerChoiceRow, 0, len(m.hostGroupPicker))
	for i, group := range m.hostGroupPicker {
		selected := i == m.hostGroupIdx
		row := pickerChoiceRow{selected: selected, label: hostAssignmentPickerLabel(m, group)}
		if m.pickerCreatingGroup && selected && isNewGroupSentinel(group) {
			row.inputView = renderNewGroupInputView(m, max(contentW-2, 1))
			rows = append(rows, row)
			continue
		}
		if isNewGroupSentinel(group) {
			style := p.styleProvider
			if selected {
				style = p.styleActiveText
			}
			row.style = style
			rows = append(rows, row)
			continue
		}
		row.mark = "[ ]"
		if group == m.hostEditName || slices.Contains(m.hostGroupDraft, group) {
			row.mark = "[x]"
		}
		row.style = p.styleNormal
		if selected {
			row.style = p.styleActiveText
		}
		rows = append(rows, row)
	}
	var sb strings.Builder
	sb.WriteString(renderPickerChoiceRows(p, rows, labelW, 0))
	sb.WriteString("\n")
	hints := toggleSaveCancelActionItems(m)
	if m.pickerCreatingGroup {
		hints = confirmActionItems(m.keys.Confirm, "create", m.keys.Back)
	}
	sb.WriteString(renderPickerHintItems(m, contentW, hints))
	return lipgloss.NewStyle().Width(contentW).Render(sb.String())
}

func hostAssignmentPickerLabel(m Model, group string) string {
	if group == m.hostEditName {
		return group + " (local)"
	}
	return group
}

func renderHostGroupToolsEditor(m Model) string {
	p := m.palette
	contentW := groupToolsContentWidth(m)
	rows := groupToolRows(m)
	var sb strings.Builder

	if m.groupToolsEditor.searchActive {
		inputW := max(contentW-2, 1)
		m.settingsInput.SetWidth(inputW)
		sb.WriteString(p.styleHelp.Render("search"))
		sb.WriteString("\n")
		sb.WriteString(renderEmptyAwareTextInputView(p, m.settingsInput, m.settingsInput.Placeholder, inputW))
		sb.WriteString("\n")
	}

	if filterBar := renderHostGroupToolsFilterBar(m); filterBar != "" {
		if m.groupToolsEditor.searchActive {
			sb.WriteString("\n")
		}
		sb.WriteString(filterBar)
		sb.WriteString("\n\n")
	} else if m.groupToolsEditor.searchActive {
		sb.WriteString("\n")
	}

	if len(rows) == 0 {
		sb.WriteString(p.styleHelp.Render("no configured tools match"))
		sb.WriteString("\n\n")
	} else {
		baseRows := groupToolRows(unfilteredHostGroupToolsModel(m))
		secondaryW := groupToolsSecondaryWidth(m, baseRows)
		nameW, providerW := popupToggleTableColumnWidths(contentW, secondaryW)
		lastSection := groupToolSection(-1)
		for i, row := range rows {
			if row.section != lastSection {
				if i > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(renderPickerSectionLabel(p, groupToolSectionLabel(row.section)))
				sb.WriteString("\n")
				lastSection = row.section
			}
			selected := i == m.groupToolsEditor.cursor
			mark := "[ ]"
			if row.enabled {
				mark = "[x]"
			}
			labelStyle := p.styleNormal
			if row.section == groupToolSectionIgnored {
				labelStyle = p.styleIgnored
			}
			if selected {
				labelStyle = p.styleActiveText
			}
			nameCell := renderNameWithPackage(p, labelStyle, row.tool, nameW, selected)
			secondaryCell := renderHostGroupToolSecondary(m, row, providerW, selected)
			rowText := renderPopupToggleTableRenderedRow(p, selected, mark, nameCell, secondaryCell, nameW, providerW)
			sb.WriteString(rowText)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	ctx := hintCtxHostGroupTools
	if m.groupToolsEditor.searchActive {
		ctx = hintCtxHostGroupToolsSearch
	}
	sb.WriteString(renderPickerHintItems(m, contentW, contextHintItems(m, ctx)))
	return lipgloss.NewStyle().Width(contentW).Render(sb.String())
}

func renderHostGroupDotsEditor(m Model) string {
	p := m.palette
	contentW := groupDotsContentWidth(m)
	rows := groupDotRows(m)
	var sb strings.Builder

	if m.groupDotsEditor.searchActive {
		inputW := max(contentW-2, 1)
		m.settingsInput.SetWidth(inputW)
		sb.WriteString(p.styleHelp.Render("search"))
		sb.WriteString("\n")
		sb.WriteString(renderEmptyAwareTextInputView(p, m.settingsInput, m.settingsInput.Placeholder, inputW))
		sb.WriteString("\n\n")
	}

	if len(rows) == 0 {
		sb.WriteString(p.styleHelp.Render("no configured dotfiles match"))
		sb.WriteString("\n\n")
	} else {
		baseRows := groupDotRows(unfilteredHostGroupDotsModel(m))
		_, secondaryW := groupDotsColumnWidths(baseRows)
		nameW, targetW := popupToggleTableColumnWidths(contentW, secondaryW)
		lastSection := groupDotSection(-1)
		for i, row := range rows {
			if row.section != lastSection {
				if i > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(renderPickerSectionLabel(p, groupDotSectionLabel(row.section)))
				sb.WriteString("\n")
				lastSection = row.section
			}
			selected := i == m.groupDotsEditor.cursor
			mark := "[ ]"
			if row.enabled {
				mark = "[x]"
			}
			labelStyle := p.styleNormal
			if row.section == groupDotSectionIgnored {
				labelStyle = p.styleIgnored
			}
			if selected {
				labelStyle = p.styleActiveText
			}
			displayName := truncatePath(row.name, nameW)
			nameCell := labelStyle.Render(displayName) + strings.Repeat(" ", max(nameW-lipgloss.Width(displayName), 0))
			targetCell := ""
			if row.target != "" {
				targetCell = p.styleHelp.Render(truncatePath(row.target, targetW))
			}
			rowText := renderPopupToggleTableRenderedRow(p, selected, mark, nameCell, targetCell, nameW, targetW)
			sb.WriteString(rowText)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	ctx := hintCtxHostGroupDots
	if m.groupDotsEditor.searchActive {
		ctx = hintCtxHostGroupDotsSearch
	}
	sb.WriteString(renderPickerHintItems(m, contentW, contextHintItems(m, ctx)))
	return lipgloss.NewStyle().Width(contentW).Render(sb.String())
}

func renderScopePicker(m Model) string {
	p := m.palette
	t := m.selectedTool()
	if t == nil {
		return p.styleHelp.Render("no tool selected")
	}
	contentW := scopePickerContentWidth(m)
	labelW, detailW := scopePickerColumnWidths(m)
	labelW, detailW = fitPickerChoiceColumnWidths(contentW, true, labelW, detailW)
	rows := make([]pickerChoiceRow, 0, len(m.scopeOptions))
	for i, opt := range m.scopeOptions {
		selected := i == m.scopeCursor
		row := pickerChoiceRow{selected: selected, label: opt.label, detail: opt.detail, mark: "[ ]"}
		if opt.checked {
			row.mark = "[x]"
		}
		row.style = p.styleNormal
		if selected {
			row.style = p.styleActiveText
		}
		rows = append(rows, row)
	}
	var sb strings.Builder
	sb.WriteString(renderPickerChoiceRows(p, rows, labelW, detailW))
	sb.WriteString("\n")
	hints := toggleSaveCancelActionItems(m)
	if m.mode == viewProviderScope {
		hints = selectCancelActionItems(m)
	}
	sb.WriteString(renderPickerHintItems(m, contentW, hints))
	return lipgloss.NewStyle().Width(contentW).Render(sb.String())
}

func renderPickerSectionLabel(p palette, label string) string {
	return "  " + p.styleHelp.Render(pickerSectionLabel(label))
}

func pickerSectionLabel(label string) string {
	switch label {
	case "Current Host":
		return "current host"
	case "Inactive":
		return "inactive groups"
	default:
		return strings.ToLower(label)
	}
}

func renderPickerHintItems(m Model, width int, hints []hintItem) string {
	width = max(width, 1)
	return popupDivider(m.palette, width) + "\n" + renderPopupActionHintText(m.palette, width, hints)
}

func pickerCursor(p palette, selected bool) string {
	if selected {
		return p.styleCursor.Render("›") + " "
	}
	return "  "
}

const (
	popupRowPrefixWidth    = 6
	popupRowSeparatorWidth = 2
	popupNameSlotMin       = 6
)

func popupToggleTableWidth(longestName, longestSecondary int) int {
	return popupRowPrefixWidth + max(longestName, popupNameSlotMin) + popupRowSeparatorWidth + max(longestSecondary, 1)
}

func popupToggleTableColumnWidths(contentW, longestSecondary int) (int, int) {
	maxSecondary := max(contentW-popupRowPrefixWidth-popupRowSeparatorWidth-popupNameSlotMin, 1)
	secondaryW := min(max(longestSecondary, 1), maxSecondary)
	nameW := max(contentW-popupRowPrefixWidth-popupRowSeparatorWidth-secondaryW, 1)
	return nameW, secondaryW
}

func renderPopupToggleTableRenderedRow(p palette, selected bool, mark, nameCell, secondaryCell string, nameW, secondaryW int) string {
	row := pickerCursor(p, selected)
	if mark != "" {
		row += p.styleHelp.Render(mark) + " "
	}
	row += fitRenderedCell(nameCell, nameW)
	row += strings.Repeat(" ", popupRowSeparatorWidth)
	row += renderRightAlignedCell(secondaryCell, secondaryW)
	return row
}

func fitRenderedCell(rendered string, width int) string {
	return rendered + strings.Repeat(" ", max(width-lipgloss.Width(rendered), 0))
}

func renderRightAlignedCell(rendered string, width int) string {
	return strings.Repeat(" ", max(width-lipgloss.Width(rendered), 0)) + rendered
}

// pickerToggleRowWidth returns the rendered width of one "[ ] label  detail"
// picker row. detailW=0 means "no detail column".
func pickerToggleRowWidth(labelW, detailW int) int {
	w := 2 + len("[ ]") + 1 + labelW
	if detailW > 0 {
		w += 2 + detailW
	}
	return w
}

type pickerChoiceRow struct {
	section   string
	mark      string
	label     string
	detail    string
	inputView string
	selected  bool
	style     lipgloss.Style
}

func renderPickerChoiceRows(p palette, rows []pickerChoiceRow, labelW, detailW int) string {
	var sb strings.Builder
	lastSection := ""
	for _, row := range rows {
		if row.section != "" && row.section != lastSection {
			if lastSection != "" {
				sb.WriteString("\n")
			}
			sb.WriteString(renderPickerSectionLabel(p, row.section))
			sb.WriteString("\n")
			lastSection = row.section
		}
		if row.inputView != "" {
			sb.WriteString(pickerCursor(p, row.selected))
			sb.WriteString(row.inputView)
			sb.WriteString("\n")
			continue
		}
		sb.WriteString(renderChoiceRow(p, row.selected, row.mark, row.label, row.detail, labelW, detailW, row.style))
		sb.WriteString("\n")
	}
	return sb.String()
}

func fitPickerChoiceColumnWidths(contentW int, hasMark bool, labelW, detailW int) (int, int) {
	prefixW := 2
	if hasMark {
		prefixW += lipgloss.Width("[ ] ")
	}
	available := max(contentW-prefixW, 1)
	if detailW <= 0 {
		return min(labelW, available), 0
	}
	available = max(available-2, 1)
	if labelW+detailW <= available {
		return labelW, detailW
	}
	if labelW < available {
		return labelW, max(available-labelW, 1)
	}
	labelMin := min(labelW, max(popupNameSlotMin, available/2))
	detailW = min(detailW, max(available-labelMin, 1))
	labelW = max(available-detailW, 1)
	return labelW, detailW
}

func renderChoiceRow(p palette, selected bool, mark, label, detail string, labelW, detailW int, labelStyle lipgloss.Style) string {
	if selected {
		labelStyle = p.styleActiveText
	}
	row := pickerCursor(p, selected)
	if mark != "" {
		row += p.styleHelp.Render(mark) + " "
	}
	label = fitCellText(label, labelW)
	row += labelStyle.Render(label) + strings.Repeat(" ", max(labelW-lipgloss.Width(label), 0))
	if detail != "" {
		detail = fitCellText(detail, detailW)
		row += "  " + p.styleHelp.Render(fmt.Sprintf("%-*s", detailW, detail))
	}
	return row
}

func scopePickerContentWidth(m Model) int {
	labelW, detailW := scopePickerColumnWidths(m)
	width := 0
	for _, opt := range m.scopeOptions {
		rowDetailW := 0
		if opt.detail != "" {
			rowDetailW = detailW
		}
		width = max(width, pickerToggleRowWidth(labelW, rowDetailW))
	}
	width = max(width, lipgloss.Width(toggleSaveCancelHintText(m)))
	return popupContentWidth(m, width, 34, 64)
}

func scopePickerPopupFrame(m Model, title string) popupFrame {
	const paddingX = 2
	return popupFrame{
		Title:          title,
		PaddingY:       1,
		PaddingX:       paddingX,
		Width:          scopePickerContentWidth(m) + 2 + paddingX*2,
		NoTitleDivider: true,
	}
}

func groupEditorContentWidth(m Model) int {
	width := lipgloss.Width("Edit Groups: " + m.hostEditName)
	width = max(width, lipgloss.Width(toggleSaveCancelHintText(m)))
	width = max(width, lipgloss.Width(confirmCancelHintText(m, "create")))
	for _, group := range m.hostGroupPicker {
		label := hostAssignmentPickerLabel(m, group)
		rowW := 2 + lipgloss.Width("[ ]") + 1 + lipgloss.Width(label)
		if isNewGroupSentinel(group) {
			rowW = 2 + lipgloss.Width(group)
		}
		width = max(width, rowW)
	}
	return popupContentWidth(m, width, 34, 64)
}

func groupToolsContentWidth(m Model) int {
	widthModel := unfilteredHostGroupToolsModel(m)
	rows := groupToolRows(widthModel)
	longestName, _ := groupToolsColumnWidths(m, rows)
	longestSecondary := groupToolsSecondaryWidth(m, rows)
	width := popupToggleTableWidth(longestName, longestSecondary)
	for _, label := range []string{"enabled", "disabled", "ignored"} {
		width = max(width, lipgloss.Width(pickerSectionLabel(label))+2)
	}
	width = max(width, lipgloss.Width(renderContextHints(m, hintCtxHostGroupTools, "")))
	if m.groupToolsEditor.searchActive {
		width = max(width, lipgloss.Width(renderEmptyAwareTextInputView(m.palette, m.settingsInput, m.settingsInput.Placeholder, 0)))
	}
	if filterBar := renderHostGroupToolsFilterBar(widthModel); filterBar != "" {
		width = max(width, lipgloss.Width(filterBar))
	}
	width = max(width, lipgloss.Width("Edit Tools: "+m.groupToolsEditor.group))
	return popupContentWidth(m, width, 40, 72)
}

func groupToolsPopupContentHeight(m Model) int {
	base := unfilteredHostGroupToolsModel(m)
	return max(lipgloss.Height(renderHostGroupToolsEditor(base)), lipgloss.Height(renderHostGroupToolsEditor(m)))
}

func groupDotsContentWidth(m Model) int {
	widthModel := unfilteredHostGroupDotsModel(m)
	rows := groupDotRows(widthModel)
	longestName, longestTarget := groupDotsColumnWidths(rows)
	width := popupToggleTableWidth(longestName, longestTarget)
	for _, label := range []string{"enabled", "disabled", "ignored"} {
		width = max(width, lipgloss.Width(pickerSectionLabel(label))+2)
	}
	width = max(width, lipgloss.Width(renderContextHints(m, hintCtxHostGroupDots, "")))
	if m.groupDotsEditor.searchActive {
		width = max(width, lipgloss.Width(renderEmptyAwareTextInputView(m.palette, m.settingsInput, m.settingsInput.Placeholder, 0)))
	}
	width = max(width, lipgloss.Width("Edit Dots: "+m.groupDotsEditor.group))
	return popupContentWidth(m, width, 40, 72)
}

func groupDotsPopupContentHeight(m Model) int {
	base := unfilteredHostGroupDotsModel(m)
	return max(lipgloss.Height(renderHostGroupDotsEditor(base)), lipgloss.Height(renderHostGroupDotsEditor(m)))
}

func unfilteredHostGroupDotsModel(m Model) Model {
	m.groupDotsEditor.search = ""
	m.groupDotsEditor.searchActive = false
	return m
}

func unfilteredHostGroupToolsModel(m Model) Model {
	m.groupToolsProviderIdx = 0
	m.groupToolsEditor.search = ""
	m.groupToolsEditor.searchActive = false
	return m
}

func groupToolsColumnWidths(m Model, rows []groupToolRow) (int, int) {
	nameW := len("tool")
	providerW := len("provider")
	for _, row := range rows {
		if row.tool == nil {
			continue
		}
		nameW = max(nameW, lipgloss.Width(nameDisplayText(row.tool)))
		providerW = max(providerW, lipgloss.Width(providerLabelForToolWithPin(row.tool, providerPinForTool(row.tool, m.toolProviderPins), m.effectiveSystemManager, m.effectivePythonManager, m.effectiveNodeManager)))
	}
	return nameW, providerW
}

func groupToolsSecondaryWidth(m Model, rows []groupToolRow) int {
	width := len("provider")
	for _, row := range rows {
		if row.tool == nil {
			continue
		}
		w := lipgloss.Width(providerLabelForToolWithPin(row.tool, providerPinForTool(row.tool, m.toolProviderPins), m.effectiveSystemManager, m.effectivePythonManager, m.effectiveNodeManager))
		if toolHasPrivilegeMarker(row.tool, m.effectiveSystemManager) {
			w += lipgloss.Width(iconPrivileged) + listColumnGap
		}
		switch {
		case row.groupIgnore:
			w += popupRowSeparatorWidth + lipgloss.Width("ignored")
		case row.toolIgnore:
			w += popupRowSeparatorWidth + lipgloss.Width("ignored: tool")
		}
		width = max(width, w)
	}
	return width
}

func renderHostGroupToolSecondary(m Model, row groupToolRow, width int, selected bool) string {
	if row.tool == nil {
		return ""
	}
	p := m.palette
	providerPin := providerPinForTool(row.tool, m.toolProviderPins)
	providerLabel := providerLabelForToolWithPin(row.tool, providerPin, m.effectiveSystemManager, m.effectivePythonManager, m.effectiveNodeManager)
	privW := 0
	if toolHasPrivilegeMarker(row.tool, m.effectiveSystemManager) {
		privW = lipgloss.Width(iconPrivileged)
	}
	privGap := 0
	if privW > 0 {
		privGap = listColumnGap
	}
	providerW := min(lipgloss.Width(providerDisplayTextForToolWithPin(row.tool, providerPin, m.effectiveSystemManager, m.effectivePythonManager, m.effectiveNodeManager)), max(width-privW-privGap, 1))
	priv := renderPrivilegeCol(privW > 0, privW, listRowColumnStyle(selected, p.styleHelp))
	provider := renderProviderColWithExplicit(p, row.tool.Provider, row.tool.InstalledWith, providerPin, m.effectiveSystemManager, m.effectivePythonManager, m.effectiveNodeManager, providerLabel, providerW, selected, false)
	rendered := renderCellGroup(privilegeProviderCells(priv, privW, provider, providerW), listColumnGap)
	remaining := max(width-lipgloss.Width(rendered)-popupRowSeparatorWidth, 0)
	ignoreStyle := listRowColumnStyle(selected, p.styleIgnored)
	switch {
	case row.groupIgnore && remaining > 0:
		rendered += strings.Repeat(" ", popupRowSeparatorWidth) + ignoreStyle.Render(fitCellText("ignored", remaining))
	case row.toolIgnore && remaining > 0:
		rendered += strings.Repeat(" ", popupRowSeparatorWidth) + ignoreStyle.Render(fitCellText("ignored: tool", remaining))
	}
	return rendered
}

func groupDotsColumnWidths(rows []groupDotRow) (int, int) {
	nameW := len("dotfile")
	targetW := len("path")
	for _, row := range rows {
		nameW = max(nameW, lipgloss.Width(row.name))
		if row.target != "" {
			targetW = max(targetW, min(lipgloss.Width(row.target), 42))
		}
	}
	return nameW, targetW
}

func groupToolRows(m Model) []groupToolRow {
	providerFilter := groupToolsProviderFilter(m)
	query := strings.ToLower(strings.TrimSpace(m.groupToolsEditor.search))
	rows := make([]groupToolRow, 0, len(m.allTools))
	for _, t := range m.allTools {
		if t == nil || !t.Tracked || t.Name == "" {
			continue
		}
		if providerFilter != "" && providerEcosystem(t.Provider) != providerFilter {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(t.Name), query) && !strings.Contains(strings.ToLower(t.Package), query) {
			continue
		}
		enabled := m.groupToolsEditor.membership[t.Name]
		groupIgnored := m.groupToolsIgnore[t.Name]
		toolIgnored := m.toolIgnoreSet[t.Name]
		section := groupToolSectionDisabled
		switch {
		case groupIgnored || toolIgnored:
			section = groupToolSectionIgnored
		case enabled:
			section = groupToolSectionEnabled
		}
		rows = append(rows, groupToolRow{
			tool:        t,
			section:     section,
			enabled:     enabled,
			groupIgnore: groupIgnored,
			toolIgnore:  toolIgnored,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].section != rows[j].section {
			return rows[i].section < rows[j].section
		}
		return strings.ToLower(rows[i].tool.Name) < strings.ToLower(rows[j].tool.Name)
	})
	return rows
}

func groupDotNames(m Model) []string {
	seen := make(map[string]bool)
	names := make([]string, 0, len(m.dotMemberships))
	for name := range m.dotMemberships {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func groupDotRows(m Model) []groupDotRow {
	query := strings.ToLower(strings.TrimSpace(m.groupDotsEditor.search))
	statusByName := make(map[string]app.DotStatus, len(m.dotsEntries))
	for _, entry := range m.dotsEntries {
		if entry.Name != "" {
			statusByName[entry.Name] = entry
		}
	}
	names := groupDotNames(m)
	rows := make([]groupDotRow, 0, len(names))
	for _, name := range names {
		status := statusByName[name]
		target := tildePath(status.TargetPath)
		if query != "" && !strings.Contains(strings.ToLower(name), query) && !strings.Contains(strings.ToLower(target), query) {
			continue
		}
		enabled := m.groupDotsEditor.membership[name]
		ignored := status.State == app.DotStateIgnored
		section := groupDotSectionDisabled
		switch {
		case ignored:
			section = groupDotSectionIgnored
		case enabled:
			section = groupDotSectionEnabled
		}
		rows = append(rows, groupDotRow{
			name:    name,
			target:  target,
			section: section,
			enabled: enabled,
			ignored: ignored,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].section != rows[j].section {
			return rows[i].section < rows[j].section
		}
		return strings.ToLower(rows[i].name) < strings.ToLower(rows[j].name)
	})
	return rows
}

func groupToolProviders(m Model) []string {
	if len(m.providerNames) > 0 {
		return append([]string(nil), m.providerNames...)
	}
	return provider.BuiltinEcosystemNames()
}

func groupToolsProviderFilter(m Model) string {
	providers := groupToolProviders(m)
	if m.groupToolsProviderIdx <= 0 || m.groupToolsProviderIdx > len(providers) {
		return ""
	}
	return providers[m.groupToolsProviderIdx-1]
}

func renderHostGroupToolsFilterBar(m Model) string {
	providers := groupToolProviders(m)
	if len(providers) == 0 {
		return ""
	}
	bar := "  " + renderPillBar(m.palette, providers, m.groupToolsProviderIdx)
	if q := strings.TrimSpace(m.groupToolsEditor.search); q != "" && !m.groupToolsEditor.searchActive {
		bar += m.palette.styleHelp.Render("   ·   search: " + q)
	}
	return bar
}

func groupDotSectionLabel(section groupDotSection) string {
	switch section {
	case groupDotSectionEnabled:
		return "enabled"
	case groupDotSectionDisabled:
		return "disabled"
	case groupDotSectionIgnored:
		return "ignored"
	default:
		return ""
	}
}

func groupToolSectionLabel(section groupToolSection) string {
	switch section {
	case groupToolSectionEnabled:
		return "enabled"
	case groupToolSectionDisabled:
		return "disabled"
	case groupToolSectionIgnored:
		return "ignored"
	default:
		return ""
	}
}

func scopePickerColumnWidths(m Model) (int, int) {
	var labelW, detailW int
	for _, opt := range m.scopeOptions {
		labelW = max(labelW, lipgloss.Width(opt.label))
		detailW = max(detailW, lipgloss.Width(opt.detail))
	}
	return labelW, detailW
}

func ignoreScopeOptions(m Model, t *database.ToolCache) []scopeOption {
	if t == nil {
		return nil
	}
	toolChecked := m.toolIgnoreSet[t.Name]
	options := []scopeOption{{
		kind:    "tool",
		label:   "tool everywhere",
		detail:  "config tools." + t.Name + ".ignore",
		checked: toolChecked, initialChecked: toolChecked,
	}}
	for _, group := range m.toolMemberships[toolMembershipKey(t)] {
		checked := m.groupIgnoreSet[t.Name] != nil && m.groupIgnoreSet[t.Name][group]
		options = append(options, scopeOption{
			kind:    "group",
			label:   "group: " + group,
			detail:  "skip in this group",
			group:   group,
			checked: checked, initialChecked: checked,
		})
	}
	if m.hostInfo != nil && m.hostInfo.Active != "" {
		checked := m.ignoreSet[t.Name]
		options = append(options, scopeOption{
			kind:    "host",
			label:   "this host",
			detail:  "local host ignore",
			checked: checked, initialChecked: checked,
		})
	}
	return options
}

func providerScopeOptions(t *database.ToolCache) []scopeOption {
	if t == nil || t.InstalledWith == "" {
		return []scopeOption{{kind: "provider-host", label: "installed provider unknown", detail: "refresh first"}}
	}
	options := []scopeOption{
		{kind: "provider-host", label: "this tool on this host", detail: t.InstalledWith},
		{kind: "provider-tool", label: "this tool everywhere", detail: t.InstalledWith},
	}
	if ecosystem, ok := provider.BuiltinEcosystemFor(t.Provider); ok && provider.BuiltinIsEcosystem(ecosystem) {
		options = append(options, scopeOption{kind: "provider-ecosystem", label: ecosystem + " manager on this host", detail: t.InstalledWith})
	}
	return options
}

func groupPickerInputWidth(m Model) int {
	width := lipgloss.Width(groupPickerNewSentinel)
	width = max(width, lipgloss.Width("new group name…"))
	for _, g := range m.pickerGroups {
		if !isNewGroupSentinel(g) {
			width = max(width, lipgloss.Width(g))
		}
	}
	return popupContentWidth(m, width, 34, 64)
}

func groupPickerColumnWidths(m Model, current string) (int, int) {
	labelW := groupPickerInputWidth(m)
	detailW := 0
	for _, group := range m.pickerGroups {
		labelW = max(labelW, lipgloss.Width(group))
		detailW = max(detailW, lipgloss.Width(groupPickerDetail(m, group, current)))
	}
	return labelW, detailW
}

func groupMembershipColumnWidths(m Model) (int, int) {
	labelW := groupPickerInputWidth(m)
	for _, group := range m.pickerGroups {
		labelW = max(labelW, lipgloss.Width(group))
	}
	return labelW, 0
}

func groupPickerDetail(m Model, group, current string) string {
	if isNewGroupSentinel(group) {
		return ""
	}
	if group == current {
		return "current"
	}
	return ""
}

func groupPickerSection(m Model, group string) string {
	if isNewGroupSentinel(group) || !groupHasActiveHostContext(m) {
		return ""
	}
	if groupInActiveHost(m, group) {
		return "Current Host"
	}
	return "Inactive"
}

func isNewGroupSentinel(group string) bool {
	return group == groupPickerNewSentinel
}

func groupMembershipContentWidth(m Model) int {
	labelW, detailW := groupMembershipColumnWidths(m)
	width := lipgloss.Width(groupMembershipPopupTitle(m))
	for range m.pickerGroups {
		width = max(width, pickerToggleRowWidth(labelW, detailW))
	}
	width = max(width, lipgloss.Width(toggleSaveCancelHintText(m)))
	for _, label := range []string{"Current Host", "Inactive"} {
		width = max(width, lipgloss.Width(pickerSectionLabel(label))+2)
	}
	return popupContentWidth(m, width, 34, 64)
}

func groupPickerContentWidth(m Model) int {
	t := m.selectedTool()
	if t == nil {
		return popupContentWidth(m, lipgloss.Width("no tool selected"), 24, 40)
	}
	width := 0
	labelW, detailW := groupPickerColumnWidths(m, m.toolGroups[toolKey(t.Name, t.Provider)])
	for _, g := range m.pickerGroups {
		rowW := 2 + labelW
		if detail := groupPickerDetail(m, g, m.toolGroups[toolKey(t.Name, t.Provider)]); detail != "" {
			rowW += 2 + detailW
		}
		width = max(width, rowW)
	}
	inputRowWidth := 2 + lipgloss.Width(m.settingsInput.Prompt) + groupPickerInputWidth(m)
	width = max(width, inputRowWidth)

	width = max(width, lipgloss.Width(confirmCancelHintText(m, "confirm")))
	width = max(width, lipgloss.Width(confirmCancelHintText(m, "create")))
	for _, label := range []string{"Current Host", "Inactive"} {
		width = max(width, lipgloss.Width(pickerSectionLabel(label))+2)
	}
	return popupContentWidth(m, width, 34, 64)
}
