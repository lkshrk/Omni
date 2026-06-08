package tui

import "strings"

// providerPriorityContentWidth is the inner text width of the provider-priority
// popup.
func providerPriorityContentWidth(m Model) int {
	return popupContentWidth(m, 40, 28, 48)
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

// renderProviderPriorityPopup renders the provider-priority editor body: one row
// per concrete provider with a cursor/hold marker, an on/off dot, and dimmed
// styling for unavailable or disabled providers, plus the context hints footer.
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
		sb.WriteString(style.Render(enum + " " + dot + " " + name))
	}

	hintCtx := hintCtxSettingsPriorityEdit
	if m.priorityHolding {
		hintCtx = hintCtxSettingsPriorityHold
	}
	return renderPopupBodyWithFooterItems(m, width, len(m.priorityDraft), sb.String(), contextHintItems(m, hintCtx))
}
