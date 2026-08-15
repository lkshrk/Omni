package tui

import (
	"sort"

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
