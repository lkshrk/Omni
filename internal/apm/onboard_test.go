package apm

import (
	"context"
	"encoding/json"
	"errors"
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
