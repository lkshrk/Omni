package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const codexFixture = `[agents]
model = "gpt"

[mcp_servers.codebase-memory-mcp]
command = "codebase-memory-mcp"

[mcp_servers.codebase-memory-mcp.env]
TOKEN = "x"

[mcp_servers.codebase-memory-mcp.tools.query_graph]
enabled = true

[mcp_servers.litellm-tools]
url = "https://api.invalid/mcp/"

[mcp_servers.litellm-tools.http_headers]
x-key = "y"

  [mcp_servers.padded]
command = "padded"

[mcp_servers.commented]  # inline note
command = "commented"

[mcp_servers."quoted.name"]
command = "quoted"

[mcp_servers.disabled]
enabled = false

[plugins."context-mode@context-mode"]
enabled = true
`

func writeHarness(t *testing.T, home, rel, content string) {
	t.Helper()
	writeFile(t, filepath.Join(home, rel), content)
}

func TestReadHarnessDeploymentsWithoutFilesIsEmpty(t *testing.T) {
	got := readHarnessDeployments(t.TempDir())
	if len(got.MCP) != 0 || len(got.LSP) != 0 || len(got.MCPConfigs) != 0 || len(got.Notices) != 0 {
		t.Fatalf("got %#v", got)
	}
}

func TestReadHarnessDeploymentsReadsClaudeSurfaces(t *testing.T) {
	home := t.TempDir()
	writeHarness(t, home, ".claude.json", `{
  "numStartups": 42,
  "mcpServers": {
    "litellm-tools": {"url": "https://api.invalid/mcp/"},
    "context-mode": {"command": "node", "args": ["./start.mjs"]}
  },
  "lspServers": {"gopls": {"command": "gopls"}}
}`)
	got := readHarnessDeployments(home)
	if len(got.Notices) != 0 {
		t.Fatalf("notices = %v", got.Notices)
	}
	if len(got.MCP) != 2 || len(got.MCP["litellm-tools"]) != 1 || got.MCP["litellm-tools"][0] != harnessClaude {
		t.Fatalf("mcp = %#v", got.MCP)
	}
	if len(got.LSP["gopls"]) != 1 || got.LSP["gopls"][0] != harnessClaude {
		t.Fatalf("lsp = %#v", got.LSP)
	}
	cfg := got.MCPConfigs["context-mode"]
	if cfg.Command != "node" || len(cfg.Args) != 1 || cfg.Args[0] != "./start.mjs" {
		t.Fatalf("config = %+v", cfg)
	}
	if got.MCPConfigs["litellm-tools"].URL != "https://api.invalid/mcp/" {
		t.Fatalf("url config = %+v", got.MCPConfigs["litellm-tools"])
	}
}

func TestReadHarnessDeploymentsParsesCodexTableHeadersOnly(t *testing.T) {
	home := t.TempDir()
	writeHarness(t, home, ".codex/config.toml", codexFixture)
	got := readHarnessDeployments(home)
	if len(got.Notices) != 0 {
		t.Fatalf("notices = %v", got.Notices)
	}
	want := []string{"codebase-memory-mcp", "litellm-tools", "padded", "commented", "quoted.name", "disabled"}
	if len(got.MCP) != len(want) {
		t.Fatalf("mcp names = %#v", got.MCP)
	}
	for _, name := range want {
		if len(got.MCP[name]) != 1 || got.MCP[name][0] != harnessCodex {
			t.Fatalf("missing codex entry %q in %#v", name, got.MCP)
		}
	}
	for name := range got.MCP {
		if strings.HasSuffix(name, ".env") || strings.HasSuffix(name, ".http_headers") || strings.Contains(name, ".tools.") {
			t.Fatalf("nested table parsed as a server: %q", name)
		}
	}
}

func TestReadHarnessDeploymentsMergesLabelsAcrossFiles(t *testing.T) {
	home := t.TempDir()
	writeHarness(t, home, ".claude.json", `{"mcpServers": {"litellm-tools": {"url": "https://api.invalid/mcp/"}}}`)
	writeHarness(t, home, ".codex/config.toml", "[mcp_servers.litellm-tools]\nurl = \"https://api.invalid/mcp/\"\n")
	got := readHarnessDeployments(home)
	labels := got.MCP["litellm-tools"]
	if len(labels) != 2 || labels[0] != harnessClaude || labels[1] != harnessCodex {
		t.Fatalf("labels = %v", labels)
	}
}

func TestReadHarnessDeploymentsDegradesToNotices(t *testing.T) {
	home := t.TempDir()
	writeHarness(t, home, ".claude.json", "{not json")
	got := readHarnessDeployments(home)
	if len(got.MCP) != 0 || len(got.Notices) != 1 || !strings.Contains(got.Notices[0], "could not be parsed") {
		t.Fatalf("got %#v", got)
	}
}

func TestReadHarnessDeploymentsSkipsOversizedClaudeConfig(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(path, make([]byte, harnessClaudeMaxKiB+1), 0o600); err != nil {
		t.Fatal(err)
	}
	got := readHarnessDeployments(home)
	if len(got.MCP) != 0 || len(got.Notices) != 1 || !strings.Contains(got.Notices[0], "exceeds 4MiB") {
		t.Fatalf("got %#v", got)
	}
}

func TestReadHarnessDeploymentsNoticesUnreadableFile(t *testing.T) {
	home := t.TempDir()
	writeHarness(t, home, ".claude.json", "{}")
	if err := os.Chmod(filepath.Join(home, ".claude.json"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(home, ".claude.json"), 0o600) })
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions")
	}
	got := readHarnessDeployments(home)
	if len(got.Notices) != 1 || !strings.Contains(got.Notices[0], "unreadable") {
		t.Fatalf("notices = %v", got.Notices)
	}
}
