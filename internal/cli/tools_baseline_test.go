package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
)

func newBaselineCLIApp(t *testing.T) (*app.App, *cliStubProvider) {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	if err := config.Save(cfgPath, &config.RootConfig{Version: config.CurrentVersion}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	apt := &cliStubProvider{name: "apt", installed: []provider.InstalledTool{
		{Tool: provider.Tool{Name: "libnss3", Provider: "apt"}},
		{Tool: provider.Tool{Name: "xvfb", Provider: "apt"}},
	}}
	a := app.New(cfgPath)
	if err := a.InitTestMode(context.Background(), apt); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a, apt
}

func runToolsBaseline(t *testing.T, a *app.App, args ...string) (string, error) {
	t.Helper()
	cmd := newToolsBaselineCmd(&rootState{app: a, yes: true})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return out.String(), err
}

func TestToolsBaselineCmd_DryRunReportsWithoutWriting(t *testing.T) {
	a, _ := newBaselineCLIApp(t)

	out, err := runToolsBaseline(t, a, "--dry-run")
	if err != nil {
		t.Fatalf("tools baseline --dry-run: %v", err)
	}
	for _, want := range []string{"Would absorb 2 packages", "apt: 2 packages", "libnss3", "xvfb"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}

	again, err := runToolsBaseline(t, a, "--dry-run")
	if err != nil {
		t.Fatalf("second tools baseline --dry-run: %v", err)
	}
	if !strings.Contains(again, "Would absorb 2 packages") {
		t.Fatalf("dry run wrote the baseline:\n%s", again)
	}
}

func TestToolsBaselineCmd_AbsorbsThenReportsNothingLeft(t *testing.T) {
	a, _ := newBaselineCLIApp(t)

	out, err := runToolsBaseline(t, a)
	if err != nil {
		t.Fatalf("tools baseline: %v", err)
	}
	if !strings.Contains(out, "Will absorb 2 packages") || !strings.Contains(out, "Absorbed 2 packages") {
		t.Fatalf("output should preview then confirm the absorption:\n%s", out)
	}

	after, err := runToolsBaseline(t, a)
	if err != nil {
		t.Fatalf("second tools baseline: %v", err)
	}
	if !strings.Contains(after, "No system packages to absorb") {
		t.Fatalf("output = %q; a second run has nothing left to absorb", after)
	}
}

func TestToolsBaselineCmd_RegisteredUnderTools(t *testing.T) {
	cmd := newToolsCmd(&rootState{})
	for _, sub := range cmd.Commands() {
		if sub.Name() == "baseline" {
			if sub.Flags().Lookup("dry-run") == nil {
				t.Fatal("tools baseline is missing --dry-run")
			}
			return
		}
	}
	t.Fatal("tools baseline is not registered under tools")
}
