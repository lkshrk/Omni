package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func apmUIConfig(servers ...config.McpServer) *config.RootConfig {
	return &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"claude-code", "gemini-cli"}},
		Agents:   config.AgentsConfig{McpServers: servers},
	}
}

func writeClaudeState(t *testing.T, a *App, dir, body string) {
	t.Helper()
	if dir == "" {
		manifestPath, err := a.globalAPMManifestPath()
		if err != nil {
			t.Fatal(err)
		}
		dir = filepath.Dir(filepath.Dir(manifestPath))
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mcpRowByName(t *testing.T, rows []McpServerRow, name string) McpServerRow {
	t.Helper()
	for _, row := range rows {
		if row.Name == name {
			return row
		}
	}
	t.Fatalf("row %q missing from %#v", name, rows)
	return McpServerRow{}
}

func TestMcpServerRowsReportsTheAPMAgentsBeyondTheDeclaredScope(t *testing.T) {
	cfg := apmUIConfig(config.McpServer{
		Name: "linear", Transport: "http", URL: "https://linear.example/mcp", Agents: []string{"claude-code"},
	})
	a, _, _ := newSyncApp(t, cfg, "name: t\nversion: 1.0.0\n", WithMcpAdapters([]McpAdapter{}))

	rows, _, err := a.McpServerRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	row := mcpRowByName(t, rows, "linear")
	if want := []string{"claude-code", "gemini-cli"}; !reflect.DeepEqual(row.APMAgents, want) {
		t.Fatalf("APMAgents = %v, want %v", row.APMAgents, want)
	}
	if want := []string{"gemini-cli"}; !reflect.DeepEqual(row.UndeclaredAPMAgents, want) {
		t.Fatalf("UndeclaredAPMAgents = %v, want %v", row.UndeclaredAPMAgents, want)
	}
	if _, ok := row.PerAgentStatus["gemini-cli"]; !ok {
		t.Fatalf("an agent APM deploys to must appear in the row: %#v", row.PerAgentStatus)
	}
}

func TestMcpServerRowsKeepsAnUnscopedServerFreeOfDivergence(t *testing.T) {
	cfg := apmUIConfig(config.McpServer{Name: "linear", Transport: "http", URL: "https://linear.example/mcp"})
	a, _, _ := newSyncApp(t, cfg, "name: t\nversion: 1.0.0\n", WithMcpAdapters([]McpAdapter{}))

	rows, _, err := a.McpServerRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if row := mcpRowByName(t, rows, "linear"); len(row.UndeclaredAPMAgents) != 0 {
		t.Fatalf("UndeclaredAPMAgents = %v, want none", row.UndeclaredAPMAgents)
	}
}

func TestMcpServerRowsReportsClaudeDriftAsAPMOwned(t *testing.T) {
	cfg := apmUIConfig(config.McpServer{Name: "shiplight", Transport: "stdio", Command: "bunx @shiplightai/mcp@1.0.0"})
	a, _, _ := newSyncApp(t, cfg, "name: t\nversion: 1.0.0\n", WithMcpAdapters([]McpAdapter{}))
	writeAPMLock(t, a, "mcp_target_servers:\n  claude:\n    - shiplight\n")
	writeClaudeState(t, a, "", `{"mcpServers":{"shiplight":{"command":"bunx","args":["@shiplightai/mcp@2.0.0"]}}}`)

	rows, _, err := a.McpServerRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	row := mcpRowByName(t, rows, "shiplight")
	if row.PerAgentStatus["claude-code"] != McpStatusDrifted || !row.Drifted {
		t.Fatalf("row = %#v, want claude-code drifted", row)
	}
	if !reflect.DeepEqual(row.DriftFields["claude-code"], []string{"command"}) {
		t.Fatalf("DriftFields = %v, want [command]", row.DriftFields)
	}
	advice := row.DriftAdvice("claude-code")
	if !strings.Contains(advice, "omni agents sync") || strings.Contains(advice, "mcp resolve") {
		t.Fatalf("advice = %q, want the sync reconcile and no resolve verb", advice)
	}
	if len(row.NativeDriftAgents()) != 0 {
		t.Fatalf("NativeDriftAgents = %v, want none: no omni verb rewrites an APM registration", row.NativeDriftAgents())
	}
}

func TestMcpServerRowsTreatsAMatchingClaudeEntryAsInstalled(t *testing.T) {
	cfg := apmUIConfig(config.McpServer{Name: "shiplight", Transport: "stdio", Command: "bunx @shiplightai/mcp@1.0.0"})
	a, _, _ := newSyncApp(t, cfg, "name: t\nversion: 1.0.0\n", WithMcpAdapters([]McpAdapter{}))
	writeClaudeState(t, a, "", `{"mcpServers":{"shiplight":{"command":"bunx","args":["@shiplightai/mcp@1.0.0"]}}}`)

	rows, _, err := a.McpServerRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if row := mcpRowByName(t, rows, "shiplight"); row.PerAgentStatus["claude-code"] != McpStatusInstalled || row.Drifted {
		t.Fatalf("row = %#v, want claude-code installed and undrifted", row)
	}
}

func TestMcpServerRowsIgnoresAnUnreadableClaudeConfig(t *testing.T) {
	cfg := apmUIConfig(config.McpServer{Name: "linear", Transport: "http", URL: "https://linear.example/mcp"})
	a, _, _ := newSyncApp(t, cfg, "name: t\nversion: 1.0.0\n", WithMcpAdapters([]McpAdapter{}))
	writeAPMLock(t, a, "mcp_target_servers:\n  claude:\n    - linear\n")
	writeClaudeState(t, a, "", "{not json")

	rows, unmanaged, err := a.McpServerRows(context.Background())
	if err != nil {
		t.Fatalf("a config claude rewrites constantly must never fail a listing: %v", err)
	}
	if row := mcpRowByName(t, rows, "linear"); row.PerAgentStatus["claude-code"] != McpStatusInstalled {
		t.Fatalf("row = %#v, want the lockfile record to stand in", row)
	}
	if len(unmanaged[claudeAgentID]) != 0 {
		t.Fatalf("unmanaged = %v, want none", unmanaged)
	}
}

func TestMcpServerRowsReadsClaudeStateFromTheConfigDirOverride(t *testing.T) {
	claudeDir := t.TempDir()
	cfg := apmUIConfig(config.McpServer{Name: "shiplight", Transport: "stdio", Command: "bunx @shiplightai/mcp@1.0.0"})
	a, _, _ := newSyncAppEnv(t, cfg, "name: t\nversion: 1.0.0\n",
		map[string]string{"CLAUDE_CONFIG_DIR": claudeDir}, WithMcpAdapters([]McpAdapter{}))
	writeClaudeState(t, a, claudeDir, `{"mcpServers":{"shiplight":{"command":"bunx","args":["@shiplightai/mcp@2.0.0"]}}}`)

	rows, _, err := a.McpServerRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if row := mcpRowByName(t, rows, "shiplight"); row.PerAgentStatus["claude-code"] != McpStatusDrifted {
		t.Fatalf("row = %#v, want the override path to be read", row)
	}
}

func TestMcpServerRowsPrefersTheLockedVersionOverTheManifestPin(t *testing.T) {
	cfg := apmUIConfig(
		config.McpServer{Name: "shiplight", Transport: "stdio", Command: "bunx @shiplightai/mcp@1.0.0"},
		config.McpServer{Name: "plain", Transport: "stdio", Command: "codebase-memory-mcp"},
	)
	a, _, _ := newSyncApp(t, cfg, "name: t\nversion: 1.0.0\n", WithMcpAdapters([]McpAdapter{}))
	writeAPMLock(t, a, "mcp_configs:\n  shiplight:\n    command: bunx\n    args: ['@shiplightai/mcp@3.1.4']\n")

	rows, _, err := a.McpServerRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := mcpRowByName(t, rows, "shiplight").Version; got != "3.1.4" {
		t.Fatalf("version = %q, want the deployed 3.1.4", got)
	}
	if got := mcpRowByName(t, rows, "plain").Version; got != "" {
		t.Fatalf("version = %q, want none", got)
	}
}

func TestMcpServerRowsListsUndeclaredClaudeServersAsUnmanaged(t *testing.T) {
	a, _, _ := newSyncApp(t, apmUIConfig(), "name: t\nversion: 1.0.0\n", WithMcpAdapters([]McpAdapter{}))
	writeClaudeState(t, a, "", `{"mcpServers":{"hand-added":{"command":"npx","args":["y"]}}}`)

	_, unmanaged, err := a.McpServerRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(unmanaged[claudeAgentID]) != 1 || unmanaged[claudeAgentID][0].Name != "hand-added" {
		t.Fatalf("unmanaged = %#v, want claude's hand-added server", unmanaged)
	}
	diff, err := a.ImportMcpServers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Unmanaged[claudeAgentID]) != 1 {
		t.Fatalf("import candidates = %#v, want claude's hand-added server", diff.Unmanaged)
	}
}

// The listing suppresses a server an installed plugin provides, so offering it for adoption would have omni
// declare a name it does not own and then fight the plugin for it on every sync.
func TestImportMcpServersAppliesThePluginShadowFilterToClaude(t *testing.T) {
	plugin := &shadowTestPluginAdapter{id: claudeAgentID, listedPlugins: []InstalledPlugin{{Name: "shadowed"}}}
	a, _, _ := newSyncApp(t, apmUIConfig(), "name: t\nversion: 1.0.0\n",
		WithMcpAdapters([]McpAdapter{}), WithPluginAdapters([]PluginAdapter{plugin}))
	writeClaudeState(t, a, "", `{"mcpServers":{"shadowed":{"command":"npx","args":["y"]},"hand-added":{"command":"npx","args":["y"]}}}`)

	diff, err := a.ImportMcpServers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(diff.Unmanaged[claudeAgentID]))
	for _, s := range diff.Unmanaged[claudeAgentID] {
		names = append(names, s.Name)
	}
	if !reflect.DeepEqual(names, []string{"hand-added"}) {
		t.Fatalf("import candidates = %v, want the plugin-provided server suppressed", names)
	}
}

func TestImportMcpServersIgnoresClaudeWhenItIsNotAnAPMTarget(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion, Settings: config.Settings{AgentsUse: []string{"codex"}}}
	a, _, _ := newSyncApp(t, cfg, "name: t\nversion: 1.0.0\n", WithMcpAdapters([]McpAdapter{}))
	writeClaudeState(t, a, "", `{"mcpServers":{"hand-added":{"command":"npx","args":["y"]}}}`)

	diff, err := a.ImportMcpServers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Unmanaged[claudeAgentID]) != 0 {
		t.Fatalf("import candidates = %#v, want claude's config left to its native owner", diff.Unmanaged)
	}
}

func TestResolveMcpDriftRefusesAnAPMOwnedAgent(t *testing.T) {
	cfg := apmUIConfig(config.McpServer{Name: "shiplight", Transport: "stdio", Command: "bunx @shiplightai/mcp@1.0.0"})
	a, _, _ := newSyncApp(t, cfg, "name: t\nversion: 1.0.0\n", WithMcpAdapters([]McpAdapter{}))
	writeClaudeState(t, a, "", `{"mcpServers":{"shiplight":{"command":"bunx","args":["@shiplightai/mcp@2.0.0"]}}}`)

	_, err := a.ResolveMcpDrift(context.Background(), ResolveMcpDriftOptions{
		Name: "shiplight", Agents: []string{"claude-code"}, Strategy: McpDriftUseManaged,
	})
	if err == nil || !strings.Contains(err.Error(), "omni agents sync") {
		t.Fatalf("err = %v, want a refusal pointing at sync", err)
	}
}

func TestResolveAllDriftLeavesAPMOwnedMcpDriftToSync(t *testing.T) {
	cfg := apmUIConfig(config.McpServer{Name: "shiplight", Transport: "stdio", Command: "bunx @shiplightai/mcp@1.0.0"})
	a, _, _ := newSyncApp(t, cfg, "name: t\nversion: 1.0.0\n", WithMcpAdapters([]McpAdapter{}))
	writeClaudeState(t, a, "", `{"mcpServers":{"shiplight":{"command":"bunx","args":["@shiplightai/mcp@2.0.0"]}}}`)

	res, err := a.ResolveAllDrift(context.Background(), ResolveAllDriftOptions{UseManaged: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.McpResolved != 0 || slices.ContainsFunc(res.Errors, func(e string) bool { return strings.Contains(e, "shiplight") }) {
		t.Fatalf("result = %#v, want no attempt on an APM-owned registration", res)
	}
	if !slices.ContainsFunc(res.Warnings, func(w string) bool { return strings.Contains(w, "omni agents sync") }) {
		t.Fatalf("warnings = %v, want the sync reconcile called out", res.Warnings)
	}
}
