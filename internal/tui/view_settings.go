package tui

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

const (
	settingLabelWidth = 28
	firstColumnGap    = listColumnGap * 2
)

const (
	settingsRowAutoImport = iota
	settingsRowSystemPriority
	settingsRowSystemProvider
	settingsRowNodeProvider
	settingsRowPythonProvider
	settingsRowNodeManager
	settingsRowPythonManager
	settingsRowDotsRepo
	settingsRowDotsSync
	settingsRowDotsCommit
	settingsRowDotsPush
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
		label:   "System Provider",
		section: "Tools",
		hint:    hintCtxSettingsToggle,
	},
	settingsRowNodeProvider: {
		label:   "Node Provider",
		section: "Tools",
		hint:    hintCtxSettingsToggle,
	},
	settingsRowPythonProvider: {
		label:   "Python Provider",
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
		label:   "Sync on This Machine",
		section: "Dotfiles",
		hint:    hintCtxSettingsDotsSync,
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

func formatSettingLabel(label string) string {
	return fmt.Sprintf("%-*s", settingLabelWidth, label)
}

func renderSettings(m Model) string {
	p := m.palette
	var buf scrollBuf
	write := buf.write
	rowInset := rowContentInset()
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
		avail := max(contentW-lipgloss.Width(rowInset)-settingLabelWidth-firstColumnGap, 12)
		return p.styleProvider.Render(truncatePath(v, avail))
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
			help:            p.styleHelp.Render("Enable the system ecosystem provider (brew/apt/dnf/…) on this machine."),
		},
		settingsRowNodeProvider: {
			settingsRowMeta: settingsRows[settingsRowNodeProvider],
			value:           onOff(providerEnabled(provider.EcosystemNode)),
			help:            p.styleHelp.Render("Enable the node ecosystem provider (bun/pnpm/npm) on this machine."),
		},
		settingsRowPythonProvider: {
			settingsRowMeta: settingsRows[settingsRowPythonProvider],
			value:           onOff(providerEnabled(provider.EcosystemPython)),
			help:            p.styleHelp.Render("Enable the python ecosystem provider (uv/pip3) on this machine."),
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
				return p.styleHelp.Render("Commit changes automatically after dots add/remove.")
			}(),
		},
		settingsRowDotsPush: {
			settingsRowMeta: settingsRows[settingsRowDotsPush],
			value:           onOff(m.settings.DotsGit.AutoPush),
			help:            p.styleHelp.Render("Push (and commit) automatically after dots add/remove."),
		},
		settingsRowResetSettings: {
			settingsRowMeta: settingsRows[settingsRowResetSettings],
			value:           p.styleHelp.Render("[reset]"),
			help:            p.styleHelp.Render("Restore all settings to defaults (tools & profiles preserved)."),
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
			write(renderFixedGroupListRow(p, true,
				[]rowCell{leftCell(p.styleActiveText.Render(rowInset+formatSettingLabel(row.label)), settingLabelWidth+lipgloss.Width(rowInset))},
				[]rowCell{rightCell(p.styleProvider.Render("[editing]"), 0)},
				firstColumnGap, listColumnGap,
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
			write(renderFixedGroupListRow(p, true,
				[]rowCell{leftCell(labelStyle.Render(rowInset+formatSettingLabel(row.label)), settingLabelWidth+lipgloss.Width(rowInset))},
				[]rowCell{rightCell(row.value, 0)},
				firstColumnGap, listColumnGap,
			) + "\n")
		} else {
			write(renderFixedGroupListRow(p, false,
				[]rowCell{leftCell(lbl.Render(rowInset+formatSettingLabel(row.label)), settingLabelWidth+lipgloss.Width(rowInset))},
				[]rowCell{rightCell(row.value, 0)},
				firstColumnGap, listColumnGap,
			) + "\n")
		}
		if i == m.settingsCursor {
			write(detailPrefix + row.help + "\n")
			write(renderContextHints(m, settingsRowHintContext(i), hintPrefix) + "\n")
		}
	}

	return buf.render(listAvailableHeight(m))
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

func renderGroupDeletePopup(m Model) string {
	p := m.palette
	groupName := m.groupDeleteName
	if groupName == "" {
		groupName = m.selectedProfileGroupName()
	}
	if groupName == "" {
		groupName = "group"
	}
	choices := []string{
		"Move last-membership tools to base",
		"Delete last-membership logical tools",
	}
	var sb strings.Builder
	sb.WriteString(p.styleMissing.Render(groupName))
	sb.WriteString("\n\n")
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
	sb.WriteString("\n")
	sb.WriteString(renderPickerHints(m, groupDeletePopupContentWidth, confirmActionHintText(m, m.keys.Confirm, "confirm")))
	return sb.String()
}

const groupDeletePopupContentWidth = 44

func renderProfileCreatePopup(m Model) string {
	return renderNameCreatePopup(m, "profile name")
}

func renderGroupCreatePopup(m Model) string {
	return renderNameCreatePopup(m, "group name")
}

func renderNameCreatePopup(m Model, label string) string {
	p := m.palette
	contentW := profileCreatePopupWidth(m)
	input := m.settingsInput
	input.Prompt = ""
	input.Placeholder = label + "..."
	input.SetWidth(max(contentW-6, 1))

	var sb strings.Builder
	sb.WriteString(renderCreateNameField(p, input.View(), contentW))
	sb.WriteString("\n\n")
	sb.WriteString(renderPickerHints(m, contentW, confirmCancelHintText(m, "create")))
	return lipgloss.NewStyle().Width(contentW).Render(sb.String())
}

func profileCreatePopupWidth(m Model) int {
	return popupContentWidth(m, 42, 28, 42)
}

func renderCreateNameField(p palette, input string, width int) string {
	return lipgloss.NewStyle().
		Width(width).
		Border(lipgloss.NormalBorder()).
		BorderForeground(p.colMuted).
		Padding(0, 1).
		Render(input)
}

func renderProfiles(m Model) string {
	p := m.palette
	rowInset := rowContentInset()
	detailPrefix := textRowContentPrefix()
	hintPrefix := textRowHintPrefix()
	var top []string

	// ── Profile-required banner ─────────────────────────────────────────────
	if m.profileRequired {
		top = append(top,
			p.styleMissing.Render("  ⚠  No active profile for this machine."),
			p.styleHelp.Render("  Create a profile or map this host to an existing one."),
			p.styleHelp.Render("  Navigation is locked until a profile is active. Press q to quit."),
			"",
		)
	}

	names := sortedProfileNames(m.profileInfo)
	allGroupNames := buildAllGroupNames(m.groupNames)
	hostCounts := profileHostCounts(m.profileInfo)
	groupCounts := make(map[string]int, len(allGroupNames))
	for _, gn := range m.toolGroups {
		groupCounts[gn]++
	}
	groupDots := make(map[string]int, len(allGroupNames))
	for _, groups := range m.dotMemberships {
		for _, gn := range groups {
			groupDots[gn]++
		}
	}
	cols := profileTableColumnWidths(names, m.profileInfo, hostCounts, allGroupNames, groupCounts, groupDots)

	profileSection := sectionedTabSection{
		title:            "Profiles",
		blankAfterHeader: false,
	}
	if len(names) == 0 {
		profileSection.empty = []string{
			p.styleHelp.Render("  No profiles configured."),
			p.styleHelp.Render("  Press p to create a new profile."),
		}
	} else {
		for i, name := range names {
			prof := m.profileInfo.Profiles[name]
			profileGroups := append([]string(nil), prof.Groups...)
			sort.Strings(profileGroups)
			groupBadge := compactCount(len(profileGroups), "group")
			hostBadge := compactCount(hostCounts[name], "host")
			nameLabel := name
			if i == m.profileCursor {
				if m.profileRenameMode {
					inputWidth := max(m.width-lipgloss.Width("    Rename: [ ")-4, 20)
					m.settingsInput.SetWidth(inputWidth)
					profileSection.rows = append(profileSection.rows, sectionedTabRow{
						selected: true,
						line: renderFixedGroupListRow(p, true,
							[]rowCell{leftCell(p.styleActiveText.Render(nameLabel), cols.name)},
							nil,
							firstColumnGap, listColumnGap,
						),
						details: []string{
							p.styleHelp.Render(detailPrefix+"Rename: ") + "[ " + m.settingsInput.View() + " ]",
							confirmCancelHintWithPrefix(m, "save", hintPrefix),
						},
					})
					continue
				}

				groups := strings.Join(profileGroups, ", ")
				if groups == "" {
					groups = "(no groups)"
				}
				details := []string{p.styleHelp.Render(detailPrefix + "groups: " + groups)}

				if hosts := hostnamesForProfile(m.profileInfo, name); len(hosts) > 0 {
					details = append(details, p.styleProvider.Render(detailPrefix+"hosts: "+strings.Join(hosts, ", ")))
				} else {
					details = append(details, p.styleProvider.Render(detailPrefix+"hosts: (none)"))
				}

				// Inline delete confirmation.
				if m.profileDeleteConfirm {
					details = append(details, renderPressAgainActionHint(p, detailPrefix, "d", "confirm delete"))
				}

				// Default inline hints — only when profiles section is focused and no sub-mode active.
				if m.profileSection == 0 && !m.profileCreating && !m.profileRenameMode && m.profileEditMode == 0 && !m.profileDeleteConfirm {
					details = append(details, renderContextHints(m, hintCtxProfileDefault, hintPrefix))
				}
				profileSection.rows = append(profileSection.rows, sectionedTabRow{
					selected: true,
					line: renderFixedGroupListRow(p, true,
						[]rowCell{leftCell(p.styleActiveText.Render(nameLabel), cols.name)},
						[]rowCell{
							leftCell(listRowColumnStyle(true, p.styleHelp).Render(groupBadge), cols.mid),
							leftCell(listRowColumnStyle(true, p.styleProvider).Render(hostBadge), cols.tail),
						},
						firstColumnGap, listColumnGap,
					),
					details: details,
				})
			} else {
				profileSection.rows = append(profileSection.rows, sectionedTabRow{
					line: renderFixedGroupListRow(p, false,
						[]rowCell{leftCell(p.styleNormal.Render(nameLabel), cols.name)},
						[]rowCell{
							leftCell(p.styleHelp.Render(groupBadge), cols.mid),
							leftCell(p.styleHelp.Render(hostBadge), cols.tail),
						},
						firstColumnGap, listColumnGap,
					),
				})
			}
		}
	}

	// ── Groups section ──────────────────────────────────────────────────────
	groupsFocused := m.profileSection == 1
	groupSection := sectionedTabSection{
		title:            "Groups",
		blankAfterHeader: false,
	}

	for i, gn := range allGroupNames {
		count := groupCounts[gn]
		label := rowInset + gn
		toolCount := compactCount(count, "tool")
		dotCount := compactCount(groupDots[gn], "dotfile")
		isSelected := groupsFocused && i == m.groupCursor

		if isSelected {
			switch {
			case m.groupRenameMode:
				inputWidth := max(m.width-lipgloss.Width("    Rename: [ ")-4, 20)
				m.settingsInput.SetWidth(inputWidth)
				groupSection.rows = append(groupSection.rows, sectionedTabRow{
					selected: true,
					line: renderFixedGroupListRow(p, true,
						[]rowCell{leftCell(p.styleActiveText.Render(label), cols.name)},
						nil,
						firstColumnGap, listColumnGap,
					),
					details: []string{
						p.styleHelp.Render(detailPrefix+"Rename: ") + "[ " + m.settingsInput.View() + " ]",
						confirmCancelHintWithPrefix(m, "confirm", hintPrefix),
					},
				})
			case m.groupDeleteConfirm:
				groupSection.rows = append(groupSection.rows, sectionedTabRow{
					selected: true,
					line: renderFixedGroupListRow(p, true,
						[]rowCell{leftCell(p.styleMissing.Render(label), cols.name)},
						[]rowCell{
							leftCell(listRowColumnStyle(true, p.styleHelp).Render(toolCount), cols.mid),
							leftCell(listRowColumnStyle(true, p.styleProvider).Render(dotCount), cols.tail),
						},
						firstColumnGap, listColumnGap,
					),
					details: []string{confirmCancelHintWithPrefix(m, "confirm delete", hintPrefix)},
				})
			default:
				details := []string{}
				if profiles := profilesForGroup(m.profileInfo, gn); len(profiles) > 0 {
					details = append(details, p.styleHelp.Render(detailPrefix+"profiles: "+strings.Join(profiles, ", ")))
				}
				details = append(details, renderContextHints(m, hintCtxGroupDefault, hintPrefix))
				groupSection.rows = append(groupSection.rows, sectionedTabRow{
					selected: true,
					line: renderFixedGroupListRow(p, true,
						[]rowCell{leftCell(p.styleActiveText.Render(label), cols.name)},
						[]rowCell{
							leftCell(listRowColumnStyle(true, p.styleHelp).Render(toolCount), cols.mid),
							leftCell(listRowColumnStyle(true, p.styleProvider).Render(dotCount), cols.tail),
						},
						firstColumnGap, listColumnGap,
					),
					details: details,
				})
			}
		} else {
			groupSection.rows = append(groupSection.rows, sectionedTabRow{
				line: renderFixedGroupListRow(p, false,
					[]rowCell{leftCell(p.styleNormal.Render(label), cols.name)},
					[]rowCell{
						leftCell(p.styleHelp.Render(toolCount), cols.mid),
						leftCell(p.styleHelp.Render(dotCount), cols.tail),
					},
					firstColumnGap, listColumnGap,
				),
			})
		}
	}

	return renderSectionedTab(m, sectionedTab{
		leadingBlank: true,
		top:          top,
		sections:     []sectionedTabSection{profileSection, groupSection},
	})
}

type profileTableColumns struct {
	name int
	mid  int
	tail int
}

func profileTableColumnWidths(profileNames []string, info *app.ProfileInfo, hostCounts map[string]int, groupNames []string, groupCounts, groupDots map[string]int) profileTableColumns {
	cols := profileTableColumns{name: 20, mid: len("groups"), tail: len("dotfiles")}
	for _, name := range profileNames {
		profile := info.Profiles[name]
		cols.name = max(cols.name, lipgloss.Width(name))
		cols.mid = max(cols.mid, lipgloss.Width(compactCount(len(profile.Groups), "group")))
		cols.tail = max(cols.tail, lipgloss.Width(compactCount(hostCounts[name], "host")))
	}
	for _, name := range groupNames {
		cols.name = max(cols.name, lipgloss.Width(rowContentInset()+name))
		cols.mid = max(cols.mid, lipgloss.Width(compactCount(groupCounts[name], "tool")))
		cols.tail = max(cols.tail, lipgloss.Width(compactCount(groupDots[name], "dotfile")))
	}
	return cols
}

func sortedProfileNames(info *app.ProfileInfo) []string {
	if info == nil || len(info.Profiles) == 0 {
		return nil
	}
	names := make([]string, 0, len(info.Profiles))
	for n := range info.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func compactCount(n int, label string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", label)
	}
	return fmt.Sprintf("%d %ss", n, label)
}

func profileHostCounts(info *app.ProfileInfo) map[string]int {
	counts := make(map[string]int)
	if info == nil {
		return counts
	}
	for _, profile := range info.Hostnames {
		counts[profile]++
	}
	return counts
}

func hostnamesForProfile(info *app.ProfileInfo, profile string) []string {
	if info == nil {
		return nil
	}
	var hosts []string
	for host, mapped := range info.Hostnames {
		if mapped == profile {
			hosts = append(hosts, host)
		}
	}
	sort.Strings(hosts)
	return hosts
}

func profilesForGroup(info *app.ProfileInfo, group string) []string {
	if info == nil {
		return nil
	}
	var profiles []string
	for name, profile := range info.Profiles {
		if slices.Contains(profile.Groups, group) {
			profiles = append(profiles, name)
		}
	}
	sort.Strings(profiles)
	return profiles
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
			sb.WriteString(pickerCursor(p, isSelected) + m.settingsInput.View() + "\n")
			continue
		}

		style := p.styleNormal
		switch {
		case isNewGroupSentinel(g):
			style = p.styleProvider
		case groupHasActiveProfileContext(m) && !groupInActiveProfile(m, g):
			style = p.styleHelp
		case isSelected:
			style = p.styleActiveText
		}
		sb.WriteString(renderChoiceRow(p, isSelected, "", g, groupPickerDetail(m, g, group), labelW, detailW, style) + "\n")
	}
	sb.WriteString("\n")
	if m.pickerCreatingGroup {
		sb.WriteString(renderPickerHints(m, contentW, confirmCancelHintText(m, "create")))
	} else {
		sb.WriteString(renderPickerHints(m, contentW, confirmCancelHintText(m, "confirm")))
	}
	return lipgloss.NewStyle().Width(contentW).Render(sb.String())
}

func renderGroupMembershipPicker(m Model) string {
	p := m.palette
	targetName, memberships, ok := m.selectedMembershipTarget()
	if !ok || targetName == "" {
		return p.styleHelp.Render("no entry selected")
	}
	contentW := groupMembershipContentWidth(m)
	labelW, detailW := groupMembershipColumnWidths(m)
	labelW, detailW = fitPickerChoiceColumnWidths(contentW, true, labelW, detailW)
	rows := make([]pickerChoiceRow, 0, len(m.pickerGroups))
	for i, group := range m.pickerGroups {
		selected := i == m.pickerCursor
		row := pickerChoiceRow{section: groupPickerSection(m, group), selected: selected, label: group}
		if m.pickerCreatingGroup && isNewGroupSentinel(group) {
			row.inputView = m.settingsInput.View()
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
		if slices.Contains(memberships, group) {
			row.mark = "[x]"
		}
		row.style = p.styleNormal
		if selected {
			row.style = p.styleActiveText
		} else if groupHasActiveProfileContext(m) && !groupInActiveProfile(m, group) {
			row.style = p.styleHelp
		}
		rows = append(rows, row)
	}
	var sb strings.Builder
	sb.WriteString(renderPickerChoiceRows(p, rows, labelW, detailW))
	sb.WriteString("\n")
	sb.WriteString(renderPickerHints(m, contentW, toggleSaveCancelHintText(m)))
	return lipgloss.NewStyle().Width(contentW).Render(sb.String())
}

func renderProfileGroupEditor(m Model) string {
	p := m.palette
	contentW := profileGroupEditorContentWidth(m)
	labelW := 0
	for _, group := range m.profileGroupPicker {
		labelW = max(labelW, lipgloss.Width(group))
	}
	labelW, _ = fitPickerChoiceColumnWidths(contentW, true, labelW, 0)
	rows := make([]pickerChoiceRow, 0, len(m.profileGroupPicker))
	for i, group := range m.profileGroupPicker {
		selected := i == m.profileGroupIdx
		row := pickerChoiceRow{selected: selected, label: group}
		if m.pickerCreatingGroup && selected && isNewGroupSentinel(group) {
			m.settingsInput.SetWidth(max(contentW-2, 1))
			row.inputView = m.settingsInput.View()
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
		if slices.Contains(m.profileGroupDraft, group) {
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
	hint := toggleSaveCancelHintText(m)
	if m.pickerCreatingGroup {
		hint = confirmCancelHintText(m, "create")
	}
	sb.WriteString(renderPickerHints(m, contentW, hint))
	return lipgloss.NewStyle().Width(contentW).Render(sb.String())
}

func renderProfileHostEditor(m Model) string {
	p := m.palette
	profile := m.profileEditName
	contentW := profileHostEditorContentWidth(m)
	labelW, detailW := profileHostEditorColumnWidths(m)
	labelW, detailW = fitPickerChoiceColumnWidths(contentW, true, labelW, detailW)
	rows := make([]pickerChoiceRow, 0, len(m.profileHostPicker))
	for i, host := range m.profileHostPicker {
		selected := i == m.profileHostIdx
		row := pickerChoiceRow{selected: selected, label: host, detail: profileHostDetail(m, host), mark: "[ ]"}
		if m.profileHostDraft[host] == profile {
			row.mark = "[x]"
		}
		row.style = p.styleNormal
		if selected {
			row.style = p.styleActiveText
		} else if m.profileHostDraft[host] != "" && m.profileHostDraft[host] != profile {
			row.style = p.styleHelp
		}
		rows = append(rows, row)
	}
	var sb strings.Builder
	sb.WriteString(renderPickerChoiceRows(p, rows, labelW, detailW))
	sb.WriteString("\n")
	sb.WriteString(renderPickerHints(m, contentW, toggleSaveCancelHintText(m)))
	return lipgloss.NewStyle().Width(contentW).Render(sb.String())
}

func renderProfileGroupToolsEditor(m Model) string {
	p := m.palette
	contentW := profileGroupToolsContentWidth(m)
	rows := profileGroupToolRows(m)
	var sb strings.Builder

	if m.groupToolsEditor.searchActive {
		m.settingsInput.SetWidth(max(contentW-2, 1))
		sb.WriteString(p.styleHelp.Render("search"))
		sb.WriteString("\n")
		sb.WriteString(m.settingsInput.View())
		sb.WriteString("\n")
	}

	if filterBar := renderProfileGroupToolsFilterBar(m); filterBar != "" {
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
		nameW := popupNameSlot(contentW)
		providerW := popupSecondaryWidth(contentW)
		lastSection := profileGroupToolSection(-1)
		for i, row := range rows {
			if row.section != lastSection {
				if i > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(renderPickerSectionLabel(p, profileGroupToolSectionLabel(row.section)))
				sb.WriteString("\n")
				lastSection = row.section
			}
			selected := i == m.groupToolsEditor.cursor
			mark := "[ ]"
			if row.enabled {
				mark = "[x]"
			}
			labelStyle := p.styleNormal
			if row.section == profileGroupToolSectionIgnored {
				labelStyle = p.styleIgnored
			}
			if selected {
				labelStyle = p.styleActiveText
			}
			rowText := pickerCursor(p, selected)
			rowText += p.styleHelp.Render(mark) + " "
			rowText += renderNameWithPackage(p, labelStyle, row.tool, nameW, selected)
			rowText += "  "
			providerLabel := providerLabelForTool(row.tool, m.effectiveSystemManager, m.effectivePythonManager, m.effectiveNodeManager)
			rowText += renderProviderCol(p, row.tool.Provider, row.tool.InstalledWith, m.effectiveSystemManager, m.effectivePythonManager, m.effectiveNodeManager, providerLabel, providerW, selected, false)
			if row.groupIgnore {
				rowText += "  " + listRowColumnStyle(selected, p.styleIgnored).Render("ignored")
			} else if row.toolIgnore {
				rowText += "  " + listRowColumnStyle(selected, p.styleIgnored).Render("ignored: tool")
			}
			sb.WriteString(rowText)
			sb.WriteString("\n")
			if selected {
				sb.WriteString(renderProfileGroupToolRowHints(m, row))
				sb.WriteString("\n")
			}
		}
		sb.WriteString("\n")
	}

	ctx := hintCtxProfileGroupTools
	if m.groupToolsEditor.searchActive {
		ctx = hintCtxProfileGroupToolsSearch
	}
	sb.WriteString(renderPickerHints(m, contentW, renderContextHints(m, ctx, "")))
	return lipgloss.NewStyle().Width(contentW).Render(sb.String())
}

func renderProfileGroupToolRowHints(m Model, row profileGroupToolRow) string {
	toggleDesc := "enable"
	if row.enabled {
		toggleDesc = "disable"
	}
	ignoreDesc := "ignore"
	if row.groupIgnore {
		ignoreDesc = "unignore"
	}
	return renderInlineHints(m.palette, []hintItem{
		hintFromBindingDesc(m.keys.Toggle, toggleDesc),
		hintFromBindingDesc(m.keys.Ignore, ignoreDesc),
	}, textRowHintPrefix())
}

func renderProfileGroupDotsEditor(m Model) string {
	p := m.palette
	contentW := profileGroupDotsContentWidth(m)
	rows := profileGroupDotRows(m)
	var sb strings.Builder

	if m.groupDotsEditor.searchActive {
		m.settingsInput.SetWidth(max(contentW-2, 1))
		sb.WriteString(p.styleHelp.Render("search"))
		sb.WriteString("\n")
		sb.WriteString(m.settingsInput.View())
		sb.WriteString("\n\n")
	}

	if len(rows) == 0 {
		sb.WriteString(p.styleHelp.Render("no configured dotfiles match"))
		sb.WriteString("\n\n")
	} else {
		nameW := popupNameSlot(contentW)
		targetW := popupSecondaryWidth(contentW)
		lastSection := profileGroupDotSection(-1)
		for i, row := range rows {
			if row.section != lastSection {
				if i > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(renderPickerSectionLabel(p, profileGroupDotSectionLabel(row.section)))
				sb.WriteString("\n")
				lastSection = row.section
			}
			selected := i == m.groupDotsEditor.cursor
			mark := "[ ]"
			if row.enabled {
				mark = "[x]"
			}
			labelStyle := p.styleNormal
			if row.section == profileGroupDotSectionIgnored {
				labelStyle = p.styleIgnored
			}
			if selected {
				labelStyle = p.styleActiveText
			}
			rowText := pickerCursor(p, selected)
			rowText += p.styleHelp.Render(mark) + " "
			displayName := truncatePath(row.name, nameW)
			rowText += labelStyle.Render(displayName) + strings.Repeat(" ", max(nameW-lipgloss.Width(displayName), 0))
			if row.target != "" {
				rowText += "  " + p.styleHelp.Render(fmt.Sprintf("%-*s", targetW, truncatePath(row.target, targetW)))
			}
			sb.WriteString(rowText)
			sb.WriteString("\n")
			if selected {
				sb.WriteString(renderProfileGroupDotRowHints(m, row))
				sb.WriteString("\n")
			}
		}
		sb.WriteString("\n")
	}

	ctx := hintCtxProfileGroupDots
	if m.groupDotsEditor.searchActive {
		ctx = hintCtxProfileGroupDotsSearch
	}
	sb.WriteString(renderPickerHints(m, contentW, renderContextHints(m, ctx, "")))
	return lipgloss.NewStyle().Width(contentW).Render(sb.String())
}

func renderProfileGroupDotRowHints(m Model, row profileGroupDotRow) string {
	toggleDesc := "enable"
	if row.enabled {
		toggleDesc = "disable"
	}
	return renderInlineHints(m.palette, []hintItem{
		hintFromBindingDesc(m.keys.Toggle, toggleDesc),
	}, textRowHintPrefix())
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
	sb.WriteString(renderPickerHints(m, contentW, toggleSaveCancelHintText(m)))
	return lipgloss.NewStyle().Width(contentW).Render(sb.String())
}

func renderPickerSectionHeader(p palette, label string, width int) string {
	if label == "" {
		return p.styleSep.Render(strings.Repeat("─", max(width, 1)))
	}
	return renderSectionHeader(p, label, width)
}

func renderPickerSectionLabel(p palette, label string) string {
	return "  " + p.styleHelp.Render(pickerSectionLabel(label))
}

func pickerSectionLabel(label string) string {
	switch label {
	case "Current Profile":
		return "current profile"
	case "Inactive":
		return "inactive groups"
	default:
		return strings.ToLower(label)
	}
}

func renderPickerHints(m Model, width int, hint string) string {
	dividerWidth := max(width-2, 1)
	return renderPickerSectionHeader(m.palette, "", dividerWidth) + "\n" +
		lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(hint)
}

func pickerCursor(p palette, selected bool) string {
	if selected {
		return p.styleCursor.Render("›") + " "
	}
	return "  "
}

const (
	popupSecondColumnFraction = 30
	popupRowPrefixWidth       = 6
	popupRowSeparatorWidth    = 2
	popupSecondColumnMinStart = 12
	popupNameSlotMin          = 6
)

func popupSecondColumnStart(contentW int) int {
	start := contentW * popupSecondColumnFraction / 100
	if start < popupSecondColumnMinStart {
		start = popupSecondColumnMinStart
	}
	return start
}

func popupNameSlot(contentW int) int {
	return max(popupSecondColumnStart(contentW)-popupRowPrefixWidth, popupNameSlotMin)
}

func popupSecondaryWidth(contentW int) int {
	return max(contentW-popupSecondColumnStart(contentW)-popupRowSeparatorWidth, 1)
}

func popupContentWidthFor(longestName, longestSecondary int) int {
	nameMin := (longestName+popupRowPrefixWidth)*100/popupSecondColumnFraction + 1
	secondaryMin := (longestSecondary+popupRowSeparatorWidth)*100/(100-popupSecondColumnFraction) + 1
	return max(nameMin, secondaryMin)
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
	labelMax := max(available*45/100, 1)
	labelW = min(labelW, labelMax)
	detailW = min(detailW, max(available-labelW, 1))
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

func profileGroupEditorContentWidth(m Model) int {
	width := lipgloss.Width(toggleSaveCancelHintText(m))
	width = max(width, lipgloss.Width(confirmCancelHintText(m, "create")))
	for _, group := range m.profileGroupPicker {
		rowW := 2 + lipgloss.Width("[ ]") + 1 + lipgloss.Width(group)
		if isNewGroupSentinel(group) {
			rowW = 2 + lipgloss.Width(group)
		}
		width = max(width, rowW)
	}
	return popupContentWidth(m, width, 34, 64)
}

func profileHostEditorContentWidth(m Model) int {
	labelW, detailW := profileHostEditorColumnWidths(m)
	width := 0
	for range m.profileHostPicker {
		width = max(width, pickerToggleRowWidth(labelW, detailW))
	}
	width = max(width, lipgloss.Width(toggleSaveCancelHintText(m)))
	return popupContentWidth(m, width, 34, 64)
}

func profileGroupToolsContentWidth(m Model) int {
	widthModel := unfilteredProfileGroupToolsModel(m)
	rows := profileGroupToolRows(widthModel)
	longestName, longestProvider := profileGroupToolsColumnWidths(m, rows)
	longestSecondary := longestProvider
	for _, row := range rows {
		secondary := longestProvider
		if row.groupIgnore {
			secondary += 2 + lipgloss.Width("ignored")
		} else if row.toolIgnore {
			secondary += 2 + lipgloss.Width("ignored: tool")
		}
		longestSecondary = max(longestSecondary, secondary)
	}
	width := popupContentWidthFor(longestName, longestSecondary)
	for _, row := range rows {
		width = max(width, lipgloss.Width(renderProfileGroupToolRowHints(m, row)))
	}
	for _, label := range []string{"enabled", "disabled", "ignored"} {
		width = max(width, lipgloss.Width(pickerSectionLabel(label))+2)
	}
	width = max(width, lipgloss.Width(renderContextHints(m, hintCtxProfileGroupTools, "")))
	if m.groupToolsEditor.searchActive {
		width = max(width, lipgloss.Width(m.settingsInput.View()))
	}
	if filterBar := renderProfileGroupToolsFilterBar(widthModel); filterBar != "" {
		width = max(width, lipgloss.Width(filterBar))
	}
	return popupContentWidth(m, width, 44, 88)
}

func profileGroupToolsPopupContentHeight(m Model) int {
	base := unfilteredProfileGroupToolsModel(m)
	return max(lipgloss.Height(renderProfileGroupToolsEditor(base)), lipgloss.Height(renderProfileGroupToolsEditor(m)))
}

func profileGroupDotsContentWidth(m Model) int {
	widthModel := unfilteredProfileGroupDotsModel(m)
	rows := profileGroupDotRows(widthModel)
	longestName, longestTarget := profileGroupDotsColumnWidths(rows)
	width := popupContentWidthFor(longestName, longestTarget)
	for _, row := range rows {
		width = max(width, lipgloss.Width(renderProfileGroupDotRowHints(m, row)))
	}
	for _, label := range []string{"enabled", "disabled", "ignored"} {
		width = max(width, lipgloss.Width(pickerSectionLabel(label))+2)
	}
	width = max(width, lipgloss.Width(renderContextHints(m, hintCtxProfileGroupDots, "")))
	if m.groupDotsEditor.searchActive {
		width = max(width, lipgloss.Width(m.settingsInput.View()))
	}
	return popupContentWidth(m, width, 44, 88)
}

func profileGroupDotsPopupContentHeight(m Model) int {
	base := unfilteredProfileGroupDotsModel(m)
	return max(lipgloss.Height(renderProfileGroupDotsEditor(base)), lipgloss.Height(renderProfileGroupDotsEditor(m)))
}

func unfilteredProfileGroupDotsModel(m Model) Model {
	m.groupDotsEditor.search = ""
	m.groupDotsEditor.searchActive = false
	return m
}

func unfilteredProfileGroupToolsModel(m Model) Model {
	m.groupToolsProviderIdx = 0
	m.groupToolsEditor.search = ""
	m.groupToolsEditor.searchActive = false
	return m
}

func profileGroupToolsColumnWidths(m Model, rows []profileGroupToolRow) (int, int) {
	nameW := len("tool")
	providerW := len("provider")
	for _, row := range rows {
		if row.tool == nil {
			continue
		}
		nameW = max(nameW, lipgloss.Width(nameDisplayText(row.tool)))
		providerW = max(providerW, lipgloss.Width(providerLabelForTool(row.tool, m.effectiveSystemManager, m.effectivePythonManager, m.effectiveNodeManager)))
	}
	return nameW, providerW
}

func profileGroupDotsColumnWidths(rows []profileGroupDotRow) (int, int) {
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

func profileGroupToolRows(m Model) []profileGroupToolRow {
	providerFilter := profileGroupToolsProviderFilter(m)
	query := strings.ToLower(strings.TrimSpace(m.groupToolsEditor.search))
	rows := make([]profileGroupToolRow, 0, len(m.allTools))
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
		section := profileGroupToolSectionDisabled
		switch {
		case groupIgnored || toolIgnored:
			section = profileGroupToolSectionIgnored
		case enabled:
			section = profileGroupToolSectionEnabled
		}
		rows = append(rows, profileGroupToolRow{
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

func profileGroupDotNames(m Model) []string {
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

func profileGroupDotRows(m Model) []profileGroupDotRow {
	query := strings.ToLower(strings.TrimSpace(m.groupDotsEditor.search))
	statusByName := make(map[string]app.DotStatus, len(m.dotsEntries))
	for _, entry := range m.dotsEntries {
		if entry.Name != "" {
			statusByName[entry.Name] = entry
		}
	}
	names := profileGroupDotNames(m)
	rows := make([]profileGroupDotRow, 0, len(names))
	for _, name := range names {
		status := statusByName[name]
		target := tildePath(status.TargetPath)
		if query != "" && !strings.Contains(strings.ToLower(name), query) && !strings.Contains(strings.ToLower(target), query) {
			continue
		}
		enabled := m.groupDotsEditor.membership[name]
		ignored := status.State == app.DotStateIgnored
		section := profileGroupDotSectionDisabled
		switch {
		case ignored:
			section = profileGroupDotSectionIgnored
		case enabled:
			section = profileGroupDotSectionEnabled
		}
		rows = append(rows, profileGroupDotRow{
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

func profileGroupToolProviders(m Model) []string {
	if len(m.providerNames) > 0 {
		return append([]string(nil), m.providerNames...)
	}
	return provider.BuiltinEcosystemNames()
}

func profileGroupToolsProviderFilter(m Model) string {
	providers := profileGroupToolProviders(m)
	if m.groupToolsProviderIdx <= 0 || m.groupToolsProviderIdx > len(providers) {
		return ""
	}
	return providers[m.groupToolsProviderIdx-1]
}

func renderProfileGroupToolsFilterBar(m Model) string {
	providers := profileGroupToolProviders(m)
	if len(providers) == 0 {
		return ""
	}
	bar := "  " + renderPillBar(m.palette, providers, m.groupToolsProviderIdx)
	if q := strings.TrimSpace(m.groupToolsEditor.search); q != "" && !m.groupToolsEditor.searchActive {
		bar += m.palette.styleHelp.Render("   ·   search: " + q)
	}
	return bar
}

func profileGroupDotSectionLabel(section profileGroupDotSection) string {
	switch section {
	case profileGroupDotSectionEnabled:
		return "enabled"
	case profileGroupDotSectionDisabled:
		return "disabled"
	case profileGroupDotSectionIgnored:
		return "ignored"
	default:
		return ""
	}
}

func profileGroupToolSectionLabel(section profileGroupToolSection) string {
	switch section {
	case profileGroupToolSectionEnabled:
		return "enabled"
	case profileGroupToolSectionDisabled:
		return "disabled"
	case profileGroupToolSectionIgnored:
		return "ignored"
	default:
		return ""
	}
}

func profileHostEditorColumnWidths(m Model) (int, int) {
	var labelW, detailW int
	for _, host := range m.profileHostPicker {
		labelW = max(labelW, lipgloss.Width(host))
		detailW = max(detailW, lipgloss.Width(profileHostDetail(m, host)))
	}
	return labelW, detailW
}

func profileHostDetail(m Model, host string) string {
	profile := m.profileHostDraft[host]
	if profile == "" {
		profile = "unmapped"
	}
	if host == shortHostname() {
		return profile + " · this host"
	}
	return profile
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
	if m.profileInfo != nil && m.profileInfo.Active != "" {
		checked := m.ignoreSet[t.Name]
		options = append(options, scopeOption{
			kind:    "profile",
			label:   "profile: " + m.profileInfo.Active,
			detail:  "local profile ignore",
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
	if isNewGroupSentinel(group) || !groupHasActiveProfileContext(m) {
		return ""
	}
	if groupInActiveProfile(m, group) {
		return "Current Profile"
	}
	return "Inactive"
}

func isNewGroupSentinel(group string) bool {
	return group == groupPickerNewSentinel
}

func groupMembershipContentWidth(m Model) int {
	labelW, detailW := groupMembershipColumnWidths(m)
	width := 0
	for range m.pickerGroups {
		width = max(width, pickerToggleRowWidth(labelW, detailW))
	}
	width = max(width, lipgloss.Width(toggleSaveCancelHintText(m)))
	for _, label := range []string{"Current Profile", "Inactive"} {
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
	for _, label := range []string{"Current Profile", "Inactive"} {
		width = max(width, lipgloss.Width(pickerSectionLabel(label))+2)
	}
	return popupContentWidth(m, width, 34, 64)
}
