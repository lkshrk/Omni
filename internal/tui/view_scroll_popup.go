package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// scrollPopupContentWidth returns the standard content width shared by all
// scrollable overlays (96 preferred, 48 min, 120 max).
func scrollPopupContentWidth(m Model) int {
	return popupContentWidth(m, 96, 48, 120)
}

// scrollPopupContentHeight returns the standard content height for a
// scrollable overlay capped to the terminal height.
func scrollPopupContentHeight(m Model) int {
	if m.height <= 0 {
		return 24
	}
	return clampPopupDimension(min(m.height-8, 26), 8, max(m.height-8, 1))
}

// scrollPopupFrame builds a popupFrame for a scrollable overlay using the
// shared width/height helpers and the given title.
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

// scrollPopupBodyHeight returns the number of content lines that fit in the
// overlay body after subtracting the hint footer.
func scrollPopupBodyHeight(m Model, title string, hints []hintItem) int {
	frame := fitPopupFrameToWindow(m, scrollPopupFrame(m, title))
	target := popupContentTargetHeight(m, frame)
	w := scrollPopupContentWidth(m)
	footerH := lipgloss.Height(renderPickerHintItems(m, w, hints))
	return max(target-footerH, 1)
}

// scrollPopupMaxScroll returns the maximum scroll offset for the given lines
// and body height.
func scrollPopupMaxScroll(lines []string, bodyH int) int {
	return max(len(lines)-bodyH, 0)
}

// scrollPopupVisibleLines returns the slice of lines visible in the overlay
// body window, replacing the first/last line with "..." when content is
// clipped above/below.
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

// renderScrollPopup renders a complete scrollable overlay: it applies the
// visible-line window, joins the body, appends the hint footer, and returns
// the rendered string ready for placeOverlay.
func renderScrollPopup(m Model, title string, lines []string, scroll int, hints []hintItem) string {
	width := scrollPopupContentWidth(m)
	bodyH := scrollPopupBodyHeight(m, title, hints)
	maxScroll := scrollPopupMaxScroll(lines, bodyH)
	start := clampRange(scroll, 0, maxScroll)
	body := strings.Join(scrollPopupVisibleLines(m.palette, lines, start, bodyH), "\n")
	return renderPopupBodyWithFooterItems(m, width, bodyH, body, hints)
}
