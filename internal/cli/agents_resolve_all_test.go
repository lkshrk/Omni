package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func runAgentsResolveAll(t *testing.T, a *app.App, yes bool, args ...string) (string, error) {
	t.Helper()
	cmd := newAgentsResolveAllCmd(&rootState{app: a, yes: yes})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return out.String(), err
}

func TestAgentsResolveAllCmd_RegisteredWithFlags(t *testing.T) {
	cmd := newAgentsCmd(&rootState{})
	resolve := findCommand(cmd, []string{"resolve"})
	if resolve == nil {
		t.Fatal("agents resolve is not registered")
	}
	for _, name := range []string{"use-managed", "use-local", "dry-run"} {
		if resolve.Flags().Lookup(name) == nil {
			t.Errorf("agents resolve is missing --%s", name)
		}
	}
	// The batch verb resolves each item on every agent it drifted on, so there is no one agent to scope to.
	if resolve.Flags().Lookup("agent") != nil {
		t.Error("agents resolve must not offer --agent")
	}
}

func TestAgentsResolveAllCmd_RequiresExactlyOneSide(t *testing.T) {
	a := newAgentsSyncTestApp(t, config.Settings{})
	for _, args := range [][]string{
		{},
		{"--use-managed", "--use-local"},
	} {
		out, err := runAgentsResolveAll(t, a, true, args...)
		if err == nil || !strings.Contains(err.Error(), "exactly one of --use-managed or --use-local") {
			t.Fatalf("args %v: err = %v, out = %q", args, err, out)
		}
	}
}

func TestAgentsResolveAllCmd_RejectsPositionalArgs(t *testing.T) {
	a := newAgentsSyncTestApp(t, config.Settings{})
	if _, err := runAgentsResolveAll(t, a, true, "owner/repo", "--use-local"); err == nil {
		t.Fatal("agents resolve takes no argument, want an error")
	}
}

// The batch is destructive to local content, so --use-managed asks once for the whole set.
func TestAgentsResolveAllCmd_UseManagedConfirmsOnceForTheBatch(t *testing.T) {
	a := newAgentsSyncTestApp(t, config.Settings{})
	out, err := runAgentsResolveAll(t, a, false, "--use-managed")
	if err != nil {
		t.Fatalf("aborted confirmation must not fail: %v", err)
	}
	if !strings.Contains(out, "Replace every drifted agent resource") || !strings.Contains(out, "Aborted.") {
		t.Fatalf("output = %q, want the batch confirmation prompt and an abort", out)
	}
}

func TestAgentsResolveAllCmd_DryRunSkipsConfirm(t *testing.T) {
	a := newAgentsSyncTestApp(t, config.Settings{})
	out, err := runAgentsResolveAll(t, a, false, "--use-managed", "--dry-run")
	if err != nil {
		t.Fatalf("dry run on a clean host should succeed: %v", err)
	}
	if strings.Contains(out, "Replace every drifted agent resource") {
		t.Fatalf("output = %q, want no confirmation for a dry run", out)
	}
}

func TestAgentsResolveAllCmd_NoDriftReportsZeroes(t *testing.T) {
	a := newAgentsSyncTestApp(t, config.Settings{})
	out, err := runAgentsResolveAll(t, a, true, "--use-local")
	if err != nil {
		t.Fatalf("a host with no drift is a clean no-op: %v", err)
	}
	if !strings.Contains(out, "0 skills, 0 mcp servers, 0 plugins resolved") {
		t.Fatalf("output = %q, want the per-capability resolved counts", out)
	}
}

func TestAgentsResolveAllCmd_AgentsDisabledIsAHardError(t *testing.T) {
	a := newAgentsSyncTestApp(t, config.Settings{AgentsDisabled: config.BoolPtr(true)})
	if _, err := runAgentsResolveAll(t, a, true, "--use-local"); err == nil {
		t.Fatal("agents resolve must fail while agents_disabled is set")
	}
}
