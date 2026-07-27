package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
)

func skillFilterBaseModel() Model {
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillsEnabled = true
	m.mcpEnabled = true
	m.pluginsEnabled = true
	m.skillsRowsKnown = true
	m.mcpRowsKnown = true
	m.pluginRowsKnown = true
	m.marketplaceRowsKnown = true
	m.enabledAgents = []string{"claude"}
	return m
}

func TestSkillFilter_TypeCyclesRight(t *testing.T) {
	t.Parallel()
	m := skillFilterBaseModel()
	m.skillTypeIdx = agentsChipSkills

	m = drive(m, tea.KeyPressMsg{Code: tea.KeyRight})
	if m.skillTypeIdx != agentsChipMcp {
		t.Errorf("skillTypeIdx after right = %d, want %d (mcp)", m.skillTypeIdx, agentsChipMcp)
	}

	m = drive(m, tea.KeyPressMsg{Code: tea.KeyRight})
	if m.skillTypeIdx != agentsChipPlugin {
		t.Errorf("skillTypeIdx after second right = %d, want %d (plugin)", m.skillTypeIdx, agentsChipPlugin)
	}

	m = drive(m, tea.KeyPressMsg{Code: tea.KeyRight})
	if m.skillTypeIdx != agentsChipMarketplace {
		t.Errorf("skillTypeIdx after third right = %d, want %d (marketplace)", m.skillTypeIdx, agentsChipMarketplace)
	}

	m = drive(m, tea.KeyPressMsg{Code: tea.KeyRight})
	if m.skillTypeIdx != agentsChipMarketplace {
		t.Errorf("skillTypeIdx after right at max = %d, want %d (clamped, marketplace)", m.skillTypeIdx, agentsChipMarketplace)
	}
}

func TestSkillFilter_TypeCyclesLeft(t *testing.T) {
	t.Parallel()
	m := skillFilterBaseModel()
	m.skillTypeIdx = agentsChipMarketplace

	m = drive(m, tea.KeyPressMsg{Code: tea.KeyLeft})
	if m.skillTypeIdx != agentsChipPlugin {
		t.Errorf("skillTypeIdx after left = %d, want %d (plugin)", m.skillTypeIdx, agentsChipPlugin)
	}

	m = drive(m, tea.KeyPressMsg{Code: tea.KeyLeft})
	if m.skillTypeIdx != agentsChipMcp {
		t.Errorf("skillTypeIdx after second left = %d, want %d (mcp)", m.skillTypeIdx, agentsChipMcp)
	}

	m = drive(m, tea.KeyPressMsg{Code: tea.KeyLeft})
	if m.skillTypeIdx != agentsChipSkills {
		t.Errorf("skillTypeIdx after third left = %d, want %d (skills)", m.skillTypeIdx, agentsChipSkills)
	}

	m = drive(m, tea.KeyPressMsg{Code: tea.KeyLeft})
	if m.skillTypeIdx != agentsChipAll {
		t.Errorf("skillTypeIdx after fourth left = %d, want %d (all)", m.skillTypeIdx, agentsChipAll)
	}

	m = drive(m, tea.KeyPressMsg{Code: tea.KeyLeft})
	if m.skillTypeIdx != agentsChipAll {
		t.Errorf("skillTypeIdx after left at min = %d, want %d (clamped, all)", m.skillTypeIdx, agentsChipAll)
	}
}

func TestSkillFilter_MCPTypeShowsPlaceholder(t *testing.T) {
	t.Parallel()
	m := skillFilterBaseModel()
	m.skillTypeIdx = agentsChipMcp

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	if !strings.Contains(out, "No MCP servers tracked yet.") {
		t.Errorf("expected mcp-specific empty placeholder, got:\n%s", out)
	}
}

func TestSkillFilter_PluginTypeShowsPlaceholder(t *testing.T) {
	t.Parallel()
	m := skillFilterBaseModel()
	m.skillTypeIdx = agentsChipPlugin

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	if !strings.Contains(out, "No plugins tracked yet.") {
		t.Errorf("expected plugin-specific empty placeholder, got:\n%s", out)
	}
}

func TestSkillFilter_SkillsTypeShowsTable(t *testing.T) {
	t.Parallel()
	m := skillFilterBaseModel()
	m.skillTypeIdx = agentsChipSkills
	m.skillsRows = []app.SkillPackageRow{
		{Source: "owner/mypkg", Name: "mypkg", Installed: true, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled}},
	}

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	if strings.Contains(out, "No MCP servers tracked yet.") {
		t.Error("skills tab should not show MCP placeholder")
	}
	if strings.Contains(out, "No plugins tracked yet.") {
		t.Error("skills tab should not show plugin placeholder")
	}
	if !strings.Contains(out, "mypkg") {
		t.Errorf("skills tab should show skill row name, got:\n%s", out)
	}
}

func TestSkillFilter_AgentBarListsAgents(t *testing.T) {
	t.Parallel()
	m := skillFilterBaseModel()
	m.skillTypeIdx = agentsChipSkills
	m.skillsRows = []app.SkillPackageRow{
		{Source: "owner/pkg1", Name: "pkg1", Agents: []string{"codex"}, Installed: true},
		{Source: "owner/pkg2", Name: "pkg2", Agents: []string{"claude-code"}, Installed: true},
	}

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	if !strings.Contains(out, "codex") {
		t.Errorf("expected agent bar to contain 'codex', got:\n%s", out)
	}
	if !strings.Contains(out, "claude-code") {
		t.Errorf("expected agent bar to contain 'claude-code', got:\n%s", out)
	}
}

func TestSkillFilter_AgentFilterShowsOnlyMatchingRows(t *testing.T) {
	t.Parallel()
	m := skillFilterBaseModel()
	m.skillTypeIdx = agentsChipSkills
	m.skillsRows = []app.SkillPackageRow{
		{Source: "owner/codex-pkg", Name: "codex-pkg", Agents: []string{"codex"}, Installed: true},
		{Source: "owner/claude-pkg", Name: "claude-pkg", Agents: []string{"claude-code"}, Installed: true},
	}

	// skillAgentIDs returns sorted agent IDs; "claude-code" < "codex" alphabetically.
	agentIDs := skillAgentIDs(m.skillsRows, m.enabledAgents)
	codexIdx := -1
	for i, id := range agentIDs {
		if id == "codex" {
			codexIdx = i + 1 // +1 because index 0 is "all"
			break
		}
	}
	if codexIdx < 0 {
		t.Fatal("'codex' not found in skillAgentIDs output")
	}

	m.skillAgentIdx = codexIdx
	out := stripANSIEscapeSequences(m.viewSkillsBody())

	if !strings.Contains(out, "codex-pkg") {
		t.Errorf("expected 'codex-pkg' to appear when filtering by codex, got:\n%s", out)
	}
	if strings.Contains(out, "claude-pkg") {
		t.Errorf("expected 'claude-pkg' to be hidden when filtering by codex, got:\n%s", out)
	}
}

func TestSkillFilter_AgentFilterAllShowsBothRows(t *testing.T) {
	t.Parallel()
	m := skillFilterBaseModel()
	m.skillTypeIdx = agentsChipSkills
	m.skillsRows = []app.SkillPackageRow{
		{Source: "owner/codex-pkg", Name: "codex-pkg", Agents: []string{"codex"}, Installed: true},
		{Source: "owner/claude-pkg", Name: "claude-pkg", Agents: []string{"claude-code"}, Installed: true},
	}
	m.skillAgentIdx = 0 // all

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	if !strings.Contains(out, "codex-pkg") {
		t.Errorf("expected 'codex-pkg' visible when skillAgentIdx=0 (all), got:\n%s", out)
	}
	if !strings.Contains(out, "claude-pkg") {
		t.Errorf("expected 'claude-pkg' visible when skillAgentIdx=0 (all), got:\n%s", out)
	}
}

func TestSkillFilter_CurlyKeysCycleAgentIdx(t *testing.T) {
	t.Parallel()
	m := skillFilterBaseModel()
	m.skillTypeIdx = agentsChipSkills
	m.skillsRows = []app.SkillPackageRow{
		{Source: "owner/pkg1", Name: "pkg1", Agents: []string{"codex"}, Installed: true},
		{Source: "owner/pkg2", Name: "pkg2", Agents: []string{"claude-code"}, Installed: true},
	}
	m.skillAgentIdx = 0

	m = drive(m, pressRune('}'))
	if m.skillAgentIdx != 1 {
		t.Errorf("skillAgentIdx after } = %d, want 1", m.skillAgentIdx)
	}

	m = drive(m, pressRune('}'))
	if m.skillAgentIdx != 2 {
		t.Errorf("skillAgentIdx after second } = %d, want 2", m.skillAgentIdx)
	}

	// Union of enabledAgents (["claude"]) and row agents ("codex", "claude-code") is 3 unique IDs, so max index = 3.
	m = drive(m, pressRune('}'))
	if m.skillAgentIdx != 3 {
		t.Errorf("skillAgentIdx after third } = %d, want 3", m.skillAgentIdx)
	}

	// Clamped at max (3 agents → max index = 3)
	m = drive(m, pressRune('}'))
	if m.skillAgentIdx != 3 {
		t.Errorf("skillAgentIdx after } at max = %d, want 3 (clamped)", m.skillAgentIdx)
	}

	m = drive(m, pressRune('{'))
	if m.skillAgentIdx != 2 {
		t.Errorf("skillAgentIdx after { = %d, want 2", m.skillAgentIdx)
	}

	m = drive(m, pressRune('{'))
	if m.skillAgentIdx != 1 {
		t.Errorf("skillAgentIdx after second { = %d, want 1", m.skillAgentIdx)
	}

	m = drive(m, pressRune('{'))
	if m.skillAgentIdx != 0 {
		t.Errorf("skillAgentIdx after third { = %d, want 0", m.skillAgentIdx)
	}

	m = drive(m, pressRune('{'))
	if m.skillAgentIdx != 0 {
		t.Errorf("skillAgentIdx after { at min = %d, want 0 (clamped)", m.skillAgentIdx)
	}
}

func TestSkillFilter_SkillAgentIDs(t *testing.T) {
	t.Parallel()
	rows := []app.SkillPackageRow{
		{Agents: []string{"codex", "claude-code"}},
		{Agents: []string{"codex"}},
		{Agents: []string{"gemini"}},
	}
	enabled := []string{"gemini2", "claude-code"}
	ids := skillAgentIDs(rows, enabled)
	if len(ids) != 4 {
		t.Fatalf("skillAgentIDs len = %d, want 4 (union of codex, claude-code, gemini, gemini2), got %v", len(ids), ids)
	}
	for i := 1; i < len(ids); i++ {
		if ids[i] < ids[i-1] {
			t.Errorf("skillAgentIDs not sorted: %v", ids)
		}
	}
	seen := map[string]int{}
	for _, id := range ids {
		seen[id]++
	}
	for _, want := range []string{"codex", "claude-code", "gemini", "gemini2"} {
		if seen[want] != 1 {
			t.Errorf("skillAgentIDs missing or duplicated %q: %v", want, ids)
		}
	}
}

func TestSkillFilter_PillBarListsEnabledAgentsEvenWithNoDeclaredAgents(t *testing.T) {
	t.Parallel()
	m := skillFilterBaseModel()
	m.skillTypeIdx = agentsChipSkills
	m.settings.AgentsUse = []string{"claude-code", "codex"}
	m.enabledAgents = []string{"claude-code", "codex"}
	m.skillsRows = []app.SkillPackageRow{
		{Source: "owner/pkg1", Name: "pkg1", Installed: true},
		{Source: "owner/pkg2", Name: "pkg2", Installed: true},
	}

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	if !strings.Contains(out, "claude-code") {
		t.Errorf("expected agent bar to contain 'claude-code' from enabled agents, got:\n%s", out)
	}
	if !strings.Contains(out, "codex") {
		t.Errorf("expected agent bar to contain 'codex' from enabled agents, got:\n%s", out)
	}
}

func TestSkillFilter_FilterByEnabledAgentKeepsRowsWithEmptyAgents(t *testing.T) {
	t.Parallel()
	m := skillFilterBaseModel()
	m.skillTypeIdx = agentsChipSkills
	m.settings.AgentsUse = []string{"claude-code", "codex"}
	m.enabledAgents = []string{"claude-code", "codex"}
	m.skillsRows = []app.SkillPackageRow{
		{Source: "owner/shared-pkg", Name: "shared-pkg", Installed: true, PerAgentStatus: map[string]app.SkillStatus{"claude-code": app.SkillStatusInstalled, "codex": app.SkillStatusInstalled}},
		{Source: "owner/claude-pkg", Name: "claude-pkg", Agents: []string{"claude-code"}, Installed: true, PerAgentStatus: map[string]app.SkillStatus{"claude-code": app.SkillStatusInstalled}},
	}

	agentIDs := skillAgentIDs(m.skillsRows, m.enabledAgents)
	codexIdx := -1
	for i, id := range agentIDs {
		if id == "codex" {
			codexIdx = i + 1 // +1 because index 0 is "all"
			break
		}
	}
	if codexIdx < 0 {
		t.Fatal("'codex' not found in skillAgentIDs output")
	}
	m.skillAgentIdx = codexIdx

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	if !strings.Contains(out, "shared-pkg") {
		t.Errorf("expected 'shared-pkg' (empty Agents, targets all enabled) to appear when filtering by codex, got:\n%s", out)
	}
	if strings.Contains(out, "claude-pkg") {
		t.Errorf("expected 'claude-pkg' (declares only claude-code) to be hidden when filtering by codex, got:\n%s", out)
	}
}
