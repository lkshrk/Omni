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
