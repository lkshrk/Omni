package tui

import "github.com/lkshrk/omni/internal/app"

type agentsRowStatus int

const (
	agentsStatusUpdates agentsRowStatus = iota
	agentsStatusOutOfSync
	agentsStatusInstalled
	agentsStatusAvailable
	agentsStatusIgnored
)

type agentsSyncMark int

const (
	agentsMarkNone agentsSyncMark = iota
	agentsMarkMissing
	agentsMarkOrphan
	// agentsMarkShadowed marks a manifest row that an installed plugin of the
	// same name already provides (see app.McpStatusShadowed /
	// app.SkillPackageRow.ShadowedByPlugin) — declared intent, not a real
	// gap, so it must render distinctly from agentsMarkMissing.
	agentsMarkShadowed
)

// skillPackageRowStatus reports a managed skills row's status/mark.
// shadowed (an installed plugin already provides this package on a target
// agent — see app.SkillPackageRow.ShadowedByPlugin) takes precedence over the
// plain installed/missing split: it is declared intent, not a real gap.
func skillPackageRowStatus(installed, shadowed bool) (agentsRowStatus, agentsSyncMark) {
	if shadowed {
		return agentsStatusInstalled, agentsMarkShadowed
	}
	if !installed {
		return agentsStatusOutOfSync, agentsMarkMissing
	}
	return agentsStatusInstalled, agentsMarkNone
}

func mcpAgentRowStatus(st app.McpStatus) (agentsRowStatus, agentsSyncMark) {
	switch st {
	case app.McpStatusMissing:
		return agentsStatusOutOfSync, agentsMarkMissing
	case app.McpStatusShadowed:
		return agentsStatusInstalled, agentsMarkShadowed
	default:
		return agentsStatusInstalled, agentsMarkNone
	}
}

func pluginAgentRowStatus(row app.PluginRow, agentID string) (agentsRowStatus, agentsSyncMark) {
	if row.PerAgentStatus[agentID] == app.PluginStatusMissing {
		return agentsStatusOutOfSync, agentsMarkMissing
	}
	if row.Outdated() {
		return agentsStatusUpdates, agentsMarkNone
	}
	return agentsStatusInstalled, agentsMarkNone
}

func marketplaceAgentRowStatus(st app.PluginStatus) (agentsRowStatus, agentsSyncMark) {
	if st == app.PluginStatusMissing {
		return agentsStatusOutOfSync, agentsMarkMissing
	}
	return agentsStatusInstalled, agentsMarkNone
}
