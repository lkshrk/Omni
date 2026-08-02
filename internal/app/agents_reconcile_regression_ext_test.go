package app_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider/script"
)

func TestMcpServerRows_UntargetedAgentIsNotDrift(t *testing.T) {
	t.Parallel()
	claude := &stubMcpAdapter{id: "claude-code", available: true, listed: []app.InstalledMcpServer{
		{Name: "ctx7", Transport: "stdio", Command: "npx -y ctx7@1"},
	}}
	codex := &stubMcpAdapter{id: "codex", available: true, listed: []app.InstalledMcpServer{
		{Name: "ctx7", Transport: "stdio", Command: "npx -y stale"},
	}}
	srv := config.McpServer{Name: "ctx7", Transport: "stdio", Command: "npx -y ctx7@1", Agents: []string{"claude-code"}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}},
		app.WithMcpAdapters([]app.McpAdapter{claude, codex}))

	rows, _, err := a.McpServerRows(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one managed row", rows)
	}
	if rows[0].Drifted {
		t.Errorf("Drifted = true; \"mcp resolve\" cannot clear drift on an agent the entry does not target")
	}
	if _, ok := rows[0].PerAgentStatus["codex"]; ok {
		t.Errorf("PerAgentStatus = %v, want no verdict for an untargeted agent", rows[0].PerAgentStatus)
	}
}

func TestPluginRows_UntargetedAgentIsNotDrift(t *testing.T) {
	t.Parallel()
	claude := &stubPluginAdapter{id: "claude-code", available: true, listedPlugins: []app.InstalledPlugin{
		{Name: "helper", Marketplace: "declared"},
	}}
	codex := &stubPluginAdapter{id: "codex", available: true, listedPlugins: []app.InstalledPlugin{
		{Name: "helper", Marketplace: "stale"},
	}}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "declared", Source: "o/declared"}},
		Plugins:      []config.Plugin{{Name: "helper", Marketplace: "declared", Agents: []string{"claude-code"}}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{claude, codex}))

	rows, _, err := a.PluginRows(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one managed row", rows)
	}
	if rows[0].Drifted {
		t.Errorf("Drifted = true; \"plugins resolve\" cannot clear drift on an agent the entry does not target")
	}
}

func TestPluginRows_DeclaredMarketplaceWinsOverListingOrder(t *testing.T) {
	t.Parallel()
	claude := &stubPluginAdapter{id: "claude-code", available: true, listedPlugins: []app.InstalledPlugin{
		{Name: "superpowers", Marketplace: "obra"},
		{Name: "superpowers", Marketplace: "fork"},
	}}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "obra", Source: "obra/superpowers"}},
		Plugins:      []config.Plugin{{Name: "superpowers", Marketplace: "obra"}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{claude}))

	rows, _, err := a.PluginRows(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Drifted {
		t.Fatalf("Drifted = true; the declared identity is installed, whichever copy the adapter listed last")
	}
}

func TestRestorePlugins_DeclaredMarketplaceWinsOverListingOrder(t *testing.T) {
	t.Parallel()
	claude := &stubPluginAdapter{id: "claude-code", available: true, listedPlugins: []app.InstalledPlugin{
		{Name: "superpowers", Marketplace: "obra"},
		{Name: "superpowers", Marketplace: "fork"},
	}}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "obra", Source: "obra/superpowers"}},
		Plugins:      []config.Plugin{{Name: "superpowers", Marketplace: "obra"}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{claude}))

	res, err := a.RestorePlugins(t.Context(), app.RestorePluginOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Drift) != 0 {
		t.Fatalf("Drift = %v, want none: the declared identity is installed", res.Drift)
	}
	if len(res.AlreadyInstalled) != 1 {
		t.Fatalf("AlreadyInstalled = %v, want the declared copy recognised", res.AlreadyInstalled)
	}
}

func TestSkillPackageRows_UnparseableSourceDegradesToOneRow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := newMcpTestApp(t, config.AgentsConfig{Packages: []config.SkillPackage{
		{Source: "git::https://x/y"},
		{Source: "owner/good"},
	}}, app.WithMcpAdapters([]app.McpAdapter{}), app.WithPluginAdapters([]app.PluginAdapter{}))

	rows, err := a.SkillPackageRows(t.Context())
	if err != nil {
		t.Fatalf("SkillPackageRows: %v, want per-row degradation", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want the healthy row rendered alongside the bad one", rows)
	}
	var errored, healthy int
	for _, r := range rows {
		if r.Error != "" {
			errored++
		} else {
			healthy++
		}
	}
	if errored != 1 || healthy != 1 {
		t.Fatalf("errored=%d healthy=%d, want one of each", errored, healthy)
	}
}

func TestAddSkillPackage_RefusesPluginShadowedPackage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	writeFakeClaudeOnPath(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	plugins := &stubPluginAdapter{id: "claude-code", available: true, listedPlugins: []app.InstalledPlugin{
		{Name: "academic-research-skills", Marketplace: "declared"},
	}}
	a := newMcpTestApp(t, config.AgentsConfig{},
		app.WithPluginAdapters([]app.PluginAdapter{plugins}),
		app.WithMcpAdapters([]app.McpAdapter{}))

	_, _, err := a.AddSkillPackage(t.Context(), "owner/academic-research-skills")
	if err == nil || !strings.Contains(err.Error(), "plugin") {
		t.Fatalf("err = %v, want the same refusal the import path gives", err)
	}
}

func TestAgentsSyncAll_McpAdoptRefusalsTravelOnOneChannel(t *testing.T) {
	claude := &stubMcpAdapter{id: "claude-code", available: true, listed: []app.InstalledMcpServer{
		{Name: "contested", Transport: "http", URL: "https://one.example.com"},
	}}
	codex := &stubMcpAdapter{id: "codex", available: true, listed: []app.InstalledMcpServer{
		{Name: "contested", Transport: "http", URL: "https://two.example.com"},
	}}
	a := parityTestApp(t, config.AgentsConfig{}, []app.McpAdapter{claude, codex}, nil)

	res, err := a.AgentsSyncAll(t.Context(), app.AgentsSyncAllOptions{ImportUnmanaged: true})
	if err != nil {
		t.Fatal(err)
	}
	if !hasItem(res.McpAdopted.Conflicts, "conflicting configuration") {
		t.Fatalf("McpAdopted.Conflicts = %v, want the refusal", res.McpAdopted.Conflicts)
	}
	if hasItem(res.Warnings, "conflicting configuration") {
		t.Errorf("Warnings = %v, want the adopt refusal on McpAdopted alone", res.Warnings)
	}
}

func TestInstall_UnpinnedGitHubRecipeSurfacesAConfigWriteFailure(t *testing.T) {
	t.Parallel()
	mock := executor.NewMatchMock().WithFallback(executor.MockCall{})
	a, cfgPath := newImportApp(t, script.New(mock))
	a.CacheDir = t.TempDir()
	a.SetGitHubFallbackAPIForTest("https://api.github.test", actionlintLatestReleaseClientWithBinaryVersion(t, nil, "v1.7.12", ""))
	if err := saveAppConfig(t, cfgPath, unpinnedActionlintConfig()); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	dir := filepath.Dir(cfgPath)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	err := a.Install(t.Context(), "actionlint", "script")
	if err == nil || !strings.Contains(err.Error(), "recording installed version") {
		t.Fatalf("Install err = %v, want the config write failure surfaced", err)
	}
}
