package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/database"
)

func traceLogPopupFrame(m Model) popupFrame {
	paddingX := 2
	contentW := traceLogContentWidth(m)
	return popupFrame{
		Title:         "Command Log",
		PaddingY:      1,
		PaddingX:      paddingX,
		Width:         popupFrameWidthForContent(contentW, paddingX),
		ContentHeight: traceLogContentHeight(m),
	}
}

func traceLogContentWidth(m Model) int {
	return popupContentWidth(m, 96, 48, 120)
}

func traceLogContentHeight(m Model) int {
	if m.height <= 0 {
		return 24
	}
	return clampPopupDimension(min(m.height-8, 26), 8, max(m.height-8, 1))
}

func traceLogBodyHeight(m Model) int {
	frame := fitPopupFrameToWindow(m, traceLogPopupFrame(m))
	target := popupContentTargetHeight(m, frame)
	footerH := lipgloss.Height(renderPickerHintItems(m, traceLogContentWidth(m), traceLogHintItems(m)))
	return max(target-footerH, 1)
}

func traceLogHintItems(m Model) []hintItem {
	return []hintItem{
		hintFromBindingDesc(m.keys.Back, "close"),
	}
}

func traceLogMaxScroll(m Model) int {
	lines := traceLogLines(m, traceLogContentWidth(m))
	return max(len(lines)-traceLogBodyHeight(m), 0)
}

func renderTraceLogPopup(m Model) string {
	width := traceLogContentWidth(m)
	bodyHeight := traceLogBodyHeight(m)
	lines := traceLogLines(m, width)
	start := 0
	if m.traceLog != nil {
		start = clampRange(m.traceLog.scroll, 0, traceLogMaxScroll(m))
	}
	body := strings.Join(dotsPeekVisibleLines(m.palette, lines, start, bodyHeight), "\n")
	return renderPopupBodyWithFooterItems(m, width, bodyHeight, body, traceLogHintItems(m))
}

func traceLogLines(m Model, width int) []string {
	if m.traceLogLoading && m.traceLog == nil {
		return []string{m.palette.styleHelp.Render("Loading command log...")}
	}
	if m.traceLog != nil && m.traceLog.err != nil {
		return []string{m.palette.styleMissing.Render("Failed to load command log: " + m.traceLog.err.Error())}
	}
	if m.traceLog == nil || len(m.traceLog.traces) == 0 {
		return []string{m.palette.styleHelp.Render("No commands recorded.")}
	}
	lines := make([]string, 0, len(m.traceLog.traces)*2)
	for _, trace := range m.traceLog.traces {
		lines = append(lines, traceLogPrimaryLine(m, trace, width)...)
		if sub := traceLogSubLine(m, trace); sub != "" {
			lines = append(lines, sub)
		}
		lines = append(lines, "")
	}
	// trim trailing blank
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func traceLogPrimaryLine(m Model, trace database.CommandTrace, width int) []string {
	when := trace.StartedAt.Local().Format("15:04:05")
	dur := fmt.Sprintf("%dms", trace.DurationMS)
	status := traceLogStatusText(m, trace)
	prefix := fmt.Sprintf("%s · %s · %s · ", when, dur, status)
	prefixWidth := lipgloss.Width(prefix)
	cmdWidth := max(width-prefixWidth, 1)
	wrapped := hardWrapLine(trace.Command, cmdWidth)
	if len(wrapped) == 0 {
		return []string{prefix}
	}
	out := make([]string, 0, len(wrapped))
	for i, seg := range wrapped {
		if i == 0 {
			out = append(out, prefix+seg)
		} else {
			out = append(out, strings.Repeat(" ", prefixWidth)+seg)
		}
	}
	return out
}

func traceLogStatusText(m Model, trace database.CommandTrace) string {
	p := m.palette
	switch strings.ToUpper(trace.Status) {
	case "OK", "SUCCESS":
		return p.styleInstalled.Render(trace.Status)
	case "ERROR", "FAIL", "FAILED":
		return p.styleMissing.Render(trace.Status)
	default:
		if trace.Status != "" {
			return p.styleHelp.Render(trace.Status)
		}
		return p.styleHelp.Render("unknown")
	}
}

func traceLogSubLine(m Model, trace database.CommandTrace) string {
	reason := strings.TrimSpace(trace.Reason)
	if reason == "" {
		return ""
	}
	return m.palette.styleHelp.Render("  reason: " + reason)
}
