package tui

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/app"
)

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
	sb.WriteString(p.styleMissing.Render(fitCellText(groupName, groupDeletePopupContentWidth)))
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
	if m.assignmentSection == 0 {
		return renderNameCreatePopup(m, "hostname")
	}
	return renderNameCreatePopup(m, "group name")
}

func groupCreatePopupTitle(m Model) string {
	if m.assignmentSection == 0 {
		return "New Host"
	}
	return "New Group"
}

func renderHostCreateChoicePopup(m Model, width int) string {
	popupModel := m
	popupModel.width = max(width, 1)
	popupModel.setupStep = m.hostCreateStep + 6
	return renderSetup(popupModel)
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

	hosts := app.PrioritizedHostSummaries(m.hostInfo)
	allGroupNames := buildAllGroupNames(m.groupNames)
	groupCounts := toolCountsByGroup(m)
	groupDots := dotCountsByGroup(m)
	cols := groupAssignmentTableColumnWidths(hosts, allGroupNames, groupCounts, groupDots)

	assignmentSection := sectionedTabSection{
		title:            "Group Assignments",
		blankAfterHeader: false,
	}
	if len(hosts) == 0 {
		assignmentSection.empty = []string{
			p.styleHelp.Render("  No host assignments configured."),
			p.styleHelp.Render("  Onboarding creates this machine's host assignment."),
		}
	} else {
		for i, host := range hosts {
			name := host.Name
			groupBadge := compactHostAssignmentList(name, host.Groups)
			hostBadge := hostStatusLabel(host)
			nameLabel := name
			hostSelected := m.assignmentSection == 0 && i == m.hostCursor && !m.cursorHidden
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

				hostTools, hostDots := hostAssignmentCounts(m, name, host.Groups)
				localStats := fmt.Sprintf("current host: %s, %s",
					compactCount(hostTools, "tool"),
					compactCount(hostDots, "dotfile"),
				)
				details := []string{p.styleHelp.Render(detailPrefix + localStats)}

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

	groupsFocused := m.assignmentSection == 1
	groupSection := sectionedTabSection{
		title:            "Groups",
		blankAfterHeader: false,
	}

	for i, gn := range allGroupNames {
		count := groupCounts[gn]
		displayName := groupDisplayName(gn)
		label := displayName
		toolCount := compactCount(count, "tool")
		dotCount := compactCount(groupDots[gn], "dotfile")
		isSelected := groupsFocused && i == m.groupCursor && !m.cursorHidden

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
				} else if hosts := app.HostNamesForGroup(m.hostInfo, gn); len(hosts) > 0 {
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

func groupAssignmentTableColumnWidths(hosts []app.HostSummary, groupNames []string, groupCounts, groupDots map[string]int) groupAssignmentTableColumns {
	cols := groupAssignmentTableColumns{name: 20, mid: len("assigned groups"), tail: len("status")}
	for _, host := range hosts {
		cols.name = max(cols.name, lipgloss.Width(host.Name))
		cols.mid = max(cols.mid, lipgloss.Width(compactHostAssignmentList(host.Name, host.Groups)))
		cols.tail = max(cols.tail, lipgloss.Width(hostStatusLabel(host)))
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

func hostStatusLabel(host app.HostSummary) string {
	if host.Active {
		return "this host"
	}
	return ""
}

func compactCount(n int, label string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", label)
	}
	return fmt.Sprintf("%d %ss", n, label)
}

const groupPickerNewSentinel = "+ new group…"

func renderGroupPicker(m Model) string {
	p := m.palette
	t, ok := m.groupPickerActionTool()
	if !ok {
		return p.styleHelp.Render("no tool selected")
	}
	group := m.toolGroups[toolKey(t.Name, t.Provider)]
	var sb strings.Builder
	contentW := groupPickerContentWidth(m)

	// When pickerCreatingGroup is true the "+ new group…" sentinel is replaced by an inline text input of the same visual width so the popup never changes size.
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
		if slices.Contains(memberships, group) {
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
	hints := membershipPickerActionItems(m)
	if m.pickerCreatingGroup {
		hints = confirmActionItems(m.keys.Confirm, "create", m.keys.Back)
	}
	sb.WriteString(renderPickerHintItems(m, contentW, hints))
	return lipgloss.NewStyle().Width(contentW).Render(sb.String())
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

func renderHostGroupToolsPopup(m Model) (string, popupFrame) {
	layout := groupToolsPopupLayoutFor(m)
	return renderHostGroupToolsEditorWithLayout(m, layout), groupToolsPopupFrameWithLayout(m, layout)
}

func renderHostGroupToolsEditorWithLayout(m Model, layout groupToolsPopupLayout) string {
	p := m.palette
	contentW := layout.contentWidth
	rows := layout.rows
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
		nameW, providerW := popupToggleTableColumnWidths(contentW, layout.secondaryWidth)
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

func renderHostGroupDotsPopup(m Model) (string, popupFrame) {
	layout := groupDotsPopupLayoutFor(m)
	return renderHostGroupDotsEditorWithLayout(m, layout), groupDotsPopupFrameWithLayout(m, layout)
}

func renderHostGroupDotsEditorWithLayout(m Model, layout groupDotsPopupLayout) string {
	p := m.palette
	contentW := layout.contentWidth
	rows := layout.rows
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
		nameW, targetW := popupToggleTableColumnWidths(contentW, layout.secondaryWidth)
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
