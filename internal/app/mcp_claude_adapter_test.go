package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestClaudeCodeAdapter_ID(t *testing.T) {
	a := NewClaudeCodeMcpAdapter(nil, nil)
	if a.ID() != "claude-code" {
		t.Fatalf("got %q", a.ID())
	}
}

func TestClaudeCodeAdapter_Add_Stdio(t *testing.T) {
	var gotCmd string
	var gotArgs []string
	exec := func(_ context.Context, cmd string, args ...string) (string, string, error) {
		gotCmd = cmd
		gotArgs = args
		return "", "", nil
	}
	lookup := func(key string) (string, bool) {
		if key == "MY_KEY" {
			return "secret", true
		}
		return "", false
	}
	a := NewClaudeCodeMcpAdapter(exec, lookup)
	s := config.McpServer{
		Name:       "linear",
		Transport:  "stdio",
		Command:    "npx -y @linear/mcp",
		Env:        []string{"MY_KEY"},
		EnvLiteral: map[string]string{"LOG_LEVEL": "info"},
	}
	if err := a.Add(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if gotCmd != "claude" {
		t.Fatalf("expected claude binary, got %q", gotCmd)
	}
	if len(gotArgs) < 5 || gotArgs[0] != "mcp" || gotArgs[1] != "add" || gotArgs[2] != "-s" || gotArgs[3] != "user" || gotArgs[4] != "linear" {
		t.Fatalf("unexpected arg prefix: %v", gotArgs)
	}
	if !mcpContainsPair(gotArgs, "-e", "MY_KEY=secret") {
		t.Fatalf("missing -e MY_KEY=secret in args: %v", gotArgs)
	}
	if !mcpContainsPair(gotArgs, "-e", "LOG_LEVEL=info") {
		t.Fatalf("missing -e LOG_LEVEL=info in args: %v", gotArgs)
	}
	if !mcpHasSeparatorThenCmd(gotArgs, []string{"npx", "-y", "@linear/mcp"}) {
		t.Fatalf("missing '-- npx -y @linear/mcp' in args: %v", gotArgs)
	}
}

func TestClaudeCodeAdapter_Add_Http(t *testing.T) {
	var gotArgs []string
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		gotArgs = args
		return "", "", nil
	}
	a := NewClaudeCodeMcpAdapter(exec, func(string) (string, bool) { return "", false })
	s := config.McpServer{
		Name:      "grafana",
		Transport: "http",
		URL:       "https://mcp.example.com/sse",
		Headers: map[string]string{
			"Authorization": "Bearer token",
			"X-Api-Key":     "abc",
		},
	}
	if err := a.Add(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"mcp", "add", "-s", "user", "grafana", "--transport", "http", "https://mcp.example.com/sse",
		"--header", "Authorization: Bearer token", "--header", "X-Api-Key: abc",
	}
	if !mcpSliceEq(gotArgs, want) {
		t.Fatalf("args mismatch\ngot:  %v\nwant: %v", gotArgs, want)
	}
}

func TestClaudeCodeAdapter_Add_Http_WithEnv(t *testing.T) {
	var gotArgs []string
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		gotArgs = args
		return "", "", nil
	}
	lookup := func(key string) (string, bool) {
		if key == "HTTP_KEY" {
			return "secret", true
		}
		return "", false
	}
	a := NewClaudeCodeMcpAdapter(exec, lookup)
	s := config.McpServer{
		Name:      "grafana",
		Transport: "http",
		URL:       "https://mcp.example.com/sse",
		Env:       []string{"HTTP_KEY"},
	}
	if err := a.Add(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if !mcpContainsPair(gotArgs, "-e", "HTTP_KEY=secret") {
		t.Fatalf("missing -e HTTP_KEY=secret in args: %v", gotArgs)
	}
	// live-verified: `claude mcp add ... --transport http -e K=V <url>` fails
	// with "missing required argument 'commandOrUrl'". Env flags must come
	// after the url positional.
	urlIdx, envIdx := -1, -1
	for i, a := range gotArgs {
		if a == "https://mcp.example.com/sse" {
			urlIdx = i
		}
		if a == "-e" && envIdx < 0 {
			envIdx = i
		}
	}
	if urlIdx < 0 || envIdx < 0 || urlIdx > envIdx {
		t.Fatalf("expected url before -e flags in args: %v", gotArgs)
	}
}

func TestClaudeCodeAdapter_Add_Http_MissingEnv(t *testing.T) {
	called := false
	exec := func(_ context.Context, _ string, _ ...string) (string, string, error) {
		called = true
		return "", "", nil
	}
	a := NewClaudeCodeMcpAdapter(exec, func(string) (string, bool) { return "", false })
	s := config.McpServer{Name: "grafana", Transport: "http", URL: "https://mcp.example.com/sse", Env: []string{"MISSING_HTTP_KEY"}}
	err := a.Add(context.Background(), s)
	if err == nil {
		t.Fatal("expected error for missing env var on http transport")
	}
	if called {
		t.Fatal("exec must not be called when env var is missing")
	}
}

func TestClaudeCodeAdapter_Add_Sse(t *testing.T) {
	var gotArgs []string
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		gotArgs = args
		return "", "", nil
	}
	a := NewClaudeCodeMcpAdapter(exec, func(string) (string, bool) { return "", false })
	s := config.McpServer{
		Name:      "stream",
		Transport: "sse",
		URL:       "https://mcp.example.com/events",
		Headers:   map[string]string{"X-Stream-Key": "abc"},
	}
	if err := a.Add(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"mcp", "add", "-s", "user", "stream", "--transport", "sse", "https://mcp.example.com/events",
		"--header", "X-Stream-Key: abc",
	}
	if !mcpSliceEq(gotArgs, want) {
		t.Fatalf("args mismatch\ngot:  %v\nwant: %v", gotArgs, want)
	}
}

func TestClaudeCodeAdapter_Add_MissingEnv(t *testing.T) {
	called := false
	exec := func(_ context.Context, _ string, _ ...string) (string, string, error) {
		called = true
		return "", "", nil
	}
	a := NewClaudeCodeMcpAdapter(exec, func(string) (string, bool) { return "", false })
	s := config.McpServer{Name: "x", Transport: "stdio", Command: "npx x", Env: []string{"MISSING_VAR"}}
	err := a.Add(context.Background(), s)
	if err == nil {
		t.Fatal("expected error for missing env var")
	}
	if called {
		t.Fatal("exec must not be called when env var is missing")
	}
}

func TestClaudeCodeAdapter_Remove(t *testing.T) {
	var gotArgs []string
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		gotArgs = args
		return "", "", nil
	}
	a := NewClaudeCodeMcpAdapter(exec, func(string) (string, bool) { return "", false })
	if err := a.Remove(context.Background(), "linear"); err != nil {
		t.Fatal(err)
	}
	want := []string{"mcp", "remove", "-s", "user", "linear"}
	if !mcpSliceEq(gotArgs, want) {
		t.Fatalf("args mismatch\ngot: %v\nwant: %v", gotArgs, want)
	}
}

// claudeConfigFixture mirrors the confirmed real-machine shape of
// ~/.claude.json's top-level mcpServers object: a stdio entry with
// command/args/env, an http entry, and an sse entry. Plugin-provided servers
// never appear here -- they live in plugin manifests, not this file -- so
// the "plugin:" exclusion the old text parser needed is unnecessary now.
const claudeConfigFixture = `{"mcpServers":{
	"smoketest": {"command": "npx", "args": ["-y", "@example/mcp"], "env": {"FOO": "bar"}},
	"smokehttp": {"type": "http", "url": "https://example.com/mcp"},
	"smokesse": {"type": "sse", "url": "https://example.com/sse"}
}}`

func TestClaudeCodeAdapter_List_ReadsConfigFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(claudeConfigFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)
	execCalled := false
	exec := func(_ context.Context, _ string, _ ...string) (string, string, error) {
		execCalled = true
		return "", "", nil
	}
	a := NewClaudeCodeMcpAdapter(exec, func(string) (string, bool) { return "", false })
	servers, err := a.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if execCalled {
		t.Fatal("List must not shell out to the claude CLI")
	}
	if len(servers) != 3 {
		t.Fatalf("expected 3 servers, got %d: %v", len(servers), servers)
	}
	if servers[0].Name != "smokehttp" || servers[0].Transport != "http" || servers[0].URL != "https://example.com/mcp" {
		t.Fatalf("unexpected first server: %+v", servers[0])
	}
	if servers[1].Name != "smokesse" || servers[1].Transport != "sse" || servers[1].URL != "https://example.com/sse" {
		t.Fatalf("unexpected second server: %+v", servers[1])
	}
	if servers[2].Name != "smoketest" || servers[2].Transport != "stdio" || servers[2].Command != "npx -y @example/mcp" {
		t.Fatalf("unexpected third server: %+v", servers[2])
	}
}

func TestClaudeCodeAdapter_List_MissingConfigFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := NewClaudeCodeMcpAdapter(nil, nil)
	servers, err := a.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if servers != nil {
		t.Fatalf("expected nil servers, got %v", servers)
	}
}

func TestParseClaudeConfigMcpServers_PopulatesVersion(t *testing.T) {
	data := []byte(`{"mcpServers":{"pinned": {"command": "npx", "args": ["-y", "@example/mcp@1.2.3"]}}}`)
	servers, err := parseClaudeConfigMcpServers(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 {
		t.Fatalf("expected 1 server, got %d: %+v", len(servers), servers)
	}
	if servers[0].Version != "1.2.3" {
		t.Fatalf("Version = %q, want 1.2.3", servers[0].Version)
	}
}

func TestExtractMcpPinnedVersion(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    string
	}{
		{"pinned npx", "npx -y @example/mcp@1.2.3", "1.2.3"},
		{"pinned bunx", "bunx some-pkg@2.0.0", "2.0.0"},
		{"scoped package not confused with scope prefix", "npx -y @scope/pkg@2.0.0", "2.0.0"},
		{"latest tag", "npx -y @example/mcp@latest", ""},
		{"next tag", "npx -y @example/mcp@next", ""},
		{"bare path no npx/bunx", "/usr/local/bin/my-mcp-server", ""},
		{"empty command", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractMcpPinnedVersion(tc.command); got != tc.want {
				t.Errorf("extractMcpPinnedVersion(%q) = %q, want %q", tc.command, got, tc.want)
			}
		})
	}
}

func mcpSliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func mcpContainsPair(args []string, flag, value string) bool {
	for i, a := range args {
		if a == flag && i+1 < len(args) && args[i+1] == value {
			return true
		}
	}
	return false
}

func mcpHasSeparatorThenCmd(args, cmdParts []string) bool {
	for i, a := range args {
		if a == "--" {
			return mcpSliceEq(args[i+1:], cmdParts)
		}
	}
	return false
}
