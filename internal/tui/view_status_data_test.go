package tui

import (
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
)

func TestStatusAgentsOutOfSyncBreakdown(t *testing.T) {
	t.Parallel()
	if got := statusAgentsOutOfSyncBreakdown(agentsDashCounts{}); got != "" {
		t.Errorf("breakdown of zero counts = %q, want empty", got)
	}

	got := statusAgentsOutOfSyncBreakdown(agentsDashCounts{
		SkillsMissing:         1,
		SkillsUnmanaged:       2,
		McpMissing:            3,
		McpUnmanaged:          4,
		PluginsMissing:        5,
		PluginsUnmanaged:      6,
		MarketplacesMissing:   7,
		MarketplacesUnmanaged: 8,
	})
	for _, want := range []string{
		"1 skill missing", "2 skills unmanaged",
		"3 mcp missing", "4 mcp unmanaged",
		"5 plugins missing", "6 plugins unmanaged",
		"7 marketplaces missing", "8 marketplaces unmanaged",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("breakdown = %q, want it to contain %q", got, want)
		}
	}
}

// Everything is installed and linked, so the only problem is one skill package behind its source and nothing is out of sync.
func agentsOutdatedDashboardModel() Model {
	m := agentsAllModel([]app.SkillPackageRow{
		{
			Name:           "sk-current",
			Source:         "o/sk-current",
			Installed:      true,
			PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled},
		},
		{
			Name:           "sk-behind",
			Source:         "o/sk-behind",
			Installed:      true,
			Outdated:       app.SkillOutdatedBehind,
			PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled},
		},
	}, nil, nil)
	m.mode = viewStatus
	return m
}

// Outdated is disjoint from out-of-sync, so the Agents row keeps saying "in sync" and only the Agent Updates row reports the pending upgrade.
func TestDashboardAgentUpdatesRow_OutdatedOnly(t *testing.T) {
	t.Parallel()
	m := agentsOutdatedDashboardModel()

	counts := statusAgentsCounts(m)
	if counts.Outdated() != 1 {
		t.Fatalf("Outdated() = %d, want 1", counts.Outdated())
	}
	if counts.OutOfSync() != 0 {
		t.Fatalf("OutOfSync() = %d, want 0 — an outdated package is still in sync", counts.OutOfSync())
	}

	row := statusAgentUpdatesAttentionRow(m)
	if !row.needsAttention {
		t.Fatalf("Agent Updates row = %#v, want it to need attention", row)
	}
	if !strings.Contains(stripANSIEscapeSequences(row.value), "1 upgrade") {
		t.Errorf("Agent Updates value = %q, want the pending upgrade count", stripANSIEscapeSequences(row.value))
	}
	if !strings.Contains(row.summary, "sk-behind") {
		t.Errorf("Agent Updates summary = %q, want the outdated package named", row.summary)
	}
	if row.action.kind != statusActionUpgradeAgents {
		t.Errorf("Agent Updates action = %v, want statusActionUpgradeAgents", row.action.kind)
	}

	if agents := statusAgentsAttentionRow(m); agents.needsAttention {
		t.Errorf("Agents sync row = %#v, want it healthy: outdated is not an out-of-sync issue", agents)
	}

	out := stripANSIEscapeSequences(renderStatus(m))
	if !strings.Contains(out, "Agent Updates") {
		t.Errorf("dashboard render missing the Agent Updates row, got:\n%s", out)
	}
	if !strings.Contains(out, "1 upgrade available") {
		t.Errorf("dashboard Agents data line missing the upgrade count, got:\n%s", out)
	}
}

func TestDashboardAgentUpdatesRow_OKWhenNothingOutdated(t *testing.T) {
	t.Parallel()
	m := agentsAllModel([]app.SkillPackageRow{
		{
			Name:           "sk-current",
			Source:         "o/sk-current",
			Installed:      true,
			PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled},
		},
	}, nil, nil)
	m.mode = viewStatus

	row := statusAgentUpdatesAttentionRow(m)
	if row.needsAttention {
		t.Fatalf("Agent Updates row = %#v, want it healthy", row)
	}
	if row.icon != iconInstalled {
		t.Errorf("healthy Agent Updates icon = %q, want %q", row.icon, iconInstalled)
	}
	if row.action.kind != statusActionNone {
		t.Errorf("healthy Agent Updates action = %v, want none", row.action.kind)
	}

	if got := stripANSIEscapeSequences(renderStatus(m)); strings.Contains(got, "upgrade available") {
		t.Errorf("dashboard claims an upgrade with nothing outdated, got:\n%s", got)
	}
}

// Disabled agents already say so once on the Agents row.
func TestDashboardAgentUpdatesRow_AbsentWhenAgentsDisabled(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, nil, nil)
	m.agentsEnabled = false
	m.mode = viewStatus

	for _, row := range statusAttentionRows(m, statusToolCounts(m)) {
		if row.label == "Agent Updates" {
			t.Fatalf("Agent Updates row present with agents disabled: %#v", row)
		}
	}
}

func TestStatusUpgradeSummary(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name                     string
		active, waiting, updates []string
		want                     string
	}{
		{"active only", []string{"rg"}, nil, nil, "upgrading rg"},
		{"active with queue", []string{"rg", "fd", "bat"}, []string{"jq", "yq"}, nil, "upgrading rg, fd, 2 queued"},
		{"waiting only", nil, []string{"jq", "yq", "fzf"}, nil, "3 queued to upgrade"},
		{"updates only", nil, nil, []string{"rg"}, "upgrading outdated tools"},
		{"nothing", nil, nil, nil, "upgrading tools"},
	}
	for _, tc := range cases {
		if got := statusUpgradeSummary(tc.active, tc.waiting, tc.updates); got != tc.want {
			t.Errorf("%s: statusUpgradeSummary = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestStatusOverflowDetails(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)

	if got := statusOverflowDetails(m, []string{"a", "b", "c"}); got != nil {
		t.Errorf("overflow of 3 names = %#v, want nil", got)
	}

	got := statusOverflowDetails(m, []string{"a", "b", "c", "d", "e"})
	if len(got) != 1 || !strings.Contains(got[0], "d, e") {
		t.Errorf("overflow of 5 names = %#v, want one 'More: d, e' line", got)
	}

	names := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	got = statusOverflowDetails(m, names)
	if len(got) != 2 {
		t.Fatalf("overflow of 10 names = %#v, want More line plus +n more line", got)
	}
	if !strings.Contains(got[1], "2 more") {
		t.Errorf("extra line = %q, want it to count the 2 names beyond the 5-name overflow window", got[1])
	}
}

func TestStatusDashboardUpgradeActionable(t *testing.T) {
	t.Parallel()
	quiet := baseModel(nil)
	if statusDashboardUpgradeActionable(quiet) {
		t.Error("no tools → upgrade step should not be actionable")
	}

	outdated := baseModel(oneInstalledOutdated())
	if !statusDashboardUpgradeActionable(outdated) {
		t.Error("an installed outdated tool should make the upgrade step actionable")
	}
}

func TestStatusDashboardFixIgnoreActionable_AndDoctorHasIgnoreFindings(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	if statusDashboardFixIgnoreActionable(m) {
		t.Error("no doctor result → fix-ignore step should not be actionable")
	}
	if doctorHasIgnoreFindings(m) {
		t.Error("no doctor result → no ignore findings")
	}

	m.doctorResult = &app.DoctorResult{Checks: []app.DoctorCheck{
		{ID: "dots.ignore", Status: app.DoctorStatusWarn, Message: "3 tracked files match ignore patterns"},
	}}
	if !doctorHasIgnoreFindings(m) {
		t.Error("a dots.ignore warn check should surface as an ignore finding")
	}
	if !statusDashboardFixIgnoreActionable(m) {
		t.Error("a dots.ignore warn check should make the fix-ignore step actionable")
	}
}
