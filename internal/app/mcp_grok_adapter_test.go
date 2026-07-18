package app

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestGrokMcpAdapter_ID(t *testing.T) {
	a := NewGrokMcpAdapter(nil, nil)
	if a.ID() != "grok" {
		t.Fatalf("got %q", a.ID())
	}
}

func TestGrokMcpAdapter_Add_Stdio(t *testing.T) {
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
	a := NewGrokMcpAdapter(exec, lookup)
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
	if gotCmd != "grok" {
		t.Fatalf("expected grok binary, got %q", gotCmd)
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

func TestGrokMcpAdapter_Add_Http(t *testing.T) {
	var gotArgs []string
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		gotArgs = args
		return "", "", nil
	}
	a := NewGrokMcpAdapter(exec, func(string) (string, bool) { return "", false })
	s := config.McpServer{
		Name:      "grafana",
		Transport: "http",
		URL:       "https://mcp.example.com/mcp",
		Headers:   map[string]string{"X-Api-Key": "abc"},
	}
	if err := a.Add(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"mcp", "add", "-s", "user", "--transport", "http", "grafana", "https://mcp.example.com/mcp",
		"--header", "X-Api-Key: abc",
	}
	if !mcpSliceEq(gotArgs, want) {
		t.Fatalf("args mismatch\ngot:  %v\nwant: %v", gotArgs, want)
	}
}

func TestGrokMcpAdapter_Add_Sse(t *testing.T) {
	var gotArgs []string
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		gotArgs = args
		return "", "", nil
	}
	a := NewGrokMcpAdapter(exec, func(string) (string, bool) { return "", false })
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
		"mcp", "add", "-s", "user", "--transport", "sse", "stream", "https://mcp.example.com/events",
		"--header", "X-Stream-Key: abc",
	}
	if !mcpSliceEq(gotArgs, want) {
		t.Fatalf("args mismatch\ngot:  %v\nwant: %v", gotArgs, want)
	}
}

func TestGrokMcpAdapter_Remove(t *testing.T) {
	var gotArgs []string
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		gotArgs = args
		return "", "", nil
	}
	a := NewGrokMcpAdapter(exec, nil)
	if err := a.Remove(context.Background(), "linear"); err != nil {
		t.Fatal(err)
	}
	want := []string{"mcp", "remove", "-s", "user", "linear"}
	if !mcpSliceEq(gotArgs, want) {
		t.Fatalf("args mismatch\ngot:  %v\nwant: %v", gotArgs, want)
	}
}

func TestParseGrokMcpList(t *testing.T) {
	out := `[
	  {"name":"stdio-srv","command":"npx","args":["-y","pkg"],"enabled":true,"scope":"user"},
	  {"name":"http-srv","url":"https://mcp.example.com/mcp","headers":{"X-Key":"value"},"enabled":true,"scope":"user"}
	]`
	got, err := parseGrokMcpList(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Transport != "stdio" || got[0].Command != "npx -y pkg" {
		t.Fatalf("stdio = %+v", got[0])
	}
	if got[1].Transport != "http" || got[1].URL != "https://mcp.example.com/mcp" {
		t.Fatalf("http = %+v", got[1])
	}
	if !got[1].HeadersKnown || got[1].Headers["X-Key"] != "value" {
		t.Fatalf("http headers = %+v, known=%v", got[1].Headers, got[1].HeadersKnown)
	}
}

func TestParseGrokMcpList_ReportsOmittedHeadersAsKnownEmpty(t *testing.T) {
	got, err := parseGrokMcpList(`[{
		"name":"http-srv","url":"https://mcp.example.com/mcp","enabled":true,"scope":"user"
	}]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].HeadersKnown || len(got[0].Headers) != 0 {
		t.Fatalf("server = %+v, want known empty headers", got)
	}
}
