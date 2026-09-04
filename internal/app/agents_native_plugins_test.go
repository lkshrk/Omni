package app

import (
	"context"
	"errors"
	"fmt"
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

func TestListNativePluginsSkipsDisabledEntries(t *testing.T) {
	for _, test := range []struct {
		cli, output string
	}{
		{cli: "claude", output: `[{"id":"off@official","enabled":false},{"id":"on@official","enabled":true}]`},
		{cli: "codex", output: `{"installed":[{"name":"off","marketplaceName":"official","enabled":false},{"name":"on","marketplaceName":"official","enabled":true}],"available":[]}`},
	} {
		t.Run(test.cli, func(t *testing.T) {
			command := test.cli + " plugin list --json"
			if test.cli == "claude" {
				command = "claude plugins list --json"
			}
			a, _ := newNativeInventoryApp(t, map[string]bool{test.cli: true}, nativeRule(command, test.output))
			got, err := a.listNativePlugins(t.Context(), test.cli)
			if err != nil || len(got) != 1 || got[0].Name != "on" {
				t.Fatalf("plugins = %#v, %v", got, err)
			}
		})
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
	_, rendered, err := a.recoverNativeAgentPlan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"name: demo", "marketplace: official", "- claude", "# apm marketplace add https://github.com/acme/plugins.git --name official"} {
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
	_, rendered, err := a.recoverNativeAgentPlan(t.Context())
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
		nativeRule("codex plugin list --json", `[{"name":"@scope/demo","marketplaceName":"official"},{"name":"superpowers","marketplaceName":"openai-curated"}]`),
		nativeRule("codex plugin marketplace list --json", `[{"name":"official","marketplaceSource":{"source":"https://github.com/ACME/plugins.git"}},{"name":"openai-curated","marketplaceSource":{"source":""}}]`),
	)
	_, rendered, err := a.recoverNativeAgentPlan(t.Context())
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
	if !strings.Contains(rendered, "# native-only: superpowers@openai-curated") || strings.Contains(rendered, "marketplace: openai-curated") {
		t.Fatalf("Codex built-in plugin was not preserved as native-only:\n%s", rendered)
	}
}

func TestCanonicalNativeMarketplaceSource(t *testing.T) {
	want := "https://github.com/mksglu/context-mode.git"
	for _, source := range []string{
		"mksglu/context-mode",
		"https://github.com/mksglu/context-mode.git",
		"git@github.com:mksglu/context-mode.git",
		"ssh://git@github.com/MKSGLU/context-mode",
	} {
		if got := canonicalNativeMarketplaceSource(source); got != want {
			t.Fatalf("canonicalNativeMarketplaceSource(%q) = %q", source, got)
		}
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
			_, rendered, err := a.recoverNativeAgentPlan(context.Background())
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

func TestRecoverNativeAgentPlanWithoutNativeCLIs(t *testing.T) {
	a, exec := newNativeInventoryApp(t, map[string]bool{})
	plan, rendered, err := a.recoverNativeAgentPlan(t.Context())
	if err != nil || len(plan.Decls.Plugins) != 0 || !strings.Contains(rendered, "name: omni-migrated") {
		t.Fatalf("plan=%+v rendered=%q err=%v", plan, rendered, err)
	}
	if exec.CallCount() != 0 {
		t.Fatalf("unavailable CLIs invoked: %+v", exec.Calls)
	}
}

func TestRecoverNativeAgentPlanIncludesAndUnionsMCP(t *testing.T) {
	a, _ := newNativeInventoryApp(t, map[string]bool{"claude": true, "codex": true},
		nativeRule("claude plugins list --json", `[]`),
		nativeRule("claude plugins marketplace list --json", `[]`),
		nativeRule("codex plugin list --json", `[]`),
		nativeRule("codex plugin marketplace list --json", `[]`),
		nativeRule("codex mcp list --json", `[{"name":"demo","transport":{"type":"stdio","command":"npx","args":["demo"]}}]`),
	)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"demo":{"type":"stdio","command":"npx","args":["demo"]}}}`)
	plan, rendered, err := a.recoverNativeAgentPlan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Decls.MCPServers) != 1 {
		t.Fatalf("MCP declarations = %#v", plan.Decls.MCPServers)
	}
	for _, want := range []string{"name: demo", "transport: stdio", "command: npx", "- demo", "- claude", "- codex"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered manifest missing %q:\n%s", want, rendered)
		}
	}
}

func TestRecoverNativeAgentPlanRejectsConflictingMCPWithoutWriting(t *testing.T) {
	a, _ := newNativeInventoryApp(t, map[string]bool{"claude": true, "codex": true},
		nativeRule("claude plugins list --json", `[]`),
		nativeRule("claude plugins marketplace list --json", `[]`),
		nativeRule("codex plugin list --json", `[]`),
		nativeRule("codex plugin marketplace list --json", `[]`),
		nativeRule("codex mcp list --json", `[{"name":"demo","transport":{"type":"stdio","command":"npx","args":["other"]}}]`),
	)
	home, _ := os.UserHomeDir()
	writeFile(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"demo":{"command":"npx","args":["demo"]}}}`)
	_, rendered, err := a.recoverNativeAgentPlan(t.Context())
	if err == nil || !strings.Contains(err.Error(), `MCP server "demo" has conflicting definitions`) || rendered != "" {
		t.Fatalf("rendered=%q err=%v", rendered, err)
	}
	template, _ := AgentsTemplatePath()
	if _, statErr := os.Stat(template); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("conflict wrote template: %v", statErr)
	}
}

func TestRecoverNativeAgentPlanSuppressesPluginOwnedMCPWrappers(t *testing.T) {
	a, exec := newNativeInventoryApp(t, map[string]bool{"claude": true, "codex": true},
		nativeRule("claude plugins list --json", `[{"id":"context-mode@context-mode"}]`),
		nativeRule("claude plugins marketplace list --json", `[{"name":"context-mode","source":"github","repo":"mksglu/context-mode"}]`),
		nativeRule("codex plugin list --json", `{"installed":[{"name":"context-mode","marketplaceName":"context-mode"}],"available":[]}`),
		nativeRule("codex plugin marketplace list --json", `{"marketplaces":[{"name":"context-mode","marketplaceSource":{"source":"https://github.com/mksglu/context-mode.git"}}]}`),
	)
	home, _ := os.UserHomeDir()
	pluginCwd := filepath.Join(home, ".codex", "plugins", "cache", "context-mode", "context-mode", "1.0.169")
	exec.AddRule(nativeRule("codex mcp list --json", fmt.Sprintf(`[{"name":"context-mode","enabled":true,"transport":{"type":"stdio","command":"node","args":["./start.mjs"],"cwd":%q,"env":{"CONTEXT_MODE_PLATFORM":"codex"}}}]`, pluginCwd)))
	writeFile(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"context-mode":{"type":"stdio","command":"node","args":["./start.mjs"]}}}`)

	plan, rendered, err := a.recoverNativeAgentPlan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Decls.MCPServers) != 0 {
		t.Fatalf("plugin-owned MCP wrapper was imported: %#v", plan.Decls.MCPServers)
	}
	for _, want := range []string{"name: context-mode", "marketplace: context-mode", "- claude", "- codex", "# suppressed: plugin-owned native MCP context-mode"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered manifest missing %q:\n%s", want, rendered)
		}
	}
}

func TestRecoverNativeAgentPlanDoesNotSuppressUnprovenMCP(t *testing.T) {
	a, _ := newNativeInventoryApp(t, map[string]bool{"claude": true, "codex": true},
		nativeRule("claude plugins list --json", `[{"id":"context-mode@context-mode"}]`),
		nativeRule("claude plugins marketplace list --json", `[{"name":"context-mode","source":"github","repo":"mksglu/context-mode"}]`),
		nativeRule("codex plugin list --json", `[{"name":"context-mode","marketplaceName":"context-mode"}]`),
		nativeRule("codex plugin marketplace list --json", `[{"name":"context-mode","marketplaceSource":{"source":"mksglu/context-mode"}}]`),
		nativeRule("codex mcp list --json", `[{"name":"context-mode","transport":{"type":"stdio","command":"node","args":["other.mjs"],"cwd":"/tmp/not-a-plugin"}}]`),
	)
	home, _ := os.UserHomeDir()
	writeFile(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"context-mode":{"type":"stdio","command":"node","args":["./start.mjs"]}}}`)

	_, _, err := a.recoverNativeAgentPlan(t.Context())
	if err == nil || !strings.Contains(err.Error(), `MCP server "context-mode" has conflicting definitions`) {
		t.Fatalf("error = %v, want conflicting definitions", err)
	}
}

func TestRecoverNativeAgentPlanDoesNotSuppressDifferentSameNameWrapper(t *testing.T) {
	a, exec := newNativeInventoryApp(t, map[string]bool{"claude": true, "codex": true},
		nativeRule("claude plugins list --json", `[{"id":"context-mode@context-mode"}]`),
		nativeRule("claude plugins marketplace list --json", `[{"name":"context-mode","source":"github","repo":"mksglu/context-mode"}]`),
		nativeRule("codex plugin list --json", `[{"name":"context-mode","marketplaceName":"context-mode"}]`),
		nativeRule("codex plugin marketplace list --json", `[{"name":"context-mode","marketplaceSource":{"source":"mksglu/context-mode"}}]`),
	)
	home, _ := os.UserHomeDir()
	pluginCwd := filepath.Join(home, ".codex", "plugins", "cache", "context-mode", "context-mode", "1.0.169")
	exec.AddRule(nativeRule("codex mcp list --json", fmt.Sprintf(`[{"name":"context-mode","transport":{"type":"stdio","command":"node","args":["./start.mjs"],"cwd":%q}}]`, pluginCwd)))
	writeFile(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"context-mode":{"type":"stdio","command":"node","args":["custom.mjs"]}}}`)

	plan, _, err := a.recoverNativeAgentPlan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Decls.MCPServers) != 1 {
		t.Fatalf("unrelated same-name MCP was suppressed: %#v", plan.Decls.MCPServers)
	}
}

func TestRecoverNativeAgentPlanDoesNotSuppressConfiguredSameNameWrapper(t *testing.T) {
	a, exec := newNativeInventoryApp(t, map[string]bool{"claude": true, "codex": true},
		nativeRule("claude plugins list --json", `[{"id":"context-mode@context-mode"}]`),
		nativeRule("claude plugins marketplace list --json", `[{"name":"context-mode","source":"github","repo":"mksglu/context-mode"}]`),
		nativeRule("codex plugin list --json", `[{"name":"context-mode","marketplaceName":"context-mode"}]`),
		nativeRule("codex plugin marketplace list --json", `[{"name":"context-mode","marketplaceSource":{"source":"mksglu/context-mode"}}]`),
	)
	home, _ := os.UserHomeDir()
	pluginCwd := filepath.Join(home, ".codex", "plugins", "cache", "context-mode", "context-mode", "1.0.169")
	exec.AddRule(nativeRule("codex mcp list --json", fmt.Sprintf(`[{"name":"context-mode","transport":{"type":"stdio","command":"node","args":["./start.mjs"],"cwd":%q}}]`, pluginCwd)))
	writeFile(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"context-mode":{"type":"stdio","command":"node","args":["./start.mjs"],"env":{"MODE":"custom"}}}}`)

	plan, _, err := a.recoverNativeAgentPlan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Decls.MCPServers) != 1 {
		t.Fatalf("configured same-name MCP was suppressed: %#v", plan.Decls.MCPServers)
	}
}

func TestRecoverNativeMCPNeverSerializesLiteralSecrets(t *testing.T) {
	t.Run("literal sensitive env fails without echoing value", func(t *testing.T) {
		a, _ := newNativeInventoryApp(t, map[string]bool{"claude": true},
			nativeRule("claude plugins list --json", `[]`),
			nativeRule("claude plugins marketplace list --json", `[]`),
		)
		home, _ := os.UserHomeDir()
		writeFile(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"demo":{"command":"server","env":{"TOKEN":"super-secret"}}}}`)
		_, rendered, err := a.recoverNativeAgentPlan(t.Context())
		if err == nil || !strings.Contains(err.Error(), `sensitive environment field "TOKEN"`) || strings.Contains(err.Error(), "super-secret") || rendered != "" {
			t.Fatalf("rendered=%q err=%v", rendered, err)
		}
	})

	t.Run("literal header fails without echoing value", func(t *testing.T) {
		a, _ := newNativeInventoryApp(t, map[string]bool{"claude": true},
			nativeRule("claude plugins list --json", `[]`),
			nativeRule("claude plugins marketplace list --json", `[]`),
		)
		home, _ := os.UserHomeDir()
		writeFile(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"demo":{"url":"https://example.test","headers":{"Authorization":"secret-value"}}}}`)
		_, rendered, err := a.recoverNativeAgentPlan(t.Context())
		if err == nil || !strings.Contains(err.Error(), `server "demo" has literal sensitive header "Authorization"`) || strings.Contains(err.Error(), "secret-value") || rendered != "" {
			t.Fatalf("rendered=%q err=%v", rendered, err)
		}
	})

	t.Run("symbolic Claude and current Codex fields", func(t *testing.T) {
		a, _ := newNativeInventoryApp(t, map[string]bool{"codex": true},
			nativeRule("codex plugin list --json", `[]`),
			nativeRule("codex plugin marketplace list --json", `[]`),
			nativeRule("codex mcp list --json", `[
			{"name":"disabled","enabled":false,"transport":{"type":"stdio","command":"bad"}},
			{"name":"stdio","enabled":true,"transport":{"type":"stdio","command":"server","env":{"TOKEN":"${TOKEN}"},"env_vars":["EXTRA"],"cwd":"/tmp/work"}},
			{"name":"remote","enabled":true,"transport":{"type":"streamable_http","url":"https://example.test","bearer_token_env_var":"API_TOKEN"}}
		]`),
		)
		_, rendered, err := a.recoverNativeAgentPlan(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"TOKEN: ${env:TOKEN}", "EXTRA: ${env:EXTRA}", "cwd: /tmp/work", "Authorization: Bearer ${env:API_TOKEN}"} {
			if !strings.Contains(rendered, want) {
				t.Fatalf("rendered manifest missing %q:\n%s", want, rendered)
			}
		}
		if strings.Contains(rendered, "literal") || strings.Contains(rendered, "disabled") {
			t.Fatalf("unsafe/disabled Codex state rendered:\n%s", rendered)
		}
	})
}

func TestAgentsMigrateWithoutSnapshotPreviewsNativeState(t *testing.T) {
	a, _ := newNativeInventoryApp(t, map[string]bool{"claude": true},
		nativeRule("claude plugins list --json", `[{"id":"demo@official"}]`),
		nativeRule("claude plugins marketplace list --json", `[{"name":"official","source":"github","repo":"acme/plugins"}]`),
	)
	home, _ := os.UserHomeDir()
	writeFile(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"native":{"command":"npx","args":["native-mcp"]}}}`)

	rendered, err := a.AgentsMigrate(t.Context(), "host", "")
	if err != nil {
		t.Fatalf("native-only migrate failed: %v", err)
	}
	for _, want := range []string{"name: demo", "marketplace: official", "name: native"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("preview missing %q:\n%s", want, rendered)
		}
	}
	template, _ := AgentsTemplatePath()
	if _, err := os.Stat(template); !os.IsNotExist(err) {
		t.Fatalf("preview wrote the template: %v", err)
	}
}
