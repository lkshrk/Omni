package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
)

func driftPromptModel(t *testing.T) Model {
	t.Helper()
	var res app.AgentsSyncAllResult
	res.AddSkills(app.RestoreSkillsResult{
		Drift: []string{`o/one: drifted on codex (another tool owns the skill entry; left untouched); resolve with "omni agents skills resolve" (--use-managed / --use-local)`},
	}, nil, nil)
	res.AddMcp(app.RestoreMcpResult{Drift: []string{"codex/ctx7: drifted on codex (url differ from the manifest; left untouched); resolve with x"}}, nil)

	m := agentsAllModel(nil, nil, nil)
	m.height = 40
	got := drive(m, agentsProgressDoneMsg{gen: m.progressGen, skills: true, mcp: true, report: &res})
	if !got.agentsDriftPromptOpen {
		t.Fatal("a run that left drift behind should end on the drift prompt")
	}
	return got
}

func TestAgentsDriftPrompt_RendersBatchKeysAndDropsTheCLIRemedy(t *testing.T) {
	t.Parallel()
	m := driftPromptModel(t)

	out := stripANSIEscapeSequences(m.viewString())
	for _, want := range []string{
		"Drift Detected",
		"2 agent resources drifted from the manifest.",
		"o/one: drifted on codex",
		"U use managed (all)",
		"L use local (all)",
		"esc dismiss",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("drift prompt lost %q, got:\n%s", want, out)
		}
	}
	// The popup's own keys are the remedy; the CLI tail is noise inside it.
	if strings.Contains(out, "omni agents skills resolve") {
		t.Errorf("drift prompt should drop the per-line CLI remedy, got:\n%s", out)
	}
}

// The modal opens unbidden and U is "upgrade all" on the tab underneath, so the batch keys are two-step like the row-scoped resolve.
func TestAgentsDriftPrompt_UppercaseKeysArmBeforeRunningTheBatchResolve(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		key  rune
		side string
	}{{'U', "managed"}, {'L', "local"}} {
		armed := drive(driftPromptModel(t), pressRune(tc.key))
		if !armed.agentsDriftPromptOpen {
			t.Errorf("%c should leave the drift prompt up while armed", tc.key)
		}
		if !armed.agentsBulkResolveConfirm || armed.agentsBulkResolveUseManaged != (tc.key == 'U') {
			t.Errorf("%c should arm the %s side, got confirm=%v useManaged=%v", tc.key, tc.side, armed.agentsBulkResolveConfirm, armed.agentsBulkResolveUseManaged)
		}
		if armed.skillsRunning || armed.mcpRunning || armed.pluginRunning {
			t.Errorf("%c must not write anything on the first press", tc.key)
		}
		if out := stripANSIEscapeSequences(armed.viewString()); !strings.Contains(out, "press "+string(tc.key)+" again") {
			t.Errorf("armed modal should say press-again, got:\n%s", out)
		}

		got := drive(armed, pressRune(tc.key))
		if got.agentsDriftPromptOpen || got.agentsBulkResolveConfirm {
			t.Errorf("second %c should close the drift prompt", tc.key)
		}
		if !got.skillsRunning || !got.mcpRunning || !got.pluginRunning {
			t.Errorf("second %c (use %s for all) should start every feature's resolve", tc.key, tc.side)
		}
	}
}

func TestAgentsDriftPrompt_ArmedBatchResolveIsCancelledByAnyOtherKey(t *testing.T) {
	t.Parallel()
	for _, r := range []rune{'L', 'u', 'S'} {
		armed := drive(driftPromptModel(t), pressRune('U'))
		got := drive(armed, pressRune(r))
		if got.agentsBulkResolveConfirm {
			t.Errorf("%c should cancel the armed batch resolve", r)
		}
		if got.skillsRunning || got.mcpRunning || got.pluginRunning {
			t.Errorf("%c dispatched the batch resolve", r)
		}
		if !got.agentsDriftPromptOpen {
			t.Errorf("%c should leave the drift prompt up", r)
		}
	}
}

func TestAgentsDriftPrompt_ArmedBatchResolveExpires(t *testing.T) {
	t.Parallel()
	armed := drive(driftPromptModel(t), pressRune('U'))
	got := drive(armed, confirmTimeoutMsg{gen: armed.confirmGen})
	if got.agentsBulkResolveConfirm {
		t.Fatal("the armed batch resolve must expire with the confirmation timeout")
	}
	if drive(got, pressRune('U')).skillsRunning {
		t.Fatal("a U after the arm expired should re-arm, not resolve")
	}
}

// U underneath the modal means "upgrade all"; leaving that hint on screen mislabels the modal's own U.
func TestAgentsDriftPrompt_SuppressesTheFooterHintsBehindIt(t *testing.T) {
	t.Parallel()
	out := stripANSIEscapeSequences(driftPromptModel(t).viewString())
	if strings.Contains(out, "U upgrade") {
		t.Errorf("the tab's U hint must not show under the drift prompt, got:\n%s", out)
	}
}

func TestAgentsDriftPrompt_CtrlCReachesTheQuitConfirmation(t *testing.T) {
	t.Parallel()
	m := driftPromptModel(t)
	model, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	armed := model.(Model)
	if !armed.confirmQuit {
		t.Fatal("ctrl+c at the drift prompt should arm the quit confirmation")
	}
	if cmdQuits(cmd) {
		t.Fatal("the first ctrl+c should not quit outright")
	}
	if _, cmd = armed.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}); !cmdQuits(cmd) {
		t.Fatal("the second ctrl+c should quit")
	}
}

func TestAgentsBulkResolveDone_ReportsPartialFailures(t *testing.T) {
	t.Parallel()
	got := drive(driftPromptModel(t), agentsBulkResolveDoneMsg{result: app.BulkDriftResolution{
		SkillsResolved: 1,
		Errors:         []string{"o/one: permission denied", "codex/ctx7: contested"},
	}})

	if !got.statusIsErr {
		t.Errorf("a partially failed batch resolve must not report as success, got %q", got.statusMsg)
	}
	for _, want := range []string{"resolved 1", "2 failed", "o/one: permission denied", "codex/ctx7: contested"} {
		if !strings.Contains(got.statusMsg, want) {
			t.Errorf("status %q lost %q", got.statusMsg, want)
		}
	}
}

func TestAgentsBulkResolveDone_ReportsCleanSuccess(t *testing.T) {
	t.Parallel()
	got := drive(driftPromptModel(t), agentsBulkResolveDoneMsg{result: app.BulkDriftResolution{SkillsResolved: 1, McpResolved: 1}})
	if got.statusIsErr || !strings.Contains(got.statusMsg, "✓ resolved 2") {
		t.Errorf("status = %q (isErr=%v), want the clean success line", got.statusMsg, got.statusIsErr)
	}
}

func TestAgentsDriftPrompt_EscDismissesWithoutResolving(t *testing.T) {
	t.Parallel()
	got := drive(driftPromptModel(t), pressEsc())

	if got.agentsDriftPromptOpen {
		t.Error("esc should dismiss the drift prompt")
	}
	if got.skillsRunning || got.mcpRunning || got.pluginRunning {
		t.Error("esc must not start a resolve")
	}
}

// The modal holding input is what frees U/L: the tab underneath binds U to upgrade-all and lowercase u/l to the row's resolution.
func TestAgentsDriftPrompt_SwallowsTheKeysItShadows(t *testing.T) {
	t.Parallel()
	for _, r := range []rune{'u', 'l', 'S', 'R', 'd'} {
		got := drive(driftPromptModel(t), pressRune(r))
		if !got.agentsDriftPromptOpen {
			t.Errorf("%c should leave the drift prompt up", r)
		}
		if got.skillsRunning || got.mcpRunning || got.pluginRunning || got.agentsSyncAllConfirm || got.agentsResolveConfirm {
			t.Errorf("%c reached the tab underneath the drift prompt", r)
		}
	}
}
