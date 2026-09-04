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

func nativePlanFor(t *testing.T, a *App) (agentBundlePlan, string, error) {
	t.Helper()
	observations, err := a.inventoryNativeAgents(t.Context())
	if err != nil {
		return agentBundlePlan{}, "", err
	}
	plan, rendered := nativeAgentPlan(resolveAgentDispositions(observations))
	return plan, rendered, nil
}

func TestNativePlanClaudeOnly(t *testing.T) {
	a, _ := newNativeInventoryApp(t, map[string]bool{"claude": true},
		nativeRule("claude plugins list --json", `[{"id":"demo@official"}]`),
		nativeRule("claude plugins marketplace list --json", `[{"name":"official","source":"github","repo":"acme/plugins"}]`),
	)
	_, rendered, err := nativePlanFor(t, a)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"name: demo", "marketplace: official", "- claude", "# apm marketplace add acme/plugins --name official"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered manifest missing %q:\n%s", want, rendered)
		}
	}
}

func TestNativePlanCodexOnlyObjectForms(t *testing.T) {
	a, _ := newNativeInventoryApp(t, map[string]bool{"codex": true},
		nativeRule("codex plugin list --json", `{"installed":[{"name":"demo","marketplaceName":"official"}],"available":[]}`),
		nativeRule("codex plugin marketplace list --json", `{"marketplaces":[{"name":"official","marketplaceSource":{"source":"https://example.test/plugins.git"}}]}`),
	)
	_, rendered, err := nativePlanFor(t, a)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"name: demo", "- codex", "# apm marketplace add https://example.test/plugins.git --name official"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered manifest missing %q:\n%s", want, rendered)
		}
	}
}

func TestNativePlanUnionsTargets(t *testing.T) {
	a, _ := newNativeInventoryApp(t, map[string]bool{"claude": true, "codex": true},
		nativeRule("claude plugins list --json", `{"installed":[{"id":"@scope/demo@official"}]}`),
		nativeRule("claude plugins marketplace list --json", `{"marketplaces":[{"name":"official","source":"github","repo":"acme/plugins"}]}`),
		nativeRule("codex plugin list --json", `[{"name":"@scope/demo","marketplaceName":"official"},{"name":"superpowers","marketplaceName":"openai-curated"}]`),
		nativeRule("codex plugin marketplace list --json", `[{"name":"official","marketplaceSource":{"source":"acme/plugins"}},{"name":"openai-curated","marketplaceSource":{"source":""}}]`),
	)
	_, rendered, err := nativePlanFor(t, a)
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
	if !strings.Contains(rendered, "# retained: plugin superpowers@openai-curated [codex]: "+agentReasonNoSource) || strings.Contains(rendered, "marketplace: openai-curated") {
		t.Fatalf("source-less marketplace plugin was not retained:\n%s", rendered)
	}
}

func TestCanonicalNativeMarketplaceSourceOnlyTrims(t *testing.T) {
	for source, want := range map[string]string{
		"  ../market ":                           "../market",
		"mksglu/context-mode":                    "mksglu/context-mode",
		"git@github.com:mksglu/context-mode.git": "git@github.com:mksglu/context-mode.git",
	} {
		if got := canonicalNativeMarketplaceSource(source); got != want {
			t.Fatalf("canonicalNativeMarketplaceSource(%q) = %q, want %q", source, got, want)
		}
	}
}

func TestInventoryNativeAgentsFailsClosed(t *testing.T) {
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
	} {
		t.Run(test.name, func(t *testing.T) {
			a, _ := newNativeInventoryApp(t, map[string]bool{"claude": true}, test.rules...)
			observations, err := a.inventoryNativeAgents(context.Background())
			if err == nil || !strings.Contains(err.Error(), test.want) || observations != nil {
				t.Fatalf("observations=%#v err=%v; want error containing %q", observations, err, test.want)
			}
			template, templateErr := AgentsTemplatePath()
			if templateErr != nil {
				t.Fatal(templateErr)
			}
			if _, statErr := os.Stat(template); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("failed inventory wrote template: %v", statErr)
			}
		})
	}
}

func TestNativePlanWithoutNativeCLIs(t *testing.T) {
	a, exec := newNativeInventoryApp(t, map[string]bool{})
	plan, rendered, err := nativePlanFor(t, a)
	if err != nil || len(plan.Decls.Plugins) != 0 || !strings.Contains(rendered, "name: omni-migrated") {
		t.Fatalf("plan=%+v rendered=%q err=%v", plan, rendered, err)
	}
	if exec.CallCount() != 0 {
		t.Fatalf("unavailable CLIs invoked: %+v", exec.Calls)
	}
}

func TestNativePlanIncludesAndUnionsMCP(t *testing.T) {
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
	plan, rendered, err := nativePlanFor(t, a)
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

func TestNativePlanRetainsPerTargetMCPDifferences(t *testing.T) {
	a, _ := newNativeInventoryApp(t, map[string]bool{"claude": true, "codex": true},
		nativeRule("claude plugins list --json", `[]`),
		nativeRule("claude plugins marketplace list --json", `[]`),
		nativeRule("codex plugin list --json", `[]`),
		nativeRule("codex plugin marketplace list --json", `[]`),
		nativeRule("codex mcp list --json", `[{"name":"demo","transport":{"type":"stdio","command":"npx","args":["other"]}}]`),
	)
	home, _ := os.UserHomeDir()
	writeFile(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"demo":{"command":"npx","args":["demo"]}}}`)
	plan, rendered, err := nativePlanFor(t, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Decls.MCPServers) != 1 {
		t.Fatalf("MCP declarations = %#v", plan.Decls.MCPServers)
	}
	if !strings.Contains(rendered, "# retained: mcp demo [codex]: "+agentReasonPerTarget) {
		t.Fatalf("codex variant was not retained:\n%s", rendered)
	}
}

// seedClaudePluginOwner points the plugin CLI at a real install root holding manifest evidence.
func seedClaudePluginOwner(t *testing.T, exec *nativeInventoryExecutor, home, manifest string) string {
	t.Helper()
	root := filepath.Join(home, ".claude", "plugins", "cache", "context-mode", "context-mode", "1.0.169")
	writeFile(t, filepath.Join(root, ".claude-plugin", "plugin.json"), manifest)
	exec.AddRule(nativeRule("claude plugins list --json", fmt.Sprintf(`[{"id":"context-mode@context-mode","version":"1.0.169","installPath":%q}]`, root)))
	exec.AddRule(nativeRule("claude plugins marketplace list --json", `[{"name":"context-mode","source":"github","repo":"mksglu/context-mode"}]`))
	return root
}

func TestNativePlanSuppressesManifestBackedMCP(t *testing.T) {
	a, exec := newNativeInventoryApp(t, map[string]bool{"claude": true})
	home, _ := os.UserHomeDir()
	root := seedClaudePluginOwner(t, exec, home, `{"name":"context-mode","mcpServers":{"context-mode":{"command":"node","args":["start.mjs"]}}}`)
	writeFile(t, filepath.Join(home, ".claude.json"), fmt.Sprintf(`{"mcpServers":{"context-mode":{"type":"stdio","command":"node","args":["start.mjs"],"cwd":%q}}}`, root))

	plan, rendered, err := nativePlanFor(t, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Decls.MCPServers) != 0 {
		t.Fatalf("plugin-owned MCP wrapper was imported: %#v", plan.Decls.MCPServers)
	}
	if !strings.Contains(rendered, "# suppressed: plugin-owned native MCP context-mode (context-mode@context-mode)") {
		t.Fatalf("suppression trailer missing:\n%s", rendered)
	}
}

func TestNativePlanDoesNotSuppressWithoutManifestEvidence(t *testing.T) {
	for _, test := range []struct {
		name     string
		manifest string
		server   string
	}{
		{name: "cache path alone", manifest: `{"name":"context-mode"}`, server: `{"type":"stdio","command":"node","args":["start.mjs"],"cwd":%q}`},
		{name: "different definition", manifest: `{"name":"context-mode","mcpServers":{"context-mode":{"command":"node","args":["start.mjs"]}}}`, server: `{"type":"stdio","command":"node","args":["custom.mjs"],"cwd":%q}`},
		{name: "extra target config", manifest: `{"name":"context-mode","mcpServers":{"context-mode":{"command":"node","args":["start.mjs"]}}}`, server: `{"type":"stdio","command":"node","args":["start.mjs"],"env":{"MODE":"custom"},"cwd":%q}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, exec := newNativeInventoryApp(t, map[string]bool{"claude": true})
			home, _ := os.UserHomeDir()
			root := seedClaudePluginOwner(t, exec, home, test.manifest)
			writeFile(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"context-mode":`+fmt.Sprintf(test.server, root)+`}}`)

			plan, rendered, err := nativePlanFor(t, a)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Decls.MCPServers) != 1 {
				t.Fatalf("unproven MCP was suppressed: %#v\n%s", plan.Decls.MCPServers, rendered)
			}
		})
	}
}

func TestInventoryNativeMCPNeverSerializesLiteralSecrets(t *testing.T) {
	t.Run("literal sensitive env fails without echoing value", func(t *testing.T) {
		a, _ := newNativeInventoryApp(t, map[string]bool{"claude": true},
			nativeRule("claude plugins list --json", `[]`),
			nativeRule("claude plugins marketplace list --json", `[]`),
		)
		home, _ := os.UserHomeDir()
		writeFile(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"demo":{"command":"server","env":{"TOKEN":"super-secret"}}}}`)
		_, rendered, err := nativePlanFor(t, a)
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
		_, rendered, err := nativePlanFor(t, a)
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
		_, rendered, err := nativePlanFor(t, a)
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
