package tui

import (
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
)

// One drifted and one clean row for both the mcp and plugin sections, so the same fixture drives every routing case.
func driftedCapabilityModel(t *testing.T) Model {
	t.Helper()
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = true
	m.mcpEnabled = true
	m.pluginsEnabled = true
	m.mcpLoaded = true
	m.pluginLoaded = true
	m.width = 120
	m.cursorHidden = false
	m.enabledAgents = []string{"claude"}
	m.mcpRows = []app.McpServerRow{
		{
			Name: "drifted-srv", Transport: "http", Agents: []string{"claude"},
			PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusDrifted},
			Drifted:        true,
			DriftFields:    map[string][]string{"claude": {"url"}},
		},
		{
			Name: "clean-srv", Transport: "http", Agents: []string{"claude"},
			PerAgentStatus: map[string]app.McpStatus{"claude": app.McpStatusInstalled},
		},
	}
	m.pluginRows = []app.PluginRow{
		{
			Name: "drifted-plugin", Marketplace: "declared", Agents: []string{"claude"},
			PerAgentStatus:    map[string]app.PluginStatus{"claude": app.PluginStatusDrifted},
			Drifted:           true,
			DriftMarketplaces: map[string]string{"claude": "other"},
		},
		{
			Name: "clean-plugin", Marketplace: "declared", Agents: []string{"claude"},
			PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled},
		},
	}
	return m
}

func TestCapabilityRowStatus_DriftedMarks(t *testing.T) {
	t.Parallel()
	if status, mark := mcpAgentRowStatus(app.McpStatusDrifted); status != agentsStatusOutOfSync || mark != agentsMarkDrifted {
		t.Errorf("mcp drifted = %v/%v, want out-of-sync/drifted", status, mark)
	}
	drifted := app.PluginRow{PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusDrifted}}
	if status, mark := pluginAgentRowStatus(drifted, "claude"); status != agentsStatusOutOfSync || mark != agentsMarkDrifted {
		t.Errorf("plugin drifted = %v/%v, want out-of-sync/drifted", status, mark)
	}
	outdated := true
	both := app.PluginRow{
		PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusDrifted},
		PathOutdated:   &outdated,
	}
	if _, mark := pluginAgentRowStatus(both, "claude"); mark != agentsMarkDrifted {
		t.Errorf("drifted+outdated mark = %v, want drifted", mark)
	}
	updates := app.PluginRow{
		PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled},
		PathOutdated:   &outdated,
	}
	if status, mark := pluginAgentRowStatus(updates, "claude"); status != agentsStatusUpdates || mark != agentsMarkNone {
		t.Errorf("outdated plugin = %v/%v, want updates/none", status, mark)
	}
}

func TestCapabilityDriftKeys_ArmAndConfirmOnAllChip(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		section agentsSection
		row     string
		running func(Model) bool
	}{
		{name: "mcp", section: agentsSectionMcp, row: "drifted-srv", running: func(m Model) bool { return m.mcpRunning }},
		{name: "plugins", section: agentsSectionPlugins, row: "drifted-plugin", running: func(m Model) bool { return m.pluginRunning }},
	} {
		m := driftedCapabilityModel(t)
		m.skillTypeIdx = agentsChipAll
		m.agentsAllCursor = agentsAllCursorFor(t, m, tc.section, tc.row)

		armed := drive(m, pressRune('u'))
		if !armed.agentsResolveConfirm || armed.agentsResolveUseLocal {
			t.Fatalf("%s: u on a drifted row = %+v, want an armed use-managed confirm", tc.name, armed)
		}
		if armed.agentsResolveSource != tc.row || armed.agentsResolveFeature != tc.section {
			t.Fatalf("%s: armed %q/%v, want %q/%v", tc.name, armed.agentsResolveSource, armed.agentsResolveFeature, tc.row, tc.section)
		}
		if tc.running(armed) {
			t.Fatalf("%s: arming must not start the resolution", tc.name)
		}

		confirmed := drive(armed, pressRune('u'))
		if confirmed.agentsResolveConfirm || !tc.running(confirmed) {
			t.Fatalf("%s: second u = %+v, want the resolution dispatched", tc.name, confirmed)
		}
	}
}

func TestCapabilityDriftKeys_UseLocalArmsAndOtherKeyCancels(t *testing.T) {
	t.Parallel()
	m := driftedCapabilityModel(t)
	m.skillTypeIdx = agentsChipAll
	m.agentsAllCursor = agentsAllCursorFor(t, m, agentsSectionPlugins, "drifted-plugin")

	armed := drive(m, pressRune('l'))
	if !armed.agentsResolveConfirm || !armed.agentsResolveUseLocal {
		t.Fatalf("l on a drifted plugin row = %+v, want an armed use-local confirm", armed)
	}
	cancelled := drive(armed, pressRune('u'))
	if cancelled.agentsResolveConfirm || cancelled.pluginRunning {
		t.Fatalf("the other side's key must cancel, got %+v", cancelled)
	}
}

// A clean mcp/plugin row keeps the old meanings: l switches chips and no resolution is armed.
func TestCapabilityDriftKeys_OnlyOnDriftedRows(t *testing.T) {
	t.Parallel()
	m := driftedCapabilityModel(t)
	m.skillTypeIdx = agentsChipAll
	m.agentsAllCursor = agentsAllCursorFor(t, m, agentsSectionMcp, "clean-srv")

	got := drive(m, pressRune('l'))
	if got.agentsResolveConfirm {
		t.Fatal("l armed a resolution on a clean row")
	}
	if got.skillTypeIdx == agentsChipAll {
		t.Fatal("l on a clean row must still switch chips")
	}
}

func TestCapabilityDriftKeys_ChipRoutesResolve(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		chip int
		set  func(*Model)
	}{
		{name: "mcp", chip: agentsChipMcp, set: func(m *Model) { m.mcpCursor = 0 }},
		{name: "plugins", chip: agentsChipPlugin, set: func(m *Model) { m.pluginCursor = 0 }},
	} {
		m := driftedCapabilityModel(t)
		m.skillTypeIdx = tc.chip
		tc.set(&m)

		armed := drive(m, pressRune('l'))
		if !armed.agentsResolveConfirm || !armed.agentsResolveUseLocal {
			t.Fatalf("%s chip: l = %+v, want an armed use-local confirm", tc.name, armed)
		}
		if armed.skillTypeIdx != tc.chip {
			t.Fatalf("%s chip: l on a drifted row must not switch chips", tc.name)
		}
	}
}

func TestCapabilityDriftKeys_HintsAndVersionCell(t *testing.T) {
	t.Parallel()
	m := driftedCapabilityModel(t)
	m.skillTypeIdx = agentsChipAll

	rows := agentsAllRowsList(m)
	var drifted agentsAllRow
	for _, row := range rows {
		if row.feature == agentsSectionMcp && row.mark == agentsMarkDrifted {
			drifted = row
		}
	}
	if drifted.sortName != "drifted-srv" {
		t.Fatalf("no drifted mcp row in the flatten: %+v", rows)
	}
	hints := stripANSIEscapeSequences(renderHintItems(m.palette, "", agentsRowHints(m, drifted)))
	for _, want := range []string{"use managed", "use local"} {
		if !strings.Contains(hints, want) {
			t.Fatalf("drifted mcp row hints = %q, want %q", hints, want)
		}
	}
	if strings.Contains(hints, "install") {
		t.Fatalf("drifted mcp row still offers install: %q", hints)
	}
	if got := agentsVersionCellText(m, drifted); got != "drifted" {
		t.Fatalf("version cell = %q, want the drifted marker", got)
	}
}

func agentsAllCursorFor(t *testing.T, m Model, feature agentsSection, name string) int {
	t.Helper()
	for i, row := range agentsAllRowsList(m) {
		if row.feature == feature && row.sortName == name {
			return i
		}
	}
	t.Fatalf("no %v row named %q", feature, name)
	return 0
}
