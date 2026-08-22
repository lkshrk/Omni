package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
)

func TestInitOnboardingReadOnlyCreatesNothing(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "missing", "settings.json")
	state := filepath.Join(root, "state")
	cache := filepath.Join(root, "cache")
	a := New(configPath)
	a.StateDir, a.CacheDir = state, cache
	if err := a.InitOnboardingReadOnly(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Dir(configPath), state, cache} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("read-only init created %s", path)
		}
	}
}

func TestInitDetectsJoinedOnboardingRecoveryBeforeMutation(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		t.Run(fmt.Sprintf("readOnly=%v", readOnly), func(t *testing.T) {
			root := t.TempDir()
			configPath := filepath.Join(root, "settings.json")
			cache := filepath.Join(root, "cache")
			state := filepath.Join(root, "state")
			if err := os.WriteFile(configPath, []byte(`{"version":24}`), 0o600); err != nil {
				t.Fatal(err)
			}
			stateRoot, err := onboardingRoot(state)
			if err != nil {
				t.Fatal(err)
			}
			op := "0123456789abcdef0123456789abcdef"
			opRoot, err := stateRoot.Child(op)
			if err != nil {
				t.Fatal(err)
			}
			journal := onboardJournal{SchemaVersion: 1, OperationID: op, PlanID: strings.Repeat("a", 64), ResolutionID: strings.Repeat("b", 64), CandidateSetID: strings.Repeat("c", 64), Phase: "apm-applied"}
			if err := writeOnboardJournal(opRoot, journal); err != nil {
				t.Fatal(err)
			}
			mock := &executor.MockExecutor{Responses: []executor.MockCall{{Stdout: "APM CLI version 0.28.0+omni.3\n"}, {Stdout: `{"ok":true,"kind":"import-status-result","result":{"schema_version":1,"operation_id":"0123456789abcdef0123456789abcdef","coordinator":"omni-v24","state":"awaiting-external-commit","next_action":"external-commit-then-finalize","finalize_token_required":true}}`}}}
			a := New(configPath)
			a.StateDir = state
			a.CacheDir = cache
			a.SetFallbackExecutor(mock)
			if readOnly {
				err = a.InitReadOnly(context.Background())
			} else {
				err = a.Init(context.Background())
			}
			var recovery *OnboardingRecoveryError
			if !errors.As(err, &recovery) {
				t.Fatalf("err=%v", err)
			}
			if _, err := os.Stat(cache); !os.IsNotExist(err) {
				t.Fatalf("cache mutated: %v", err)
			}
			if _, err := os.Stat(configPath + settingsBackupSuffix); !os.IsNotExist(err) {
				t.Fatalf("backup created: %v", err)
			}
		})
	}
}

func TestExtractLegacyCandidatesNestedIncludesAndSecrets(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "settings.json")
	one := filepath.Join(dir, "one.json")
	two := filepath.Join(dir, "two.json")
	mustWrite := func(path, data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(root, `{"version":23,"$include":["one.json"],"settings":{"dots_repo":"keep"},"agents":{"packages":[{"source":"https://user:TOPSECRET@example/pkg","agents":["codex"]}]}}`)
	mustWrite(one, `{"$include":["two.json"],"agents":{"skills":[{"name":"review","source":"https://example/skills","agents":["claude-code"]}]}}`)
	mustWrite(two, `{"agents":{"mcp_servers":[{"name":"api","transport":"http","url":"https://user:TOPSECRET@example.test","headers":{"Authorization":"TOPSECRET"},"agents":["codex"]}]}}`)
	first, err := ExtractLegacyCandidates(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ExtractLegacyCandidates(root)
	if err != nil {
		t.Fatal(err)
	}
	if first.Envelope.CandidateSetID != second.Envelope.CandidateSetID || len(first.Envelope.Candidates) != 3 {
		t.Fatalf("unstable/incomplete candidates: %#v", first.Envelope)
	}
	encoded, _ := json.Marshal(first.Envelope)
	if strings.Contains(string(encoded), "TOPSECRET") || !strings.Contains(string(encoded), "literal-secret") {
		t.Fatalf("secret leak/redaction missing: %s", encoded)
	}
	if len(first.Documents) != 3 {
		t.Fatalf("documents=%v", first.Documents)
	}
}

func TestExtractLegacyCandidatesCycle(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "settings.json")
	child := filepath.Join(dir, "child.json")
	_ = os.WriteFile(root, []byte(`{"version":22,"$include":["child.json"]}`), 0o600)
	_ = os.WriteFile(child, []byte(`{"$include":["settings.json"]}`), 0o600)
	if _, err := ExtractLegacyCandidates(root); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("err=%v", err)
	}
}

func TestExtractLegacyCandidatesV24UsesEmptyArrays(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"version":24}`), 0o600); err != nil {
		t.Fatal(err)
	}
	inventory, err := ExtractLegacyCandidates(path)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(inventory.Envelope)
	if strings.Contains(string(data), `"candidates":null`) || strings.Contains(string(data), `"source_preimages":null`) {
		t.Fatalf("invalid envelope: %s", data)
	}
}

func TestExtractLegacyCandidatesMarksUnscopedTargetsForChoice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"version":23,"agents":{"skills":[{"name":"x","source":"https://example/x"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	inventory, err := ExtractLegacyCandidates(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Envelope.Candidates) != 1 || !strings.Contains(string(inventory.Envelope.Candidates[0].Payload), "target_resolution_required") {
		t.Fatalf("candidate=%#v", inventory.Envelope.Candidates)
	}
}

func TestExtractLegacyCandidatesResolvesActiveGroupsAndEmitsConditionalChoices(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	dir := t.TempDir()
	root := filepath.Join(dir, "settings.json")
	nested := filepath.Join(dir, "nested.json")
	rootJSON := `{"version":22,"$include":["nested.json"],"hosts":{"testhost":["active"]},"agents":{"skills":[{"name":"active-skill","source":"https://example/active","agents":["codex"]}]},"groups":[{"name":"active","skills":["active-skill"]}]}`
	nestedJSON := `{"groups":[{"name":"inactive","plugins":["later"]}],"host_settings":{"other":{"skills_disabled":true}}}`
	if err := os.WriteFile(root, []byte(rootJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nested, []byte(nestedJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	inventory, err := ExtractLegacyCandidates(root)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, candidate := range inventory.Envelope.Candidates {
		kinds[candidate.Name] = candidate.Kind
	}
	if kinds["active-skill"] != "skill" || kinds["inactive"] != "unsupported" || kinds["host-other"] != "unsupported" {
		t.Fatalf("candidates=%v", kinds)
	}
	for _, candidate := range inventory.Envelope.Candidates {
		if candidate.Kind == "unsupported" && !strings.Contains(string(candidate.Payload), "conditional-group-host") {
			t.Fatalf("payload=%s", candidate.Payload)
		}
	}
}

func TestCommitLegacyFragmentsPreservesUnrelatedKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	data := []byte(`{"version":23,"settings":{"dots_repo":"keep","agents_disabled":true},"agents":{"packages":[{"source":"x"}]}}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	docs, err := captureJournalDocuments([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if err := commitLegacyFragments(docs, path); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), `"dots_repo": "keep"`) || strings.Contains(string(got), `"agents"`) || !strings.Contains(string(got), `"version": 24`) {
		t.Fatalf("got %s", got)
	}
}

func TestCommitLegacyFragmentsPreservesConcurrentUnrelatedEditAndResumes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	original := []byte(`{"version":23,"settings":{"dots_repo":"old"},"agents":{"packages":[{"source":"x"}]}}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	docs, err := captureJournalDocuments([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	changed := []byte(`{"version":23,"settings":{"dots_repo":"new"},"agents":{"packages":[{"source":"x"}]}}`)
	if err := os.WriteFile(path, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := commitLegacyFragments(docs, path); err != nil {
		t.Fatal(err)
	}
	if err := commitLegacyFragments(docs, path); err != nil {
		t.Fatalf("idempotent resume: %v", err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), `"dots_repo": "new"`) {
		t.Fatalf("unrelated edit lost: %s", got)
	}
}

func TestCommitLegacyFragmentsRejectsChangedLegacyNode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	original := []byte(`{"version":23,"agents":{"packages":[{"source":"x"}]}}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	docs, err := captureJournalDocuments([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	changed := []byte(`{"version":23,"agents":{"packages":[{"source":"y"}]}}`)
	if err := os.WriteFile(path, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := commitLegacyFragments(docs, path); err == nil || !strings.Contains(err.Error(), "fragment-conflict") {
		t.Fatalf("err=%v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(changed) {
		t.Fatal("conflict changed file")
	}
}

func TestCommitLegacyFragmentsRejectsModeAndSymlinkSwaps(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("covered by Windows ACL/reparse job")
	}
	for _, kind := range []string{"mode", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "settings.json")
			data := []byte(`{"version":23,"agents":{"packages":[{"source":"x"}]}}`)
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			docs, err := captureJournalDocuments([]string{path})
			if err != nil {
				t.Fatal(err)
			}
			if kind == "mode" {
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			} else {
				other := filepath.Join(dir, "other.json")
				if err := os.WriteFile(other, data, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, path); err != nil {
					t.Fatal(err)
				}
			}
			if err := commitLegacyFragments(docs, path); err == nil || !strings.Contains(err.Error(), "fragment-conflict") {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestCommitLegacyFragmentsRestartsAtRenameBoundaries(t *testing.T) {
	for _, boundary := range []string{"before-rename", "after-rename"} {
		t.Run(boundary, func(t *testing.T) {
			dir := t.TempDir()
			root := filepath.Join(dir, "settings.json")
			fragment := filepath.Join(dir, "agents.json")
			if err := os.WriteFile(root, []byte(`{"version":23,"$include":["agents.json"],"agents":{"packages":[{"source":"root","agents":["codex"]}]}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(fragment, []byte(`{"agents":{"skills":[{"name":"nested","source":"x","agents":["codex"]}]}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			docs, err := captureJournalDocuments([]string{root, fragment})
			if err != nil {
				t.Fatal(err)
			}
			failed := false
			onboardingFragmentFailpoint = func(got, _ string) error {
				if got == boundary && !failed {
					failed = true
					return errors.New("injected")
				}
				return nil
			}
			defer func() { onboardingFragmentFailpoint = nil }()
			if err := commitLegacyFragments(docs, root); err == nil {
				t.Fatal("failpoint did not fire")
			}
			onboardingFragmentFailpoint = nil
			if err := commitLegacyFragments(docs, root); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{root, fragment} {
				data, _ := os.ReadFile(path)
				if strings.Contains(string(data), `"agents"`) {
					t.Fatalf("legacy state remains in %s: %s", path, data)
				}
			}
		})
	}
}
