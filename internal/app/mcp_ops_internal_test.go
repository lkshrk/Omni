package app

import (
	"context"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

type listingMcpAdapter struct {
	id     string
	listed []InstalledMcpServer
}

func (s *listingMcpAdapter) ID() string      { return s.id }
func (s *listingMcpAdapter) Available() bool { return true }
func (s *listingMcpAdapter) List(context.Context) ([]InstalledMcpServer, error) {
	return append([]InstalledMcpServer(nil), s.listed...), nil
}
func (s *listingMcpAdapter) Add(context.Context, config.McpServer) error { return nil }
func (s *listingMcpAdapter) Remove(context.Context, string) error        { return nil }

func adoptTestApp(t *testing.T) *App {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	mcp := &listingMcpAdapter{id: "claude-code", listed: []InstalledMcpServer{
		{
			Name: "secret-remote", Transport: "http", URL: "https://secret.example.com",
			Headers: map[string]string{"Authorization": "Bearer sk-live-1"}, HeadersKnown: true,
		},
		{
			Name: "env-remote", Transport: "http", URL: "https://env.example.com",
			Headers:      map[string]string{"X-Key": "${REMOTE_KEY}"},
			EnvLiteral:   map[string]string{"MODE": "prod"},
			HeadersKnown: true,
		},
	}}
	plugins := &shadowTestPluginAdapter{id: "claude-code", listedPlugins: []InstalledPlugin{
		{Name: "helper", Marketplace: "declared"},
	}}
	return newSkillsTestApp(t,
		config.AgentsConfig{Marketplaces: []config.Marketplace{{Name: "declared", Source: "o/declared"}}},
		WithMcpAdapters([]McpAdapter{mcp}),
		WithPluginAdapters([]PluginAdapter{plugins}),
		WithEnvLookup(func(name string) string {
			if name == "MODE" {
				return "prod"
			}
			return ""
		}))
}

// Adoption is omni copying what another tool configured, so a header literal the user never typed is
// refused rather than written with advice. The reference-bearing sibling is unaffected: one refused
// server must not take the rest of the pass down with it.
func TestAdoptUnmanagedMcpServers_RefusesLiteralHeaderValues(t *testing.T) {
	a := adoptTestApp(t)

	res, err := a.AdoptUnmanagedMcpServers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Adopted != 1 || len(res.Skipped) != 1 {
		t.Fatalf("result = %+v, want the literal header refused and the reference-bearing server claimed", res)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("Warnings = %v, want no adopt path to produce an advisory", res.Warnings)
	}
	if !strings.Contains(res.Skipped[0], "secret-remote") || !strings.Contains(res.Skipped[0], "Authorization") {
		t.Fatalf("Skipped = %v, want the refusal naming the server and the resolved header", res.Skipped)
	}
	if strings.Contains(res.Skipped[0], "sk-live-1") {
		t.Fatalf("Skipped = %v, want the refusal to name the header without quoting the value", res.Skipped)
	}
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if findMcpServer(cfg.Agents.McpServers, "secret-remote") != nil {
		t.Fatalf("manifest = %+v, want nothing written for the refused server", cfg.Agents.McpServers)
	}
}

func TestAdoptUnmanagedMcpServers_RecordsProvenEnvAsPassthroughName(t *testing.T) {
	a := adoptTestApp(t)

	if _, err := a.AdoptUnmanagedMcpServers(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	adopted := findMcpServer(cfg.Agents.McpServers, "env-remote")
	if adopted == nil {
		t.Fatalf("manifest = %+v, want the env-referencing server claimed", cfg.Agents.McpServers)
	}
	if len(adopted.Env) != 1 || adopted.Env[0] != "MODE" {
		t.Fatalf("Env = %v, want the ambient variable recorded by name", adopted.Env)
	}
	if len(adopted.EnvLiteral) != 0 {
		t.Fatalf("EnvLiteral = %v, want no value carried into the manifest", adopted.EnvLiteral)
	}
}

func TestAdoptUnmanaged_DryRunPreviewsWithoutWriting(t *testing.T) {
	a := adoptTestApp(t)
	ctx := context.Background()

	mcpRes, err := a.adoptUnmanagedMcpServers(ctx, mcpAdoptOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if mcpRes.Adopted != 0 || len(mcpRes.WouldAdopt) != 1 {
		t.Fatalf("mcp preview = %+v, want the claimable server previewed and nothing claimed", mcpRes)
	}
	if !strings.Contains(mcpRes.WouldAdopt[0], "env-remote") {
		t.Fatalf("mcp preview = %+v, want the claimable server named", mcpRes)
	}
	// A preview that promised a claim the real path refuses would be a licence to commit the secret.
	if len(mcpRes.Skipped) != 1 || !strings.Contains(mcpRes.Skipped[0], "secret-remote") {
		t.Fatalf("mcp preview = %+v, want the literal-header server refused on the dry path too", mcpRes)
	}
	pluginRes, err := a.adoptUnmanagedPlugins(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if pluginRes.Adopted != 0 || len(pluginRes.WouldAdopt) != 1 || !strings.Contains(pluginRes.WouldAdopt[0], "helper") {
		t.Fatalf("plugin preview = %+v, want the claimable plugin previewed and nothing claimed", pluginRes)
	}

	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.McpServers) != 0 || len(cfg.Agents.Plugins) != 0 {
		t.Fatalf("manifest = %+v, want a dry run to leave it untouched", cfg.Agents)
	}
}

func TestAdoptUnmanagedMcpServers_FiltersPluginShadowPerAgent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	server := InstalledMcpServer{
		Name: "shared", Transport: "http", URL: "https://example.com/mcp", HeadersKnown: true,
	}
	a := newSkillsTestApp(t, config.AgentsConfig{},
		WithMcpAdapters([]McpAdapter{
			&listingMcpAdapter{id: "claude-code", listed: []InstalledMcpServer{server}},
			&listingMcpAdapter{id: "codex", listed: []InstalledMcpServer{server}},
		}),
		WithPluginAdapters([]PluginAdapter{
			&shadowTestPluginAdapter{id: "claude-code", listedPlugins: []InstalledPlugin{{Name: "shared"}}},
			&shadowTestPluginAdapter{id: "codex"},
		}),
	)

	res, err := a.adoptUnmanagedMcpServers(context.Background(), mcpAdoptOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.WouldAdopt) != 1 || !strings.Contains(res.WouldAdopt[0], "on codex") {
		t.Fatalf("WouldAdopt = %v, want shared adopted only for unshadowed codex", res.WouldAdopt)
	}
	if strings.Contains(res.WouldAdopt[0], "claude-code") {
		t.Fatalf("WouldAdopt = %v, want plugin-shadowed claude-code filtered only", res.WouldAdopt)
	}
}

func resolveNames(cfg *config.RootConfig, group string) []string {
	servers := resolveMcpServers(cfg, group)
	names := make([]string, len(servers))
	for i, s := range servers {
		names[i] = s.Name
	}
	return names
}

func containsName(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

func TestResolveMcpServers_UngroupedServer_AppearsOnAllHosts(t *testing.T) {
	t.Parallel()
	cfg := &config.RootConfig{
		Agents: config.AgentsConfig{
			McpServers: []config.McpServer{
				{Name: "global", Transport: "http", URL: "https://example.com"},
			},
		},
	}
	for _, group := range []string{"box", "work", ""} {
		got := resolveNames(cfg, group)
		if !containsName(got, "global") {
			t.Errorf("group=%q: ungrouped server must appear; got %v", group, got)
		}
	}
}

func TestResolveMcpServers_GroupedServer_MatchingGroup_Included(t *testing.T) {
	t.Parallel()
	cfg := &config.RootConfig{
		Agents: config.AgentsConfig{
			McpServers: []config.McpServer{
				{Name: "work-only", Transport: "http", URL: "https://work.example.com"},
			},
		},
		Groups: []*config.GroupConfig{
			{Name: "work", McpServers: []string{"work-only"}},
		},
	}
	got := resolveNames(cfg, "work")
	if !containsName(got, "work-only") {
		t.Fatalf("grouped server must appear when its group is active; got %v", got)
	}
}

func TestResolveMcpServers_GroupedServer_NonMatchingGroup_Excluded(t *testing.T) {
	t.Parallel()
	cfg := &config.RootConfig{
		Agents: config.AgentsConfig{
			McpServers: []config.McpServer{
				{Name: "work-only", Transport: "http", URL: "https://work.example.com"},
			},
		},
		Groups: []*config.GroupConfig{
			{Name: "work", McpServers: []string{"work-only"}},
		},
	}
	got := resolveNames(cfg, "personal")
	if containsName(got, "work-only") {
		t.Fatalf("grouped server must not appear when its group is not active; got %v", got)
	}
}

func TestResolveMcpServers_HostAssignedGroup_Included(t *testing.T) {
	t.Parallel()
	cfg := &config.RootConfig{
		Agents: config.AgentsConfig{
			McpServers: []config.McpServer{
				{Name: "litellm-tools", Transport: "http", URL: "https://litellm.example.com"},
			},
		},
		Groups: []*config.GroupConfig{
			{Name: "ai", McpServers: []string{"litellm-tools"}},
		},
		Hosts: map[string][]string{
			"topaz": {"ai"},
		},
	}
	got := resolveNames(cfg, "topaz")
	if !containsName(got, "litellm-tools") {
		t.Fatalf("server in a group assigned to the host via cfg.Hosts must appear; got %v", got)
	}
}

func TestResolveMcpServers_ServerInMultipleGroups_AppearsOncePerActiveGroup(t *testing.T) {
	t.Parallel()
	cfg := &config.RootConfig{
		Agents: config.AgentsConfig{
			McpServers: []config.McpServer{
				{Name: "shared", Transport: "http", URL: "https://example.com"},
			},
		},
		Groups: []*config.GroupConfig{
			{Name: "work", McpServers: []string{"shared"}},
			{Name: "home", McpServers: []string{"shared"}},
		},
	}
	got := resolveNames(cfg, "work")
	var count int
	for _, n := range got {
		if n == "shared" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("shared server must appear exactly once; got %d in %v", count, got)
	}
	got2 := resolveNames(cfg, "other")
	if containsName(got2, "shared") {
		t.Fatalf("shared server must not appear when neither group is active; got %v", got2)
	}
}
