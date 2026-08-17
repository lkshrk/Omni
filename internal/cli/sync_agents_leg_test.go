package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/executor"
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

type failingAPMExecutor struct {
	executor.MockExecutor
	orphan string
}

func (e *failingAPMExecutor) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	if name == "apm" {
		if err := os.MkdirAll(filepath.Dir(e.orphan), 0o700); err == nil {
			_ = os.WriteFile(e.orphan, []byte("deployed\n"), 0o600)
		}
	}
	return e.MockExecutor.Run(ctx, name, args...)
}

// The reversal warnings describe what the failed install left behind and what was undone; returning the error
// without printing them loses the only record this host gets.
func TestSyncAllAgentsLegPrintsReversalsWhenTheInstallFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{AgentsUse: []string{"codex"}},
		Agents:   config.AgentsConfig{Packages: []config.SkillPackage{{Source: "acme/shared", Agents: []string{"codex"}}}},
	})
	a := app.New(cfgPath)
	orphan := filepath.Join(home, ".agents", "skills", "acme-shared", "SKILL.md")
	failing := &failingAPMExecutor{orphan: orphan}
	failing.Responses = []executor.MockCall{{Stderr: "install aborted\n", Err: errors.New("exit status 1")}}
	a.SetFallbackExecutor(failing)

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	err := runSyncAllAgentsLeg(cmd, &rootState{app: a}, false)
	if err == nil {
		t.Fatalf("failed install exited 0; output:\n%s", out.String())
	}
	if !strings.Contains(out.String(), orphan) {
		t.Fatalf("output = %q, want the reversal reported", out.String())
	}
}
