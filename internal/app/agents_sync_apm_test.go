package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/executor"
)

const emptySyncManifest = "name: test\nversion: 1.0.0\n"

func newSyncApp(t *testing.T, cfg *config.RootConfig, manifest string, opts ...func(*App)) (*App, *executor.MockExecutor, string) {
	t.Helper()
	return newSyncAppEnv(t, cfg, manifest, nil, opts...)
}

func newSyncAppEnv(
	t *testing.T,
	cfg *config.RootConfig,
	manifest string,
	env map[string]string,
	opts ...func(*App),
) (*App, *executor.MockExecutor, string) {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "settings.json")
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if manifest != "" {
		if err := os.MkdirAll(filepath.Join(home, ".apm"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, ".apm", "apm.yml"), []byte(manifest), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mock := &executor.MockExecutor{}
	all := append([]func(*App){WithEnvLookup(func(name string) string {
		if name == "HOME" {
			return home
		}
		return env[name]
	})}, opts...)
	a := New(configPath, all...)
	a.SetFallbackExecutor(mock)
	return a, mock, configPath
}

// The native restore path shells out to the agent CLIs for its plugin-shadow check; only apm calls matter here.
func apmCalls(mock *executor.MockExecutor) [][]string {
	var out [][]string
	for _, call := range mock.Calls {
		if call.Name == "apm" {
			out = append(out, call.Args)
		}
	}
	return out
}

// appliedOwned is the record a host whose installs all succeeded carries: what omni rendered is also what it applied.
func appliedOwned(packages, mcp []string) apmOwnedIdentities {
	return apmOwnedIdentities{
		Rendered: apmSurfaceIdentities{Packages: slices.Clone(packages), Mcp: slices.Clone(mcp)},
		Applied:  apmSurfaceIdentities{Packages: slices.Clone(packages), Mcp: slices.Clone(mcp)},
	}
}

// A surface failure is recorded rather than returned, so the sync can reach the surfaces that still work.
func syncErrorText(res AgentsSyncAllResult) string {
	parts := make([]string, 0, len(res.Errors))
	for _, e := range res.Errors {
		parts = append(parts, e.Error())
	}
	return strings.Join(parts, "\n")
}

func readSyncManifest(t *testing.T, a *App) []byte {
	t.Helper()
	path, err := a.globalAPMManifestPath()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("manifest not written: %v", err)
	}
	return raw
}

func syncManifestDeps(t *testing.T, a *App, key string) []any {
	t.Helper()
	path, err := a.globalAPMManifestPath()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("manifest not written: %v", err)
	}
	var doc struct {
		Name         string           `yaml:"name"`
		Version      string           `yaml:"version"`
		Dependencies map[string][]any `yaml:"dependencies"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("manifest %q: %v", raw, err)
	}
	if doc.Name == "" || doc.Version == "" {
		t.Fatalf("manifest lacks the required name/version: %q", raw)
	}
	return doc.Dependencies[key]
}

// A frozen replay must stay scoped: a bare install reconciles every surface across every target APM
// auto-detects, which would push MCP credentials at agents this host never selected.
func TestAgentsSyncAllFrozenReplaysEachSurfaceScoped(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"claude-code"}},
		Agents: config.AgentsConfig{
			Packages:   []config.SkillPackage{{Source: "acme/shared", Agents: []string{"claude-code"}}},
			McpServers: []config.McpServer{{Name: "linear", Transport: "http", URL: "https://linear.example/mcp"}},
		},
	}
	manifest := "name: test\nversion: 1.0.0\ndependencies:\n  apm:\n    - git: acme/shared\n  mcp:\n  - name: linear\n    registry: false\n    transport: http\n    url: https://linear.example/mcp\n"
	a, mock, _ := newSyncApp(t, cfg, manifest, WithMcpAdapters([]McpAdapter{}))
	mock.Responses = []executor.MockCall{{Stdout: "installed\n", Stderr: "warning\n"}}

	result, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{Frozen: true, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "installed\n" || result.Stderr != "warning\n" {
		t.Fatalf("result = %#v", result)
	}
	want := [][]string{
		{"install", "-g", "--frozen", "--only", "apm", "--dry-run", "--target", "claude"},
		{"install", "-g", "--frozen", "--only", "mcp", "--dry-run", "--target", "claude"},
	}
	if calls := apmCalls(mock); !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
	if !bytes.Equal(readSyncManifest(t, a), []byte(manifest)) {
		t.Fatal("frozen replay regenerated the manifest")
	}
}

// Frozen resolves targets from config even though it never regenerates the manifest: an unscoped install is
// never safe, so a surface with no target is skipped instead.
func TestAgentsSyncAllFrozenSkipsMcpWithoutATarget(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"codex"}},
		Agents:   config.AgentsConfig{McpServers: []config.McpServer{{Name: "linear", Transport: "http", URL: "https://linear.example/mcp"}}},
	}
	manifest := "name: test\nversion: 1.0.0\ndependencies:\n  mcp:\n  - name: linear\n    registry: false\n    transport: http\n    url: https://linear.example/mcp\n"
	a, mock, _ := newSyncApp(t, cfg, manifest, WithMcpAdapters([]McpAdapter{&recordingMcpAdapter{id: "codex"}}))

	result, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{Frozen: true})
	if err != nil {
		t.Fatal(err)
	}
	if calls := apmCalls(mock); len(calls) != 0 {
		t.Fatalf("calls = %#v, want the unscoped mcp replay refused", calls)
	}
	if !strings.Contains(strings.Join(result.Warnings, " "), "no MCP-capable APM target") {
		t.Fatalf("warnings = %v", result.Warnings)
	}
}

func TestAgentsSyncAllRendersManifestAndInstallsOncePerSurface(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"codex"}},
		Agents: config.AgentsConfig{Packages: []config.SkillPackage{
			{Source: "acme/shared", Agents: []string{"codex"}, Ref: "v1"},
		}},
	}
	a, mock, configPath := newSyncApp(t, cfg, "")

	result, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"install", "-g", "--only", "apm", "--target", "codex"}
	if len(mock.Calls) != 1 || !reflect.DeepEqual(mock.Calls[0].Args, want) {
		t.Fatalf("calls = %#v, want apm %v", mock.Calls, want)
	}
	deps := syncManifestDeps(t, a, "apm")
	wantDeps := []any{map[string]any{"git": "acme/shared", "ref": "v1", "targets": []any{"codex"}}}
	if !reflect.DeepEqual(deps, wantDeps) {
		t.Fatalf("apm deps = %#v, want %#v", deps, wantDeps)
	}
	if result.InstalledPackages != 1 {
		t.Fatalf("result = %#v", result)
	}
	got, err := config.Load(configPath)
	if err != nil || len(got.Agents.Packages) != 1 {
		t.Fatalf("config mutated: %+v, %v", got.Agents, err)
	}
}

func TestAgentsSyncAllHonorsGroupHostScoping(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "workhost")
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"codex"}},
		Agents: config.AgentsConfig{Packages: []config.SkillPackage{
			{Source: "acme/everywhere", Agents: []string{"codex"}},
			{Source: "acme/grouped", Agents: []string{"codex"}},
		}},
		Groups: []*config.GroupConfig{{Name: "ai-plugins", Skills: []string{"acme/grouped"}}},
		Hosts:  map[string][]string{"otherhost": {"ai-plugins"}, "workhost": {}},
	}
	a, mock, configPath := newSyncApp(t, cfg, "")

	if _, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{}); err != nil {
		t.Fatal(err)
	}
	want := []string{"install", "-g", "--only", "apm", "--target", "codex"}
	if len(mock.Calls) != 1 || !reflect.DeepEqual(mock.Calls[0].Args, want) {
		t.Fatalf("calls = %#v, want apm %v", mock.Calls, want)
	}
	deps := syncManifestDeps(t, a, "apm")
	if len(deps) != 1 || deps[0].(map[string]any)["git"] != "acme/everywhere" {
		t.Fatalf("apm deps = %#v, want the ungrouped package only", deps)
	}
	got, err := config.Load(configPath)
	if err != nil || len(got.Agents.Packages) != 2 || len(got.Groups) != 1 || len(got.Groups[0].Skills) != 1 {
		t.Fatalf("config mutated: %+v, %v", got.Agents, err)
	}
}

// A host-scoped-out package must disappear from that host's manifest, or the next install redeploys it.
func TestAgentsSyncAllDropsManagedEntryScopedAwayFromThisHost(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "workhost")
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"codex"}},
		Agents: config.AgentsConfig{Packages: []config.SkillPackage{
			{Source: "acme/everywhere", Agents: []string{"codex"}},
			{Source: "acme/grouped", Agents: []string{"codex"}},
		}},
		Groups: []*config.GroupConfig{{Name: "ai-plugins", Skills: []string{"acme/grouped"}}},
		Hosts:  map[string][]string{"otherhost": {"ai-plugins"}, "workhost": {}},
	}
	manifest := "name: test\nversion: 1.0.0\ndependencies:\n  apm:\n    - git: acme/grouped\n      targets: [codex]\n"
	a, _, _ := newSyncApp(t, cfg, manifest)

	if _, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{}); err != nil {
		t.Fatal(err)
	}
	deps := syncManifestDeps(t, a, "apm")
	if len(deps) != 1 || deps[0].(map[string]any)["git"] != "acme/everywhere" {
		t.Fatalf("apm deps = %#v, want the scoped-away package dropped", deps)
	}
}

func TestAgentsSyncAllInstallsGroupPackagesOnMemberHost(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "workhost")
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"codex"}},
		Agents: config.AgentsConfig{Packages: []config.SkillPackage{
			{Source: "acme/grouped", Agents: []string{"codex"}},
		}},
		Groups: []*config.GroupConfig{{Name: "ai-plugins", Skills: []string{"acme/grouped"}}},
		Hosts:  map[string][]string{"workhost": {"ai-plugins"}},
	}
	a, mock, _ := newSyncApp(t, cfg, "")

	if _, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{}); err != nil {
		t.Fatal(err)
	}
	want := []string{"install", "-g", "--only", "apm", "--target", "codex"}
	if len(mock.Calls) != 1 || !reflect.DeepEqual(mock.Calls[0].Args, want) {
		t.Fatalf("calls = %#v, want apm %v", mock.Calls, want)
	}
}

// One invocation reconciles the whole package surface; per-dependency targets carry the scoping.
func TestAgentsSyncAllUsesOneInvocationWithTheUnionTargetSet(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"codex", "claude-code"}},
		Agents: config.AgentsConfig{Packages: []config.SkillPackage{
			{Source: "acme/one", Agents: []string{"codex"}},
			{Source: "acme/two", Agents: []string{"claude-code"}},
			{Source: "acme/three", Agents: []string{"codex"}},
		}},
	}
	a, mock, _ := newSyncApp(t, cfg, "")

	if _, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{}); err != nil {
		t.Fatal(err)
	}
	want := []string{"install", "-g", "--only", "apm", "--target", "claude,codex"}
	if len(mock.Calls) != 1 || !reflect.DeepEqual(mock.Calls[0].Args, want) {
		t.Fatalf("calls = %#v, want a single apm %v", mock.Calls, want)
	}
	deps := syncManifestDeps(t, a, "apm")
	want = []string{"acme/one", "acme/two", "acme/three"}
	for i, dep := range deps {
		if dep.(map[string]any)["git"] != want[i] {
			t.Fatalf("apm deps = %#v, want %v in order", deps, want)
		}
	}
	if len(deps) != 3 {
		t.Fatalf("apm deps = %#v, want three entries", deps)
	}
}

func TestAgentsSyncAllSkipsPackagesDisabledByAgentsUse(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"codex"}},
		Agents: config.AgentsConfig{Packages: []config.SkillPackage{
			{Source: "acme/kept", Agents: []string{"codex"}},
			{Source: "acme/disabled", Agents: []string{"claude-code"}},
		}},
	}
	a, mock, _ := newSyncApp(t, cfg, "")

	result, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"install", "-g", "--only", "apm", "--target", "codex"}
	if len(mock.Calls) != 1 || !reflect.DeepEqual(mock.Calls[0].Args, want) {
		t.Fatalf("calls = %#v, want apm %v", mock.Calls, want)
	}
	if !strings.Contains(strings.Join(result.Warnings, " "), `"acme/disabled" skipped`) {
		t.Fatalf("warnings = %v", result.Warnings)
	}
	deps := syncManifestDeps(t, a, "apm")
	if len(deps) != 1 || deps[0].(map[string]any)["git"] != "acme/kept" {
		t.Fatalf("apm deps = %#v, want the enabled package only", deps)
	}
}

// A host that declares nothing and has no manifest gets no ~/.apm: creating one would be the sync's only
// effect, and it fails outright where HOME is not writable.
func TestAgentsSyncAllCreatesNoManifestWhenNothingResolves(t *testing.T) {
	a, mock, _ := newSyncApp(t, &config.RootConfig{Version: config.CurrentVersion}, "")
	result, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("apm invoked with nothing to do: %#v", mock.Calls)
	}
	if !strings.Contains(strings.Join(result.Warnings, " "), "nothing to install") {
		t.Fatalf("warnings = %v", result.Warnings)
	}
	path, err := a.globalAPMManifestPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("stat %s = %v, want the APM directory left uncreated", filepath.Dir(path), err)
	}
}

// A manifest that exists is still regenerated when nothing resolves, so entries the config dropped are pruned.
func TestAgentsSyncAllEmptiesAnExistingManifestWhenNothingResolves(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion, Settings: config.Settings{AgentsUse: []string{"codex"}}}
	manifest := "name: test\nversion: 1.0.0\ndependencies:\n  apm:\n    - git: acme/dropped\n      targets:\n        - codex\n"
	a, mock, _ := newSyncApp(t, cfg, manifest)
	home, err := a.apmDeployRoot()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".apm", apmOwnedSidecarName),
		[]byte(`{"packages":["acme/dropped"],"mcp":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{}); err != nil {
		t.Fatal(err)
	}
	want := []string{"install", "-g", "--only", "apm", "--target", "codex"}
	if calls := apmCalls(mock); len(calls) != 1 || !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("calls = %#v, want one %v", calls, want)
	}
	if deps := syncManifestDeps(t, a, "apm"); len(deps) != 0 {
		t.Fatalf("apm deps = %#v, want the dropped entry pruned", deps)
	}
}

// A foreign entry is preserved but never installed on omni's behalf. This host selects a target, so one is
// available to scope from — installing anyway would deploy a package the config never declared.
func TestAgentsSyncAllPreservesForeignManifestEntriesWithoutInstallingThem(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"codex"}},
	}
	manifest := "name: test\nversion: 1.0.0\ndependencies:\n  apm:\n    - someone/else\n"
	a, mock, _ := newSyncApp(t, cfg, manifest)
	result, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if calls := apmCalls(mock); len(calls) != 0 {
		t.Fatalf("calls = %#v, want the foreign-only manifest left uninstalled", calls)
	}
	if !strings.Contains(strings.Join(result.Warnings, " "), "nothing to install") {
		t.Fatalf("warnings = %v", result.Warnings)
	}
	deps := syncManifestDeps(t, a, "apm")
	if !reflect.DeepEqual(deps, []any{"someone/else"}) {
		t.Fatalf("apm deps = %#v, want the foreign entry preserved verbatim", deps)
	}
}

func mcpSyncConfig(servers ...config.McpServer) *config.RootConfig {
	return &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"claude-code", "codex"}},
		Agents:   config.AgentsConfig{McpServers: servers},
	}
}

// claude-code has no native adapter left: APM owns that target's MCP state outright.
func mcpSyncAdapters() func(*App) {
	return WithMcpAdapters([]McpAdapter{&recordingMcpAdapter{id: "codex"}})
}

func TestAgentsSyncAllInstallsMcpOnceWithTheCompleteServerAndTargetSet(t *testing.T) {
	cfg := mcpSyncConfig(
		config.McpServer{Name: "linear", Transport: "http", URL: "https://linear.example/mcp",
			Headers: map[string]string{"X-Tok": "${env:LINEAR_TOKEN}"}},
		config.McpServer{Name: "docs", Transport: "stdio", Command: "npx -y docs-server",
			Env: []string{"DOCS_KEY"}},
	)
	a, mock, _ := newSyncAppEnv(t, cfg, "", map[string]string{"DOCS_KEY": "secret"}, mcpSyncAdapters())

	result, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"install", "-g", "--only", "mcp", "--target", "claude"}
	if calls := apmCalls(mock); len(calls) != 1 || !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("calls = %#v, want a single apm %v", calls, want)
	}
	deps := syncManifestDeps(t, a, "mcp")
	wantDeps := []any{
		map[string]any{"name": "linear", "registry": false, "transport": "http",
			"url": "https://linear.example/mcp", "headers": map[string]any{"X-Tok": "${env:LINEAR_TOKEN}"}},
		map[string]any{"name": "docs", "registry": false, "transport": "stdio",
			"command": "npx", "args": []any{"-y", "docs-server"}, "env": map[string]any{"DOCS_KEY": "${env:DOCS_KEY}"}},
	}
	if !reflect.DeepEqual(deps, wantDeps) {
		t.Fatalf("mcp deps = %#v, want %#v", deps, wantDeps)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("errors = %v", result.Errors)
	}
}

func TestAgentsSyncAllKeepsCodexScopedServersOnTheNativePath(t *testing.T) {
	cfg := mcpSyncConfig(
		config.McpServer{Name: "codexonly", Transport: "stdio", Command: "codex-mcp", Agents: []string{"codex"}},
		config.McpServer{Name: "shared", Transport: "http", URL: "https://shared.example/mcp"},
	)
	codex := &recordingMcpAdapter{id: "codex"}
	a, mock, _ := newSyncApp(t, cfg, "", WithMcpAdapters([]McpAdapter{codex}))

	if _, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{}); err != nil {
		t.Fatal(err)
	}
	want := []string{"install", "-g", "--only", "mcp", "--target", "claude"}
	if calls := apmCalls(mock); len(calls) != 1 || !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("calls = %#v, want apm %v", calls, want)
	}
	deps := syncManifestDeps(t, a, "mcp")
	if len(deps) != 1 || deps[0].(map[string]any)["name"] != "shared" {
		t.Fatalf("mcp deps = %#v, want the codex-scoped server excluded", deps)
	}
	names := make([]string, 0, len(codex.added))
	for _, s := range codex.added {
		names = append(names, s.Name)
	}
	if !reflect.DeepEqual(names, []string{"codexonly", "shared"}) {
		t.Fatalf("native codex installs = %v, want both codex-targeted servers", names)
	}
}

// An unset variable is the agent's problem at runtime, not the sync's: the placeholder deploys either way.
func TestAgentsSyncAllWarnsOnUnsetMcpEnvWithoutRefusing(t *testing.T) {
	cfg := mcpSyncConfig(config.McpServer{
		Name: "docs", Transport: "stdio", Command: "npx -y docs-server",
		Env: []string{"DOCS_KEY"}, Agents: []string{"claude-code"},
	})
	a, mock, _ := newSyncApp(t, cfg, "", mcpSyncAdapters())

	result, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("errors = %v, want the unset variable reported as a warning", result.Errors)
	}
	if !strings.Contains(strings.Join(result.Warnings, " "), "DOCS_KEY") {
		t.Fatalf("warnings = %v, want the unset variable named", result.Warnings)
	}
	want := []string{"install", "-g", "--only", "mcp", "--target", "claude"}
	if calls := apmCalls(mock); len(calls) != 1 || !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("calls = %#v, want the install to run anyway", calls)
	}
	if deps := syncManifestDeps(t, a, "mcp"); len(deps) != 1 {
		t.Fatalf("mcp deps = %#v, want the server declared", deps)
	}
}

// The install must not be able to read the variables it would otherwise resolve into the agent config.
func TestAgentsSyncAllScrubsReferencedVariablesFromTheMcpInstall(t *testing.T) {
	cfg := mcpSyncConfig(config.McpServer{
		Name: "litellm", Transport: "http", URL: "https://${LITELLM_HOST}/mcp",
		Headers:    map[string]string{"x-litellm-api-key": "${LITELLM_API}"},
		EnvLiteral: map[string]string{"MODE": "${env:LITELLM_MODE}", "PLAIN": "verbatim"},
		Env:        []string{"LITELLM_EXTRA"},
		Agents:     []string{"claude-code"},
	})
	a, mock, _ := newSyncAppEnv(t, cfg, "", map[string]string{"LITELLM_API": "secret"}, mcpSyncAdapters())

	if _, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{}); err != nil {
		t.Fatal(err)
	}
	var scrubbed []string
	for _, call := range mock.Calls {
		if call.Name == "apm" && slices.Contains(call.Args, "mcp") {
			scrubbed = call.Env
		}
	}
	want := []string{"LITELLM_API", "LITELLM_EXTRA", "LITELLM_HOST", "LITELLM_MODE"}
	if !reflect.DeepEqual(scrubbed, want) {
		t.Fatalf("scrubbed env = %#v, want every referenced variable %v", scrubbed, want)
	}
	for _, call := range mock.Calls {
		if call.Name == "apm" && slices.Contains(call.Args, "apm") && len(call.Env) != 0 {
			t.Fatalf("package install ran scrubbed: %#v", call.Env)
		}
	}
}

// A failed package batch reverts its own surface; the servers this host declares are a separate deployment.
func TestAgentsSyncAllDeploysMcpAfterAFailedPackageInstall(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"claude-code", "codex"}},
		Agents: config.AgentsConfig{
			Packages: []config.SkillPackage{{Source: "acme/shared", Agents: []string{"claude-code"}}},
			McpServers: []config.McpServer{
				{Name: "linear", Transport: "http", URL: "https://linear.example/mcp"},
				{Name: "codexonly", Transport: "stdio", Command: "codex-mcp", Agents: []string{"codex"}},
			},
		},
	}
	codex := &recordingMcpAdapter{id: "codex"}
	a, mock, _ := newSyncApp(t, cfg, "", WithMcpAdapters([]McpAdapter{codex}))
	mock.Responses = []executor.MockCall{{Stderr: "package install aborted\n", Err: errors.New("exit status 1")}}

	res, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(syncErrorText(res), "package install aborted") {
		t.Fatalf("errors = %v, want the package failure recorded", res.Errors)
	}
	want := [][]string{
		{"install", "-g", "--only", "apm", "--target", "claude"},
		{"install", "-g", "--only", "mcp", "--target", "claude"},
	}
	if calls := apmCalls(mock); !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want the mcp surface reached anyway %v", calls, want)
	}
	names := make([]string, 0, len(codex.added))
	for _, s := range codex.added {
		names = append(names, s.Name)
	}
	if !reflect.DeepEqual(names, []string{"linear", "codexonly"}) {
		t.Fatalf("native codex installs = %v, want the native restore to have run", names)
	}
}

// The scrub is the first defence; this is the second, for a variable that reached APM another way.
func TestAgentsSyncAllRestoresAnExpandedPlaceholderAfterTheMcpInstall(t *testing.T) {
	cfg := mcpSyncConfig(config.McpServer{
		Name: "litellm", Transport: "http", URL: "https://litellm.example/mcp",
		Headers: map[string]string{"x-litellm-api-key": "${LITELLM_API}", "x-plain": "verbatim"},
		Agents:  []string{"claude-code"},
	})
	a, _, _ := newSyncAppEnv(t, cfg, "", map[string]string{"LITELLM_API": "secret"}, mcpSyncAdapters())
	home, err := a.apmDeployRoot()
	if err != nil {
		t.Fatal(err)
	}
	claudeConfig := filepath.Join(home, ".claude.json")
	expanded := &hookedExecutor{before: func(name string, args []string) {
		if name != "apm" || !slices.Contains(args, "mcp") {
			return
		}
		writeLedgerFile(t, claudeConfig, `{"mcpServers":{"litellm":{"url":"https://litellm.example/mcp",`+
			`"headers":{"x-litellm-api-key":"secret","x-plain":"verbatim"}}}}`)
	}}
	a.SetFallbackExecutor(expanded)

	res, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("errors = %v", res.Errors)
	}
	deployed := parseClaudeStateMcpServers([]byte(readLedgerFile(t, claudeConfig)))["litellm"]
	if got := deployed.Headers["x-litellm-api-key"]; got != "${LITELLM_API}" {
		t.Fatalf("deployed header = %q, want the placeholder restored", got)
	}
	if got := deployed.Headers["x-plain"]; got != "verbatim" {
		t.Fatalf("literal header = %q, want it left alone", got)
	}
	if !strings.Contains(strings.Join(res.Warnings, " "), "x-litellm-api-key") {
		t.Fatalf("warnings = %v, want the restored key named", res.Warnings)
	}
	if strings.Contains(strings.Join(res.Warnings, " "), "secret") {
		t.Fatalf("warnings = %v, want the value kept out of the report", res.Warnings)
	}
}

// APM's hermes adapter rebuilds a managed entry from a whitelist and writes nothing on removal, so hermes
// stays on its native adapter and never appears in the --target set.
func TestAgentsSyncAllKeepsHermesScopedServersOnTheNativePath(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"claude-code", "hermes-agent"}},
		Agents: config.AgentsConfig{McpServers: []config.McpServer{
			{Name: "grafana", Transport: "http", URL: "https://grafana.example/mcp", Agents: []string{"hermes-agent"}},
			{Name: "shared", Transport: "http", URL: "https://shared.example/mcp"},
		}},
	}
	hermes := &recordingMcpAdapter{id: "hermes-agent"}
	a, mock, _ := newSyncApp(t, cfg, "", WithMcpAdapters([]McpAdapter{hermes}))

	if _, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{}); err != nil {
		t.Fatal(err)
	}
	want := []string{"install", "-g", "--only", "mcp", "--target", "claude"}
	if calls := apmCalls(mock); len(calls) != 1 || !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("calls = %#v, want apm %v", calls, want)
	}
	deps := syncManifestDeps(t, a, "mcp")
	if len(deps) != 1 || deps[0].(map[string]any)["name"] != "shared" {
		t.Fatalf("mcp deps = %#v, want the hermes-scoped server excluded", deps)
	}
	names := make([]string, 0, len(hermes.added))
	for _, s := range hermes.added {
		names = append(names, s.Name)
	}
	if !reflect.DeepEqual(names, []string{"grafana", "shared"}) {
		t.Fatalf("native hermes installs = %v, want both hermes-targeted servers", names)
	}
}

func TestAgentsSyncAllWarnsThatDeclaredMcpScopingIsNotEnforced(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"claude-code", "gemini-cli"}},
		Agents: config.AgentsConfig{McpServers: []config.McpServer{
			{Name: "linear", Transport: "http", URL: "https://linear.example/mcp", Agents: []string{"claude-code"}},
		}},
	}
	a, mock, _ := newSyncApp(t, cfg, "", WithMcpAdapters([]McpAdapter{}))

	result, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"install", "-g", "--only", "mcp", "--target", "claude,gemini"}
	if calls := apmCalls(mock); len(calls) != 1 || !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("calls = %#v, want apm %v", calls, want)
	}
	if !strings.Contains(strings.Join(result.Warnings, " "), "gemini-cli as well") {
		t.Fatalf("warnings = %v, want the declared-vs-deployed divergence surfaced", result.Warnings)
	}
}

func TestAgentsSyncAllSkipsMcpWhenNoTargetIsMcpCapable(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"grok"}},
		Agents: config.AgentsConfig{McpServers: []config.McpServer{
			{Name: "linear", Transport: "http", URL: "https://linear.example/mcp"},
		}},
	}
	grok := &recordingMcpAdapter{id: "grok"}
	a, mock, _ := newSyncApp(t, cfg, "", WithMcpAdapters([]McpAdapter{grok}))

	result, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if calls := apmCalls(mock); len(calls) != 0 {
		t.Fatalf("calls = %#v, want none: grok-build cannot accept MCP config", calls)
	}
	if len(grok.added) != 1 || grok.added[0].Name != "linear" {
		t.Fatalf("native grok installs = %#v, want the server on the native path", grok.added)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("errors = %v", result.Errors)
	}
}

func TestAgentsUpdateAllWithoutManifestReturnsGuidance(t *testing.T) {
	a, mock, _ := newSyncApp(t, &config.RootConfig{Version: config.CurrentVersion}, "")
	_, _, err := a.AgentsUpdateAll(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "omni agents sync") {
		t.Fatalf("error lacks guidance: %v", err)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("apm invoked despite missing manifest: %#v", mock.Calls)
	}
}

// An unscoped `apm update` would refresh the packages into codex and register the claude-only server there too.
func TestAgentsUpdateAllRefreshesEachSurfaceInItsOwnTargets(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"claude-code", "codex"}},
		Agents: config.AgentsConfig{
			Packages:   []config.SkillPackage{{Source: "acme/shared", Agents: []string{"codex"}}},
			McpServers: []config.McpServer{{Name: "linear", Transport: "http", URL: "https://linear.example/mcp", Agents: []string{"claude-code"}}},
		},
	}
	a, mock, _ := newSyncApp(t, cfg, emptySyncManifest, WithMcpAdapters([]McpAdapter{}))

	_, _, err := a.AgentsUpdateAll(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"install", "-g", "--only", "apm", "--update", "--target", "codex"},
		{"install", "-g", "--only", "mcp", "--update", "--target", "claude"},
	}
	if calls := apmCalls(mock); !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

// The unscoped `apm update` reached the mcp surface with the variable set and no backstop behind it.
func TestAgentsUpdateAllScrubsMcpVariablesAndRestoresPlaceholders(t *testing.T) {
	cfg := mcpSyncConfig(config.McpServer{
		Name: "litellm", Transport: "http", URL: "https://litellm.example/mcp",
		Headers: map[string]string{"x-litellm-api-key": "${LITELLM_API}"},
		Agents:  []string{"claude-code"},
	})
	a, _, _ := newSyncAppEnv(t, cfg, emptySyncManifest, map[string]string{"LITELLM_API": "secret"}, mcpSyncAdapters())
	home, err := a.apmDeployRoot()
	if err != nil {
		t.Fatal(err)
	}
	claudeConfig := filepath.Join(home, ".claude.json")
	expanded := &hookedExecutor{before: func(name string, args []string) {
		if name != "apm" || !slices.Contains(args, "mcp") {
			return
		}
		writeLedgerFile(t, claudeConfig, `{"mcpServers":{"litellm":{"url":"https://litellm.example/mcp",`+
			`"headers":{"x-litellm-api-key":"secret"}}}}`)
	}}
	a.SetFallbackExecutor(expanded)

	_, warnings, err := a.AgentsUpdateAll(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	mcpCall := expanded.Calls[len(expanded.Calls)-1]
	if !slices.Contains(mcpCall.Args, "mcp") || !slices.Contains(mcpCall.Env, "LITELLM_API") {
		t.Fatalf("mcp update did not scrub LITELLM_API: %v env=%v", mcpCall.Args, mcpCall.Env)
	}
	deployed := parseClaudeStateMcpServers([]byte(readLedgerFile(t, claudeConfig)))["litellm"]
	if got := deployed.Headers["x-litellm-api-key"]; got != "${LITELLM_API}" {
		t.Fatalf("deployed header = %q, want the placeholder restored", got)
	}
	if !strings.Contains(strings.Join(warnings, " "), "x-litellm-api-key") {
		t.Fatalf("warnings = %v, want the restored key named", warnings)
	}
}

// The claude adapter is gone, so nothing but the APM target map can put claude in the install.
func TestAgentsSyncAllTargetsAnAPMDrivenAgentThatHasNoNativeAdapter(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"claude-code"}},
		Agents: config.AgentsConfig{McpServers: []config.McpServer{
			{Name: "linear", Transport: "http", URL: "https://linear.example/mcp"},
		}},
	}
	a, mock, _ := newSyncApp(t, cfg, "", WithMcpAdapters([]McpAdapter{}))

	if _, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{}); err != nil {
		t.Fatal(err)
	}
	want := []string{"install", "-g", "--only", "mcp", "--target", "claude"}
	if calls := apmCalls(mock); len(calls) != 1 || !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("calls = %#v, want apm %v", calls, want)
	}
	if deps := syncManifestDeps(t, a, "mcp"); len(deps) != 1 {
		t.Fatalf("mcp deps = %#v, want the server generated for the adapterless target", deps)
	}
}

func TestAgentsSyncAllWarnsThatAnMcpIncapableAgentGetsNothing(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"claude-code", "grok"}},
		Agents: config.AgentsConfig{McpServers: []config.McpServer{
			{Name: "linear", Transport: "http", URL: "https://linear.example/mcp"},
		}},
	}
	a, mock, _ := newSyncApp(t, cfg, "", WithMcpAdapters([]McpAdapter{}))

	result, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"install", "-g", "--only", "mcp", "--target", "claude"}
	if calls := apmCalls(mock); len(calls) != 1 || !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("calls = %#v, want grok kept out of the target set", calls)
	}
	if !strings.Contains(strings.Join(result.Warnings, " "), "grok cannot receive mcp servers") {
		t.Fatalf("warnings = %v, want the lost target surfaced", result.Warnings)
	}
}

// A non-canonical config source resolves to the same manifest entry the generator writes, so keying the
// managed set off the raw string would append a second copy on every sync and call the first one foreign.
func TestAgentsSyncAllKeepsURLFormSourcesToASingleManifestEntry(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"codex"}},
		Agents: config.AgentsConfig{Packages: []config.SkillPackage{
			{Source: "https://github.com/acme/skills.git", Agents: []string{"codex"}},
		}},
	}
	a, _, _ := newSyncApp(t, cfg, "")

	for range 2 {
		if _, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	deps := syncManifestDeps(t, a, "apm")
	want := []any{map[string]any{"git": "acme/skills", "targets": []any{"codex"}}}
	if !reflect.DeepEqual(deps, want) {
		t.Fatalf("apm deps = %#v, want %#v", deps, want)
	}
	foreign, err := a.ForeignAPMEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(foreign.Packages) != 0 {
		t.Fatalf("foreign = %#v, want the config-declared package recognised as omni's", foreign.Packages)
	}
}

// Dropping the last server leaves nothing to render, but APM only prunes what it is told about: without an
// install the deployed entry survives on every target forever.
func TestAgentsSyncAllReconcilesMcpAfterTheLastServerIsRemoved(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"claude-code"}},
	}
	manifest := "name: test\nversion: 1.0.0\ndependencies:\n  mcp:\n  - name: linear\n    registry: false\n    transport: http\n    url: https://linear.example/mcp\n"
	a, mock, _ := newSyncApp(t, cfg, manifest)
	if err := writeAPMOwnedIdentities(filepath.Join(a.lookupEnv("HOME"), ".apm", "apm.yml"),
		appliedOwned(nil, []string{"linear"})); err != nil {
		t.Fatal(err)
	}

	if _, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{}); err != nil {
		t.Fatal(err)
	}
	want := []string{"install", "-g", "--only", "mcp", "--target", "claude"}
	if calls := apmCalls(mock); len(calls) != 1 || !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("calls = %#v, want the prune reconciled through apm %v", calls, want)
	}
	if deps := syncManifestDeps(t, a, "mcp"); len(deps) != 0 {
		t.Fatalf("mcp deps = %#v, want the retired server dropped", deps)
	}
}

// The package surface needs the same prune reconciliation the mcp one gets: dropping the last package leaves
// nothing to render, and without an install the deployed files survive on every target forever.
func TestAgentsSyncAllReconcilesPackagesAfterTheLastPackageIsRemoved(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"codex"}},
	}
	manifest := "name: test\nversion: 1.0.0\ndependencies:\n  apm:\n    - git: acme/dropped\n      targets: [codex]\n"
	a, mock, _ := newSyncApp(t, cfg, manifest)
	if err := writeAPMOwnedIdentities(filepath.Join(a.lookupEnv("HOME"), ".apm", "apm.yml"),
		appliedOwned([]string{"acme/dropped"}, nil)); err != nil {
		t.Fatal(err)
	}

	if _, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{}); err != nil {
		t.Fatal(err)
	}
	want := []string{"install", "-g", "--only", "apm", "--target", "codex"}
	if calls := apmCalls(mock); len(calls) != 1 || !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("calls = %#v, want the prune reconciled through apm %v", calls, want)
	}
	if deps := syncManifestDeps(t, a, "apm"); len(deps) != 0 {
		t.Fatalf("apm deps = %#v, want the retired package dropped", deps)
	}
}

// A package whose install failed is one omni rendered and never applied. Recording only what installed leaves
// the config dropping it with an entry no record claims, which reads as foreign and is re-attempted forever.
func TestAgentsSyncAllRetiresAPackageWhoseInstallNeverSucceeded(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"codex"}},
		Agents: config.AgentsConfig{Packages: []config.SkillPackage{
			{Source: "acme/broken", Agents: []string{"codex"}},
		}},
	}
	manifest := "name: test\nversion: 1.0.0\ndependencies:\n  apm:\n    - someone/else\n"
	a, _, configPath := newSyncApp(t, cfg, manifest)
	a.SetFallbackExecutor(&executor.MockExecutor{Responses: []executor.MockCall{
		{Stderr: "install aborted\n", Err: errors.New("exit status 1")},
	}})

	res, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(syncErrorText(res), "install aborted") {
		t.Fatalf("errors = %v, want the failed install recorded", res.Errors)
	}

	cfg.Agents.Packages = nil
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	retry := &executor.MockExecutor{}
	a.SetFallbackExecutor(retry)
	res, err = a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := AgentsSyncAllFailure(res); err != nil {
		t.Fatalf("re-sync = %v (%v), want the undeclared entry retired instead of re-attempted", err, res.Errors)
	}
	if calls := apmCalls(retry); len(calls) != 0 {
		t.Fatalf("calls = %#v, want nothing installed for an entry that was never deployed", calls)
	}
	deps := syncManifestDeps(t, a, "apm")
	if !reflect.DeepEqual(deps, []any{"someone/else"}) {
		t.Fatalf("apm deps = %#v, want the failed entry retired and the foreign one kept", deps)
	}
	foreign, err := a.ForeignAPMEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(foreign.Packages) != 1 || foreign.Packages[0].Source != "someone/else" {
		t.Fatalf("foreign = %#v, want only the genuinely foreign entry offered for import", foreign.Packages)
	}
}

// An entry the previous generation owned is omni's to retire, not someone else's to preserve; without the
// ownership record a hand-deleted config entry stays deployed and reappears as a phantom import offer.
func TestAgentsSyncAllRetiresEntriesTheConfigNoLongerDeclares(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"codex"}},
		Agents: config.AgentsConfig{Packages: []config.SkillPackage{
			{Source: "acme/kept", Agents: []string{"codex"}},
			{Source: "acme/dropped", Agents: []string{"codex"}},
		}},
	}
	manifest := "name: test\nversion: 1.0.0\ndependencies:\n  apm:\n    - someone/else\n"
	a, _, configPath := newSyncApp(t, cfg, manifest)
	if _, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{}); err != nil {
		t.Fatal(err)
	}

	cfg.Agents.Packages = cfg.Agents.Packages[:1]
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{}); err != nil {
		t.Fatal(err)
	}

	var sources []string
	for _, dep := range syncManifestDeps(t, a, "apm") {
		switch entry := dep.(type) {
		case string:
			sources = append(sources, entry)
		case map[string]any:
			sources = append(sources, entry["git"].(string))
		}
	}
	if !reflect.DeepEqual(sources, []string{"someone/else", "acme/kept"}) {
		t.Fatalf("apm deps = %v, want the hand-deleted entry retired and the foreign one kept", sources)
	}
	foreign, err := a.ForeignAPMEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(foreign.Packages) != 1 || foreign.Packages[0].Source != "someone/else" {
		t.Fatalf("foreign = %#v, want only the genuinely foreign entry offered for import", foreign.Packages)
	}
}

// The manifest APM would read is the one still on disk, so previewing an install against it reports on a
// config delta this sync has not written.
func TestAgentsSyncAllDryRunReportsTheDeltaInsteadOfPreviewingAStaleManifest(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"codex"}},
		Agents: config.AgentsConfig{Packages: []config.SkillPackage{
			{Source: "acme/added", Agents: []string{"codex"}},
		}},
	}
	manifest := "name: test\nversion: 1.0.0\ndependencies:\n  apm:\n    - git: acme/retired\n"
	a, mock, _ := newSyncApp(t, cfg, manifest)
	if err := writeAPMOwnedIdentities(filepath.Join(a.lookupEnv("HOME"), ".apm", "apm.yml"),
		appliedOwned([]string{"acme/retired"}, nil)); err != nil {
		t.Fatal(err)
	}

	result, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if calls := apmCalls(mock); len(calls) != 0 {
		t.Fatalf("calls = %#v, want no preview of the stale manifest", calls)
	}
	plan := strings.Join(result.Plan, "\n")
	for _, want := range []string{"would add acme/added", "would remove acme/retired", "targets codex"} {
		if !strings.Contains(plan, want) {
			t.Fatalf("plan = %v, want it to mention %q", result.Plan, want)
		}
	}
	if result.InstalledPackages != 0 {
		t.Fatalf("result = %#v, want no install counted for a dry run", result)
	}
	if !strings.Contains(strings.Join(result.Warnings, " "), "APM was not consulted") {
		t.Fatalf("warnings = %v", result.Warnings)
	}
	if !bytes.Equal(readSyncManifest(t, a), []byte(manifest)) {
		t.Fatal("dry run wrote the manifest")
	}
}

func TestAgentsSyncAllSkipsIgnoredPackagesAndServers(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"claude-code"}},
		Agents: config.AgentsConfig{
			Packages:   []config.SkillPackage{{Source: "acme/kept"}, {Source: "acme/ignored"}},
			McpServers: []config.McpServer{{Name: "linear", Transport: "http", URL: "https://linear.example/mcp"}, {Name: "muted", Transport: "http", URL: "https://muted.example/mcp"}},
			Ignore:     config.AgentsIgnore{Skills: []string{"ignored"}, McpServers: []string{"muted"}},
		},
	}
	a, _, _ := newSyncApp(t, cfg, "")

	if _, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{}); err != nil {
		t.Fatal(err)
	}
	deps := syncManifestDeps(t, a, "apm")
	if len(deps) != 1 || deps[0].(map[string]any)["git"] != "acme/kept" {
		t.Fatalf("apm deps = %#v, want the ignored package left out", deps)
	}
	mcp := syncManifestDeps(t, a, "mcp")
	if len(mcp) != 1 || mcp[0].(map[string]any)["name"] != "linear" {
		t.Fatalf("mcp deps = %#v, want the ignored server left out", mcp)
	}
}

// A foreign entry keeps the manifest non-empty, so the prune of the last declared package must still be
// scoped from what this host selects rather than skipped for want of a declared target.
func TestAgentsSyncAllPrunesAlongsideAForeignEntry(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"codex"}},
	}
	manifest := "name: test\nversion: 1.0.0\ndependencies:\n  apm:\n    - git: someone/else\n    - git: acme/dropped\n      targets:\n        - codex\n"
	a, mock, _ := newSyncApp(t, cfg, manifest)
	home, err := a.apmDeployRoot()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".apm", apmOwnedSidecarName),
		[]byte(`{"packages":["acme/dropped"],"mcp":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"install", "-g", "--only", "apm", "--target", "codex"}
	if calls := apmCalls(mock); len(calls) != 1 || !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("calls = %#v, want one %v", calls, want)
	}
	if joined := strings.Join(res.Warnings, "\n"); strings.Contains(joined, "no APM target") {
		t.Fatalf("warnings = %q, want the prune scoped instead of skipped", joined)
	}
	deps := syncManifestDeps(t, a, "apm")
	if len(deps) != 1 {
		t.Fatalf("manifest apm deps = %#v, want only the foreign entry", deps)
	}
}
