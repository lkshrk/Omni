package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

// Pairs a resolve verb with how to reach it so each test states the mcp/plugins pair once.
type capabilityResolveCase struct {
	name    string
	group   func(*rootState) *cobra.Command
	resolve func(*rootState) *cobra.Command
	prompt  string
}

func capabilityResolveCases() []capabilityResolveCase {
	return []capabilityResolveCase{
		{
			name:    "mcp",
			group:   newAgentsMcpCmd,
			resolve: newAgentsMcpResolveCmd,
			prompt:  "Replace the live registration",
		},
		{
			name:    "plugins",
			group:   newAgentsPluginsCmd,
			resolve: newAgentsPluginsResolveCmd,
			prompt:  "Uninstall the foreign copy",
		},
	}
}

func runCapabilityResolve(t *testing.T, tc capabilityResolveCase, a *app.App, yes bool, args ...string) (string, error) {
	t.Helper()
	cmd := tc.resolve(&rootState{app: a, yes: yes})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return out.String(), err
}

func TestAgentsCapabilityResolveCmds_RegisteredWithFlags(t *testing.T) {
	for _, tc := range capabilityResolveCases() {
		resolve := findCommand(tc.group(&rootState{}), []string{"resolve"})
		if resolve == nil {
			t.Fatalf("agents %s resolve is not registered", tc.name)
		}
		for _, flag := range []string{"use-managed", "use-local", "agent", "dry-run"} {
			if resolve.Flags().Lookup(flag) == nil {
				t.Errorf("agents %s resolve is missing --%s", tc.name, flag)
			}
		}
	}
}

func TestAgentsCapabilityResolveCmds_RequireExactlyOneSide(t *testing.T) {
	a := newAgentsSyncTestApp(t, config.Settings{})
	for _, tc := range capabilityResolveCases() {
		for _, args := range [][]string{
			{"thing"},
			{"thing", "--use-managed", "--use-local"},
		} {
			out, err := runCapabilityResolve(t, tc, a, true, args...)
			if err == nil || !strings.Contains(err.Error(), "exactly one of --use-managed or --use-local") {
				t.Fatalf("%s %v: err = %v, out = %q", tc.name, args, err, out)
			}
		}
	}
}

// An aborted confirmation must not reach the app, so the command exits cleanly without the confirmed run's manifest lookup error.
func TestAgentsCapabilityResolveCmds_UseManagedConfirms(t *testing.T) {
	for _, tc := range capabilityResolveCases() {
		a := newAgentsSyncTestApp(t, config.Settings{})
		out, err := runCapabilityResolve(t, tc, a, false, "thing", "--use-managed")
		if err != nil {
			t.Fatalf("%s: aborted confirmation must not fail: %v", tc.name, err)
		}
		if !strings.Contains(out, tc.prompt) || !strings.Contains(out, "Aborted.") {
			t.Fatalf("%s output = %q, want the confirmation prompt and an abort", tc.name, out)
		}
		if _, err := runCapabilityResolve(t, tc, a, true, "thing", "--use-managed"); err == nil ||
			!strings.Contains(err.Error(), "not in this host's manifest") {
			t.Fatalf("%s: --yes must run the resolution, got err = %v", tc.name, err)
		}
	}
}

func TestAgentsCapabilityResolveCmds_DryRunSkipsConfirm(t *testing.T) {
	for _, tc := range capabilityResolveCases() {
		a := newAgentsSyncTestApp(t, config.Settings{})
		out, err := runCapabilityResolve(t, tc, a, false, "thing", "--use-managed", "--dry-run")
		if err == nil || !strings.Contains(err.Error(), "not in this host's manifest") {
			t.Fatalf("%s: err = %v, want the app lookup to run", tc.name, err)
		}
		if strings.Contains(out, tc.prompt) {
			t.Fatalf("%s output = %q, want no confirmation for a dry run", tc.name, out)
		}
	}
}
