package app

import (
	"context"
	"reflect"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

const authoringManifest = `name: test
version: 1.0.0
dependencies:
  apm:
    - someone/else
  mcp:
    - name: linear
      registry: false
      transport: http
      url: https://linear.example/mcp
`

func authoringConfig(agents []string, servers ...config.McpServer) *config.RootConfig {
	return &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: agents},
		Agents:   config.AgentsConfig{McpServers: servers},
	}
}

// APM has no removal verb, so undeclaring a server only reaches the deployed configs through a regenerated
// manifest and another scoped install.
func TestRemoveMcpServerPrunesTheAPMManifestAndReinstalls(t *testing.T) {
	cfg := authoringConfig([]string{"claude-code"},
		config.McpServer{Name: "linear", Transport: "http", URL: "https://linear.example/mcp"})
	a, mock, _ := newSyncApp(t, cfg, authoringManifest, WithMcpAdapters([]McpAdapter{}))

	if _, err := a.RemoveMcpServer(context.Background(), "linear"); err != nil {
		t.Fatal(err)
	}
	want := []string{"install", "-g", "--only", "mcp", "--target", "claude"}
	if calls := apmCalls(mock); len(calls) != 1 || !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("calls = %#v, want apm %v", calls, want)
	}
	if deps := syncManifestDeps(t, a, "mcp"); len(deps) != 0 {
		t.Fatalf("mcp deps = %#v, want the undeclared server pruned", deps)
	}
	if deps := syncManifestDeps(t, a, "apm"); !reflect.DeepEqual(deps, []any{"someone/else"}) {
		t.Fatalf("apm deps = %#v, want the package surface untouched", deps)
	}
}

func TestRemoveMcpServerLeavesAPMAloneForANativelyScopedServer(t *testing.T) {
	cfg := authoringConfig([]string{"claude-code", "codex"},
		config.McpServer{Name: "codexonly", Transport: "stdio", Command: "codex-mcp", Agents: []string{"codex"}})
	codex := &recordingMcpAdapter{id: "codex", listed: []InstalledMcpServer{{Name: "codexonly", Transport: "stdio", Command: "codex-mcp"}}}
	a, mock, _ := newSyncApp(t, cfg, authoringManifest, WithMcpAdapters([]McpAdapter{codex}))

	if _, err := a.RemoveMcpServer(context.Background(), "codexonly"); err != nil {
		t.Fatal(err)
	}
	if calls := apmCalls(mock); len(calls) != 0 {
		t.Fatalf("calls = %#v, want none: the native adapter owns this server", calls)
	}
	if !reflect.DeepEqual(codex.removed, []string{"codexonly"}) {
		t.Fatalf("native removals = %v, want the server unregistered natively", codex.removed)
	}
}

func TestAddMcpServerDeploysThroughTheAPMManifest(t *testing.T) {
	a, mock, _ := newSyncApp(t, authoringConfig([]string{"claude-code"}), "name: test\nversion: 1.0.0\n",
		WithMcpAdapters([]McpAdapter{}))

	if _, err := a.AddMcpServer(context.Background(), config.McpServer{
		Name: "docs", Transport: "http", URL: "https://docs.example/mcp",
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{"install", "-g", "--only", "mcp", "--target", "claude"}
	if calls := apmCalls(mock); len(calls) != 1 || !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("calls = %#v, want apm %v", calls, want)
	}
	deps := syncManifestDeps(t, a, "mcp")
	if len(deps) != 1 || deps[0].(map[string]any)["name"] != "docs" {
		t.Fatalf("mcp deps = %#v, want the declared server generated", deps)
	}
}

// Narrowing to an agent APM does not drive is the only scoping change global MCP can honour.
func TestSetMcpServerAgentsPrunesTheAPMManifestWhenNarrowedToANativeAgent(t *testing.T) {
	cfg := authoringConfig([]string{"claude-code", "codex"},
		config.McpServer{Name: "linear", Transport: "http", URL: "https://linear.example/mcp"})
	a, mock, _ := newSyncApp(t, cfg, authoringManifest, WithMcpAdapters([]McpAdapter{&recordingMcpAdapter{id: "codex"}}))

	if _, err := a.SetMcpServerAgents(context.Background(), "linear", []string{"codex"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"install", "-g", "--only", "mcp", "--target", "claude"}
	if calls := apmCalls(mock); len(calls) != 1 || !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("calls = %#v, want apm %v", calls, want)
	}
	if deps := syncManifestDeps(t, a, "mcp"); len(deps) != 0 {
		t.Fatalf("mcp deps = %#v, want the deselected server pruned from the APM surface", deps)
	}
}
