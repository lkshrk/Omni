package tui

import (
	"testing"

	"github.com/lkshrk/omni/internal/app"
)

func TestAgentsMarkCell_IconAndStyleByState(t *testing.T) {
	pal := parityPalette()

	cases := []struct {
		name      string
		status    agentsRowStatus
		mark      agentsSyncMark
		wantIcon  string
		wantStyle string
	}{
		{"ignored", agentsStatusIgnored, agentsMarkNone, iconIgnored, "styleIgnored"},
		{"orphan", agentsStatusOutOfSync, agentsMarkOrphan, iconOrphan, "styleOrphan"},
		{"updatesAvailable", agentsStatusUpdates, agentsMarkNone, iconOutdated, "styleOutdated"},
		{"missing", agentsStatusOutOfSync, agentsMarkMissing, iconMissing, "styleMissing"},
		{"installed", agentsStatusInstalled, agentsMarkNone, iconInstalled, "styleInstalled"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cell := agentsMarkCell(pal, c.status, c.mark, false)
			plain := stripANSIEscapeSequences(cell)
			if plain != c.wantIcon {
				t.Errorf("icon = %q, want %q", plain, c.wantIcon)
			}
			requireRenderedStyle(t, pal, c.wantIcon, cell, c.wantStyle)
		})
	}
}

// agentsIconEndToEndCase describes one (state, feature) combination expected
// to be reachable through agentsAllRowsList -> agentsRowCells in production.
type agentsIconEndToEndCase struct {
	name      string
	feature   agentsSection
	wantIcon  string
	wantStyle string
	build     func() Model
}

func agentsIconEndToEndCases() []agentsIconEndToEndCase {
	return []agentsIconEndToEndCase{
		{
			name:      "skills/ignored",
			feature:   agentsSectionSkills,
			wantIcon:  iconIgnored,
			wantStyle: "styleIgnored",
			build: func() Model {
				m := agentsAllModel([]app.SkillPackageRow{
					{Name: "sk-ignored", Source: "o/sk-ignored", Installed: true, PerAgentStatus: map[string]bool{"claude": true}},
				}, nil, nil)
				m.agentsIgnore.Skills = []string{"sk-ignored"}
				return m
			},
		},
		{
			name:      "skills/orphan",
			feature:   agentsSectionSkills,
			wantIcon:  iconOrphan,
			wantStyle: "styleOrphan",
			build: func() Model {
				m := agentsAllModel(nil, nil, nil)
				m.skillsUnmanagedRows = []app.SkillPackageRow{
					{Name: "sk-orphan", Source: "o/sk-orphan"},
				}
				return m
			},
		},
		// skills/updatesAvailable: unreachable — skillPackageRowStatus (agents_status.go)
		// only ever returns agentsStatusOutOfSync or agentsStatusInstalled.
		{
			name:      "skills/missing",
			feature:   agentsSectionSkills,
			wantIcon:  iconMissing,
			wantStyle: "styleMissing",
			build: func() Model {
				return agentsAllModel([]app.SkillPackageRow{
					{Name: "sk-missing", Source: "o/sk-missing", Installed: false},
				}, nil, nil)
			},
		},
		{
			name:      "skills/installed",
			feature:   agentsSectionSkills,
			wantIcon:  iconInstalled,
			wantStyle: "styleInstalled",
			build: func() Model {
				return agentsAllModel([]app.SkillPackageRow{
					{Name: "sk-installed", Source: "o/sk-installed", Installed: true, PerAgentStatus: map[string]bool{"claude": true}},
				}, nil, nil)
			},
		},
		{
			name:      "mcp/ignored",
			feature:   agentsSectionMcp,
			wantIcon:  iconIgnored,
			wantStyle: "styleIgnored",
			build: func() Model {
				m := agentsAllModel(nil, []app.McpServerRow{
					{Name: "mcp-ignored", Transport: "stdio", PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusInstalled}},
				}, nil)
				m.agentsIgnore.McpServers = []string{"mcp-ignored"}
				return m
			},
		},
		{
			name:      "mcp/orphan",
			feature:   agentsSectionMcp,
			wantIcon:  iconOrphan,
			wantStyle: "styleOrphan",
			build: func() Model {
				m := agentsAllModel(nil, nil, nil)
				m.mcpUnmanaged = map[string][]app.InstalledMcpServer{
					"claude-code": {{Name: "mcp-orphan", Transport: "stdio"}},
				}
				return m
			},
		},
		// mcp/updatesAvailable: unreachable — mcpAgentRowStatus (agents_status.go)
		// only ever returns agentsStatusOutOfSync or agentsStatusInstalled.
		{
			name:      "mcp/missing",
			feature:   agentsSectionMcp,
			wantIcon:  iconMissing,
			wantStyle: "styleMissing",
			build: func() Model {
				return agentsAllModel(nil, []app.McpServerRow{
					{Name: "mcp-missing", Transport: "stdio", PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusMissing}},
				}, nil)
			},
		},
		{
			name:      "mcp/installed",
			feature:   agentsSectionMcp,
			wantIcon:  iconInstalled,
			wantStyle: "styleInstalled",
			build: func() Model {
				return agentsAllModel(nil, []app.McpServerRow{
					{Name: "mcp-installed", Transport: "stdio", PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusInstalled}},
				}, nil)
			},
		},
		{
			name:      "plugin/ignored",
			feature:   agentsSectionPlugins,
			wantIcon:  iconIgnored,
			wantStyle: "styleIgnored",
			build: func() Model {
				m := agentsAllModel(nil, nil, []app.PluginRow{
					{Name: "pl-ignored", Marketplace: "acme", PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}},
				})
				m.agentsIgnore.Plugins = []string{"pl-ignored"}
				return m
			},
		},
		{
			name:      "plugin/orphan",
			feature:   agentsSectionPlugins,
			wantIcon:  iconOrphan,
			wantStyle: "styleOrphan",
			build: func() Model {
				m := agentsAllModel(nil, nil, nil)
				m.pluginUnmanaged = map[string][]app.InstalledPlugin{
					"claude-code": {{Name: "pl-orphan", Marketplace: "acme"}},
				}
				return m
			},
		},
		{
			name:      "plugin/updatesAvailable",
			feature:   agentsSectionPlugins,
			wantIcon:  iconOutdated,
			wantStyle: "styleOutdated",
			build: func() Model {
				return agentsAllModel(nil, nil, []app.PluginRow{
					{Name: "pl-outdated", Marketplace: "acme", Version: "1.0.0", LatestVersion: "2.0.0", PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}},
				})
			},
		},
		{
			name:      "plugin/missing",
			feature:   agentsSectionPlugins,
			wantIcon:  iconMissing,
			wantStyle: "styleMissing",
			build: func() Model {
				return agentsAllModel(nil, nil, []app.PluginRow{
					{Name: "pl-missing", Marketplace: "acme", PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusMissing}},
				})
			},
		},
		{
			name:      "plugin/installed",
			feature:   agentsSectionPlugins,
			wantIcon:  iconInstalled,
			wantStyle: "styleInstalled",
			build: func() Model {
				return agentsAllModel(nil, nil, []app.PluginRow{
					{Name: "pl-installed", Marketplace: "acme", Version: "1.0.0", PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}},
				})
			},
		},
	}
}

// TestAgentsIconEndToEnd_StateFeatureMatrix exercises the real
// agentsAllRowsList -> agentsRowCells pipeline for every reachable
// (state, feature) combination and asserts the row's mark icon and style
// match the expected mapping, catching regressions the isolated
// agentsMarkCell unit test above cannot (e.g. a wrong status/mark computed
// upstream in agentsAllRowsList never reaching agentsMarkCell as expected).
func TestAgentsIconEndToEnd_StateFeatureMatrix(t *testing.T) {
	pal := parityPalette()

	for _, c := range agentsIconEndToEndCases() {
		t.Run(c.name, func(t *testing.T) {
			m := c.build()
			m.palette = pal

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
				t.Fatalf("no %v row found in agentsAllRowsList for case %s", c.feature, c.name)
			}

			cols := agentsColWidths(m, []agentsAllRow{entry})
			left, _ := agentsRowCells(m, pal, cols, entry, false)
			if len(left) == 0 {
				t.Fatalf("agentsRowCells returned no left cells for case %s", c.name)
			}
			rendered := left[0].text

			plain := stripANSIEscapeSequences(rendered)
			if len(plain) == 0 || string([]rune(plain)[0]) != c.wantIcon {
				t.Fatalf("case %s: rendered left cell %q does not start with icon %q", c.name, plain, c.wantIcon)
			}

			iconRendered := agentsMarkCell(pal, entry.status, entry.mark, false)
			requireRenderedStyle(t, pal, c.wantIcon, iconRendered, c.wantStyle)
		})
	}
}
