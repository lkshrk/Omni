package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/database"
)

const traceLogTitle = "Command Log"

func traceLogPopupFrame(m Model) popupFrame {
	return scrollPopupFrame(m, traceLogTitle)
}

func traceLogContentWidth(m Model) int {
	return scrollPopupContentWidth(m)
}

func traceLogBodyHeight(m Model) int {
	return scrollPopupBodyHeight(m, traceLogTitle, traceLogHintItems(m))
}

func traceLogHintItems(m Model) []hintItem {
	return []hintItem{
		hintFromBindingDesc(m.keys.Back, "close"),
	}
}

func traceLogMaxScroll(m Model) int {
	lines := traceLogLines(m, traceLogContentWidth(m))
	return scrollPopupMaxScroll(lines, traceLogBodyHeight(m))
}

func renderTraceLogPopup(m Model) string {
	lines := traceLogLines(m, traceLogContentWidth(m))
	scroll := 0
	if m.traceLog != nil {
		scroll = m.traceLog.scroll
	}
	return renderScrollPopup(m, traceLogTitle, lines, scroll, traceLogHintItems(m))
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
