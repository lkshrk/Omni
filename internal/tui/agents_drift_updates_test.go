package tui

import (
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
)

// One managed package taken over on codex while its source moved on: the row sits in one section, so the update must survive outside the row's status.
func driftedOutdatedModel() Model {
	m := agentsAllModel([]app.SkillPackageRow{{
		Name:      "caveman",
		Source:    "github.com/foo/caveman",
		Installed: false,
		Outdated:  app.SkillOutdatedBehind,
		PerAgentStatus: map[string]app.SkillStatus{
			"claude-code": app.SkillStatusInstalled,
			"codex":       app.SkillStatusDrifted,
		},
	}}, nil, nil)
	m.enabledAgents = []string{"claude-code", "codex"}
	return m
}

func TestAgentsDashCounts_DriftedPackageStillCountsAsAnUpdate(t *testing.T) {
	t.Parallel()
	m := driftedOutdatedModel()
	counts := agentsDashCountsFromRows(agentsAllRowsList(m))
	if counts.SkillsDrifted != 1 {
		t.Errorf("SkillsDrifted = %d, want 1", counts.SkillsDrifted)
	}
	if counts.SkillsOutdated != 1 {
		t.Errorf("SkillsOutdated = %d, want the pending upgrade counted alongside the drift", counts.SkillsOutdated)
	}
	if counts.Outdated() != 1 {
		t.Errorf("Outdated() = %d, want the Agent Updates row to report the upgrade", counts.Outdated())
	}
	if names := statusAgentsOutdatedNames(m); len(names) != 1 || names[0] != "caveman" {
		t.Errorf("outdated names = %v, want [caveman]", names)
	}
}

// Settling the drift changes only the sync bucket; the upgrade was there all along and its count must not move.
func TestAgentsDashCounts_ResolvingDriftLeavesTheUpdateCount(t *testing.T) {
	t.Parallel()
	m := driftedOutdatedModel()
	before := agentsDashCountsFromRows(agentsAllRowsList(m))

	m.skillsRows[0].Installed = true
	m.skillsRows[0].PerAgentStatus["codex"] = app.SkillStatusInstalled
	after := agentsDashCountsFromRows(agentsAllRowsList(m))

	if after.SkillsDrifted != 0 {
		t.Errorf("SkillsDrifted = %d after resolving, want 0", after.SkillsDrifted)
	}
	if after.SkillsOutdated != before.SkillsOutdated {
		t.Errorf("SkillsOutdated moved from %d to %d, want the same count", before.SkillsOutdated, after.SkillsOutdated)
	}
}

// A package installed nowhere has nothing to upgrade, so it stays out of the update count.
func TestAgentsDashCounts_MissingPackageIsNotAnUpdate(t *testing.T) {
	t.Parallel()
	m := agentsAllModel([]app.SkillPackageRow{{
		Name:           "caveman",
		Source:         "github.com/foo/caveman",
		Outdated:       app.SkillOutdatedBehind,
		PerAgentStatus: map[string]app.SkillStatus{"claude-code": app.SkillStatusMissing},
	}}, nil, nil)
	m.enabledAgents = []string{"claude-code"}

	counts := agentsDashCountsFromRows(agentsAllRowsList(m))
	if counts.SkillsMissing != 1 || counts.SkillsOutdated != 0 {
		t.Fatalf("missing=%d outdated=%d, want 1 and 0", counts.SkillsMissing, counts.SkillsOutdated)
	}
}

func TestAgentsDashCounts_DriftedPluginAcrossTwoAgentsCountsOnce(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, nil, []app.PluginRow{{
		Name:        "caveman",
		Marketplace: "foo",
		Drifted:     true,
		PerAgentStatus: map[string]app.PluginStatus{
			"claude-code": app.PluginStatusDrifted,
			"codex":       app.PluginStatusDrifted,
		},
	}})
	m.enabledAgents = []string{"claude-code", "codex"}

	counts := agentsDashCountsFromRows(agentsAllRowsList(m))
	if counts.PluginsDrifted != 1 {
		t.Errorf("PluginsDrifted = %d, want the item counted once across both agents", counts.PluginsDrifted)
	}
	if counts.OutOfSync() != 1 {
		t.Errorf("OutOfSync() = %d, want 1", counts.OutOfSync())
	}
}

func TestAgentsDashCounts_MixedItemsEachCountOnce(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(
		[]app.SkillPackageRow{{
			Name:           "sk-missing",
			Source:         "o/sk-missing",
			PerAgentStatus: map[string]app.SkillStatus{"claude-code": app.SkillStatusMissing, "codex": app.SkillStatusMissing},
		}},
		[]app.McpServerRow{{
			Name:      "mcp-missing",
			Transport: "stdio",
			Command:   "mcp-missing-bin",
			PerAgentStatus: map[string]app.McpStatus{
				"claude-code": app.McpStatusMissing,
				"codex":       app.McpStatusMissing,
			},
		}, {
			Name:      "mcp-drifted",
			Transport: "stdio",
			Command:   "mcp-drifted-bin",
			Drifted:   true,
			PerAgentStatus: map[string]app.McpStatus{
				"claude-code": app.McpStatusDrifted,
				"codex":       app.McpStatusDrifted,
			},
		}},
		[]app.PluginRow{{
			Name:        "pl-missing",
			Marketplace: "foo",
			PerAgentStatus: map[string]app.PluginStatus{
				"claude-code": app.PluginStatusMissing,
				"codex":       app.PluginStatusMissing,
			},
		}},
	)
	m.enabledAgents = []string{"claude-code", "codex"}

	counts := agentsDashCountsFromRows(agentsAllRowsList(m))
	if counts.SkillsMissing != 1 || counts.McpMissing != 1 || counts.McpDrifted != 1 || counts.PluginsMissing != 1 {
		t.Errorf("counts = %+v, want each item counted once in its bucket", counts)
	}
	if counts.OutOfSync() != 4 {
		t.Errorf("OutOfSync() = %d, want 4 (one per item)", counts.OutOfSync())
	}
}

// The row renders in Out of Sync, so the Updates Available section never names it and the row itself must say an upgrade is waiting.
func TestAgentsRow_DriftedAndOutdatedRowShowsTheUpgrade(t *testing.T) {
	t.Parallel()
	m := driftedOutdatedModel()
	out := stripANSIEscapeSequences(m.viewSkillsBody())
	if !strings.Contains(out, "drifted · upgrade") {
		t.Errorf("drifted+outdated row lost its upgrade marker, got:\n%s", out)
	}

	details := stripANSIEscapeSequences(strings.Join(skillDetailLines(m, m.skillsRows[0]), "\n"))
	if !strings.Contains(details, "upgrade available") {
		t.Errorf("drifted+outdated detail = %q, want it to name the pending upgrade", details)
	}
}

func TestAgentsRow_DriftedOnlyRowKeepsThePlainMarker(t *testing.T) {
	t.Parallel()
	m := driftedOutdatedModel()
	m.skillsRows[0].Outdated = app.SkillOutdatedCurrent
	out := stripANSIEscapeSequences(m.viewSkillsBody())
	if strings.Contains(out, "drifted · upgrade") {
		t.Errorf("up-to-date drifted row claimed an upgrade, got:\n%s", out)
	}
	if !strings.Contains(out, "drifted") {
		t.Errorf("drifted row lost its marker, got:\n%s", out)
	}
}
