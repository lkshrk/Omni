package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rogpeppe/go-internal/lockedfile"

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
	codexHome := t.TempDir()
	var gotArgs []string
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		gotArgs = args
		return "", "", nil
	}
	a := NewCodexMcpAdapter(exec, func(name string) (string, bool) { return codexHome, name == "CODEX_HOME" })
	s := config.McpServer{Name: "grafana", Transport: "http", URL: "https://mcp.example.com"}
	if err := a.Add(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	want := []string{"mcp", "add", "grafana", "--url", "https://mcp.example.com"}
	if !mcpSliceEq(gotArgs, want) {
		t.Fatalf("args mismatch\ngot: %v\nwant: %v", gotArgs, want)
	}
}

func TestCodexAdapter_Add_Http_WritesHeadersToConfig(t *testing.T) {
	codexHome := t.TempDir()
	configPath := filepath.Join(codexHome, "config.toml")
	var gotArgs []string
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		gotArgs = args
		if err := os.WriteFile(configPath, []byte("[mcp_servers.grafana]\nurl = \"https://mcp.example.com\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return "", "", nil
	}
	lookup := func(name string) (string, bool) {
		if name == "CODEX_HOME" {
			return codexHome, true
		}
		return "", false
	}
	a := NewCodexMcpAdapter(exec, lookup)
	err := a.Add(context.Background(), config.McpServer{
		Name:      "grafana",
		Transport: "http",
		URL:       "https://mcp.example.com",
		Headers: map[string]string{
			"X-Api-Key": "${GRAFANA_API_KEY}",
			"X-Literal": "a value",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"mcp", "add", "grafana", "--url", "https://mcp.example.com"}
	if !mcpSliceEq(gotArgs, wantArgs) {
		t.Fatalf("args mismatch\ngot: %v\nwant: %v", gotArgs, wantArgs)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{
		`[mcp_servers."grafana".http_headers]`,
		`"X-Literal" = "a value"`,
		`[mcp_servers."grafana".env_http_headers]`,
		`"X-Api-Key" = "GRAFANA_API_KEY"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("config missing %q:\n%s", want, got)
		}
	}
}

func TestCodexAdapter_Add_Sse_WritesHeadersToConfig(t *testing.T) {
	codexHome := t.TempDir()
	configPath := filepath.Join(codexHome, "config.toml")
	var gotArgs []string
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		gotArgs = args
		if err := os.WriteFile(configPath, []byte("[mcp_servers.stream]\nurl = \"https://mcp.example.com/events\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return "", "", nil
	}
	a := NewCodexMcpAdapter(exec, func(name string) (string, bool) {
		return codexHome, name == "CODEX_HOME"
	})
	err := a.Add(context.Background(), config.McpServer{
		Name:      "stream",
		Transport: "sse",
		URL:       "https://mcp.example.com/events",
		Headers:   map[string]string{"X-Stream-Key": "abc"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !mcpSliceEq(gotArgs, []string{"mcp", "add", "stream", "--url", "https://mcp.example.com/events"}) {
		t.Fatalf("args mismatch: %v", gotArgs)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"X-Stream-Key" = "abc"`) {
		t.Fatalf("config missing SSE header:\n%s", data)
	}
}

func TestCodexAdapter_Add_Http_RollsBackWhenHeadersCannotBeWritten(t *testing.T) {
	codexHome := t.TempDir()
	var calls [][]string
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		calls = append(calls, append([]string(nil), args...))
		return "", "", nil
	}
	lookup := func(name string) (string, bool) {
		if name == "CODEX_HOME" {
			return codexHome, true
		}
		return "", false
	}
	a := NewCodexMcpAdapter(exec, lookup)
	err := a.Add(context.Background(), config.McpServer{
		Name:      "grafana",
		Transport: "http",
		URL:       "https://mcp.example.com",
		Headers:   map[string]string{"X-Api-Key": "abc"},
	})
	if err == nil {
		t.Fatal("expected config write error")
	}
	if len(calls) != 2 || !mcpSliceEq(calls[1], []string{"mcp", "remove", "grafana"}) {
		t.Fatalf("calls = %v, want add followed by rollback remove", calls)
	}
}

func TestCodexAdapter_Add_Http_ReportsRollbackFailure(t *testing.T) {
	codexHome := t.TempDir()
	var calls [][]string
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		calls = append(calls, append([]string(nil), args...))
		if len(calls) == 2 {
			return "", "remove stderr", errors.New("remove failed")
		}
		return "", "", nil
	}
	a := NewCodexMcpAdapter(exec, func(name string) (string, bool) {
		return codexHome, name == "CODEX_HOME"
	})
	err := a.Add(context.Background(), config.McpServer{
		Name:      "grafana",
		Transport: "http",
		URL:       "https://mcp.example.com",
		Headers:   map[string]string{"X-Api-Key": "abc"},
	})
	if err == nil || !strings.Contains(err.Error(), "rollback failed: remove failed: remove stderr") {
		t.Fatalf("error = %v, want header write and rollback failure", err)
	}
	if len(calls) != 2 || !mcpSliceEq(calls[0], []string{"mcp", "add", "grafana", "--url", "https://mcp.example.com"}) ||
		!mcpSliceEq(calls[1], []string{"mcp", "remove", "grafana"}) {
		t.Fatalf("calls = %v, want add followed by rollback remove", calls)
	}
}

func TestCodexAdapter_Add_Http_PreservesConfigSymlinkAndMode(t *testing.T) {
	codexHome := t.TempDir()
	target := filepath.Join(t.TempDir(), "config.toml")
	const original = "[mcp_servers.grafana]\nurl = \"https://mcp.example.com\"\n"
	if err := os.WriteFile(target, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(codexHome, "config.toml")
	if err := os.Symlink(target, configPath); err != nil {
		t.Fatal(err)
	}
	a := NewCodexMcpAdapter(
		func(_ context.Context, _ string, _ ...string) (string, string, error) { return "", "", nil },
		func(name string) (string, bool) { return codexHome, name == "CODEX_HOME" },
	)
	if err := a.Add(context.Background(), config.McpServer{
		Name:      "grafana",
		Transport: "http",
		URL:       "https://mcp.example.com",
		Headers:   map[string]string{"X-Api-Key": "abc"},
	}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(configPath); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("config symlink was not preserved: info=%v err=%v", info, err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("config mode = %o, want 640", got)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"X-Api-Key" = "abc"`) {
		t.Fatalf("target missing header:\n%s", data)
	}
}

func TestCodexAdapter_Add_Http_UsesDefaultCodexHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	codexHome := filepath.Join(home, ".codex")
	if err := os.Mkdir(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(codexHome, "config.toml")
	if err := os.WriteFile(configPath, []byte("[mcp_servers.grafana]\nurl = \"https://mcp.example.com\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := NewCodexMcpAdapter(
		func(_ context.Context, _ string, _ ...string) (string, string, error) { return "", "", nil },
		func(string) (string, bool) { return "", false },
	)
	if err := a.Add(context.Background(), config.McpServer{
		Name:      "grafana",
		Transport: "http",
		URL:       "https://mcp.example.com",
		Headers:   map[string]string{"X-Api-Key": "abc"},
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"X-Api-Key" = "abc"`) {
		t.Fatalf("default config missing header:\n%s", data)
	}
}

func TestExactEnvReference(t *testing.T) {
	tests := []struct {
		value string
		want  string
		ok    bool
	}{
		{value: "${API_KEY}", want: "API_KEY", ok: true},
		{value: "${_API2}", want: "_API2", ok: true},
		{value: "prefix-${API_KEY}", ok: false},
		{value: "${2API}", ok: false},
		{value: "${API-KEY}", ok: false},
		{value: "${}", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			got, ok := exactEnvReference(tt.value)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("exactEnvReference(%q) = %q, %v; want %q, %v", tt.value, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestRenderCodexHeaders_ReplacesExistingTables(t *testing.T) {
	input := []byte(`[mcp_servers.grafana]
url = "https://mcp.example.com"

[mcp_servers.grafana.http_headers]
"X-Old" = "old"

[mcp_servers."grafana".env_http_headers]
"X-Old-Env" = "OLD_ENV"

[features]
apps = true
`)
	got := string(renderCodexHeaders(input, "grafana", map[string]string{
		"X-New":     "new",
		"X-New-Env": "${NEW_ENV}",
	}))
	for _, old := range []string{"X-Old", "X-Old-Env"} {
		if strings.Contains(got, old) {
			t.Fatalf("stale header %q remains:\n%s", old, got)
		}
	}
	for _, want := range []string{`"X-New" = "new"`, `"X-New-Env" = "NEW_ENV"`, "[features]\napps = true"} {
		if !strings.Contains(got, want) {
			t.Fatalf("config missing %q:\n%s", want, got)
		}
	}
	if count := strings.Count(got, `[mcp_servers."grafana".http_headers]`); count != 1 {
		t.Fatalf("http_headers table count = %d, want 1:\n%s", count, got)
	}
}

func TestReplaceFileAtomically_RejectsStaleSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("newer\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	written, err := replaceFileAtomically(path, []byte("old\n"), []byte("ours\n"))
	if err != nil {
		t.Fatal(err)
	}
	if written {
		t.Fatal("stale snapshot must not be written")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "newer\n" {
		t.Fatalf("config = %q, want concurrent contents preserved", got)
	}
}

func TestCodexAdapter_WriteHeaders_SerializesConcurrentWriters(t *testing.T) {
	codexHome := t.TempDir()
	path := filepath.Join(codexHome, "config.toml")
	if err := os.WriteFile(path, []byte("model = \"test\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	adapter := &codexMcpAdapter{lookupEnv: func(key string) (string, bool) {
		if key == "CODEX_HOME" {
			return codexHome, true
		}
		return "", false
	}}

	unlock, err := lockedfile.MutexAt(path + ".omni.lock").Lock()
	if err != nil {
		t.Fatal(err)
	}
	blocked := make(chan error, 1)
	go func() { blocked <- adapter.writeHeaders("blocked", map[string]string{"X-Key": "blocked"}) }()
	select {
	case err := <-blocked:
		unlock()
		t.Fatalf("writer bypassed config lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	unlock()
	if err := <-blocked; err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, name := range []string{"one", "two"} {
		name := name
		go func() {
			<-start
			errs <- adapter.writeHeaders(name, map[string]string{"X-Key": name})
		}()
	}
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`[mcp_servers."one".http_headers]`,
		`[mcp_servers."two".http_headers]`,
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("concurrent config missing %q:\n%s", want, data)
		}
	}
}

func TestCodexAdapter_Add_Http_SerializesCompleteTransactions(t *testing.T) {
	codexHome := t.TempDir()
	path := filepath.Join(codexHome, "config.toml")
	if err := os.WriteFile(path, []byte("model = \"test\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var active atomic.Int32
	var overlapped atomic.Bool
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		if len(args) < 5 || args[0] != "mcp" || args[1] != "add" {
			return "", "", nil
		}
		if active.Add(1) != 1 {
			overlapped.Store(true)
		}
		defer active.Add(-1)
		current, err := os.ReadFile(path)
		if err != nil {
			return "", "", err
		}
		time.Sleep(50 * time.Millisecond)
		block := fmt.Sprintf("\n[mcp_servers.%s]\nurl = %q\n", args[2], args[4])
		return "", "", os.WriteFile(path, append(current, block...), 0o600)
	}
	lookup := func(name string) (string, bool) { return codexHome, name == "CODEX_HOME" }
	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, name := range []string{"one", "two"} {
		name := name
		go func() {
			<-start
			adapter := NewCodexMcpAdapter(exec, lookup)
			errs <- adapter.Add(context.Background(), config.McpServer{
				Name: name, Transport: "http", URL: "https://" + name + ".example.com/mcp",
				Headers: map[string]string{"X-Key": name},
			})
		}()
	}
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if overlapped.Load() {
		t.Fatal("concurrent Codex add commands bypassed the config lock")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`url = "https://one.example.com/mcp"`,
		`url = "https://two.example.com/mcp"`,
		`[mcp_servers."one".http_headers]`,
		`[mcp_servers."two".http_headers]`,
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("serialized config missing %q:\n%s", want, data)
		}
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
      "http_headers": {"X-Literal": "literal-value"},
      "env_http_headers": {"X-API-Key": "API_KEY"}
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
	if servers[0].Headers["X-Literal"] != "literal-value" || servers[0].Headers["X-API-Key"] != "${API_KEY}" {
		t.Fatalf("unexpected first server headers: %+v", servers[0].Headers)
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
