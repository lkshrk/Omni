package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/executor"
)

func TestAgentsRowsCountAnUndeclaredAPMTargetsMissingDeployment(t *testing.T) {
	m := baseModel(nil)
	m.mcpLoaded = true
	m.enabledAgents = []string{"claude-code"}
	m.mcpRows = []app.McpServerRow{{
		Name:                "linear",
		Agents:              []string{"claude-code"},
		APMAgents:           []string{"claude-code", "gemini-cli"},
		UndeclaredAPMAgents: []string{"gemini-cli"},
		PerAgentStatus: map[string]app.McpStatus{
			"claude-code": app.McpStatusInstalled,
			"gemini-cli":  app.McpStatusMissing,
		},
	}}

	if counts := statusAgentsCounts(m); counts.McpMissing != 1 {
		t.Fatalf("McpMissing = %d, want the agent APM deploys to counted despite the narrower scoping", counts.McpMissing)
	}
}

// Rows sliced out of one cached array share its storage: appending an APM-only target into a row's spare
// capacity would rewrite the next row's scoping.
func TestMcpRowAgentIDsLeavesTheRowsOwnAgentsIntact(t *testing.T) {
	agents := []string{"claude-code", "codex"}
	rows := []app.McpServerRow{
		{Name: "linear", Agents: agents[:1], APMAgents: []string{"gemini-cli"}},
		{Name: "context7", Agents: agents[1:]},
	}

	if ids := mcpRowAgentIDs(rows[0], nil); !reflect.DeepEqual(ids, []string{"claude-code", "gemini-cli"}) {
		t.Fatalf("ids = %v", ids)
	}
	if !reflect.DeepEqual(rows[1].Agents, []string{"codex"}) {
		t.Fatalf("rows[1].Agents = %v, want the neighbouring row's scoping untouched", rows[1].Agents)
	}
}

func TestAgentsTabSyncDelegatesToGlobalAPM(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".apm"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "name: test\nversion: 1.0.0\ndependencies:\n  apm:\n    - git: acme/shared\n"
	if err := os.WriteFile(filepath.Join(home, ".apm", "apm.yml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "settings.json")
	if err := config.Save(configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"codex"}},
		Agents:   config.AgentsConfig{Packages: []config.SkillPackage{{Source: "acme/shared"}}},
	}); err != nil {
		t.Fatal(err)
	}
	a := app.New(configPath)
	if err := a.InitTestMode(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	m := modelForCmds(a)
	m.mode = viewSkills
	mock := &executor.MockExecutor{}
	m.app.SetFallbackExecutor(mock)
	cmds := m.doAgentsSyncAll()
	if len(cmds) < 2 {
		t.Fatalf("sync commands = %d", len(cmds))
	}
	msg := cmds[1]()
	if done, ok := msg.(agentsProgressDoneMsg); !ok || done.skillsErr != nil {
		t.Fatalf("sync result = %#v", msg)
	}
	want := []string{"install", "-g", "--only", "apm", "--target", "codex"}
	if len(mock.Calls) != 1 || mock.Calls[0].Name != "apm" || !reflect.DeepEqual(mock.Calls[0].Args, want) {
		t.Fatalf("calls = %#v, want apm %v", mock.Calls, want)
	}
}

func TestAgentsTabUpdateDelegatesToGlobalAPM(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".apm"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".apm", "apm.yml"), []byte("name: test\nversion: 1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "settings.json")
	if err := config.Save(configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"codex"}},
		Agents:   config.AgentsConfig{Packages: []config.SkillPackage{{Source: "acme/shared", Agents: []string{"codex"}}}},
	}); err != nil {
		t.Fatal(err)
	}
	a := app.New(configPath)
	if err := a.InitTestMode(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	m := modelForCmds(a)
	mock := &executor.MockExecutor{}
	m.app.SetFallbackExecutor(mock)
	cmds := m.doAgentsUpdateAll()
	if len(cmds) < 2 {
		t.Fatalf("update commands = %d", len(cmds))
	}
	if done, ok := cmds[1]().(agentsProgressDoneMsg); !ok || done.skillsErr != nil {
		t.Fatalf("update result = %#v", done)
	}
	want := []string{"install", "-g", "--only", "apm", "--update", "--target", "codex"}
	if len(mock.Calls) != 1 || mock.Calls[0].Name != "apm" || !reflect.DeepEqual(mock.Calls[0].Args, want) {
		t.Fatalf("calls = %#v, want apm %v", mock.Calls, want)
	}
}

func TestAgentsTabUpdateWithoutManifestReturnsGuidance(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(t.TempDir(), "settings.json")
	if err := config.Save(configPath, &config.RootConfig{Version: config.CurrentVersion}); err != nil {
		t.Fatal(err)
	}
	a := app.New(configPath)
	if err := a.InitTestMode(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	m := modelForCmds(a)
	mock := &executor.MockExecutor{}
	m.app.SetFallbackExecutor(mock)
	cmds := m.doAgentsUpdateAll()
	done, ok := cmds[1]().(agentsProgressDoneMsg)
	if !ok || done.skillsErr == nil {
		t.Fatalf("update result = %#v", done)
	}
	if !strings.Contains(done.skillsErr.Error(), "omni agents sync") {
		t.Fatalf("error lacks guidance: %v", done.skillsErr)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("apm invoked despite missing manifest: %#v", mock.Calls)
	}
}

func TestSetupAgentsImportInstallsConfiguredPackages(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(t.TempDir(), "settings.json")
	if err := config.Save(configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"codex"}},
		Agents:   config.AgentsConfig{Packages: []config.SkillPackage{{Source: "acme/demo", Agents: []string{"codex"}}}},
	}); err != nil {
		t.Fatal(err)
	}
	a := app.New(configPath)
	mock := &executor.MockExecutor{}
	a.SetFallbackExecutor(mock)
	m := modelForCmds(a)
	msg := m.doSetupAgentsImportAll()()
	done, ok := msg.(setupAgentsImportDoneMsg)
	if !ok || done.err != nil || done.skills != 1 {
		t.Fatalf("setup install = %#v", msg)
	}
	want := []string{"install", "-g", "--only", "apm", "--target", "codex"}
	if len(mock.Calls) != 1 || !reflect.DeepEqual(mock.Calls[0].Args, want) {
		t.Fatalf("calls = %#v, want apm %v", mock.Calls, want)
	}
	got, err := config.Load(configPath)
	if err != nil || len(got.Agents.Packages) != 1 {
		t.Fatalf("config mutated: %+v, %v", got.Agents, err)
	}
}

// Global MCP has no per-server scoping, so a row that showed neither the owning side nor APM's wider reach
// would read as if the config's narrower agents list had been honoured.
func TestAgentsTabRendersMcpOwnershipDivergenceAndVersion(t *testing.T) {
	m := baseModel(nil)
	m.width = 200
	m.agentsEnabled, m.mcpEnabled = true, true
	m.mcpRowsKnown = true
	m.enabledAgents = []string{"claude-code", "codex"}
	m.mcpRows = []app.McpServerRow{{
		Name:                "linear",
		Transport:           "http",
		Version:             "1.4.0",
		Agents:              []string{"claude-code"},
		APMAgents:           []string{"claude-code", "gemini-cli"},
		UndeclaredAPMAgents: []string{"gemini-cli"},
		PerAgentStatus: map[string]app.McpStatus{
			"claude-code": app.McpStatusInstalled,
			"gemini-cli":  app.McpStatusMissing,
		},
	}, {
		Name:           "codexonly",
		Transport:      "stdio",
		Agents:         []string{"codex"},
		PerAgentStatus: map[string]app.McpStatus{"codex": app.McpStatusInstalled},
	}}

	body := m.viewSkillsBody()
	for _, want := range []string{
		"linear", "1.4.0",
		"claude-code(✓ apm)",
		"gemini-cli(- apm) deployed, undeclared (APM)",
		"codexonly", "codex(✓ native)",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("agents body missing %q:\n%s", want, body)
		}
	}
}
