package tui

import (
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
)

func TestAgentsLayout_FixedGridColumnsAlignAcrossMixedRows(t *testing.T) {
	m := agentsAllModel(
		[]app.SkillPackageRow{
			{Name: "widget-dated", Source: "a/a", Installed: true, Updated: "2026-01-02", PerAgentStatus: map[string]bool{"claude": true}},
		},
		[]app.McpServerRow{
			{Name: "widget-server", Transport: "stdio", PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusInstalled}},
		},
		[]app.PluginRow{
			{Name: "widget-grouped", Marketplace: "acme", Groups: []string{"g1"}, PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}},
		},
	)
	m.width = 120

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	lines := strings.Split(out, "\n")

	var datedLine, serverLine, groupedLine string
	for _, l := range lines {
		switch {
		case strings.Contains(l, "widget-dated"):
			datedLine = l
		case strings.Contains(l, "widget-server"):
			serverLine = l
		case strings.Contains(l, "widget-grouped"):
			groupedLine = l
		}
	}
	if datedLine == "" || serverLine == "" || groupedLine == "" {
		t.Fatalf("expected all three rows present, got:\n%s", out)
	}

	agentIdxDated := strings.LastIndex(datedLine, "claude")
	agentIdxServer := strings.LastIndex(serverLine, "claude")
	agentIdxGrouped := strings.LastIndex(groupedLine, "claude")
	if agentIdxDated < 0 || agentIdxServer < 0 || agentIdxGrouped < 0 {
		t.Fatalf("expected 'claude' agent label on all three rows, got dated=%q server=%q grouped=%q", datedLine, serverLine, groupedLine)
	}
	if agentIdxDated != agentIdxServer || agentIdxServer != agentIdxGrouped {
		t.Errorf("agent column start offset differs across rows: dated=%d server=%d grouped=%d\ndated:   %q\nserver:  %q\ngrouped: %q",
			agentIdxDated, agentIdxServer, agentIdxGrouped, datedLine, serverLine, groupedLine)
	}
}

func TestAgentsLayout_MissingMarkRendersStyledMissingVersion(t *testing.T) {
	m := agentsAllModel(
		[]app.SkillPackageRow{{Name: "absent-skill", Source: "a/a", Installed: false}},
		nil, nil,
	)
	p := m.palette
	rows := agentsAllRowsList(m)
	cols := agentsColWidths(m, rows)

	var missingEntry agentsAllRow
	var found bool
	for _, e := range rows {
		if e.mark == agentsMarkMissing {
			missingEntry = e
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected a row with agentsMarkMissing")
	}

	_, right := agentsRowCells(m, p, cols, missingEntry, false)
	if len(right) < 3 {
		t.Fatalf("expected at least 3 right cells, got %+v", right)
	}
	verCell := right[2]
	if stripANSIEscapeSequences(verCell.text) != "missing" {
		t.Errorf("missing row version cell = %q, want stripped text %q", verCell.text, "missing")
	}
	want := p.styleMissing.Render("missing")
	if verCell.text != want {
		t.Errorf("missing row version cell styling = %q, want %q", verCell.text, want)
	}
}

func TestAgentsLayout_ProvColumnNeverShrinksBelowAgentIDFloor(t *testing.T) {
	m := agentsAllModel(
		[]app.SkillPackageRow{{Name: "s", Source: "a/a", Installed: true}},
		nil, nil,
	)
	rows := agentsAllRowsList(m)
	cols := agentsColWidths(m, rows)

	if cols.prov < agentsAgentIDColFloor {
		t.Errorf("cols.prov = %d, want >= agentsAgentIDColFloor (%d)", cols.prov, agentsAgentIDColFloor)
	}
}

func TestAgentsLayout_SkillsOrphanAgentCellBlankButMcpPluginOrphansKeepAgentID(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	m.skillsUnmanagedRows = []app.SkillPackageRow{
		{Name: "owner/orphan-skill", Source: "owner/orphan-skill", Installed: true},
	}
	m.mcpUnmanaged = map[string][]app.InstalledMcpServer{
		"claude-code": {{Name: "orphan-mcp", Transport: "stdio"}},
	}
	m.pluginUnmanaged = map[string][]app.InstalledPlugin{
		"claude-code": {{Name: "orphan-plugin"}},
	}

	p := m.palette
	rows := agentsAllRowsList(m)
	cols := agentsColWidths(m, rows)

	var skillsOrphan, mcpOrphan, pluginOrphan agentsAllRow
	var haveSkills, haveMcp, havePlugin bool
	for _, e := range rows {
		if e.status == agentsStatusOutOfSync && e.mark == agentsMarkOrphan {
			switch e.feature {
			case agentsSectionSkills:
				skillsOrphan, haveSkills = e, true
			case agentsSectionMcp:
				mcpOrphan, haveMcp = e, true
			case agentsSectionPlugins:
				pluginOrphan, havePlugin = e, true
			}
		}
	}
	if !haveSkills || !haveMcp || !havePlugin {
		t.Fatalf("expected orphan rows for all three features, got skills=%v mcp=%v plugin=%v", haveSkills, haveMcp, havePlugin)
	}

	_, rightSkills := agentsRowCells(m, p, cols, skillsOrphan, false)
	if got := strings.TrimSpace(stripANSIEscapeSequences(rightSkills[1].text)); got != "" {
		t.Errorf("skills orphan agent cell = %q, want blank", got)
	}
	if rightSkills[1].width != cols.prov {
		t.Errorf("skills orphan agent cell width = %d, want cols.prov = %d", rightSkills[1].width, cols.prov)
	}

	_, rightMcp := agentsRowCells(m, p, cols, mcpOrphan, false)
	if got := strings.TrimSpace(stripANSIEscapeSequences(rightMcp[1].text)); got != mcpOrphan.agentID {
		t.Errorf("mcp orphan agent cell = %q, want owner agent id %q", got, mcpOrphan.agentID)
	}

	_, rightPlugin := agentsRowCells(m, p, cols, pluginOrphan, false)
	if got := strings.TrimSpace(stripANSIEscapeSequences(rightPlugin[1].text)); got != pluginOrphan.agentID {
		t.Errorf("plugin orphan agent cell = %q, want owner agent id %q", got, pluginOrphan.agentID)
	}
}
