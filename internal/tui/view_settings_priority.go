package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

func providerPriorityContentWidth(m Model) int {
	// Wide enough that the footer hints sit on a single line instead of wrapping the primary action below.
	return popupContentWidth(m, 54, 48, 64)
}

func providerPriorityPopupFrame(m Model) popupFrame {
	return popupFrame{
		Title:          "Provider Priority",
		PaddingY:       1,
		PaddingX:       2,
		Width:          popupFrameWidthForContent(providerPriorityContentWidth(m), 2),
		ContentHeight:  len(m.priorityDraft) + popupFooterHeight,
		NoTitleDivider: true,
	}
}

const providerPriorityRowIndent = "   "

func renderProviderPriorityPopup(m Model) string {
	p := m.palette
	width := providerPriorityContentWidth(m)

	var sb strings.Builder
	for j, name := range m.priorityDraft {
		cursorOn := j == m.priorityCursor
		enum := " "
		if cursorOn {
			if m.priorityHolding {
				enum = "⇅"
			} else {
				enum = "‣"
			}
		}
		dot := "●"
		if m.priorityDisabled[name] {
			dot = "○"
		}
		unavailable := m.priorityAvailable != nil && !m.priorityAvailable[name]
		style := p.styleNormal
		switch {
		case cursorOn:
			style = p.styleActiveText
		case unavailable || m.priorityDisabled[name]:
			style = p.styleHelp
		}
		if j > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(providerPriorityRowIndent + style.Render(enum+" "+dot+" "+name))
	}

	hintCtx := hintCtxSettingsPriorityEdit
	if m.priorityHolding {
		hintCtx = hintCtxSettingsPriorityHold
	}
	footer := popupDivider(p, width) + "\n" + renderJustifiedHintItems(p, width, contextHintItems(m, hintCtx))
	return lipgloss.NewStyle().Width(width).Render(sb.String() + "\n" + footer)
}

// Space-between across width: the first item sits at the left edge, the last at the right edge, with equal gaps.
func renderJustifiedHintItems(pal palette, width int, hints []hintItem) string {
	if len(hints) == 0 {
		return ""
	}
	parts := make([]string, len(hints))
	total := 0
	for i, h := range hints {
		parts[i] = renderHintItem(pal, h)
		total += lipgloss.Width(parts[i])
	}
	if len(parts) == 1 {
		return lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(parts[0])
	}
	gaps := len(parts) - 1
	slack := width - total
	if slack < gaps {
		return strings.Join(parts, " ") // too narrow to justify; single-space join
	}
	base := slack / gaps
	extra := slack % gaps
	var b strings.Builder
	for i, part := range parts {
		b.WriteString(part)
		if i < gaps {
			g := base
			if i < extra {
				g++
			}
			b.WriteString(strings.Repeat(" ", g))
		}
	}
	return b.String()
}
