//go:build integration

package app

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/apm"
	"github.com/lkshrk/omni/internal/securefile"
)

func TestAgentsOnboardRealPinnedAPM(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
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
	cursorConfig := filepath.Join(home, ".cursor", "settings.json")
	if err := os.MkdirAll(filepath.Dir(cursorConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cursorConfig, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cursorAgent := filepath.Join(home, ".cursor", "agents", "cursor-reviewer.md")
	if err := os.MkdirAll(filepath.Dir(cursorAgent), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cursorAgent, []byte("# Cursor reviewer\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	intellijConfig := filepath.Join(home, ".config", "github-copilot", "intellij", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(intellijConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(intellijConfig, []byte(`{"servers":{"idea":{"type":"local","command":"uvx","args":["demo"]}}}`+"\n"), 0o600); err != nil {
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
	sawCursor, sawAgentSkill, sawIntelliJ := false, false, false
	for i := range preview.Envelope.Plan.Items {
		item := &preview.Envelope.Plan.Items[i]
		switch item.Name {
		case "cursor-reviewer":
			sawCursor = true
		case "native-demo":
			sawAgentSkill = slices.Contains(item.CurrentTargets, "agent-skills") || slices.Contains(item.ProposedTargets, "agent-skills")
		case "idea":
			sawIntelliJ = slices.Contains(item.CurrentTargets, "intellij") || slices.Contains(item.ProposedTargets, "intellij")
		}
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
		case "unsupported":
			item.Resolution.Decision = "exclude"
		}
	}
	if !sawCursor || !sawAgentSkill || !sawIntelliJ {
		t.Fatalf("APM default discovery cursor=%v agent-skills=%v intellij=%v plan=%#v", sawCursor, sawAgentSkill, sawIntelliJ, preview.Envelope.Plan)
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
	manifest, err := os.ReadFile(filepath.Join(home, ".apm", "apm.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"agent-skills", "cursor", "intellij"} {
		if !strings.Contains(string(manifest), target) {
			t.Fatalf("global default import omitted %s: %s", target, manifest)
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

func TestAgentsOnboardPlansSanitizedRealDotfilesWithPinnedAPM(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APM_E2E_TESTS", "1")
	t.Setenv("PATH", "/home/coder/apm/.venv/bin:"+os.Getenv("PATH"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	fixtureRoot := filepath.Join("testdata", "onboarding", "real-dotfiles-v22")
	configRoot := filepath.Join(home, ".config", "omni")
	for _, relative := range []string{"settings.json", filepath.Join("settings.d", "agents.json"), filepath.Join("settings.d", "groups.json")} {
		data, err := os.ReadFile(filepath.Join(fixtureRoot, relative))
		if err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(configRoot, relative)
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	before := snapshotOnboardingTestTree(t, home)
	a := New(filepath.Join(configRoot, "settings.json"))
	a.StateDir = filepath.Join(home, ".local", "state", "omni")
	if err := a.InitOnboardingReadOnly(context.Background()); err != nil {
		t.Fatal(err)
	}
	preview, err := a.AgentsOnboardPlan(context.Background(), AgentsOnboardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Envelope.Plan == nil || len(preview.Envelope.Plan.Items) != 48 || len(preview.Envelope.Plan.Blockers) != 4 || !maps.Equal(preview.Envelope.Plan.Summary, map[string]int{"excluded": 16, "importable": 27, "needs-choice": 5}) {
		t.Fatalf("unexpected real-world plan: %#v", preview.Envelope.Plan)
	}
	after := snapshotOnboardingTestTree(t, home)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("dry-run changed isolated HOME\nbefore=%#v\nafter=%#v", before, after)
	}
	if _, err := os.Stat(filepath.Join(home, ".apm")); !os.IsNotExist(err) {
		t.Fatalf("dry-run created APM state: %v", err)
	}
	if _, err := os.Stat(a.StateDir); !os.IsNotExist(err) {
		t.Fatalf("dry-run created Omni state: %v", err)
	}
}

func TestAgentsOnboardProjectRealLocalAPMIsolatedAndIdempotent(t *testing.T) {
	home := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "workspace")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APM_E2E_TESTS", "1")
	t.Setenv("PATH", "/home/coder/apm/.venv/bin:"+os.Getenv("PATH"))
	if err := os.MkdirAll(filepath.Join(workspace, ".vscode"), 0o700); err != nil {
		t.Fatal(err)
	}
	native := filepath.Join(workspace, ".vscode", "mcp.json")
	originalNative := `{"servers":{"demo":{"type":"stdio","command":"python","args":["-m","demo"]}}}` + "\n"
	if err := os.WriteFile(native, []byte(originalNative), 0o600); err != nil {
		t.Fatal(err)
	}
	codexNative := filepath.Join(workspace, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(codexNative), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexNative, []byte("[mcp_servers.codex-demo]\ncommand = \"python\"\nargs = [\"-m\", \"codex-demo\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, ".config", "omni", "settings.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := []byte(`{"version":23,"agents":{"skills":[]}}` + "\n")
	if err := os.WriteFile(configPath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	a := New(configPath)
	a.StateDir = filepath.Join(home, ".local", "state", "omni")
	if err := a.InitOnboardingReadOnly(context.Background()); err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(t.TempDir(), "plan.json")
	preview, err := a.AgentsOnboardPlan(context.Background(), AgentsOnboardOptions{PlanJSON: planPath, ProjectRoot: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Envelope.Plan == nil || preview.Envelope.Plan.Scope != "project" || preview.Envelope.Plan.ProjectRoot != workspace {
		t.Fatalf("plan=%#v", preview.Envelope.Plan)
	}
	names := make([]string, 0, len(preview.Envelope.Plan.Items))
	for _, item := range preview.Envelope.Plan.Items {
		names = append(names, item.Name)
	}
	if !slices.Contains(names, "demo") || !slices.Contains(names, "codex-demo") {
		t.Fatalf("project default did not discover Codex and VSCode: %v", names)
	}
	if err := os.WriteFile(native, []byte(strings.Replace(originalNative, `"demo"]`, `"changed"]`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.AgentsOnboardApply(context.Background(), planPath); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale project plan err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "apm.yml")); !os.IsNotExist(err) {
		t.Fatalf("stale plan wrote workspace manifest: %v", err)
	}
	if err := os.WriteFile(native, []byte(originalNative), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := a.AgentsOnboardApply(context.Background(), planPath)
	if err != nil {
		t.Fatal(err)
	}
	if result.Envelope.State != "complete" {
		t.Fatalf("state=%q", result.Envelope.State)
	}
	stateRoot, err := securefile.OpenRoot(filepath.Join(a.StateDir, "onboarding"))
	if err != nil {
		t.Fatal(err)
	}
	opRoot, err := stateRoot.OpenChild(result.Envelope.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := readOnboardJournal(opRoot)
	if err != nil || journal.Scope != "project" || journal.ProjectRoot != workspace {
		t.Fatalf("journal=%#v err=%v", journal, err)
	}
	for _, path := range []string{filepath.Join(workspace, "apm.yml"), filepath.Join(workspace, "apm.lock.yaml"), native, codexNative} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing project state %s: %v", path, err)
		}
	}
	projectManifest, err := os.ReadFile(filepath.Join(workspace, "apm.yml"))
	if err != nil || !strings.Contains(string(projectManifest), "codex-demo") || !strings.Contains(string(projectManifest), "demo") {
		t.Fatalf("mixed project manifest=%s err=%v", projectManifest, err)
	}
	for _, path := range []string{filepath.Join(home, ".apm", "apm.yml"), filepath.Join(home, ".apm", "imported"), filepath.Join(home, ".vscode")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("project import wrote global state %s: %v", path, err)
		}
	}
	if got, err := os.ReadFile(configPath); err != nil || string(got) != string(legacy) {
		t.Fatalf("project import changed Omni config: %q err=%v", got, err)
	}
	second, err := a.AgentsOnboardPlan(context.Background(), AgentsOnboardOptions{ProjectRoot: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if second.Envelope.Plan == nil || len(second.Envelope.Plan.Items) != 2 || second.Envelope.Plan.Items[0].Classification != "already-managed" || second.Envelope.Plan.Items[1].Classification != "already-managed" {
		t.Fatalf("second scan=%#v", second.Envelope.Plan)
	}
}

type onboardingTestTreeEntry struct {
	Mode    os.FileMode
	ModTime int64
	Data    string
}

func snapshotOnboardingTestTree(t *testing.T, root string) map[string]onboardingTestTreeEntry {
	t.Helper()
	result := map[string]onboardingTestTreeEntry{}
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		entry := onboardingTestTreeEntry{Mode: info.Mode(), ModTime: info.ModTime().UnixNano()}
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			entry.Data = string(data)
		}
		result[relative] = entry
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return result
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
