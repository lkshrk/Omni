package tui

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/app"
)

func agentsRowName(m Model, e agentsAllRow) string {
	if e.synthetic {
		return e.sortName
	}
	switch e.feature {
	case agentsSectionSkills:
		rows, _, _ := skillsVisibleRows(m)
		if e.localIdx >= 0 && e.localIdx < len(rows) {
			return rows[e.localIdx].Name
		}
	case agentsSectionMcp:
		if e.localIdx >= 0 && e.localIdx < len(m.mcpRows) {
			return m.mcpRows[e.localIdx].Name
		}
		flat, idx := mcpUnmanagedFlat(m.mcpUnmanaged), e.localIdx-len(m.mcpRows)
		if idx >= 0 && idx < len(flat) {
			return flat[idx].srv.Name
		}
	case agentsSectionPlugins:
		if e.localIdx >= 0 && e.localIdx < len(m.pluginRows) {
			return m.pluginRows[e.localIdx].Name
		}
		flat, idx := pluginUnmanagedFlat(m.pluginUnmanaged), e.localIdx-len(m.pluginRows)
		if idx >= 0 && idx < len(flat) {
			return flat[idx].plugin.Name
		}
	case agentsSectionMarketplaces:
		if e.localIdx >= 0 && e.localIdx < len(m.marketplaceRows) {
			return m.marketplaceRows[e.localIdx].Name
		}
		flat, idx := marketplaceUnmanagedFlat(m.marketplaceUnmanaged), e.localIdx-len(m.marketplaceRows)
		if idx >= 0 && idx < len(flat) {
			return flat[idx].marketplace.Name
		}
	}
	return ""
}

func agentsMcpStatusMarker(status app.McpStatus) string {
	switch status {
	case app.McpStatusInstalled:
		return "✓"
	case app.McpStatusShadowed:
		return "via-plugin"
	case app.McpStatusDrifted:
		return "drift"
	default:
		return "-"
	}
}

func agentsMcpVersionText(version string) string {
	if version == "" {
		return "-"
	}
	return version
}

// Which side owns the registration decides what any remedy can be, so it travels with every cell; global MCP
// has no per-server scoping, so an agent the config never named is called out as deployed anyway.
func agentsMcpRowCells(row app.McpServerRow, agentIDs []string) []string {
	cells := make([]string, 0, len(agentIDs))
	for _, id := range agentIDs {
		status, ok := row.PerAgentStatus[id]
		if !ok || status == app.McpStatusAgentUnavailable {
			continue
		}
		owner := "native"
		if row.APMManaged(id) {
			owner = "apm"
		}
		cell := fmt.Sprintf("%s(%s %s)", id, agentsMcpStatusMarker(status), owner)
		if slices.Contains(row.UndeclaredAPMAgents, id) {
			cell += " deployed, undeclared (APM)"
		}
		cells = append(cells, cell)
	}
	return cells
}

func agentsMcpRowLine(row app.McpServerRow, agentIDs []string) string {
	line := row.Name + "  " + row.Transport + "  " + agentsMcpVersionText(row.Version)
	if cells := agentsMcpRowCells(row, agentIDs); len(cells) > 0 {
		line += "  " + strings.Join(cells, " ")
	}
	return line
}

// APM owns deployment now, so the tab lists rather than edits — but ownership and APM's wider reach are
// state only omni can report, and a row that showed neither read as if the config had been honoured.
func agentsMcpSummaryLines(m Model) []string {
	if !m.mcpSectionEnabled() || !m.mcpRowsKnown {
		return nil
	}
	ignored := map[string]bool{}
	for _, name := range m.agentsIgnore.McpServers {
		ignored[name] = true
	}
	var lines []string
	for _, row := range m.mcpRows {
		if ignored[row.Name] {
			continue
		}
		lines = append(lines, agentsMcpRowLine(row, mcpRowAgentIDs(row, m.enabledAgents)))
	}
	return lines
}

func skillAgentsWithStatus(r app.SkillPackageRow, enabledAgents []string, wanted app.SkillStatus) []string {
	enabled := make(map[string]bool, len(enabledAgents))
	for _, id := range enabledAgents {
		enabled[id] = true
	}
	var matched []string
	for id, status := range r.PerAgentStatus {
		if status == wanted && enabled[id] {
			matched = append(matched, id)
		}
	}
	sort.Strings(matched)
	return matched
}

func skillDriftedAgents(r app.SkillPackageRow, enabledAgents []string) []string {
	return skillAgentsWithStatus(r, enabledAgents, app.SkillStatusDrifted)
}

func skillShadowedAgents(r app.SkillPackageRow, enabledAgents []string) []string {
	return skillAgentsWithStatus(r, enabledAgents, app.SkillStatusShadowed)
}

func skillMissingAgents(r app.SkillPackageRow, enabledAgents []string) []string {
	return skillAgentsWithStatus(r, enabledAgents, app.SkillStatusMissing)
}

func agentsFeatureCursor(m Model, feature agentsSection) int {
	switch feature {
	case agentsSectionSkills:
		return m.skillsCursor
	case agentsSectionMcp:
		return m.mcpCursor
	case agentsSectionPlugins:
		return m.pluginCursor
	case agentsSectionMarketplaces:
		return m.marketplaceCursor
	default:
		return m.pluginCursor
	}
}
