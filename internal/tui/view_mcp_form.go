package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
)

func mcpFormPopupFrame(m Model) popupFrame {
	paddingX := 2
	contentW := popupContentWidth(m, 52, 40, 60)
	contentH := 5 + 2 + popupFooterHeight // 5 fields + blank + optional error line
	if m.mcpFormErr != nil {
		contentH++
	}
	return popupFrame{
		Title:          "Add MCP Server",
		PaddingY:       1,
		PaddingX:       paddingX,
		Width:          popupFrameWidthForContent(contentW, paddingX),
		ContentHeight:  contentH,
		NoTitleDivider: true,
	}
}

func renderMcpFormPopup(m Model) string {
	p := m.palette
	contentW := popupContentWidth(m, 52, 40, 60)

	commandLabel := "Command:"
	if m.mcpFormTransport > 0 {
		commandLabel = "URL:"
	}

	var sb strings.Builder
	sb.WriteString(renderMcpFormRow(m, contentW, "Name:", 0))
	sb.WriteString("\n")
	sb.WriteString(renderMcpFormTransportRow(m, contentW))
	sb.WriteString("\n")
	sb.WriteString(renderMcpFormRow(m, contentW, commandLabel, 2))
	sb.WriteString("\n")
	sb.WriteString(renderMcpFormRow(m, contentW, "Env vars:", 3))
	sb.WriteString("\n")
	sb.WriteString(renderMcpFormRow(m, contentW, "Env literal:", 4))
	sb.WriteString("\n\n")

	if m.mcpFormErr != nil {
		sb.WriteString(p.styleErr.Render(m.mcpFormErr.Error()))
		sb.WriteString("\n")
	}

	if m.mcpRunning {
		sb.WriteString(p.styleHelp.Render(m.spinner.View() + " adding…"))
		sb.WriteString("\n")
	}

	sb.WriteString(renderPickerHintItems(m, contentW, mcpFormHintItems(m)))
	return lipgloss.NewStyle().Width(contentW).Render(sb.String())
}

func mcpFormHintItems(m Model) []hintItem {
	return []hintItem{
		hintFromBindingDesc(m.keys.Tab, "next/prev field"),
		hintFromBindingDesc(m.keys.Confirm, "add"),
		hintFromBindingDesc(m.keys.Back, "cancel"),
	}
}

const mcpFormLabelWidth = 13

func renderMcpFormRow(m Model, width int, label string, field int) string {
	p := m.palette
	labelStyle := p.styleHelp
	if m.mcpFormField == field {
		labelStyle = p.styleActiveText
	}
	paddedLabel := label + strings.Repeat(" ", max(mcpFormLabelWidth-lipgloss.Width(label), 1))
	inputWidth := max(width-lipgloss.Width(paddedLabel)-4, 1)

	var input textinput.Model
	switch field {
	case 0:
		input = m.mcpFormName
	case 2:
		if m.mcpFormTransport == 0 {
			input = m.mcpFormCommand
		} else {
			input = m.mcpFormURL
		}
	case 3:
		input = m.mcpFormEnv
	case 4:
		input = m.mcpFormEnvLit
	}
	inputView := renderEmptyAwareTextInputView(p, input, input.Placeholder, inputWidth)

	return labelStyle.Render(paddedLabel) + p.styleHelp.Render("[ ") + inputView + p.styleHelp.Render(" ]")
}

func renderMcpFormTransportRow(m Model, width int) string {
	p := m.palette
	labelStyle := p.styleHelp
	if m.mcpFormField == 1 {
		labelStyle = p.styleActiveText
	}
	label := "Transport:"
	paddedLabel := label + strings.Repeat(" ", max(mcpFormLabelWidth-lipgloss.Width(label), 1))

	options := []string{"stdio", "http", "sse"}
	var parts []string
	for i, o := range options {
		style := p.styleNormal
		if i == m.mcpFormTransport {
			style = p.styleActiveText.Bold(true)
		}
		parts = append(parts, style.Render(o))
	}
	value := strings.Join(parts, p.styleHelp.Render("  ←→  "))

	return labelStyle.Render(paddedLabel) + p.styleHelp.Render("[ ") + value + p.styleHelp.Render(" ]")
}
