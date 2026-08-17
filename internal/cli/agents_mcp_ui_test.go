package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/executor"
)

func newAgentsUITestApp(t *testing.T, root config.RootConfig, manifest, lock, claudeState string) *app.App {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if manifest != "" || lock != "" {
		if err := os.MkdirAll(filepath.Join(home, ".apm"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{
		filepath.Join(home, ".apm", "apm.yml"):       manifest,
		filepath.Join(home, ".apm", "apm.lock.yaml"): lock,
		filepath.Join(home, ".claude.json"):          claudeState,
	} {
		if body == "" {
			continue
		}
		if err := os.WriteFile(name, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	if err := config.Save(cfgPath, &root); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath, app.WithMcpAdapters([]app.McpAdapter{}))
	a.SetFallbackExecutor(&executor.MockExecutor{})
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func runAgentsCmd(t *testing.T, a *app.App, build func(*rootState) *cobra.Command, args ...string) (string, error) {
	t.Helper()
	cmd := build(&rootState{app: a})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return out.String(), err
}

func uiTestConfig(servers ...config.McpServer) config.RootConfig {
	return config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"claude-code", "gemini-cli"}},
		Agents:   config.AgentsConfig{McpServers: servers},
	}
}

func TestAgentsMcpList_ShowsEffectiveAPMDeployment(t *testing.T) {
	a := newAgentsUITestApp(t, uiTestConfig(config.McpServer{
		Name: "linear", Transport: "http", URL: "https://linear.example/mcp", Agents: []string{"claude-code"},
	}), "name: t\nversion: 1.0.0\n", "mcp_target_servers:\n  claude:\n    - linear\n", "")

	out, err := runAgentsCmd(t, a, newAgentsMcpListCmd)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "claude-code(✓ apm)") || !strings.Contains(out, "gemini-cli(- apm)") {
		t.Fatalf("out = %q, want both APM targets marked", out)
	}
	if !strings.Contains(out, "declared: claude-code — deployed: all APM targets (APM limitation): claude-code, gemini-cli") {
		t.Fatalf("out = %q, want the scope divergence spelled out", out)
	}
}

func TestAgentsMcpList_ShowsVersionAndAPMOnlyDriftAdvice(t *testing.T) {
	a := newAgentsUITestApp(t,
		uiTestConfig(config.McpServer{Name: "shiplight", Transport: "stdio", Command: "bunx @shiplightai/mcp@1.0.0"}),
		"name: t\nversion: 1.0.0\n",
		"mcp_configs:\n  shiplight:\n    command: bunx\n    args: ['@shiplightai/mcp@3.1.4']\n",
		`{"mcpServers":{"shiplight":{"command":"bunx","args":["@shiplightai/mcp@3.1.4"]}}}`)

	out, err := runAgentsCmd(t, a, newAgentsMcpListCmd)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "3.1.4") {
		t.Fatalf("out = %q, want the resolved version column", out)
	}
	if !strings.Contains(out, `drifted on command; the next "omni agents sync" reconciles it`) {
		t.Fatalf("out = %q, want APM drift reported without a resolve verb", out)
	}
	if strings.Contains(out, "mcp resolve") {
		t.Fatalf("out = %q, must not offer a remedy no omni verb can apply", out)
	}
}

func TestAgentsMcpList_ShowsAMissingVersionAsADash(t *testing.T) {
	a := newAgentsUITestApp(t,
		uiTestConfig(config.McpServer{Name: "plain", Transport: "stdio", Command: "codebase-memory-mcp"}),
		"name: t\nversion: 1.0.0\n", "", "")

	out, err := runAgentsCmd(t, a, newAgentsMcpListCmd)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "plain  stdio  -") {
		t.Fatalf("out = %q, want a dash for an unpinned server", out)
	}
}

func TestAgentsMcpList_MarksNativelyManagedAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	root := config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"claude-code", "codex"}},
		Agents: config.AgentsConfig{McpServers: []config.McpServer{
			{Name: "local", Transport: "stdio", Command: "npx local-mcp", Agents: []string{"codex"}},
		}},
	}
	if err := config.Save(cfgPath, &root); err != nil {
		t.Fatal(err)
	}
	stub := &importStubMcpAdapter{
		id:     "codex",
		listed: []app.InstalledMcpServer{{Name: "local", Transport: "stdio", Command: "npx local-mcp"}},
	}
	a := app.New(cfgPath, app.WithMcpAdapters([]app.McpAdapter{stub}))
	a.SetFallbackExecutor(&executor.MockExecutor{})
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	out, err := runAgentsCmd(t, a, newAgentsMcpListCmd)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "codex(✓ native)") {
		t.Fatalf("out = %q, want the codex registration marked native", out)
	}
	if strings.Contains(out, "claude-code") {
		t.Fatalf("out = %q, want a codex-only server to stay off the APM surface", out)
	}
}

const cliForeignManifest = `name: omni-host
version: 0.0.0
dependencies:
  apm:
    - someone/else#v1.2.0
  mcp:
    - name: foreign
      registry: false
      transport: stdio
      command: npx
      args: ['foreign-mcp']
`

func TestAgentsMcpImport_ListsAndAdoptsForeignManifestEntries(t *testing.T) {
	a := newAgentsUITestApp(t, uiTestConfig(), cliForeignManifest, "", "")

	out, err := runAgentsCmd(t, a, newAgentsMcpImportCmd)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "-- undeclared in the APM manifest --") || !strings.Contains(out, "foreign  stdio") {
		t.Fatalf("out = %q, want the preserved manifest entry listed", out)
	}

	out, err = runAgentsCmd(t, a, newAgentsMcpImportCmd, "foreign")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "imported mcp server foreign") {
		t.Fatalf("out = %q, want the import confirmed", out)
	}
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.McpServers) != 1 || cfg.Agents.McpServers[0].Name != "foreign" {
		t.Fatalf("config = %#v, want the foreign server declared", cfg.Agents.McpServers)
	}
}

func TestAgentsImport_ListsAndAdoptsForeignPackages(t *testing.T) {
	a := newAgentsUITestApp(t, uiTestConfig(), cliForeignManifest, "", "")

	out, err := runAgentsCmd(t, a, newAPMAgentsImportCmd)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "someone/else#v1.2.0") {
		t.Fatalf("out = %q, want the undeclared package listed", out)
	}

	if _, err = runAgentsCmd(t, a, newAPMAgentsImportCmd, "someone/else"); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.Packages) != 1 || cfg.Agents.Packages[0].Ref != "v1.2.0" {
		t.Fatalf("config = %#v, want the foreign package declared", cfg.Agents.Packages)
	}
}
