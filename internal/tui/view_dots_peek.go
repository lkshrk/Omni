package tui

import (
	"fmt"
	"strings"

	"github.com/lkshrk/omni/internal/app"
)

func dotsPeekPopupFrame(m Model) popupFrame {
	return scrollPopupFrame(m, dotsPeekPopupTitle(m))
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
	return scrollPopupContentWidth(m)
}

func dotsPeekBodyHeight(m Model) int {
	return scrollPopupBodyHeight(m, dotsPeekPopupTitle(m), dotsPeekHintItems(m))
}

func renderDotsPeekPopup(m Model) string {
	title := dotsPeekPopupTitle(m)
	lines := dotsPeekLines(m, dotsPeekContentWidth(m))
	scroll := 0
	if m.dotsPeek != nil {
		scroll = m.dotsPeek.scroll
	}
	return renderScrollPopup(m, title, lines, scroll, dotsPeekHintItems(m))
}

func dotsPeekHintItems(m Model) []hintItem {
	return []hintItem{
		hintFromBindingDesc(m.keys.Back, "close"),
	}
}

func dotsPeekMaxScroll(m Model) int {
	lines := dotsPeekLines(m, dotsPeekContentWidth(m))
	return scrollPopupMaxScroll(lines, dotsPeekBodyHeight(m))
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
