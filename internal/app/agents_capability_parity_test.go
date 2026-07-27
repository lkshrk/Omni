package app_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func parityTestApp(t *testing.T, agents config.AgentsConfig, mcp []app.McpAdapter, plugins []app.PluginAdapter) *app.App {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_STATE_HOME", "")
	return newMcpTestApp(t, agents, app.WithMcpAdapters(mcp), app.WithPluginAdapters(plugins))
}

func hasItem(items []string, substr string) bool {
	for _, item := range items {
		if strings.Contains(item, substr) {
			return true
		}
	}
	return false
}

func TestAgentsSyncAll_ClaimsMcpAndPlugins(t *testing.T) {
	claudeMcp := &stubMcpAdapter{id: "claude-code", available: true, listed: []app.InstalledMcpServer{
		{Name: "adoptable", Transport: "stdio", Command: "npx -y adoptable"},
		{Name: "contested", Transport: "http", URL: "https://one.example.com"},
	}}
	codexMcp := &stubMcpAdapter{id: "codex", available: true, listed: []app.InstalledMcpServer{
		{Name: "contested", Transport: "http", URL: "https://two.example.com"},
	}}
	claudePlugins := &stubPluginAdapter{id: "claude-code", available: true, listedPlugins: []app.InstalledPlugin{
		{Name: "adoptable-plugin", Marketplace: "declared"},
		{Name: "orphan-plugin", Marketplace: "undeclared"},
	}}
	agents := config.AgentsConfig{Marketplaces: []config.Marketplace{{Name: "declared", Source: "o/declared"}}}
	a := parityTestApp(t, agents,
		[]app.McpAdapter{claudeMcp, codexMcp},
		[]app.PluginAdapter{claudePlugins})

	res, err := a.AgentsSyncAll(context.Background(), app.AgentsSyncAllOptions{ImportUnmanaged: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("Errors = %v, want the refusals reported as warnings instead", res.Errors)
	}
	if res.McpAdopted.Adopted != 1 || res.PluginsAdopted.Adopted != 1 {
		t.Fatalf("adopted mcp=%d plugins=%d, want 1/1", res.McpAdopted.Adopted, res.PluginsAdopted.Adopted)
	}
	if !hasItem(res.McpAdopted.Conflicts, "conflicting configuration") {
		t.Errorf("McpAdopted.Conflicts = %v, want the cross-agent mcp conflict line", res.McpAdopted.Conflicts)
	}
	if !hasItem(res.Warnings, "not declared") {
		t.Errorf("Warnings = %v, want the undeclared-marketplace skip line", res.Warnings)
	}

	cfg, err := a.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.McpServers) != 1 || cfg.Agents.McpServers[0].Name != "adoptable" {
		t.Errorf("manifest servers = %+v, want only the uncontested one", cfg.Agents.McpServers)
	}
	if len(cfg.Agents.Plugins) != 1 || cfg.Agents.Plugins[0].Name != "adoptable-plugin" {
		t.Errorf("manifest plugins = %+v, want only the declared-marketplace one", cfg.Agents.Plugins)
	}
	if !strings.HasPrefix(app.AgentsSyncAllSummaryText(res), "2 imported") {
		t.Errorf("summary = %q, want the claimed capabilities counted", app.AgentsSyncAllSummaryText(res))
	}
}

func TestAgentsSyncAll_ClaimLegOptIn(t *testing.T) {
	mcp := &stubMcpAdapter{id: "claude-code", available: true, listed: []app.InstalledMcpServer{
		{Name: "adoptable", Transport: "stdio", Command: "npx -y adoptable"},
	}}
	a := parityTestApp(t, config.AgentsConfig{}, []app.McpAdapter{mcp}, nil)

	res, err := a.AgentsSyncAll(context.Background(), app.AgentsSyncAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.McpAdopted.Adopted != 0 {
		t.Fatalf("adopted = %d, want 0 without ImportUnmanaged", res.McpAdopted.Adopted)
	}
	cfg, err := a.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.McpServers) != 0 {
		t.Fatalf("manifest servers = %+v, want none claimed", cfg.Agents.McpServers)
	}
}

func TestDashboardAgentsSummary_CountsMcpAndPluginAttention(t *testing.T) {
	outdated := true
	mcp := &stubMcpAdapter{id: "claude-code", available: true, listed: []app.InstalledMcpServer{
		{Name: "srv", Transport: "http", URL: "https://elsewhere.example.com"},
	}}
	plugins := &stubPluginAdapter{id: "claude-code", available: true, listedPlugins: []app.InstalledPlugin{
		{Name: "wrong-market", Marketplace: "other"},
		{Name: "stale", Marketplace: "declared", PathOutdated: &outdated},
	}}
	agents := config.AgentsConfig{
		McpServers: []config.McpServer{{Name: "srv", Transport: "http", URL: "https://mcp.example.com"}},
		Plugins: []config.Plugin{
			{Name: "wrong-market", Marketplace: "declared"},
			{Name: "stale", Marketplace: "declared"},
		},
		Marketplaces: []config.Marketplace{{Name: "declared", Source: "o/declared"}, {Name: "other", Source: "o/other"}},
	}
	a := parityTestApp(t, agents, []app.McpAdapter{mcp}, []app.PluginAdapter{plugins})

	cfg, err := a.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	summary, err := a.DashboardAgentsSummary(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if summary.McpDrifted != 1 || len(summary.McpDriftedNames) != 1 || summary.McpDriftedNames[0] != "srv" {
		t.Errorf("McpDrifted = %d %v, want 1 [srv]", summary.McpDrifted, summary.McpDriftedNames)
	}
	if summary.PluginsDrifted != 1 || summary.PluginsDriftedNames[0] != "wrong-market" {
		t.Errorf("PluginsDrifted = %d %v, want 1 [wrong-market]", summary.PluginsDrifted, summary.PluginsDriftedNames)
	}
	if summary.PluginsOutdated != 1 || summary.PluginsOutdatedNames[0] != "stale" {
		t.Errorf("PluginsOutdated = %d %v, want 1 [stale]", summary.PluginsOutdated, summary.PluginsOutdatedNames)
	}
	if summary.OutOfSync() != 2 {
		t.Errorf("OutOfSync = %d, want only the two drift states counted (outdated is installed, not out of sync)", summary.OutOfSync())
	}
}

func TestDoctorAgents_ReportsMcpAndPluginDrift(t *testing.T) {
	outdated := true
	mcp := &stubMcpAdapter{id: "claude-code", available: true, listed: []app.InstalledMcpServer{
		{Name: "srv", Transport: "http", URL: "https://elsewhere.example.com"},
		{Name: "stray", Transport: "stdio", Command: "npx -y stray"},
	}}
	plugins := &stubPluginAdapter{id: "claude-code", available: true, listedPlugins: []app.InstalledPlugin{
		{Name: "wrong-market", Marketplace: "other"},
		{Name: "stale", Marketplace: "declared", PathOutdated: &outdated},
		{Name: "stray-plugin", Marketplace: "other"},
	}}
	agents := config.AgentsConfig{
		McpServers: []config.McpServer{{Name: "srv", Transport: "http", URL: "https://mcp.example.com"}},
		Plugins: []config.Plugin{
			{Name: "wrong-market", Marketplace: "declared"},
			{Name: "stale", Marketplace: "declared"},
		},
		Marketplaces: []config.Marketplace{{Name: "declared", Source: "o/declared"}, {Name: "other", Source: "o/other"}},
	}
	a := newDoctorAgentsApp(t, agents,
		app.WithMcpAdapters([]app.McpAdapter{mcp}),
		app.WithPluginAdapters([]app.PluginAdapter{plugins}))

	result, err := a.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check := doctorCheck(result, "agents")
	if check.Status != app.DoctorStatusWarn {
		t.Errorf("status = %s, want warn for drifted resources", check.Status)
	}
	mcpGroup := doctorAgentsGroup(check, "mcp servers")
	if mcpGroup == nil {
		t.Fatal("missing mcp servers group")
	}
	if !hasItem(mcpGroup.Items, "srv: drifted on claude-code (url") {
		t.Errorf("mcp items = %v, want the drift item naming the field", mcpGroup.Items)
	}
	if !hasItem(mcpGroup.Items, "omni agents mcp resolve") {
		t.Errorf("mcp items = %v, want the resolve hint", mcpGroup.Items)
	}
	if !hasItem(mcpGroup.Items, "1 mcp server(s) not in manifest") {
		t.Errorf("mcp items = %v, want the unmanaged info line", mcpGroup.Items)
	}
	pluginGroup := doctorAgentsGroup(check, "plugins")
	if pluginGroup == nil {
		t.Fatal("missing plugins group")
	}
	if !hasItem(pluginGroup.Items, "wrong-market: drifted on claude-code (installed from other") {
		t.Errorf("plugin items = %v, want the drift item naming both marketplaces", pluginGroup.Items)
	}
	if !hasItem(pluginGroup.Items, "omni agents plugins resolve") {
		t.Errorf("plugin items = %v, want the resolve hint", pluginGroup.Items)
	}
	if !hasItem(pluginGroup.Items, "1 plugin(s) behind their marketplace") {
		t.Errorf("plugin items = %v, want the outdated info line", pluginGroup.Items)
	}
	if !hasItem(pluginGroup.Items, "1 plugin(s) not in manifest") {
		t.Errorf("plugin items = %v, want the unmanaged info line", pluginGroup.Items)
	}
}
