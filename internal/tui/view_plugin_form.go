package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
)

func pluginFormPopupFrame(m Model) popupFrame {
	paddingX := 2
	contentW := popupContentWidth(m, 52, 40, 60)
	contentH := 3 + 2 + popupFooterHeight // 3 fields + blank + optional error line
	if m.pluginFormErr != nil {
		contentH++
	}
	return popupFrame{
		Title:          "Add Plugin",
		PaddingY:       1,
		PaddingX:       paddingX,
		Width:          popupFrameWidthForContent(contentW, paddingX),
		ContentHeight:  contentH,
		NoTitleDivider: true,
	}
}

func renderPluginFormPopup(m Model) string {
	p := m.palette
	contentW := popupContentWidth(m, 52, 40, 60)

	var sb strings.Builder
	sb.WriteString(renderPluginFormRow(m, contentW, "Name:", 0))
	sb.WriteString("\n")
	sb.WriteString(renderPluginFormRow(m, contentW, "Marketplace:", 1))
	sb.WriteString("\n")
	sb.WriteString(renderPluginFormRow(m, contentW, "Agents:", 2))
	sb.WriteString("\n\n")

	if m.pluginFormErr != nil {
		sb.WriteString(p.styleErr.Render(m.pluginFormErr.Error()))
		sb.WriteString("\n")
	}

	if m.pluginRunning {
		sb.WriteString(p.styleHelp.Render(m.spinner.View() + " adding…"))
		sb.WriteString("\n")
	}

	sb.WriteString(renderPickerHintItems(m, contentW, pluginFormHintItems(m)))
	return lipgloss.NewStyle().Width(contentW).Render(sb.String())
}

func pluginFormHintItems(m Model) []hintItem {
	return []hintItem{
		hintFromBindingDesc(m.keys.Tab, "next/prev field"),
		hintFromBindingDesc(m.keys.Confirm, "add"),
		hintFromBindingDesc(m.keys.Back, "cancel"),
	}
}

const pluginFormLabelWidth = 13

func renderPluginFormRow(m Model, width int, label string, field int) string {
	p := m.palette
	labelStyle := p.styleHelp
	if m.pluginFormField == field {
		labelStyle = p.styleActiveText
	}
	paddedLabel := label + strings.Repeat(" ", max(pluginFormLabelWidth-lipgloss.Width(label), 1))
	inputWidth := max(width-lipgloss.Width(paddedLabel)-4, 1)

	var input textinput.Model
	switch field {
	case 0:
		input = m.pluginFormName
	case 1:
		input = m.pluginFormMarketplace
	case 2:
		input = m.pluginFormAgents
	}
	inputView := renderEmptyAwareTextInputView(p, input, input.Placeholder, inputWidth)

	return labelStyle.Render(paddedLabel) + p.styleHelp.Render("[ ") + inputView + p.styleHelp.Render(" ]")
}
