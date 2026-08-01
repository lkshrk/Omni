package tui

import (
	"context"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func TestAgentsGrouped_SectionOrderAndEmptySectionsOmitted(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(
		[]app.SkillPackageRow{
			{Name: "skill-ok", Source: "a/a", Installed: true, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled}},
			{Name: "skill-missing", Source: "b/b", Installed: false, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusMissing}},
		},
		nil,
		[]app.PluginRow{
			{Name: "plugin-updates", Marketplace: "acme", Version: "1.0.0", LatestVersion: "2.0.0", PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}},
		},
	)
	m.skillFindResults = []app.FindResult{{Source: "owner/find-me", Skill: "find-me"}}

	out := stripANSIEscapeSequences(m.viewSkillsBody())

	updatesIdx := strings.Index(out, "Updates Available")
	outOfSyncIdx := strings.Index(out, "Out of Sync")
	installedIdx := strings.Index(out, "Installed")
	availableIdx := strings.Index(out[updatesIdx+len("Updates Available"):], "Available")
	if availableIdx >= 0 {
		availableIdx += updatesIdx + len("Updates Available")
	}

	if updatesIdx < 0 || outOfSyncIdx < 0 || installedIdx < 0 || availableIdx < 0 {
		t.Fatalf("expected all four sections present, got updates=%d outOfSync=%d installed=%d available=%d in:\n%s",
			updatesIdx, outOfSyncIdx, installedIdx, availableIdx, out)
	}
	if !(updatesIdx < outOfSyncIdx && outOfSyncIdx < installedIdx && installedIdx < availableIdx) {
		t.Fatalf("expected order Updates Available < Out of Sync < Installed < Available, got %d, %d, %d, %d",
			updatesIdx, outOfSyncIdx, installedIdx, availableIdx)
	}
}

func TestAgentsGrouped_PluginOutdatedRowUnderUpdatesWithArrow(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, nil, []app.PluginRow{
		{Name: "old-plugin", Marketplace: "acme", Version: "1.0.0", LatestVersion: "2.0.0", PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}},
	})

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	if !strings.Contains(out, "Updates Available") {
		t.Fatalf("expected 'Updates Available' section, got:\n%s", out)
	}
	if !strings.Contains(out, "1.0.0") || !strings.Contains(out, "2.0.0") {
		t.Errorf("expected both old and new version in output, got:\n%s", out)
	}
	if strings.Contains(out, "Out of Sync") {
		t.Errorf("outdated-but-installed plugin should not appear under Out of Sync, got:\n%s", out)
	}
}

func TestAgentsGrouped_PluginUpToDateRowUnderInstalledNoArrow(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, nil, []app.PluginRow{
		{Name: "current-plugin", Marketplace: "acme", Version: "2.0.0", LatestVersion: "2.0.0", PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}},
	})

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	if strings.Contains(out, "Updates Available") {
		t.Fatalf("up-to-date plugin should not trigger an Updates Available section, got:\n%s", out)
	}
	if !strings.Contains(out, "Installed") {
		t.Fatalf("expected 'Installed' section, got:\n%s", out)
	}
	lines := strings.Split(out, "\n")
	for _, l := range lines {
		if strings.Contains(l, "current-plugin") && strings.Contains(l, "→") {
			t.Errorf("up-to-date plugin row should not show a version arrow, got: %q", l)
		}
	}
}

func TestAgentsGrouped_MissingRowsUseMissingMark(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(
		[]app.SkillPackageRow{{Name: "absent-skill", Source: "a/a", Installed: false}},
		nil, nil,
	)

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	lines := strings.Split(out, "\n")
	var rowLine string
	for _, l := range lines {
		if strings.Contains(l, "absent-skill") {
			rowLine = l
			break
		}
	}
	if rowLine == "" {
		t.Fatal("could not find row for absent-skill")
	}
	if !strings.Contains(rowLine, iconMissing) {
		t.Errorf("missing skill row should use mark %q, got: %q", iconMissing, rowLine)
	}
}

func TestAgentsGrouped_UnmanagedRowsUseOrphanMarkDistinctFromMissing(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, nil, nil)
	m.mcpUnmanaged = map[string][]app.InstalledMcpServer{
		"claude-code": {{Name: "unmanaged-mcp", Transport: "stdio"}},
	}

	rows := agentsAllRowsList(m)
	var found bool
	for _, r := range rows {
		if r.feature == agentsSectionMcp {
			found = true
			if r.mark != agentsMarkOrphan {
				t.Errorf("unmanaged mcp row mark = %v, want agentsMarkOrphan", r.mark)
			}
			if r.mark == agentsMarkMissing {
				t.Error("orphan mark should differ from missing mark")
			}
		}
	}
	if !found {
		t.Fatal("expected an mcp row for the unmanaged server")
	}
}

func TestAgentsProvCellText_OrphanSkillsRowMatchesManagedRowLinkageSummary(t *testing.T) {
	t.Parallel()
	status := map[string]app.SkillStatus{"claude": app.SkillStatusInstalled}
	managed := agentsAllModel([]app.SkillPackageRow{
		{Name: "shared-skill", Source: "a/a", Installed: true, PerAgentStatus: status},
	}, nil, nil)
	managedRows := agentsAllRowsList(managed)
	if len(managedRows) != 1 {
		t.Fatalf("expected 1 managed row, got %d", len(managedRows))
	}
	managedCell := agentsProvCellText(managed, managedRows[0])
	if managedCell == "" {
		t.Fatal("managed row linkage summary should not be blank")
	}

	orphan := agentsAllModel(nil, nil, nil)
	orphan.skillsUnmanagedRows = []app.SkillPackageRow{
		{Name: "shared-skill", Source: "a/a", Installed: true, PerAgentStatus: status},
	}
	orphanRows := agentsAllRowsList(orphan)
	if len(orphanRows) != 1 {
		t.Fatalf("expected 1 orphan row, got %d", len(orphanRows))
	}
	if orphanRows[0].mark != agentsMarkOrphan {
		t.Fatalf("expected orphan row mark, got %v", orphanRows[0].mark)
	}
	orphanCell := agentsProvCellText(orphan, orphanRows[0])

	if orphanCell != managedCell {
		t.Errorf("orphan linkage summary = %q, want same as managed row %q", orphanCell, managedCell)
	}
	if orphanCell == "" {
		t.Error("orphan skills row should show linkage summary, not blank")
	}
}

func TestAgentsRowCells_OrphanSkillsRowShowsLinkageInAgentColumn(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, nil, nil)
	m.skillsUnmanagedRows = []app.SkillPackageRow{
		{Name: "orphan-skill", Source: "a/a", Installed: true, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled}},
	}
	rows := agentsAllRowsList(m)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	p := defaultPalette()
	cols := agentsColWidths(m, rows)
	_, right := agentsRowCells(m, p, cols, rows[0], false)
	if len(right) < 2 {
		t.Fatalf("expected agent column cell in right cells, got %+v", right)
	}
	agentCell := stripANSIEscapeSequences(right[1].text)
	if strings.TrimSpace(agentCell) == "" {
		t.Errorf("orphan skills row agent-column cell should not be blank, got %q", agentCell)
	}
}

func TestAgentsGrouped_SkillVersionSlotShowsUpdatedDate(t *testing.T) {
	t.Parallel()
	m := agentsAllModel([]app.SkillPackageRow{
		{Name: "dated-skill", Source: "a/a", Installed: true, Updated: "2026-01-02", PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled}},
	}, nil, nil)

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	if !strings.Contains(out, "2026-01-02") {
		t.Errorf("expected skill row to show Updated date in version slot, got:\n%s", out)
	}
}

func TestAgentsGrouped_McpVersionSlotBlank(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, []app.McpServerRow{{Name: "srv-a", Transport: "stdio"}}, nil)

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	lines := strings.Split(out, "\n")
	for _, l := range lines {
		if strings.Contains(l, "srv-a") {
			trimmed := strings.TrimRight(l, " ")
			if strings.Contains(trimmed, "-") && strings.Contains(trimmed, "mcp") {
				continue
			}
		}
	}
	// A version string would have to come from somewhere other than mcpRowStatus, which never populates one for mcp rows.
	if !strings.Contains(out, "mcp") {
		t.Fatalf("expected type column label 'mcp' for mcp row, got:\n%s", out)
	}
}

// mcp/plugin rows show the exact per-agent id, while a package-level skills row shows its PerAgentStatus linkage summary.
func TestAgentsGrouped_AgentColumnLabelsExact(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(
		[]app.SkillPackageRow{{Name: "s", Source: "a/a", Installed: true, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled}}},
		[]app.McpServerRow{{Name: "m", Transport: "stdio", PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusInstalled}}},
		[]app.PluginRow{{Name: "p", Marketplace: "acme", PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}}},
	)
	rows := agentsAllRowsList(m)
	cols := agentsColWidths(m, rows)
	p := m.palette
	seen := map[agentsSection]bool{}
	for _, e := range rows {
		_, right := agentsRowCells(m, p, cols, e, false)
		agentCell := stripANSIEscapeSequences(right[1].text)
		if strings.TrimSpace(agentCell) != "claude" {
			t.Errorf("feature %v agent cell = %q, want %q", e.feature, strings.TrimSpace(agentCell), "claude")
		}
		seen[e.feature] = true
	}
	for _, f := range []agentsSection{agentsSectionSkills, agentsSectionMcp, agentsSectionPlugins} {
		if !seen[f] {
			t.Errorf("expected to see a row for feature %v", f)
		}
	}
}

func TestAgentsGrouped_ChipFilterGroupsByStatusWithinFeature(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(
		[]app.SkillPackageRow{
			{Name: "installed-skill", Source: "a/a", Installed: true, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled}},
			{Name: "missing-skill", Source: "b/b", Installed: false, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusMissing}},
		},
		[]app.McpServerRow{{Name: "mcp-a", PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusInstalled}}},
		nil,
	)
	m.skillTypeIdx = agentsChipSkills
	m.skillFindResults = []app.FindResult{{Source: "owner/find-me", Skill: "find-me"}}

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	if strings.Contains(out, "mcp-a") {
		t.Fatalf("skills chip should not render mcp rows, got:\n%s", out)
	}

	outOfSyncIdx := strings.Index(out, "Out of Sync")
	installedIdx := strings.Index(out, "Installed")
	availableIdx := strings.Index(out, "Available")
	missingRowIdx := strings.Index(out, "missing-skill")
	installedRowIdx := strings.Index(out, "installed-skill")
	findRowIdx := strings.Index(out, "find-me")

	if outOfSyncIdx < 0 || installedIdx < 0 || availableIdx < 0 {
		t.Fatalf("expected all three sections present on the skills chip, got:\n%s", out)
	}
	if !(missingRowIdx > outOfSyncIdx && missingRowIdx < installedIdx) {
		t.Errorf("missing-skill should render under Out of Sync, got idx=%d (OutOfSync=%d, Installed=%d)", missingRowIdx, outOfSyncIdx, installedIdx)
	}
	if !(installedRowIdx > installedIdx && installedRowIdx < availableIdx) {
		t.Errorf("installed-skill should render under Installed, got idx=%d (Installed=%d, Available=%d)", installedRowIdx, installedIdx, availableIdx)
	}
	if findRowIdx < availableIdx {
		t.Errorf("find-me should render under Available, got idx=%d (Available=%d)", findRowIdx, availableIdx)
	}
}

func TestAgentsGrouped_CursorTraversalMovesAcrossFeatureBoundariesWithinStatus(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(
		[]app.SkillPackageRow{{Name: "z-skill", Source: "a/a", Installed: true}},
		[]app.McpServerRow{{Name: "a-mcp", PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusInstalled}}},
		nil,
	)

	rows := agentsAllRowsList(m)
	if len(rows) != 2 {
		t.Fatalf("expected 2 installed rows, got %d", len(rows))
	}
	// Sort key is (status, feature, sortName): both rows share Installed, so feature order (skills before mcp) wins over name.
	if rows[0].feature != agentsSectionSkills || rows[1].feature != agentsSectionMcp {
		t.Fatalf("expected skills row before mcp row within the Installed status, got %+v", rows)
	}

	m.agentsAllCursor = 0
	got := drive(m, tea.KeyPressMsg{Code: tea.KeyDown})
	entry, ok := agentsAllEntryAt(got, got.agentsAllCursor)
	if !ok {
		t.Fatal("expected a valid row after moving down")
	}
	if entry.feature != agentsSectionMcp {
		t.Errorf("cursor should land on the mcp row after crossing the feature boundary, got feature=%v", entry.feature)
	}
}

func TestAgentsGrouped_ClaimUnmanagedSkillRowDispatchesAdopt(t *testing.T) {
	t.Parallel()
	a := newScanPlanTestApp(t)
	m := agentsAllModel(nil, nil, nil)
	m.app = a
	m.skillTypeIdx = agentsChipSkills
	m.skillsUnmanagedRows = []app.SkillPackageRow{
		{Name: "owner/unmanaged", Source: "owner/unmanaged", Installed: true},
	}
	m.skillsCursor = 0

	_, findStart, unmanagedStart := skillsVisibleRows(m)
	if unmanagedStart < 0 {
		t.Fatal("expected an unmanaged row to be present")
	}
	m.skillsCursor = unmanagedStart
	_ = findStart

	gotEnter, cmdEnter := m.Update(pressEnter())
	gotEnter2 := gotEnter.(Model)
	if gotEnter2.skillAddRunning {
		t.Fatal("expected Enter on an unmanaged skills row to be a no-op")
	}
	if cmdEnter != nil {
		t.Fatal("expected Enter on an unmanaged skills row to dispatch no command")
	}

	got, cmd := m.Update(pressRune('c'))
	got2 := got.(Model)
	if got2.mode != viewGroupPicker {
		t.Fatalf("expected 'c' on an unmanaged skills row to open the group picker, mode = %v", got2.mode)
	}
	if !got2.pickerPurposeClaim {
		t.Fatal("expected the opened picker to be in claim mode")
	}
	if cmd != nil {
		t.Fatal("expected opening the group picker to dispatch no command")
	}
}

func TestAgentsGrouped_EmptyGroupBadgeAbsentEvenWhenSelected(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, nil, []app.PluginRow{
		{Name: "grouped-plugin", Marketplace: "acme", Groups: []string{"g1"}, PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}},
		{Name: "ungrouped-plugin", Marketplace: "acme", PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}},
	})
	p := m.palette
	rows := agentsAllRowsList(m)
	cols := agentsColWidths(m, rows)

	var groupedEntry, ungroupedEntry agentsAllRow
	for _, e := range rows {
		r := m.pluginRows[e.localIdx]
		if r.Name == "grouped-plugin" {
			groupedEntry = e
		}
		if r.Name == "ungrouped-plugin" {
			ungroupedEntry = e
		}
	}

	// cols.group is non-zero, so every row must reserve a width-cols.group badge cell (empty when it has none) or the agent/version columns shift.
	for _, selected := range []bool{true, false} {
		_, right := agentsRowCells(m, p, cols, ungroupedEntry, selected)
		if len(right) != 4 {
			t.Errorf("no-groups row (selected=%v) right cells = %d, want 4 (width-reserved empty badge cell): %+v", selected, len(right), right)
		}
		badge := right[3]
		if badge.text != "" {
			t.Errorf("no-groups row (selected=%v) badge cell text = %q, want empty unstyled string", selected, badge.text)
		}
		if badge.width != cols.group {
			t.Errorf("no-groups row (selected=%v) badge cell width = %d, want cols.group = %d", selected, badge.width, cols.group)
		}
	}

	_, rightGrouped := agentsRowCells(m, p, cols, groupedEntry, false)
	if len(rightGrouped) != 4 {
		t.Fatalf("grouped row right cells = %d, want 4 (type, agent, version, badge): %+v", len(rightGrouped), rightGrouped)
	}
	if !strings.Contains(stripANSIEscapeSequences(rightGrouped[3].text), "[g1]") {
		t.Errorf("expected badge cell to contain [g1], got %q", rightGrouped[3].text)
	}
}

func TestAgentsGrouped_ColumnAlignmentUnaffectedBySelectionOnNoGroupsRow(t *testing.T) {
	t.Parallel(
	// Fixture name deliberately avoids "plugin"/"mcp"/"skills" so the type-column search cannot false-match inside the name cell.
	)

	m := agentsAllModel(nil, nil, []app.PluginRow{
		{Name: "widget-a", Marketplace: "acme", PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}},
	})
	p := m.palette
	rows := agentsAllRowsList(m)
	cols := agentsColWidths(m, rows)
	entry := rows[0]

	leftSel, rightSel := agentsRowCells(m, p, cols, entry, true)
	leftUnsel, rightUnsel := agentsRowCells(m, p, cols, entry, false)

	lineSel := renderSplitRow(leftSel, rightSel, rowAvailableWidth(m.width), listColumnGap, listColumnGap)
	lineUnsel := renderSplitRow(leftUnsel, rightUnsel, rowAvailableWidth(m.width), listColumnGap, listColumnGap)

	strippedSel := stripANSIEscapeSequences(lineSel)
	strippedUnsel := stripANSIEscapeSequences(lineUnsel)

	typeIdxSel := strings.LastIndex(strippedSel, "claude")
	typeIdxUnsel := strings.LastIndex(strippedUnsel, "claude")
	if typeIdxSel < 0 || typeIdxUnsel < 0 {
		t.Fatalf("expected 'claude' agent label in both renders, got selected=%q unselected=%q", strippedSel, strippedUnsel)
	}
	if typeIdxSel != typeIdxUnsel {
		t.Errorf("agent column start offset differs between selected (%d) and unselected (%d) no-groups rows: %q vs %q",
			typeIdxSel, typeIdxUnsel, strippedSel, strippedUnsel)
	}
}

func TestAgentsGrouped_ColumnAlignmentMatchesAcrossGroupsAndNoGroupsRows(t *testing.T) {
	t.Parallel(
	// Same-length names isolate the badge-cell effect from differing name lengths, and avoid "plugin"/"mcp"/"skills" so the LastIndex search cannot false-match in the name cell.
	)

	m := agentsAllModel(nil, nil, []app.PluginRow{
		{Name: "widget-aaaaaaaaaaaaaaaaaaa", Marketplace: "acme", Groups: []string{"g1"}, PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}},
		{Name: "widget-bbbbbbbbbbbbbbbbbbb", Marketplace: "acme", PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}},
	})
	p := m.palette
	rows := agentsAllRowsList(m)
	cols := agentsColWidths(m, rows)

	var groupedEntry, ungroupedEntry agentsAllRow
	for _, e := range rows {
		r := m.pluginRows[e.localIdx]
		if r.Name == "widget-aaaaaaaaaaaaaaaaaaa" {
			groupedEntry = e
		}
		if r.Name == "widget-bbbbbbbbbbbbbbbbbbb" {
			ungroupedEntry = e
		}
	}

	_, rightG := agentsRowCells(m, p, cols, groupedEntry, false)
	_, rightU := agentsRowCells(m, p, cols, ungroupedEntry, true)

	if len(rightG) != 4 {
		t.Fatalf("grouped row right cells = %d, want 4 (type, agent, version, badge): %+v", len(rightG), rightG)
	}
	if len(rightU) != 4 {
		t.Fatalf("ungrouped row right cells = %d, want 4 (type, agent, version, empty badge): %+v", len(rightU), rightU)
	}

	agentCellG := stripANSIEscapeSequences(renderCell(rightG[1]))
	agentCellU := stripANSIEscapeSequences(renderCell(rightU[1]))
	if agentCellG != agentCellU {
		t.Errorf("agent cell differs between grouped (%q) and ungrouped (%q) rows", agentCellG, agentCellU)
	}

	verCellG := stripANSIEscapeSequences(renderCell(rightG[2]))
	verCellU := stripANSIEscapeSequences(renderCell(rightU[2]))
	if verCellG != verCellU {
		t.Errorf("version cell differs between grouped (%q) and ungrouped (%q) rows", verCellG, verCellU)
	}

	lineG := renderSplitRow(nil, rightG, rowAvailableWidth(m.width), listColumnGap, listColumnGap)
	lineU := renderSplitRow(nil, rightU, rowAvailableWidth(m.width), listColumnGap, listColumnGap)
	strippedG := stripANSIEscapeSequences(lineG)
	strippedU := stripANSIEscapeSequences(lineU)
	typeIdxG := strings.Index(strippedG, "claude")
	typeIdxU := strings.Index(strippedU, "claude")
	if typeIdxG < 0 || typeIdxU < 0 {
		t.Fatalf("expected 'claude' agent label in both renders, got grouped=%q ungrouped=%q", strippedG, strippedU)
	}
	if typeIdxG != typeIdxU {
		t.Errorf("agent column start offset differs between grouped (%d) and ungrouped (%d) rows: %q vs %q",
			typeIdxG, typeIdxU, strippedG, strippedU)
	}
}

func TestAgentsRowDetailLines_SkillsRowShowsAllSkillsNoTruncation(t *testing.T) {
	t.Parallel()
	skillNames := []string{"s1", "s2", "s3", "s4", "s5", "s6", "s7", "s8", "s9", "s10", "s11", "s12"}
	agentIDs := []string{"a1", "a2", "a3", "a4", "a5", "a6"}
	perAgent := make(map[string]app.SkillStatus, len(agentIDs))
	for _, id := range agentIDs {
		perAgent[id] = app.SkillStatusInstalled
	}

	m := agentsAllModel([]app.SkillPackageRow{
		{
			Name: "skillpack", Source: "a/a", Installed: true,
			Skills:         skillNames,
			PerAgentStatus: perAgent,
		},
	}, nil, nil)
	m.enabledAgents = agentIDs

	rows := agentsAllRowsList(m)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	lines := agentsRowDetailLines(m, rows[0])

	var skillsLines, linkedLines []string
	for _, l := range lines {
		s := stripANSIEscapeSequences(l)
		if strings.Contains(s, "+") && strings.Contains(s, "more") {
			t.Errorf("expected no truncation marker in detail lines, got line %q", s)
		}
		if strings.Contains(s, "owner:") {
			t.Errorf("skills detail line should not contain owner: text, got %q", s)
		}
		trimmed := strings.TrimSpace(s)
		switch {
		case strings.HasPrefix(trimmed, "skills:") || strings.HasPrefix(trimmed, "s6,") || strings.HasPrefix(trimmed, "s11,"):
			skillsLines = append(skillsLines, s)
		case strings.HasPrefix(trimmed, "linked:") || strings.HasPrefix(trimmed, "a6"):
			linkedLines = append(linkedLines, s)
		}
	}

	combined := strings.Join(lines, "\n")
	stripped := stripANSIEscapeSequences(combined)
	for _, name := range skillNames {
		if !strings.Contains(stripped, name) {
			t.Errorf("expected skill name %q present in detail lines, got:\n%s", name, stripped)
		}
	}
	for _, id := range agentIDs {
		if !strings.Contains(stripped, id) {
			t.Errorf("expected linked agent %q present in detail lines, got:\n%s", id, stripped)
		}
	}

	// At width 120 the names and agent IDs fit one line each now that wrapping fills to the available width instead of a fixed 5-per-line chunk.
	if len(skillsLines) != 1 {
		t.Errorf("expected 1 skills detail line at width 120 (fill-to-width), got %d: %+v", len(skillsLines), skillsLines)
	}
	if len(linkedLines) != 1 {
		t.Errorf("expected 1 linked detail line at width 120 (fill-to-width), got %d: %+v", len(linkedLines), linkedLines)
	}
}

func TestWrapNamesLines_FillsToWidthWithHangIndent(t *testing.T) {
	t.Parallel()
	names := []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel", "india", "juliet"}

	t.Run("narrow width wraps across more lines with hang indent", func(t *testing.T) {
		m := agentsAllModel(nil, nil, nil)
		m.width = 80

		lines := wrapNamesLines(m, "skills: ", names)
		if len(lines) < 2 {
			t.Fatalf("expected multiple wrapped lines at narrow width, got %d: %+v", len(lines), lines)
		}
		indent := strings.Repeat(" ", len("skills: "))
		for i, l := range lines {
			if i == 0 {
				if !strings.HasPrefix(l, "skills: ") {
					t.Errorf("first line should start with the label, got %q", l)
				}
				continue
			}
			if !strings.HasPrefix(l, indent) {
				t.Errorf("continuation line %d should hang-indent to %q, got %q", i, indent, l)
			}
		}
		var reconstructed []string
		for _, l := range lines {
			trimmed := strings.TrimPrefix(strings.TrimPrefix(l, "skills: "), indent)
			reconstructed = append(reconstructed, strings.Split(trimmed, ", ")...)
		}
		if strings.Join(reconstructed, ",") != strings.Join(names, ",") {
			t.Errorf("names split across lines should reassemble to the original list in order with no name split mid-word:\ngot:  %v\nwant: %v", reconstructed, names)
		}
	})

	t.Run("wide width fits everything on one line", func(t *testing.T) {
		m := agentsAllModel(nil, nil, nil)
		m.width = 940

		lines := wrapNamesLines(m, "skills: ", names)
		if len(lines) != 1 {
			t.Fatalf("expected exactly 1 line at width 940, got %d: %+v", len(lines), lines)
		}
		for _, name := range names {
			if !strings.Contains(lines[0], name) {
				t.Errorf("expected name %q on the single wide line, got %q", name, lines[0])
			}
		}
	})
}

// A skill package symlinked into two agent dirs produces exactly one row, summarized as "2 agents", with both listed in the detail line.
func TestAgentsAll_SkillsFlatten_TwoAgentTargetsProduceOneRow(t *testing.T) {
	t.Parallel()
	m := agentsAllModel([]app.SkillPackageRow{
		{
			Name: "skillpack", Source: "a/a", Installed: true,
			Agents:         []string{"claude", "cursor"},
			PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled, "cursor": app.SkillStatusInstalled},
		},
	}, nil, nil)
	m.enabledAgents = []string{"claude", "cursor"}

	rows := agentsAllRowsList(m)
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 row for a 2-agent-targeting skill package, got %d: %+v", len(rows), rows)
	}
	if rows[0].agentID != "" {
		t.Fatalf("expected skills row agentID to be empty (package-level), got %q", rows[0].agentID)
	}

	cols := agentsColWidths(m, rows)
	_, right := agentsRowCells(m, m.palette, cols, rows[0], false)
	agentCell := strings.TrimSpace(stripANSIEscapeSequences(right[1].text))
	if agentCell != "2 agents" {
		t.Fatalf("agent cell = %q, want %q", agentCell, "2 agents")
	}

	lines := agentsRowDetailLines(m, rows[0])
	var linkedLine string
	for _, l := range lines {
		s := stripANSIEscapeSequences(l)
		if strings.Contains(s, "linked:") {
			linkedLine = s
		}
	}
	if linkedLine == "" {
		t.Fatalf("expected a linked: detail line, got %+v", lines)
	}
	if !strings.Contains(linkedLine, "claude") || !strings.Contains(linkedLine, "cursor") {
		t.Fatalf("expected linked: line to list both claude and cursor, got %q", linkedLine)
	}
}

func TestAgentsRowDetailLines_McpManagedRowShowsTransportAndCommand(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, []app.McpServerRow{
		{
			Name:           "mcp-a",
			Transport:      "stdio",
			Command:        "npx -y mcp-a",
			Agents:         []string{"claude"},
			PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusInstalled},
		},
	}, nil)
	rows := agentsAllRowsList(m)
	var mcpEntry agentsAllRow
	for _, e := range rows {
		if e.feature == agentsSectionMcp {
			mcpEntry = e
		}
	}

	lines := agentsRowDetailLines(m, mcpEntry)
	if len(lines) != 1 {
		t.Fatalf("expected exactly one detail line for managed mcp row, got %+v", lines)
	}
	got := stripANSIEscapeSequences(lines[0])
	if !strings.Contains(got, "transport: stdio") || !strings.Contains(got, "command: npx -y mcp-a") {
		t.Errorf("expected transport/command summary in mcp detail line, got %q", got)
	}
}

func TestAgentsRowDetailLines_SkillsOrphanIncludesLinkedLine(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, nil, nil)
	m.skillsUnmanagedRows = []app.SkillPackageRow{
		{Name: "orphan-skill", Source: "a/a", Installed: true, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled}},
	}
	rows := agentsAllRowsList(m)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	lines := agentsRowDetailLines(m, rows[0])
	var found bool
	for _, l := range lines {
		if strings.Contains(stripANSIEscapeSequences(l), "linked:") {
			found = true
			if !strings.Contains(stripANSIEscapeSequences(l), "claude") {
				t.Errorf("linked line = %q, want to contain claude", l)
			}
		}
	}
	if !found {
		t.Errorf("expected a linked: detail line for orphan skills row, got %+v", lines)
	}
}

func TestAgentsRowDetailLines_McpOrphanNoOwnerText(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, nil, nil)
	m.mcpUnmanaged = map[string][]app.InstalledMcpServer{
		"claude-code": {{Name: "orphan-mcp", Transport: "stdio", Command: "npx -y orphan-mcp"}},
	}
	rows := agentsAllRowsList(m)
	var mcpEntry agentsAllRow
	for _, e := range rows {
		if e.feature == agentsSectionMcp {
			mcpEntry = e
		}
	}

	lines := agentsRowDetailLines(m, mcpEntry)
	if len(lines) != 1 {
		t.Fatalf("expected exactly one detail line for unmanaged mcp row, got %+v", lines)
	}
	got := stripANSIEscapeSequences(lines[0])
	if strings.Contains(got, "owner:") {
		t.Errorf("detail line should not contain owner: text, got %q", got)
	}
	if !strings.Contains(got, "transport: stdio") {
		t.Errorf("expected transport: stdio in detail line, got %q", got)
	}
}

func TestAgentsRowDetailLines_PluginRowShowsMarketplaceAndVersion(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, nil, []app.PluginRow{
		{
			Name:           "plugin-a",
			Marketplace:    "acme",
			Version:        "1.2.3",
			Agents:         []string{"claude"},
			PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled},
		},
	})
	rows := agentsAllRowsList(m)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	lines := agentsRowDetailLines(m, rows[0])
	if len(lines) != 1 {
		t.Fatalf("expected exactly one detail line for managed plugin row, got %+v", lines)
	}
	got := stripANSIEscapeSequences(lines[0])
	if !strings.Contains(got, "marketplace: acme") || !strings.Contains(got, "version: 1.2.3") {
		t.Errorf("expected marketplace/version summary in plugin detail line, got %q", got)
	}
}

func TestAgentsRowDetailLines_DirectPluginShowsSource(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, nil, []app.PluginRow{{
		Name: "hermes-plugin", Source: "owner/repo", PerAgentStatus: map[string]app.PluginStatus{"hermes-agent": app.PluginStatusInstalled},
	}})
	m.enabledAgents = []string{"hermes-agent"}
	rows := agentsAllRowsList(m)
	if len(rows) != 1 {
		t.Fatalf("rows=%d", len(rows))
	}
	got := stripANSIEscapeSequences(agentsRowDetailLines(m, rows[0])[0])
	if !strings.Contains(got, "source: owner/repo") || strings.Contains(got, "marketplace:") {
		t.Fatalf("detail=%q", got)
	}
}

func TestAgentsRowDetailLines_UnmanagedRowShowsSummary(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, nil, nil)
	m.pluginUnmanaged = map[string][]app.InstalledPlugin{
		"claude-code": {{Name: "unmanaged-plugin", Marketplace: "some-marketplace", Version: "1.2.3"}},
	}
	rows := agentsAllRowsList(m)
	var unmanagedEntry agentsAllRow
	for _, e := range rows {
		if e.feature == agentsSectionPlugins {
			unmanagedEntry = e
		}
	}

	lines := agentsRowDetailLines(m, unmanagedEntry)
	if len(lines) != 1 {
		t.Fatalf("expected exactly one detail line for unmanaged plugin row, got %+v", lines)
	}
	got := stripANSIEscapeSequences(lines[0])
	if !strings.Contains(got, "marketplace: some-marketplace (unmanaged)") {
		t.Errorf("expected marketplace/(unmanaged) summary in detail line, got %q", got)
	}
	if !strings.Contains(got, "version: 1.2.3") {
		t.Errorf("expected version summary in detail line, got %q", got)
	}
}

func TestAgentsRowDetailLines_UnmanagedRowShaFallback(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, nil, nil)
	m.pluginUnmanaged = map[string][]app.InstalledPlugin{
		"claude-code": {{Name: "unmanaged-plugin", Marketplace: "some-marketplace", Sha: "abcdef1234567890"}},
	}
	rows := agentsAllRowsList(m)
	var unmanagedEntry agentsAllRow
	for _, e := range rows {
		if e.feature == agentsSectionPlugins {
			unmanagedEntry = e
		}
	}

	lines := agentsRowDetailLines(m, unmanagedEntry)
	if len(lines) != 1 {
		t.Fatalf("expected exactly one detail line for unmanaged plugin row, got %+v", lines)
	}
	got := stripANSIEscapeSequences(lines[0])
	if !strings.Contains(got, "sha: "+shaShort("abcdef1234567890")) {
		t.Errorf("expected sha summary in detail line, got %q", got)
	}
	if strings.Contains(got, "version:") {
		t.Errorf("expected no version in detail line when Version is empty, got %q", got)
	}
}

func TestAgentsRowDetailLines_MarketplaceManagedRowShowsSource(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, nil, nil)
	m.marketplaceRows = []app.MarketplaceRow{
		{
			Name:           "acme-market",
			Source:         "acme/marketplace-repo",
			Agents:         []string{"claude"},
			PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled},
		},
	}
	rows := agentsAllRowsList(m)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}

	lines := agentsRowDetailLines(m, rows[0])
	if len(lines) != 1 {
		t.Fatalf("expected exactly one detail line for managed marketplace row, got %+v", lines)
	}
	got := stripANSIEscapeSequences(lines[0])
	if !strings.Contains(got, "source: acme/marketplace-repo") {
		t.Errorf("expected source summary in marketplace detail line, got %q", got)
	}
	if strings.Contains(got, "(unmanaged)") {
		t.Errorf("managed marketplace row should not show (unmanaged), got %q", got)
	}
}

func TestAgentsRowDetailLines_MarketplaceUnmanagedRowShowsSourceUnmanaged(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, nil, nil)
	m.marketplaceUnmanaged = map[string][]app.InstalledMarketplace{
		"claude-code": {{Name: "orphan-market", Source: "orphan/marketplace-repo"}},
	}
	rows := agentsAllRowsList(m)
	var unmanagedEntry agentsAllRow
	for _, e := range rows {
		if e.feature == agentsSectionMarketplaces {
			unmanagedEntry = e
		}
	}

	lines := agentsRowDetailLines(m, unmanagedEntry)
	if len(lines) != 1 {
		t.Fatalf("expected exactly one detail line for unmanaged marketplace row, got %+v", lines)
	}
	got := stripANSIEscapeSequences(lines[0])
	if !strings.Contains(got, "source: orphan/marketplace-repo (unmanaged)") {
		t.Errorf("expected source/(unmanaged) summary in detail line, got %q", got)
	}
}

func TestAgentsRowDetailLines_UnselectedRowShowsNoDetails(t *testing.T) {
	t.Parallel()
	m := agentsAllModel([]app.SkillPackageRow{
		{Name: "skillpack-a", Source: "a/a", Installed: true, Skills: []string{"s1"}},
		{Name: "skillpack-b", Source: "b/b", Installed: true, Skills: []string{"s2"}},
	}, nil, nil)
	m.agentsAllCursor = 1

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	if strings.Contains(out, "skills: s1") {
		t.Fatalf("unselected row (cursor is on the second row) should not render detail lines for the first row, got:\n%s", out)
	}
	if !strings.Contains(out, "skills: s2") {
		t.Fatalf("selected row (second row) should render its detail line, got:\n%s", out)
	}
}

func TestAgentsGrouped_VersionWidthConsistencyAcrossAllAndPluginChip(t *testing.T) {
	t.Parallel()
	longPlugin := app.PluginRow{
		Name:           "long-version-plugin",
		Marketplace:    "acme",
		Version:        "1.2.3-alpha.long-prerelease-tag",
		LatestVersion:  "2.0.0-beta.another-long-tag",
		PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled},
	}

	mAll := agentsAllModel(nil, nil, []app.PluginRow{longPlugin})
	mAll.skillTypeIdx = agentsChipAll
	mAll.width = 120
	outAll := stripANSIEscapeSequences(mAll.viewSkillsBody())

	mPlugin := agentsAllModel(nil, nil, []app.PluginRow{longPlugin})
	mPlugin.skillTypeIdx = agentsChipPlugin
	mPlugin.width = 120
	outPlugin := stripANSIEscapeSequences(mPlugin.viewSkillsBody())

	if !strings.Contains(outAll, "→") {
		t.Errorf("expected all-chip render to show the version arrow, got:\n%s", outAll)
	}
	if !strings.Contains(outPlugin, "→") {
		t.Errorf("expected plugin-chip render to show the version arrow, got:\n%s", outPlugin)
	}
	rowLineAll := findLineContaining(outAll, "long-version-plugin")
	rowLinePlugin := findLineContaining(outPlugin, "long-version-plugin")
	if rowLineAll == "" || rowLinePlugin == "" {
		t.Fatalf("expected to find the plugin row line in both renders, got all=%q plugin=%q", rowLineAll, rowLinePlugin)
	}
	if rowLineAll != rowLinePlugin {
		t.Errorf("the plugin row itself is expected to truncate identically on both chips at width 120 (only the chip-bar header should differ):\nall:  %q\nplugin: %q", rowLineAll, rowLinePlugin)
	}
}

func TestFitUpgradeVersionText_ArrowVisibleWhenCurrentOverflows(t *testing.T) {
	t.Parallel()
	const width = 12
	current, latest := fitUpgradeVersionText("1.2.3-alpha.long-prerelease-tag", "2.0.0-beta.another-long-tag", width)

	if !strings.Contains(latest, "→") {
		t.Errorf("expected latest component to contain the arrow, got current=%q latest=%q", current, latest)
	}
	if total := lipgloss.Width(current) + lipgloss.Width(latest); total > width {
		t.Errorf("expected combined width <= %d, got %d (current=%q latest=%q)", width, total, current, latest)
	}
	trimmed := strings.TrimPrefix(latest, " → ")
	if trimmed == "" {
		t.Errorf("expected some part of the latest version to survive, got current=%q latest=%q", current, latest)
	}
}

func TestStyleForAgent_DeterministicForSameAgentID(t *testing.T) {
	t.Parallel()
	p := buildPaletteFor(true)

	s1 := styleForAgent(p, "claude-code")
	s2 := styleForAgent(p, "claude-code")

	if s1.Render("x") != s2.Render("x") {
		t.Errorf("styleForAgent not deterministic for same agentID: %q vs %q", s1.Render("x"), s2.Render("x"))
	}
}

func TestStyleForAgent_EmptyAgentIDReturnsStyleHelp(t *testing.T) {
	t.Parallel()
	p := buildPaletteFor(true)

	got := styleForAgent(p, "")
	want := p.styleHelp
	if got.Render("x") != want.Render("x") {
		t.Errorf("styleForAgent(p, \"\") = %q, want p.styleHelp render %q", got.Render("x"), want.Render("x"))
	}
}

func TestAgentsRowCells_McpAgentLabelUsesHuedStyleNotStyleHelp(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, []app.McpServerRow{
		{Name: "mcp-a", Transport: "stdio", PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusInstalled}},
	}, nil)
	m.palette = buildPaletteFor(true)
	p := m.palette

	rows := agentsAllRowsList(m)
	var mcpEntry agentsAllRow
	var found bool
	for _, e := range rows {
		if e.feature == agentsSectionMcp {
			mcpEntry = e
			found = true
		}
	}
	if !found {
		t.Fatal("expected an mcp row")
	}
	if mcpEntry.agentID != "claude" {
		t.Fatalf("expected mcp row agentID = %q, got %q", "claude", mcpEntry.agentID)
	}

	cols := agentsColWidths(m, rows)
	_, right := agentsRowCells(m, p, cols, mcpEntry, false)
	agentCell := right[1].text

	wantText := fitCellText(mcpEntry.agentID, cols.prov)
	want := styleForAgent(p, mcpEntry.agentID).Render(wantText)
	if agentCell != want {
		t.Errorf("mcp agent-label cell = %q, want %q (styleForAgent hue)", agentCell, want)
	}

	helpCode := ansiCodePrefix(p.styleHelp.Render("x"))
	if helpCode == "" {
		t.Fatal("test fixture invalid: p.styleHelp renders no ANSI code under buildPaletteFor(true)")
	}
	if strings.Contains(agentCell, helpCode) {
		t.Errorf("mcp agent-label cell still carries p.styleHelp's ANSI code %q, got %q", helpCode, agentCell)
	}
}

// A multi-agent linkage summary is not a literal agentID, so styleForAgent must leave it on the flat styleHelp.
func TestAgentsRowCells_SkillsMultiAgentSummaryKeepsStyleHelp(t *testing.T) {
	t.Parallel()
	m := agentsAllModel([]app.SkillPackageRow{
		{
			Name: "skillpack", Source: "a/a", Installed: true,
			Agents:         []string{"claude", "cursor"},
			PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled, "cursor": app.SkillStatusInstalled},
		},
	}, nil, nil)
	m.enabledAgents = []string{"claude", "cursor"}
	m.palette = buildPaletteFor(true)
	p := m.palette

	rows := agentsAllRowsList(m)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	entry := rows[0]
	if entry.agentID != "" {
		t.Fatalf("expected skills row agentID to be empty (package-level summary), got %q", entry.agentID)
	}

	cols := agentsColWidths(m, rows)
	_, right := agentsRowCells(m, p, cols, entry, false)
	agentCell := right[1].text

	agentLabel := strings.TrimSpace(stripANSIEscapeSequences(agentCell))
	if agentLabel != "2 agents" {
		t.Fatalf("agent cell = %q, want %q", agentLabel, "2 agents")
	}

	wantText := fitCellText("2 agents", cols.prov)
	want := p.styleHelp.Render(wantText)
	if agentCell != want {
		t.Errorf("skills multi-agent summary cell = %q, want p.styleHelp render %q", agentCell, want)
	}
}

func findLineContaining(s, substr string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, substr) {
			return l
		}
	}
	return ""
}

func runBatchCmd(cmd tea.Cmd) []tea.Msg {
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			if c == nil {
				continue
			}
			out = append(out, c())
		}
		return out
	}
	return []tea.Msg{msg}
}

func findSkillAddedMsg(msgs []tea.Msg) (skillAddedMsg, bool) {
	for _, m := range msgs {
		if added, ok := m.(skillAddedMsg); ok {
			return added, true
		}
	}
	return skillAddedMsg{}, false
}

func TestAgentsRowCells_InFlightOpShowsSpinnerInsteadOfMark(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(
		[]app.SkillPackageRow{{Name: "skill-ok", Source: "a/a", Installed: true, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled}}},
		nil,
		nil,
	)
	rows := agentsAllRowsList(m)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	row := rows[0]
	cols := agentsColWidths(m, rows)
	p := m.palette

	left, _ := agentsRowCells(m, p, cols, row, false)
	if !strings.Contains(left[0].text, iconInstalled) {
		t.Errorf("baseline mark cell = %q, want it to contain iconInstalled %q", left[0].text, iconInstalled)
	}

	m.agentsOpKey = agentsRowRunKey(row)
	left, _ = agentsRowCells(m, p, cols, row, false)
	spinner := rowSpinnerIcon(m)
	if !strings.Contains(left[0].text, spinner) {
		t.Errorf("in-flight mark cell = %q, want it to contain spinner glyph %q", left[0].text, spinner)
	}
	if strings.Contains(left[0].text, iconInstalled) {
		t.Errorf("in-flight mark cell = %q, should not contain normal mark icon %q", left[0].text, iconInstalled)
	}

	m.agentsOpKey = ""
	left, _ = agentsRowCells(m, p, cols, row, false)
	if !strings.Contains(left[0].text, iconInstalled) {
		t.Errorf("mark cell after clearing agentsOpKey = %q, want it to contain iconInstalled %q", left[0].text, iconInstalled)
	}

	m.agentsOpKey = "nonmatching\x00key\x00value"
	left, _ = agentsRowCells(m, p, cols, row, false)
	if !strings.Contains(left[0].text, iconInstalled) {
		t.Errorf("mark cell with non-matching agentsOpKey = %q, want it to contain iconInstalled %q", left[0].text, iconInstalled)
	}
}

// Filtered chips and the all chip must render byte-identical hint text for the same row state, so eligibility-blind hints cannot reappear on one path.
func TestAgentsGrouped_HintLineParityBetweenAllAndFilteredChip(t *testing.T) {
	t.Parallel()
	for _, feature := range []agentsSection{agentsSectionSkills, agentsSectionMcp, agentsSectionPlugins} {
		t.Run(feature.String(), func(t *testing.T) {
			m := agentsAllModel(
				[]app.SkillPackageRow{{Name: "ignored-skill", Source: "a/a", Installed: true, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled}}},
				[]app.McpServerRow{{Name: "ignored-mcp", Transport: "stdio", PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusInstalled}}},
				[]app.PluginRow{{Name: "ignored-plugin", Marketplace: "acme", Version: "1.0.0", PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}}},
			)
			m.agentsIgnore = config.AgentsIgnore{
				Skills:     []string{"ignored-skill"},
				McpServers: []string{"ignored-mcp"},
				Plugins:    []string{"ignored-plugin"},
			}
			m.width = 120

			var chip int
			switch feature {
			case agentsSectionSkills:
				chip = agentsChipSkills
				m.skillsCursor = 0
			case agentsSectionMcp:
				chip = agentsChipMcp
				m.mcpCursor = 0
			default:
				chip = agentsChipPlugin
				m.pluginCursor = 0
			}

			rows := agentsAllRowsList(m)
			flattenIdx := -1
			for i, e := range rows {
				if e.feature == feature {
					flattenIdx = i
					break
				}
			}
			if flattenIdx < 0 {
				t.Fatalf("expected a %s row in the flatten", feature)
			}

			mAll := m
			mAll.skillTypeIdx = agentsChipAll
			mAll.agentsAllCursor = flattenIdx
			outAll := stripANSIEscapeSequences(mAll.viewSkillsBody())

			mFiltered := m
			mFiltered.skillTypeIdx = chip
			outFiltered := stripANSIEscapeSequences(mFiltered.viewSkillsBody())

			hintKey := "x unignore"
			hintLineAll := findLineContaining(outAll, hintKey)
			hintLineFiltered := findLineContaining(outFiltered, hintKey)
			if hintLineAll == "" {
				t.Fatalf("all chip: expected to find hint line containing %q, got:\n%s", hintKey, outAll)
			}
			if hintLineFiltered == "" {
				t.Fatalf("filtered chip: expected to find hint line containing %q, got:\n%s", hintKey, outFiltered)
			}
			if hintLineAll != hintLineFiltered {
				t.Errorf("hint line must be identical between all and filtered chip for the same row state:\nall:      %q\nfiltered: %q", hintLineAll, hintLineFiltered)
			}
			if strings.Contains(hintLineAll, "install") || strings.Contains(hintLineFiltered, "install") {
				t.Errorf("an ignored row must not show 'install' on either chip, got all=%q filtered=%q", hintLineAll, hintLineFiltered)
			}
		})
	}
}

func (f agentsSection) String() string {
	switch f {
	case agentsSectionSkills:
		return "skills"
	case agentsSectionMcp:
		return "mcp"
	default:
		return "plugins"
	}
}

// One mcp item targeting two agents renders two rows sharing a localIdx; down must land on the second agent's row, not skip to another item.
func TestAgentsFilteredNav_McpChipDownMovesOneRenderedAgentRow(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(
		nil,
		[]app.McpServerRow{{
			Name:   "alpha-mcp",
			Agents: []string{"claude-code", "codex"},
			PerAgentStatus: map[string]app.McpStatus{
				"claude-code": app.McpStatusInstalled,
				"codex":       app.McpStatusInstalled,
			},
		}},
		nil,
	)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipMcp

	rows := agentsFilteredRowsList(m, agentsSectionMcp)
	if len(rows) != 2 {
		t.Fatalf("expected 2 expanded per-agent rows for the single mcp item, got %d: %+v", len(rows), rows)
	}
	if rows[0].localIdx != rows[1].localIdx {
		t.Fatalf("expected both rows to share the same localIdx (same manifest item), got %d and %d", rows[0].localIdx, rows[1].localIdx)
	}
	if rows[0].agentID != "claude-code" || rows[1].agentID != "codex" {
		t.Fatalf("expected sort order [claude-code, codex] by agentID within the item, got [%s, %s]", rows[0].agentID, rows[1].agentID)
	}

	m.mcpCursor = rows[0].localIdx
	m.mcpCursorAgentID = rows[0].agentID

	got := drive(m, tea.KeyPressMsg{Code: tea.KeyDown})

	if got.mcpCursor != rows[1].localIdx {
		t.Errorf("mcpCursor localIdx after down = %d, want %d (same item)", got.mcpCursor, rows[1].localIdx)
	}
	if got.mcpCursorAgentID != "codex" {
		t.Errorf("mcpCursorAgentID after down = %q, want %q (second agent row)", got.mcpCursorAgentID, "codex")
	}

	out := stripANSIEscapeSequences(renderAgentsGroupedTab(got, defaultPalette(), nil, agentsSectionMcp, true))
	codexLine := findLineContaining(out, "codex")
	claudeLine := findLineContaining(out, "claude-code")
	if codexLine == "" || claudeLine == "" {
		t.Fatalf("expected both agent rows to render, got:\n%s", out)
	}
	if !strings.Contains(codexLine, ">") {
		t.Errorf("expected the codex row to carry the selection marker after moving down, got line=%q\nfull:\n%s", codexLine, out)
	}
	if strings.Contains(claudeLine, ">") {
		t.Errorf("expected the claude-code row to NOT carry the selection marker after moving down, got line=%q\nfull:\n%s", claudeLine, out)
	}
}

// Up from an item's last expanded row moves to the previous row of the SAME item, not to an unrelated item.
func TestAgentsFilteredNav_McpChipUpFromLastRowReturnsToPriorAgentRowSameItem(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(
		nil,
		[]app.McpServerRow{{
			Name:   "alpha-mcp",
			Agents: []string{"claude-code", "codex"},
			PerAgentStatus: map[string]app.McpStatus{
				"claude-code": app.McpStatusInstalled,
				"codex":       app.McpStatusInstalled,
			},
		}},
		nil,
	)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipMcp

	rows := agentsFilteredRowsList(m, agentsSectionMcp)
	if len(rows) != 2 {
		t.Fatalf("expected 2 expanded per-agent rows, got %d: %+v", len(rows), rows)
	}

	m.mcpCursor = rows[1].localIdx
	m.mcpCursorAgentID = rows[1].agentID

	got := drive(m, tea.KeyPressMsg{Code: tea.KeyUp})

	if got.mcpCursor != rows[0].localIdx {
		t.Errorf("mcpCursor localIdx after up = %d, want %d (same item)", got.mcpCursor, rows[0].localIdx)
	}
	if got.mcpCursorAgentID != "claude-code" {
		t.Errorf("mcpCursorAgentID after up = %q, want %q (first agent row)", got.mcpCursorAgentID, "claude-code")
	}
}

// Mirrors the mcp case for the plugin chip, confirming the behavior is not mcp-specific.
func TestAgentsFilteredNav_PluginChipDownMovesOneRenderedAgentRow(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(
		nil,
		nil,
		[]app.PluginRow{{
			Name:    "linter-plugin",
			Agents:  []string{"claude-code", "codex"},
			Version: "1.0.0",
			PerAgentStatus: map[string]app.PluginStatus{
				"claude-code": app.PluginStatusInstalled,
				"codex":       app.PluginStatusInstalled,
			},
		}},
	)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipPlugin

	rows := agentsFilteredRowsList(m, agentsSectionPlugins)
	if len(rows) != 2 {
		t.Fatalf("expected 2 expanded per-agent rows for the single plugin item, got %d: %+v", len(rows), rows)
	}
	if rows[0].localIdx != rows[1].localIdx {
		t.Fatalf("expected both rows to share the same localIdx, got %d and %d", rows[0].localIdx, rows[1].localIdx)
	}
	if rows[0].agentID != "claude-code" || rows[1].agentID != "codex" {
		t.Fatalf("expected sort order [claude-code, codex] by agentID within the item, got [%s, %s]", rows[0].agentID, rows[1].agentID)
	}

	m.pluginCursor = rows[0].localIdx
	m.pluginCursorAgentID = rows[0].agentID

	got := drive(m, tea.KeyPressMsg{Code: tea.KeyDown})

	if got.pluginCursor != rows[1].localIdx {
		t.Errorf("pluginCursor localIdx after down = %d, want %d (same item)", got.pluginCursor, rows[1].localIdx)
	}
	if got.pluginCursorAgentID != "codex" {
		t.Errorf("pluginCursorAgentID after down = %q, want %q (second agent row)", got.pluginCursorAgentID, "codex")
	}

	out := stripANSIEscapeSequences(renderAgentsGroupedTab(got, defaultPalette(), nil, agentsSectionPlugins, true))
	codexLine := findLineContaining(out, "codex")
	claudeLine := findLineContaining(out, "claude-code")
	if codexLine == "" || claudeLine == "" {
		t.Fatalf("expected both agent rows to render, got:\n%s", out)
	}
	if !strings.Contains(codexLine, ">") {
		t.Errorf("expected the codex row to carry the selection marker after moving down, got line=%q\nfull:\n%s", codexLine, out)
	}
	if strings.Contains(claudeLine, ">") {
		t.Errorf("expected the claude-code row to NOT carry the selection marker after moving down, got line=%q\nfull:\n%s", claudeLine, out)
	}
}

// agentsChipMoveRow routes through the same filtered-flatten mechanism as mcp/plugin, so down visits rows in agentsFilteredRowsList order.
func TestAgentsGrouped_SkillsChipDownNavigationMatchesCaseInsensitiveRenderOrder(t *testing.T) {
	t.Parallel()
	m := agentsAllModel([]app.SkillPackageRow{
		{Name: "beta", Source: "o/beta", Installed: true, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled}},
		{Name: "Alpha", Source: "o/Alpha", Installed: true, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled}},
		{Name: "gamma", Source: "o/gamma", Installed: true, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled}},
		{Name: "Delta", Source: "o/Delta", Installed: false, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusMissing}},
		{Name: "echo", Source: "o/echo", Installed: false, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusMissing}},
	}, nil, nil)
	m.skillTypeIdx = agentsChipSkills
	m.skillsUnmanagedRows = []app.SkillPackageRow{
		{Name: "zulu-orphan", Source: "o/zulu-orphan", Installed: true},
		{Name: "Alpha-orphan", Source: "o/Alpha-orphan", Installed: true},
	}
	want := agentsFilteredRowsList(m, agentsSectionSkills)
	if len(want) != 7 {
		t.Fatalf("expected 7 flattened skills rows, got %d: %+v", len(want), want)
	}
	for i := 1; i < len(want); i++ {
		if want[i-1].status == want[i].status {
			if strings.ToLower(want[i-1].sortName) > strings.ToLower(want[i].sortName) {
				t.Fatalf("render order not case-insensitive-alphabetical within status group: %q before %q", want[i-1].sortName, want[i].sortName)
			}
		}
	}

	// delta=0 with an unresolved cursor lands directly on row 0, giving a deterministic starting point.
	m.skillsCursor = -1
	m.agentsChipMoveRow(agentsSectionSkills, 0)
	rows := agentsFilteredRowsList(m, agentsSectionSkills)
	pos := agentsChipRowPosition(rows, m.skillsCursor, "")
	if pos < 0 {
		t.Fatal("expected cursor to resolve to a row after landing on row 0")
	}
	if rows[pos].sortName != want[0].sortName {
		t.Errorf("initial landing = %q, want alphabetically-first row %q", rows[pos].sortName, want[0].sortName)
	}

	var visited []string
	for i := 0; i < len(want); i++ {
		rows := agentsFilteredRowsList(m, agentsSectionSkills)
		pos := agentsChipRowPosition(rows, m.skillsCursor, "")
		if pos < 0 {
			t.Fatalf("cursor did not resolve to a row at step %d", i)
		}
		visited = append(visited, rows[pos].sortName)
		m.agentsChipMoveRow(agentsSectionSkills, 1)
	}

	var wantNames []string
	for _, e := range want {
		wantNames = append(wantNames, e.sortName)
	}
	if strings.Join(visited, ",") != strings.Join(wantNames, ",") {
		t.Errorf("down-navigation visit order = %v, want render order %v", visited, wantNames)
	}
}

// Exercises the same path via the down key (handleSkillsKeyMsg) rather than calling agentsChipMoveRow directly.
func TestAgentsGrouped_SkillsDownKeyMatchesRenderOrder(t *testing.T) {
	t.Parallel()
	m := agentsAllModel([]app.SkillPackageRow{
		{Name: "Zebra", Source: "o/Zebra", Installed: true, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled}},
		{Name: "apple", Source: "o/apple", Installed: true, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled}},
	}, nil, nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills
	m.skillsCursor = 0

	got := drive(m, tea.KeyPressMsg{Code: tea.KeyDown})

	want := agentsFilteredRowsList(m, agentsSectionSkills)
	rows := agentsFilteredRowsList(got, agentsSectionSkills)
	pos := agentsChipRowPosition(rows, got.skillsCursor, "")
	if pos < 0 {
		t.Fatal("expected cursor to resolve to a row after down")
	}
	if rows[pos].sortName != want[0].sortName {
		t.Errorf("down key landed on %q, want alphabetically-first row %q", rows[pos].sortName, want[0].sortName)
	}
}

func TestAgentsRowClaim_SkillsOrphan_OpensGroupPickerWithoutAdopting(t *testing.T) {
	t.Parallel()
	a := newScanPlanTestApp(t)
	m := agentsAllModel(nil, nil, nil)
	m.app = a
	m.skillsUnmanagedRows = []app.SkillPackageRow{
		{Name: "owner/unmanaged", Source: "owner/unmanaged", Installed: true},
	}
	_, _, unmanagedStart := skillsVisibleRows(m)
	if unmanagedStart < 0 {
		t.Fatal("expected an unmanaged row")
	}
	e := agentsAllRow{feature: agentsSectionSkills, localIdx: unmanagedStart, mark: agentsMarkOrphan}

	handled, cmds := m.agentsRowClaim(e)

	if !handled {
		t.Fatal("expected agentsRowClaim to handle an orphan skills row")
	}
	if m.skillAddRunning {
		t.Error("claiming should not adopt immediately; skillAddRunning should stay false until confirm")
	}
	if len(cmds) != 0 {
		t.Errorf("expected no cmds dispatched before confirm, got %+v", cmds)
	}
	if m.mode != viewGroupPicker {
		t.Fatalf("expected mode = viewGroupPicker, got %v", m.mode)
	}
	if !m.pickerPurposeClaim {
		t.Error("expected pickerPurposeClaim = true")
	}
	if !m.pickerClaimAgentsSet || m.pickerClaimAgentsRow != e {
		t.Errorf("expected pickerClaimAgentsRow = %+v, got set=%v row=%+v", e, m.pickerClaimAgentsSet, m.pickerClaimAgentsRow)
	}
	if m.pickerMembershipKind != pickerMembershipSkill {
		t.Errorf("pickerMembershipKind = %q, want %q", m.pickerMembershipKind, pickerMembershipSkill)
	}
}

// Confirming the picker must both persist (SetMcpGroups) and reload (doLoadMcpRows) so the row's Groups reflects the change end to end.
func TestMcpGroupMembershipPicker_ConfirmPersistsAndReloadsRowGroups(t *testing.T) {
	t.Parallel()
	a := newScanPlanTestApp(t)
	if err := a.CreateGroup("work"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.AddMcpServer(context.Background(), config.McpServer{Name: "srv", Transport: "stdio", Command: "echo"}); err != nil {
		t.Fatal(err)
	}

	m := agentsAllModel(nil, []app.McpServerRow{{Name: "srv"}}, nil)
	m.app = a
	m.ctx = context.Background()
	m.mcpCursor = 0
	m.openMcpGroupMembershipPicker()
	if m.mode != viewGroupMembership {
		t.Fatalf("expected mode = viewGroupMembership, got %v", m.mode)
	}
	m.mcpMemberships["srv"] = []string{"work"}

	var cmds []tea.Cmd
	m.saveGroupMembershipPicker(&cmds)
	if len(cmds) != 1 {
		t.Fatalf("expected 1 cmd dispatched by saveGroupMembershipPicker, got %d", len(cmds))
	}

	msgs := runBatchCmd(cmds[0])
	final := m
	for _, msg := range msgs {
		var tm tea.Model = final
		tm, cmd := tm.Update(msg)
		final = tm.(Model)
		if cmd != nil {
			for _, follow := range runBatchCmd(cmd) {
				tm, _ = final.Update(follow)
				final = tm.(Model)
			}
		}
	}

	var got app.McpServerRow
	found := false
	for _, r := range final.mcpRows {
		if r.Name == "srv" {
			got, found = r, true
		}
	}
	if !found {
		t.Fatalf("expected reloaded mcpRows to contain srv, got %+v", final.mcpRows)
	}
	if len(got.Groups) != 1 || got.Groups[0] != "work" {
		t.Errorf("srv.Groups after confirm+reload = %v, want [work]", got.Groups)
	}
}

// Claiming an mcp server unmanaged under several agents must declare every one; both entries share Transport/Command/URL so the case stays on the union path rather than tripping mcpUnmanagedConflict.
func TestAgentsClaimGroupPicker_McpUnionsAllUnmanagedAgents(t *testing.T) {
	t.Parallel()
	a := newScanPlanTestApp(t)
	m := agentsAllModel(nil, nil, nil)
	m.app = a
	m.ctx = context.Background()
	m.mcpUnmanaged = map[string][]app.InstalledMcpServer{
		"claude": {{Name: "srv", Transport: "stdio", Command: "echo"}},
		"codex":  {{Name: "srv", Transport: "stdio", Command: "echo"}},
	}

	flat := mcpUnmanagedFlat(m.mcpUnmanaged)
	idx := -1
	for i, entry := range flat {
		if entry.agentID == "claude" && entry.srv.Name == "srv" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("expected a flattened unmanaged entry for claude/srv")
	}
	m.pickerPurposeClaim = true
	m.pickerClaimAgentsSet = true
	m.pickerClaimAgentsRow = agentsAllRow{
		feature:  agentsSectionMcp,
		localIdx: len(m.mcpRows) + idx,
		agentID:  "claude",
		mark:     agentsMarkOrphan,
	}

	var cmds []tea.Cmd
	if !m.runAgentsClaimGroupPickerAction("", &cmds) {
		t.Fatal("expected runAgentsClaimGroupPickerAction to report handled")
	}
	if len(cmds) == 0 {
		t.Fatal("expected at least one dispatched cmd")
	}

	final := m
	pending := cmds
	for len(pending) > 0 {
		cmd := pending[0]
		pending = pending[1:]
		if cmd == nil {
			continue
		}
		for _, msg := range runBatchCmd(cmd) {
			if adopted, ok := msg.(mcpImportAdoptDoneMsg); ok && adopted.err != nil {
				t.Fatalf("mcpImportAdoptDoneMsg.err = %v, want nil", adopted.err)
			}
			var tm tea.Model = final
			tm, next := tm.Update(msg)
			final = tm.(Model)
			if next != nil {
				pending = append(pending, next)
			}
		}
	}

	srv, ok, err := a.McpServerByName("srv")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected srv to be persisted in the manifest")
	}
	want := []string{"claude", "codex"}
	if !slices.Equal(srv.Agents, want) {
		t.Errorf("srv.Agents = %v, want %v", srv.Agents, want)
	}
}

func TestAgentsRowClaim_McpOrphan_OpensGroupPickerWithoutAdopting(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, nil, nil)
	m.mcpUnmanaged = map[string][]app.InstalledMcpServer{
		"codex": {{Name: "unmanaged-mcp", Transport: "stdio"}},
	}
	e := agentsAllRow{feature: agentsSectionMcp, localIdx: len(m.mcpRows), agentID: "codex", mark: agentsMarkOrphan}

	handled, cmds := m.agentsRowClaim(e)

	if !handled {
		t.Fatal("expected agentsRowClaim to handle an orphan mcp row")
	}
	if m.mcpRunning {
		t.Error("claiming should not adopt immediately; mcpRunning should stay false until confirm")
	}
	if len(cmds) != 0 {
		t.Errorf("expected no cmds dispatched before confirm, got %+v", cmds)
	}
	if m.mode != viewGroupPicker {
		t.Fatalf("expected mode = viewGroupPicker, got %v", m.mode)
	}
	if m.pickerMembershipKind != pickerMembershipMcp {
		t.Errorf("pickerMembershipKind = %q, want %q", m.pickerMembershipKind, pickerMembershipMcp)
	}
}

func TestAgentsRowClaim_PluginOrphan_OpensGroupPickerWithoutAdopting(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, nil, nil)
	m.pluginUnmanaged = map[string][]app.InstalledPlugin{
		"codex": {{Name: "unmanaged-plugin", Marketplace: "acme"}},
	}
	e := agentsAllRow{feature: agentsSectionPlugins, localIdx: len(m.pluginRows), agentID: "codex", mark: agentsMarkOrphan}

	handled, cmds := m.agentsRowClaim(e)

	if !handled {
		t.Fatal("expected agentsRowClaim to handle an orphan plugin row")
	}
	if m.pluginRunning {
		t.Error("claiming should not adopt immediately; pluginRunning should stay false until confirm")
	}
	if len(cmds) != 0 {
		t.Errorf("expected no cmds dispatched before confirm, got %+v", cmds)
	}
	if m.mode != viewGroupPicker {
		t.Fatalf("expected mode = viewGroupPicker, got %v", m.mode)
	}
	if m.pickerMembershipKind != pickerMembershipPlugin {
		t.Errorf("pickerMembershipKind = %q, want %q", m.pickerMembershipKind, pickerMembershipPlugin)
	}
}

func TestAgentsClaimGroupPicker_ConfirmAdoptsAndAssignsGroup(t *testing.T) {
	t.Parallel()
	a := newScanPlanTestApp(t)
	m := agentsAllModel(nil, nil, nil)
	m.app = a
	m.skillsUnmanagedRows = []app.SkillPackageRow{
		{Name: "owner/unmanaged", Source: "owner/unmanaged", Installed: true},
	}
	_, _, unmanagedStart := skillsVisibleRows(m)
	e := agentsAllRow{feature: agentsSectionSkills, localIdx: unmanagedStart, mark: agentsMarkOrphan}

	handled, _ := m.agentsRowClaim(e)
	if !handled {
		t.Fatal("expected agentsRowClaim to handle an orphan skills row")
	}
	if len(m.pickerGroups) == 0 {
		t.Fatal("expected at least one group option in the picker")
	}
	m.pickerCursor = 0

	var cmds []tea.Cmd
	m.confirmGroupPickerSelection(&cmds)

	if !m.skillAddRunning {
		t.Error("expected skillAddRunning=true after confirming claim")
	}
	if len(cmds) == 0 {
		t.Fatal("expected non-empty cmds after confirming claim")
	}
	if m.mode == viewGroupPicker {
		t.Error("expected picker to close on confirm")
	}
}

func TestAgentsClaimGroupPicker_CancelDoesNotAdopt(t *testing.T) {
	t.Parallel()
	a := newScanPlanTestApp(t)
	m := agentsAllModel(nil, nil, nil)
	m.app = a
	m.skillsUnmanagedRows = []app.SkillPackageRow{
		{Name: "owner/unmanaged", Source: "owner/unmanaged", Installed: true},
	}
	_, _, unmanagedStart := skillsVisibleRows(m)
	e := agentsAllRow{feature: agentsSectionSkills, localIdx: unmanagedStart, mark: agentsMarkOrphan}

	handled, _ := m.agentsRowClaim(e)
	if !handled {
		t.Fatal("expected agentsRowClaim to handle an orphan skills row")
	}

	m.cancelGroupPicker()

	if m.skillAddRunning {
		t.Error("expected skillAddRunning=false after canceling claim")
	}
	if m.mode == viewGroupPicker {
		t.Error("expected picker to close on cancel")
	}
	if m.pickerClaimAgentsSet {
		t.Error("expected pickerClaimAgentsSet cleared after cancel")
	}

	cfg, err := a.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range cfg.Agents.Packages {
		if pkg.Source == "owner/unmanaged" {
			t.Fatal("canceling the claim picker must not adopt the package into the manifest")
		}
	}
}

func TestAgentsClaimGroupPicker_OpenSetsClaimAgentsFlag(t *testing.T) {
	t.Parallel()
	a := newScanPlanTestApp(t)
	m := agentsAllModel(nil, nil, nil)
	m.app = a
	m.skillsUnmanagedRows = []app.SkillPackageRow{
		{Name: "owner/unmanaged", Source: "owner/unmanaged", Installed: true},
	}
	_, _, unmanagedStart := skillsVisibleRows(m)
	e := agentsAllRow{feature: agentsSectionSkills, localIdx: unmanagedStart, mark: agentsMarkOrphan}

	handled, _ := m.agentsRowClaim(e)
	if !handled {
		t.Fatal("expected agentsRowClaim to handle an orphan skills row")
	}
	if m.mode != viewGroupPicker {
		t.Fatalf("mode = %v, want viewGroupPicker", m.mode)
	}
	if !m.pickerClaimAgentsSet {
		t.Error("expected pickerClaimAgentsSet = true after opening claim picker from agents tab")
	}
}

func TestAgentsClaimGroupPicker_ConfirmReturnsToSkillsTab(t *testing.T) {
	t.Parallel()
	a := newScanPlanTestApp(t)
	m := agentsAllModel(nil, nil, nil)
	m.app = a
	m.skillsUnmanagedRows = []app.SkillPackageRow{
		{Name: "owner/unmanaged", Source: "owner/unmanaged", Installed: true},
	}
	_, _, unmanagedStart := skillsVisibleRows(m)
	e := agentsAllRow{feature: agentsSectionSkills, localIdx: unmanagedStart, mark: agentsMarkOrphan}

	handled, _ := m.agentsRowClaim(e)
	if !handled {
		t.Fatal("expected agentsRowClaim to handle an orphan skills row")
	}
	m.pickerCursor = 0

	var cmds []tea.Cmd
	m.confirmGroupPickerSelection(&cmds)

	if m.mode != viewSkills {
		t.Fatalf("mode after confirm = %v, want viewSkills", m.mode)
	}
}

func TestAgentsClaimGroupPicker_CancelReturnsToSkillsTab(t *testing.T) {
	t.Parallel()
	a := newScanPlanTestApp(t)
	m := agentsAllModel(nil, nil, nil)
	m.app = a
	m.skillsUnmanagedRows = []app.SkillPackageRow{
		{Name: "owner/unmanaged", Source: "owner/unmanaged", Installed: true},
	}
	_, _, unmanagedStart := skillsVisibleRows(m)
	e := agentsAllRow{feature: agentsSectionSkills, localIdx: unmanagedStart, mark: agentsMarkOrphan}

	handled, _ := m.agentsRowClaim(e)
	if !handled {
		t.Fatal("expected agentsRowClaim to handle an orphan skills row")
	}

	m.cancelGroupPicker()

	if m.mode != viewSkills {
		t.Fatalf("mode after cancel = %v, want viewSkills", m.mode)
	}
}

// Unit test of closeGroupPicker's wasAgentsClaim branch, isolated from the open flow.
func TestCloseGroupPicker_AgentsClaimModeRestoresSkills(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewGroupPicker
	m.pickerClaimAgentsSet = true

	m.closeGroupPicker()

	if m.mode != viewSkills {
		t.Fatalf("mode = %v, want viewSkills", m.mode)
	}
	if m.pickerClaimAgentsSet {
		t.Error("expected pickerClaimAgentsSet cleared after closeGroupPicker")
	}
}

func TestCloseGroupPicker_DefaultRestoresList(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewGroupPicker

	m.closeGroupPicker()

	if m.mode != viewList {
		t.Fatalf("mode = %v, want viewList", m.mode)
	}
}

func TestCloseGroupPicker_DotAddRestoresDots(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewGroupPicker
	m.pickerPurposeDotAdd = true

	m.closeGroupPicker()

	if m.mode != viewDots {
		t.Fatalf("mode = %v, want viewDots", m.mode)
	}
}

func TestAgentsRowHints_OrphanRow_ShowsClaimNotAdoptOrImport(t *testing.T) {
	t.Parallel()
	for _, feature := range []agentsSection{agentsSectionSkills, agentsSectionMcp, agentsSectionPlugins} {
		m := agentsAllModel(nil, nil, nil)
		e := agentsAllRow{feature: feature, mark: agentsMarkOrphan}

		hints := agentsRowHints(m, e)

		var keys, descs []string
		for _, h := range hints {
			keys = append(keys, h.key)
			descs = append(descs, h.desc)
		}
		joinedKeys := strings.Join(keys, ",")
		joinedDescs := strings.Join(descs, ",")

		if !strings.Contains(joinedKeys, "c") || !strings.Contains(joinedDescs, "claim") {
			t.Errorf("feature=%v: expected 'c'/'claim' hint, got keys=%v descs=%v", feature, keys, descs)
		}
		for _, h := range hints {
			if h.key == "enter" || h.desc == "adopt" {
				t.Errorf("feature=%v: unexpected enter/adopt hint on orphan row: %+v", feature, h)
			}
			if h.key == "i" && h.desc == "import" {
				t.Errorf("feature=%v: unexpected 'i'/'import' hint on orphan row: %+v", feature, h)
			}
		}
	}
}

func TestSkillsKeyMsg_OrphanRow_EnterNoop_CImports_IBulkImportUnaffected(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, nil, nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills
	m.skillsUnmanagedRows = []app.SkillPackageRow{
		{Name: "owner/unmanaged", Source: "owner/unmanaged", Installed: true},
	}
	_, _, unmanagedStart := skillsVisibleRows(m)
	if unmanagedStart < 0 {
		t.Fatal("expected an unmanaged row")
	}
	m.skillsCursor = unmanagedStart

	gotEnter := drive(m, pressEnter())
	if gotEnter.skillAddRunning {
		t.Error("expected enter on an orphan skills row to be a no-op")
	}

	gotClaim := drive(m, pressRune('c'))
	if gotClaim.mode != viewGroupPicker {
		t.Errorf("expected 'c' on an orphan skills row to open the group picker, mode = %v", gotClaim.mode)
	}

	gotBulkImport := drive(m, pressRune('i'))
	if !gotBulkImport.skillsRunning {
		t.Error("expected 'i' to still trigger the unrelated bulk import action (skillsRunning=true)")
	}
	if gotBulkImport.skillAddRunning {
		t.Error("expected 'i' bulk import to leave skillAddRunning untouched (false)")
	}
}

func TestSkillsKeyMsg_FindResultRow_EnterCallsAddSkillPackage(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, nil, nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills
	m.skillFindResults = []app.FindResult{{Source: "owner/found-skill", Skill: "found-skill"}}
	_, findStart, _ := skillsVisibleRows(m)
	if findStart < 0 {
		t.Fatal("expected a find-result row")
	}
	m.skillsCursor = findStart

	got, cmd := m.Update(pressEnter())
	got2 := got.(Model)

	if !got2.skillAddRunning {
		t.Error("expected skillAddRunning=true after enter on a find-result row")
	}
	if cmd == nil {
		t.Fatal("expected a non-nil command after enter on a find-result row")
	}
}

// An agent ID true in PerAgentStatus but absent from enabledAgents must be excluded.
func TestSkillLinkedAgents_FiltersToEnabledAgentsOnly(t *testing.T) {
	t.Parallel()
	r := app.SkillPackageRow{
		Name: "skillpack",
		PerAgentStatus: map[string]app.SkillStatus{
			"claude":   app.SkillStatusInstalled,
			"cursor":   app.SkillStatusInstalled,
			"excluded": app.SkillStatusInstalled,
		},
	}
	enabledAgents := []string{"claude", "cursor"}

	got := skillLinkedAgents(r, enabledAgents)

	for _, id := range []string{"claude", "cursor"} {
		found := false
		for _, g := range got {
			if g == id {
				found = true
			}
		}
		if !found {
			t.Errorf("expected %q present in skillLinkedAgents, got %v", id, got)
		}
	}
	for _, g := range got {
		if g == "excluded" {
			t.Errorf("expected 'excluded' agent (true in PerAgentStatus but not enabled) to be filtered out, got %v", got)
		}
	}
}

func TestAgentsVersionCellText_McpManagedRowShowsVersion(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, []app.McpServerRow{
		{
			Name:           "mcp-a",
			Version:        "1.2.3",
			Agents:         []string{"claude"},
			PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusInstalled},
		},
	}, nil)
	rows := agentsAllRowsList(m)
	var mcpEntry agentsAllRow
	for _, e := range rows {
		if e.feature == agentsSectionMcp {
			mcpEntry = e
		}
	}

	got := agentsVersionCellText(m, mcpEntry)
	if !strings.Contains(got, "1.2.3") {
		t.Errorf("agentsVersionCellText = %q, want it to contain %q", got, "1.2.3")
	}

	cols := agentsColWidths(m, rows)
	_, right := agentsRowCells(m, m.palette, cols, mcpEntry, false)
	verCell := stripANSIEscapeSequences(renderCell(right[2]))
	if !strings.Contains(verCell, "1.2.3") {
		t.Errorf("agentsRowCells version cell = %q, want it to contain %q", verCell, "1.2.3")
	}
}

func TestAgentsVersionCellText_McpManagedRowEmptyVersionBlank(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, []app.McpServerRow{
		{
			Name:           "mcp-b",
			Version:        "",
			Agents:         []string{"claude"},
			PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusInstalled},
		},
	}, nil)
	rows := agentsAllRowsList(m)
	var mcpEntry agentsAllRow
	for _, e := range rows {
		if e.feature == agentsSectionMcp {
			mcpEntry = e
		}
	}

	got := strings.TrimSpace(stripANSIEscapeSequences(agentsVersionCellText(m, mcpEntry)))
	if got == "missing" {
		t.Errorf("agentsVersionCellText = %q, want blank (not missing) for empty version on a non-missing row", got)
	}
	if strings.Contains(got, "1.2.3") {
		t.Errorf("agentsVersionCellText = %q, want no stray version text", got)
	}
}

func TestSkillDetailLines_WithDescriptionPrependsLine(t *testing.T) {
	t.Parallel()
	m := agentsAllModel([]app.SkillPackageRow{
		{
			Name:        "skillpack",
			Source:      "a/a",
			Description: "does a thing",
			Installed:   true,
			Skills:      []string{"s1", "s2"},
		},
	}, nil, nil)
	m.enabledAgents = []string{"claude"}

	rows := agentsAllRowsList(m)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	r := app.SkillPackageRow{Name: "skillpack", Source: "a/a", Description: "does a thing", Skills: []string{"s1", "s2"}}
	lines := skillDetailLines(m, r)
	if len(lines) < 1 {
		t.Fatalf("expected at least one detail line, got %+v", lines)
	}
	want := statusDetailLine(m, "does a thing")
	if lines[0] != want {
		t.Errorf("first line = %q, want %q", lines[0], want)
	}
	got := stripANSIEscapeSequences(lines[1])
	if !strings.Contains(got, "source: a/a") {
		t.Errorf("second line = %q, want source: a/a", got)
	}
}

func TestSkillDetailLines_WithoutDescriptionOmitsLine(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, nil, nil)
	r := app.SkillPackageRow{Name: "skillpack", Source: "a/a", Skills: []string{"s1"}}
	lines := skillDetailLines(m, r)
	if len(lines) < 1 {
		t.Fatalf("expected at least one detail line, got %+v", lines)
	}
	got := stripANSIEscapeSequences(lines[0])
	if !strings.Contains(got, "source: a/a") {
		t.Errorf("first line = %q, want source: a/a (no description line)", got)
	}
	for _, l := range lines {
		if strings.Contains(stripANSIEscapeSequences(l), "does a thing") {
			t.Errorf("did not expect description text in lines, got %+v", lines)
		}
	}
}

func TestSkillDetailLines_OrderPreservedWithDescription(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, nil, nil)
	m.enabledAgents = []string{"claude"}
	r := app.SkillPackageRow{
		Name:           "skillpack",
		Source:         "a/a",
		Description:    "desc line",
		Skills:         []string{"s1"},
		Agents:         []string{"claude"},
		PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled},
	}
	lines := skillDetailLines(m, r)
	var stripped []string
	for _, l := range lines {
		stripped = append(stripped, strings.TrimSpace(stripANSIEscapeSequences(l)))
	}
	if len(stripped) != 4 {
		t.Fatalf("expected 4 lines (description, source, skills, linked), got %d: %+v", len(stripped), stripped)
	}
	if !strings.Contains(stripped[0], "desc line") {
		t.Errorf("line 0 = %q, want description", stripped[0])
	}
	if !strings.HasPrefix(stripped[1], "source:") {
		t.Errorf("line 1 = %q, want source: prefix", stripped[1])
	}
	if !strings.HasPrefix(stripped[2], "skills:") {
		t.Errorf("line 2 = %q, want skills: prefix", stripped[2])
	}
	if !strings.HasPrefix(stripped[3], "linked:") {
		t.Errorf("line 3 = %q, want linked: prefix", stripped[3])
	}
}

func TestAgentsRowDetailLines_PluginRowWithDescriptionPrependsLine(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, nil, []app.PluginRow{
		{
			Name:           "plugin-a",
			Marketplace:    "acme",
			Version:        "1.2.3",
			Description:    "installs a thing",
			Agents:         []string{"claude"},
			PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled},
		},
	})
	rows := agentsAllRowsList(m)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	lines := agentsRowDetailLines(m, rows[0])
	if len(lines) != 2 {
		t.Fatalf("expected exactly two detail lines (description + summary), got %+v", lines)
	}
	descGot := stripANSIEscapeSequences(lines[0])
	if !strings.Contains(descGot, "installs a thing") {
		t.Errorf("expected description in first detail line, got %q", descGot)
	}
	summaryGot := stripANSIEscapeSequences(lines[1])
	if !strings.Contains(summaryGot, "marketplace: acme") || !strings.Contains(summaryGot, "version: 1.2.3") {
		t.Errorf("expected marketplace/version summary in second detail line, got %q", summaryGot)
	}
}

func TestAgentsRowDetailLines_PluginRowWithoutDescriptionShowsOnlySummary(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, nil, []app.PluginRow{
		{
			Name:           "plugin-a",
			Marketplace:    "acme",
			Version:        "1.2.3",
			Agents:         []string{"claude"},
			PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled},
		},
	})
	rows := agentsAllRowsList(m)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	lines := agentsRowDetailLines(m, rows[0])
	if len(lines) != 1 {
		t.Fatalf("expected exactly one detail line for managed plugin row without description, got %+v", lines)
	}
	got := stripANSIEscapeSequences(lines[0])
	if !strings.Contains(got, "marketplace: acme") || !strings.Contains(got, "version: 1.2.3") {
		t.Errorf("expected marketplace/version summary in plugin detail line, got %q", got)
	}
}

func TestAgentsRowDetailLines_UnmanagedPluginRowUnchangedByDescriptionChange(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, nil, nil)
	m.pluginUnmanaged = map[string][]app.InstalledPlugin{
		"claude-code": {{Name: "unmanaged-plugin", Marketplace: "some-marketplace", Version: "1.2.3"}},
	}
	rows := agentsAllRowsList(m)
	var unmanagedEntry agentsAllRow
	for _, e := range rows {
		if e.feature == agentsSectionPlugins {
			unmanagedEntry = e
		}
	}

	lines := agentsRowDetailLines(m, unmanagedEntry)
	if len(lines) != 1 {
		t.Fatalf("expected exactly one detail line for unmanaged plugin row, got %+v", lines)
	}
	got := stripANSIEscapeSequences(lines[0])
	if !strings.Contains(got, "marketplace: some-marketplace (unmanaged)") || !strings.Contains(got, "version: 1.2.3") {
		t.Errorf("expected unchanged marketplace/version summary in detail line, got %q", got)
	}
}

// The cursor tool belongs to a real group ("work") to prove the claim popup no longer leaks that tool's current group or name.
func TestAgentsClaimGroupPicker_IgnoresUnrelatedToolsListCursor(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, nil, nil)
	m.allTools = threeTools()
	m.visibleTools = threeTools()
	m.cursor = 0
	m.toolGroups = map[string]string{toolKey("git", "brew"): "work"}
	m.skillsUnmanagedRows = []app.SkillPackageRow{
		{Name: "owner/unmanaged", Source: "owner/unmanaged", Installed: true},
	}
	_, _, unmanagedStart := skillsVisibleRows(m)
	e := agentsAllRow{feature: agentsSectionSkills, localIdx: unmanagedStart, mark: agentsMarkOrphan}

	handled, _ := m.agentsRowClaim(e)
	if !handled {
		t.Fatal("expected agentsRowClaim to handle an orphan skills row")
	}
	m.pickerGroups = []string{"work", "other", groupPickerNewSentinel}

	out := renderGroupPicker(m)
	if strings.Contains(out, "current") {
		t.Errorf("claim picker must not mark any group as current (claim target is an orphan), got:\n%s", out)
	}

	title := popupTitleForGroupPickerTool(m, "Choose Group")
	if !strings.Contains(title, agentsRowName(m, m.pickerClaimAgentsRow)) {
		t.Errorf("popup title = %q, want it to contain claimed orphan name %q", title, agentsRowName(m, m.pickerClaimAgentsRow))
	}
	if strings.Contains(title, "git") {
		t.Errorf("popup title = %q, must not contain unrelated tools-list cursor tool name", title)
	}
}

// Guards that the legitimate tools-tab picker flow still marks the current group.
func TestGroupPicker_ToolsTabStillMarksCurrentGroup(t *testing.T) {
	t.Parallel()
	m := baseModel(threeTools())
	m.mode = viewGroupPicker
	tool := *m.allTools[0]
	m.pickerActionTool = tool
	m.pickerActionToolSet = true
	m.toolGroups = map[string]string{toolKey(tool.Name, tool.Provider): "work"}
	m.pickerGroups = []string{"work", "other", groupPickerNewSentinel}

	out := renderGroupPicker(m)
	if !strings.Contains(out, "current") {
		t.Errorf("expected tools-tab group picker to mark tool's group as current, got:\n%s", out)
	}
}

func TestRenderGroupPicker_NoClaimNoSelectionFallsBackToNoToolSelected(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewGroupPicker
	m.cursor = -1

	out := renderGroupPicker(m)
	if !strings.Contains(out, "no tool selected") {
		t.Errorf("expected 'no tool selected' fallback, got: %q", out)
	}
}

func TestAgentsAllRowsList_OrphanedIgnoreEntry_RendersAsSyntheticIgnoredRow(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		feature   agentsSection
		ghostName string
		setIgnore func(*Model)
	}{
		{"skills", agentsSectionSkills, "ghost-skill", func(m *Model) { m.agentsIgnore.Skills = []string{"ghost-skill"} }},
		{"mcp", agentsSectionMcp, "ghost-mcp", func(m *Model) { m.agentsIgnore.McpServers = []string{"ghost-mcp"} }},
		{"plugins", agentsSectionPlugins, "ghost-plugin", func(m *Model) { m.agentsIgnore.Plugins = []string{"ghost-plugin"} }},
		{"marketplaces", agentsSectionMarketplaces, "ghost-market", func(m *Model) { m.agentsIgnore.Marketplaces = []string{"ghost-market"} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := agentsAllModel(nil, nil, nil)
			tt.setIgnore(&m)

			rows := agentsAllRowsList(m)
			var found agentsAllRow
			var ok bool
			for _, e := range rows {
				if e.feature == tt.feature && e.sortName == tt.ghostName {
					found, ok = e, true
				}
			}
			if !ok {
				t.Fatalf("expected a synthetic row for ghost entry %q", tt.ghostName)
			}
			if !found.synthetic {
				t.Error("expected synthetic=true")
			}
			if found.status != agentsStatusIgnored {
				t.Errorf("status = %v, want agentsStatusIgnored", found.status)
			}
			if found.mark != agentsMarkNone {
				t.Errorf("mark = %v, want agentsMarkNone", found.mark)
			}
			if found.localIdx != -1 {
				t.Errorf("localIdx = %d, want -1", found.localIdx)
			}

			detail := agentsRowDetailLines(m, found)
			if len(detail) != 1 || !strings.Contains(stripANSIEscapeSequences(detail[0]), "ignored — not currently installed") {
				t.Errorf("agentsRowDetailLines = %v, want a single line containing %q", detail, "ignored — not currently installed")
			}
		})
	}
}

// agentsOrphanedIgnoreRows iterates the raw ignore map, not per-agent rows, so a synthetic row stays visible under any agent-pill or chip filter.
func TestAgentsAllRowsList_SyntheticRowSurvivesAgentPillFilter(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(
		[]app.SkillPackageRow{{Name: "skill-a", Source: "a/a", Installed: true, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled}}},
		nil, nil,
	)
	m.agentsIgnore.Skills = []string{"ghost-skill"}
	m.skillTypeIdx = agentsChipSkills
	m.skillAgentIdx = 1 // filter to a specific agent, not "all"

	rows := agentsAllRowsList(m)
	var found bool
	for _, e := range rows {
		if e.feature == agentsSectionSkills && e.sortName == "ghost-skill" && e.synthetic {
			found = true
		}
	}
	if !found {
		t.Fatal("expected the synthetic ghost row to survive the agent-pill filter")
	}
}

func TestAgentsAllRowsList_NameInBothIgnoreAndLiveRow_RendersOnce(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		feature agentsSection
		dupName string
		build   func() Model
	}{
		{
			"skills",
			agentsSectionSkills,
			"dup-skill",
			func() Model {
				m := agentsAllModel(
					[]app.SkillPackageRow{{Name: "dup-skill", Source: "a/a", Installed: true, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled}}},
					nil, nil,
				)
				m.agentsIgnore.Skills = []string{"dup-skill"}
				return m
			},
		},
		{
			"plugins",
			agentsSectionPlugins,
			"dup-plugin",
			func() Model {
				m := agentsAllModel(nil, nil, []app.PluginRow{
					{Name: "dup-plugin", Agents: []string{"claude"}, PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}},
				})
				m.agentsIgnore.Plugins = []string{"dup-plugin"}
				return m
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := tt.build()
			rows := agentsAllRowsList(m)
			var matches []agentsAllRow
			for _, e := range rows {
				if e.feature == tt.feature && e.sortName == tt.dupName {
					matches = append(matches, e)
				}
			}
			if len(matches) != 1 {
				t.Fatalf("got %d rows for the ignored-but-live name, want exactly 1: %+v", len(matches), matches)
			}
			if matches[0].synthetic {
				t.Error("expected the live row to win, synthetic=false")
			}
			if matches[0].status != agentsStatusIgnored {
				t.Errorf("status = %v, want agentsStatusIgnored", matches[0].status)
			}
		})
	}
}

func TestSyntheticRow_OnlyUnignoreIsValid(t *testing.T) {
	t.Parallel()
	for _, feature := range []agentsSection{agentsSectionSkills, agentsSectionPlugins} {
		t.Run(agentsFeatureLabel(feature), func(t *testing.T) {
			m := agentsAllModel(nil, nil, nil)
			e := agentsAllRow{feature: feature, localIdx: -1, status: agentsStatusIgnored, mark: agentsMarkNone, sortName: "ghost", synthetic: true}

			if handled, cmds := m.agentsRowUpgrade(e); handled || len(cmds) != 0 {
				t.Errorf("agentsRowUpgrade on synthetic row: handled=%v cmds=%v, want false/empty", handled, cmds)
			}
			if handled, cmds := m.agentsRowInstall(e); handled || len(cmds) != 0 {
				t.Errorf("agentsRowInstall on synthetic row: handled=%v cmds=%v, want false/empty", handled, cmds)
			}
			if handled, cmds := m.agentsRowClaim(e); handled || len(cmds) != 0 {
				t.Errorf("agentsRowClaim on synthetic row: handled=%v cmds=%v, want false/empty", handled, cmds)
			}
			if handled, cmds := m.agentsRowGroup(e); handled || len(cmds) != 0 {
				t.Errorf("agentsRowGroup on synthetic row: handled=%v cmds=%v, want false/empty", handled, cmds)
			}
			if handled, cmds := m.agentsRowArmDelete(e); handled || len(cmds) != 0 {
				t.Errorf("agentsRowArmDelete on synthetic row: handled=%v cmds=%v, want false/empty", handled, cmds)
			}
			handled, cmds := m.agentsRowToggleIgnore(e)
			if !handled {
				t.Error("expected agentsRowToggleIgnore to handle a synthetic row")
			}
			if len(cmds) == 0 {
				t.Error("expected non-empty cmds arming the ignore confirm")
			}
		})
	}
}

func TestSyntheticRow_ToggleIgnoreRoutesCorrectFeatureAndName(t *testing.T) {
	t.Parallel()
	features := []struct {
		feature agentsSection
		setUp   func(*Model)
	}{
		{agentsSectionSkills, func(m *Model) { m.agentsIgnore.Skills = []string{"ghost"} }},
		{agentsSectionMcp, func(m *Model) { m.agentsIgnore.McpServers = []string{"ghost"} }},
		{agentsSectionPlugins, func(m *Model) { m.agentsIgnore.Plugins = []string{"ghost"} }},
		{agentsSectionMarketplaces, func(m *Model) { m.agentsIgnore.Marketplaces = []string{"ghost"} }},
	}
	for _, tt := range features {
		t.Run(agentsFeatureLabel(tt.feature), func(t *testing.T) {
			a := newScanPlanTestApp(t)
			m := agentsAllModel(nil, nil, nil)
			m.app = a
			m.ctx = context.Background()
			tt.setUp(&m)
			e := agentsAllRow{feature: tt.feature, localIdx: -1, status: agentsStatusIgnored, mark: agentsMarkNone, sortName: "ghost", synthetic: true}

			handled, _ := m.agentsRowToggleIgnore(e)
			if !handled {
				t.Fatal("expected agentsRowToggleIgnore to arm the confirm")
			}
			if m.agentsIgnoreFeature != tt.feature {
				t.Errorf("agentsIgnoreFeature = %v, want %v", m.agentsIgnoreFeature, tt.feature)
			}
			if m.agentsIgnoreName != "ghost" {
				t.Errorf("agentsIgnoreName = %q, want %q", m.agentsIgnoreName, "ghost")
			}

			cmds := m.handleAgentsIgnoreConfirmKeyMsg(pressRune('x').(tea.KeyPressMsg))
			if len(cmds) == 0 {
				t.Fatal("expected non-empty cmds executing the unignore toggle")
			}
		})
	}
}

func TestSetAgentsChip_CursorOnSyntheticSkillsRow_NoClampGuardsMinusOne(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, nil, nil)
	m.agentsIgnore.Skills = []string{"ghost-skill"}
	m.skillTypeIdx = agentsChipAll

	rows := agentsAllRowsList(m)
	idx := -1
	for i, e := range rows {
		if e.feature == agentsSectionSkills && e.synthetic {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("expected a synthetic skills row in the all-chip list")
	}
	m.agentsAllCursor = idx

	m.setAgentsChip(agentsChipSkills)

	if m.skillsCursor != -1 {
		t.Errorf("skillsCursor = %d, want -1 (clampSkillsCursor must be skipped for a synthetic row, which would otherwise clamp -1 to 0)", m.skillsCursor)
	}
}

func TestSetAgentsChip_CursorOnSyntheticRow_McpPluginMarketplace_NoPanicNoClamp(t *testing.T) {
	t.Parallel()
	tests := []struct {
		feature agentsSection
		chip    int
		setUp   func(*Model)
	}{
		{agentsSectionMcp, agentsChipMcp, func(m *Model) { m.agentsIgnore.McpServers = []string{"ghost-mcp"} }},
		{agentsSectionPlugins, agentsChipPlugin, func(m *Model) { m.agentsIgnore.Plugins = []string{"ghost-plugin"} }},
		{agentsSectionMarketplaces, agentsChipMarketplace, func(m *Model) { m.agentsIgnore.Marketplaces = []string{"ghost-market"} }},
	}
	for _, tt := range tests {
		t.Run(agentsFeatureLabel(tt.feature), func(t *testing.T) {
			m := agentsAllModel(nil, nil, nil)
			tt.setUp(&m)
			m.skillTypeIdx = agentsChipAll

			rows := agentsAllRowsList(m)
			idx := -1
			for i, e := range rows {
				if e.feature == tt.feature && e.synthetic {
					idx = i
				}
			}
			if idx < 0 {
				t.Fatalf("expected a synthetic %s row in the all-chip list", agentsFeatureLabel(tt.feature))
			}
			m.agentsAllCursor = idx

			m.setAgentsChip(tt.chip)

			switch tt.feature {
			case agentsSectionMcp:
				if m.mcpCursor != -1 || m.mcpCursorAgentID != "" {
					t.Errorf("mcpCursor=%d mcpCursorAgentID=%q, want -1/\"\"", m.mcpCursor, m.mcpCursorAgentID)
				}
			case agentsSectionPlugins:
				if m.pluginCursor != -1 || m.pluginCursorAgentID != "" {
					t.Errorf("pluginCursor=%d pluginCursorAgentID=%q, want -1/\"\"", m.pluginCursor, m.pluginCursorAgentID)
				}
			case agentsSectionMarketplaces:
				if m.marketplaceCursor != -1 || m.marketplaceCursorAgentID != "" {
					t.Errorf("marketplaceCursor=%d marketplaceCursorAgentID=%q, want -1/\"\"", m.marketplaceCursor, m.marketplaceCursorAgentID)
				}
			}
		})
	}
}

func TestSkillDetailLines_NotesUnknownAgentTargets(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, nil, nil)
	r := app.SkillPackageRow{
		Name:          "skillpack",
		Source:        "a/a",
		Agents:        []string{"claude-code", "clode-code"},
		UnknownAgents: []string{"clode-code"},
	}
	for _, l := range skillDetailLines(m, r) {
		if strings.Contains(stripANSIEscapeSequences(l), "unknown agent target(s): clode-code") {
			return
		}
	}
	t.Errorf("detail lines = %+v, want an unknown-target note", skillDetailLines(m, r))
}
