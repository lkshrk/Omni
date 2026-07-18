package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/app"
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
	lines := make([]string, 0, len(m.traceLog.traces)*6)
	for _, trace := range m.traceLog.traces {
		lines = append(lines, traceLogEntryLines(m, trace, width)...)
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

func traceLogEntryLines(m Model, trace app.CommandTraceView, width int) []string {
	when := trace.StartedAt.Local().Format("15:04:05")
	dur := fmt.Sprintf("%dms", trace.DurationMS)
	status := traceLogStatusText(m, trace)
	header := fmt.Sprintf("%s · %s · %s", when, status, dur)
	if trace.ExitCode != nil {
		header += fmt.Sprintf(" · exit %d", *trace.ExitCode)
	}
	out := []string{header}
	out = append(out, traceLogFieldLines(m, "command", trace.Command, width, m.palette.styleNormal)...)
	out = append(out, traceLogFieldLines(m, "reason", trace.Reason, width, m.palette.styleHelp)...)
	if problem := traceLogProblem(trace); problem != "" {
		out = append(out, traceLogFieldLines(m, "problem", problem, width, m.palette.styleErr)...)
	}
	out = append(out, traceLogFieldLines(m, "error", trace.Error, width, m.palette.styleErr)...)
	out = append(out, traceLogFieldLines(m, "stderr", trace.Stderr, width, m.palette.styleHelp)...)
	return out
}

func traceLogFieldLines(m Model, label, value string, width int, style lipgloss.Style) []string {
	value = stripANSIEscapeSequences(strings.TrimRight(value, "\r\n"))
	if strings.TrimSpace(value) == "" {
		return nil
	}
	prefix := fmt.Sprintf("  %-9s", label+":")
	indent := strings.Repeat(" ", lipgloss.Width(prefix))
	valueWidth := max(width-lipgloss.Width(prefix), 1)
	var out []string
	first := true
	for _, physicalLine := range strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
		wrapped := hardWrapLine(physicalLine, valueWidth)
		if len(wrapped) == 0 {
			wrapped = []string{""}
		}
		for _, line := range wrapped {
			linePrefix := indent
			if first {
				linePrefix = m.palette.styleHelp.Render(prefix)
				first = false
			}
			out = append(out, linePrefix+style.Render(line))
		}
	}
	return out
}

func traceLogProblem(trace app.CommandTraceView) string {
	status := strings.ToUpper(strings.TrimSpace(trace.Status))
	if trace.Error == "" && status != "ERROR" && status != "FAIL" && status != "FAILED" {
		return ""
	}
	if strings.TrimSpace(trace.Stderr) != "" {
		return rowErrorSummary(trace.Stderr)
	}
	return rowErrorSummary(trace.Error)
}

func traceLogStatusText(m Model, trace app.CommandTraceView) string {
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
