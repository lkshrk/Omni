package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
)

// One contested skill package, one drifted mcp server, and one package failing outright in a single run.
func mixedComposedReport() *app.AgentsSyncAllResult {
	var res app.AgentsSyncAllResult
	res.AddSkillsImport(app.ImportDiff{Added: []string{"o/claimed"}}, nil)
	res.AddSkills(app.RestoreSkillsResult{
		Installed: []string{"o/one"},
		Failed:    []app.SkillFailure{{Name: "o/bad", Message: "unreachable"}},
		Drift:     []string{"o/one: drifted on codex"},
		Warnings:  []string{"legacy install remains"},
	}, nil, nil)
	res.AddMcp(app.RestoreMcpResult{Drift: []string{"codex/ctx7: url differs"}}, nil)
	res.AddPlugins(app.RestorePluginResult{Installed: []string{"claude-code/plug"}}, nil)
	return &res
}

// The run outcome belongs in the footer and the drift modal; a per-run block above the rows pushed the table down and outlived the moment it described.
func TestAgentsComposedRun_LeavesNoReportBlockAboveTheRows(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, nil, nil)
	got := drive(m, agentsProgressDoneMsg{
		gen:       m.progressGen,
		skills:    true,
		mcp:       true,
		plugin:    true,
		skillsErr: errors.New("o/bad: unreachable"),
		report:    mixedComposedReport(),
	})

	body := stripANSIEscapeSequences(got.viewSkillsBody())
	for _, unwanted := range []string{
		"imported: 1",
		"skills: 1 installed",
		"mcp: 0 installed",
		"plugins: 1 installed",
		"warn: skills: legacy install remains",
		"drifted: 2",
	} {
		if strings.Contains(body, unwanted) {
			t.Errorf("agents tab still renders %q above the rows, got:\n%s", unwanted, body)
		}
	}
	// A real per-feature failure keeps its own error line; only the run report went away.
	if !strings.Contains(body, "error: o/bad: unreachable") {
		t.Errorf("agents tab lost the skills error line, got:\n%s", body)
	}
	if !got.agentsDriftPromptOpen {
		t.Error("a run with drift should still raise the bulk resolve prompt")
	}
}

func TestAgentsComposedRun_CleanRunSetsTheSummaryStatus(t *testing.T) {
	t.Parallel()
	m := agentsAllModel(nil, nil, nil)
	var res app.AgentsSyncAllResult
	res.AddSkills(app.RestoreSkillsResult{Installed: []string{"o/one"}}, nil, nil)
	got := drive(m, agentsProgressDoneMsg{gen: m.progressGen, skills: true, report: &res})

	if got.statusIsErr {
		t.Error("a clean run must not set an error status")
	}
	if !strings.Contains(got.statusMsg, "1 skills installed") {
		t.Errorf("statusMsg = %q, want the composed run summary", got.statusMsg)
	}
}
