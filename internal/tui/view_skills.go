package tui

import (
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/app"
)

// skillAgentIDs returns the sorted unique union of the host's enabled agents
// and every row's declared Agents field.
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

// skillRowTargetsAgent mirrors effectiveSkillAgents: a package with no declared
// agents installs to every enabled agent, so it matches any agent filter.
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

// looksLikeSkillSource returns true when the input looks like a package source
// (contains "/" and no spaces, or starts with "https://") rather than a free-text query.
func looksLikeSkillSource(s string) bool {
	if strings.HasPrefix(s, "https://") {
		return true
	}
	return strings.Contains(s, "/") && !strings.Contains(s, " ")
}

// skillsVisibleRows builds the ordered, filtered list of rows (local, find
// results, then unmanaged lockfile packages) that the cursor indexes. Local
// rows come first (Installed section sorted by name, then Not-Installed
// section sorted by name), followed by find results, followed by unmanaged
// rows. Returns the flat list plus the index at which find results begin
// (-1 if none) and the index at which unmanaged rows begin (-1 if none).
func skillsVisibleRows(m Model) (rows []app.SkillPackageRow, findStart int, unmanagedStart int) {
	agentIDs := skillAgentIDs(m.skillsRows, m.enabledAgents)
	q := ""
	if m.skillsSearchActive {
		q = strings.ToLower(m.filter.Value())
	}

	var installed, missing []app.SkillPackageRow
	for _, r := range m.skillsRows {
		// agent filter
		if m.skillAgentIdx > 0 && m.skillAgentIdx <= len(agentIDs) {
			if !skillRowTargetsAgent(r, agentIDs[m.skillAgentIdx-1]) {
				continue
			}
		}
		// search-text filter
		if q != "" {
			hay := strings.ToLower(r.Source + " " + r.Name)
			if !strings.Contains(hay, q) {
				continue
			}
		}
		if r.Installed {
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

	unmanagedStart = -1
	if len(m.skillsUnmanagedRows) > 0 {
		unmanagedStart = len(rows)
		rows = append(rows, m.skillsUnmanagedRows...)
	}
	return rows, findStart, unmanagedStart
}

// skillsManagedRowsEnd returns the exclusive end index of the managed
// (local) row range within skillsVisibleRows' output, i.e. the index of the
// first find-result or unmanaged row, whichever comes first.
func skillsManagedRowsEnd(findStart, unmanagedStart, total int) int {
	end := total
	if findStart >= 0 && findStart < end {
		end = findStart
	}
	if unmanagedStart >= 0 && unmanagedStart < end {
		end = unmanagedStart
	}
	return end
}

// clampSkillsCursor clamps m.skillsCursor to the visible row count.
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

	if !m.agentsEnabled {
		var b strings.Builder
		b.WriteString("\n")
		b.WriteString(p.styleHelp.Render(pad+"Agent skills are disabled for this machine.") + "\n\n")
		b.WriteString(p.styleHelp.Render(pad+"Toggle Agent Skills in Settings to enable.") + "\n")
		return b.String()
	}

	if !m.skillsSectionEnabled() && !m.mcpSectionEnabled() && !m.pluginsSectionEnabled() {
		var b strings.Builder
		b.WriteString("\n")
		b.WriteString(p.styleHelp.Render(pad+"All agent features are disabled for this machine.") + "\n\n")
		b.WriteString(p.styleHelp.Render(pad+"Toggle them in Settings to enable.") + "\n")
		return b.String()
	}

	// Pill filter bars as top lines.
	available := max(m.width-lipgloss.Width(pad), 1)
	disabledChips := map[int]bool{
		agentsChipSkills: !m.skillsSectionEnabled(),
		agentsChipMcp:    !m.mcpSectionEnabled(),
		agentsChipPlugin: !m.pluginsSectionEnabled(),
	}
	typeBar := renderPillBarDim(p, []string{"all", "skills", "mcp", "plugin"}, m.skillTypeIdx, available, disabledChips)
	typeBarW := lipgloss.Width(typeBar)

	var topLines []string
	if m.skillsSearchActive {
		topLines = append(topLines, renderSkillsSearchControl(m))
	}
	agentIDs := skillAgentIDs(m.skillsRows, m.enabledAgents)
	agentBarPart := ""
	if len(agentIDs) > 0 {
		sep := p.styleHelp.Render("   ·   ")
		sepW := lipgloss.Width(sep)
		remaining := available - typeBarW
		if remaining > sepW {
			agentBarPart = sep + renderPillBarFit(p, agentIDs, m.skillAgentIdx, remaining-sepW)
		}
	}
	topLines = append(topLines, "  "+typeBar+agentBarPart)

	if m.skillTypeIdx != agentsChipSkills {
		only := agentsSectionSkills
		switch m.skillTypeIdx {
		case agentsChipMcp:
			only = agentsSectionMcp
		case agentsChipPlugin:
			only = agentsSectionPlugins
		}
		return renderAgentsGroupedTab(m, p, topLines, only, m.skillTypeIdx != agentsChipAll)
	}

	if m.skillsErr != nil {
		topLines = append(topLines, p.styleErr.Render(pad+"error: "+m.skillsErr.Error()))
	}

	if len(m.skillsRows) == 0 && len(m.skillFindResults) == 0 {
		return renderSectionedTab(m, sectionedTab{
			leadingBlank: false,
			top:          topLines,
			sections: []sectionedTabSection{{
				rows: nil,
				empty: []string{
					p.styleHelp.Render(pad + "No agent skills tracked yet."),
					"",
					pad + p.styleNormal.Render("[i] import ") + p.styleHelp.Render(" capture skills already installed via the skills CLI"),
					pad + p.styleNormal.Render("[r] restore") + p.styleHelp.Render(" install your declared skills on this machine"),
				},
			}},
		})
	}

	return renderAgentsGroupedTab(m, p, topLines, agentsSectionSkills, true)
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
	labelW := 0
	for _, row := range rows {
		w := lipgloss.Width(row.Display)
		if row.Installed {
			w += lipgloss.Width(" ● installed")
		}
		if w > labelW {
			labelW = w
		}
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
		label := r.Display
		if r.Installed {
			label += " " + p.styleInstalled.Render("● installed")
		}
		pickerRows = append(pickerRows, pickerChoiceRow{selected: selected, label: label, mark: mark, style: style})
	}
	var sb strings.Builder
	sb.WriteString(renderPickerChoiceRows(p, pickerRows, labelW, 0))
	sb.WriteString("\n")
	sb.WriteString(renderPickerHintItems(m, contentW, toggleSaveCancelActionItems(m)))
	return sb.String()
}

func renderSkillsSearchControl(m Model) string {
	p := m.palette
	return "  " + p.styleNormal.Render("/") + " " + renderEmptyAwareTextInputView(p, m.filter, m.filter.Placeholder, 0)
}
