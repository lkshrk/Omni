package tui

import (
	"testing"

	"github.com/lkshrk/omni/internal/app"
)

// agentsComposedCase describes one reachable (state, feature) row this test
// asserts always carries at least one detail line and one hint once selected.
type agentsComposedCase struct {
	name    string
	feature agentsSection
	build   func() Model
}

func agentsComposedCases() []agentsComposedCase {
	return []agentsComposedCase{
		{
			name:    "skills/installed",
			feature: agentsSectionSkills,
			build: func() Model {
				return agentsAllModel([]app.SkillPackageRow{
					{Name: "sk-installed", Source: "o/sk-installed", Skills: []string{"a"}, Installed: true, PerAgentStatus: map[string]bool{"claude": true}},
				}, nil, nil)
			},
		},
		{
			name:    "skills/missing",
			feature: agentsSectionSkills,
			build: func() Model {
				return agentsAllModel([]app.SkillPackageRow{
					{Name: "sk-missing", Source: "o/sk-missing", Skills: []string{"a"}, Installed: false},
				}, nil, nil)
			},
		},
		{
			name:    "skills/missing-unscanned",
			feature: agentsSectionSkills,
			build: func() Model {
				return agentsAllModel([]app.SkillPackageRow{
					{Name: "sk-unscanned", Source: "o/sk-unscanned", Installed: false},
				}, nil, nil)
			},
		},
		{
			name:    "skills/orphan",
			feature: agentsSectionSkills,
			build: func() Model {
				m := agentsAllModel(nil, nil, nil)
				m.skillsUnmanagedRows = []app.SkillPackageRow{
					{Name: "sk-orphan", Source: "o/sk-orphan"},
				}
				return m
			},
		},
		{
			name:    "skills/ignored",
			feature: agentsSectionSkills,
			build: func() Model {
				m := agentsAllModel([]app.SkillPackageRow{
					{Name: "sk-ignored", Source: "o/sk-ignored", Skills: []string{"a"}, Installed: true, PerAgentStatus: map[string]bool{"claude": true}},
				}, nil, nil)
				m.agentsIgnore.Skills = []string{"sk-ignored"}
				return m
			},
		},
		// skills/updatesAvailable: unreachable — skillPackageRowStatus
		// (agents_status.go) only ever returns agentsStatusOutOfSync or
		// agentsStatusInstalled, never agentsStatusUpdates.
		{
			name:    "mcp/installed",
			feature: agentsSectionMcp,
			build: func() Model {
				return agentsAllModel(nil, []app.McpServerRow{
					{Name: "mcp-installed", Transport: "stdio", Command: "mcp-installed-bin", PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusInstalled}},
				}, nil)
			},
		},
		{
			name:    "mcp/missing",
			feature: agentsSectionMcp,
			build: func() Model {
				return agentsAllModel(nil, []app.McpServerRow{
					{Name: "mcp-missing", Transport: "stdio", Command: "mcp-missing-bin", PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusMissing}},
				}, nil)
			},
		},
		{
			name:    "mcp/orphan",
			feature: agentsSectionMcp,
			build: func() Model {
				m := agentsAllModel(nil, nil, nil)
				m.mcpUnmanaged = map[string][]app.InstalledMcpServer{
					"claude-code": {{Name: "mcp-orphan", Transport: "stdio"}},
				}
				return m
			},
		},
		{
			name:    "mcp/ignored",
			feature: agentsSectionMcp,
			build: func() Model {
				m := agentsAllModel(nil, []app.McpServerRow{
					{Name: "mcp-ignored", Transport: "stdio", PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusInstalled}},
				}, nil)
				m.agentsIgnore.McpServers = []string{"mcp-ignored"}
				return m
			},
		},
		// mcp/updatesAvailable: unreachable — mcpAgentRowStatus (agents_status.go)
		// only ever returns agentsStatusOutOfSync or agentsStatusInstalled, never
		// agentsStatusUpdates (mcp has no version/sha drift tracked per-adapter).
		{
			name:    "plugin/installed",
			feature: agentsSectionPlugins,
			build: func() Model {
				return agentsAllModel(nil, nil, []app.PluginRow{
					{Name: "pl-installed", Marketplace: "acme", Version: "1.0.0", PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}},
				})
			},
		},
		{
			name:    "plugin/missing",
			feature: agentsSectionPlugins,
			build: func() Model {
				return agentsAllModel(nil, nil, []app.PluginRow{
					{Name: "pl-missing", Marketplace: "acme", PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusMissing}},
				})
			},
		},
		{
			name:    "plugin/orphan",
			feature: agentsSectionPlugins,
			build: func() Model {
				m := agentsAllModel(nil, nil, nil)
				m.pluginUnmanaged = map[string][]app.InstalledPlugin{
					"claude-code": {{Name: "pl-orphan", Marketplace: "acme"}},
				}
				return m
			},
		},
		{
			name:    "plugin/ignored",
			feature: agentsSectionPlugins,
			build: func() Model {
				m := agentsAllModel(nil, nil, []app.PluginRow{
					{Name: "pl-ignored", Marketplace: "acme", Version: "1.0.0", PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}},
				})
				m.agentsIgnore.Plugins = []string{"pl-ignored"}
				return m
			},
		},
		{
			name:    "plugin/updatesAvailable",
			feature: agentsSectionPlugins,
			build: func() Model {
				return agentsAllModel(nil, nil, []app.PluginRow{
					{Name: "pl-outdated", Marketplace: "acme", Version: "1.0.0", LatestVersion: "2.0.0", PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}},
				})
			},
		},
	}
}

// TestAgentsComposedPath_DetailLinesAndHintsPresentAcrossStateFeatureMatrix
// is the regression guard for the class of bug that escaped all prior rounds
// (orphan rows silently rendering zero detail lines and/or zero hints): for
// every reachable (state, feature) combination, the selected row must
// surface both at least one detail line and at least one hint. Unreachable
// combinations (skills/mcp updatesAvailable) are omitted with a comment
// above, mirroring parity_harness_test.go's documented-divergence style
// rather than silently skipping them.
func TestAgentsComposedPath_DetailLinesAndHintsPresentAcrossStateFeatureMatrix(t *testing.T) {
	for _, c := range agentsComposedCases() {
		t.Run(c.name, func(t *testing.T) {
			m := c.build()

			rows := agentsAllRowsList(m)
			var entry agentsAllRow
			var found bool
			for _, e := range rows {
				if e.feature == c.feature {
					entry = e
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("combo %s: no %v row found in agentsAllRowsList", c.name, c.feature)
			}

			details := agentsRowDetailLines(m, entry)
			if len(details) < 1 {
				t.Errorf("combo %s (feature=%v status=%v mark=%v): agentsRowDetailLines returned %d lines, want >= 1",
					c.name, entry.feature, entry.status, entry.mark, len(details))
			}

			hints := agentsRowHints(m, entry)
			if len(hints) < 1 {
				t.Errorf("combo %s (feature=%v status=%v mark=%v): agentsRowHints returned %d hints, want >= 1",
					c.name, entry.feature, entry.status, entry.mark, len(hints))
			}
		})
	}
}
