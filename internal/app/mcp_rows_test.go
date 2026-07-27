package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func TestMcpServerRows_ListErrorIsSurfaced(t *testing.T) {
	t.Parallel()
	listErr := errors.New("parse json: expected array, got null")
	adapter := &stubMcpAdapter{
		id:        "codex",
		available: true,
		listErr:   listErr,
	}
	srv := config.McpServer{Name: "linear", Transport: "stdio", Command: "npx x"}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}}, app.WithMcpAdapters([]app.McpAdapter{adapter}))
	rows, _, err := a.McpServerRows(t.Context())
	if err == nil || !errors.Is(err, listErr) || !strings.Contains(err.Error(), "list mcp servers for codex") {
		t.Fatalf("McpServerRows error = %v, want adapter parse error", err)
	}
	if rows != nil {
		t.Fatalf("McpServerRows returned misleading rows after adapter parse error: %+v", rows)
	}
}

func TestMcpServerRows_InstalledStatus(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{
		id:        "claude-code",
		available: true,
		listed:    []app.InstalledMcpServer{{Name: "linear", Transport: "stdio", Command: "npx x"}},
	}
	srv := config.McpServer{Name: "linear", Transport: "stdio", Command: "npx x", Agents: []string{"claude-code"}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	managed, _, err := a.McpServerRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(managed) != 1 {
		t.Fatalf("expected 1 managed row, got %d", len(managed))
	}
	if managed[0].PerAgentStatus["claude-code"] != app.McpStatusInstalled {
		t.Fatalf("expected installed, got %q", managed[0].PerAgentStatus["claude-code"])
	}
}

func TestMcpServerRows_ReflectsGroupMembership(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{id: "claude-code", available: true, listed: nil}
	srv := config.McpServer{Name: "linear", Transport: "stdio", Command: "npx x"}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	if err := a.CreateGroup("work"); err != nil {
		t.Fatal(err)
	}

	if err := a.SetMcpGroups(context.Background(), "linear", []string{"work"}); err != nil {
		t.Fatal(err)
	}

	managed, _, err := a.McpServerRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(managed) != 1 {
		t.Fatalf("expected 1 managed row, got %d", len(managed))
	}
	if len(managed[0].Groups) != 1 || managed[0].Groups[0] != "work" {
		t.Fatalf("McpServerRow.Groups = %v, want [work] after SetMcpGroups", managed[0].Groups)
	}
}

func TestMcpServerRows_MissingStatus(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{id: "claude-code", available: true, listed: nil}
	srv := config.McpServer{Name: "linear", Transport: "stdio", Command: "npx x"}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	managed, _, err := a.McpServerRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if managed[0].PerAgentStatus["claude-code"] != app.McpStatusMissing {
		t.Fatalf("expected missing, got %q", managed[0].PerAgentStatus["claude-code"])
	}
}

func TestMcpServerRows_UnavailableStatus(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{id: "claude-code", available: false}
	srv := config.McpServer{Name: "linear", Transport: "stdio", Command: "npx x"}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	managed, _, err := a.McpServerRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if managed[0].PerAgentStatus["claude-code"] != app.McpStatusAgentUnavailable {
		t.Fatalf("expected agent-unavailable, got %q", managed[0].PerAgentStatus["claude-code"])
	}
}

func TestMcpServerRows_PopulatesVersionFromCommand(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{
		id:        "claude-code",
		available: true,
		listed:    []app.InstalledMcpServer{{Name: "linear", Transport: "stdio", Command: "npx -y linear-mcp@1.2.3"}},
	}
	srv := config.McpServer{Name: "linear", Transport: "stdio", Command: "npx -y linear-mcp@1.2.3", Agents: []string{"claude-code"}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	managed, _, err := a.McpServerRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(managed) != 1 {
		t.Fatalf("expected 1 managed row, got %d", len(managed))
	}
	if managed[0].Version != "1.2.3" {
		t.Fatalf("Version = %q, want 1.2.3", managed[0].Version)
	}
}

func TestMcpServerRows_UnmanagedSection(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{
		id:        "claude-code",
		available: true,
		listed:    []app.InstalledMcpServer{{Name: "hand-added", Transport: "stdio", Command: "npx y"}},
	}
	a := newMcpTestApp(t, config.AgentsConfig{}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	managed, unmanaged, err := a.McpServerRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(managed) != 0 {
		t.Fatalf("expected 0 managed rows, got %d", len(managed))
	}
	if len(unmanaged["claude-code"]) != 1 || unmanaged["claude-code"][0].Name != "hand-added" {
		t.Fatalf("expected unmanaged hand-added, got %v", unmanaged)
	}
}

func TestMcpServerRows_UnmanagedSuppressedByInstalledPlugin(t *testing.T) {
	t.Parallel()
	mcpStub := &stubMcpAdapter{
		id:        "claude-code",
		available: true,
		listed:    []app.InstalledMcpServer{{Name: "context-mode", Transport: "stdio"}},
	}
	pluginStub := &stubPluginAdapter{
		id:            "claude-code",
		available:     true,
		listedPlugins: []app.InstalledPlugin{{Name: "context-mode", Marketplace: "some-marketplace"}},
	}
	a := newMcpTestApp(t, config.AgentsConfig{},
		app.WithMcpAdapters([]app.McpAdapter{mcpStub}),
		app.WithPluginAdapters([]app.PluginAdapter{pluginStub}),
	)
	managed, unmanaged, err := a.McpServerRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(managed) != 0 {
		t.Fatalf("expected 0 managed rows, got %d", len(managed))
	}
	if len(unmanaged["claude-code"]) != 0 {
		t.Fatalf("expected context-mode suppressed from unmanaged (plugin-provided), got %v", unmanaged)
	}
}

func TestMcpServerRows_ManifestEntryShadowedByPlugin_NotHidden(t *testing.T) {
	t.Parallel()
	mcpStub := &stubMcpAdapter{id: "claude-code", available: true, listed: nil}
	pluginStub := &stubPluginAdapter{
		id:            "claude-code",
		available:     true,
		listedPlugins: []app.InstalledPlugin{{Name: "context-mode", Marketplace: "some-marketplace"}},
	}
	srv := config.McpServer{Name: "context-mode", Transport: "stdio", Command: "npx x"}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}},
		app.WithMcpAdapters([]app.McpAdapter{mcpStub}),
		app.WithPluginAdapters([]app.PluginAdapter{pluginStub}),
	)
	managed, _, err := a.McpServerRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(managed) != 1 {
		t.Fatalf("expected 1 managed row (kept, not hidden), got %d", len(managed))
	}
	if !managed[0].ShadowedByPlugin {
		t.Fatalf("expected ShadowedByPlugin=true, got %+v", managed[0])
	}
	if managed[0].PerAgentStatus["claude-code"] != app.McpStatusShadowed {
		t.Fatalf("expected McpStatusShadowed, got %q", managed[0].PerAgentStatus["claude-code"])
	}
}
