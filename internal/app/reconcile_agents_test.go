package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func newReconcileAgentsApp(t *testing.T, hostname string) (*App, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("OMNI_HOSTNAME", hostname)
	stubBinariesOnPath(t, "claude")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := New(filepath.Join(t.TempDir(), "settings.json"),
		WithMcpAdapters([]McpAdapter{}),
		WithPluginAdapters([]PluginAdapter{}))
	if err := a.InitTestMode(t.Context()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a, home
}

func TestReconcile_AgentsLegClaimsAndRestores(t *testing.T) {
	a, home := newReconcileAgentsApp(t, "reconcile-agents-host")
	source := filepath.Join(t.TempDir(), "legacy-skills")
	writeAppSkill(t, filepath.Join(source, "skills", "demo"), "demo")
	agentsDir := filepath.Join(home, ".agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lock := `{"skills":{"demo":{"source":"` + source + `","updated_at":"2026-01-01T00:00:00Z"}}}`
	if err := os.WriteFile(filepath.Join(agentsDir, ".skill-lock.json"), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.withConfig(func(*config.RootConfig) error { return nil }); err != nil {
		t.Fatal(err)
	}

	var progress []string
	result, err := a.Reconcile(context.Background(), ReconcileOptions{
		Progress: func(message string) { progress = append(progress, message) },
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if result.Agents == nil {
		t.Fatal("result.Agents = nil, want the composed agents run")
	}
	if !slices.Contains(result.Agents.Imported.Added, source) {
		t.Fatalf("imported = %+v, want the unmanaged lockfile source claimed", result.Agents.Imported)
	}
	if !slices.Contains(result.Agents.Skills.Installed, source) {
		t.Fatalf("installed = %v, want the claimed package restored", result.Agents.Skills.Installed)
	}
	if !slices.Contains(progress, "syncing agents...") {
		t.Fatalf("progress = %v, want a 'syncing agents...' step", progress)
	}

	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.Packages) != 1 || cfg.Agents.Packages[0].Source != source {
		t.Fatalf("manifest packages = %+v, want the claimed source", cfg.Agents.Packages)
	}
	link := filepath.Join(home, ".claude", "skills", "demo")
	if _, err := os.Stat(link); err != nil {
		t.Fatalf("restored skill entry missing at %s: %v", link, err)
	}
}

func TestReconcile_SkipsAgentsLegWhenDisabled(t *testing.T) {
	a, _ := newReconcileAgentsApp(t, "reconcile-agents-off-host")
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Settings.AgentsDisabled = config.BoolPtr(true)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	var progress []string
	result, err := a.Reconcile(context.Background(), ReconcileOptions{
		Progress: func(message string) { progress = append(progress, message) },
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if result.Agents != nil {
		t.Fatalf("result.Agents = %+v, want nil when agent features are off", result.Agents)
	}
	if slices.Contains(progress, "syncing agents...") {
		t.Fatalf("progress = %v, want no agents step when agent features are off", progress)
	}
}

func TestReconcile_AgentFailureDoesNotStopDotPhases(t *testing.T) {
	a, _ := newReconcileAgentsApp(t, "reconcile-agents-fail-host")
	missing := filepath.Join(t.TempDir(), "no-such-source")
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Agents.Packages = []config.SkillPackage{{Source: missing}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	result, err := a.Reconcile(context.Background(), ReconcileOptions{})
	if err != nil {
		t.Fatalf("Reconcile err = %v, want a per-feature failure reported instead", err)
	}
	if result.Agents == nil || len(result.Agents.Errors) == 0 {
		t.Fatalf("result.Agents = %+v, want the failing skill package reported", result.Agents)
	}
	if result.DotsSkipped != "dotfiles not configured" {
		t.Fatalf("DotsSkipped = %q, want the dot phases to have run after the agents leg", result.DotsSkipped)
	}
	if issues := SummarizeReconcileIssues(result); issues.AgentFailures != len(result.Agents.Errors) {
		t.Fatalf("AgentFailures = %d, want %d", issues.AgentFailures, len(result.Agents.Errors))
	}
	if lines := ReconcileIssueLines(result); !slices.Contains(lines, "1 agent operation failed") {
		t.Fatalf("issue lines = %v, want an agent-operation failure line", lines)
	}
}
