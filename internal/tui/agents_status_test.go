package tui

import (
	"sort"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
)

func TestMcpAgentRowStatus_Missing(t *testing.T) {
	t.Parallel()
	status, mark := mcpAgentRowStatus(app.McpStatusMissing)
	if status != agentsStatusOutOfSync || mark != agentsMarkMissing {
		t.Fatalf("mcpAgentRowStatus(Missing) = (%v, %v), want (agentsStatusOutOfSync, agentsMarkMissing)", status, mark)
	}
}

func TestMcpAgentRowStatus_Installed(t *testing.T) {
	t.Parallel()
	status, mark := mcpAgentRowStatus(app.McpStatusInstalled)
	if status != agentsStatusInstalled || mark != agentsMarkNone {
		t.Fatalf("mcpAgentRowStatus(Installed) = (%v, %v), want (agentsStatusInstalled, agentsMarkNone)", status, mark)
	}
}

// A server absent from the agent's own config but provided by an installed plugin must render as installed/shadowed, not a real missing gap.
func TestMcpAgentRowStatus_Shadowed(t *testing.T) {
	t.Parallel()
	status, mark := mcpAgentRowStatus(app.McpStatusShadowed)
	if status != agentsStatusInstalled || mark != agentsMarkShadowed {
		t.Fatalf("mcpAgentRowStatus(Shadowed) = (%v, %v), want (agentsStatusInstalled, agentsMarkShadowed)", status, mark)
	}
}

func TestPluginAgentRowStatus_MissingBeatsOutdated(t *testing.T) {
	t.Parallel()
	row := app.PluginRow{
		Version:       "1.0.0",
		LatestVersion: "2.0.0",
		PerAgentStatus: map[string]app.PluginStatus{
			"claude": app.PluginStatusInstalled,
			"codex":  app.PluginStatusMissing,
		},
	}
	status, mark := pluginAgentRowStatus(row, "codex")
	if status != agentsStatusOutOfSync || mark != agentsMarkMissing {
		t.Fatalf("pluginAgentRowStatus(missing agent) = (%v, %v), want (agentsStatusOutOfSync, agentsMarkMissing)", status, mark)
	}
}

func TestPluginAgentRowStatus_OutdatedWithoutMissing(t *testing.T) {
	t.Parallel()
	row := app.PluginRow{
		Version:       "1.0.0",
		LatestVersion: "2.0.0",
		PerAgentStatus: map[string]app.PluginStatus{
			"claude": app.PluginStatusInstalled,
		},
	}
	status, mark := pluginAgentRowStatus(row, "claude")
	if status != agentsStatusUpdates || mark != agentsMarkNone {
		t.Fatalf("pluginAgentRowStatus(outdated) = (%v, %v), want (agentsStatusUpdates, agentsMarkNone)", status, mark)
	}
}

func TestPluginAgentRowStatus_Installed(t *testing.T) {
	t.Parallel()
	row := app.PluginRow{
		Version:       "1.0.0",
		LatestVersion: "1.0.0",
		PerAgentStatus: map[string]app.PluginStatus{
			"claude": app.PluginStatusInstalled,
		},
	}
	status, mark := pluginAgentRowStatus(row, "claude")
	if status != agentsStatusInstalled || mark != agentsMarkNone {
		t.Fatalf("pluginAgentRowStatus(up-to-date) = (%v, %v), want (agentsStatusInstalled, agentsMarkNone)", status, mark)
	}
}

func TestSkillPackageRowStatus_NotInstalledIsOutOfSync(t *testing.T) {
	t.Parallel()
	status, mark := skillPackageRowStatus(false, false, false, false, false)
	if status != agentsStatusOutOfSync || mark != agentsMarkMissing {
		t.Fatalf("skillPackageRowStatus(false, false, false, false) = (%v, %v), want (agentsStatusOutOfSync, agentsMarkMissing)", status, mark)
	}
}

func TestSkillPackageRowStatus_Installed(t *testing.T) {
	t.Parallel()
	status, mark := skillPackageRowStatus(true, false, false, false, false)
	if status != agentsStatusInstalled || mark != agentsMarkNone {
		t.Fatalf("skillPackageRowStatus(true, false, false, false) = (%v, %v), want (agentsStatusInstalled, agentsMarkNone)", status, mark)
	}
}

// An installed package behind its source lands in Updates, while one also missing or drifted keeps the mark for the gap that closes first.
func TestSkillPackageRowStatus_OutdatedOnlyOnAHealthyRow(t *testing.T) {
	t.Parallel()
	status, mark := skillPackageRowStatus(true, false, false, false, true)
	if status != agentsStatusUpdates || mark != agentsMarkNone {
		t.Fatalf("installed+outdated = (%v, %v), want (agentsStatusUpdates, agentsMarkNone)", status, mark)
	}
	status, mark = skillPackageRowStatus(false, false, false, false, true)
	if status != agentsStatusOutOfSync || mark != agentsMarkMissing {
		t.Fatalf("missing+outdated = (%v, %v), want (agentsStatusOutOfSync, agentsMarkMissing)", status, mark)
	}
	status, mark = skillPackageRowStatus(false, false, false, true, true)
	if status != agentsStatusOutOfSync || mark != agentsMarkDrifted {
		t.Fatalf("drifted+outdated = (%v, %v), want (agentsStatusOutOfSync, agentsMarkDrifted)", status, mark)
	}
}

// A plugin-shadowed package renders installed/shadowed even when not installed, since the plugin already provides it.
func TestSkillPackageRowStatus_ShadowedTakesPrecedence(t *testing.T) {
	t.Parallel()
	status, mark := skillPackageRowStatus(false, true, false, false, false)
	if status != agentsStatusInstalled || mark != agentsMarkShadowed {
		t.Fatalf("skillPackageRowStatus(false, true, false, false) = (%v, %v), want (agentsStatusInstalled, agentsMarkShadowed)", status, mark)
	}
}

func TestSkillPackageRowStatus_PackageShadowedIsInstalledAndSelectable(t *testing.T) {
	t.Parallel()
	status, mark := skillPackageRowStatus(false, false, true, false, false)
	if status != agentsStatusInstalled || mark != agentsMarkPackageShadowed {
		t.Fatalf("package-shadowed = (%v, %v), want (agentsStatusInstalled, agentsMarkPackageShadowed)", status, mark)
	}
}

func TestAgentsPackageShadowDoesNotHideMissingAgent(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(
		[]app.SkillPackageRow{{
			Name:   "partly-shadowed",
			Source: "o/partly-shadowed",
			Agents: []string{"claude", "codex"},
			PerAgentStatus: map[string]app.SkillStatus{
				"claude": app.SkillStatusShadowed,
				"codex":  app.SkillStatusMissing,
			},
		}},
		nil, nil,
	)
	m.enabledAgents = []string{"claude", "codex"}
	e := agentsAllRowsList(m)[0]
	if e.status != agentsStatusOutOfSync || e.mark != agentsMarkMissing {
		t.Fatalf("row = (%v, %v), want out-of-sync/missing", e.status, e.mark)
	}
}

// Asserts the flatten groups Updates, OutOfSync, Installed, Available, and that every (feature, localIdx) pair survives the sort.
func TestFlattenOrder_GroupsByStatusThenFeatureThenName(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(
		[]app.SkillPackageRow{
			{Name: "zeta-skill", Source: "o/zeta-skill", Installed: true, Agents: []string{"claude"}, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled}},
			{Name: "alpha-skill", Source: "o/alpha-skill", Installed: false, Agents: []string{"claude"}, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusMissing}},
		},
		[]app.McpServerRow{
			{Name: "zeta-mcp", Agents: []string{"claude"}, PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusInstalled}},
			{Name: "alpha-mcp", Agents: []string{"claude"}, PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusMissing}},
		},
		[]app.PluginRow{
			{Name: "zeta-plugin", Version: "1.0.0", LatestVersion: "2.0.0", Agents: []string{"claude"}, PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}},
			{Name: "alpha-plugin", Agents: []string{"claude"}, PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}},
		},
	)

	entries := agentsAllRowsList(m)
	prePairs := map[[2]int]bool{}
	for _, r := range entries {
		prePairs[[2]int{int(r.feature), r.localIdx}] = true
	}
	wantPairs := [][2]int{
		{int(agentsSectionSkills), 0}, {int(agentsSectionSkills), 1},
		{int(agentsSectionMcp), 0}, {int(agentsSectionMcp), 1},
		{int(agentsSectionPlugins), 0}, {int(agentsSectionPlugins), 1},
	}
	for _, want := range wantPairs {
		if !prePairs[want] {
			t.Fatalf("missing (feature, localIdx) pair %v in flattened list", want)
		}
	}

	if !sort.SliceIsSorted(entries, func(i, j int) bool {
		if entries[i].status != entries[j].status {
			return entries[i].status < entries[j].status
		}
		if entries[i].feature != entries[j].feature {
			return entries[i].feature < entries[j].feature
		}
		if entries[i].sortName != entries[j].sortName {
			return entries[i].sortName < entries[j].sortName
		}
		return entries[i].agentID < entries[j].agentID
	}) {
		t.Fatalf("entries not sorted by (status, feature, sortName, agentID): %+v", entries)
	}

	var sawUpdates, sawOutOfSync, sawInstalled bool
	for _, e := range entries {
		switch e.status {
		case agentsStatusUpdates:
			if sawOutOfSync || sawInstalled {
				t.Fatalf("agentsStatusUpdates entry found after a later-group entry")
			}
			sawUpdates = true
		case agentsStatusOutOfSync:
			if sawInstalled {
				t.Fatalf("agentsStatusOutOfSync entry found after agentsStatusInstalled entry")
			}
			sawOutOfSync = true
		case agentsStatusInstalled:
			sawInstalled = true
		}
	}
	if !sawUpdates || !sawOutOfSync || !sawInstalled {
		t.Fatalf("expected at least one entry in each of Updates/OutOfSync/Installed, got sawUpdates=%v sawOutOfSync=%v sawInstalled=%v", sawUpdates, sawOutOfSync, sawInstalled)
	}
}

func TestAgentsMarkCell_Shadowed_RendersWrongProvIcon(t *testing.T) {
	t.Parallel()
	p := palette{}
	out := stripANSIEscapeSequences(agentsMarkCell(p, agentsStatusInstalled, agentsMarkShadowed, false))
	if out != iconWrongProv {
		t.Fatalf("agentsMarkCell(shadowed) = %q, want %q", out, iconWrongProv)
	}
}

func TestAgentsRowCells_Skills_Shadowed_ShowsViaPlugin(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(
		[]app.SkillPackageRow{{Name: "shadow-skill", Source: "o/shadow-skill", ShadowedByPlugin: true}},
		nil, nil,
	)
	e := agentsAllRow{feature: agentsSectionSkills, localIdx: 0, status: agentsStatusInstalled, mark: agentsMarkShadowed, sortName: "shadow-skill"}
	cols := colWidths{name: 20, typ: agentsTypeColW, prov: agentsAgentIDColFloor, ver: 20, screenW: m.width}

	_, right := agentsRowCells(m, palette{}, cols, e, false)
	var verText string
	for _, c := range right {
		verText += stripANSIEscapeSequences(c.text)
	}
	if !strings.Contains(verText, "via plugin") {
		t.Fatalf("expected skills row cells to contain %q, got %q", "via plugin", verText)
	}
}

func TestAgentsRowCells_Mcp_Shadowed_ShowsViaPlugin(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(
		nil,
		[]app.McpServerRow{{Name: "shadow-mcp", PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusShadowed}, ShadowedByPlugin: true}},
		nil,
	)
	e := agentsAllRow{feature: agentsSectionMcp, localIdx: 0, agentID: "claude", status: agentsStatusInstalled, mark: agentsMarkShadowed, sortName: "shadow-mcp"}
	cols := colWidths{name: 20, typ: agentsTypeColW, prov: agentsAgentIDColFloor, ver: 20, screenW: m.width}

	_, right := agentsRowCells(m, palette{}, cols, e, false)
	var verText string
	for _, c := range right {
		verText += stripANSIEscapeSequences(c.text)
	}
	if !strings.Contains(verText, "via plugin") {
		t.Fatalf("expected mcp row cells to contain %q, got %q", "via plugin", verText)
	}
}

// skillPackageRepoNameDisplay strips the "owner/" prefix so the detail line names the plugin the way it is actually known.
func TestSkillDetailLines_Shadowed_StripsOwnerPrefix(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, nil, nil)
	r := app.SkillPackageRow{Name: "academic-research-skills", Source: "owner/academic-research-skills", ShadowedByPlugin: true}

	lines := skillDetailLines(m, r)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "provided by plugin academic-research-skills") {
		t.Fatalf("expected detail lines to contain %q, got:\n%s", "provided by plugin academic-research-skills", joined)
	}
	if strings.Contains(joined, "provided by plugin owner/academic-research-skills") {
		t.Fatalf("expected owner prefix stripped, got:\n%s", joined)
	}
}

func TestAgentsRowDetailLines_Mcp_Shadowed_ShowsProvidedByPlugin(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(
		nil,
		[]app.McpServerRow{{Name: "shadow-mcp", Transport: "stdio", PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusShadowed}, ShadowedByPlugin: true}},
		nil,
	)
	e := agentsAllRow{feature: agentsSectionMcp, localIdx: 0, agentID: "claude", status: agentsStatusInstalled, mark: agentsMarkShadowed}

	lines := agentsRowDetailLines(m, e)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "provided by plugin shadow-mcp") {
		t.Fatalf("expected mcp detail lines to contain %q, got:\n%s", "provided by plugin shadow-mcp", joined)
	}
}

// The app-layer ShadowedByPlugin flag must reach the flattened row's mark rather than being lost or misclassified as agentsMarkMissing.
func TestAgentsAllRowsList_ShadowedSkill_SurvivesToMark(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(
		[]app.SkillPackageRow{{Name: "shadow-skill", Source: "o/shadow-skill", Installed: false, ShadowedByPlugin: true}},
		nil, nil,
	)

	entries := agentsAllRowsList(m)
	found := false
	for _, e := range entries {
		if e.feature != agentsSectionSkills || e.sortName != "shadow-skill" {
			continue
		}
		found = true
		if e.mark != agentsMarkShadowed {
			t.Errorf("mark = %v, want agentsMarkShadowed", e.mark)
		}
		if e.status != agentsStatusInstalled {
			t.Errorf("status = %v, want agentsStatusInstalled", e.status)
		}
	}
	if !found {
		t.Fatal("expected a shadow-skill row in the flattened list")
	}
}

// A McpStatusShadowed per-agent status must produce agentsMarkShadowed, not agentsMarkMissing.
func TestAgentsAllRowsList_ShadowedMcp_SurvivesToMark(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(
		nil,
		[]app.McpServerRow{{Name: "shadow-mcp", Agents: []string{"claude"}, PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusShadowed}, ShadowedByPlugin: true}},
		nil,
	)

	entries := agentsAllRowsList(m)
	found := false
	for _, e := range entries {
		if e.feature != agentsSectionMcp || e.sortName != "shadow-mcp" {
			continue
		}
		found = true
		if e.mark != agentsMarkShadowed {
			t.Errorf("mark = %v, want agentsMarkShadowed", e.mark)
		}
		if e.status != agentsStatusInstalled {
			t.Errorf("status = %v, want agentsStatusInstalled", e.status)
		}
	}
	if !found {
		t.Fatal("expected a shadow-mcp row in the flattened list")
	}
}

func TestSkillsFindRows_ClassifyAsAvailable(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, nil, nil)
	m.skillFindResults = []app.FindResult{{Skill: "found-skill", Source: "o/found-skill"}}

	entries := agentsAllRowsList(m)
	visible, findStart, _ := skillsVisibleRows(m)
	if findStart < 0 {
		t.Fatal("expected findStart >= 0 with skillFindResults set")
	}
	found := false
	for _, e := range entries {
		if e.feature != agentsSectionSkills {
			continue
		}
		if e.localIdx >= findStart && e.localIdx < len(visible) {
			found = true
			if e.status != agentsStatusAvailable {
				t.Fatalf("find-result entry status = %v, want agentsStatusAvailable", e.status)
			}
			if e.agentID != "" {
				t.Fatalf("find-result entry agentID = %q, want empty (find rows are not expanded per-agent)", e.agentID)
			}
		}
	}
	if !found {
		t.Fatal("expected a find-result entry in the flattened list")
	}
}
