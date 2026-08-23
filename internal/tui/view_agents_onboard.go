package tui

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lkshrk/omni/internal/app"
)

func onboardKindLabel(kind string) string {
	switch kind {
	case "mcp":
		return "MCP Server"
	case "skill":
		return "Skill"
	case "plugin":
		return "Plugin"
	case "package":
		return "Package"
	case "marketplace":
		return "Marketplace"
	case "agent":
		return "Agent"
	case "prompt":
		return "Prompt"
	case "command":
		return "Command"
	case "hook":
		return "Hook"
	case "unsupported":
		return "Unsupported Finding"
	default:
		if kind == "" {
			return "Finding"
		}
		return strings.ToUpper(kind[:1]) + kind[1:]
	}
}

func agentsOnboardPopupFrame(m Model) popupFrame {
	title := "Agent Onboarding"
	if prompt := m.agentsOnboardPrompt; prompt != nil {
		if prompt.item >= 0 && m.agentsOnboardPlan != nil && m.agentsOnboardPlan.Envelope.Plan != nil {
			plan := m.agentsOnboardPlan.Envelope.Plan
			item := plan.Items[prompt.item]
			name := strings.TrimSpace(item.Name)
			if name == "" {
				name = "unnamed"
			}
			title = fmt.Sprintf("Review %s: %s · %d/%d", onboardKindLabel(item.Kind), name, prompt.item+1, len(plan.Items))
		} else if prompt.kind == agentsPromptBlocked {
			title = "Agent Onboarding Blocked"
		} else if prompt.kind == agentsPromptApply {
			title = "Apply Agent Onboarding"
		}
	}
	return popupFrame{Title: title, PaddingY: 1, PaddingX: 2, Width: clampPopupDimension(68, 42, popupFrameMaxWidth(m)), NoTitleDivider: true}
}

type onboardDetail struct{ label, value string }

func onboardPayload(item app.OnboardItem) map[string]any {
	var payload map[string]any
	_ = json.Unmarshal(item.Payload, &payload)
	return payload
}

func onboardString(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}

func onboardDisplayPath(value string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return value
	}
	clean := filepath.Clean(value)
	if clean == filepath.Clean(home) {
		return "~"
	}
	prefix := filepath.Clean(home) + string(os.PathSeparator)
	if strings.HasPrefix(clean, prefix) {
		return "~" + string(os.PathSeparator) + strings.TrimPrefix(clean, prefix)
	}
	return value
}

func onboardDisplaySource(value string) string {
	parsed, err := url.Parse(value)
	if err == nil && parsed.IsAbs() && parsed.Host != "" {
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.Fragment = ""
		return parsed.String()
	}
	return onboardDisplayPath(value)
}

func onboardFindingDetails(item app.OnboardItem) []onboardDetail {
	payload := onboardPayload(item)
	var details []onboardDetail
	if item.Dots != nil {
		owner := "Native agent configuration"
		if !item.Dots.Native {
			owner = "Omni dots"
			if item.Dots.Entry != "" {
				owner += " · " + item.Dots.Entry
			}
		}
		details = append(details, onboardDetail{"Owner", owner})
		if item.Dots.SourcePath != "" {
			details = append(details, onboardDetail{"Source", onboardDisplayPath(item.Dots.SourcePath)})
		}
		if item.Dots.TargetPath != "" && item.Dots.TargetPath != item.Dots.SourcePath {
			details = append(details, onboardDetail{"Target", onboardDisplayPath(item.Dots.TargetPath)})
		}
	} else {
		source := onboardString(payload, "source")
		if source == "" {
			source = onboardString(payload, "url")
		}
		if source == "" && strings.HasPrefix(item.Source, "native:") {
			source = strings.TrimPrefix(item.Source, "native:")
		}
		if source == "" && (strings.Contains(item.Source, "/") || strings.HasPrefix(item.Source, ".")) {
			source = item.Source
		}
		if source == "" {
			source = "Omni legacy agent configuration"
		}
		details = append(details, onboardDetail{"Source", onboardDisplaySource(source)})
	}
	for _, field := range []struct{ key, label string }{{"transport", "Transport"}, {"marketplace", "Marketplace"}, {"ref", "Version"}} {
		if value := onboardString(payload, field.key); value != "" {
			details = append(details, onboardDetail{field.label, value})
		}
	}
	if command := onboardString(payload, "command"); command != "" {
		parts := strings.Fields(command)
		if len(parts) > 1 {
			parts = append(parts[:1], "…")
		}
		details = append(details, onboardDetail{"Command", strings.Join(parts, " ")})
	}
	if reason := onboardString(payload, "unsupported_reason"); reason != "" {
		details = append(details, onboardDetail{"Reason", strings.ReplaceAll(reason, "-", " ")})
	}
	for _, field := range []struct{ key, label string }{{"group", "Group"}, {"host", "Host"}, {"detail", "Detail"}} {
		if value := onboardString(payload, field.key); value != "" {
			details = append(details, onboardDetail{field.label, value})
		}
	}
	if len(item.ProposedTargets) > 0 {
		details = append(details, onboardDetail{"Targets", strings.Join(item.ProposedTargets, ", ")})
	} else if onboardHasBlocker(item, func(blocker string) bool { return blocker == "target-resolution-required" }) {
		details = append(details, onboardDetail{"Targets", "Not detected"})
	}
	if len(item.TargetOptions) > 0 && !slices.Equal(item.ProposedTargets, item.TargetOptions) {
		details = append(details, onboardDetail{"Available", strings.Join(item.TargetOptions, ", ")})
	}
	if fields := onboardSecretFields(item.Payload); len(fields) > 0 {
		details = append(details, onboardDetail{"Secrets", strings.Join(fields, ", ") + " (values redacted)"})
	}
	if len(item.Blockers) > 0 {
		details = append(details, onboardDetail{"Issue", onboardBlockerText(item.Blockers)})
	}
	return details
}

func onboardBlockerText(blockers []string) string {
	issues := make([]string, 0, len(blockers))
	for _, blocker := range blockers {
		issue := ""
		switch {
		case blocker == "target-resolution-required":
			issue = "An install target is required; none was detected."
		case strings.HasPrefix(blocker, "unknown-target:"):
			issue = fmt.Sprintf("Configured target %q is unavailable.", strings.TrimPrefix(blocker, "unknown-target:"))
		case blocker == "secret-mapping-required":
			issue = "Secret fields must be mapped to environment variables."
		case blocker == "unsupported":
			issue = "This finding cannot be represented in APM."
		case blocker == "dependency-conflict":
			issue = "This conflicts with an existing APM dependency."
		default:
			issue = strings.ReplaceAll(blocker, "-", " ") + "."
		}
		if !slices.Contains(issues, issue) {
			issues = append(issues, issue)
		}
	}
	return strings.Join(issues, " ")
}

func renderOnboardDetail(p palette, detail onboardDetail, width int) string {
	labelWidth := 11
	if width < 48 {
		labelWidth = 8
	}
	valueWidth := max(width-labelWidth, 1)
	lines := strings.Split(ansi.Hardwrap(detail.value, valueWidth, false), "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}
	var out strings.Builder
	for i, line := range lines {
		label := ""
		if i == 0 {
			label = detail.label
		}
		out.WriteString(p.styleHelp.Render(fmt.Sprintf("%-*s", labelWidth, label)))
		out.WriteString(p.styleNormal.Render(line))
		if i < len(lines)-1 {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

func renderOnboardFinding(p palette, item app.OnboardItem, width int, compact bool) string {
	name := strings.TrimSpace(item.Name)
	if name == "" {
		name = "Unnamed " + strings.ToLower(onboardKindLabel(item.Kind))
	}
	var out strings.Builder
	if !compact {
		out.WriteString(p.styleTitle.Render(name))
	}
	for _, detail := range onboardFindingDetails(item) {
		if compact && detail.label != "Owner" && detail.label != "Source" && detail.label != "Issue" {
			continue
		}
		if out.Len() > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(renderOnboardDetail(p, detail, width))
	}
	return out.String()
}

func renderOnboardHints(p palette, width int, primary string, bulk bool) string {
	lines := []string{primary}
	if bulk {
		lines = append(lines, "ctrl+x keep all remaining unmanaged")
	}
	var out []string
	for _, line := range lines {
		for _, wrapped := range wrapText(line, width) {
			out = append(out, p.styleHelp.Render(wrapped))
		}
	}
	return strings.Join(out, "\n")
}

func renderAgentsOnboardPopup(m Model) string {
	prompt := m.agentsOnboardPrompt
	if prompt == nil || m.agentsOnboardPlan == nil || m.agentsOnboardPlan.Envelope.Plan == nil {
		return ""
	}
	p := m.palette
	plan := m.agentsOnboardPlan.Envelope.Plan
	width := popupInnerContentWidth(fitPopupFrameToWindow(m, agentsOnboardPopupFrame(m)))
	rows := []pickerChoiceRow{}
	question, hint := "", "↑/↓ choose  Enter confirm  Esc cancel"
	bulk := prompt.item >= 0 && prompt.kind != agentsPromptApply
	var item *app.OnboardItem
	if prompt.item >= 0 {
		item = &plan.Items[prompt.item]
	}
	switch prompt.kind {
	case agentsPromptOwnership:
		keep := "Keep in dots"
		if item.Dots.Native {
			keep = "Keep unmanaged"
		}
		question = "Choose Ownership: who should manage this finding?"
		rows = []pickerChoiceRow{{label: "Move to APM", selected: prompt.cursor == 0, style: p.styleNormal}, {label: keep, selected: prompt.cursor == 1, style: p.styleNormal}}
	case agentsPromptTargets:
		question = "Install this " + strings.ToLower(onboardKindLabel(item.Kind)) + " for:"
		for i, target := range item.TargetOptions {
			mark := "[ ]"
			if slices.Contains(item.Resolution.ApprovedTargets, target) {
				mark = "[x]"
			}
			rows = append(rows, pickerChoiceRow{mark: mark, label: target, selected: prompt.cursor == i, style: p.styleNormal})
		}
		hint = "↑/↓ choose  Space toggle  a all  Enter confirm  Esc cancel"
	case agentsPromptSecret:
		question = fmt.Sprintf("Environment variable for %q:", prompt.secretFields[prompt.secret])
	case agentsPromptBlocked:
		if prompt.item < 0 {
			return p.styleErr.Render("The plan contains a global conflict that cannot be resolved here.") + "\n\n" + renderOnboardHints(p, width, "Esc cancel", false)
		}
		question = "This finding cannot be migrated automatically."
		rows = []pickerChoiceRow{{label: "Keep unmanaged", selected: true, style: p.styleNormal}}
	case agentsPromptApply:
		migrate, move, dots, unmanaged := 0, 0, 0, 0
		for _, current := range plan.Items {
			switch current.Resolution.Decision {
			case "migrate", "map-secret":
				migrate++
			case "move-to-apm":
				move++
			case "keep-in-dots":
				dots++
			case "keep-unmanaged":
				unmanaged++
			}
		}
		question = fmt.Sprintf("%d findings reviewed\n%d migrate to APM\n%d move from dots/native to APM\n%d stay in dots\n%d remain unmanaged", len(plan.Items), migrate, move, dots, unmanaged)
		rows = []pickerChoiceRow{{label: "Apply changes", selected: prompt.cursor == 0, style: p.styleNormal}, {label: "Cancel", selected: prompt.cursor == 1, style: p.styleNormal}}
	}
	var out strings.Builder
	if item != nil {
		out.WriteString(renderOnboardFinding(p, *item, width, m.height > 0 && m.height < 24))
		out.WriteString("\n\n")
	}
	out.WriteString(p.styleNormal.Render(question))
	if prompt.kind == agentsPromptSecret {
		out.WriteString("\n\n")
		out.WriteString(renderEmptyAwareTextInputView(p, m.settingsInput, "ENV_NAME", width))
	} else if len(rows) > 0 {
		out.WriteString("\n\n")
		labelWidth := 0
		for _, row := range rows {
			labelWidth = max(labelWidth, lipgloss.Width(row.label))
		}
		out.WriteString(strings.TrimSuffix(renderPickerChoiceRows(p, rows, labelWidth, 0), "\n"))
	}
	out.WriteString("\n\n")
	out.WriteString(renderOnboardHints(p, width, hint, bulk))
	return out.String()
}
