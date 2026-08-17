package tui

import (
	"sort"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/app"
)

func skillAgentIDs(rows []app.SkillPackageRow, enabled []string) []string {
	seen := make(map[string]bool)
	for _, id := range enabled {
		seen[id] = true
	}
	for _, r := range rows {
		for _, id := range r.Agents {
			seen[id] = true
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Mirrors effectiveSkillAgents: a package with no declared agents installs to every enabled agent, so it matches any agent filter.
func skillRowTargetsAgent(r app.SkillPackageRow, id string) bool {
	if len(r.Agents) == 0 {
		return true
	}
	for _, a := range r.Agents {
		if a == id {
			return true
		}
	}
	return false
}

// Order is local rows (installed then not-installed, each by name), then find results, then unmanaged; returns the start index of the latter two, -1 when absent.
func skillsVisibleRows(m Model) (rows []app.SkillPackageRow, findStart int, unmanagedStart int) {
	agentIDs := skillAgentIDs(m.skillsRows, m.enabledAgents)
	q := agentsFilterQuery(m)

	agentFilter := ""
	if m.skillAgentIdx > 0 && m.skillAgentIdx <= len(agentIDs) {
		agentFilter = agentIDs[m.skillAgentIdx-1]
	}
	matchesText := func(r app.SkillPackageRow) bool {
		return q == "" || strings.Contains(strings.ToLower(r.Source+" "+r.Name), q)
	}

	var installed, missing []app.SkillPackageRow
	for _, r := range m.skillsRows {
		if agentFilter != "" && !skillRowTargetsAgent(r, agentFilter) {
			continue
		}
		if !matchesText(r) {
			continue
		}
		packageShadowed := len(skillShadowedAgents(r, m.enabledAgents)) > 0 &&
			len(skillMissingAgents(r, m.enabledAgents)) == 0
		if r.Installed || r.ShadowedByPlugin || packageShadowed {
			installed = append(installed, r)
		} else {
			missing = append(missing, r)
		}
	}
	sort.Slice(installed, func(i, j int) bool { return installed[i].Name < installed[j].Name })
	sort.Slice(missing, func(i, j int) bool { return missing[i].Name < missing[j].Name })

	rows = append(rows, installed...)
	rows = append(rows, missing...)

	findStart = -1
	if len(m.skillFindResults) > 0 {
		findStart = len(rows)
		for _, fr := range m.skillFindResults {
			rows = append(rows, app.SkillPackageRow{
				Name:   fr.Skill,
				Source: fr.Source,
				Ref:    fr.Installs,
			})
		}
	}

	var unmanaged []app.SkillPackageRow
	for _, r := range m.skillsUnmanagedRows {
		// Unmanaged lockfile rows declare no agent list, so where the package actually landed is only readable from the per-agent status map.
		if agentFilter != "" && r.PerAgentStatus[agentFilter] != app.SkillStatusInstalled {
			continue
		}
		if !matchesText(r) {
			continue
		}
		unmanaged = append(unmanaged, r)
	}
	unmanagedStart = -1
	if len(unmanaged) > 0 {
		unmanagedStart = len(rows)
		rows = append(rows, unmanaged...)
	}
	return rows, findStart, unmanagedStart
}

// The query is tab-level: every section's row list narrows by it, not just skills.
func agentsFilterQuery(m Model) string {
	if !m.skillsSearchActive {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(m.filter.Value()))
}

func clampSkillsCursor(m *Model) {
	visible, _, _ := skillsVisibleRows(*m)
	n := len(visible)
	if n == 0 {
		m.skillsCursor = 0
		return
	}
	if m.skillsCursor >= n {
		m.skillsCursor = n - 1
	}
	if m.skillsCursor < 0 {
		m.skillsCursor = 0
	}
}

func (m Model) viewSkillsBody() string {
	p := m.palette
	pad := screenEdgeInset()
	lines := []string{
		"",
		p.styleNormal.Render(pad + "Agent packages are managed by Microsoft APM."),
		p.styleHelp.Render(pad + "Manifest: ~/.apm/apm.yml"),
		"",
		p.styleHelp.Render(pad + "S sync   U update   e logs"),
		p.styleHelp.Render(pad + "CLI: omni agents add|remove|update|search"),
	}
	if mcp := agentsMcpSummaryLines(m); len(mcp) > 0 {
		lines = append(lines, "", p.styleHelp.Render(pad+"MCP servers"))
		for _, line := range mcp {
			lines = append(lines, p.styleNormal.Render(pad+"  "+line))
		}
	}
	if m.skillsErr != nil {
		text := strings.Join(strings.Fields("error: "+m.skillsErr.Error()), " ")
		const maxErrorRunes = 600
		if runes := []rune(text); len(runes) > maxErrorRunes {
			text = string(runes[:maxErrorRunes-1]) + "…"
		}
		avail := max(screenContentWidth(m.width)-screenEdgePadding, 20)
		lines = append(lines, "", p.styleErr.PaddingLeft(screenEdgePadding).Width(avail).Render(text))
	}
	return strings.Join(lines, "\n") + "\n"
}

// Caps the modal's list so a fleet-scale drift report stays a prompt rather than a scrolling log.
const agentsDriftPromptMaxRows = 10

func renderAgentsDriftPromptPopup(m Model) string {
	p := m.palette
	contentW := agentsDriftPromptContentWidth(m)
	lines := m.agentsDriftPromptLines

	var sb strings.Builder
	summary := "1 agent resource drifted from the manifest."
	if len(lines) != 1 {
		summary = strconv.Itoa(len(lines)) + " agent resources drifted from the manifest."
	}
	sb.WriteString(p.styleOutdated.Render(fitCellText(summary, contentW)))
	sb.WriteString("\n\n")
	shown := min(len(lines), agentsDriftPromptMaxRows)
	for _, line := range lines[:shown] {
		sb.WriteString(p.styleHelp.Render(fitCellText("  "+agentsDriftPromptRowText(line), contentW)))
		sb.WriteString("\n")
	}
	if rest := len(lines) - shown; rest > 0 {
		sb.WriteString(p.styleHelp.Render(fitCellText("  …and "+strconv.Itoa(rest)+" more", contentW)))
		sb.WriteString("\n")
	}
	sb.WriteString(renderPickerHintItems(m, contentW, agentsDriftPromptHints(m)))
	return lipgloss.NewStyle().Width(contentW).Render(sb.String())
}

func agentsDriftPromptHints(m Model) []hintItem {
	if m.agentsBulkResolveConfirm {
		armed := m.keys.AgentsUseLocalAll
		if m.agentsBulkResolveUseManaged {
			armed = m.keys.AgentsUseManagedAll
		}
		return []hintItem{pressAgainHint(armed.Help().Key, armed.Help().Desc), hintFromBindingDesc(m.keys.Back, "cancel")}
	}
	return []hintItem{
		hintFromBinding(m.keys.AgentsUseManagedAll),
		hintFromBinding(m.keys.AgentsUseLocalAll),
		hintFromBindingDesc(m.keys.Back, "dismiss"),
	}
}

// Drops the trailing CLI remedy every drift line carries: the modal's own U/L keys are the remedy here.
func agentsDriftPromptRowText(line string) string {
	if idx := strings.Index(line, "; resolve with "); idx >= 0 {
		return line[:idx]
	}
	return line
}

func agentsDriftPromptContentWidth(m Model) int {
	preferred := 56
	for _, line := range m.agentsDriftPromptLines {
		preferred = max(preferred, lipgloss.Width(agentsDriftPromptRowText(line))+2)
	}
	return popupContentWidth(m, preferred, 44, 96)
}

func agentsDriftPromptPopupFrame(m Model) popupFrame {
	paddingX := 2
	return popupFrame{
		Title:          "Drift Detected",
		PaddingY:       1,
		PaddingX:       paddingX,
		Width:          popupFrameWidthForContent(agentsDriftPromptContentWidth(m), paddingX),
		NoTitleDivider: true,
	}
}

func skillAgentsPickerFrame(m Model) popupFrame {
	contentH := len(m.skillAgentsRows) + popupFooterHeight
	if len(m.skillAgentsRows) == 0 {
		contentH = 1 + popupFooterHeight
	}
	title := "Agents"
	if m.skillAgentsSource != "" {
		title = "Agents: " + m.skillAgentsSource
	}
	return popupFrame{
		Title:          title,
		PaddingY:       1,
		PaddingX:       2,
		Width:          popupFrameWidthForContent(popupContentWidth(m, 40, 32, 56), 2),
		ContentHeight:  contentH,
		NoTitleDivider: true,
	}
}

// Kept separate from the selection checkbox so the two never read as one claim.
func skillAgentInstalledLabel(r app.SkillAgentRow) string {
	if r.Installed {
		return "● installed"
	}
	return "○ not installed"
}

func renderSkillAgentsPicker(m Model) string {
	p := m.palette
	rows := m.skillAgentsRows
	if len(rows) == 0 {
		prefix := textRowContentPrefix()
		var sb strings.Builder
		sb.WriteString(prefix + p.styleHelp.Render("No supported agents detected on this machine.") + "\n")
		sb.WriteString(renderPickerHintItems(m, rowAvailableWidth(m.width), []hintItem{
			hintFromBindingDesc(m.keys.Back, "cancel"),
		}))
		return sb.String()
	}
	contentW := rowAvailableWidth(m.width)
	// The checkbox is selection and the detail column is on-disk state; gluing "installed" onto the name made an unchecked box beside it read as a contradiction.
	labelW, detailW := 0, 0
	for _, row := range rows {
		labelW = max(labelW, lipgloss.Width(row.Display))
		detailW = max(detailW, lipgloss.Width(skillAgentInstalledLabel(row)))
	}
	pickerRows := make([]pickerChoiceRow, 0, len(rows))
	for i, r := range rows {
		selected := i == m.skillAgentsCursor
		style := p.styleNormal
		if selected {
			style = p.styleActiveText
		}
		mark := "[ ]"
		if r.Targeted {
			mark = "[x]"
		}
		pickerRows = append(pickerRows, pickerChoiceRow{
			selected: selected,
			label:    r.Display,
			detail:   skillAgentInstalledLabel(r),
			mark:     mark,
			style:    style,
		})
	}
	var sb strings.Builder
	sb.WriteString(renderPickerChoiceRows(p, pickerRows, labelW, detailW))
	sb.WriteString("\n")
	sb.WriteString(renderPickerHintItems(m, contentW, toggleSaveCancelActionItems(m)))
	return sb.String()
}
