package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
)

func TestDecodeNativeListAcceptsEmptySuccessfulOutput(t *testing.T) {
	var entries []map[string]any
	if err := decodeNativeList("", "installed", &entries); err != nil || len(entries) != 0 {
		t.Fatalf("decode empty inventory = %#v, %v", entries, err)
	}
}

type nativeInventoryExecutor struct {
	*executor.MatchMockExecutor
	available map[string]bool
}

func (e *nativeInventoryExecutor) CommandAvailable(name string) bool { return e.available[name] }

func newNativeInventoryApp(t *testing.T, available map[string]bool, rules ...executor.MatchRule) (*App, *nativeInventoryExecutor) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	configPath := filepath.Join(home, "config", "omni", "settings.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	exec := &nativeInventoryExecutor{MatchMockExecutor: executor.NewMatchMock(rules...), available: available}
	a := New(configPath)
	a.StateDir = filepath.Join(home, "state", "omni")
	a.SetFallbackExecutor(exec)
	return a, exec
}

func nativeRule(command, stdout string) executor.MatchRule {
	return executor.MatchRule{Pattern: command, Response: executor.MockCall{Stdout: stdout}}
}

func TestRecoverNativePluginPlanClaudeOnly(t *testing.T) {
	a, _ := newNativeInventoryApp(t, map[string]bool{"claude": true},
		nativeRule("claude plugins list --json", `[{"id":"demo@official"}]`),
		nativeRule("claude plugins marketplace list --json", `[{"name":"official","source":"github","repo":"acme/plugins"}]`),
	)
	_, rendered, err := a.recoverNativePluginPlan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"name: demo", "marketplace: official", "- claude", "# apm marketplace add acme/plugins --name official"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered manifest missing %q:\n%s", want, rendered)
		}
	}
}

func TestRecoverNativePluginPlanCodexOnlyObjectForms(t *testing.T) {
	a, _ := newNativeInventoryApp(t, map[string]bool{"codex": true},
		nativeRule("codex plugin list --json", `{"installed":[{"name":"demo","marketplaceName":"official"}],"available":[]}`),
		nativeRule("codex plugin marketplace list --json", `{"marketplaces":[{"name":"official","marketplaceSource":{"source":"https://example.test/plugins.git"}}]}`),
	)
	_, rendered, err := a.recoverNativePluginPlan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"name: demo", "- codex", "# apm marketplace add https://example.test/plugins.git --name official"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered manifest missing %q:\n%s", want, rendered)
		}
	}
}

func TestRecoverNativePluginPlanUnionsTargets(t *testing.T) {
	a, _ := newNativeInventoryApp(t, map[string]bool{"claude": true, "codex": true},
		nativeRule("claude plugins list --json", `{"installed":[{"id":"@scope/demo@official"}]}`),
		nativeRule("claude plugins marketplace list --json", `{"marketplaces":[{"name":"official","source":"github","repo":"acme/plugins"}]}`),
		nativeRule("codex plugin list --json", `[{"name":"@scope/demo","marketplaceName":"official"}]`),
		nativeRule("codex plugin marketplace list --json", `[{"name":"official","marketplaceSource":{"source":"acme/plugins"}}]`),
	)
	_, rendered, err := a.recoverNativePluginPlan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"name: '@scope/demo'", "- claude", "- codex"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered manifest missing %q:\n%s", want, rendered)
		}
	}
	if strings.Count(rendered, "marketplace: official") != 1 {
		t.Fatalf("plugin was not unioned:\n%s", rendered)
	}
}

func TestRecoverNativePluginPlanFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name  string
		rules []executor.MatchRule
		want  string
	}{
		{name: "malformed plugin JSON", rules: []executor.MatchRule{
			nativeRule("claude plugins list --json", `{`),
		}, want: "parse json"},
		{name: "marketplace command failure identifies installed plugin", rules: []executor.MatchRule{
			nativeRule("claude plugins list --json", `[{"id":"demo@official"}]`),
			{Pattern: "claude plugins marketplace list --json", Response: executor.MockCall{Err: errors.New("boom")}},
		}, want: "demo@official"},
		{name: "ambiguous marketplace source identifies installed plugin", rules: []executor.MatchRule{
			nativeRule("claude plugins list --json", `[{"id":"demo@official"}]`),
			nativeRule("claude plugins marketplace list --json", `[{"name":"official","source":"github","repo":"one/plugins"},{"name":"official","source":"github","repo":"two/plugins"}]`),
		}, want: "demo@official"},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, _ := newNativeInventoryApp(t, map[string]bool{"claude": true}, test.rules...)
			_, rendered, err := a.recoverNativePluginPlan(context.Background())
			if err == nil || !strings.Contains(err.Error(), test.want) || rendered != "" {
				t.Fatalf("rendered=%q err=%v; want error containing %q", rendered, err, test.want)
			}
			template, templateErr := AgentsTemplatePath()
			if templateErr != nil {
				t.Fatal(templateErr)
			}
			if _, statErr := os.Stat(template); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("failed recovery wrote template: %v", statErr)
			}
		})
	}
}

func TestPrepareAgentsOnboardingRecoversNativePluginsAndHandlesNoNative(t *testing.T) {
	t.Run("native", func(t *testing.T) {
		a, _ := newNativeInventoryApp(t, map[string]bool{"claude": true},
			nativeRule("claude plugins list --json", `[{"id":"demo@official"}]`),
			nativeRule("claude plugins marketplace list --json", `[{"name":"official","source":"github","repo":"acme/plugins"}]`),
		)
		result, snapshot, err := a.prepareAgentsOnboarding(t.Context(), "host")
		if err != nil || snapshot != "" || result.Readiness.State != AgentsReadinessTemplateOnly {
			t.Fatalf("result=%+v snapshot=%q err=%v", result, snapshot, err)
		}
		raw, err := os.ReadFile(result.Readiness.TemplatePath)
		if err != nil || !strings.Contains(string(raw), "name: demo") {
			t.Fatalf("template=%q err=%v", raw, err)
		}
	})

	t.Run("no native", func(t *testing.T) {
		a, exec := newNativeInventoryApp(t, map[string]bool{})
		plan, rendered, err := a.recoverNativePluginPlan(t.Context())
		if err != nil || len(plan.Decls.Plugins) != 0 || !strings.Contains(rendered, "name: omni-migrated") {
			t.Fatalf("plan=%+v rendered=%q err=%v", plan, rendered, err)
		}
		if exec.CallCount() != 0 {
			t.Fatalf("unavailable CLIs invoked: %+v", exec.Calls)
		}
	})
}
