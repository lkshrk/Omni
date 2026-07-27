package tui

import (
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
)

func agentsFilterModel() Model {
	m := agentsAllModel(
		[]app.SkillPackageRow{
			{Name: "claude-only", Source: "acme/claude-only", Installed: true, Agents: []string{"claude-code"}},
			{Name: "codex-only", Source: "acme/codex-only", Installed: true, Agents: []string{"codex"}},
		},
		[]app.McpServerRow{{Name: "mcp-widget", PerAgentStatus: map[string]app.McpStatus{"claude-code": app.McpStatusInstalled}}},
		[]app.PluginRow{{Name: "plugin-widget", PerAgentStatus: map[string]app.PluginStatus{"claude-code": app.PluginStatusInstalled}}},
	)
	m.enabledAgents = []string{"claude-code", "codex"}
	// Lockfile rows declare no agent list — only the per-agent map says where the package actually landed.
	m.skillsUnmanagedRows = []app.SkillPackageRow{{
		Name:           "legacy-src",
		Source:         "/seed/legacy-src",
		Installed:      true,
		PerAgentStatus: map[string]app.SkillStatus{"codex": app.SkillStatusInstalled, "claude-code": app.SkillStatusMissing},
	}}
	return m
}

func visibleRowNames(m Model) []string {
	rows, _, _ := skillsVisibleRows(m)
	names := make([]string, 0, len(rows))
	for _, r := range rows {
		names = append(names, r.Name)
	}
	return names
}

func agentFilterIdxFor(m Model, id string) int {
	for i, got := range skillAgentIDs(m.skillsRows, m.enabledAgents) {
		if got == id {
			return i + 1
		}
	}
	return 0
}

func TestSkillsAgentFilterExcludesOtherAgentsPackages(t *testing.T) {
	t.Parallel()
	m := agentsFilterModel()
	m.skillAgentIdx = agentFilterIdxFor(m, "claude-code")

	got := visibleRowNames(m)
	for _, unwanted := range []string{"codex-only", "legacy-src"} {
		if slicesContains(got, unwanted) {
			t.Errorf("filtering to claude-code still lists %q: %v", unwanted, got)
		}
	}
	if !slicesContains(got, "claude-only") {
		t.Errorf("filtering to claude-code dropped its own package: %v", got)
	}
}

func TestSkillsAgentFilterKeepsUnmanagedRowOnItsOwnAgent(t *testing.T) {
	t.Parallel()
	m := agentsFilterModel()
	m.skillAgentIdx = agentFilterIdxFor(m, "codex")

	got := visibleRowNames(m)
	if !slicesContains(got, "legacy-src") {
		t.Errorf("codex filter dropped the lockfile package installed for codex: %v", got)
	}
	if slicesContains(got, "claude-only") {
		t.Errorf("codex filter still lists a claude-only package: %v", got)
	}
}

func TestAgentsSearchFilterNarrowsEverySection(t *testing.T) {
	t.Parallel()
	m := agentsFilterModel()
	m.skillsSearchActive = true
	m.filter.SetValue("widget")

	if got := visibleRowNames(m); len(got) != 0 {
		t.Errorf("query %q should match no skill rows, got %v", "widget", got)
	}

	names := make([]string, 0)
	for _, e := range agentsAllRowsList(m) {
		names = append(names, e.sortName)
	}
	for _, want := range []string{"mcp-widget", "plugin-widget"} {
		if !slicesContains(names, want) {
			t.Errorf("query %q dropped matching row %q: %v", "widget", want, names)
		}
	}
	for _, unwanted := range []string{"claude-only", "codex-only", "legacy-src"} {
		if slicesContains(names, unwanted) {
			t.Errorf("query %q still lists non-matching row %q: %v", "widget", unwanted, names)
		}
	}
}

func TestAgentsSearchFilterNarrowsRenderedRows(t *testing.T) {
	t.Parallel()
	m := agentsFilterModel()
	m.skillsSearchActive = true
	m.filter.SetValue("legacy")

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	if !strings.Contains(out, "legacy-src") {
		t.Errorf("query %q should keep the matching row:\n%s", "legacy", out)
	}
	for _, unwanted := range []string{"mcp-widget", "plugin-widget", "claude-only"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("query %q still renders %q:\n%s", "legacy", unwanted, out)
		}
	}
}

func slicesContains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
