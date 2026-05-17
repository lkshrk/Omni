package tui

import (
	"image/color"
	"strings"

	"charm.land/bubbles/v2/help"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	iconInstalled  = "✓"
	iconMissing    = "✗"
	iconOutdated   = "↑"
	iconIgnored    = "–"
	iconOrphan     = "+"
	iconWrongProv  = "⚠"
	iconFailed     = "!"
	iconPending    = "◷"
	iconPrivileged = "⚿"
)

// newHelp creates a help.Model styled to match the omni palette.
func newHelp() help.Model {
	p := defaultPalette()
	h := help.New()
	h.Styles.ShortKey = lipgloss.NewStyle().Foreground(p.colTitle)
	h.Styles.ShortDesc = lipgloss.NewStyle().Foreground(p.colHelp)
	h.Styles.ShortSeparator = lipgloss.NewStyle().Foreground(p.colMuted)
	h.Styles.Ellipsis = lipgloss.NewStyle().Foreground(p.colMuted)
	h.Styles.FullKey = lipgloss.NewStyle().Foreground(p.colTitle)
	h.Styles.FullDesc = lipgloss.NewStyle().Foreground(p.colHelp)
	h.Styles.FullSeparator = lipgloss.NewStyle().Foreground(p.colMuted)
	return h
}

// View renders the full TUI and satisfies the tea.Model interface.
func (m Model) View() tea.View {
	v := tea.NewView(m.viewString())
	v.AltScreen = true
	v.WindowTitle = m.windowTitle()
	v.ReportFocus = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// windowTitle returns a tab-specific terminal title.
func (m Model) windowTitle() string {
	switch m.mode {
	case viewDots:
		return "omni — dots"
	case viewGroups:
		return "omni — groups"
	case viewSettings:
		return "omni — settings"
	case viewStatus:
		return "omni — status"
	case viewSetup:
		return "omni — setup"
	case viewAdminTerminal:
		return "omni — admin"
	default:
		return "omni"
	}
}

// viewString builds the full terminal output as a plain string.
// Used internally so overlay helpers (placeOverlay) can operate on strings.
func (m Model) viewString() string {
	p := m.palette

	// File picker popup — overlays whatever view is currently active (same pattern as help overlay).
	if m.showFilePicker {
		bgModel := m
		bgModel.showFilePicker = false
		bg := bgModel.viewString()
		return placePopup(bg, m, renderFilePickerPopup(m), filePickerPopupFrame(m))
	}

	if m.err != nil {
		return p.styleErr.Render("Error: "+m.err.Error()) + "\n" + p.styleHelp.Render("Press q to quit.")
	}

	if m.stowInstallPrompt {
		bgModel := m
		bgModel.stowInstallPrompt = false
		bg := bgModel.viewString()
		return placePopup(bg, m, renderStowInstallPopup(m), popupFrame{Title: "Install Stow", PaddingY: 1, PaddingX: 2, Width: 48})
	}

	if m.setupReloading {
		bgModel := m
		bgModel.setupReloading = false
		bgModel.loading = true
		bgModel.visibleTools = nil
		if bgModel.mode == viewSetup {
			bgModel.mode = bgModel.setupBackgroundMode
			if bgModel.mode == viewSetup {
				bgModel.mode = viewStatus
			}
		}
		return placeOverlay(bgModel.viewString(), renderPostSetupLoading(m), m.width, m.height)
	}

	// Setup/onboarding is modal over the regular tools UI. Keep mode=viewSetup
	// for key routing, but render a normal background so async scans/status stay visible.
	if m.mode == viewSetup {
		bgModel := m
		bgModel.mode = m.setupBackgroundMode
		if bgModel.mode == viewSetup {
			bgModel.mode = viewStatus
		}
		bg := bgModel.viewString()
		frame := setupPopupFrame(m)
		contentWidth := popupInnerContentWidth(fitPopupFrameToWindow(m, frame))
		return placePopup(bg, m, renderSetupPopup(m, contentWidth), frame)
	}

	// Group picker — overlays the tool list so context stays visible.
	if m.mode == viewGroupPicker {
		bgModel := m
		bgModel.mode = viewList
		bg := bgModel.viewString()
		title := "Choose Group"
		if m.pickerPurposeClaim {
			title = "Add To Config"
		} else if m.pickerPurposeInstall {
			title = "Install And Add"
		}
		paddingX := 2
		return placePopup(bg, m, renderGroupPicker(m), popupFrame{Title: popupTitleForGroupPickerTool(m, title), PaddingY: 1, PaddingX: paddingX, Width: popupFrameWidthForContent(groupPickerContentWidth(m), paddingX), NoTitleDivider: true})
	}

	if m.mode == viewGroupMembership {
		bgModel := m
		bgModel.mode = viewList
		if m.pickerMembershipKind == pickerMembershipDot {
			bgModel.mode = viewDots
		}
		bg := bgModel.viewString()
		paddingX := 2
		return placePopup(bg, m, renderGroupMembershipPicker(m), popupFrame{Title: groupMembershipPopupTitle(m), PaddingY: 1, PaddingX: paddingX, Width: popupFrameWidthForContent(groupMembershipContentWidth(m), paddingX), NoTitleDivider: true})
	}

	if m.mode == viewGroupTools {
		bgModel := m
		bgModel.mode = viewGroups
		bg := bgModel.viewString()
		return placePopup(bg, m, renderHostGroupToolsEditor(m), groupToolsPopupFrame(m))
	}

	if m.mode == viewGroupDots {
		bgModel := m
		bgModel.mode = viewGroups
		bg := bgModel.viewString()
		return placePopup(bg, m, renderHostGroupDotsEditor(m), groupDotsPopupFrame(m))
	}

	if m.mode == viewIgnoreScope {
		bgModel := m
		bgModel.mode = viewList
		bg := bgModel.viewString()
		return placePopup(bg, m, renderScopePicker(m), scopePickerPopupFrame(m, popupTitleForScopeTool(m, "Ignore")))
	}

	if m.mode == viewProviderScope {
		bgModel := m
		bgModel.mode = viewList
		bg := bgModel.viewString()
		return placePopup(bg, m, renderScopePicker(m), scopePickerPopupFrame(m, popupTitleForScopeTool(m, "Pin Provider")))
	}

	if m.mode == viewAdminTerminal {
		bgModel := m
		bgModel.adminTerminal = nil
		bgModel.mode = viewList
		if m.adminTerminal != nil {
			switch m.adminTerminal.returnMode {
			case viewList, viewSearch, viewDots:
				bgModel.mode = m.adminTerminal.returnMode
			}
		}
		bg := bgModel.viewString()
		return placePopup(bg, m, renderAdminTerminalPopup(m), adminTerminalPopupFrame(m))
	}

	if m.dashboardReconcilePlanOpen {
		bgModel := m
		bgModel.dashboardReconcilePlanOpen = false
		bg := bgModel.viewString()
		return placePopup(bg, m, renderDashboardReconcilePlanPopup(m), dashboardReconcilePlanPopupFrame(m))
	}

	if m.mode == viewGroups && m.groupDeleteConfirm {
		bgModel := m
		bgModel.groupDeleteConfirm = false
		bgModel.suppressFooterHints = true
		bg := bgModel.viewString()
		paddingX := 1
		return placePopup(bg, m, renderGroupDeletePopup(m), popupFrame{Title: "Delete Group", PaddingX: paddingX, Width: popupFrameWidthForContent(groupDeletePopupContentWidth, paddingX)})
	}

	if m.mode == viewGroups && m.groupCreating {
		bgModel := m
		bgModel.groupCreating = false
		bg := bgModel.viewString()
		paddingX := 2
		return placePopup(bg, m, renderGroupCreatePopup(m), popupFrame{Title: "New Group", PaddingY: 1, PaddingX: paddingX, Width: popupFrameWidthForContent(groupCreatePopupWidth(m), paddingX), NoTitleDivider: true})
	}

	if m.mode == viewGroups && m.hostEditMode == 1 {
		bgModel := m
		bgModel.hostEditMode = 0
		bgModel.pickerCreatingGroup = false
		bg := bgModel.viewString()
		return placePopup(bg, m, renderHostGroupEditor(m), groupEditorPopupFrame(m))
	}

	// Help — centered popup overlay, drawn over the current tab.
	if m.help.ShowAll {
		// Render background without the overlay so the main screen stays visible.
		bgModel := m
		bgModel.help.ShowAll = false
		bg := bgModel.viewString()
		const helpPopupPaddingW = 3
		helpContentWidth := helpPopupContentWidth(m)
		helpFrameWidth := helpContentWidth + helpPopupPaddingW*2 + 2
		helpFrame := popupFrame{
			Title:    helpPopupTitle(m),
			PaddingY: 1,
			PaddingX: helpPopupPaddingW,
			Width:    helpFrameWidth,
		}
		helpFrame = fitPopupFrameToWindow(m, helpFrame)
		helpContentWidth = max(popupInnerContentWidth(helpFrame), 1)
		return placePopup(bg, m, renderHelpPopupWithWidth(m, helpContentWidth), popupFrame{
			Title:    helpFrame.Title,
			PaddingY: helpFrame.PaddingY,
			PaddingX: helpFrame.PaddingX,
			Width:    helpFrame.Width,
		})
	}

	var sb strings.Builder

	// Header
	sb.WriteString(renderHeader(m))
	sb.WriteByte('\n')
	sb.WriteString(renderHRule(p, m.width))
	sb.WriteByte('\n')

	// Filter input (when active)
	if m.mode == viewSearch {
		sb.WriteString(screenEdgeInset() + renderEmptyAwareTextInputView(p, m.filter, m.filter.Placeholder, 0))
		sb.WriteByte('\n')
		sb.WriteString(renderHRule(p, m.width))
		sb.WriteByte('\n')
	}

	// Command palette input (when active)
	if m.mode == viewCommand {
		sb.WriteString(p.styleHelp.Render(screenEdgeInset()+": ") + renderEmptyAwareTextInputView(p, m.commandInput, m.commandInput.Placeholder, 0))
		sb.WriteByte('\n')
		sb.WriteString(renderHRule(p, m.width))
		sb.WriteByte('\n')
	}

	// Main body — collected separately so it can be padded to fill available
	// height, keeping the footer pinned to the bottom of the terminal.
	var body string
	switch {
	case m.mode == viewSettings:
		body = renderSettings(m)
	case m.mode == viewGroups:
		body = renderGroups(m)
	case m.mode == viewDots:
		body = renderDots(m)
	case m.mode == viewStatus:
		if len(m.allTools) == 0 || m.launchBatchActive {
			body = ""
		} else {
			body = renderStatus(m)
		}
	case m.mode == viewCommand:
		body = renderPalette(m)
	default:
		body = renderList(m)
	}

	avail := listAvailableHeight(m)
	if n := strings.Count(body, "\n"); n < avail {
		body += strings.Repeat("\n", avail-n)
	}
	sb.WriteString(body)

	sb.WriteString(renderHRule(p, m.width))
	sb.WriteByte('\n')
	sb.WriteString(renderStatusBar(m))

	return sb.String()
}

// placeOverlay renders fg centered on top of bg using the lipgloss compositor.
// The compositor uses a proper cell buffer with z-ordering, so ANSI sequences
// that span the overlay boundary are handled correctly.
func placeOverlay(bg, fg string, bgW, bgH int) string {
	fgLines := strings.Split(fg, "\n")
	fgH := len(fgLines)
	fgW := 0
	for _, l := range fgLines {
		if w := lipgloss.Width(l); w > fgW {
			fgW = w
		}
	}

	startX := (bgW - fgW) / 2
	startY := (bgH - fgH) / 2
	if startX < 0 {
		startX = 0
	}
	if startY < 0 {
		startY = 0
	}

	bgLayer := lipgloss.NewLayer(bg)
	fgLayer := lipgloss.NewLayer(fg).X(startX).Y(startY).Z(1)
	return lipgloss.NewCompositor(bgLayer, fgLayer).Render()
}

type popupFrame struct {
	Title          string
	BorderColor    color.Color
	PaddingY       int
	PaddingX       int
	Width          int
	ContentHeight  int
	NoTitleDivider bool
}

func clampPopupDimension(preferred, minWidth, maxWidth int) int {
	maxWidth = max(maxWidth, 1)
	if maxWidth < minWidth {
		return maxWidth
	}
	return min(max(preferred, minWidth), maxWidth)
}

func popupContentMaxWidth(m Model) int {
	if m.width <= 0 {
		return 80
	}
	return max(m.width-12, 1)
}

func popupContentWidth(m Model, preferred, minWidth, maxPreferred int) int {
	if maxPreferred > 0 {
		preferred = min(preferred, maxPreferred)
	}
	return clampPopupDimension(preferred, minWidth, popupContentMaxWidth(m))
}

func popupFrameMaxWidth(m Model) int {
	if m.width <= 0 {
		return 90
	}
	return max(m.width-2, 1)
}

func fitPopupFrameToWindow(m Model, frame popupFrame) popupFrame {
	maxWidth := popupFrameMaxWidth(m)
	if frame.Width <= 0 {
		frame.Width = clampPopupDimension(64, 36, maxWidth)
	} else {
		frame.Width = min(frame.Width, maxWidth)
	}
	return frame
}

func popupInnerContentWidth(frame popupFrame) int {
	innerWidth := frame.Width - 2 // frame borders
	innerWidth -= frame.PaddingX * 2
	return max(innerWidth, 0)
}

func popupFrameWidthForContent(contentWidth, paddingX int) int {
	return max(contentWidth, 1) + paddingX*2 + 2
}

func popupDividerWithStyle(style lipgloss.Style, width int) string {
	return style.Render(strings.Repeat("─", max(width, 1)))
}

func popupDivider(p palette, width int) string {
	return popupDividerWithStyle(p.styleSep, width)
}

func fitPopupLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	fitted := lipgloss.NewStyle().
		Inline(true).
		MaxWidth(width).
		Render(line)
	return lipgloss.NewStyle().
		Inline(true).
		Width(width).
		Render(fitted)
}

func normalizePopupContent(content string, width int) string {
	if width <= 0 {
		return content
	}
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = fitPopupLine(line, width)
	}
	return strings.Join(lines, "\n")
}

func renderPopupFrame(p palette, content string, frame popupFrame) string {
	borderColor := frame.BorderColor
	if borderColor == nil {
		borderColor = p.colMuted
	}
	width := frame.Width
	innerWidth := popupInnerContentWidth(frame)
	if width <= 0 {
		width = lipgloss.Width(content)
		innerWidth = max(width-2-frame.PaddingX*2, 1)
	}
	innerWidth = max(innerWidth, 1)
	if frame.Title != "" {
		titleText := fitCellText(frame.Title, innerWidth)
		title := lipgloss.NewStyle().
			Width(innerWidth).
			Align(lipgloss.Center).
			Render(p.styleTitle.PaddingLeft(0).Render(titleText))
		if frame.NoTitleDivider {
			content = title + "\n\n" + content
		} else {
			content = title + "\n\n" + popupDivider(p, innerWidth) + "\n\n" + content
		}
	}
	content = normalizePopupContent(content, innerWidth)
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(frame.PaddingY, frame.PaddingX)
	if frame.Width > 0 {
		style = style.Width(frame.Width)
	}
	return style.Render(content)
}

func fitPopupContent(p palette, content string, maxLines int) string {
	if maxLines <= 0 || lipgloss.Height(content) <= maxLines {
		return content
	}
	lines := strings.Split(content, "\n")
	footerStart := popupFooterStart(lines)
	if footerStart < 0 {
		return strings.Join(scrollPopupLines(p, lines, maxLines), "\n")
	}

	body := lines[:footerStart]
	footer := lines[footerStart:]
	bodyLines := maxLines - len(footer)
	if bodyLines < 1 {
		return strings.Join(scrollPopupLines(p, lines, maxLines), "\n")
	}
	body = scrollPopupLines(p, body, bodyLines)
	out := append(append([]string(nil), body...), footer...)
	return strings.Join(out, "\n")
}

func popupFooterStart(lines []string) int {
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if strings.Contains(line, "─") && lipgloss.Width(line) >= 8 {
			return i
		}
	}
	return -1
}

func scrollPopupLines(p palette, lines []string, maxLines int) []string {
	if maxLines <= 0 || len(lines) <= maxLines {
		return lines
	}
	cursor := 0
	for i, line := range lines {
		if strings.Contains(line, "›") {
			cursor = i
			break
		}
	}
	start := cursor - maxLines/2
	if start < 0 {
		start = 0
	}
	if start+maxLines > len(lines) {
		start = len(lines) - maxLines
	}
	out := append([]string(nil), lines[start:start+maxLines]...)
	if maxLines >= 3 {
		if start > 0 {
			out[0] = p.styleHelp.Render("  …")
		}
		if start+maxLines < len(lines) {
			out[len(out)-1] = p.styleHelp.Render("  …")
		}
	}
	return out
}

func popupContentMaxHeight(m Model, frame popupFrame) int {
	if m.height <= 0 {
		return 0
	}
	used := 2 + frame.PaddingY*2 // border + vertical padding
	if frame.Title != "" {
		if frame.NoTitleDivider {
			used += 2 // title + blank line
		} else {
			used += 4 // title + blank line + divider + blank line
		}
	}
	return max(m.height-used, 1)
}

func popupContentTargetHeight(m Model, frame popupFrame) int {
	maxHeight := popupContentMaxHeight(m, frame)
	if frame.ContentHeight <= 0 {
		return maxHeight
	}
	if maxHeight <= 0 {
		return frame.ContentHeight
	}
	return min(frame.ContentHeight, maxHeight)
}

func padPopupContentToHeight(content string, height int) string {
	if height <= 0 {
		return content
	}
	current := lipgloss.Height(content)
	if current >= height {
		return content
	}
	lines := strings.Split(content, "\n")
	pad := make([]string, height-current)
	footerStart := popupFooterStart(lines)
	if footerStart < 0 {
		lines = append(lines, pad...)
	} else {
		lines = append(append([]string(nil), lines[:footerStart]...), append(pad, lines[footerStart:]...)...)
	}
	return strings.Join(lines, "\n")
}

func popupTitleForSelectedTool(m Model, action string) string {
	t := m.selectedTool()
	if t == nil || t.Name == "" {
		return action
	}
	return popupTitleForName(action, t.Name)
}

func popupTitleForGroupPickerTool(m Model, action string) string {
	if m.pickerActionToolSet {
		return popupTitleForName(action, m.pickerActionTool.Name)
	}
	return popupTitleForSelectedTool(m, action)
}

func popupTitleForMembershipTool(m Model, action string) string {
	if m.pickerMembershipName != "" {
		return popupTitleForName(action, m.pickerMembershipName)
	}
	return popupTitleForSelectedTool(m, action)
}

func popupTitleForScopeTool(m Model, action string) string {
	if m.scopeTargetSet {
		return popupTitleForName(action, m.scopeTarget.Name)
	}
	return popupTitleForSelectedTool(m, action)
}

func popupTitleForName(action, name string) string {
	if name == "" {
		return action
	}
	return action + ": " + name
}

func popupTitleForSelectedDot(m Model, action string) string {
	if m.pickerMembershipName == "" {
		return action
	}
	return popupTitleForName(action, m.pickerMembershipName)
}

func groupMembershipPopupTitle(m Model) string {
	if m.pickerMembershipKind == pickerMembershipDot {
		return popupTitleForSelectedDot(m, "Move Group")
	}
	return popupTitleForMembershipTool(m, "Move Group")
}

func groupEditorPopupFrame(m Model) popupFrame {
	paddingX := 2
	return popupFrame{
		Title:          "Edit Groups: " + m.hostEditName,
		PaddingY:       1,
		PaddingX:       paddingX,
		Width:          popupFrameWidthForContent(groupEditorContentWidth(m), paddingX),
		NoTitleDivider: true,
	}
}

func groupToolsPopupFrame(m Model) popupFrame {
	paddingX := 2
	return popupFrame{
		Title:          "Edit Tools: " + m.groupToolsEditor.group,
		PaddingY:       1,
		PaddingX:       paddingX,
		Width:          popupFrameWidthForContent(groupToolsContentWidth(m), paddingX),
		ContentHeight:  groupToolsPopupContentHeight(m),
		NoTitleDivider: true,
	}
}

func groupDotsPopupFrame(m Model) popupFrame {
	paddingX := 2
	return popupFrame{
		Title:          "Edit Dots: " + m.groupDotsEditor.group,
		PaddingY:       1,
		PaddingX:       paddingX,
		Width:          popupFrameWidthForContent(groupDotsContentWidth(m), paddingX),
		ContentHeight:  groupDotsPopupContentHeight(m),
		NoTitleDivider: true,
	}
}

func placePopup(bg string, m Model, content string, frame popupFrame) string {
	frame = fitPopupFrameToWindow(m, frame)
	targetHeight := popupContentTargetHeight(m, frame)
	content = fitPopupContent(m.palette, content, targetHeight)
	if frame.ContentHeight > 0 {
		content = padPopupContentToHeight(content, targetHeight)
	}
	return placeOverlay(bg, renderPopupFrame(m.palette, content, frame), m.width, m.height)
}

func renderPopupBodyWithFooterItems(m Model, width, bodyHeight int, body string, hints []hintItem) string {
	body = strings.TrimRight(body, "\n")
	if bodyHeight > 0 {
		body = fitPopupContent(m.palette, body, bodyHeight)
		body = padPopupContentToHeight(body, bodyHeight)
	}
	if len(hints) > 0 {
		body += "\n" + renderPickerHintItems(m, width, hints)
	}
	return lipgloss.NewStyle().Width(width).Render(body)
}

// listAvailableHeight returns the number of terminal lines available for the
// scrollable tool list. It subtracts the fixed header, separators, and status
// bar from the total terminal height.
func listAvailableHeight(m Model) int {
	h := m.height
	h -= 2 // title row + top separator
	if m.mode == viewSearch || m.mode == viewCommand {
		h -= 2 // input row + second separator
	}
	h -= 2 // bottom separator + status bar
	if h < 1 {
		return 1
	}
	return h
}

// renderFilePickerPopup returns the file picker popup content. The shared popup
// frame is applied by View so the file picker matches other modal surfaces.
func renderFilePickerPopup(m Model) string {
	p := m.palette
	contentW := filePickerContentWidth(m)
	fp := m.dotsFilePicker

	var sb strings.Builder
	sb.WriteString(strings.TrimRight(fp.View(p), "\n"))

	return renderPopupBodyWithFooterItems(m, contentW, filePickerBrowseBodyHeight(m), sb.String(), contextHintItems(m, hintCtxFilePickerBrowse))
}

func filePickerPopupFrame(m Model) popupFrame {
	frame := popupFrame{
		Title:          m.filePickerTitle,
		PaddingY:       1,
		PaddingX:       2,
		Width:          popupFrameWidthForContent(filePickerContentWidth(m), 2),
		ContentHeight:  filePickerPopupContentHeight(m),
		NoTitleDivider: true,
	}
	return frame
}

func filePickerPopupContentHeight(m Model) int {
	return filePickerBrowseBodyHeight(m) + popupFooterHeight
}

func filePickerBrowseBodyHeight(m Model) int {
	return 2 + filePickerListHeight(m)
}

const popupFooterHeight = 3

func filePickerListHeight(m Model) int {
	if m.height <= 0 {
		return 14
	}
	return clampPopupDimension(16, 6, max(m.height-10, 1))
}

func filePickerContentWidth(m Model) int {
	return popupContentWidth(m, 72, 36, 72)
}
