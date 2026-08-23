package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

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
	plan := &app.OnboardPlan{SchemaVersion: 1, OperationID: "0123456789abcdef0123456789abcdef", Items: []app.OnboardItem{{ID: "one", Resolution: app.OnboardResolution{Decision: "migrate"}}}}
	got := drive(m, agentsOnboardPlanDoneMsg{result: app.AgentsOnboardResult{Envelope: app.OnboardEnvelope{Plan: plan}}})
	if got.agentsOnboardConfirm || got.agentsOnboardPlan == nil || !strings.Contains(got.apmOutput, "1 item(s), 0 blocker(s)") {
		t.Fatalf("preview state: %#v", got.agentsOnboardPlan)
	}
	got = drive(got, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !got.agentsOnboardConfirm {
		t.Fatal("reviewed plan did not request confirmation")
	}
	got = drive(got, tea.KeyPressMsg{Code: 'n'})
	if got.agentsOnboardConfirm || got.agentsOnboardPlan != nil || !strings.Contains(got.statusMsg, "cancelled") {
		t.Fatalf("cancel state: confirm=%v status=%q", got.agentsOnboardConfirm, got.statusMsg)
	}
}

func TestAgentsOnboardPreviewIsLocalMigration(t *testing.T) {
	plan := &app.OnboardPlan{Items: []app.OnboardItem{}}
	got := onboardPlanSummary(app.AgentsOnboardResult{Envelope: app.OnboardEnvelope{Plan: plan}})
	if strings.Contains(got, "project") || !strings.Contains(got, "Agent onboarding preview") {
		t.Fatalf("summary=%q", got)
	}
}

func TestAgentsOnboardBlockerAndRecoveryError(t *testing.T) {
	m := baseModel(nil)
	plan := &app.OnboardPlan{SchemaVersion: 1, OperationID: "0123456789abcdef0123456789abcdef", Items: []app.OnboardItem{{ID: "secret", Name: "secret", Blockers: []string{"secret-mapping-required"}, Resolution: app.OnboardResolution{Decision: "migrate"}}}, Blockers: []string{"secret-mapping-required"}}
	got := drive(m, agentsOnboardPlanDoneMsg{result: app.AgentsOnboardResult{Envelope: app.OnboardEnvelope{Plan: plan}}})
	if got.agentsOnboardConfirm || !got.statusIsErr || !strings.Contains(got.statusMsg, "blockers") {
		t.Fatalf("blocker state: confirm=%v status=%q", got.agentsOnboardConfirm, got.statusMsg)
	}
	got = drive(got, agentsOnboardApplyDoneMsg{err: errors.New("recoverable partial")})
	if !got.statusIsErr || !strings.Contains(got.statusMsg, "resume") {
		t.Fatalf("recovery status=%q", got.statusMsg)
	}
}

func TestAgentsOnboardResolvesLocalMigrationChoices(t *testing.T) {
	dots := &app.OnboardDotsRef{}
	plan := &app.OnboardPlan{Items: []app.OnboardItem{
		{ID: "target", TargetOptions: []string{"future/agent"}, Blockers: []string{"target-resolution-required"}, Resolution: app.OnboardResolution{Decision: "migrate"}},
		{ID: "secret", Name: "api", Payload: []byte(`{"env":{"TOKEN":{"blocked":true}}}`), Blockers: []string{"secret-mapping-required"}, Resolution: app.OnboardResolution{Decision: "migrate"}},
		{ID: "unsupported", Name: "custom", Blockers: []string{"unsupported"}, Resolution: app.OnboardResolution{Decision: "migrate"}},
		{ID: "move", Name: "move", Dots: dots, Resolution: app.OnboardResolution{Decision: "keep-in-dots"}},
		{ID: "keep", Name: "keep", Dots: dots, Resolution: app.OnboardResolution{Decision: "move-to-apm"}},
	}}
	resolveOnboardItem(&plan.Items[0], "1")
	resolveOnboardItem(&plan.Items[1], "m")
	resolveOnboardItem(&plan.Items[2], "x")
	resolveOnboardItem(&plan.Items[3], "M")
	resolveOnboardItem(&plan.Items[4], "d")
	if got := onboardBlockerCount(plan); got != 0 {
		t.Fatalf("blockers=%d plan=%#v", got, plan)
	}
	if plan.Items[1].Resolution.EnvBindings["TOKEN"] != "OMNI_API_TOKEN" {
		t.Fatalf("bindings=%v", plan.Items[1].Resolution.EnvBindings)
	}
	if plan.Items[2].Resolution.Decision != "keep-unmanaged" || plan.Items[3].Resolution.Decision != "move-to-apm" || plan.Items[4].Resolution.Decision != "keep-in-dots" {
		t.Fatalf("decisions=%q,%q,%q", plan.Items[2].Resolution.Decision, plan.Items[3].Resolution.Decision, plan.Items[4].Resolution.Decision)
	}
	if plan.Items[0].Resolution.ApprovedTargets[0] != "future/agent" || !strings.Contains(onboardTargetChoiceHelp(plan.Items[0]), "1 future/agent") {
		t.Fatalf("dynamic target choice=%#v", plan.Items[0].Resolution)
	}
}

func TestAgentsOnboardTargetSelectionPreservesDotsOwnership(t *testing.T) {
	dots := &app.OnboardDotsRef{}
	for _, test := range []struct {
		name string
		keys []string
		want []string
	}{
		{name: "move then target", keys: []string{"M", "1"}, want: []string{"claude"}},
		{name: "target then move", keys: []string{"1", "M"}, want: []string{"claude"}},
		{name: "move then all", keys: []string{"M", "a"}, want: []string{"claude", "codex"}},
		{name: "all then move", keys: []string{"a", "M"}, want: []string{"claude", "codex"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			item := app.OnboardItem{Dots: dots, TargetOptions: []string{"claude", "codex"}, Resolution: app.OnboardResolution{Decision: "keep-in-dots"}}
			for _, key := range test.keys {
				if !resolveOnboardItem(&item, key) {
					t.Fatalf("key %q was not handled", key)
				}
			}
			if item.Resolution.Decision != "move-to-apm" || strings.Join(item.Resolution.ApprovedTargets, ",") != strings.Join(test.want, ",") {
				t.Fatalf("resolution=%#v", item.Resolution)
			}
		})
	}
	item := app.OnboardItem{Dots: dots, TargetOptions: []string{"codex"}}
	resolveOnboardItem(&item, "1")
	if item.Resolution.Decision != "" {
		t.Fatalf("target selection invented dots ownership decision %q", item.Resolution.Decision)
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
