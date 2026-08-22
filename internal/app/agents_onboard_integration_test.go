//go:build integration

package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/apm"
)

func TestAgentsOnboardRealPinnedAPM(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("APM_E2E_TESTS", "1")
	t.Setenv("PATH", "/home/coder/apm/.venv/bin:"+os.Getenv("PATH"))
	configPath := filepath.Join(home, ".config", "omni", "settings.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	one := createLegacySkillRepo(t, filepath.Join(home, "one"), "one")
	two := createLegacySkillRepo(t, filepath.Join(home, "two"), "two")
	legacyData, _ := json.Marshal(map[string]any{
		"version": 23, "settings": map[string]any{"dots_repo": "keep"},
		"agents": map[string]any{"skills": []any{map[string]any{"name": "legacy-review", "source": "file://" + one, "agents": []string{"codex"}}, map[string]any{"name": "legacy-review", "source": "file://" + two, "agents": []string{"codex"}}}, "mcp_servers": []any{map[string]any{"name": "secret-api", "transport": "http", "url": "https://example.invalid/mcp", "env_literal": map[string]string{"TOKEN": "literal"}, "agents": []string{"codex"}}}},
		"groups": []any{map[string]any{"name": "later", "skills": []string{"legacy-review"}}},
	})
	if err := os.WriteFile(configPath, legacyData, 0o600); err != nil {
		t.Fatal(err)
	}
	skill := filepath.Join(home, ".agents", "skills", "native-demo")
	if err := os.MkdirAll(skill, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("# Native demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	a := New(configPath)
	a.StateDir = filepath.Join(home, ".local", "state", "omni")
	if err := a.InitOnboardingReadOnly(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(configPath)
	preview, err := a.AgentsOnboardPlan(context.Background(), AgentsOnboardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(configPath)
	if string(before) != string(after) {
		t.Fatal("plan changed config")
	}
	if _, err := os.Stat(filepath.Join(home, ".apm")); !os.IsNotExist(err) {
		entries, _ := os.ReadDir(filepath.Join(home, ".apm"))
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("plan created APM state %v: %v", names, err)
	}
	if preview.Envelope.Plan == nil {
		t.Fatal("missing plan")
	}
	for i := range preview.Envelope.Plan.Items {
		item := &preview.Envelope.Plan.Items[i]
		switch item.Classification {
		case "conflict":
			item.Resolution.Decision = "select-origin"
			item.Resolution.SelectedOriginID = item.CandidateIDs[0]
		case "needs-choice":
			if slices.Contains(item.ReasonCodes, "conditional-group-host") {
				item.Resolution.Decision = "exclude"
			} else if slices.Contains(item.ReasonCodes, "legacy-unscoped-targets") {
				item.Resolution.Decision = "import"
				item.Resolution.ApprovedTargets = []string{"codex"}
			}
		case "secret-blocked":
			item.Resolution.Decision = "map-secret"
			item.Resolution.EnvBindings = map[string]string{"/env/TOKEN": "API_TOKEN"}
		}
	}
	if err := apm.BindImportPlanResolution(preview.Envelope.Plan); err != nil {
		t.Fatal(err)
	}
	bound := *preview.Envelope.Plan
	wantPlanID, wantResolutionID, wantOperationID := bound.PlanID, bound.ResolutionID, bound.OperationID
	bound.PlanID = ""
	if err := apm.BindImportPlanResolution(&bound); err != nil {
		t.Fatal(err)
	}
	if bound.PlanID != wantPlanID || bound.ResolutionID != wantResolutionID || bound.OperationID != wantOperationID {
		t.Fatalf("identity mismatch got plan=%s resolution=%s operation=%s want %s %s %s", bound.PlanID, bound.ResolutionID, bound.OperationID, wantPlanID, wantResolutionID, wantOperationID)
	}
	planPath := filepath.Join(home, "plan.json")
	planData, _ := json.MarshalIndent(preview.Envelope.Plan, "", "  ")
	if err := os.WriteFile(planPath, planData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("# Changed after review\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.AgentsOnboardApply(context.Background(), planPath); err == nil {
		t.Fatal("stale plan applied")
	}
	if _, err := os.Stat(filepath.Join(a.StateDir, "onboarding")); !os.IsNotExist(err) {
		t.Fatalf("stale plan stranded journal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("# Native demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := a.AgentsOnboardApply(context.Background(), planPath)
	if err != nil {
		t.Fatal(err)
	}
	if result.Envelope.State != "complete" {
		t.Fatalf("state=%q", result.Envelope.State)
	}
	for _, path := range []string{
		filepath.Join(home, ".apm", "apm.yml"),
		filepath.Join(home, ".apm", "apm.lock.yaml"),
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if strings.Contains(text, "env_literal") || strings.Contains(text, "literal-secret") || strings.Contains(text, `TOKEN: literal`) {
			t.Fatalf("non-canonical MCP secret material in %s: %s", path, text)
		}
		if !strings.Contains(text, "${API_TOKEN}") {
			t.Fatalf("MCP placeholder missing from %s: %s", path, text)
		}
	}
	cleanupPreview, err := a.AgentsOnboardCleanup(context.Background(), result.Envelope.OperationID, false)
	if err != nil {
		t.Fatal(err)
	}
	if cleanupPreview.Count < 2 || len(cleanupPreview.Paths) != cleanupPreview.Count {
		t.Fatalf("preview=%#v", cleanupPreview)
	}
	if _, err := a.AgentsOnboardCleanup(context.Background(), result.Envelope.OperationID, true); err != nil {
		t.Fatal(err)
	}
	repeated, err := a.AgentsOnboardCleanup(context.Background(), result.Envelope.OperationID, true)
	if err != nil || !repeated.AlreadyClean {
		t.Fatalf("repeated=%#v err=%v", repeated, err)
	}
	got, _ := os.ReadFile(configPath)
	if strings.Contains(string(got), `"agents"`) || !strings.Contains(string(got), `"version": 24`) || !strings.Contains(string(got), `"dots_repo": "keep"`) {
		t.Fatalf("config=%s", got)
	}
}

func createLegacySkillRepo(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("# "+body+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "apm.yml"), []byte("name: "+body+"\nversion: 1.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"config", "user.email", "test@example.invalid"}, {"config", "user.name", "Test"}, {"add", "."}, {"commit", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = path
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	return path
}
