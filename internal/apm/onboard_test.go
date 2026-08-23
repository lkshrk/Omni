package apm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
)

func TestImportApplyUsesPrivateStdinAndFence(t *testing.T) {
	mock := &executor.MockExecutor{Responses: []executor.MockCall{{Stdout: `{"ok":true,"kind":"import-apply-result","result":{"schema_version":1,"operation_id":"0123456789abcdef0123456789abcdef","coordinator":"omni-v24","state":"awaiting-external-commit","next_action":"external-commit-then-finalize","finalize_token_required":true}}`}}}
	token := []byte(strings.Repeat("x", 32))
	got, _, err := New(mock, Global).ImportApply(context.Background(), ImportRequest{CandidateFile: "/tmp/candidates.json", PlanFile: "/tmp/plan.json", PreimageSet: "hash", FinalizeToken: token})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "awaiting-external-commit" {
		t.Fatalf("state %q", got.State)
	}
	if len(mock.Calls) != 1 || string(mock.Calls[0].Stdin) != string(token) {
		t.Fatalf("private stdin not forwarded: %#v", mock.Calls)
	}
	joined := strings.Join(mock.Calls[0].Args, " ")
	if strings.Contains(joined, string(token)) || !strings.Contains(joined, "--token-stdin") {
		t.Fatalf("unsafe argv: %s", joined)
	}
}

func TestImportProjectPlanUsesReviewedWorkspace(t *testing.T) {
	root := t.TempDir()
	mock := &executor.MockExecutor{Responses: []executor.MockCall{{Stdout: fmt.Sprintf(`{"ok":true,"kind":"import-plan","plan":{"schema_version":1,"coordinator":"omni-v24","plan_id":"%s","resolution_id":"%s","operation_id":"0123456789abcdef0123456789abcdef","scope":"project","project_root":%q,"sources":["vscode"],"candidate_set_id":"x","inventory_fingerprint":"x","items":[],"summary":{},"warnings":[],"blockers":[]}}`, strings.Repeat("a", 64), strings.Repeat("b", 64), root)}}}
	got, _, err := New(mock, Global).ImportPlan(context.Background(), ImportRequest{CandidateFile: filepath.Join(root, "candidates.json"), PreimageSet: "hash", ProjectRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if got.Plan == nil || got.Plan.ProjectRoot != root || len(mock.Calls) != 1 || mock.Calls[0].Dir != root || slices.Contains(mock.Calls[0].Args, "--global") || slices.Contains(mock.Calls[0].Args, "--from") {
		t.Fatalf("plan=%#v call=%#v", got.Plan, mock.Calls)
	}
}

func TestImportSourceNamesAreOpaqueAndSafe(t *testing.T) {
	for _, name := range []string{"cursor", "agent-skills", "intellij", "future-agent", "grok.cloud"} {
		if !ValidImportName(name) {
			t.Fatalf("rejected %q", name)
		}
	}
	for _, name := range []string{"", "bad/name", " bad", "_hidden"} {
		if ValidImportName(name) {
			t.Fatalf("accepted %q", name)
		}
	}
}

func TestImportExplicitFutureSourcePassesThrough(t *testing.T) {
	root := t.TempDir()
	mock := &executor.MockExecutor{Responses: []executor.MockCall{{Stdout: fmt.Sprintf(`{"ok":true,"kind":"import-plan","plan":{"schema_version":1,"coordinator":"omni-v24","plan_id":"%s","resolution_id":"%s","operation_id":"0123456789abcdef0123456789abcdef","scope":"project","project_root":%q,"sources":["future-agent"],"candidate_set_id":"x","inventory_fingerprint":"x","items":[],"summary":{},"warnings":[],"blockers":[]}}`, strings.Repeat("a", 64), strings.Repeat("b", 64), root)}}}
	if _, _, err := New(mock, Global).ImportPlan(context.Background(), ImportRequest{CandidateFile: filepath.Join(root, "candidates.json"), PreimageSet: "hash", Sources: []string{"future-agent"}, ProjectRoot: root}); err != nil {
		t.Fatal(err)
	}
	args := mock.Calls[0].Args
	for i := range args[:len(args)-1] {
		if args[i] == "--from" && args[i+1] == "future-agent" {
			return
		}
	}
	t.Fatalf("future source not passed through: %v", args)
}

func TestImportProjectScopeValidation(t *testing.T) {
	root := t.TempDir()
	valid := ImportPlan{SchemaVersion: 1, Coordinator: "omni-v24", Scope: "project", ProjectRoot: root}
	if err := ValidateImportPlanProtocol(valid); err != nil {
		t.Fatal(err)
	}
	for _, plan := range []ImportPlan{
		{SchemaVersion: 1, Coordinator: "omni-v24", Scope: "project"},
		{SchemaVersion: 1, Coordinator: "omni-v24", Scope: "global", ProjectRoot: root},
		{SchemaVersion: 1, Coordinator: "omni-v24", Scope: "surprise"},
	} {
		if err := ValidateImportPlanProtocol(plan); err == nil {
			t.Fatalf("accepted %#v", plan)
		}
	}
}

func TestImportProjectRecoveryCommandsUseReviewedWorkspace(t *testing.T) {
	root := t.TempDir()
	op := "0123456789abcdef0123456789abcdef"
	result := func(kind, state, next string, token bool) executor.MockCall {
		return executor.MockCall{Stdout: fmt.Sprintf(`{"ok":true,"kind":%q,"result":{"schema_version":1,"operation_id":%q,"coordinator":"omni-v24","state":%q,"next_action":%q,"finalize_token_required":%t}}`, kind, op, state, next, token)}
	}
	mock := &executor.MockExecutor{Responses: []executor.MockCall{
		result(ImportKindStatus, "recoverable-partial", "resume", false),
		result(ImportKindResume, "awaiting-external-commit", "external-commit-then-finalize", true),
		result(ImportKindFinalize, "complete", "none", false),
		result(ImportKindRollback, "rolled-back", "none", false),
		result(ImportKindCleanup, "complete", "none", false),
	}}
	client := New(mock, Global)
	token := []byte(strings.Repeat("x", 32))
	if _, _, err := client.ImportStatusAt(context.Background(), op, root); err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.ImportResume(context.Background(), ImportRequest{OperationID: op, CandidateFile: filepath.Join(root, "candidates.json"), PlanFile: filepath.Join(root, "plan.json"), PreimageSet: "hash", FinalizeToken: token, ProjectRoot: root}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.ImportFinalize(context.Background(), ImportRequest{OperationID: op, PreimageSet: "hash", FinalizeToken: token, ProjectRoot: root}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.ImportRollback(context.Background(), op, root); err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.ImportCleanupAt(context.Background(), op, root); err != nil {
		t.Fatal(err)
	}
	for _, call := range mock.Calls {
		if call.Dir != root {
			t.Fatalf("call %v dir=%q", call.Args, call.Dir)
		}
	}
}

func TestImportPlanRejectsUnknownClassification(t *testing.T) {
	mock := &executor.MockExecutor{Responses: []executor.MockCall{{Stdout: `{"ok":true,"kind":"import-plan","plan":{"schema_version":1,"coordinator":"omni-v24","plan_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","resolution_id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","operation_id":"0123456789abcdef0123456789abcdef","scope":"global","sources":[],"candidate_set_id":"x","inventory_fingerprint":"x","items":[{"id":"x","candidate_ids":[],"kind":"skill","name":"x","classification":"surprise","proposed_action":"none","current_targets":[],"proposed_targets":[],"proposed_destination":"","reason_codes":[],"resolution":{}}],"summary":{},"warnings":[],"blockers":[]}}`}}}
	_, _, err := New(mock, Global).ImportPlan(context.Background(), ImportRequest{CandidateFile: "/tmp/candidates.json", PreimageSet: "hash"})
	if err == nil || !strings.Contains(err.Error(), "classification") {
		t.Fatalf("err=%v", err)
	}
}

func TestImportPlanRejectsCanonicalRawPlanWithoutEnvelope(t *testing.T) {
	mock := &executor.MockExecutor{Responses: []executor.MockCall{{Stdout: `{"schema_version":1,"coordinator":"omni-v24","operation_id":"0123456789abcdef0123456789abcdef","scope":"global","sources":["omni-v24"],"candidate_set_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","inventory_fingerprint":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","items":[],"summary":{},"warnings":[],"blockers":[]}`}}}
	_, _, err := New(mock, Global).ImportPlan(context.Background(), ImportRequest{CandidateFile: "/tmp/candidates.json", PreimageSet: "hash"})
	if err == nil || !strings.Contains(err.Error(), "envelope") {
		t.Fatalf("err=%v", err)
	}
}

func TestImportProtocolRejectsMissingOrUnknownEnvelopeFields(t *testing.T) {
	for name, stdout := range map[string]string{
		"missing ok":    `{"kind":"import-plan","plan":{}}`,
		"missing kind":  `{"ok":true,"plan":{}}`,
		"wrong wrapper": `{"ok":true,"kind":"import-plan","result":{}}`,
		"unknown field": `{"ok":true,"kind":"import-plan","plan":{},"extra":1}`,
	} {
		t.Run(name, func(t *testing.T) {
			mock := &executor.MockExecutor{Responses: []executor.MockCall{{Stdout: stdout}}}
			_, _, err := New(mock, Global).ImportPlan(context.Background(), ImportRequest{CandidateFile: "/tmp/candidates.json", PreimageSet: "hash"})
			if err == nil {
				t.Fatal("invalid envelope accepted")
			}
		})
	}
}

func TestImportFailureRequiresCanonicalErrorEnvelope(t *testing.T) {
	mock := &executor.MockExecutor{Responses: []executor.MockCall{{Stdout: `{"ok":false,"kind":"import-error","error":{"code":"protocol-incompatible","message":"stale","operation_id":"0123456789abcdef0123456789abcdef"}}`, Err: errors.New("exit status 4")}}}
	envelope, _, err := New(mock, Global).ImportPlan(context.Background(), ImportRequest{CandidateFile: "/tmp/candidates.json", PreimageSet: "hash"})
	if err == nil || envelope.Kind != ImportKindError || envelope.Code != "protocol-incompatible" {
		t.Fatalf("envelope=%#v err=%v", envelope, err)
	}
	mock = &executor.MockExecutor{Responses: []executor.MockCall{{Stdout: `{"schema_version":1,"error":{"code":"stale-plan","message":"stale"}}`, Err: errors.New("exit status 4")}}}
	if _, _, err := New(mock, Global).ImportPlan(context.Background(), ImportRequest{CandidateFile: "/tmp/candidates.json", PreimageSet: "hash"}); err == nil || !strings.Contains(err.Error(), "envelope") {
		t.Fatalf("err=%v", err)
	}
}

func TestImportResultRequiresFinalizeBoolean(t *testing.T) {
	mock := &executor.MockExecutor{Responses: []executor.MockCall{{Stdout: `{"ok":true,"kind":"import-status-result","result":{"schema_version":1,"operation_id":"0123456789abcdef0123456789abcdef","coordinator":"omni-v24","state":"complete","next_action":"none"}}`}}}
	if _, _, err := New(mock, Global).ImportStatus(context.Background(), "0123456789abcdef0123456789abcdef"); err == nil || !strings.Contains(err.Error(), "finalize_token_required") {
		t.Fatalf("err=%v", err)
	}
}

func TestImportPlanIdentityBindsResolution(t *testing.T) {
	plan := ImportPlan{SchemaVersion: 1, Coordinator: "omni-v24", Scope: "global", Sources: []string{"omni-v24"}, CandidateSetID: strings.Repeat("c", 64), InventoryFingerprint: strings.Repeat("i", 64), Items: []ImportItem{{ID: "item", CandidateIDs: []string{"candidate"}, Kind: "skill", Name: "demo", Classification: "needs-choice", ProposedAction: "block", CurrentTargets: []string{"claude", "codex"}, ProposedTargets: []string{"claude", "codex"}, ReasonCodes: []string{"legacy-unscoped-targets"}, Resolution: ImportResolution{}}}, Summary: map[string]int{"needs-choice": 1}, Warnings: []json.RawMessage{}, Blockers: []json.RawMessage{}}
	if err := BindImportPlanResolution(&plan); err != nil {
		t.Fatal(err)
	}
	firstOp := plan.OperationID
	plan.Items[0].Resolution = ImportResolution{Decision: "import", ApprovedTargets: []string{"codex"}}
	if err := BindImportPlanResolution(&plan); err != nil {
		t.Fatal(err)
	}
	if plan.OperationID == firstOp {
		t.Fatal("resolution did not change operation identity")
	}
	if err := ValidateImportPlanBinding(plan); err != nil {
		t.Fatal(err)
	}
	plan.Items[0].Name = "tampered"
	if err := ValidateImportPlanBinding(plan); err == nil {
		t.Fatal("immutable plan tamper accepted")
	}
}
