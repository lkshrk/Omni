package cli

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
)

func runAgentsMcpImportSplit(t *testing.T, a *app.App, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	state := &rootState{app: a}
	cmd := newAgentsMcpImportCmd(state)
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err = cmd.ExecuteContext(context.Background())
	return out.String(), errOut.String(), err
}

// Import is omni copying what another tool configured, so a header the user never typed must not reach
// settings.json — the same verdict `--header` in argv already gets. `agents mcp add` stays the consented
// door and is deliberately left unvetted; it is not a reason to adopt silently here.
func TestAgentsMcpImport_RefusesResolvedHeaderWithoutEchoingTheValue(t *testing.T) {
	adapter := &importStubMcpAdapter{id: "claude-code", listed: []app.InstalledMcpServer{{
		Name:      "linear",
		Transport: "http",
		URL:       "https://mcp.linear.app/mcp",
		Headers:   map[string]string{"Authorization": "Bearer live-token", "X-Env": "${LINEAR_ENV}"},
	}}}
	a := newImportTestApp(t, []app.McpAdapter{adapter})

	stdout, stderr, err := runAgentsMcpImportSplit(t, a, "linear")
	if err == nil {
		t.Fatal("import succeeded; a resolved header must be refused")
	}
	if !strings.Contains(err.Error(), "Authorization") || !strings.Contains(err.Error(), "linear") {
		t.Fatalf("error = %v; want the refusal naming the server and header", err)
	}
	if strings.Contains(err.Error(), "X-Env") {
		t.Fatalf("error = %v; want the reference-bearing header left out", err)
	}
	if strings.Contains(stdout, "imported linear") {
		t.Fatalf("stdout = %q; nothing may be reported as imported on refusal", stdout)
	}
	if strings.Contains(stdout+stderr+err.Error(), "live-token") {
		t.Fatalf("output echoes the secret: stdout=%q stderr=%q err=%v", stdout, stderr, err)
	}
	if len(adapter.addedServers) != 0 {
		t.Fatalf("adapter received %d servers; nothing may be written on refusal", len(adapter.addedServers))
	}

	cfg, err := a.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Agents.McpServers) != 0 {
		t.Fatalf("config holds %d mcp servers; nothing may be written on refusal", len(cfg.Agents.McpServers))
	}
}

// The consented door stays open: `agents mcp add --header` applies no vetting, exactly as `--command`
// applies none, so the literal the import path just refused is still installable when the user types it.
func TestAgentsMcpAdd_LiteralHeaderStaysUnvetted(t *testing.T) {
	adapter := &importStubMcpAdapter{id: "claude-code"}
	a := newImportTestApp(t, []app.McpAdapter{adapter})

	state := &rootState{app: a}
	cmd := newAgentsMcpAddCmd(state)
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{
		"--name", "linear", "--transport", "http", "--url", "https://mcp.linear.app/mcp",
		"--header", "Authorization: Bearer live-token",
	})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("agents mcp add: %v", err)
	}

	cfg, err := a.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Agents.McpServers) != 1 ||
		cfg.Agents.McpServers[0].Headers["Authorization"] != "Bearer live-token" {
		t.Fatalf("manifest = %+v; want the typed literal recorded verbatim", cfg.Agents.McpServers)
	}
}

const importProvenEnvValue = "ambient-value-4b71"

func importEnvTestApp(t *testing.T, adapter *importStubMcpAdapter, env map[string]string) *app.App {
	t.Helper()
	return newImportTestApp(t, []app.McpAdapter{adapter},
		app.WithEnvLookup(func(name string) string { return env[name] }))
}

func TestAgentsMcpImport_ProvenEnvKeepsValueOutOfTheManifestFile(t *testing.T) {
	adapter := &importStubMcpAdapter{id: "claude-code", listed: []app.InstalledMcpServer{{
		Name:       "linear",
		Transport:  "http",
		URL:        "https://mcp.linear.app/mcp",
		Headers:    map[string]string{"Authorization": "${LINEAR_TOKEN}"},
		EnvLiteral: map[string]string{"LINEAR_WORKSPACE": importProvenEnvValue},
	}}}
	a := importEnvTestApp(t, adapter, map[string]string{"LINEAR_WORKSPACE": importProvenEnvValue})

	if _, err := runAgentsMcpImport(t, a, "linear"); err != nil {
		t.Fatalf("agents mcp import linear: %v", err)
	}
	cfg, err := a.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Agents.McpServers) != 1 {
		t.Fatalf("config holds %d mcp servers; want 1", len(cfg.Agents.McpServers))
	}
	got := cfg.Agents.McpServers[0]
	if got.Headers["Authorization"] != "${LINEAR_TOKEN}" {
		t.Fatalf("headers = %v; want the ${ENV_VAR} reference preserved", got.Headers)
	}
	if len(got.Env) != 1 || got.Env[0] != "LINEAR_WORKSPACE" {
		t.Fatalf("env = %v; want the ambient variable recorded by name", got.Env)
	}
	if len(got.EnvLiteral) != 0 {
		t.Fatalf("env_literal = %v; want no value carried into the manifest", got.EnvLiteral)
	}
	raw, err := os.ReadFile(a.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), importProvenEnvValue) {
		t.Fatal("settings.json holds the environment value; import must write the variable name only")
	}
	if !strings.Contains(string(raw), "LINEAR_WORKSPACE") {
		t.Fatalf("settings.json = %s; want the variable name recorded", raw)
	}
}

func TestAgentsMcpImport_RefusesEnvValueTheEnvironmentDoesNotHold(t *testing.T) {
	adapter := &importStubMcpAdapter{id: "claude-code", listed: []app.InstalledMcpServer{{
		Name:       "linear",
		Transport:  "http",
		URL:        "https://mcp.linear.app/mcp",
		EnvLiteral: map[string]string{"LINEAR_TOKEN": importProvenEnvValue},
	}}}
	a := importEnvTestApp(t, adapter, map[string]string{"LINEAR_TOKEN": "something-else"})

	_, err := runAgentsMcpImport(t, a, "linear")
	if err == nil {
		t.Fatal("import succeeded; an unproven env value must be refused")
	}
	if !strings.Contains(err.Error(), "linear") || !strings.Contains(err.Error(), "LINEAR_TOKEN") {
		t.Fatalf("error = %v; want the server and offending variable named", err)
	}
	if strings.Contains(err.Error(), importProvenEnvValue) {
		t.Fatalf("error = %v; the refusal must not echo the value", err)
	}
	if len(adapter.addedServers) != 0 {
		t.Fatalf("adapter received %d servers; nothing may be written on refusal", len(adapter.addedServers))
	}
	cfg, err := a.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Agents.McpServers) != 0 {
		t.Fatalf("config holds %d mcp servers; nothing may be written on refusal", len(cfg.Agents.McpServers))
	}
}

func TestAgentsMcpImport_RefusesEmptyValueOfAnUnsetVariable(t *testing.T) {
	adapter := &importStubMcpAdapter{id: "claude-code", listed: []app.InstalledMcpServer{{
		Name:       "linear",
		Transport:  "http",
		URL:        "https://mcp.linear.app/mcp",
		EnvLiteral: map[string]string{"LINEAR_TOKEN": ""},
	}}}
	a := importEnvTestApp(t, adapter, nil)

	_, err := runAgentsMcpImport(t, a, "linear")
	if err == nil {
		t.Fatal("import succeeded; an unset variable is not a proven passthrough")
	}
	if !strings.Contains(err.Error(), "LINEAR_TOKEN") {
		t.Fatalf("error = %v; want the unproven variable named", err)
	}
}

func TestAgentsMcpImport_RefusesWholeServerWhenOneEnvValueIsUnproven(t *testing.T) {
	adapter := &importStubMcpAdapter{id: "claude-code", listed: []app.InstalledMcpServer{{
		Name:       "linear",
		Transport:  "http",
		URL:        "https://mcp.linear.app/mcp",
		EnvLiteral: map[string]string{"LINEAR_WORKSPACE": "omni", "LINEAR_TOKEN": importProvenEnvValue},
	}}}
	a := importEnvTestApp(t, adapter, map[string]string{"LINEAR_WORKSPACE": "omni"})

	_, err := runAgentsMcpImport(t, a, "linear")
	if err == nil {
		t.Fatal("import succeeded; a server with one unproven variable must not be partially adopted")
	}
	if !strings.Contains(err.Error(), "LINEAR_TOKEN") || strings.Contains(err.Error(), "LINEAR_WORKSPACE") {
		t.Fatalf("error = %v; want only the unproven variable named", err)
	}
	cfg, err := a.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Agents.McpServers) != 0 {
		t.Fatalf("config holds %d mcp servers; nothing may be written on refusal", len(cfg.Agents.McpServers))
	}
}

// Both surfaces refuse now, and the refusal must still name the offending variable rather than stopping at the first surface it found.
func TestAgentsMcpImport_RefusesUnprovenEnvAlongsideAResolvedHeader(t *testing.T) {
	adapter := &importStubMcpAdapter{id: "claude-code", listed: []app.InstalledMcpServer{{
		Name:       "linear",
		Transport:  "http",
		URL:        "https://mcp.linear.app/mcp",
		Headers:    map[string]string{"Authorization": "Bearer " + importProvenEnvValue},
		EnvLiteral: map[string]string{"LINEAR_TOKEN": importProvenEnvValue},
	}}}
	a := importEnvTestApp(t, adapter, nil)

	_, err := runAgentsMcpImport(t, a, "linear")
	if err == nil {
		t.Fatal("import succeeded; an unproven env value must be refused")
	}
	if !strings.Contains(err.Error(), "LINEAR_TOKEN") {
		t.Fatalf("error = %v; want the offending variable reported", err)
	}
	if strings.Contains(err.Error(), importProvenEnvValue) {
		t.Fatalf("error = %v; the refusal must not echo a live value", err)
	}
	if len(adapter.addedServers) != 0 {
		t.Fatalf("adapter received %d servers; nothing may be written on refusal", len(adapter.addedServers))
	}
}

func TestPrintAgentsSyncAllResult_DryRunListsAdoptedClaims(t *testing.T) {
	var out bytes.Buffer
	printAgentsSyncAllResult(&out, app.AgentsSyncAllResult{
		McpAdopted:     app.McpAdoptResult{WouldAdopt: []string{`would claim mcp server "linear" on claude`}},
		PluginsAdopted: app.PluginAdoptResult{WouldAdopt: []string{`would claim plugin "ecc" from marketplace on claude`}},
	}, true)

	for _, want := range []string{
		`mcp: would claim mcp server "linear" on claude`,
		`plugins: would claim plugin "ecc" from marketplace on claude`,
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, out.String())
		}
	}
}
