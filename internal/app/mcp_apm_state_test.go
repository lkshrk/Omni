package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func apmRowsConfig() *config.RootConfig {
	return &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"claude-code"}},
		Agents: config.AgentsConfig{McpServers: []config.McpServer{
			{Name: "linear", Transport: "http", URL: "https://linear.example/mcp"},
		}},
	}
}

func writeAPMLock(t *testing.T, a *App, body string) {
	t.Helper()
	manifestPath, err := a.globalAPMManifestPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(manifestPath), "apm.lock.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMcpServerRowsReportsAPMDeployedServersFromTheLockfile(t *testing.T) {
	a, _, _ := newSyncApp(t, apmRowsConfig(), "name: t\nversion: 1.0.0\n", WithMcpAdapters([]McpAdapter{}))
	writeAPMLock(t, a, "mcp_target_servers:\n  claude:\n    - linear\n")

	rows, _, err := a.McpServerRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].PerAgentStatus["claude-code"] != McpStatusInstalled {
		t.Fatalf("rows = %#v, want linear installed on claude-code", rows)
	}
	if rows[0].Drifted {
		t.Fatal("APM presence records cannot prove drift")
	}
}

func TestMcpServerRowsReportsAPMServerMissingWithoutALockfileRecord(t *testing.T) {
	a, _, _ := newSyncApp(t, apmRowsConfig(), "name: t\nversion: 1.0.0\n", WithMcpAdapters([]McpAdapter{}))
	writeAPMLock(t, a, "mcp_target_servers:\n  codex:\n    - linear\n")

	rows, _, err := a.McpServerRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].PerAgentStatus["claude-code"] != McpStatusMissing {
		t.Fatalf("rows = %#v, want linear missing on claude-code", rows)
	}
}

func TestMcpServerRowsReportsAMalformedLockfile(t *testing.T) {
	a, _, _ := newSyncApp(t, apmRowsConfig(), "name: t\nversion: 1.0.0\n", WithMcpAdapters([]McpAdapter{}))
	writeAPMLock(t, a, "mcp_target_servers: [unclosed\n")

	if _, _, err := a.McpServerRows(context.Background()); err == nil {
		t.Fatal("expected an error for a malformed lockfile")
	}
}

func TestRestoreMcpServersWarnsThatAPMAgentsNeedTheSyncVerb(t *testing.T) {
	a, _, _ := newSyncApp(t, apmRowsConfig(), "name: t\nversion: 1.0.0\n", WithMcpAdapters([]McpAdapter{}))

	res, err := a.RestoreMcpServers(context.Background(), RestoreMcpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(res.Warnings, func(w string) bool { return strings.Contains(w, `run "omni agents sync"`) }) {
		t.Fatalf("warnings = %v, want the APM-driven agents called out", res.Warnings)
	}
}

func TestRestoreMcpServersStaysQuietWhenSyncNarrowsToNativeAgents(t *testing.T) {
	a, _, _ := newSyncApp(t, apmRowsConfig(), "name: t\nversion: 1.0.0\n", WithMcpAdapters([]McpAdapter{}))

	res, err := a.RestoreMcpServers(context.Background(), RestoreMcpOptions{OnlyAgents: []string{"codex"}})
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(res.Warnings, func(w string) bool { return strings.Contains(w, "omni agents sync") }) {
		t.Fatalf("warnings = %v, want no advice on the path sync already drives", res.Warnings)
	}
}
