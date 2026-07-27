package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

func scrollPopupContentWidth(m Model) int {
	return popupContentWidth(m, 96, 48, 120)
}

func scrollPopupContentHeight(m Model) int {
	if m.height <= 0 {
		return 24
	}
	return clampPopupDimension(min(m.height-8, 26), 8, max(m.height-8, 1))
}

func scrollPopupFrame(m Model, title string) popupFrame {
	paddingX := 2
	contentW := scrollPopupContentWidth(m)
	return popupFrame{
		Title:         title,
		PaddingY:      1,
		PaddingX:      paddingX,
		Width:         popupFrameWidthForContent(contentW, paddingX),
		ContentHeight: scrollPopupContentHeight(m),
	}
}

func scrollPopupBodyHeight(m Model, title string, hints []hintItem) int {
	frame := fitPopupFrameToWindow(m, scrollPopupFrame(m, title))
	target := popupContentTargetHeight(m, frame)
	w := scrollPopupContentWidth(m)
	footerH := lipgloss.Height(renderPickerHintItems(m, w, hints))
	return max(target-footerH, 1)
}

func scrollPopupMaxScroll(lines []string, bodyH int) int {
	return max(len(lines)-bodyH, 0)
}

func scrollPopupVisibleLines(p palette, lines []string, start, height int) []string {
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

// renderScrollPopup clamps scroll to [0, maxScroll] so callers may pass an unclamped offset.
func renderScrollPopup(m Model, title string, lines []string, scroll int, hints []hintItem) string {
	width := scrollPopupContentWidth(m)
	bodyH := scrollPopupBodyHeight(m, title, hints)
	maxScroll := scrollPopupMaxScroll(lines, bodyH)
	start := clampRange(scroll, 0, maxScroll)
	body := strings.Join(scrollPopupVisibleLines(m.palette, lines, start, bodyH), "\n")
	return renderPopupBodyWithFooterItems(m, width, bodyH, body, hints)
}
