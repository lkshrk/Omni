package tui

import (
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/app"
)

func (m Model) viewSkillsBody() string {
	p := m.palette
	pad := screenEdgeInset()
	var b strings.Builder
	b.WriteString("\n")

	if !m.agentsEnabled {
		b.WriteString(p.styleHelp.Render(pad+"Agent skills are disabled for this machine.") + "\n\n")
		b.WriteString(p.styleHelp.Render(pad+"Toggle Agent Skills in Settings to enable.") + "\n")
		return b.String()
	}
	if m.skillsErr != nil {
		b.WriteString(p.styleErr.Render(pad+"error: "+m.skillsErr.Error()) + "\n")
		return b.String()
	}

	if len(m.skillsRows) == 0 {
		b.WriteString(p.styleHelp.Render(pad+"No agent skills tracked yet.") + "\n\n")
		b.WriteString(pad + p.styleNormal.Render("[i] import ") + p.styleHelp.Render(" capture skills already installed via the skills CLI") + "\n")
		b.WriteString(pad + p.styleNormal.Render("[r] restore") + p.styleHelp.Render(" install your declared skills on this machine") + "\n")
		return b.String()
	}

	b.WriteString(m.renderSkillsGrouped())
	if m.skillsRunning {
		b.WriteString("\n" + p.styleHelp.Render(pad+"working…") + "\n")
	}
	if m.skillsResult != nil {
		b.WriteString("\n" + p.styleNormal.Render(pad+app.RestoreSkillsSummaryText(*m.skillsResult)) + "\n")
	}
	if m.skillsImport != nil {
		b.WriteString("\n" + p.styleNormal.Render(pad+app.ImportDiffSummaryText(*m.skillsImport)) + "\n")
	}
	b.WriteString("\n" + p.styleHelp.Render(pad+"[r] restore   [i] import   [u] update") + "\n")
	return b.String()
}

func (m Model) renderSkillsGrouped() string {
	p := m.palette
	contentW := rowAvailableWidth(m.width)
	iconGap := strings.Repeat(" ", toolIconNameGapWidth)

	skillStatusLabel := func(r app.SkillRow) string {
		if r.Installed {
			return "installed"
		}
		return "missing"
	}

	statusW, srcW, refW, updW := 0, 0, 0, 0
	for _, r := range m.skillsRows {
		statusW = max(statusW, lipgloss.Width(skillStatusLabel(r)))
		srcW = max(srcW, lipgloss.Width(r.Source))
		refW = max(refW, lipgloss.Width(r.Ref))
		updW = max(updW, lipgloss.Width(r.Updated))
	}

	renderRow := func(r app.SkillRow) string {
		var icon string
		var statusStyle lipgloss.Style
		if r.Installed {
			icon = p.styleInstalled.Render(iconInstalled)
			statusStyle = p.styleInstalled
		} else {
			icon = p.styleMissing.Render(iconMissing)
			statusStyle = p.styleMissing
		}
		left := []rowCell{leftCell(icon+iconGap+p.styleNormal.Render(r.Name), 0)}
		right := []rowCell{
			leftCell(p.styleHelp.Render(fitCellText(r.Source, srcW)), srcW),
			leftCell(p.styleHelp.Render(fitCellText(r.Ref, refW)), refW),
			leftCell(statusStyle.Render(fitCellText(skillStatusLabel(r), statusW)), statusW),
			leftCell(p.styleVersionMuted.Render(fitCellText(r.Updated, updW)), updW),
		}
		return inactiveRowPrefix() + renderSplitRow(left, right, contentW, listColumnGap, listColumnGap)
	}

	installed := make([]app.SkillRow, 0)
	missing := make([]app.SkillRow, 0)
	for _, r := range m.skillsRows {
		if r.Installed {
			installed = append(installed, r)
		} else {
			missing = append(missing, r)
		}
	}
	sort.Slice(installed, func(i, j int) bool { return installed[i].Name < installed[j].Name })
	sort.Slice(missing, func(i, j int) bool { return missing[i].Name < missing[j].Name })

	var buf strings.Builder
	write := func(s string) { buf.WriteString(s) }
	sections := newListSectionWriter(p, m.width, write)

	if len(installed) > 0 {
		sections.Header("Installed")
		for _, r := range installed {
			write(renderRow(r) + "\n")
		}
	}
	if len(missing) > 0 {
		sections.Header("Not Installed")
		for _, r := range missing {
			write(renderRow(r) + "\n")
		}
	}

	return buf.String()
}
