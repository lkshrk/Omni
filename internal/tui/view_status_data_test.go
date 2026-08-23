package tui

import (
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
)

// Everything is installed and linked, so the only problem is one skill package behind its source and nothing is out of sync.

// Outdated is disjoint from out-of-sync, so the Agents row keeps saying "in sync" and only the Agent Updates row reports the pending upgrade.

// Disabled agents already say so once on the Agents row.

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

func TestAgentDashboardUsesOnlyAPMWorkspaceStatus(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.apmOutput = "workspace healthy"

	if got := statusAgentsCounts(m); got.OutOfSync() != 0 || got.Outdated() != 0 {
		t.Fatalf("agent counts = %#v, want no native lifecycle counters", got)
	}
	details := statusAgentsOverviewDetails(m, agentsDashboardViewFor(m))
	if len(details) != 1 || !strings.Contains(details[0], "workspace healthy") {
		t.Fatalf("agent details = %#v, want APM workspace output", details)
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
