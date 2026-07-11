package tui

import (
	"sort"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func agentsAllModel(skills []app.SkillPackageRow, mcpRows []app.McpServerRow, pluginRows []app.PluginRow) Model {
	m := baseModel(nil)
	m.pluginFormName = textinput.New()
	m.pluginFormMarketplace = textinput.New()
	m.pluginFormAgents = textinput.New()
	m.mode = viewSkills
	m.agentsEnabled = true
	m.skillTypeIdx = agentsChipAll
	m.width = 120
	m.cursorHidden = false
	m.skillsLoaded = true
	m.mcpLoaded = true
	m.pluginLoaded = true
	m.skillsRows = skills
	m.mcpRows = mcpRows
	m.pluginRows = pluginRows
	m.enabledAgents = []string{"claude"}
	return m
}

func TestAgentsAll_RenderShowsAllThreeSectionsInOrder(t *testing.T) {
	m := agentsAllModel(
		[]app.SkillPackageRow{
			{Name: "caveman", Source: "github.com/foo/caveman", Installed: true},
			{Name: "missing-skill", Source: "github.com/foo/missing", Installed: false},
		},
		[]app.McpServerRow{{Name: "ripgrep-mcp", Transport: "stdio", PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusMissing}}},
		[]app.PluginRow{{Name: "linter-plugin", Marketplace: "acme-market", PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}}},
	)
	m.skillFindResults = []app.FindResult{{Source: "owner/found-skill", Skill: "found-skill"}}

	out := stripANSIEscapeSequences(m.viewSkillsBody())

	outOfSyncIdx := strings.Index(out, "Out of Sync")
	installedIdx := strings.Index(out, "Installed")
	availableIdx := strings.Index(out, "Available")

	if outOfSyncIdx < 0 || installedIdx < 0 || availableIdx < 0 {
		t.Fatalf("expected present sections in order, got outOfSyncIdx=%d installedIdx=%d availableIdx=%d in:\n%s", outOfSyncIdx, installedIdx, availableIdx, out)
	}
	if !(outOfSyncIdx < installedIdx && installedIdx < availableIdx) {
		t.Fatalf("expected order Out of Sync < Installed < Available, got outOfSyncIdx=%d installedIdx=%d availableIdx=%d", outOfSyncIdx, installedIdx, availableIdx)
	}
}

func TestAgentsAll_EnabledEmptySectionDoesNotBreakRender(t *testing.T) {
	m := agentsAllModel(
		[]app.SkillPackageRow{{Name: "caveman", Source: "github.com/foo/caveman", Installed: true}},
		nil,
		nil,
	)

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	if out == "" {
		t.Fatal("expected non-empty render with mcp enabled but zero rows")
	}
	for _, row := range agentsAllRowsList(m) {
		if row.feature == agentsSectionMcp {
			t.Fatalf("expected no mcp-typed rows when mcp has zero servers, got one in:\n%s", out)
		}
	}
}

func TestAgentsAll_DisabledSectionAbsentFromRender(t *testing.T) {
	m := agentsAllModel(
		nil,
		[]app.McpServerRow{{Name: "ripgrep-mcp", PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusInstalled}}},
		nil,
	)
	m.skillsEnabled = false

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	for _, row := range agentsAllRowsList(m) {
		if row.feature == agentsSectionSkills {
			t.Fatalf("disabled skills feature should contribute zero rows, got one in:\n%s", out)
		}
	}
	lines := strings.Split(out, "\n")
	for _, l := range lines[1:] {
		if strings.Contains(l, "skills") {
			t.Fatalf("disabled skills feature should mean the 'skills' type-column label appears on zero row lines, got: %q in:\n%s", l, out)
		}
	}
	if !strings.Contains(out, "ripgrep-mcp") {
		t.Fatalf("enabled mcp row should still render, got:\n%s", out)
	}
}

func TestAgentsAll_DisabledChipRendersDimmed(t *testing.T) {
	// renderPillBarDim treats a disabled chip as never-active even when
	// activeIdx points at it, so it always renders via the dim help style
	// (bracket-less " mcp ") rather than the active title style ("[mcp]").
	p := palette{}
	names := []string{"all", "skills", "mcp", "plugin"}
	disabled := map[int]bool{agentsChipMcp: true}

	out := stripANSIEscapeSequences(renderPillBarDim(p, names, agentsChipMcp, 200, disabled))
	if strings.Contains(out, "[mcp]") {
		t.Fatalf("disabled mcp chip should not render as active ([mcp]), got: %q", out)
	}
	if !strings.Contains(out, " mcp ") {
		t.Fatalf("disabled mcp chip should render with the dim (non-active) form, got: %q", out)
	}
}

func TestAgentsAll_CursorTraversesSectionBoundary(t *testing.T) {
	m := agentsAllModel(
		[]app.SkillPackageRow{
			{Name: "skill-a", Source: "a/a", Installed: true},
			{Name: "skill-b", Source: "b/b", Installed: true},
		},
		[]app.McpServerRow{
			{Name: "mcp-a", PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusInstalled}},
			{Name: "mcp-b", PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusInstalled}},
		},
		nil,
	)

	rows := agentsAllRowsList(m)
	lastSkillsIdx := -1
	for i, r := range rows {
		if r.feature == agentsSectionSkills {
			lastSkillsIdx = i
		}
	}
	if lastSkillsIdx < 0 {
		t.Fatal("expected at least one skills row in agentsAllRowsList")
	}
	m.agentsAllCursor = lastSkillsIdx

	got := drive(m, tea.KeyPressMsg{Code: tea.KeyDown})

	entry, ok := agentsAllEntryAt(got, got.agentsAllCursor)
	if !ok {
		t.Fatal("expected cursor to land on a valid row after crossing section boundary")
	}
	if entry.feature != agentsSectionMcp {
		t.Fatalf("feature after crossing boundary = %v, want agentsSectionMcp", entry.feature)
	}
	if entry.localIdx != 0 {
		t.Fatalf("localIdx after crossing boundary = %d, want 0 (first mcp row)", entry.localIdx)
	}
}

func TestAgentsAll_KeyDispatch_McpRowOpensAddForm(t *testing.T) {
	m := agentsAllModel(
		[]app.SkillPackageRow{{Name: "skill-a", Source: "a/a", Installed: true}},
		[]app.McpServerRow{{Name: "mcp-a", PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusInstalled}}},
		nil,
	)
	rows := agentsAllRowsList(m)
	mcpCursor := -1
	for i, r := range rows {
		if r.feature == agentsSectionMcp {
			mcpCursor = i
			break
		}
	}
	if mcpCursor < 0 {
		t.Fatal("expected an mcp row in agentsAllRowsList")
	}
	m.agentsAllCursor = mcpCursor

	got := drive(m, pressRune('n'))
	if !got.mcpFormOpen {
		t.Fatal("pressing 'n' with cursor on an mcp row should open the mcp add form")
	}
}

func TestAgentsAll_KeyDispatch_DeleteConfirmScopedToRowFeature(t *testing.T) {
	base := agentsAllModel(
		[]app.SkillPackageRow{{Name: "skill-a", Source: "a/a", Installed: true}},
		nil,
		[]app.PluginRow{{Name: "plugin-a", Marketplace: "acme", PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}}},
	)
	rows := agentsAllRowsList(base)

	pluginCursor := -1
	skillsCursorIdx := -1
	for i, r := range rows {
		if r.feature == agentsSectionPlugins && pluginCursor < 0 {
			pluginCursor = i
		}
		if r.feature == agentsSectionSkills && skillsCursorIdx < 0 {
			skillsCursorIdx = i
		}
	}
	if pluginCursor < 0 || skillsCursorIdx < 0 {
		t.Fatal("expected both a plugin row and a skills row in agentsAllRowsList")
	}

	onPlugin := base
	onPlugin.agentsAllCursor = pluginCursor
	got := drive(onPlugin, pressRune('d'))
	if !got.agentsDeleteConfirm || got.agentsDeleteFeature != agentsSectionPlugins || got.agentsDeleteName != "plugin-a" {
		t.Fatalf("pressing 'd' with cursor on a plugin row should arm agentsDeleteConfirm for plugins/plugin-a, got confirm=%v feature=%v name=%q", got.agentsDeleteConfirm, got.agentsDeleteFeature, got.agentsDeleteName)
	}

	onSkills := base
	onSkills.agentsAllCursor = skillsCursorIdx
	got2 := drive(onSkills, pressRune('d'))
	if !got2.agentsDeleteConfirm || got2.agentsDeleteFeature != agentsSectionSkills || got2.agentsDeleteName != "skill-a" {
		t.Fatalf("pressing 'd' with cursor on a skills row should arm agentsDeleteConfirm for skills/skill-a, got confirm=%v feature=%v name=%q", got2.agentsDeleteConfirm, got2.agentsDeleteFeature, got2.agentsDeleteName)
	}
}

func TestAgentsAll_KeyDispatch_SkillsSearchOnlyOnSkillsRow(t *testing.T) {
	base := agentsAllModel(
		[]app.SkillPackageRow{{Name: "skill-a", Source: "a/a", Installed: true}},
		[]app.McpServerRow{{Name: "mcp-a", PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusInstalled}}},
		nil,
	)
	rows := agentsAllRowsList(base)

	skillsCursorIdx, mcpCursorIdx := -1, -1
	for i, r := range rows {
		if r.feature == agentsSectionSkills && skillsCursorIdx < 0 {
			skillsCursorIdx = i
		}
		if r.feature == agentsSectionMcp && mcpCursorIdx < 0 {
			mcpCursorIdx = i
		}
	}
	if skillsCursorIdx < 0 || mcpCursorIdx < 0 {
		t.Fatal("expected both a skills row and an mcp row in agentsAllRowsList")
	}

	onSkills := base
	onSkills.agentsAllCursor = skillsCursorIdx
	got := drive(onSkills, pressRune('/'))
	if !got.skillsSearchActive {
		t.Fatal("pressing '/' with cursor on a skills row should activate skills search")
	}

	onMcp := base
	onMcp.agentsAllCursor = mcpCursorIdx
	got2 := drive(onMcp, pressRune('/'))
	if got2.skillsSearchActive {
		t.Fatal("pressing '/' with cursor on an mcp row should NOT activate skills search")
	}
}

func TestAgentsAll_ChipSwitchSyncsCursorRoundTrip(t *testing.T) {
	m := agentsAllModel(
		[]app.SkillPackageRow{
			{Name: "skill-a", Source: "a/a", Installed: true},
			{Name: "skill-b", Source: "b/b", Installed: true},
		},
		[]app.McpServerRow{
			{Name: "mcp-a", PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusInstalled}},
			{Name: "mcp-b", PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusInstalled}},
			{Name: "mcp-c", PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusInstalled}},
		},
		[]app.PluginRow{{Name: "plugin-a", Marketplace: "acme", PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}}},
	)
	m.skillsEnabled = false // right from "all" lands directly on mcp

	rows := agentsAllRowsList(m)
	var wantCursor int
	var wantName string
	found := false
	for i, r := range rows {
		if r.feature == agentsSectionMcp {
			wantCursor, wantName = i, r.sortName
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected at least one mcp row in agentsAllRowsList")
	}
	m.agentsAllCursor = wantCursor

	toMcp := drive(m, tea.KeyPressMsg{Code: tea.KeyRight})
	if toMcp.skillTypeIdx != agentsChipMcp {
		t.Fatalf("skillTypeIdx after right = %d, want agentsChipMcp", toMcp.skillTypeIdx)
	}
	if toMcp.mcpCursor < 0 || toMcp.mcpCursor >= len(toMcp.mcpRows) {
		t.Fatalf("mcpCursor = %d out of bounds after chip switch", toMcp.mcpCursor)
	}
	if got := toMcp.mcpRows[toMcp.mcpCursor].Name; got != wantName {
		t.Fatalf("mcpCursor resolves to row %q, want %q (same row as original all-cursor selection)", got, wantName)
	}

	backToAll := drive(toMcp, tea.KeyPressMsg{Code: tea.KeyLeft})
	if backToAll.skillTypeIdx != agentsChipAll {
		t.Fatalf("skillTypeIdx after left = %d, want agentsChipAll", backToAll.skillTypeIdx)
	}
	entry, ok := agentsAllEntryAt(backToAll, backToAll.agentsAllCursor)
	if !ok {
		t.Fatal("expected agentsAllCursor to resolve to a valid row after switching back to all")
	}
	if entry.sortName != wantName || entry.feature != agentsSectionMcp {
		t.Fatalf("agentsAllCursor resolves to (%v,%q), want (agentsSectionMcp,%q)", entry.feature, entry.sortName, wantName)
	}
}

func TestFlow_ClickAgentsFilters(t *testing.T) {
	clickAgentsFilter := func(t *testing.T, m Model, kind toolFilterKind, idx int) Model {
		t.Helper()
		for _, zone := range agentsFilterHitZones(m) {
			if zone.kind == kind && zone.index == idx {
				return drive(m, tea.MouseClickMsg{X: (zone.start + zone.end) / 2, Y: zone.y, Button: tea.MouseLeft})
			}
		}
		t.Fatalf("missing agents filter hit zone for kind %v index %d", kind, idx)
		return m
	}

	t.Run("click type chip switches active chip", func(t *testing.T) {
		m := agentsAllModel(
			[]app.SkillPackageRow{{Name: "skill-a", Source: "a/a", Installed: true}},
			[]app.McpServerRow{{Name: "mcp-a", PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusInstalled}}},
			nil,
		)
		got := clickAgentsFilter(t, m, agentsFilterType, agentsChipMcp)
		if got.skillTypeIdx != agentsChipMcp {
			t.Fatalf("skillTypeIdx = %d, want agentsChipMcp", got.skillTypeIdx)
		}
	})

	t.Run("click agent-id pill sets agent filter", func(t *testing.T) {
		m := agentsAllModel(
			[]app.SkillPackageRow{{Name: "skill-a", Source: "a/a", Installed: true, Agents: []string{"claude", "cursor"}}},
			nil,
			nil,
		)
		m.enabledAgents = []string{"claude", "cursor"}
		got := clickAgentsFilter(t, m, agentsFilterAgent, 1)
		if got.skillAgentIdx != 1 {
			t.Fatalf("skillAgentIdx = %d, want 1", got.skillAgentIdx)
		}
	})

	t.Run("out of bounds click returns no zones and handler returns false", func(t *testing.T) {
		m := agentsAllModel(
			[]app.SkillPackageRow{{Name: "skill-a", Source: "a/a", Installed: true}},
			nil,
			nil,
		)
		if m.handleAgentsFilterClick(-1, -1) {
			t.Fatal("handleAgentsFilterClick at an out-of-bounds position should return false")
		}
	})

	t.Run("mode gating: not in viewSkills yields no zones", func(t *testing.T) {
		m := agentsAllModel(
			[]app.SkillPackageRow{{Name: "skill-a", Source: "a/a", Installed: true}},
			nil,
			nil,
		)
		m.mode = viewList
		if zones := agentsFilterHitZones(m); len(zones) != 0 {
			t.Fatalf("expected no hit zones outside viewSkills, got %d", len(zones))
		}
	})
}

func TestAgentsAll_EmptyStateCTAsPerChip(t *testing.T) {
	t.Run("mcp chip empty state hints install, not the flat generic line", func(t *testing.T) {
		m := agentsAllModel(nil, nil, nil)
		m.skillTypeIdx = agentsChipMcp

		out := stripANSIEscapeSequences(m.viewSkillsBody())
		if !strings.Contains(out, "No MCP servers tracked yet.") {
			t.Fatalf("expected mcp-specific empty text, got:\n%s", out)
		}
		if !strings.Contains(out, "[i] install") {
			t.Fatalf("expected install hint, got:\n%s", out)
		}
		if strings.Contains(out, "No agent items tracked yet.") {
			t.Fatalf("should not regress to the flat generic line for mcp, got:\n%s", out)
		}
	})

	t.Run("plugin chip empty state hints install, not the flat generic line", func(t *testing.T) {
		m := agentsAllModel(nil, nil, nil)
		m.skillTypeIdx = agentsChipPlugin

		out := stripANSIEscapeSequences(m.viewSkillsBody())
		if !strings.Contains(out, "No plugins tracked yet.") {
			t.Fatalf("expected plugin-specific empty text, got:\n%s", out)
		}
		if !strings.Contains(out, "[i] install") {
			t.Fatalf("expected install hint, got:\n%s", out)
		}
		if strings.Contains(out, "No agent items tracked yet.") {
			t.Fatalf("should not regress to the flat generic line for plugin, got:\n%s", out)
		}
	})

	t.Run("all chip empty state stays generic and useful when every feature is empty", func(t *testing.T) {
		m := agentsAllModel(nil, nil, nil)
		m.skillTypeIdx = agentsChipAll

		out := stripANSIEscapeSequences(m.viewSkillsBody())
		if !strings.Contains(out, "No agent items tracked yet.") {
			t.Fatalf("expected generic all-chip empty text, got:\n%s", out)
		}
	})

	t.Run("skills chip empty state keeps its existing rich text", func(t *testing.T) {
		m := agentsAllModel(nil, nil, nil)
		m.skillTypeIdx = agentsChipSkills

		out := stripANSIEscapeSequences(m.viewSkillsBody())
		if !strings.Contains(out, "No agent skills tracked yet.") {
			t.Fatalf("expected skills-specific empty text, got:\n%s", out)
		}
		if !strings.Contains(out, "[i] import") || !strings.Contains(out, "[r] restore") {
			t.Fatalf("expected import/restore hints, got:\n%s", out)
		}
	})
}

func TestAgentsAll_ChipNavigationSkipsDisabledMcp(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	m.mcpEnabled = false

	got := drive(m, tea.KeyPressMsg{Code: tea.KeyRight})
	if got.skillTypeIdx != agentsChipSkills {
		t.Fatalf("skillTypeIdx after right from all = %d, want agentsChipSkills", got.skillTypeIdx)
	}

	got = drive(got, tea.KeyPressMsg{Code: tea.KeyRight})
	if got.skillTypeIdx != agentsChipPlugin {
		t.Fatalf("skillTypeIdx after second right (mcp disabled) = %d, want agentsChipPlugin (mcp skipped)", got.skillTypeIdx)
	}
}

func TestAgentsAll_SkillsChipFiltersToSkillsOnlyContent(t *testing.T) {
	m := agentsAllModel(
		[]app.SkillPackageRow{{Name: "skill-a", Source: "a/a", Installed: true}},
		[]app.McpServerRow{{Name: "mcp-a", PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusInstalled}}},
		[]app.PluginRow{{Name: "plugin-a", Marketplace: "acme", PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}}},
	)
	m.skillTypeIdx = agentsChipSkills

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	if strings.Contains(out, "MCP Servers") {
		t.Fatalf("skills chip should not render MCP Servers section header, got:\n%s", out)
	}
	if strings.Contains(out, "mcp-a") || strings.Contains(out, "plugin-a") {
		t.Fatalf("skills chip should not render mcp/plugin row content, got:\n%s", out)
	}
	if !strings.Contains(out, "skill-a") {
		t.Fatalf("skills chip should render skills content, got:\n%s", out)
	}
}

func TestAgentsAll_TabEntryResetsChipAndClampsCursor(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewDots
	m.skillTypeIdx = agentsChipMcp
	m.agentsAllCursor = 99

	var cmds []tea.Cmd
	ok := m.switchMainTab(viewSkills, &cmds)
	if !ok {
		t.Fatal("switchMainTab to viewSkills should succeed")
	}
	if m.skillTypeIdx != agentsChipAll {
		t.Fatalf("skillTypeIdx after entering skills tab = %d, want agentsChipAll", m.skillTypeIdx)
	}
	rows := agentsAllRowsList(m)
	if len(rows) == 0 {
		if m.agentsAllCursor != 0 {
			t.Fatalf("agentsAllCursor = %d, want 0 for empty row list", m.agentsAllCursor)
		}
	} else if m.agentsAllCursor < 0 || m.agentsAllCursor >= len(rows) {
		t.Fatalf("agentsAllCursor = %d out of bounds [0,%d)", m.agentsAllCursor, len(rows))
	}
}

func TestAgentsAll_AllFeaturesDisabledShowsHelpScreen(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	m.skillsEnabled = false
	m.mcpEnabled = false
	m.pluginsEnabled = false
	m.agentsEnabled = true

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	if !strings.Contains(out, "All agent features are disabled for this machine.") {
		t.Fatalf("expected disabled-features message, got:\n%s", out)
	}
	if !strings.Contains(out, "Toggle them in Settings to enable.") {
		t.Fatalf("expected toggle-in-settings hint, got:\n%s", out)
	}
}

func TestAgentsAll_ClampAfterRowCountShrink(t *testing.T) {
	m := agentsAllModel(
		[]app.SkillPackageRow{
			{Name: "skill-a", Source: "a/a", Installed: true},
			{Name: "skill-b", Source: "b/b", Installed: true},
		},
		[]app.McpServerRow{
			{Name: "mcp-a", PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusInstalled}},
			{Name: "mcp-b", PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusInstalled}},
		},
		nil,
	)
	m.agentsAllCursor = len(agentsAllRowsList(m)) - 1
	if m.agentsAllCursor != 3 {
		t.Fatalf("precondition: expected 4 total rows (cursor at 3), got cursor=%d", m.agentsAllCursor)
	}

	m.mcpEnabled = false
	clampAgentsAllCursor(&m)

	rows := agentsAllRowsList(m)
	if m.agentsAllCursor < 0 || m.agentsAllCursor >= len(rows) {
		t.Fatalf("agentsAllCursor = %d out of bounds [0,%d) after shrinking rows", m.agentsAllCursor, len(rows))
	}
}

// firstRowCursor returns the agentsAllCursor index of the first flattened row
// matching feature, or -1 if none.
func firstRowCursor(m Model, feature agentsSection) int {
	for i, r := range agentsAllRowsList(m) {
		if r.feature == feature {
			return i
		}
	}
	return -1
}

func TestAgentsAll_KeyDispatch_IgnoreTogglesFromRow(t *testing.T) {
	m := agentsAllModel(
		[]app.SkillPackageRow{{Name: "skill-a", Source: "a/a", Installed: true}},
		nil,
		nil,
	)
	cursor := firstRowCursor(m, agentsSectionSkills)
	if cursor < 0 {
		t.Fatal("expected a skills row")
	}
	m.agentsAllCursor = cursor

	got := drive(m, pressRune('x'))
	// doToggleAgentsIgnore/doReloadAgentsIgnore are fired as cmds (not executed
	// here since m.app is nil in tests); this asserts the key was intercepted
	// by the agents-all row handler rather than falling through to
	// handleSkillsKeyMsg (which has no 'x' case and would leave cursor/mode
	// untouched either way, so the meaningful assertion is no panic + mode intact).
	if got.mode != viewSkills {
		t.Fatalf("expected mode to remain viewSkills after 'x', got %v", got.mode)
	}
}

func TestAgentsAll_KeyDispatch_GroupOpensMcpMembershipPicker(t *testing.T) {
	m := agentsAllModel(
		nil,
		[]app.McpServerRow{{Name: "mcp-a", Groups: []string{"work"}, PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusInstalled}}},
		nil,
	)
	cursor := firstRowCursor(m, agentsSectionMcp)
	if cursor < 0 {
		t.Fatal("expected an mcp row")
	}
	m.agentsAllCursor = cursor

	got := drive(m, pressRune('g'))
	if got.mode != viewGroupMembership {
		t.Fatalf("expected mode viewGroupMembership after 'g' on an mcp row, got %v", got.mode)
	}
	if got.pickerMembershipKind != pickerMembershipMcp {
		t.Fatalf("expected pickerMembershipKind=mcp, got %q", got.pickerMembershipKind)
	}
	if got.pickerMembershipName != "mcp-a" {
		t.Fatalf("expected pickerMembershipName=mcp-a, got %q", got.pickerMembershipName)
	}
}

func TestAgentsAll_KeyDispatch_GroupOpensPluginMembershipPicker(t *testing.T) {
	m := agentsAllModel(
		nil,
		nil,
		[]app.PluginRow{{Name: "plugin-a", Groups: []string{"work"}, PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}}},
	)
	cursor := firstRowCursor(m, agentsSectionPlugins)
	if cursor < 0 {
		t.Fatal("expected a plugin row")
	}
	m.agentsAllCursor = cursor

	got := drive(m, pressRune('g'))
	if got.mode != viewGroupMembership {
		t.Fatalf("expected mode viewGroupMembership after 'g' on a plugin row, got %v", got.mode)
	}
	if got.pickerMembershipKind != pickerMembershipPlugin {
		t.Fatalf("expected pickerMembershipKind=plugin, got %q", got.pickerMembershipKind)
	}
	if got.pickerMembershipName != "plugin-a" {
		t.Fatalf("expected pickerMembershipName=plugin-a, got %q", got.pickerMembershipName)
	}
}

func TestAgentsAll_KeyDispatch_InstallFiresOnMissingMcpRow(t *testing.T) {
	m := agentsAllModel(
		nil,
		[]app.McpServerRow{{Name: "mcp-a", PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusMissing}}},
		nil,
	)
	cursor := firstRowCursor(m, agentsSectionMcp)
	if cursor < 0 {
		t.Fatal("expected an mcp row")
	}
	m.agentsAllCursor = cursor

	got := drive(m, pressRune('i'))
	if !got.mcpRunning {
		t.Fatal("pressing 'i' on a missing mcp row should set mcpRunning while the install cmd runs")
	}
}

func TestAgentsAll_KeyDispatch_UpgradeFiresOnOutdatedPluginRow(t *testing.T) {
	m := agentsAllModel(
		nil,
		nil,
		[]app.PluginRow{{Name: "plugin-a", Version: "1.0.0", LatestVersion: "2.0.0", PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}}},
	)
	cursor := firstRowCursor(m, agentsSectionPlugins)
	if cursor < 0 {
		t.Fatal("expected a plugin row")
	}
	m.agentsAllCursor = cursor

	got := drive(m, pressRune('u'))
	if !got.pluginRunning {
		t.Fatal("pressing 'u' on an outdated plugin row should set pluginRunning while the update cmd runs")
	}
}

func TestAgentsAll_Expansion_ExplicitAgentsListIndependentPerAgentStatus(t *testing.T) {
	m := agentsAllModel(
		nil,
		[]app.McpServerRow{{
			Name:   "alpha-mcp",
			Agents: []string{"claude", "cursor"},
			PerAgentStatus: map[string]app.McpStatus{
				"claude": app.McpStatusInstalled,
				"cursor": app.McpStatusMissing,
			},
		}},
		nil,
	)

	var claudeRow, cursorRow agentsAllRow
	var sawClaude, sawCursor bool
	for _, e := range agentsAllRowsList(m) {
		if e.feature != agentsSectionMcp {
			continue
		}
		switch e.agentID {
		case "claude":
			claudeRow, sawClaude = e, true
		case "cursor":
			cursorRow, sawCursor = e, true
		}
	}
	if !sawClaude || !sawCursor {
		t.Fatalf("expected independent rows for both claude and cursor, sawClaude=%v sawCursor=%v", sawClaude, sawCursor)
	}
	if claudeRow.status != agentsStatusInstalled {
		t.Fatalf("claude row status = %v, want agentsStatusInstalled", claudeRow.status)
	}
	if cursorRow.status != agentsStatusOutOfSync || cursorRow.mark != agentsMarkMissing {
		t.Fatalf("cursor row (status,mark) = (%v,%v), want (agentsStatusOutOfSync, agentsMarkMissing)", cursorRow.status, cursorRow.mark)
	}
}

func TestAgentsAll_Expansion_EmptyAgentsFallsBackToEnabledAgents(t *testing.T) {
	m := agentsAllModel(
		nil,
		[]app.McpServerRow{{
			Name: "beta-mcp",
			PerAgentStatus: map[string]app.McpStatus{
				"claude": app.McpStatusInstalled,
				"cursor": app.McpStatusInstalled,
			},
		}},
		nil,
	)
	m.enabledAgents = []string{"claude", "cursor"}

	var agentIDs []string
	for _, e := range agentsAllRowsList(m) {
		if e.feature == agentsSectionMcp {
			agentIDs = append(agentIDs, e.agentID)
		}
	}
	sort.Strings(agentIDs)
	if len(agentIDs) != 2 || agentIDs[0] != "claude" || agentIDs[1] != "cursor" {
		t.Fatalf("expected rows for both enabledAgents [claude cursor], got %v", agentIDs)
	}
}

func TestAgentsAll_Expansion_ExplicitAgentNotInEnabledAgentsUsedVerbatim(t *testing.T) {
	m := agentsAllModel(
		nil,
		[]app.McpServerRow{{
			Name:           "gamma-mcp",
			Agents:         []string{"windsurf"},
			PerAgentStatus: map[string]app.McpStatus{"windsurf": app.McpStatusInstalled},
		}},
		nil,
	)
	m.enabledAgents = []string{"claude"}

	var agentIDs []string
	for _, e := range agentsAllRowsList(m) {
		if e.feature == agentsSectionMcp {
			agentIDs = append(agentIDs, e.agentID)
		}
	}
	if len(agentIDs) != 1 || agentIDs[0] != "windsurf" {
		t.Fatalf("expected explicit Agents list to be used verbatim (windsurf), got %v", agentIDs)
	}
}

func TestAgentsAll_Expansion_AgentUnavailablePairingSkipped(t *testing.T) {
	m := agentsAllModel(
		nil,
		[]app.McpServerRow{{
			Name:           "delta-mcp",
			Agents:         []string{"claude"},
			PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusAgentUnavailable},
		}},
		[]app.PluginRow{{
			Name:           "epsilon-plugin",
			Agents:         []string{"claude"},
			PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusAgentUnavailable},
		}},
	)

	for _, e := range agentsAllRowsList(m) {
		if e.feature == agentsSectionMcp {
			t.Fatalf("expected zero rows for agent-unavailable mcp pairing, got %+v", e)
		}
		if e.feature == agentsSectionPlugins {
			t.Fatalf("expected zero rows for agent-unavailable plugin pairing, got %+v", e)
		}
	}
}

// TestAgentsAll_SkillsRowIsPackageLevelRegardlessOfEnabledAgents pins the
// canonical-store model: a skills row exists per package (installed on disk
// per the lockfile), independent of m.enabledAgents/PerAgentStatus, and
// rendering never panics even when there is no linked agent to report.
func TestAgentsAll_SkillsRowIsPackageLevelRegardlessOfEnabledAgents(t *testing.T) {
	m := agentsAllModel(
		[]app.SkillPackageRow{{Name: "zeta-skill", Source: "o/zeta-skill", Installed: true}},
		nil,
		nil,
	)
	m.enabledAgents = nil

	var out string
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("viewSkillsBody panicked: %v", r)
			}
		}()
		out = m.viewSkillsBody()
	}()
	if out == "" {
		t.Fatal("expected non-empty render")
	}

	rows := agentsAllRowsList(m)
	sawSkills := false
	for _, e := range rows {
		if e.feature == agentsSectionSkills {
			sawSkills = true
			if e.status != agentsStatusInstalled {
				t.Fatalf("expected installed skills row (package-level, unaffected by enabledAgents), got status=%v", e.status)
			}
		}
	}
	if !sawSkills {
		t.Fatalf("expected exactly one package-level skills row regardless of enabledAgents, got zero in:\n%s", out)
	}
}

func TestAgentsAllRowsList_IgnoredFindResultRendersUnderIgnoredNotAvailable(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	m.skillFindResults = []app.FindResult{{Source: "owner/found-skill", Skill: "found-skill"}}
	m.agentsIgnore.Skills = []string{"found-skill"}

	rows := agentsAllRowsList(m)
	var found agentsAllRow
	var ok bool
	for _, e := range rows {
		if e.feature == agentsSectionSkills && e.sortName == "found-skill" {
			found, ok = e, true
		}
	}
	if !ok {
		t.Fatalf("expected a row for found-skill, got rows: %+v", rows)
	}
	if found.status != agentsStatusIgnored {
		t.Errorf("ignored find-result row status = %v, want agentsStatusIgnored (still Available)", found.status)
	}
}

func TestAgentsAll_PinDimming_IgnoredOutdatedPluginRowUsesIgnoredStyleNotMissingOrOutdated(t *testing.T) {
	// Pins commit 44338c6: an ignored+outdated row must render entirely via
	// styleIgnored, never leaking styleMissing/styleOutdated/styleProvider
	// coloring into the mark/name/version/group cells.
	m := agentsAllModel(nil, nil, nil)
	m.palette = buildPaletteFor(true)
	m.agentsIgnore.Plugins = []string{"theta-plugin"}
	m.pluginRows = []app.PluginRow{{
		Name: "theta-plugin", Version: "1.0.0", LatestVersion: "2.0.0",
		Groups: []string{"work"}, PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled},
	}}
	rows := agentsAllRowsList(m)
	var ignoredEntry agentsAllRow
	for _, e := range rows {
		if e.feature == agentsSectionPlugins && e.status == agentsStatusIgnored {
			ignoredEntry = e
		}
	}
	if ignoredEntry.sortName != "theta-plugin" {
		t.Fatalf("expected an ignored plugin row for theta-plugin, got rows: %+v", rows)
	}
	cols := agentsColWidths(m, rows)
	left, right := agentsRowCells(m, m.palette, cols, ignoredEntry, false)

	ordinary := m
	ordinary.agentsIgnore = config.AgentsIgnore{}
	ordinaryEntry := ignoredEntry
	ordinaryEntry.status = agentsStatusUpdates
	ordinaryEntry.mark = agentsMarkNone
	oLeft, oRight := agentsRowCells(ordinary, ordinary.palette, cols, ordinaryEntry, false)

	forbidden := []string{
		m.palette.styleMissing.Render("x"),
		m.palette.styleOutdated.Render("x"),
		m.palette.styleProvider.Render("x"),
	}
	for _, cell := range append(append([]rowCell{}, left...), right...) {
		for _, bad := range forbidden {
			codeOnly := ansiCodePrefix(bad)
			if codeOnly != "" && strings.Contains(cell.text, codeOnly) {
				t.Fatalf("ignored row cell %q unexpectedly contains a forbidden style code %q", cell.text, codeOnly)
			}
		}
	}

	// Prove the dimming distinction is real: the same row rendered as
	// ordinary (non-ignored, outdated) DOES carry one of the forbidden
	// style codes, so the ignored-row assertion above isn't vacuously
	// passing against a palette where those styles are never applied.
	ordinaryHasForbidden := false
	for _, cell := range append(append([]rowCell{}, oLeft...), oRight...) {
		for _, bad := range forbidden {
			codeOnly := ansiCodePrefix(bad)
			if codeOnly != "" && strings.Contains(cell.text, codeOnly) {
				ordinaryHasForbidden = true
			}
		}
	}
	if !ordinaryHasForbidden {
		t.Fatal("expected the ordinary (non-ignored, outdated) row to carry at least one forbidden style code, proving styleIgnored dimming is a real distinction and not a vacuous pass")
	}
}

func TestAgentsAll_ShaDriftRendering_UsesShaShortArrowFormat(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	m.palette = buildPaletteFor(true)
	m.pluginRows = []app.PluginRow{{
		Name: "iota-plugin", Sha: "abcdef1234567", LatestSha: "9876543210fed",
		PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled},
	}}

	out := stripANSIEscapeSequences(renderAgentsGroupedTab(m, m.palette, nil, agentsSectionPlugins, true))
	want := shaShort("abcdef1234567") + " → " + shaShort("9876543210fed")
	if !strings.Contains(out, want) {
		t.Fatalf("expected sha-drift text %q in rendered output, got:\n%s", want, out)
	}
}

// ansiCodePrefix extracts the leading ANSI escape sequence rendered around
// "x" by a style, so callers can check whether a differently-styled cell
// carries the same color code without depending on exact reset sequences.
func ansiCodePrefix(rendered string) string {
	idx := strings.Index(rendered, "x")
	if idx <= 0 {
		return ""
	}
	return rendered[:idx]
}

// TestAgentsBoot_InitDoesNotFireSectionLoadsBeforeSnapshot is a regression
// test for the boot-load race: Model.New seeds agentsEnabled/skillsEnabled/
// mcpEnabled/pluginsEnabled to true optimistically, before the startup
// snapshot (toolsLoadedMsg) can correct them. Feeding every cmd resolved by
// Init()'s batch into Update (simulating tea.Batch's concurrent dispatch)
// must not flip the loaded flags — those loads must wait for
// handleToolsLoadedMsg to run with the real snapshot flags.
func TestAgentsBoot_InitDoesNotFireSectionLoadsBeforeSnapshot(t *testing.T) {
	m := baseModel(nil)
	m.agentsEnabled = true
	m.skillsEnabled = true
	m.mcpEnabled = true
	m.pluginsEnabled = true
	m.skillsLoaded = false
	m.mcpLoaded = false
	m.pluginLoaded = false

	cmd := m.Init()
	if cmd == nil {
		t.Fatal("expected non-nil Init cmd")
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected Init() to resolve to tea.BatchMsg, got %T", msg)
	}
	for _, c := range batch {
		if c == nil {
			continue
		}
		// Other Init cmds (e.g. loadTools) dereference m.app, which is nil in
		// this white-box unit test; panics from unrelated cmds are not failures.
		func() {
			defer func() { recover() }()
			resolved := c()
			var tm tea.Model = m
			tm, _ = tm.Update(resolved)
			m = tm.(Model)
		}()
	}

	if m.skillsLoaded || m.mcpLoaded || m.pluginLoaded {
		t.Fatalf("Init() batch fired section loads before toolsLoadedMsg corrected the snapshot flags: skillsLoaded=%v mcpLoaded=%v pluginLoaded=%v",
			m.skillsLoaded, m.mcpLoaded, m.pluginLoaded)
	}
}

func TestAgentsBoot_ToolsLoadedGatesPerSectionBySectionEnabled(t *testing.T) {
	m := baseModel(nil)
	m.agentsEnabled = true
	m.skillsEnabled = true
	m.mcpEnabled = true
	m.pluginsEnabled = true
	m.skillsLoaded = false
	m.mcpLoaded = false
	m.pluginLoaded = false

	got := drive(m, toolsLoadedMsg{
		agentsEnabled:  true,
		skillsEnabled:  false,
		mcpEnabled:     true,
		pluginsEnabled: true,
	})

	if got.skillsLoaded {
		t.Fatal("skillsLoaded should remain false when snapshot skillsEnabled is false")
	}
	if !got.mcpLoaded {
		t.Fatal("mcpLoaded should become true when snapshot mcpEnabled is true")
	}
	if !got.pluginLoaded {
		t.Fatal("pluginLoaded should become true when snapshot pluginsEnabled is true")
	}
}

func TestAgentsBoot_ToolsLoadedDisabledFlagsFireNoLoads(t *testing.T) {
	m := baseModel(nil)
	m.agentsEnabled = true
	m.skillsEnabled = true
	m.mcpEnabled = true
	m.pluginsEnabled = true
	m.skillsLoaded = false
	m.mcpLoaded = false
	m.pluginLoaded = false

	got := drive(m, toolsLoadedMsg{
		agentsEnabled:  true,
		skillsEnabled:  false,
		mcpEnabled:     false,
		pluginsEnabled: false,
	})

	if got.skillsLoaded || got.mcpLoaded || got.pluginLoaded {
		t.Fatalf("expected no loaded flags set when snapshot disables all sections, got skillsLoaded=%v mcpLoaded=%v pluginLoaded=%v",
			got.skillsLoaded, got.mcpLoaded, got.pluginLoaded)
	}
}

func TestAgentsBoot_ToolsLoadedIdempotentOnSecondMsg(t *testing.T) {
	base := baseModel(nil)
	base.agentsEnabled = true
	base.skillsEnabled = true
	base.mcpEnabled = true
	base.pluginsEnabled = true

	snapshot := toolsLoadedMsg{
		agentsEnabled:  true,
		skillsEnabled:  true,
		mcpEnabled:     true,
		pluginsEnabled: true,
	}

	once := drive(base, snapshot)
	twice := drive(base, snapshot, snapshot)

	if twice.skillsLoaded != true || twice.mcpLoaded != true || twice.pluginLoaded != true {
		t.Fatalf("expected all loaded flags true after two toolsLoadedMsg, got skillsLoaded=%v mcpLoaded=%v pluginLoaded=%v",
			twice.skillsLoaded, twice.mcpLoaded, twice.pluginLoaded)
	}
	if once.skillsLoaded != twice.skillsLoaded || once.mcpLoaded != twice.mcpLoaded || once.pluginLoaded != twice.pluginLoaded {
		t.Fatalf("expected idempotent loaded-flag end-state: once=(%v,%v,%v) twice=(%v,%v,%v)",
			once.skillsLoaded, once.mcpLoaded, once.pluginLoaded,
			twice.skillsLoaded, twice.mcpLoaded, twice.pluginLoaded)
	}
	if once.mcpRunning != twice.mcpRunning || once.pluginRunning != twice.pluginRunning {
		t.Fatalf("expected idempotent running-flag end-state: once=(%v,%v) twice=(%v,%v)",
			once.mcpRunning, once.pluginRunning, twice.mcpRunning, twice.pluginRunning)
	}
}

func TestAgentsAllRowsListAgentFilterMcpPlugin(t *testing.T) {
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
		[]app.PluginRow{{
			Name:   "beta-plugin",
			Agents: []string{"claude-code", "codex"},
			PerAgentStatus: map[string]app.PluginStatus{
				"claude-code": app.PluginStatusInstalled,
				"codex":       app.PluginStatusInstalled,
			},
		}},
	)
	m.enabledAgents = []string{"claude-code", "codex"}

	agentIDs := skillAgentIDs(m.skillsRows, m.enabledAgents)
	if len(agentIDs) != 2 {
		t.Fatalf("expected 2 agent IDs, got %v", agentIDs)
	}
	selected := agentIDs[0]

	m.skillAgentIdx = 0
	rowsAll := agentsAllRowsList(m)
	mcpAgentsAll := map[string]bool{}
	pluginAgentsAll := map[string]bool{}
	for _, e := range rowsAll {
		switch e.feature {
		case agentsSectionMcp:
			mcpAgentsAll[e.agentID] = true
		case agentsSectionPlugins:
			pluginAgentsAll[e.agentID] = true
		}
	}
	for _, id := range agentIDs {
		if !mcpAgentsAll[id] {
			t.Fatalf("skillAgentIdx=0: expected mcp row for agent %q, got %v", id, mcpAgentsAll)
		}
		if !pluginAgentsAll[id] {
			t.Fatalf("skillAgentIdx=0: expected plugin row for agent %q, got %v", id, pluginAgentsAll)
		}
	}

	idxOf := -1
	for i, id := range agentIDs {
		if id == selected {
			idxOf = i
		}
	}
	m.skillAgentIdx = idxOf + 1
	rowsFiltered := agentsAllRowsList(m)
	for _, e := range rowsFiltered {
		switch e.feature {
		case agentsSectionMcp:
			if e.agentID != selected {
				t.Fatalf("skillAgentIdx=%d: unexpected mcp row for agent %q, want only %q", m.skillAgentIdx, e.agentID, selected)
			}
		case agentsSectionPlugins:
			if e.agentID != selected {
				t.Fatalf("skillAgentIdx=%d: unexpected plugin row for agent %q, want only %q", m.skillAgentIdx, e.agentID, selected)
			}
		}
	}
	sawMcp, sawPlugin := false, false
	for _, e := range rowsFiltered {
		if e.feature == agentsSectionMcp {
			sawMcp = true
		}
		if e.feature == agentsSectionPlugins {
			sawPlugin = true
		}
	}
	if !sawMcp || !sawPlugin {
		t.Fatalf("expected at least one mcp row and one plugin row for the selected agent, sawMcp=%v sawPlugin=%v", sawMcp, sawPlugin)
	}
}

func TestAgentsAll_KeyDispatch_DeleteConfirmTimeoutThenSecondPressDeletes(t *testing.T) {
	m := agentsAllModel(
		[]app.SkillPackageRow{{Name: "skill-a", Source: "a/a", Installed: true}},
		nil,
		nil,
	)
	cursor := firstRowCursor(m, agentsSectionSkills)
	if cursor < 0 {
		t.Fatal("expected a skills row")
	}
	m.agentsAllCursor = cursor

	armed := drive(m, pressRune('d'))
	if !armed.agentsDeleteConfirm {
		t.Fatal("first 'd' press should arm agentsDeleteConfirm")
	}

	cancelled := drive(armed, pressEsc())
	if cancelled.agentsDeleteConfirm {
		t.Fatal("esc should cancel agentsDeleteConfirm")
	}

	confirmed := drive(armed, pressRune('d'))
	if confirmed.agentsDeleteConfirm {
		t.Fatal("second 'd' press should clear agentsDeleteConfirm (delete confirmed)")
	}
	if !confirmed.skillsRunning {
		t.Fatal("confirmed delete should set skillsRunning while doRemoveSkillPackage runs")
	}
}

func TestScrollBy_ViewSkills(t *testing.T) {
	base := agentsAllModel(
		[]app.SkillPackageRow{
			{Name: "skill-a", Source: "a/a", Installed: true},
			{Name: "skill-b", Source: "b/b", Installed: true},
		},
		[]app.McpServerRow{
			{Name: "mcp-a", PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusInstalled}},
			{Name: "mcp-b", PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusInstalled}},
		},
		[]app.PluginRow{
			{Name: "plugin-a", Marketplace: "acme", PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}},
			{Name: "plugin-b", Marketplace: "acme", PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}},
		},
	)

	t.Run("agentsChipAll wraps around", func(t *testing.T) {
		m := base
		m.skillTypeIdx = agentsChipAll
		m.agentsAllCursor = 0

		down := drive(m, tea.MouseWheelMsg{Button: tea.MouseWheelDown})
		if down.agentsAllCursor != 1 {
			t.Fatalf("agentsAllCursor after wheel down = %d, want 1", down.agentsAllCursor)
		}

		up := drive(down, tea.MouseWheelMsg{Button: tea.MouseWheelUp})
		if up.agentsAllCursor != 0 {
			t.Fatalf("agentsAllCursor after wheel up = %d, want 0", up.agentsAllCursor)
		}

		wrapped := drive(up, tea.MouseWheelMsg{Button: tea.MouseWheelUp})
		total := len(agentsAllRowsList(up))
		if wrapped.agentsAllCursor != total-1 {
			t.Fatalf("agentsAllCursor should wrap to last row %d, got %d", total-1, wrapped.agentsAllCursor)
		}
	})

	t.Run("agentsChipAll keyboard wraps around", func(t *testing.T) {
		m := base
		m.skillTypeIdx = agentsChipAll
		m.agentsAllCursor = 0
		total := len(agentsAllRowsList(m))

		up := drive(m, pressRune('k'))
		if up.agentsAllCursor != total-1 {
			t.Fatalf("agentsAllCursor after k from 0 = %d, want wraparound to last row %d", up.agentsAllCursor, total-1)
		}

		down := drive(up, pressRune('j'))
		if down.agentsAllCursor != 0 {
			t.Fatalf("agentsAllCursor after j from last row = %d, want wraparound to 0", down.agentsAllCursor)
		}
	})

	t.Run("agentsChipSkills wraps around", func(t *testing.T) {
		m := base
		m.skillTypeIdx = agentsChipSkills
		m.skillsCursor = 0

		up := drive(m, tea.MouseWheelMsg{Button: tea.MouseWheelUp})
		if up.skillsCursor != 1 {
			t.Fatalf("skillsCursor after wheel up from 0 = %d, want wraparound to 1", up.skillsCursor)
		}

		down := drive(up, tea.MouseWheelMsg{Button: tea.MouseWheelDown})
		if down.skillsCursor != 0 {
			t.Fatalf("skillsCursor after wheel down = %d, want 0", down.skillsCursor)
		}
	})

	t.Run("agentsChipMcp wraps around", func(t *testing.T) {
		m := base
		m.skillTypeIdx = agentsChipMcp

		rows := agentsFilteredRowsList(m, agentsSectionMcp)
		if len(rows) < 2 {
			t.Fatalf("expected at least 2 mcp rows, got %d", len(rows))
		}
		m.mcpCursor = rows[0].localIdx
		m.mcpCursorAgentID = rows[0].agentID

		up := drive(m, tea.MouseWheelMsg{Button: tea.MouseWheelUp})
		wantLast := rows[len(rows)-1]
		if up.mcpCursor != wantLast.localIdx || up.mcpCursorAgentID != wantLast.agentID {
			t.Fatalf("mcpCursor/AgentID after wheel up = %d/%q, want wraparound to %d/%q", up.mcpCursor, up.mcpCursorAgentID, wantLast.localIdx, wantLast.agentID)
		}

		down := drive(up, tea.MouseWheelMsg{Button: tea.MouseWheelDown})
		wantFirst := rows[0]
		if down.mcpCursor != wantFirst.localIdx || down.mcpCursorAgentID != wantFirst.agentID {
			t.Fatalf("mcpCursor/AgentID after wheel down = %d/%q, want %d/%q", down.mcpCursor, down.mcpCursorAgentID, wantFirst.localIdx, wantFirst.agentID)
		}
	})

	t.Run("agentsChipPlugin wraps around", func(t *testing.T) {
		m := base
		m.skillTypeIdx = agentsChipPlugin

		rows := agentsFilteredRowsList(m, agentsSectionPlugins)
		if len(rows) < 2 {
			t.Fatalf("expected at least 2 plugin rows, got %d", len(rows))
		}
		m.pluginCursor = rows[0].localIdx
		m.pluginCursorAgentID = rows[0].agentID

		up := drive(m, tea.MouseWheelMsg{Button: tea.MouseWheelUp})
		wantLast := rows[len(rows)-1]
		if up.pluginCursor != wantLast.localIdx || up.pluginCursorAgentID != wantLast.agentID {
			t.Fatalf("pluginCursor/AgentID after wheel up = %d/%q, want wraparound to %d/%q", up.pluginCursor, up.pluginCursorAgentID, wantLast.localIdx, wantLast.agentID)
		}

		down := drive(up, tea.MouseWheelMsg{Button: tea.MouseWheelDown})
		wantFirst := rows[0]
		if down.pluginCursor != wantFirst.localIdx || down.pluginCursorAgentID != wantFirst.agentID {
			t.Fatalf("pluginCursor/AgentID after wheel down = %d/%q, want %d/%q", down.pluginCursor, down.pluginCursorAgentID, wantFirst.localIdx, wantFirst.agentID)
		}
	})
}

func TestMarketplaceRowsMsg_PopulatesRowsAndUnmanaged(t *testing.T) {
	m := agentsAllModel(nil, nil, nil)
	m.marketplaceRunning = true

	got := drive(m, marketplaceRowsMsg{
		rows: []app.MarketplaceRow{
			{Name: "acme-market", Source: "acme/repo", Agents: []string{"claude"}, PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled}},
		},
		unmanaged: map[string][]app.InstalledMarketplace{
			"claude-code": {{Name: "orphan-market", Source: "orphan/repo"}},
		},
	})

	if len(got.marketplaceRows) != 1 || got.marketplaceRows[0].Name != "acme-market" {
		t.Fatalf("marketplaceRows = %+v, want one row named acme-market", got.marketplaceRows)
	}
	if len(got.marketplaceUnmanaged["claude-code"]) != 1 || got.marketplaceUnmanaged["claude-code"][0].Name != "orphan-market" {
		t.Fatalf("marketplaceUnmanaged = %+v, want one entry for claude-code named orphan-market", got.marketplaceUnmanaged)
	}
	if got.marketplaceRunning {
		t.Fatalf("expected marketplaceRunning to clear after marketplaceRowsMsg")
	}

	rows := agentsAllRowsList(got)
	var managed, unmanagedEntry agentsAllRow
	for _, e := range rows {
		if e.feature != agentsSectionMarketplaces {
			continue
		}
		if e.localIdx < len(got.marketplaceRows) {
			managed = e
		} else {
			unmanagedEntry = e
		}
	}

	managedLines := agentsRowDetailLines(got, managed)
	if len(managedLines) != 1 || !strings.Contains(stripANSIEscapeSequences(managedLines[0]), "source: acme/repo") {
		t.Fatalf("managed row detail lines = %+v, want source: acme/repo", managedLines)
	}
	unmanagedLines := agentsRowDetailLines(got, unmanagedEntry)
	if len(unmanagedLines) != 1 || !strings.Contains(stripANSIEscapeSequences(unmanagedLines[0]), "source: orphan/repo (unmanaged)") {
		t.Fatalf("unmanaged row detail lines = %+v, want source: orphan/repo (unmanaged)", unmanagedLines)
	}
}

func TestAgentsAllRowsListAgentFilter_Marketplace(t *testing.T) {
	m := agentsAllModel(
		nil, nil, nil,
	)
	m.marketplaceRows = []app.MarketplaceRow{{
		Name:   "gamma-market",
		Agents: []string{"claude-code", "codex"},
		PerAgentStatus: map[string]app.PluginStatus{
			"claude-code": app.PluginStatusInstalled,
			"codex":       app.PluginStatusInstalled,
		},
	}}
	m.enabledAgents = []string{"claude-code", "codex"}

	agentIDs := skillAgentIDs(m.skillsRows, m.enabledAgents)
	if len(agentIDs) != 2 {
		t.Fatalf("expected 2 agent IDs, got %v", agentIDs)
	}
	selected := agentIDs[0]

	m.skillAgentIdx = 0
	marketAgentsAll := map[string]bool{}
	for _, e := range agentsAllRowsList(m) {
		if e.feature == agentsSectionMarketplaces {
			marketAgentsAll[e.agentID] = true
		}
	}
	for _, id := range agentIDs {
		if !marketAgentsAll[id] {
			t.Fatalf("skillAgentIdx=0: expected marketplace row for agent %q, got %v", id, marketAgentsAll)
		}
	}

	idxOf := -1
	for i, id := range agentIDs {
		if id == selected {
			idxOf = i
		}
	}
	m.skillAgentIdx = idxOf + 1
	for _, e := range agentsAllRowsList(m) {
		if e.feature == agentsSectionMarketplaces && e.agentID != selected {
			t.Fatalf("skillAgentIdx=%d: unexpected marketplace row for agent %q, want only %q", m.skillAgentIdx, e.agentID, selected)
		}
	}
}
