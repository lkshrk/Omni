package app

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestCodexAdapter_ID(t *testing.T) {
	a := NewCodexMcpAdapter(nil, nil)
	if a.ID() != "codex" {
		t.Fatalf("got %q", a.ID())
	}
}

func TestCodexAdapter_Add_Stdio(t *testing.T) {
	var gotArgs []string
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		gotArgs = args
		return "", "", nil
	}
	lookup := func(key string) (string, bool) {
		if key == "API" {
			return "tok", true
		}
		return "", false
	}
	a := NewCodexMcpAdapter(exec, lookup)
	s := config.McpServer{
		Name:      "linear",
		Transport: "stdio",
		Command:   "npx -y foo",
		Env:       []string{"API"},
	}
	if err := a.Add(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if len(gotArgs) < 2 || gotArgs[0] != "mcp" || gotArgs[1] != "add" {
		t.Fatalf("unexpected start: %v", gotArgs)
	}
	if !mcpContainsPair(gotArgs, "--env", "API=tok") {
		t.Fatalf("missing --env API=tok in args: %v", gotArgs)
	}
	if !mcpHasSeparatorThenCmd(gotArgs, []string{"npx", "-y", "foo"}) {
		t.Fatalf("missing '-- npx -y foo' in args: %v", gotArgs)
	}
	sepIdx := -1
	for i, a := range gotArgs {
		if a == "--" {
			sepIdx = i
			break
		}
	}
	nameFound := false
	for _, a := range gotArgs[:sepIdx] {
		if a == "linear" {
			nameFound = true
			break
		}
	}
	if !nameFound {
		t.Fatalf("name 'linear' not found before '--' in args: %v", gotArgs)
	}
}

func TestCodexAdapter_Add_Http(t *testing.T) {
	var gotArgs []string
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		gotArgs = args
		return "", "", nil
	}
	a := NewCodexMcpAdapter(exec, func(string) (string, bool) { return "", false })
	s := config.McpServer{Name: "grafana", Transport: "http", URL: "https://mcp.example.com"}
	if err := a.Add(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	want := []string{"mcp", "add", "grafana", "--url", "https://mcp.example.com"}
	if !mcpSliceEq(gotArgs, want) {
		t.Fatalf("args mismatch\ngot: %v\nwant: %v", gotArgs, want)
	}
}

// TestCodexAdapter_Add_Http_RejectsEnv pins live-verified codex behavior:
// `codex mcp add --env FOO=bar smokehttp2 --url https://example.com/mcp`
// exits 1 with "Error: command is required" -- codex's own --help documents
// --env as "Only valid with stdio servers". omni must refuse before exec
// rather than silently dropping the env vars.
func TestCodexAdapter_Add_Http_RejectsEnv(t *testing.T) {
	called := false
	exec := func(_ context.Context, _ string, _ ...string) (string, string, error) {
		called = true
		return "", "", nil
	}
	lookup := func(key string) (string, bool) {
		if key == "HTTP_KEY" {
			return "secret", true
		}
		return "", false
	}
	a := NewCodexMcpAdapter(exec, lookup)
	s := config.McpServer{
		Name:      "grafana",
		Transport: "http",
		URL:       "https://mcp.example.com",
		Env:       []string{"HTTP_KEY"},
	}
	err := a.Add(context.Background(), s)
	if err == nil {
		t.Fatal("expected error: codex does not support env for http/sse servers")
	}
	if called {
		t.Fatal("exec must not be called when env is set for http/sse transport")
	}
}

func TestCodexAdapter_Add_Sse_RejectsEnvLiteral(t *testing.T) {
	called := false
	exec := func(_ context.Context, _ string, _ ...string) (string, string, error) {
		called = true
		return "", "", nil
	}
	a := NewCodexMcpAdapter(exec, func(string) (string, bool) { return "", false })
	s := config.McpServer{
		Name:       "stream",
		Transport:  "sse",
		URL:        "https://mcp.example.com/events",
		EnvLiteral: map[string]string{"LOG_LEVEL": "info"},
	}
	err := a.Add(context.Background(), s)
	if err == nil {
		t.Fatal("expected error: codex does not support env for http/sse servers")
	}
	if called {
		t.Fatal("exec must not be called when env_literal is set for http/sse transport")
	}
}

func TestCodexAdapter_Add_Http_MissingEnv(t *testing.T) {
	called := false
	exec := func(_ context.Context, _ string, _ ...string) (string, string, error) {
		called = true
		return "", "", nil
	}
	a := NewCodexMcpAdapter(exec, func(string) (string, bool) { return "", false })
	s := config.McpServer{Name: "grafana", Transport: "http", URL: "https://mcp.example.com", Env: []string{"MISSING_HTTP_KEY"}}
	err := a.Add(context.Background(), s)
	if err == nil {
		t.Fatal("expected error for missing env var on http transport")
	}
	if called {
		t.Fatal("exec must not be called when env var is missing")
	}
}

func TestCodexAdapter_Add_MissingEnv(t *testing.T) {
	a := NewCodexMcpAdapter(
		func(_ context.Context, _ string, _ ...string) (string, string, error) { return "", "", nil },
		func(string) (string, bool) { return "", false },
	)
	s := config.McpServer{Name: "x", Transport: "stdio", Command: "npx x", Env: []string{"NOT_SET"}}
	if err := a.Add(context.Background(), s); err == nil {
		t.Fatal("expected error for missing env var")
	}
}

// codexMcpListJSONFixture is the exact output of a live, sandboxed
// `codex mcp list --json` run (CODEX_HOME pointed at a scratch dir) after
// `codex mcp add smokehttp --url https://example.com/mcp` and
// `codex mcp add t -- echo hi`.
const codexMcpListJSONFixture = `[
  {
    "name": "smokehttp",
    "enabled": true,
    "disabled_reason": null,
    "transport": {
      "type": "streamable_http",
      "url": "https://example.com/mcp",
      "bearer_token_env_var": null,
      "http_headers": null,
      "env_http_headers": null
    },
    "startup_timeout_sec": null,
    "tool_timeout_sec": null,
    "auth_status": "unsupported"
  },
  {
    "name": "t",
    "enabled": true,
    "disabled_reason": null,
    "transport": {
      "type": "stdio",
      "command": "echo",
      "args": ["hi"],
      "env": null,
      "env_vars": [],
      "cwd": null
    },
    "startup_timeout_sec": null,
    "tool_timeout_sec": null,
    "auth_status": "unsupported"
  }
]`

func TestCodexAdapter_List_ParsesJSON(t *testing.T) {
	var gotArgs []string
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		gotArgs = args
		return codexMcpListJSONFixture, "", nil
	}
	a := NewCodexMcpAdapter(exec, func(string) (string, bool) { return "", false })
	servers, err := a.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !mcpSliceEq(gotArgs, []string{"mcp", "list", "--json"}) {
		t.Fatalf("expected `mcp list --json`, got %v", gotArgs)
	}
	if len(servers) != 2 {
		t.Fatalf("expected 2 servers, got %d: %v", len(servers), servers)
	}
	if servers[0].Name != "smokehttp" || servers[0].Transport != "http" || servers[0].URL != "https://example.com/mcp" {
		t.Fatalf("unexpected first server: %+v", servers[0])
	}
	if servers[1].Name != "t" || servers[1].Transport != "stdio" || servers[1].Command != "echo hi" {
		t.Fatalf("unexpected second server: %+v", servers[1])
	}
}

func TestCodexAdapter_Remove(t *testing.T) {
	var gotArgs []string
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		gotArgs = args
		return "", "", nil
	}
	a := NewCodexMcpAdapter(exec, func(string) (string, bool) { return "", false })
	if err := a.Remove(context.Background(), "linear"); err != nil {
		t.Fatal(err)
	}
	want := []string{"mcp", "remove", "linear"}
	if !mcpSliceEq(gotArgs, want) {
		t.Fatalf("args mismatch\ngot: %v\nwant: %v", gotArgs, want)
	}
}
