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
	// Declared intent, not a real gap, so it must render distinctly from agentsMarkMissing.
	agentsMarkShadowed
	// Another managed skill package owns the path; unlike plugin shadowing, the user can transfer ownership.
	agentsMarkPackageShadowed
	// A real gap like agentsMarkMissing, but one restore cannot silently close.
	agentsMarkDrifted
)

// Plugin shadowing wins outright; real drift still outranks package shadowing and updates.
func skillPackageRowStatus(installed, pluginShadowed, packageShadowed, drifted, outdated bool) (agentsRowStatus, agentsSyncMark) {
	if pluginShadowed {
		return agentsStatusInstalled, agentsMarkShadowed
	}
	if drifted {
		return agentsStatusOutOfSync, agentsMarkDrifted
	}
	if packageShadowed {
		return agentsStatusInstalled, agentsMarkPackageShadowed
	}
	if !installed {
		return agentsStatusOutOfSync, agentsMarkMissing
	}
	if outdated {
		return agentsStatusUpdates, agentsMarkNone
	}
	return agentsStatusInstalled, agentsMarkNone
}

func skillRowOutdated(r app.SkillPackageRow) bool {
	return r.Outdated == app.SkillOutdatedBehind
}

// Includes an entry another tool took over, where the attempt surfaces the collision instead of silently overwriting.
func agentsMarkNeedsInstall(mark agentsSyncMark) bool {
	return mark == agentsMarkMissing || mark == agentsMarkDrifted
}

func mcpAgentRowStatus(st app.McpStatus) (agentsRowStatus, agentsSyncMark) {
	switch st {
	case app.McpStatusMissing:
		return agentsStatusOutOfSync, agentsMarkMissing
	case app.McpStatusDrifted:
		return agentsStatusOutOfSync, agentsMarkDrifted
	case app.McpStatusShadowed:
		return agentsStatusInstalled, agentsMarkShadowed
	default:
		return agentsStatusInstalled, agentsMarkNone
	}
}

// Ranks drift above an available update: a plugin from the wrong marketplace has to be settled before "up to date" means anything.
func pluginAgentRowStatus(row app.PluginRow, agentID string) (agentsRowStatus, agentsSyncMark) {
	switch row.PerAgentStatus[agentID] {
	case app.PluginStatusMissing:
		return agentsStatusOutOfSync, agentsMarkMissing
	case app.PluginStatusDrifted:
		return agentsStatusOutOfSync, agentsMarkDrifted
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
