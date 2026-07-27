package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func runAgentsSkillsResolve(t *testing.T, a *app.App, yes bool, args ...string) (string, error) {
	t.Helper()
	cmd := newAgentsResolveSkillsCmd(&rootState{app: a, yes: yes})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return out.String(), err
}

func TestAgentsSkillsResolveCmd_RegisteredWithFlags(t *testing.T) {
	cmd := newAgentsSkillsCmd(&rootState{})
	resolve := findCommand(cmd, []string{"resolve"})
	if resolve == nil {
		t.Fatal("agents skills resolve is not registered")
	}
	for _, name := range []string{"use-managed", "use-local", "agent", "dry-run"} {
		if resolve.Flags().Lookup(name) == nil {
			t.Errorf("agents skills resolve is missing --%s", name)
		}
	}
}

func TestAgentsSkillsResolveCmd_RequiresExactlyOneSide(t *testing.T) {
	a := newAgentsSyncTestApp(t, config.Settings{})
	for _, args := range [][]string{
		{"owner/repo"},
		{"owner/repo", "--use-managed", "--use-local"},
	} {
		out, err := runAgentsSkillsResolve(t, a, true, args...)
		if err == nil || !strings.Contains(err.Error(), "exactly one of --use-managed or --use-local") {
			t.Fatalf("args %v: err = %v, out = %q", args, err, out)
		}
	}
}

// An aborted confirmation must not reach the app, so the command exits cleanly without the manifest lookup error.
func TestAgentsSkillsResolveCmd_UseManagedConfirms(t *testing.T) {
	a := newAgentsSyncTestApp(t, config.Settings{})
	out, err := runAgentsSkillsResolve(t, a, false, "owner/repo", "--use-managed")
	if err != nil {
		t.Fatalf("aborted confirmation must not fail: %v", err)
	}
	if !strings.Contains(out, "Replace the foreign skill content") || !strings.Contains(out, "Aborted.") {
		t.Fatalf("output = %q, want the confirmation prompt and an abort", out)
	}

	if _, err := runAgentsSkillsResolve(t, a, true, "owner/repo", "--use-managed"); err == nil ||
		!strings.Contains(err.Error(), "not in this host's manifest") {
		t.Fatalf("--yes must run the resolution, got err = %v", err)
	}
}

// --dry-run replaces nothing, so there is nothing to confirm.
func TestAgentsSkillsResolveCmd_DryRunSkipsConfirm(t *testing.T) {
	a := newAgentsSyncTestApp(t, config.Settings{})
	out, err := runAgentsSkillsResolve(t, a, false, "owner/repo", "--use-managed", "--dry-run")
	if err == nil || !strings.Contains(err.Error(), "not in this host's manifest") {
		t.Fatalf("err = %v, want the app lookup to run", err)
	}
	if strings.Contains(out, "Replace the foreign skill content") {
		t.Fatalf("output = %q, want no confirmation for a dry run", out)
	}
}

func TestPrintSkillDriftResolution_PrefixesDryRunActions(t *testing.T) {
	var out bytes.Buffer
	res := app.SkillDriftResolution{
		Actions:  []string{"replace /tmp/skills/demo with omni's managed link for claude-code"},
		Warnings: []string{"heads up"},
	}
	printDriftResolution(&out, res.Actions, res.Warnings, true)
	if !strings.Contains(out.String(), "  ! heads up\n") {
		t.Errorf("output = %q, want the warning line", out.String())
	}
	if !strings.Contains(out.String(), "would replace /tmp/skills/demo") {
		t.Errorf("output = %q, want the dry-run prefix", out.String())
	}
}
