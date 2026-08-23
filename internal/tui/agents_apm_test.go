package tui

import (
	"errors"
	"slices"
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

func TestAgentsOnboardDeterministicPlanSkipsReviewAndConfirmsApply(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	plan := &app.OnboardPlan{SchemaVersion: 1, OperationID: "0123456789abcdef0123456789abcdef", Items: []app.OnboardItem{{ID: "one", Resolution: app.OnboardResolution{Decision: "migrate"}}}}
	got := drive(m, agentsOnboardPlanDoneMsg{result: app.AgentsOnboardResult{Envelope: app.OnboardEnvelope{Plan: plan}}})
	if !got.agentsOnboardConfirm || got.agentsOnboardPrompt == nil || got.agentsOnboardPrompt.kind != agentsPromptApply || got.agentsOnboardPlan == nil || !strings.Contains(got.apmOutput, "1 item(s), 0 blocker(s)") {
		t.Fatalf("preview state: %#v", got.agentsOnboardPlan)
	}
	got = drive(got, tea.KeyPressMsg{Code: 'n'})
	if got.agentsOnboardConfirm || got.agentsOnboardPrompt != nil || got.agentsOnboardPlan != nil || !strings.Contains(got.statusMsg, "cancelled") {
		t.Fatalf("cancel state: confirm=%v prompt=%#v status=%q", got.agentsOnboardConfirm, got.agentsOnboardPrompt, got.statusMsg)
	}
}

func TestAgentsOnboardDotsOwnershipUsesPopup(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	plan := &app.OnboardPlan{Items: []app.OnboardItem{{
		ID:         "skill:custom",
		Name:       "custom",
		Dots:       &app.OnboardDotsRef{},
		Resolution: app.OnboardResolution{Decision: "keep-in-dots"},
	}}}

	got := drive(m, agentsOnboardPlanDoneMsg{result: app.AgentsOnboardResult{Envelope: app.OnboardEnvelope{Plan: plan}}})
	if got.agentsOnboardPrompt == nil || got.agentsOnboardPrompt.kind != agentsPromptOwnership {
		t.Fatalf("prompt=%#v, want ownership popup", got.agentsOnboardPrompt)
	}
	if got.agentsOnboardPrompt.item != 0 {
		t.Fatalf("prompt item=%d, want 0", got.agentsOnboardPrompt.item)
	}
}

func TestAgentsOnboardOwnershipPopupRendersChoices(t *testing.T) {
	m := baseModel(nil)
	m.mode, m.width, m.height = viewSkills, 100, 30
	plan := &app.OnboardPlan{Items: []app.OnboardItem{{
		ID:         "skill:custom",
		Name:       "custom",
		Dots:       &app.OnboardDotsRef{},
		Resolution: app.OnboardResolution{Decision: "keep-in-dots"},
	}}}

	got := drive(m, agentsOnboardPlanDoneMsg{result: app.AgentsOnboardResult{Envelope: app.OnboardEnvelope{Plan: plan}}})
	view := stripANSIEscapeSequences(got.View().Content)
	for _, want := range []string{"Choose Ownership", "Move to APM", "Keep in dots"} {
		if !strings.Contains(view, want) {
			t.Fatalf("popup missing %q:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"j/k inspect", "m map secret", "decision="} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("popup contains legacy inline control %q:\n%s", unwanted, view)
		}
	}
}

func TestAgentsOnboardMissingTargetsUsesPopup(t *testing.T) {
	m := baseModel(nil)
	plan := &app.OnboardPlan{Items: []app.OnboardItem{{
		ID:            "mcp:custom",
		Name:          "custom",
		TargetOptions: []string{"claude", "codex"},
		Blockers:      []string{"target-resolution-required"},
		Resolution:    app.OnboardResolution{Decision: "migrate"},
	}}}

	got := drive(m, agentsOnboardPlanDoneMsg{result: app.AgentsOnboardResult{Envelope: app.OnboardEnvelope{Plan: plan}}})
	if got.agentsOnboardPrompt == nil || got.agentsOnboardPrompt.kind != agentsPromptTargets {
		t.Fatalf("prompt=%#v, want targets popup", got.agentsOnboardPrompt)
	}
}

func TestAgentsOnboardPromptsOnlyForMissingChoicesInOrder(t *testing.T) {
	m := baseModel(nil)
	plan := &app.OnboardPlan{Items: []app.OnboardItem{
		{
			ID:            "skill:custom",
			Name:          "custom",
			Dots:          &app.OnboardDotsRef{},
			TargetOptions: []string{"claude"},
			Blockers:      []string{"target-resolution-required"},
			Resolution:    app.OnboardResolution{Decision: "keep-in-dots"},
		},
		{
			ID:         "mcp:api",
			Name:       "api",
			Payload:    []byte(`{"env":{"TOKEN":{"blocked":true}}}`),
			Blockers:   []string{"secret-mapping-required"},
			Resolution: app.OnboardResolution{Decision: "migrate"},
		},
	}}

	got := drive(m, agentsOnboardPlanDoneMsg{result: app.AgentsOnboardResult{Envelope: app.OnboardEnvelope{Plan: plan}}})
	if got.agentsOnboardPrompt == nil || got.agentsOnboardPrompt.kind != agentsPromptOwnership {
		t.Fatalf("first prompt=%#v, want ownership", got.agentsOnboardPrompt)
	}
	view := got.viewString()
	if !strings.Contains(view, "Choose Ownership") || strings.Contains(view, "j/k inspect") || strings.Contains(view, "1/2 ") {
		t.Fatalf("onboarding did not use the popup flow:\n%s", view)
	}
	got = drive(got, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got.agentsOnboardPrompt == nil || got.agentsOnboardPrompt.kind != agentsPromptTargets {
		t.Fatalf("second prompt=%#v, want targets", got.agentsOnboardPrompt)
	}
	got = drive(got, tea.KeyPressMsg{Code: tea.KeySpace})
	got = drive(got, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got.agentsOnboardPrompt == nil || got.agentsOnboardPrompt.kind != agentsPromptSecret {
		t.Fatalf("third prompt=%#v, want secret", got.agentsOnboardPrompt)
	}
	got = drive(got, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got.agentsOnboardPrompt == nil || got.agentsOnboardPrompt.kind != agentsPromptApply {
		t.Fatalf("final prompt=%#v, want apply", got.agentsOnboardPrompt)
	}
	items := got.agentsOnboardPlan.Envelope.Plan.Items
	if items[0].Resolution.Decision != "move-to-apm" || !slices.Equal(items[0].Resolution.ApprovedTargets, []string{"claude"}) || items[1].Resolution.EnvBindings["TOKEN"] != "OMNI_API_TOKEN" {
		t.Fatalf("resolutions=%#v", items)
	}
}

func TestAgentsOnboardInvalidEnvironmentNameStaysInSecretPopup(t *testing.T) {
	m := baseModel(nil)
	plan := &app.OnboardPlan{Items: []app.OnboardItem{{
		ID:         "mcp:api",
		Name:       "api",
		Payload:    []byte(`{"env":{"TOKEN":{"blocked":true}}}`),
		Blockers:   []string{"secret-mapping-required"},
		Resolution: app.OnboardResolution{Decision: "migrate"},
	}}}
	got := drive(m, agentsOnboardPlanDoneMsg{result: app.AgentsOnboardResult{Envelope: app.OnboardEnvelope{Plan: plan}}})
	got.settingsInput.SetValue("bad-name")

	got = drive(got, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got.agentsOnboardPrompt == nil || got.agentsOnboardPrompt.kind != agentsPromptSecret {
		t.Fatalf("prompt=%#v, want secret after invalid input", got.agentsOnboardPrompt)
	}
	if !got.statusIsErr || len(got.agentsOnboardPlan.Envelope.Plan.Items[0].Resolution.EnvBindings) != 0 {
		t.Fatalf("statusIsErr=%v bindings=%v", got.statusIsErr, got.agentsOnboardPlan.Envelope.Plan.Items[0].Resolution.EnvBindings)
	}
}

func TestAgentsOnboardSecretPopupMapsEveryMissingField(t *testing.T) {
	m := baseModel(nil)
	plan := &app.OnboardPlan{Items: []app.OnboardItem{{
		ID: "mcp:api", Name: "api", Payload: []byte(`{"env":{"TOKEN":{"blocked":true}},"headers":{"Authorization":{"blocked":true}}}`),
		Blockers: []string{"secret-mapping-required"}, Resolution: app.OnboardResolution{Decision: "migrate"},
	}}}
	got := drive(m, agentsOnboardPlanDoneMsg{result: app.AgentsOnboardResult{Envelope: app.OnboardEnvelope{Plan: plan}}})
	got = drive(got, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got.agentsOnboardPrompt == nil || got.agentsOnboardPrompt.kind != agentsPromptSecret {
		t.Fatalf("second secret prompt=%#v", got.agentsOnboardPrompt)
	}
	got = drive(got, tea.KeyPressMsg{Code: tea.KeyEnter})
	bindings := got.agentsOnboardPlan.Envelope.Plan.Items[0].Resolution.EnvBindings
	if got.agentsOnboardPrompt == nil || got.agentsOnboardPrompt.kind != agentsPromptApply || bindings["TOKEN"] == "" || bindings["Authorization"] == "" {
		t.Fatalf("prompt=%#v bindings=%v", got.agentsOnboardPrompt, bindings)
	}
}

func TestAgentsOnboardGlobalBlockerNeverOffersApply(t *testing.T) {
	m := baseModel(nil)
	plan := &app.OnboardPlan{Items: []app.OnboardItem{{ID: "resolved", Resolution: app.OnboardResolution{Decision: "migrate"}}}, Blockers: []string{"ambiguous-existing-dependencies"}}
	got := drive(m, agentsOnboardPlanDoneMsg{result: app.AgentsOnboardResult{Envelope: app.OnboardEnvelope{Plan: plan}}})
	got = drive(got, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got.agentsOnboardPrompt == nil || got.agentsOnboardPrompt.kind != agentsPromptBlocked || got.agentsOnboardPrompt.item != -1 || got.agentsOnboardConfirm {
		t.Fatalf("global blocker offered apply: prompt=%#v", got.agentsOnboardPrompt)
	}
}

func TestAgentsOnboardPreviewIsLocalMigration(t *testing.T) {
	plan := &app.OnboardPlan{Items: []app.OnboardItem{}}
	got := onboardPlanSummary(app.AgentsOnboardResult{Envelope: app.OnboardEnvelope{Plan: plan}})
	if strings.Contains(got, "project") || !strings.Contains(got, "Agent onboarding preview") {
		t.Fatalf("summary=%q", got)
	}
}

func TestAgentsOnboardSecretBlockerUsesPopup(t *testing.T) {
	m := baseModel(nil)
	plan := &app.OnboardPlan{SchemaVersion: 1, OperationID: "0123456789abcdef0123456789abcdef", Items: []app.OnboardItem{{ID: "secret", Name: "secret", Payload: []byte(`{"env":{"TOKEN":{"blocked":true}}}`), Blockers: []string{"secret-mapping-required"}, Resolution: app.OnboardResolution{Decision: "migrate"}}}, Blockers: []string{"secret-mapping-required"}}
	got := drive(m, agentsOnboardPlanDoneMsg{result: app.AgentsOnboardResult{Envelope: app.OnboardEnvelope{Plan: plan}}})
	if got.agentsOnboardConfirm || got.agentsOnboardPrompt == nil || got.agentsOnboardPrompt.kind != agentsPromptSecret {
		t.Fatalf("prompt=%#v, want secret popup", got.agentsOnboardPrompt)
	}
	if !got.settingsInput.Focused() {
		t.Fatal("secret popup input is not focused")
	}
}

func TestAgentsOnboardRecoveryErrorOffersResume(t *testing.T) {
	m := baseModel(nil)
	got := drive(m, agentsOnboardApplyDoneMsg{err: errors.New("recoverable partial")})
	if !got.statusIsErr || !strings.Contains(got.statusMsg, "resume") {
		t.Fatalf("recovery status=%q", got.statusMsg)
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
