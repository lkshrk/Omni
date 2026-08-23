package tui

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/apm"
	"github.com/lkshrk/omni/internal/app"
)

func runBatchCmd(cmd tea.Cmd) []tea.Msg {
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		out := make([]tea.Msg, 0, len(batch))
		for _, sub := range batch {
			if sub != nil {
				out = append(out, sub())
			}
		}
		return out
	}
	return []tea.Msg{msg}
}

func TestAgentsOnboardPreviewConfirmationJourney(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	plan := &apm.ImportPlan{SchemaVersion: 1, Coordinator: "omni-v24", OperationID: "0123456789abcdef0123456789abcdef", Items: []apm.ImportItem{{ID: "one"}}, Blockers: []json.RawMessage{}}
	got := drive(m, agentsOnboardPlanDoneMsg{result: app.AgentsOnboardResult{Envelope: apm.ImportEnvelope{SchemaVersion: 1, Plan: plan}}})
	if !got.agentsOnboardConfirm || got.agentsOnboardPlan == nil || !strings.Contains(got.apmOutput, "1 item(s), 0 blocker(s)") {
		t.Fatalf("preview state: %#v", got.agentsOnboardPlan)
	}
	got = drive(got, tea.KeyPressMsg{Code: 'n'})
	if got.agentsOnboardConfirm || got.agentsOnboardPlan != nil || !strings.Contains(got.statusMsg, "cancelled") {
		t.Fatalf("cancel state: confirm=%v status=%q", got.agentsOnboardConfirm, got.statusMsg)
	}
}

func TestAgentsOnboardProjectPreviewNamesReviewedRoot(t *testing.T) {
	plan := &apm.ImportPlan{Scope: "project", ProjectRoot: "/workspace/demo", Items: []apm.ImportItem{}}
	got := onboardPlanSummary(app.AgentsOnboardResult{Envelope: apm.ImportEnvelope{Plan: plan}})
	if !strings.Contains(got, "/workspace/demo") {
		t.Fatalf("summary=%q", got)
	}
}

func TestAgentsOnboardBlockerAndRecoveryError(t *testing.T) {
	m := baseModel(nil)
	plan := &apm.ImportPlan{SchemaVersion: 1, Coordinator: "omni-v24", OperationID: "0123456789abcdef0123456789abcdef", Items: []apm.ImportItem{{ID: "secret", Name: "secret", Classification: "secret-blocked", ReasonCodes: []string{"secret-field:/env/TOKEN"}}}, Blockers: []json.RawMessage{json.RawMessage(`{"reason":"conflict"}`)}}
	got := drive(m, agentsOnboardPlanDoneMsg{result: app.AgentsOnboardResult{Envelope: apm.ImportEnvelope{SchemaVersion: 1, Plan: plan}}})
	if got.agentsOnboardConfirm || !got.statusIsErr || !strings.Contains(got.statusMsg, "blockers") {
		t.Fatalf("blocker state: confirm=%v status=%q", got.agentsOnboardConfirm, got.statusMsg)
	}
	got = drive(got, agentsOnboardApplyDoneMsg{err: errors.New("recoverable partial")})
	if !got.statusIsErr || !strings.Contains(got.statusMsg, "resume") {
		t.Fatalf("recovery status=%q", got.statusMsg)
	}
}

func TestAgentsOnboardResolvesTargetsSecretsExecutablesAndExclusions(t *testing.T) {
	plan := &apm.ImportPlan{Items: []apm.ImportItem{{ID: "target", Classification: "needs-choice", CurrentTargets: []string{"future-agent"}, ProposedTargets: []string{"future-agent"}}, {ID: "secret", Name: "api", Classification: "secret-blocked", ReasonCodes: []string{"secret-field:/env/TOKEN"}}, {ID: "exec", Classification: "importable", ReasonCodes: []string{"executable:bin/run"}}, {ID: "unsupported", Name: "cursor", Classification: "unsupported", ReasonCodes: []string{"native-import-decoder-unavailable"}}, {ID: "conflict", Classification: "conflict", CandidateIDs: []string{"winner", "loser"}}, {ID: "conditional", Classification: "needs-choice", ReasonCodes: []string{"conditional-group-host"}}, {ID: "changed", Classification: "excluded-changed"}}}
	resolveOnboardItem(&plan.Items[0], "1")
	resolveOnboardItem(&plan.Items[1], "m")
	resolveOnboardItem(&plan.Items[2], "E")
	resolveOnboardItem(&plan.Items[3], "x")
	resolveOnboardItem(&plan.Items[4], "o")
	resolveOnboardItem(&plan.Items[5], "x")
	resolveOnboardItem(&plan.Items[6], "x")
	if got := onboardBlockerCount(plan); got != 0 {
		t.Fatalf("blockers=%d plan=%#v", got, plan)
	}
	if plan.Items[1].Resolution.EnvBindings["/env/TOKEN"] != "OMNI_API_SECRET" {
		t.Fatalf("bindings=%v", plan.Items[1].Resolution.EnvBindings)
	}
	if plan.Items[3].Resolution.Decision != "exclude" {
		t.Fatalf("unsupported client decision=%q", plan.Items[3].Resolution.Decision)
	}
	if plan.Items[0].Resolution.ApprovedTargets[0] != "future-agent" || !strings.Contains(onboardTargetChoiceHelp(plan.Items[0]), "1 future-agent") {
		t.Fatalf("dynamic target choice=%#v", plan.Items[0].Resolution)
	}
}

func TestAPMCommandOutputPreservesStdoutAndStderr(t *testing.T) {
	got := apmCommandOutput("installed\n", "warning\n")
	if got != "installed\nwarning" {
		t.Fatalf("output = %q", got)
	}
}

func TestAgentsViewShowsLastAPMResult(t *testing.T) {
	m := Model{width: 100, palette: defaultPalette(), apmCommand: "apm deps list -g", apmOutput: "pkg@1.0.0", apmErr: errors.New("audit failed")}
	view := m.viewSkillsBody()
	for _, want := range []string{"~/.apm/apm.yml", "~/.apm/apm.lock.yaml", "pkg@1.0.0", "audit failed"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestAPMCommandFailureKeepsOutputAndError(t *testing.T) {
	m := baseModel(nil)
	m.apmRunning = true
	got := drive(m, apmCommandDoneMsg{
		command: "apm audit --ci",
		stdout:  "audit report",
		stderr:  "invalid dependency",
		err:     errors.New("exit status 2"),
	})
	if got.apmRunning || got.apmCommand != "apm audit --ci" {
		t.Fatalf("command state = running %v, command %q", got.apmRunning, got.apmCommand)
	}
	if got.apmOutput != "audit report\ninvalid dependency" || got.apmErr == nil {
		t.Fatalf("result = output %q, err %v", got.apmOutput, got.apmErr)
	}
	if !got.statusIsErr {
		t.Fatal("failed APM command reported success")
	}
}
