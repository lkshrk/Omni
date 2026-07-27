package app_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lkshrk/omni/internal/agent"
	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

const codexSseListJSON = `[
  {
    "name": "docs",
    "transport": {
      "type": "sse",
      "url": "https://docs.example.com/sse",
      "http_headers": null,
      "env_http_headers": {"Authorization": "OLD_TOKEN"}
    }
  }
]`

type codexExecRecorder struct {
	mu    sync.Mutex
	calls [][]string
}

func (r *codexExecRecorder) run(_ context.Context, _ string, args ...string) (string, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, args)
	if len(args) >= 3 && args[0] == "mcp" && args[1] == "list" {
		return codexSseListJSON, "", nil
	}
	return "", "", nil
}

func (r *codexExecRecorder) argv() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	joined := make([]string, 0, len(r.calls))
	for _, c := range r.calls {
		joined = append(joined, strings.Join(c, " "))
	}
	return joined
}

// A codex-reported sse server must converge headers exactly like a reported http one: the flavour codex
// names is not evidence that headers went unreported, and treating it as such strands a rotated token.
func TestRestoreMcpServers_ConvergesHeadersForCodexReportedSseServer(t *testing.T) {
	binDir := t.TempDir()
	writeTestExecutable(t, binDir, "codex")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	codexHome := t.TempDir()
	configPath := filepath.Join(codexHome, "config.toml")
	if err := os.WriteFile(configPath, []byte("[mcp_servers.docs]\nurl = \"https://docs.example.com/sse\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := &codexExecRecorder{}
	adapter := agent.NewCodexMcpAdapter(rec.run, func(name string) (string, bool) {
		if name == "CODEX_HOME" {
			return codexHome, true
		}
		return "", false
	})

	srv := config.McpServer{
		Name: "docs", Transport: "sse", URL: "https://docs.example.com/sse",
		Headers: map[string]string{"Authorization": "${DOCS_TOKEN}"}, Agents: []string{"codex"},
	}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}},
		app.WithMcpAdapters([]app.McpAdapter{adapter}))

	res, err := a.RestoreMcpServers(context.Background(), app.RestoreMcpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Updated) != 1 || res.Updated[0] != "codex/docs" {
		t.Fatalf("Updated = %v (AlreadyInstalled=%v, Drift=%v), want [codex/docs]",
			res.Updated, res.AlreadyInstalled, res.Drift)
	}
	want := []string{
		"mcp list --json",
		"mcp remove docs",
		"mcp add docs --url https://docs.example.com/sse",
	}
	if got := rec.argv(); len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("codex calls = %v, want %v", got, want)
	}

	written, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), "\"Authorization\" = \"DOCS_TOKEN\"") {
		t.Fatalf("config.toml did not receive the manifest header:\n%s", written)
	}
	if strings.Contains(string(written), "OLD_TOKEN") {
		t.Fatalf("stale header survived convergence:\n%s", written)
	}
}
