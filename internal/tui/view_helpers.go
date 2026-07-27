package tui

import (
	"os"
	"strings"

	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Emoji-capable terminals get the globe; NO_EMOJI=1 forces the plain "o" fallback.
var logoMark = func() string {
	if os.Getenv("NO_EMOJI") != "" {
		return "o"
	}
	switch os.Getenv("TERM_PROGRAM") {
	case "iTerm.app", "WezTerm", "vscode", "ghostty", "Hyper", "Apple_Terminal":
		return "🌐"
	}
	if ct := os.Getenv("COLORTERM"); ct == "truecolor" || ct == "24bit" {
		return "🌐"
	}
	return "o"
}()

func renderHRule(pal palette, width int) string {
	return pal.styleSep.Render(strings.Repeat("─", max(width, 1)))
}

func renderEmptyAwareTextInputView(p palette, input textinput.Model, placeholder string, width int) string {
	if placeholder == "" {
		placeholder = input.Placeholder
	}
	if input.Value() != "" {
		input.Placeholder = placeholder
		if width > 0 {
			input.SetWidth(width)
		}
		return input.View()
	}

	input.Placeholder = ""
	input.SetWidth(0)
	view := input.View() + p.styleHelp.Render(placeholder)
	if width <= 0 {
		return view
	}
	return view + strings.Repeat(" ", max(width-lipgloss.Width(view), 0))
}

func alignLR(left, right string, totalWidth, minGap int) string {
	gap := max(totalWidth-lipgloss.Width(left)-lipgloss.Width(right), minGap)
	return left + strings.Repeat(" ", gap) + right
}

// fitCellText walks runes and would slice through an escape sequence, so anything carrying ANSI must use this instead.
func fitStyledText(s string, width int) string {
	if width < 1 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	return ansi.Truncate(s, width, "…")
}

// Preserves the line count (and any trailing newline) so callers tracking cursor rows stay in sync; a width below 1 means "size unknown" and leaves s untouched.
func clipLines(s string, width int) string {
	if width < 1 {
		return s
	}
	trailing := strings.HasSuffix(s, "\n")
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	for i, line := range lines {
		lines[i] = fitStyledText(line, width)
	}
	out := strings.Join(lines, "\n")
	if trailing {
		out += "\n"
	}
	return out
}

// A height below 1 means "size unknown" and leaves s untouched.
func clipFrame(s string, height int) string {
	if height < 1 {
		return s
	}
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	if len(lines) <= height {
		return s
	}
	return strings.Join(lines[:height], "\n")
}

type rowCellAlign int

const (
	rowCellAlignLeft rowCellAlign = iota
	rowCellAlignRight
)

type rowCell struct {
	text  string
	width int
	align rowCellAlign
}

func leftCell(text string, width int) rowCell {
	return rowCell{text: text, width: width, align: rowCellAlignLeft}
}

func rightCell(text string, width int) rowCell {
	return rowCell{text: text, width: width, align: rowCellAlignRight}
}

func renderSplitRow(left, right []rowCell, totalWidth, minGap, columnGap int) string {
	leftText := renderCellGroup(left, columnGap)
	rightText := renderCellGroup(right, columnGap)
	if rightText == "" {
		return leftText
	}
	if leftText == "" {
		return rightText
	}
	return alignLR(leftText, rightText, totalWidth, minGap)
}

func renderFixedGroupRow(first []rowCell, rest []rowCell, firstGap, columnGap int) string {
	firstText := renderCellGroup(first, columnGap)
	restText := renderCellGroup(rest, columnGap)
	if restText == "" {
		return firstText
	}
	if firstText == "" {
		return restText
	}
	return firstText + strings.Repeat(" ", firstGap) + restText
}

func renderCellGroup(cells []rowCell, columnGap int) string {
	var parts []string
	for _, cell := range cells {
		if cell.text == "" && cell.width == 0 {
			continue
		}
		parts = append(parts, renderCell(cell))
	}
	return strings.Join(parts, strings.Repeat(" ", columnGap))
}

func renderCell(cell rowCell) string {
	width := cell.width
	if width <= 0 {
		return cell.text
	}
	pad := max(width-lipgloss.Width(cell.text), 0)
	if cell.align == rowCellAlignRight {
		return strings.Repeat(" ", pad) + cell.text
	}
	return cell.text + strings.Repeat(" ", pad)
}

const (
	rowMarkerWidth        = 2
	screenEdgePadding     = rowMarkerWidth
	listIconWidth         = 1
	listIconGapWidth      = 1
	toolIconNameGapWidth  = 1
	listWideIconGapWidth  = 2
	listDetailExtraIndent = 1
	listHintExtraIndent   = 2
)

const selectedRowMarker = ">"

func screenEdgeInset() string {
	return strings.Repeat(" ", screenEdgePadding)
}

func screenContentWidth(width int) int {
	return max(width-screenEdgePadding, 1)
}

func rowAvailableWidth(width int) int {
	return max(width-rowMarkerWidth-screenEdgePadding, 1)
}

func selectedRowPrefix(p palette) string {
	return p.styleCursor.Render(selectedRowMarker) + " "
}

func inactiveRowPrefix() string {
	return strings.Repeat(" ", rowMarkerWidth)
}

func listRowPrefix(p palette, selected bool) string {
	if selected {
		return selectedRowPrefix(p)
	}
	return inactiveRowPrefix()
}

func rowSpinnerIcon(m Model) string {
	return strings.TrimSpace(m.spinner.View())
}

func listRowColumnStyle(selected bool, style lipgloss.Style) lipgloss.Style {
	if selected {
		return style.Bold(true)
	}
	return style
}

// Header, rows immediately below it, and exactly one blank line before the next section.
type listSectionWriter struct {
	pal          palette
	width        int
	write        func(string)
	wroteSection bool
}

func newListSectionWriter(pal palette, width int, write func(string)) *listSectionWriter {
	return &listSectionWriter{pal: pal, width: width, write: write}
}

func (w *listSectionWriter) Header(label string) {
	if w.wroteSection {
		w.write("\n")
	}
	w.write(renderSectionHeader(w.pal, label, w.width) + "\n")
	w.wroteSection = true
}

func rowContentInset() string {
	return strings.Repeat(" ", 2)
}

func listTextPrefix() string {
	return listTextPrefixWithGap(listIconGapWidth)
}

func listHintPrefix() string {
	return listHintPrefixWithGap(listIconGapWidth)
}

func listTextPrefixWithGap(iconGapWidth int) string {
	return strings.Repeat(" ", rowMarkerWidth+listIconWidth+iconGapWidth+listDetailExtraIndent)
}

func listHintPrefixWithGap(iconGapWidth int) string {
	return strings.Repeat(" ", rowMarkerWidth+listIconWidth+iconGapWidth+listDetailExtraIndent+listHintExtraIndent)
}

func textRowContentPrefix() string {
	return strings.Repeat(" ", rowMarkerWidth+2+listDetailExtraIndent)
}

func textRowHintPrefix() string {
	return strings.Repeat(" ", rowMarkerWidth+2+listDetailExtraIndent+listHintExtraIndent)
}

func confirmCancelHintWithPrefix(m Model, confirmLabel, prefix string) string {
	return renderConfirmActionHints(m, prefix, m.keys.Confirm, confirmLabel)
}

func confirmCancelHintText(m Model, confirmLabel string) string {
	return confirmActionHintText(m, m.keys.Confirm, confirmLabel)
}

type scrollBuf struct {
	sb         strings.Builder
	lineCount  int
	cursorLine int
}

func (b *scrollBuf) write(s string) {
	b.sb.WriteString(s)
	b.lineCount += strings.Count(s, "\n")
}

// Call just before writing the selected item's content.
func (b *scrollBuf) markCursor() {
	b.cursorLine = b.lineCount
}

// Use after rendering selected-row details or hints that must remain in view.
func (b *scrollBuf) markCursorEnd() {
	if b.lineCount > 0 {
		b.cursorLine = b.lineCount - 1
	}
}

func (b *scrollBuf) render(avail int) string {
	return applyScrollWindow(b.sb.String(), b.cursorLine, avail)
}

// titleSt styles the label; lineSt styles the trailing rule.
func renderSectionHeaderWith(label string, width int, titleSt, lineSt lipgloss.Style) string {
	title := titleSt.Render(label)
	pad := max(width-lipgloss.Width(title)-4, 1)
	return "  " + title + " " + lineSt.Render(strings.Repeat("─", pad))
}

func renderSectionHeader(pal palette, label string, width int) string {
	return renderSectionHeaderWith(label, width, pal.styleSection, pal.styleSep)
}

func renderSectionHeaderDanger(pal palette, label string, width int) string {
	return renderSectionHeaderWith(label, width, pal.styleDangerSection, pal.styleDangerLabel)
}

// activeIdx 0 = "all", 1+ = names[idx-1].
func renderPillBar(pal palette, names []string, activeIdx int) string {
	pill := func(label string, active bool) string {
		if active {
			return pal.styleTitle.Render("[" + label + "]")
		}
		return pal.styleHelp.Render(" " + label + " ")
	}
	var sb strings.Builder
	sb.WriteString(pill("all", activeIdx == 0))
	for i, name := range names {
		sb.WriteString(pill(name, activeIdx == i+1))
	}
	return sb.String()
}
