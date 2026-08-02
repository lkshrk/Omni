package cli

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func runSyncAll(t *testing.T, a *app.App, args ...string) (string, error) {
	t.Helper()
	cmd := newSyncCmd(&rootState{app: a, yes: true})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"--all"}, args...))
	err := cmd.ExecuteContext(context.Background())
	return out.String(), err
}

func newSyncAllAgentsTestApp(t *testing.T, settings config.Settings) *app.App {
	t.Helper()
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Settings: settings,
		Groups:   []*config.GroupConfig{cliTestHostGroup()},
	})
	a := app.New(cfgPath)
	a.CacheDir = t.TempDir()
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func TestSyncAllFlag_RunsAgentsLegAfterTools(t *testing.T) {
	a := newSyncAllAgentsTestApp(t, config.Settings{})
	out, err := runSyncAll(t, a)
	if err != nil {
		t.Fatalf("sync --all: %v", err)
	}
	toolsIdx := strings.Index(out, "Sync all complete")
	agentsIdx := strings.Index(out, "Agents sync complete")
	if toolsIdx < 0 || agentsIdx < 0 {
		t.Fatalf("output = %q, want both the tools and agents summaries", out)
	}
	if toolsIdx > agentsIdx {
		t.Errorf("output = %q, want the agents leg after the tool phase", out)
	}
	if !strings.Contains(out, "importing unmanaged skills") {
		t.Errorf("output = %q, want the claim step announced", out)
	}
}

func TestSyncAllFlag_DryRunPreviewsAgentsLeg(t *testing.T) {
	a := newSyncAllAgentsTestApp(t, config.Settings{})
	out, err := runSyncAll(t, a, "--dry-run")
	if err != nil {
		t.Fatalf("sync --all --dry-run: %v", err)
	}
	last := -1
	for _, want := range []string{
		"would import unmanaged plugins",
		"would restore plugins",
		"would import unmanaged skills",
		"would restore skills",
		"would import unmanaged mcp servers",
		"would restore mcp servers",
	} {
		idx := strings.Index(out, want)
		if idx < 0 {
			t.Fatalf("output = %q, want %q", out, want)
		}
		if idx <= last {
			t.Fatalf("output = %q, want %q after the previous sync phase", out, want)
		}
		last = idx
	}
}

func TestSyncAllFlag_SkipsAgentsLegWhenAgentsDisabled(t *testing.T) {
	a := newSyncAllAgentsTestApp(t, config.Settings{AgentsDisabled: config.BoolPtr(true)})
	out, err := runSyncAll(t, a)
	if err != nil {
		t.Fatalf("sync --all: %v", err)
	}
	if strings.Contains(out, "Agents sync complete") {
		t.Errorf("output = %q, want the agents leg skipped entirely", out)
	}
}

func TestSyncAllFlag_ExitsNonZeroWhenAgentsLegErrors(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{AgentsUse: []string{"codex"}},
		Groups:   []*config.GroupConfig{cliTestHostGroup()},
		Agents: config.AgentsConfig{
			Packages: []config.SkillPackage{{Source: filepath.Join(home, "no-such-skill-source")}},
		},
	})
	a := app.New(cfgPath)
	a.CacheDir = t.TempDir()
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	out, err := runSyncAll(t, a)
	if err == nil {
		t.Fatalf("sync --all exited 0 with a failing agents leg:\n%s", out)
	}
	if !strings.Contains(err.Error(), "agent operation(s) failed") {
		t.Fatalf("error = %v, want the agent-failure verdict", err)
	}
	if !strings.Contains(out, "Sync all complete") {
		t.Errorf("output = %q, want the tool phase summary to still print", out)
	}
}

func TestSyncAllFlag_ConfirmPromptNamesAgentScope(t *testing.T) {
	a := newSyncAllAgentsTestApp(t, config.Settings{})
	cmd := newSyncCmd(&rootState{app: a})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--all"})
	// The prompt aborts without a terminal answer; the question text is what matters here.
	_ = cmd.ExecuteContext(context.Background())
	prompt := out.String()
	for _, want := range []string{"add discovered tools to config", "import unmanaged agent skills", "sync agent skills, MCP servers, and plugins"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("confirm prompt = %q, want it to mention %q", prompt, want)
		}
	}
}

func TestSyncAllFlag_ExitsNonZeroWhenAToolFails(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Tools:  map[string]config.ToolSpec{"ripgrep": {Provider: "brew"}},
		Groups: []*config.GroupConfig{cliTestHostGroup("ripgrep")},
	})
	a := app.New(cfgPath)
	a.CacheDir = t.TempDir()
	if err := a.InitTestMode(context.Background(), &cliStubProvider{
		name:       "brew",
		installErr: errors.New("network unreachable"),
	}); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	out, err := runSyncAll(t, a)
	if err == nil {
		t.Fatalf("sync --all exited 0 with a failing tool; output:\n%s", out)
	}
	if !strings.Contains(err.Error(), "failed") {
		t.Fatalf("error = %q, want it to name the tool failure", err)
	}
}
