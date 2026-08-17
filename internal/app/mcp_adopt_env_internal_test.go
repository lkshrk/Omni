package app

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

const provenEnvValue = "ambient-value-9f2c"

func adoptEnvTestApp(t *testing.T, servers []InstalledMcpServer, env map[string]string) *App {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	mcp := &listingMcpAdapter{id: "codex", listed: servers}
	return newSkillsTestApp(t, config.AgentsConfig{},
		WithMcpAdapters([]McpAdapter{mcp}),
		WithEnvLookup(func(name string) string { return env[name] }))
}

func TestAdoptUnmanagedMcpServers_ProvenEnvKeepsValueOutOfTheManifestFile(t *testing.T) {
	a := adoptEnvTestApp(t,
		[]InstalledMcpServer{{
			Name: "gh", Transport: "stdio", Command: "npx gh-mcp", HeadersKnown: true,
			EnvLiteral: map[string]string{"GITHUB_TOKEN": provenEnvValue},
		}},
		map[string]string{"GITHUB_TOKEN": provenEnvValue})

	res, err := a.AdoptUnmanagedMcpServers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Adopted != 1 || len(res.Skipped) != 0 {
		t.Fatalf("result = %+v, want the proven passthrough adopted without a refusal", res)
	}
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	adopted := findMcpServer(cfg.Agents.McpServers, "gh")
	if adopted == nil {
		t.Fatalf("manifest = %+v, want the server claimed", cfg.Agents.McpServers)
	}
	if len(adopted.Env) != 1 || adopted.Env[0] != "GITHUB_TOKEN" {
		t.Fatalf("Env = %v, want the ambient variable recorded by name", adopted.Env)
	}
	if len(adopted.EnvLiteral) != 0 {
		t.Fatalf("EnvLiteral = %v, want no value carried into the manifest", adopted.EnvLiteral)
	}
	raw, err := os.ReadFile(a.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), provenEnvValue) {
		t.Fatal("settings.json holds the environment value; adoption must write the variable name only")
	}
	if !strings.Contains(string(raw), "GITHUB_TOKEN") {
		t.Fatalf("settings.json = %s, want the variable name recorded", raw)
	}
}

func TestAdoptUnmanagedMcpServers_RefusesEnvValueTheEnvironmentDoesNotHold(t *testing.T) {
	a := adoptEnvTestApp(t,
		[]InstalledMcpServer{{
			Name: "gh", Transport: "stdio", Command: "npx gh-mcp", HeadersKnown: true,
			EnvLiteral: map[string]string{"GITHUB_TOKEN": provenEnvValue},
		}},
		map[string]string{"GITHUB_TOKEN": "something-else"})

	res, err := a.AdoptUnmanagedMcpServers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Adopted != 0 {
		t.Fatalf("Adopted = %d, want an unproven value refused", res.Adopted)
	}
	if len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0], "gh") ||
		!strings.Contains(res.Skipped[0], "GITHUB_TOKEN") {
		t.Fatalf("Skipped = %v, want one line naming the server and the offending variable", res.Skipped)
	}
	if strings.Contains(res.Skipped[0], provenEnvValue) {
		t.Fatal("refusal echoes the environment value; it must name variables only")
	}
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.McpServers) != 0 {
		t.Fatalf("manifest = %+v, want nothing written on refusal", cfg.Agents.McpServers)
	}
}

func TestAdoptUnmanagedMcpServers_RefusesEmptyValueOfAnUnsetVariable(t *testing.T) {
	a := adoptEnvTestApp(t,
		[]InstalledMcpServer{{
			Name: "gh", Transport: "stdio", Command: "npx gh-mcp", HeadersKnown: true,
			EnvLiteral: map[string]string{"GITHUB_TOKEN": ""},
		}},
		nil)

	res, err := a.AdoptUnmanagedMcpServers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Adopted != 0 {
		t.Fatalf("Adopted = %d, want an unset variable refused rather than read as a passthrough", res.Adopted)
	}
	if len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0], "GITHUB_TOKEN") {
		t.Fatalf("Skipped = %v, want the unproven variable named", res.Skipped)
	}
}

func TestAdoptUnmanagedMcpServers_RefusesWholeServerWhenOneEnvValueIsUnproven(t *testing.T) {
	a := adoptEnvTestApp(t,
		[]InstalledMcpServer{{
			Name: "gh", Transport: "stdio", Command: "npx gh-mcp", HeadersKnown: true,
			EnvLiteral: map[string]string{"MODE": "prod", "GITHUB_TOKEN": provenEnvValue},
		}},
		map[string]string{"MODE": "prod"})

	res, err := a.AdoptUnmanagedMcpServers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Adopted != 0 {
		t.Fatalf("Adopted = %d, want no partial adoption of a server with an unproven variable", res.Adopted)
	}
	if len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0], "GITHUB_TOKEN") {
		t.Fatalf("Skipped = %v, want the unproven variable named", res.Skipped)
	}
	if strings.Contains(res.Skipped[0], "MODE") {
		t.Fatalf("Skipped = %v, want only the unproven variable named", res.Skipped)
	}
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.McpServers) != 0 {
		t.Fatalf("manifest = %+v, want nothing written on refusal", cfg.Agents.McpServers)
	}
}

// Both surfaces carry the same literal, so both refuse and the user is told about each; the pass must not stop at the first one it found. Neither message may echo the value.
func TestAdoptUnmanagedMcpServers_ReportsHeaderAndEnvRefusalsTogether(t *testing.T) {
	a := adoptEnvTestApp(t,
		[]InstalledMcpServer{{
			Name: "gh", Transport: "http", URL: "https://gh.example.com", HeadersKnown: true,
			Headers:    map[string]string{"Authorization": "Bearer " + provenEnvValue},
			EnvLiteral: map[string]string{"GITHUB_TOKEN": provenEnvValue},
		}},
		nil)

	res, err := a.AdoptUnmanagedMcpServers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(res.Skipped, "\n")
	if res.Adopted != 0 || len(res.Skipped) != 2 {
		t.Fatalf("result = %+v, want one refusal per carrying surface and nothing claimed", res)
	}
	if !strings.Contains(joined, "GITHUB_TOKEN") || !strings.Contains(joined, "Authorization") {
		t.Fatalf("Skipped = %v, want both the variable and the header named", res.Skipped)
	}
	if strings.Contains(joined, provenEnvValue) {
		t.Fatal("refusal echoes a live value; it must name header and variable names only")
	}
}

func TestAdoptUnmanagedMcpServers_ServerWithoutEnvAdoptsUnchanged(t *testing.T) {
	a := adoptEnvTestApp(t,
		[]InstalledMcpServer{{
			Name: "plain", Transport: "stdio", Command: "npx plain", HeadersKnown: true,
		}},
		nil)

	res, err := a.AdoptUnmanagedMcpServers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Adopted != 1 || len(res.Skipped) != 0 {
		t.Fatalf("result = %+v, want a server without env adopted as before", res)
	}
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	adopted := findMcpServer(cfg.Agents.McpServers, "plain")
	if adopted == nil {
		t.Fatalf("manifest = %+v, want the server claimed", cfg.Agents.McpServers)
	}
	if len(adopted.Env) != 0 || len(adopted.EnvLiteral) != 0 {
		t.Fatalf("entry = %+v, want no env recorded", adopted)
	}
	if adopted.Command != "npx plain" {
		t.Fatalf("entry = %+v, want the reported definition copied", adopted)
	}
}
