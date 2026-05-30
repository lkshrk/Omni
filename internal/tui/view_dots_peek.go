package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/app"
)

func dotsPeekPopupFrame(m Model) popupFrame {
	paddingX := 2
	contentW := dotsPeekContentWidth(m)
	return popupFrame{
		Title:         dotsPeekPopupTitle(m),
		PaddingY:      1,
		PaddingX:      paddingX,
		Width:         popupFrameWidthForContent(contentW, paddingX),
		ContentHeight: dotsPeekContentHeight(m),
	}
}

func dotsPeekPopupTitle(m Model) string {
	if m.dotsPeek == nil {
		return "Peek"
	}
	return popupTitleForName("Peek", dotsPeekPopupTitleTarget(m.dotsPeek.result))
}

func dotsPeekPopupTitleTarget(result app.DotsPeekResult) string {
	if path := strings.TrimSpace(result.Local.Path); path != "" {
		return tildePath(path)
	}
	if path := strings.TrimSpace(result.Path); path != "" {
		return tildePath(path)
	}
	if path := strings.TrimSpace(result.Repo.Path); path != "" {
		return tildePath(path)
	}
	return strings.TrimSpace(result.Title)
}

func dotsPeekContentWidth(m Model) int {
	return popupContentWidth(m, 96, 48, 120)
}

func dotsPeekContentHeight(m Model) int {
	if m.height <= 0 {
		return 24
	}
	return clampPopupDimension(min(m.height-8, 26), 8, max(m.height-8, 1))
}

func dotsPeekBodyHeight(m Model) int {
	frame := fitPopupFrameToWindow(m, dotsPeekPopupFrame(m))
	target := popupContentTargetHeight(m, frame)
	footerH := lipgloss.Height(renderPickerHintItems(m, dotsPeekContentWidth(m), dotsPeekHintItems(m)))
	return max(target-footerH, 1)
}

func renderDotsPeekPopup(m Model) string {
	width := dotsPeekContentWidth(m)
	bodyHeight := dotsPeekBodyHeight(m)
	lines := dotsPeekLines(m, width)
	start := 0
	if m.dotsPeek != nil {
		start = clampRange(m.dotsPeek.scroll, 0, dotsPeekMaxScroll(m))
	}
	body := strings.Join(dotsPeekVisibleLines(m.palette, lines, start, bodyHeight), "\n")
	return renderPopupBodyWithFooterItems(m, width, bodyHeight, body, dotsPeekHintItems(m))
}

func dotsPeekHintItems(m Model) []hintItem {
	return []hintItem{
		hintFromBindingDesc(m.keys.Back, "close"),
	}
}

func dotsPeekMaxScroll(m Model) int {
	lines := dotsPeekLines(m, dotsPeekContentWidth(m))
	return max(len(lines)-dotsPeekBodyHeight(m), 0)
}

func dotsPeekVisibleLines(p palette, lines []string, start, height int) []string {
	if height <= 0 {
		return nil
	}
	if len(lines) == 0 {
		return []string{""}
	}
	start = clampRange(start, 0, max(len(lines)-height, 0))
	end := min(start+height, len(lines))
	out := append([]string(nil), lines[start:end]...)
	if len(out) == 0 {
		out = []string{""}
	}
	if start > 0 {
		out[0] = p.styleHelp.Render("...")
	}
	if end < len(lines) {
		out[len(out)-1] = p.styleHelp.Render("...")
	}
	return out
}

func dotsPeekLines(m Model, width int) []string {
	if m.dotsPeekLoading && m.dotsPeek == nil {
		return []string{m.palette.styleHelp.Render("Opening dotfile...")}
	}
	if m.dotsPeek == nil {
		return []string{m.palette.styleHelp.Render("No dotfile selected.")}
	}
	result := m.dotsPeek.result
	lines := []string{
		dotsPeekSourceLine(m, "repo source", result.Repo),
		dotsPeekSourceLine(m, "local source", result.Local),
	}
	if strings.TrimSpace(result.Notice) != "" {
		lines = append(lines, m.palette.styleHelp.Render(result.Notice))
	}
	lines = append(lines, "")

	switch result.Mode {
	case app.DotsPeekModeDiff:
		lines = append(lines, dotsPeekWrappedContent(result.Content, width)...)
	case app.DotsPeekModeText:
		if result.Source != "" && result.Path != "" {
			lines = append(lines, m.palette.styleHelp.Render(fmt.Sprintf("showing %s source: %s", result.Source, result.Path)))
		}
		lines = append(lines, dotsPeekWrappedContent(result.Content, width)...)
	default:
		lines = append(lines, dotsPeekMetadataLines(m, result)...)
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func dotsPeekSourceLine(m Model, label string, side app.DotsPeekSide) string {
	path := side.Path
	if path == "" {
		path = "-"
	}
	parts := []string{label + ": " + path}
	switch {
	case !side.Exists:
		parts = append(parts, "missing")
	case side.Binary:
		parts = append(parts, "binary")
	case side.Truncated:
		parts = append(parts, "large")
	default:
		parts = append(parts, fmt.Sprintf("%d B", side.Size))
	}
	if side.SymlinkTarget != "" {
		parts = append(parts, "link -> "+side.SymlinkTarget)
	}
	if side.Notice != "" && side.Notice != "truncated to 256 KiB" {
		parts = append(parts, side.Notice)
	}
	return m.palette.styleHelp.Render(strings.Join(parts, "  "))
}

func dotsPeekMetadataLines(m Model, result app.DotsPeekResult) []string {
	lines := []string{}
	if result.Notice != "" {
		lines = append(lines, m.palette.styleHelp.Render(result.Notice))
	}
	lines = append(lines,
		dotsPeekSideMetadataLine(m, result.Repo),
		dotsPeekSideMetadataLine(m, result.Local),
	)
	return lines
}

func dotsPeekSideMetadataLine(m Model, side app.DotsPeekSide) string {
	status := "missing"
	switch {
	case side.Binary:
		status = "binary"
	case side.Truncated:
		status = "too large"
	case side.Exists:
		status = fmt.Sprintf("%d B", side.Size)
	}
	if side.Notice != "" {
		status += " (" + side.Notice + ")"
	}
	return m.palette.styleHelp.Render(fmt.Sprintf("%s: %s", side.Label, status))
}

func dotsPeekWrappedContent(content string, width int) []string {
	if content == "" {
		return []string{""}
	}
	rawLines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		lines = append(lines, hardWrapLine(line, max(width, 1))...)
	}
	return lines
}

func hardWrapLine(line string, width int) []string {
	if width <= 0 {
		return []string{""}
	}
	runes := []rune(line)
	if len(runes) == 0 {
		return []string{""}
	}
	if len(runes) <= width {
		return []string{line}
	}
	out := make([]string, 0, len(runes)/width+1)
	for len(runes) > width {
		out = append(out, string(runes[:width]))
		runes = runes[width:]
	}
	if len(runes) > 0 {
		out = append(out, string(runes))
	}
	return out
}
