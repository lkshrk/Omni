package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func newAgentsSyncTestApp(t *testing.T, settings config.Settings, opts ...func(*app.App)) *app.App {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	root := config.RootConfig{Version: config.CurrentVersion, Settings: settings}
	if err := config.Save(cfgPath, &root); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath, opts...)
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func runAgentsSync(t *testing.T, a *app.App, args ...string) (string, error) {
	t.Helper()
	cmd := newAgentsSyncCmd(&rootState{app: a})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return out.String(), err
}

func TestAgentsSyncCmd_RegisteredUnderAgents(t *testing.T) {
	cmd := newAgentsCmd(&rootState{})
	found := false
	for _, sub := range cmd.Commands() {
		if sub.Name() == "restore" {
			found = true
			if sub.Flags().Lookup("dry-run") == nil {
				t.Error("agents sync is missing --dry-run")
			}
		}
	}
	if !found {
		t.Fatal("agents sync is not registered under agents")
	}
}

func TestAgentsSyncCmd_ReportsEveryFeature(t *testing.T) {
	a := newAgentsSyncTestApp(t, config.Settings{})
	out, err := runAgentsSync(t, a)
	if err != nil {
		t.Fatalf("agents sync: %v", err)
	}
	if !strings.Contains(out, "skills:") {
		t.Errorf("output = %q, want a skills line", out)
	}
}

// The aggregate restore stays converge-only; adopting on-disk directories belongs to `omni tools sync --all`.
func TestAgentsSyncCmd_DoesNotClaimUnmanagedSkills(t *testing.T) {
	a := newAgentsSyncTestApp(t, config.Settings{})
	out, err := runAgentsSync(t, a)
	if err != nil {
		t.Fatalf("agents sync: %v", err)
	}
	if strings.Contains(out, "skills import:") {
		t.Errorf("output = %q, want no import section", out)
	}
}

func TestAgentsSyncCmd_DisabledFeaturesWarnAndExitZero(t *testing.T) {
	a := newAgentsSyncTestApp(t, config.Settings{
		SkillsDisabled:  config.BoolPtr(true),
		McpDisabled:     config.BoolPtr(true),
		PluginsDisabled: config.BoolPtr(true),
	})
	out, err := runAgentsSync(t, a)
	if err != nil {
		t.Fatalf("disabled features must warn, not fail: %v", err)
	}
	for _, want := range []string{"skills are disabled", "mcp servers are disabled", "plugins are disabled"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, want a warning containing %q", out, want)
		}
	}
}

func TestAgentsSyncCmd_MasterSwitchStillErrors(t *testing.T) {
	a := newAgentsSyncTestApp(t, config.Settings{AgentsDisabled: config.BoolPtr(true)})
	if _, err := runAgentsSync(t, a, "--dry-run"); err == nil {
		t.Fatal("agents sync must fail while agents_disabled is set")
	}
}
