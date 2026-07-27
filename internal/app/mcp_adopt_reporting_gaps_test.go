package app

import (
	"context"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

type recordingMcpAdapter struct {
	id      string
	listed  []InstalledMcpServer
	added   []config.McpServer
	removed []string
}

func (s *recordingMcpAdapter) ID() string      { return s.id }
func (s *recordingMcpAdapter) Available() bool { return true }
func (s *recordingMcpAdapter) List(context.Context) ([]InstalledMcpServer, error) {
	return append([]InstalledMcpServer(nil), s.listed...), nil
}

func (s *recordingMcpAdapter) Add(_ context.Context, srv config.McpServer) error {
	s.added = append(s.added, srv)
	return nil
}

func (s *recordingMcpAdapter) Remove(_ context.Context, name string) error {
	s.removed = append(s.removed, name)
	return nil
}

// grok installs whatever transport it is handed but reported every url server back as http, so an sse
// server was permanently drifted — and the drift branch returns before headers are reconciled, which
// froze header convergence for that server too. Once the adapter round-trips sse, both resume.
func TestRestoreMcpServers_FaithfulSseReportConvergesHeaders(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	desired := config.McpServer{
		Name: "docs", Transport: "sse", URL: "https://docs.example.com/mcp",
		Headers: map[string]string{"X-Key": "${DOCS_TOKEN}"},
	}
	adapter := &recordingMcpAdapter{id: "grok", listed: []InstalledMcpServer{{
		Name: "docs", Transport: "sse", URL: "https://docs.example.com/mcp",
		Headers: map[string]string{"X-Key": "${OLD_TOKEN}"}, HeadersKnown: true,
	}}}
	a := newSkillsTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{desired}},
		WithMcpAdapters([]McpAdapter{adapter}))

	res, err := a.RestoreMcpServers(context.Background(), RestoreMcpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Drift) != 0 {
		t.Fatalf("Drift = %v, want a faithfully reported sse server not to drift", res.Drift)
	}
	if len(res.Updated) != 1 {
		t.Fatalf("Updated = %v, want the header change to converge", res.Updated)
	}
	if len(adapter.added) != 1 || adapter.added[0].Headers["X-Key"] != "${DOCS_TOKEN}" {
		t.Fatalf("added = %+v, want the manifest header pushed to the agent", adapter.added)
	}
}

// Adapters normalize a url server's flavour differently, so http from one CLI against sse from another
// is a disagreement between reporters, not two different servers. Drift against the manifest keeps the
// strict comparison, so only the adopt path is lenient and only this test pins that.
func TestMcpDefinitionsConflict_URLFlavourDisagreementIsNotAConflict(t *testing.T) {
	t.Parallel()
	http := InstalledMcpServer{Name: "docs", Transport: "http", URL: "https://docs.example.com/mcp"}
	sse := InstalledMcpServer{Name: "docs", Transport: "sse", URL: "https://docs.example.com/mcp"}
	if mcpDefinitionsConflict(http, sse) {
		t.Fatal("http against sse across two reporters must not block adoption")
	}
	stdio := InstalledMcpServer{Name: "docs", Transport: "stdio", Command: "npx -y docs"}
	if !mcpDefinitionsConflict(http, stdio) {
		t.Fatal("stdio against a url transport is a real disagreement")
	}
}

func TestAdoptUnmanagedMcpServers_ClaimsServerWhenAgentsDisagreeOnURLFlavour(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	claude := &listingMcpAdapter{id: "claude-code", listed: []InstalledMcpServer{{
		Name: "docs", Transport: "sse", URL: "https://docs.example.com/mcp", HeadersKnown: true,
	}}}
	grok := &listingMcpAdapter{id: "grok", listed: []InstalledMcpServer{{
		Name: "docs", Transport: "http", URL: "https://docs.example.com/mcp", HeadersKnown: true,
	}}}
	a := newSkillsTestApp(t, config.AgentsConfig{}, WithMcpAdapters([]McpAdapter{claude, grok}))

	res, err := a.AdoptUnmanagedMcpServers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 0 || res.Adopted != 1 {
		t.Fatalf("result = %+v, want the server claimed despite the flavour disagreement", res)
	}
}

func TestMcpIdentityDrift_StillReportsARealTransportChange(t *testing.T) {
	t.Parallel()
	desired := config.McpServer{Name: "x", Transport: "stdio", Command: "npx srv"}
	installed := InstalledMcpServer{Name: "x", Transport: "http", URL: "https://x.example.com"}
	if fields := mcpIdentityDrift(desired, installed); len(fields) == 0 {
		t.Fatal("stdio against http is a real divergence and must still report as drift")
	}
}

// codex never populates EnvLiteral, so comparing it against claude's declared the same server conflicting and left it unadoptable on both agents.
func TestMcpDefinitionsConflict_SurfaceOneAdapterCannotReportIsNotAConflict(t *testing.T) {
	t.Parallel()
	claude := InstalledMcpServer{
		Name: "github", Transport: "stdio", Command: "npx -y @modelcontextprotocol/server-github",
		EnvLiteral: map[string]string{"GITHUB_TOKEN": "ghp_abc"},
	}
	codex := InstalledMcpServer{
		Name: "github", Transport: "stdio", Command: "npx  -y  @modelcontextprotocol/server-github",
	}
	if mcpDefinitionsConflict(claude, codex) {
		t.Fatal("an unreported env map is missing information, not a competing definition")
	}

	grok := claude
	grok.EnvLiteral = map[string]string{"GITHUB_TOKEN": "ghp_different"}
	if !mcpDefinitionsConflict(claude, grok) {
		t.Fatal("two adapters that both report env and disagree is a real conflict")
	}

	headerA := InstalledMcpServer{Name: "h", Transport: "http", URL: "https://x", HeadersKnown: true,
		Headers: map[string]string{"X-Key": "${A}"}}
	headerB := InstalledMcpServer{Name: "h", Transport: "http", URL: "https://x"}
	if mcpDefinitionsConflict(headerA, headerB) {
		t.Fatal("headers an adapter cannot report must not read as a difference")
	}
	headerB.HeadersKnown = true
	if !mcpDefinitionsConflict(headerA, headerB) {
		t.Fatal("two adapters that both report headers and disagree is a real conflict")
	}
}

func TestAdoptUnmanagedMcpServers_ClaimsServerWhenOnlyOneAdapterReportsEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const ambient = "ambient-token-4b1e"
	claude := &listingMcpAdapter{id: "claude-code", listed: []InstalledMcpServer{{
		Name: "github", Transport: "stdio", Command: "npx -y server-github",
		EnvLiteral: map[string]string{"GITHUB_TOKEN": ambient},
	}}}
	codex := &listingMcpAdapter{id: "codex", listed: []InstalledMcpServer{{
		Name: "github", Transport: "stdio", Command: "npx -y server-github",
	}}}
	a := newSkillsTestApp(t, config.AgentsConfig{},
		WithMcpAdapters([]McpAdapter{claude, codex}),
		WithEnvLookup(func(name string) string {
			if name == "GITHUB_TOKEN" {
				return ambient
			}
			return ""
		}))

	res, err := a.AdoptUnmanagedMcpServers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 0 {
		t.Fatalf("Conflicts = %v, want no conflict from a surface codex cannot report", res.Conflicts)
	}
	if res.Adopted != 1 {
		t.Fatalf("result = %+v, want the server claimed once across both agents", res)
	}
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	adopted := findMcpServer(cfg.Agents.McpServers, "github")
	if adopted == nil || strings.Join(adopted.Agents, ",") != "claude-code,codex" {
		t.Fatalf("adopted = %+v, want both agents targeted", adopted)
	}
	if len(adopted.Env) != 1 || adopted.Env[0] != "GITHUB_TOKEN" {
		t.Fatalf("Env = %v, want the proven ambient variable recorded by name", adopted.Env)
	}
}
