package tui

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

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

type groupToolsPopupLayout struct {
	contentWidth   int
	contentHeight  int
	rows           []groupToolRow
	secondaryWidth int
}

type groupDotsPopupLayout struct {
	contentWidth   int
	contentHeight  int
	rows           []groupDotRow
	secondaryWidth int
}

func groupToolsPopupLayoutFor(m Model) groupToolsPopupLayout {
	base := unfilteredHostGroupToolsModel(m)
	baseRows := groupToolRows(base)
	rows := baseRows
	if groupToolsNeedsFilteredRows(m) {
		rows = groupToolRows(m)
	}

	longestName, _ := groupToolsColumnWidths(m, baseRows)
	longestSecondary := groupToolsSecondaryWidth(m, baseRows)
	width := popupToggleTableWidth(longestName, longestSecondary)
	for _, label := range []string{"enabled", "disabled", "ignored"} {
		width = max(width, lipgloss.Width(pickerSectionLabel(label))+2)
	}
	width = max(width, lipgloss.Width(renderContextHints(m, hintCtxHostGroupTools, "")))
	if m.groupToolsEditor.searchActive {
		width = max(width, lipgloss.Width(renderEmptyAwareTextInputView(m.palette, m.settingsInput, m.settingsInput.Placeholder, 0)))
	}
	if filterBar := renderHostGroupToolsFilterBar(base); filterBar != "" {
		width = max(width, lipgloss.Width(filterBar))
	}
	width = max(width, lipgloss.Width("Edit Tools: "+m.groupToolsEditor.group))
	contentWidth := popupContentWidth(m, width, 40, 72)
	contentHeight := groupToolsEditorContentHeight(base, baseRows, contentWidth)
	if groupToolsNeedsFilteredRows(m) || m.groupToolsEditor.searchActive {
		contentHeight = max(contentHeight, groupToolsEditorContentHeight(m, rows, contentWidth))
	}
	return groupToolsPopupLayout{
		contentWidth:   contentWidth,
		contentHeight:  contentHeight,
		rows:           rows,
		secondaryWidth: longestSecondary,
	}
}

func groupToolsNeedsFilteredRows(m Model) bool {
	return m.groupToolsProviderIdx != 0 || strings.TrimSpace(m.groupToolsEditor.search) != ""
}

func groupToolsContentWidth(m Model) int {
	return groupToolsPopupLayoutFor(m).contentWidth
}

func groupDotsPopupLayoutFor(m Model) groupDotsPopupLayout {
	base := unfilteredHostGroupDotsModel(m)
	baseRows := groupDotRows(base)
	rows := baseRows
	if groupDotsNeedsFilteredRows(m) {
		rows = groupDotRows(m)
	}

	longestName, longestTarget := groupDotsColumnWidths(baseRows)
	width := popupToggleTableWidth(longestName, longestTarget)
	for _, label := range []string{"enabled", "disabled", "ignored"} {
		width = max(width, lipgloss.Width(pickerSectionLabel(label))+2)
	}
	width = max(width, lipgloss.Width(renderContextHints(m, hintCtxHostGroupDots, "")))
	if m.groupDotsEditor.searchActive {
		width = max(width, lipgloss.Width(renderEmptyAwareTextInputView(m.palette, m.settingsInput, m.settingsInput.Placeholder, 0)))
	}
	width = max(width, lipgloss.Width("Edit Dots: "+m.groupDotsEditor.group))
	contentWidth := popupContentWidth(m, width, 40, 72)
	contentHeight := groupDotsEditorContentHeight(base, baseRows, contentWidth)
	if groupDotsNeedsFilteredRows(m) || m.groupDotsEditor.searchActive {
		contentHeight = max(contentHeight, groupDotsEditorContentHeight(m, rows, contentWidth))
	}
	return groupDotsPopupLayout{
		contentWidth:   contentWidth,
		contentHeight:  contentHeight,
		rows:           rows,
		secondaryWidth: longestTarget,
	}
}

func groupDotsNeedsFilteredRows(m Model) bool {
	return strings.TrimSpace(m.groupDotsEditor.search) != ""
}

func groupDotsContentWidth(m Model) int {
	return groupDotsPopupLayoutFor(m).contentWidth
}

func groupToolsEditorContentHeight(m Model, rows []groupToolRow, contentW int) int {
	height := 0
	if m.groupToolsEditor.searchActive {
		height += 2
	}
	if filterBar := renderHostGroupToolsFilterBar(m); filterBar != "" {
		if m.groupToolsEditor.searchActive {
			height++
		}
		height += lipgloss.Height(lipgloss.NewStyle().Width(contentW).Render(filterBar)) + 1
	} else if m.groupToolsEditor.searchActive {
		height++
	}
	height += sectionedPopupRowsHeight(len(rows), groupToolSectionCount(rows))

	ctx := hintCtxHostGroupTools
	if m.groupToolsEditor.searchActive {
		ctx = hintCtxHostGroupToolsSearch
	}
	height += lipgloss.Height(renderPickerHintItems(m, contentW, contextHintItems(m, ctx)))
	return height
}

func groupDotsEditorContentHeight(m Model, rows []groupDotRow, contentW int) int {
	height := 0
	if m.groupDotsEditor.searchActive {
		height += 3
	}
	height += sectionedPopupRowsHeight(len(rows), groupDotSectionCount(rows))

	ctx := hintCtxHostGroupDots
	if m.groupDotsEditor.searchActive {
		ctx = hintCtxHostGroupDotsSearch
	}
	height += lipgloss.Height(renderPickerHintItems(m, contentW, contextHintItems(m, ctx)))
	return height
}

func sectionedPopupRowsHeight(rowCount int, sectionCount int) int {
	if rowCount == 0 {
		return 2
	}
	return rowCount + sectionCount*2
}

func groupToolSectionCount(rows []groupToolRow) int {
	count := 0
	lastSection := groupToolSection(-1)
	for _, row := range rows {
		if row.section != lastSection {
			count++
			lastSection = row.section
		}
	}
	return count
}

func groupDotSectionCount(rows []groupDotRow) int {
	count := 0
	lastSection := groupDotSection(-1)
	for _, row := range rows {
		if row.section != lastSection {
			count++
			lastSection = row.section
		}
	}
	return count
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
		if entry.Name == "" {
			continue
		}
		current, exists := statusByName[entry.Name]
		currentIgnored := exists && dotStatusState(current) == app.DotStateIgnored
		entryIgnored := dotStatusState(entry) == app.DotStateIgnored
		if !exists || currentIgnored && !entryIgnored {
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
		ignored := dotStatusState(status) == app.DotStateIgnored
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
