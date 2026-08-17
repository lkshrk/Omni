package app_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func TestMcpServerRows_IdentityDriftPerField(t *testing.T) {
	t.Parallel()
	manifest := config.McpServer{
		Name: "srv", Transport: "http", URL: "https://mcp.example.com",
		Agents: []string{"codex"},
	}
	stdio := config.McpServer{
		Name: "srv", Transport: "stdio", Command: "npx -y srv",
		Agents: []string{"codex"},
	}
	tests := []struct {
		name     string
		declared config.McpServer
		live     app.InstalledMcpServer
		want     app.McpStatus
		field    string
	}{
		{
			name:     "matching http registration",
			declared: manifest,
			live:     app.InstalledMcpServer{Name: "srv", Transport: "http", URL: "https://mcp.example.com"},
			want:     app.McpStatusInstalled,
		},
		{
			name:     "transport differs",
			declared: manifest,
			live:     app.InstalledMcpServer{Name: "srv", Transport: "sse", URL: "https://mcp.example.com"},
			want:     app.McpStatusDrifted,
			field:    "transport",
		},
		{
			name:     "url differs",
			declared: manifest,
			live:     app.InstalledMcpServer{Name: "srv", Transport: "http", URL: "https://elsewhere.example.com"},
			want:     app.McpStatusDrifted,
			field:    "url",
		},
		{
			name:     "command differs",
			declared: stdio,
			live:     app.InstalledMcpServer{Name: "srv", Transport: "stdio", Command: "npx -y other"},
			want:     app.McpStatusDrifted,
			field:    "command",
		},
		{
			name:     "command whitespace only",
			declared: stdio,
			live:     app.InstalledMcpServer{Name: "srv", Transport: "stdio", Command: "npx  -y   srv"},
			want:     app.McpStatusInstalled,
		},
		{
			name:     "headers differ",
			declared: manifest,
			live: app.InstalledMcpServer{
				Name: "srv", Transport: "http", URL: "https://mcp.example.com",
				Headers: map[string]string{"X-Key": "stale"}, HeadersKnown: true,
			},
			want: app.McpStatusInstalled,
		},
		{
			name:     "env differs",
			declared: stdio,
			live: app.InstalledMcpServer{
				Name: "srv", Transport: "stdio", Command: "npx -y srv",
				EnvLiteral: map[string]string{"MODE": "whatever"},
			},
			want: app.McpStatusInstalled,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stub := &stubMcpAdapter{id: "codex", available: true, listed: []app.InstalledMcpServer{tc.live}}
			a := newMcpTestApp(t,
				config.AgentsConfig{McpServers: []config.McpServer{tc.declared}},
				app.WithMcpAdapters([]app.McpAdapter{stub}),
				app.WithPluginAdapters(nil))
			rows, _, err := a.McpServerRows(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 {
				t.Fatalf("rows = %d, want 1", len(rows))
			}
			if got := rows[0].PerAgentStatus["codex"]; got != tc.want {
				t.Fatalf("status = %q, want %q", got, tc.want)
			}
			if tc.want != app.McpStatusDrifted {
				if rows[0].Drifted {
					t.Fatal("Drifted set on a non-drifted row")
				}
				return
			}
			if !rows[0].Drifted {
				t.Fatal("Drifted not set on a drifted row")
			}
			fields := rows[0].DriftFields["codex"]
			if len(fields) != 1 || fields[0] != tc.field {
				t.Fatalf("DriftFields = %v, want [%s]", fields, tc.field)
			}
		})
	}
}

func TestRestoreMcpServers_SkipsDriftedIdentity(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{id: "codex", available: true, listed: []app.InstalledMcpServer{{
		Name: "srv", Transport: "http", URL: "https://elsewhere.example.com",
	}}}
	srv := config.McpServer{Name: "srv", Transport: "http", URL: "https://mcp.example.com", Agents: []string{"codex"}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}}, app.WithMcpAdapters([]app.McpAdapter{stub}))

	res, err := a.RestoreMcpServers(context.Background(), app.RestoreMcpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.addedServers)+len(stub.removedNames) != 0 {
		t.Fatalf("drifted server was touched: add=%v remove=%v", stub.addedServers, stub.removedNames)
	}
	if len(res.Drift) != 1 || !strings.Contains(res.Drift[0], "url") {
		t.Fatalf("Drift = %v, want one line naming the url field", res.Drift)
	}
	if !strings.Contains(res.Drift[0], "mcp resolve") {
		t.Fatalf("Drift line = %q, want the resolve remedy", res.Drift[0])
	}
	if len(res.AlreadyInstalled)+len(res.Installed)+len(res.Updated) != 0 {
		t.Fatal("a drifted server must not be counted as converged")
	}
}

func TestRestoreMcpServers_HeadersStayManifestAuthoritative(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{id: "codex", available: true, listed: []app.InstalledMcpServer{{
		Name: "srv", Transport: "http", URL: "https://mcp.example.com",
		Headers: map[string]string{"X-Key": "old"}, HeadersKnown: true,
	}}}
	srv := config.McpServer{
		Name: "srv", Transport: "http", URL: "https://mcp.example.com",
		Headers: map[string]string{"X-Key": "new"}, Agents: []string{"codex"},
	}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}}, app.WithMcpAdapters([]app.McpAdapter{stub}))

	res, err := a.RestoreMcpServers(context.Background(), app.RestoreMcpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Drift) != 0 {
		t.Fatalf("Drift = %v, want none for a header change", res.Drift)
	}
	if len(res.Updated) != 1 || res.Updated[0] != "codex/srv" {
		t.Fatalf("Updated = %v, want the header update applied", res.Updated)
	}
	if len(stub.addedServers) != 1 || stub.addedServers[0].Headers["X-Key"] != "new" {
		t.Fatalf("re-added server = %v, want the manifest headers", stub.addedServers)
	}
}

func TestResolveMcpDrift_UseManagedReinstallsManifestDefinition(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{id: "codex", available: true, listed: []app.InstalledMcpServer{{
		Name: "srv", Transport: "stdio", Command: "npx -y hijacked",
	}}}
	srv := config.McpServer{Name: "srv", Transport: "stdio", Command: "npx -y srv", Agents: []string{"codex"}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}}, app.WithMcpAdapters([]app.McpAdapter{stub}))

	res, err := a.ResolveMcpDrift(context.Background(), app.ResolveMcpDriftOptions{
		Name: "srv", Strategy: app.McpDriftUseManaged,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Agents) != 1 || res.Agents[0] != "codex" {
		t.Fatalf("Agents = %v, want [codex]", res.Agents)
	}
	if len(stub.removedNames) != 1 || len(stub.addedServers) != 1 {
		t.Fatalf("remove/add = %v/%v, want one reinstall", stub.removedNames, stub.addedServers)
	}
	if stub.addedServers[0].Command != "npx -y srv" {
		t.Fatalf("re-added command = %q, want the manifest's", stub.addedServers[0].Command)
	}
	if got := loadMcpTestConfig(t, a).Agents.McpServers[0].Command; got != "npx -y srv" {
		t.Fatalf("manifest command = %q, want it untouched", got)
	}
}

func TestResolveMcpDrift_UseLocalAdoptsLiveIdentityKeepingSecrets(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{id: "codex", available: true, listed: []app.InstalledMcpServer{{
		Name: "srv", Transport: "http", URL: "https://elsewhere.example.com",
		Headers: map[string]string{"X-Key": "live"}, HeadersKnown: true,
	}}}
	srv := config.McpServer{
		Name: "srv", Transport: "http", URL: "https://mcp.example.com",
		Headers: map[string]string{"X-Key": "${ROTATED}"}, Env: []string{"TOKEN"},
		Agents: []string{"codex"},
	}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}}, app.WithMcpAdapters([]app.McpAdapter{stub}))

	if _, err := a.ResolveMcpDrift(context.Background(), app.ResolveMcpDriftOptions{
		Name: "srv", Strategy: app.McpDriftUseLocal,
	}); err != nil {
		t.Fatal(err)
	}
	if len(stub.addedServers)+len(stub.removedNames) != 0 {
		t.Fatal("use-local must not touch the agent")
	}
	got := loadMcpTestConfig(t, a).Agents.McpServers[0]
	if got.URL != "https://elsewhere.example.com" {
		t.Fatalf("manifest url = %q, want the live one adopted", got.URL)
	}
	if got.Headers["X-Key"] != "${ROTATED}" || len(got.Env) != 1 || got.Env[0] != "TOKEN" {
		t.Fatalf("manifest secrets = %+v, want headers and env preserved", got)
	}
}

func TestResolveMcpDrift_UseLocalRefusesCrossAgentConflict(t *testing.T) {
	t.Parallel()
	codex := &stubMcpAdapter{id: "codex", available: true, listed: []app.InstalledMcpServer{{
		Name: "srv", Transport: "http", URL: "https://one.example.com",
	}}}
	hermes := &stubMcpAdapter{id: "hermes-agent", available: true, listed: []app.InstalledMcpServer{{
		Name: "srv", Transport: "http", URL: "https://two.example.com",
	}}}
	srv := config.McpServer{Name: "srv", Transport: "http", URL: "https://mcp.example.com"}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}},
		app.WithMcpAdapters([]app.McpAdapter{codex, hermes}))

	_, err := a.ResolveMcpDrift(context.Background(), app.ResolveMcpDriftOptions{
		Name: "srv", Strategy: app.McpDriftUseLocal,
	})
	if err == nil || !strings.Contains(err.Error(), "drifted differently") {
		t.Fatalf("error = %v, want a cross-agent conflict refusal", err)
	}
	if got := loadMcpTestConfig(t, a).Agents.McpServers[0].URL; got != "https://mcp.example.com" {
		t.Fatalf("manifest url = %q, want it unchanged by the refusal", got)
	}
}

func TestResolveMcpDrift_DryRunAndNotDrifted(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{id: "codex", available: true, listed: []app.InstalledMcpServer{{
		Name: "srv", Transport: "http", URL: "https://elsewhere.example.com",
	}}}
	srv := config.McpServer{Name: "srv", Transport: "http", URL: "https://mcp.example.com", Agents: []string{"codex"}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}}, app.WithMcpAdapters([]app.McpAdapter{stub}))

	res, err := a.ResolveMcpDrift(context.Background(), app.ResolveMcpDriftOptions{
		Name: "srv", Strategy: app.McpDriftUseManaged, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Actions) != 1 || !strings.Contains(res.Actions[0], "reinstall srv on codex") {
		t.Fatalf("Actions = %v, want a reinstall preview", res.Actions)
	}
	if len(stub.addedServers)+len(stub.removedNames) != 0 {
		t.Fatal("dry run mutated the agent")
	}

	stub.listed = []app.InstalledMcpServer{{Name: "srv", Transport: "http", URL: "https://mcp.example.com"}}
	if _, err := a.ResolveMcpDrift(context.Background(), app.ResolveMcpDriftOptions{
		Name: "srv", Strategy: app.McpDriftUseManaged,
	}); err == nil || !strings.Contains(err.Error(), "not drifted") {
		t.Fatalf("error = %v, want a not-drifted refusal", err)
	}
}

func TestResolveMcpDrift_AgentFlagNarrowsAndValidates(t *testing.T) {
	t.Parallel()
	codex := &stubMcpAdapter{id: "codex", available: true, listed: []app.InstalledMcpServer{{
		Name: "srv", Transport: "stdio", Command: "npx -y hijacked",
	}}}
	hermes := &stubMcpAdapter{id: "hermes-agent", available: true, listed: []app.InstalledMcpServer{{
		Name: "srv", Transport: "stdio", Command: "npx -y srv",
	}}}
	srv := config.McpServer{Name: "srv", Transport: "stdio", Command: "npx -y srv"}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}},
		app.WithMcpAdapters([]app.McpAdapter{codex, hermes}))

	if _, err := a.ResolveMcpDrift(context.Background(), app.ResolveMcpDriftOptions{
		Name: "srv", Agents: []string{"hermes-agent"}, Strategy: app.McpDriftUseManaged,
	}); err == nil || !strings.Contains(err.Error(), "not drifted on hermes-agent") {
		t.Fatalf("error = %v, want a per-agent not-drifted refusal", err)
	}
	res, err := a.ResolveMcpDrift(context.Background(), app.ResolveMcpDriftOptions{
		Name: "srv", Agents: []string{"codex"}, Strategy: app.McpDriftUseManaged,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Agents) != 1 || res.Agents[0] != "codex" {
		t.Fatalf("Agents = %v, want only codex", res.Agents)
	}
	if len(hermes.removedNames) != 0 {
		t.Fatal("the healthy agent was reinstalled")
	}
}
