package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

func TestReconcile_APMIgnoresLegacyHostDisableSwitch(t *testing.T) {
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
	if result.Agents == nil {
		t.Fatal("result.Agents = nil, want the global APM leg to run")
	}
	if !slices.Contains(progress, "syncing agents...") {
		t.Fatalf("progress = %v, want the global APM leg", progress)
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
	if err == nil || !strings.Contains(err.Error(), "sync agents") {
		t.Fatalf("Reconcile err = %v, want the APM failure returned", err)
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
