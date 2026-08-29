package app

import (
	"context"
	"path/filepath"
	"testing"
)

func newReconcileAgentsApp(t *testing.T, hostname string) (*App, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("OMNI_HOSTNAME", hostname)
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	a.SetFallbackExecutor(&availExecutor{available: nil})
	if err := a.InitTestMode(t.Context()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a, home
}

func TestReconcileWithoutAPMWorkspaceContinuesDotPhases(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	a, _ := newReconcileAgentsApp(t, "reconcile-agents-fail-host")

	result, err := a.Reconcile(context.Background(), ReconcileOptions{})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if result.Agents == nil {
		t.Fatal("result.Agents = nil, want the no-op APM leg recorded")
	}
	if result.DotsSkipped != "dotfiles not configured" {
		t.Fatalf("DotsSkipped = %q, want the dot phases to have run after the agents leg", result.DotsSkipped)
	}
}
