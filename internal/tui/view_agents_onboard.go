package tui

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
)

func agentsOnboardPopupFrame(m Model) popupFrame {
	title := "Agent Onboarding"
	if prompt := m.agentsOnboardPrompt; prompt != nil {
		switch prompt.kind {
		case agentsPromptOwnership:
			title = "Choose Ownership"
		case agentsPromptTargets:
			title = "Choose Agent Targets"
		case agentsPromptSecret:
			title = "Map Secret"
		case agentsPromptBlocked:
			title = "Cannot Migrate Automatically"
		case agentsPromptApply:
			title = "Apply Agent Onboarding"
		}
	}
	return popupFrame{Title: title, PaddingY: 1, PaddingX: 2, Width: clampPopupDimension(62, 38, popupFrameMaxWidth(m)), NoTitleDivider: true}
}

func renderAgentsOnboardPopup(m Model) string {
	prompt := m.agentsOnboardPrompt
	if prompt == nil || m.agentsOnboardPlan == nil || m.agentsOnboardPlan.Envelope.Plan == nil {
		return ""
	}
	p := m.palette
	var itemName string
	if prompt.item >= 0 {
		item := m.agentsOnboardPlan.Envelope.Plan.Items[prompt.item]
		itemName = item.Name
		if itemName == "" {
			itemName = item.Kind
		}
	}
	rows := []pickerChoiceRow{}
	intro, hint := "", "↑/↓ choose  Enter confirm  Esc cancel"
	switch prompt.kind {
	case agentsPromptOwnership:
		item := m.agentsOnboardPlan.Envelope.Plan.Items[prompt.item]
		keep := "Keep in dots"
		if item.Dots.Native {
			keep = "Keep unmanaged"
		}
		intro = fmt.Sprintf("How should %s be managed?", itemName)
		rows = []pickerChoiceRow{{mark: "", label: "Move to APM", selected: prompt.cursor == 0, style: p.styleNormal}, {mark: "", label: keep, selected: prompt.cursor == 1, style: p.styleNormal}}
	case agentsPromptTargets:
		item := m.agentsOnboardPlan.Envelope.Plan.Items[prompt.item]
		intro = fmt.Sprintf("Where should %s be installed?", itemName)
		for i, target := range item.TargetOptions {
			mark := "[ ]"
			if slices.Contains(item.Resolution.ApprovedTargets, target) {
				mark = "[x]"
			}
			rows = append(rows, pickerChoiceRow{mark: mark, label: target, selected: prompt.cursor == i, style: p.styleNormal})
		}
		hint = "↑/↓ choose  Space toggle  a all  Enter confirm  Esc cancel"
	case agentsPromptSecret:
		field := prompt.secretFields[prompt.secret]
		intro = fmt.Sprintf("Environment variable for %s (%s):", itemName, field)
		inputWidth := max(popupInnerContentWidth(fitPopupFrameToWindow(m, agentsOnboardPopupFrame(m)))-2, 1)
		return p.styleNormal.Render(intro) + "\n\n" + renderEmptyAwareTextInputView(p, m.settingsInput, "ENV_NAME", inputWidth) + "\n\n" + p.styleHelp.Render("Enter confirm  Esc cancel")
	case agentsPromptBlocked:
		if prompt.item < 0 {
			return p.styleErr.Render("The plan contains a global conflict that cannot be resolved here.") + "\n\n" + p.styleHelp.Render("Esc cancel")
		}
		item := m.agentsOnboardPlan.Envelope.Plan.Items[prompt.item]
		intro = fmt.Sprintf("%s cannot be migrated automatically: %s", itemName, strings.Join(item.Blockers, ", "))
		rows = []pickerChoiceRow{{label: "Keep unmanaged", selected: true, style: p.styleNormal}}
	case agentsPromptApply:
		intro = fmt.Sprintf("Apply the resolved plan for %d item(s)?", len(m.agentsOnboardPlan.Envelope.Plan.Items))
		rows = []pickerChoiceRow{{label: "Apply", selected: prompt.cursor == 0, style: p.styleNormal}, {label: "Cancel", selected: prompt.cursor == 1, style: p.styleNormal}}
	}
	var out strings.Builder
	out.WriteString(p.styleNormal.Render(intro))
	if len(rows) > 0 {
		out.WriteString("\n\n")
		labelWidth := 0
		for _, row := range rows {
			labelWidth = max(labelWidth, lipgloss.Width(row.label))
		}
		out.WriteString(strings.TrimSuffix(renderPickerChoiceRows(p, rows, labelWidth, 0), "\n"))
	}
	out.WriteString("\n\n")
	out.WriteString(p.styleHelp.Render(hint))
	return out.String()
}
