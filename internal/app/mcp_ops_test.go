package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/agent"
	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

type stubMcpAdapter struct {
	id           string
	available    bool
	addErr       error
	addErrors    []error
	addFunc      func(context.Context, config.McpServer) error
	removeErr    error
	removeHook   func()
	listErr      error
	listed       []app.InstalledMcpServer
	addedServers []config.McpServer
	removedNames []string
	listCalls    int
}

type trackingInPlaceMcpAdapter struct {
	app.McpAdapter
	updater agent.McpInPlaceUpdater
	removes int
}

func (a *trackingInPlaceMcpAdapter) UpdateMcpServer(ctx context.Context, server config.McpServer) error {
	return a.updater.UpdateMcpServer(ctx, server)
}

func (a *trackingInPlaceMcpAdapter) Remove(ctx context.Context, name string) error {
	a.removes++
	return a.McpAdapter.Remove(ctx, name)
}

type recordingMcpExecutor struct {
	names []string
	args  [][]string
	err   error
}

func (e *recordingMcpExecutor) Run(_ context.Context, name string, args ...string) (string, string, error) {
	e.names = append(e.names, name)
	e.args = append(e.args, append([]string(nil), args...))
	return "", "", e.err
}

func (s *stubMcpAdapter) ID() string      { return s.id }
func (s *stubMcpAdapter) Available() bool { return s.available }
func (s *stubMcpAdapter) List(_ context.Context) ([]app.InstalledMcpServer, error) {
	s.listCalls++
	if s.listErr != nil {
		return nil, s.listErr
	}
	return append([]app.InstalledMcpServer(nil), s.listed...), nil
}
func (s *stubMcpAdapter) Add(ctx context.Context, srv config.McpServer) error {
	s.addedServers = append(s.addedServers, srv)
	if s.addFunc != nil {
		return s.addFunc(ctx, srv)
	}
	if len(s.addErrors) > 0 {
		err := s.addErrors[0]
		s.addErrors = s.addErrors[1:]
		return err
	}
	return s.addErr
}
func (s *stubMcpAdapter) Remove(_ context.Context, name string) error {
	s.removedNames = append(s.removedNames, name)
	if s.removeHook != nil {
		s.removeHook()
	}
	return s.removeErr
}

// The home override keeps agent detection and the generated APM manifest off the developer's real host.
func newMcpTestApp(t *testing.T, agents config.AgentsConfig, opts ...func(*app.App)) *app.App {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	root := config.RootConfig{
		Version: config.CurrentVersion,
		Agents:  agents,
	}
	if err := config.Save(cfgPath, &root); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	all := append([]func(*app.App){app.WithEnvLookup(func(name string) string {
		if name == "HOME" {
			return home
		}
		return os.Getenv(name)
	})}, opts...)
	a := app.New(cfgPath, all...)
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func loadMcpTestConfig(t *testing.T, a *app.App) *config.RootConfig {
	t.Helper()
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestProductionMcpAdaptersUseFallbackExecutorSetAfterNew(t *testing.T) {
	binDir := t.TempDir()
	writeTestExecutable(t, binDir, "codex")
	t.Setenv("PATH", binDir)

	a := newMcpTestApp(t, config.AgentsConfig{})
	exec := &recordingMcpExecutor{}
	a.SetFallbackExecutor(exec)

	server := config.McpServer{
		Name:      "demo",
		Transport: "stdio",
		Command:   "demo-server",
		Agents:    []string{"codex"},
	}
	res, err := a.AddMcpServer(context.Background(), server)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("AddMcpServer errors = %v", res.Errors)
	}
	if len(exec.names) == 0 {
		t.Fatal("fallback executor was never called")
	}
	added := false
	for i, name := range exec.names {
		if name != "codex" {
			t.Fatalf("fallback executor calls = %v %v, want codex only", exec.names, exec.args)
		}
		if len(exec.args[i]) >= 2 && exec.args[i][0] == "mcp" && exec.args[i][1] == "add" {
			added = true
		}
	}
	if !added {
		t.Fatalf("fallback executor calls = %v %v, want an mcp add", exec.names, exec.args)
	}
}

func TestProductionAdaptersSkipUnavailableAgents(t *testing.T) {
	home := t.TempDir()
	binDir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", binDir)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("GROK_HOME", "")

	a := newMcpTestApp(t, config.AgentsConfig{})
	exec := &recordingMcpExecutor{err: errors.New("unavailable agent invoked")}
	a.SetFallbackExecutor(exec)

	if _, _, err := a.McpServerRows(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.PluginRows(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.MarketplaceRows(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(exec.names) != 0 {
		t.Fatalf("fallback executor calls = %v %v, want none", exec.names, exec.args)
	}
}

func TestRestoreMcpServers_InstallsTargetedServer(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{id: "codex", available: true}
	srv := config.McpServer{Name: "linear", Transport: "stdio", Command: "npx x", Agents: []string{"codex"}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	res, err := a.RestoreMcpServers(context.Background(), app.RestoreMcpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.addedServers) != 1 {
		t.Fatalf("expected 1 Add call, got %d", len(stub.addedServers))
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}
}

func TestAddMcpServer_ValidatesBeforeAdapterSideEffects(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{id: "codex", available: true}
	a := newMcpTestApp(t, config.AgentsConfig{}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	_, err := a.AddMcpServer(context.Background(), config.McpServer{
		Name: "invalid", Transport: "http", URL: "https://mcp.example.com",
		Headers: map[string]string{"Bad Name": "value"}, Agents: []string{"codex"},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid HTTP header name") {
		t.Fatalf("error = %v, want invalid header name", err)
	}
	if stub.listCalls != 0 || len(stub.addedServers) != 0 || len(stub.removedNames) != 0 {
		t.Fatalf("invalid server touched adapter: list=%d add=%v remove=%v", stub.listCalls, stub.addedServers, stub.removedNames)
	}
	if len(loadMcpTestConfig(t, a).Agents.McpServers) != 0 {
		t.Fatal("invalid server mutated manifest")
	}
}

func TestRestoreMcpServers_SkipsNonTargetedAgent(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{id: "hermes-agent", available: true}
	srv := config.McpServer{Name: "linear", Transport: "stdio", Command: "npx x", Agents: []string{"codex"}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	_, err := a.RestoreMcpServers(context.Background(), app.RestoreMcpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.addedServers) != 0 {
		t.Fatal("hermes-agent should be skipped when not in server's Agents list")
	}
}

func TestRestoreMcpServers_EmptyAgentsMeansAll(t *testing.T) {
	t.Parallel()
	codex := &stubMcpAdapter{id: "codex", available: true, listed: []app.InstalledMcpServer{{Name: "x"}}}
	hermes := &stubMcpAdapter{id: "hermes-agent", available: true}
	srv := config.McpServer{Name: "grafana", Transport: "http", URL: "https://mcp.example.com", Agents: nil}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}}, app.WithMcpAdapters([]app.McpAdapter{codex, hermes}))
	_, err := a.RestoreMcpServers(context.Background(), app.RestoreMcpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(codex.addedServers) != 1 {
		t.Fatalf("expected codex to receive Add, got %d calls", len(codex.addedServers))
	}
	if len(hermes.addedServers) != 1 {
		t.Fatalf("expected hermes-agent to receive Add, got %d calls", len(hermes.addedServers))
	}
}

func TestRestoreMcpServers_SkipsUnavailableAgent(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{id: "codex", available: false}
	srv := config.McpServer{Name: "x", Transport: "stdio", Command: "npx x"}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	res, err := a.RestoreMcpServers(context.Background(), app.RestoreMcpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.addedServers) != 0 {
		t.Fatal("unavailable agent should be skipped")
	}
	if len(res.Warnings) == 0 {
		t.Fatal("expected warning for unavailable agent")
	}
}

func TestRestoreMcpServers_ContinuesAfterServerInstallFailure(t *testing.T) {
	t.Parallel()
	addErr := errors.New("env var MISSING not set")
	stub := &stubMcpAdapter{id: "codex", available: true, addErrors: []error{addErr, nil}}
	servers := []config.McpServer{
		{Name: "fails", Transport: "stdio", Command: "npx fails"},
		{Name: "succeeds", Transport: "stdio", Command: "npx succeeds"},
	}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: servers}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	res, err := a.RestoreMcpServers(context.Background(), app.RestoreMcpOptions{})
	if err != nil {
		t.Fatalf("restore must not return top-level error for per-server failure: %v", err)
	}
	if len(res.Errors) != 1 || res.Errors[0].AgentID != "codex" || res.Errors[0].ServerName != "fails" || !errors.Is(res.Errors[0].Err, addErr) {
		t.Fatalf("Errors = %v, want codex/fails: %v", res.Errors, addErr)
	}
	if !slices.Equal(res.Installed, []string{"codex/succeeds"}) {
		t.Fatalf("Installed = %v, want [codex/succeeds]", res.Installed)
	}
	if len(stub.addedServers) != 2 || stub.addedServers[0].Name != "fails" || stub.addedServers[1].Name != "succeeds" {
		t.Fatalf("Add calls = %v, want [fails succeeds]", stub.addedServers)
	}
}

func TestRestoreMcpServers_SkipsAlreadyInstalled(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{id: "codex", available: true, listed: []app.InstalledMcpServer{{Name: "linear", Transport: "stdio", Command: "npx x"}}}
	srv := config.McpServer{Name: "linear", Transport: "stdio", Command: "npx x", Agents: []string{"codex"}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	res, err := a.RestoreMcpServers(context.Background(), app.RestoreMcpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.addedServers) != 0 {
		t.Fatalf("expected no Add call for already-present server, got %v", stub.addedServers)
	}
	if len(res.AlreadyInstalled) != 1 || res.AlreadyInstalled[0] != "codex/linear" {
		t.Fatalf("expected already-installed entry, got %v", res.AlreadyInstalled)
	}
	if len(res.Installed) != 0 {
		t.Fatalf("expected no installed entries, got %v", res.Installed)
	}
}

func TestRestoreMcpServers_UpdatesChangedHeaders(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{
		id:        "hermes-agent",
		available: true,
		listed: []app.InstalledMcpServer{{
			Name: "grafana", Transport: "http", URL: "https://mcp.example.com",
			Headers: map[string]string{"X-Key": "old"}, HeadersKnown: true, EnvLiteral: map[string]string{"MODE": "old"},
		}},
	}
	srv := config.McpServer{
		Name: "grafana", Transport: "http", URL: "https://mcp.example.com",
		Headers: map[string]string{"X-Key": "new"}, Agents: []string{"hermes-agent"},
	}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	res, err := a.RestoreMcpServers(context.Background(), app.RestoreMcpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.removedNames) != 1 || stub.removedNames[0] != "grafana" || len(stub.addedServers) != 1 {
		t.Fatalf("remove/add calls = %v/%v, want one update", stub.removedNames, stub.addedServers)
	}
	if got := stub.addedServers[0].Headers["X-Key"]; got != "new" {
		t.Fatalf("updated header = %q, want new", got)
	}
	if len(res.Updated) != 1 || res.Updated[0] != "hermes-agent/grafana" {
		t.Fatalf("Updated = %v, want [hermes-agent/grafana]", res.Updated)
	}
}

func TestRestoreMcpServers_HermesHeaderUpdatePreservesExtendedConfig(t *testing.T) {
	hermesHome := t.TempDir()
	t.Setenv("HERMES_HOME", hermesHome)
	binDir := t.TempDir()
	writeTestExecutable(t, binDir, "hermes")
	t.Setenv("PATH", binDir)
	path := filepath.Join(hermesHome, "config.yaml")
	original := []byte(`# root comment
mcp_servers:
  grafana:
    url: https://mcp.example.com
    headers:
      X-Key: old
    enabled: false # enabled comment
    timeout: 42
    tools:
      include: [query]
    custom: keep
`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	realAdapter := agent.NewHermesMcpAdapter(nil, os.LookupEnv)
	tracked := &trackingInPlaceMcpAdapter{
		McpAdapter: realAdapter,
		updater:    realAdapter.(agent.McpInPlaceUpdater),
	}
	desired := config.McpServer{
		Name: "grafana", Transport: "http", URL: "https://mcp.example.com",
		Headers: map[string]string{"X-Key": "new"}, Agents: []string{"hermes-agent"},
	}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{desired}}, app.WithMcpAdapters([]app.McpAdapter{tracked}))
	res, err := a.RestoreMcpServers(context.Background(), app.RestoreMcpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if tracked.removes != 0 {
		t.Fatalf("Remove calls = %d, want 0", tracked.removes)
	}
	if len(res.Errors) != 0 || !slices.Equal(res.Updated, []string{"hermes-agent/grafana"}) {
		t.Fatalf("Restore result = %+v", res)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"# root comment", "# enabled comment", "X-Key: new", "enabled: false", "timeout: 42", "include: [query]", "custom: keep"} {
		if !strings.Contains(text, want) {
			t.Errorf("config missing %q:\n%s", want, text)
		}
	}
}

func TestRestoreMcpServers_DryRunReportsChangedHeaders(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{
		id: "hermes-agent", available: true,
		listed: []app.InstalledMcpServer{{
			Name: "grafana", Transport: "http", URL: "https://mcp.example.com",
			Headers: map[string]string{"X-Key": "old"}, HeadersKnown: true,
		}},
	}
	srv := config.McpServer{Name: "grafana", Transport: "http", URL: "https://mcp.example.com", Headers: map[string]string{"X-Key": "new"}, Agents: []string{"hermes-agent"}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	res, err := a.RestoreMcpServers(context.Background(), app.RestoreMcpOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.removedNames)+len(stub.addedServers) != 0 {
		t.Fatalf("dry run changed adapter: remove=%v add=%v", stub.removedNames, stub.addedServers)
	}
	if len(res.WouldUpdate) != 1 || res.WouldUpdate[0] != "hermes-agent/grafana" {
		t.Fatalf("WouldUpdate = %v, want [hermes-agent/grafana]", res.WouldUpdate)
	}
}

func TestRestoreMcpServers_HeaderUpdateRemoveFailureDoesNotAdd(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{
		id: "hermes-agent", available: true, removeErr: errors.New("remove failed"),
		listed: []app.InstalledMcpServer{{
			Name: "grafana", Transport: "http", URL: "https://mcp.example.com",
			Headers: map[string]string{"X-Key": "old"}, HeadersKnown: true,
		}},
	}
	srv := config.McpServer{Name: "grafana", Transport: "http", URL: "https://mcp.example.com", Headers: map[string]string{"X-Key": "new"}, Agents: []string{"hermes-agent"}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	res, err := a.RestoreMcpServers(context.Background(), app.RestoreMcpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.addedServers) != 0 {
		t.Fatalf("Add called after failed remove: %v", stub.addedServers)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("Errors = %v, want one remove failure", res.Errors)
	}
}

func TestRestoreMcpServers_HeaderUpdateAddFailureRestoresPrevious(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{
		id: "hermes-agent", available: true, addErrors: []error{errors.New("add failed"), nil},
		listed: []app.InstalledMcpServer{{
			Name: "grafana", Transport: "http", URL: "https://mcp.example.com",
			Headers: map[string]string{"X-Key": "old"}, HeadersKnown: true, EnvLiteral: map[string]string{"MODE": "old"},
		}},
	}
	srv := config.McpServer{Name: "grafana", Transport: "http", URL: "https://mcp.example.com", Headers: map[string]string{"X-Key": "new"}, Agents: []string{"hermes-agent"}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	res, err := a.RestoreMcpServers(context.Background(), app.RestoreMcpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.addedServers) != 2 || stub.addedServers[1].Headers["X-Key"] != "old" || stub.addedServers[1].EnvLiteral["MODE"] != "old" {
		t.Fatalf("Add calls = %v, want failed desired then previous registration", stub.addedServers)
	}
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0].Error(), "previous registration restored") {
		t.Fatalf("Errors = %v, want restored-previous error", res.Errors)
	}
}

func TestRestoreMcpServers_HeaderUpdateRollbackSurvivesRequestCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	addCalls := 0
	stub := &stubMcpAdapter{
		id: "hermes-agent", available: true,
		listed: []app.InstalledMcpServer{{
			Name: "grafana", Transport: "http", URL: "https://mcp.example.com",
			Headers: map[string]string{"X-Key": "old"}, HeadersKnown: true,
		}},
		removeHook: cancel,
		addFunc: func(addCtx context.Context, _ config.McpServer) error {
			addCalls++
			if addCalls == 1 {
				return errors.New("add failed")
			}
			if err := addCtx.Err(); err != nil {
				t.Fatalf("rollback inherited cancelled request context: %v", err)
			}
			return nil
		},
	}
	srv := config.McpServer{
		Name: "grafana", Transport: "http", URL: "https://mcp.example.com",
		Headers: map[string]string{"X-Key": "new"}, Agents: []string{"hermes-agent"},
	}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	res, err := a.RestoreMcpServers(ctx, app.RestoreMcpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if addCalls != 2 || len(stub.addedServers) != 2 || stub.addedServers[1].Headers["X-Key"] != "old" {
		t.Fatalf("Add calls = %v, want failed desired then uncancelled rollback", stub.addedServers)
	}
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0].Error(), "previous registration restored") {
		t.Fatalf("Errors = %v, want restored-previous error", res.Errors)
	}
}

func TestRestoreMcpServers_SkipsShadowedByPlugin(t *testing.T) {
	t.Parallel()
	mcpStub := &stubMcpAdapter{id: "codex", available: true}
	pluginStub := &stubPluginAdapter{
		id:            "codex",
		available:     true,
		listedPlugins: []app.InstalledPlugin{{Name: "context-mode", Marketplace: "some-marketplace"}},
	}
	srv := config.McpServer{Name: "context-mode", Transport: "stdio", Command: "npx x", Agents: []string{"codex"}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}},
		app.WithMcpAdapters([]app.McpAdapter{mcpStub}),
		app.WithPluginAdapters([]app.PluginAdapter{pluginStub}),
	)
	res, err := a.RestoreMcpServers(context.Background(), app.RestoreMcpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(mcpStub.addedServers) != 0 {
		t.Fatalf("expected no Add call for plugin-shadowed server, got %v", mcpStub.addedServers)
	}
	if len(res.ShadowedByPlugin) != 1 || res.ShadowedByPlugin[0] != "codex/context-mode" {
		t.Fatalf("expected shadowed entry, got %v", res.ShadowedByPlugin)
	}
	if len(res.Installed) != 0 || len(res.AlreadyInstalled) != 0 || len(res.Errors) != 0 {
		t.Fatalf("expected only ShadowedByPlugin populated, got %+v", res)
	}
}

func TestRestoreMcpServers_ListErrorWarnsAndAttemptsInstall(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{id: "codex", available: true, listErr: errors.New("list failed")}
	srv := config.McpServer{Name: "linear", Transport: "stdio", Command: "npx x", Agents: []string{"codex"}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	res, err := a.RestoreMcpServers(context.Background(), app.RestoreMcpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 {
		t.Fatalf("expected a warning for the list failure, got %v", res.Warnings)
	}
	if len(stub.addedServers) != 1 || stub.addedServers[0].Name != "linear" {
		t.Fatalf("expected install attempted despite list failure, got %v", stub.addedServers)
	}
	if len(res.Installed) != 1 {
		t.Fatalf("expected 1 installed entry, got %v", res.Installed)
	}
}

func TestRestoreMcpServers_DryRun(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{id: "codex", available: true}
	srv := config.McpServer{Name: "x", Transport: "stdio", Command: "npx x"}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	res, err := a.RestoreMcpServers(context.Background(), app.RestoreMcpOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.addedServers) != 0 {
		t.Fatal("dry-run must not call Add")
	}
	if len(res.WouldInstall) != 1 || res.WouldInstall[0] != "codex/x" {
		t.Fatalf("WouldInstall = %v, want [codex/x]", res.WouldInstall)
	}
	if len(res.Skipped) != 0 {
		t.Fatalf("Skipped must be empty for a targeted dry-run; got %v", res.Skipped)
	}
}

func TestRestoreMcpServers_DryRun_NonTargetedNotInWouldInstall(t *testing.T) {
	t.Parallel()
	codexStub := &stubMcpAdapter{id: "codex", available: true}
	hermesStub := &stubMcpAdapter{id: "hermes-agent", available: true}
	srv := config.McpServer{Name: "x", Transport: "stdio", Command: "npx x", Agents: []string{"codex"}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}},
		app.WithMcpAdapters([]app.McpAdapter{codexStub, hermesStub}))
	res, err := a.RestoreMcpServers(context.Background(), app.RestoreMcpOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.WouldInstall) != 1 || res.WouldInstall[0] != "codex/x" {
		t.Fatalf("WouldInstall = %v, want only [codex/x]", res.WouldInstall)
	}
	if len(res.Skipped) != 1 || res.Skipped[0] != "hermes-agent/x" {
		t.Fatalf("Skipped = %v, want [hermes-agent/x] (non-targeted)", res.Skipped)
	}
}

func TestAddMcpServer_PersistsToManifest(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{id: "codex", available: true}
	a := newMcpTestApp(t, config.AgentsConfig{}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	srv := config.McpServer{Name: "new", Transport: "stdio", Command: "npx new", Agents: []string{"codex"}}
	if _, err := a.AddMcpServer(context.Background(), srv); err != nil {
		t.Fatal(err)
	}
	if len(stub.addedServers) != 1 {
		t.Fatalf("expected 1 adapter Add call, got %d", len(stub.addedServers))
	}
	cfg := loadMcpTestConfig(t, a)
	found := false
	for _, s := range cfg.Agents.McpServers {
		if s.Name == "new" {
			found = true
		}
	}
	if !found {
		t.Fatal("server not persisted to manifest")
	}
}

func TestAddMcpServer_Upsert(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{id: "codex", available: true}
	existing := config.McpServer{Name: "x", Transport: "stdio", Command: "npx old"}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{existing}}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	updated := config.McpServer{Name: "x", Transport: "stdio", Command: "npx new"}
	if _, err := a.AddMcpServer(context.Background(), updated); err != nil {
		t.Fatal(err)
	}
	cfg := loadMcpTestConfig(t, a)
	var count int
	for _, s := range cfg.Agents.McpServers {
		if s.Name == "x" {
			count++
			if s.Command != "npx new" {
				t.Fatalf("expected updated command, got %q", s.Command)
			}
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 entry for 'x', got %d", count)
	}
}

func TestAddMcpServer_UpdatesChangedInstalledHeaders(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{
		id: "codex", available: true,
		listed: []app.InstalledMcpServer{{
			Name: "grafana", Transport: "http", URL: "https://mcp.example.com",
			Headers: map[string]string{"X-Key": "old"}, HeadersKnown: true,
		}},
	}
	a := newMcpTestApp(t, config.AgentsConfig{}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	desired := config.McpServer{Name: "grafana", Transport: "http", URL: "https://mcp.example.com", Headers: map[string]string{"X-Key": "new"}, Agents: []string{"codex"}}
	res, err := a.AddMcpServer(context.Background(), desired)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Updated) != 1 || len(stub.removedNames) != 1 || len(stub.addedServers) != 1 {
		t.Fatalf("result/remove/add = %+v/%v/%v, want one update", res, stub.removedNames, stub.addedServers)
	}
	cfg := loadMcpTestConfig(t, a)
	if got := cfg.Agents.McpServers[0].Headers["X-Key"]; got != "new" {
		t.Fatalf("persisted header = %q, want new", got)
	}
}

func TestAddMcpServer_PartialAdapterFailureStillPersistsManifest(t *testing.T) {
	t.Parallel()
	ok := &stubMcpAdapter{id: "codex", available: true}
	fails := &stubMcpAdapter{id: "hermes-agent", available: true, addErr: errors.New("boom")}
	a := newMcpTestApp(t, config.AgentsConfig{}, app.WithMcpAdapters([]app.McpAdapter{ok, fails}))
	srv := config.McpServer{Name: "both", Transport: "stdio", Command: "npx both"}
	res, err := a.AddMcpServer(context.Background(), srv)
	if err != nil {
		t.Fatalf("AddMcpServer must not fail wholly on a per-adapter error: %v", err)
	}
	if len(ok.addedServers) != 1 {
		t.Fatalf("expected the working adapter to still receive Add, got %d calls", len(ok.addedServers))
	}
	if len(fails.addedServers) != 1 {
		t.Fatalf("expected every target adapter to be attempted, got %d calls to failing adapter", len(fails.addedServers))
	}
	if len(res.Errors) != 1 || res.Errors[0].AgentID != "hermes-agent" {
		t.Fatalf("expected 1 per-adapter error for hermes-agent, got %v", res.Errors)
	}
	cfg := loadMcpTestConfig(t, a)
	found := false
	for _, s := range cfg.Agents.McpServers {
		if s.Name == "both" {
			found = true
		}
	}
	if !found {
		t.Fatal("manifest must persist the add even though one adapter failed")
	}
}

func TestMcpServerByName_ReturnsFullEntry(t *testing.T) {
	t.Parallel()
	existing := config.McpServer{Name: "x", Transport: "stdio", Command: "npx x", Env: []string{"FOO"}, EnvLiteral: map[string]string{"BAR": "baz"}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{existing}})
	got, ok, err := a.McpServerByName("x")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected ok=true for a manifest entry")
	}
	if got.Command != "npx x" || len(got.Env) != 1 || got.EnvLiteral["BAR"] != "baz" {
		t.Fatalf("expected full entry with Env/EnvLiteral preserved, got %+v", got)
	}
}

func TestMcpServerByName_NotFound(t *testing.T) {
	t.Parallel()
	a := newMcpTestApp(t, config.AgentsConfig{})
	_, ok, err := a.McpServerByName("missing")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected ok=false for an absent manifest entry")
	}
}

func TestRemoveMcpServer_PersistsRemoval(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{id: "codex", available: true, listed: []app.InstalledMcpServer{{Name: "del"}}}
	existing := config.McpServer{Name: "del", Transport: "stdio", Command: "npx del", Agents: []string{"codex"}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{existing}}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	if _, err := a.RemoveMcpServer(context.Background(), "del"); err != nil {
		t.Fatal(err)
	}
	if len(stub.removedNames) != 1 || stub.removedNames[0] != "del" {
		t.Fatalf("expected adapter Remove(del), got %v", stub.removedNames)
	}
	cfg := loadMcpTestConfig(t, a)
	for _, s := range cfg.Agents.McpServers {
		if s.Name == "del" {
			t.Fatal("server still in manifest after remove")
		}
	}
}

func TestRemoveMcpServer_PersistsAcrossStaleDuplicateFragment(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "settings.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("settings.json", `{
  "version": 17,
  "$include": ["settings.d/agents.json", "settings.d/tools.json"],
  "settings": {}
}`)
	write("settings.d/agents.json", `{
  "agents": { "mcp_servers": [{ "name": "node_repl", "transport": "stdio", "command": "node-repl", "agents": ["hermes-agent"] }] }
}`)
	write("settings.d/tools.json", `{
  "agents": { "mcp_servers": [{ "name": "node_repl", "transport": "stdio", "command": "node-repl", "agents": ["hermes-agent"] }] },
  "tools": {}
}`)
	stub := &stubMcpAdapter{id: "hermes-agent", available: true}
	a := app.New(filepath.Join(dir, "settings.json"), app.WithMcpAdapters([]app.McpAdapter{stub}))
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	if _, err := a.RemoveMcpServer(context.Background(), "node_repl"); err != nil {
		t.Fatal(err)
	}
	cfg := loadMcpTestConfig(t, a)
	for _, s := range cfg.Agents.McpServers {
		if s.Name == "node_repl" {
			t.Fatal("server resurrected by stale duplicate fragment after remove")
		}
	}
}

func TestRemoveMcpServer_ScrubsGroupRefs(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{id: "hermes-agent", available: true}
	existing := config.McpServer{Name: "del", Transport: "stdio", Command: "npx del", Agents: []string{"hermes-agent"}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{existing}}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	if err := a.SetMcpGroups(context.Background(), "del", []string{"ai-plugins"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RemoveMcpServer(context.Background(), "del"); err != nil {
		t.Fatal(err)
	}
	cfg := loadMcpTestConfig(t, a)
	for _, g := range cfg.Groups {
		for _, ref := range g.McpServers {
			if ref == "del" {
				t.Fatalf("group %q still references removed server", g.Name)
			}
		}
	}
}

func TestRemoveMcpServer_RejectsUnmanaged(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{id: "codex", available: true}
	a := newMcpTestApp(t, config.AgentsConfig{}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	_, err := a.RemoveMcpServer(context.Background(), "not-in-manifest")
	if err == nil {
		t.Fatal("expected error: omni must not remove servers it did not add")
	}
	if len(stub.removedNames) != 0 {
		t.Fatal("adapter Remove must not be called for unmanaged server")
	}
}

func TestRemoveMcpServer_PartialAdapterFailureStillPersistsManifest(t *testing.T) {
	t.Parallel()
	ok := &stubMcpAdapter{id: "codex", available: true, listed: []app.InstalledMcpServer{{Name: "del"}}}
	fails := &stubMcpAdapter{id: "hermes-agent", available: true, removeErr: errors.New("boom"), listed: []app.InstalledMcpServer{{Name: "del"}}}
	existing := config.McpServer{Name: "del", Transport: "stdio", Command: "npx del"}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{existing}}, app.WithMcpAdapters([]app.McpAdapter{ok, fails}))
	res, err := a.RemoveMcpServer(context.Background(), "del")
	if err != nil {
		t.Fatalf("RemoveMcpServer must not fail wholly on a per-adapter error: %v", err)
	}
	if len(ok.removedNames) != 1 {
		t.Fatalf("expected the working adapter to still receive Remove, got %d calls", len(ok.removedNames))
	}
	if len(fails.removedNames) != 1 {
		t.Fatalf("expected every target adapter to be attempted, got %d calls to failing adapter", len(fails.removedNames))
	}
	if len(res.Errors) != 1 || res.Errors[0].AgentID != "hermes-agent" {
		t.Fatalf("expected 1 per-adapter error for hermes-agent, got %v", res.Errors)
	}
	cfg := loadMcpTestConfig(t, a)
	for _, s := range cfg.Agents.McpServers {
		if s.Name == "del" {
			t.Fatal("manifest must drop the server even though one adapter failed to remove it live")
		}
	}
}

func TestSetMcpServerAgents_NarrowingRemovesFromDeselectedAdapter(t *testing.T) {
	t.Parallel()
	codex := &stubMcpAdapter{id: "codex", available: true, listed: []app.InstalledMcpServer{{Name: "x"}}}
	hermes := &stubMcpAdapter{id: "hermes-agent", available: true}
	existing := config.McpServer{Name: "x", Transport: "stdio", Command: "npx x", Agents: []string{"codex", "hermes-agent"}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{existing}}, app.WithMcpAdapters([]app.McpAdapter{codex, hermes}))
	res, err := a.SetMcpServerAgents(context.Background(), "x", []string{"codex"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hermes.removedNames) != 1 || hermes.removedNames[0] != "x" {
		t.Fatalf("expected hermes-agent to receive Remove(x), got %v", hermes.removedNames)
	}
	if len(codex.addedServers) != 0 {
		t.Fatalf("codex was already targeted and installed; expected no re-Add, got %d", len(codex.addedServers))
	}
	if len(codex.removedNames) != 0 {
		t.Fatal("codex stays targeted; must not be removed")
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}
	cfg := loadMcpTestConfig(t, a)
	for _, s := range cfg.Agents.McpServers {
		if s.Name == "x" && (len(s.Agents) != 1 || s.Agents[0] != "codex") {
			t.Fatalf("expected manifest Agents=[codex], got %v", s.Agents)
		}
	}
}

func TestSetMcpServerAgents_WideningAddsToNewlySelectedAdapter(t *testing.T) {
	t.Parallel()
	codex := &stubMcpAdapter{id: "codex", available: true, listed: []app.InstalledMcpServer{{Name: "x"}}}
	hermes := &stubMcpAdapter{id: "hermes-agent", available: true}
	existing := config.McpServer{Name: "x", Transport: "stdio", Command: "npx x", Agents: []string{"codex"}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{existing}}, app.WithMcpAdapters([]app.McpAdapter{codex, hermes}))
	res, err := a.SetMcpServerAgents(context.Background(), "x", []string{"codex", "hermes-agent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hermes.addedServers) != 1 {
		t.Fatalf("expected hermes-agent to receive Add, got %d calls", len(hermes.addedServers))
	}
	if len(codex.addedServers) != 0 {
		t.Fatal("codex was already targeted; must not be re-Added")
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}
	cfg := loadMcpTestConfig(t, a)
	for _, s := range cfg.Agents.McpServers {
		if s.Name == "x" && len(s.Agents) != 2 {
			t.Fatalf("expected manifest Agents to have 2 entries, got %v", s.Agents)
		}
	}
}

func TestSetMcpServerAgents_NoChangeTouchesNoAdapter(t *testing.T) {
	t.Parallel()
	codex := &stubMcpAdapter{id: "codex", available: true, listed: []app.InstalledMcpServer{{Name: "x"}}}
	existing := config.McpServer{Name: "x", Transport: "stdio", Command: "npx x", Agents: []string{"codex"}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{existing}}, app.WithMcpAdapters([]app.McpAdapter{codex}))
	res, err := a.SetMcpServerAgents(context.Background(), "x", []string{"codex"})
	if err != nil {
		t.Fatal(err)
	}
	if len(codex.addedServers) != 0 || len(codex.removedNames) != 0 {
		t.Fatalf("expected no adapter calls for unchanged targeting, got Add=%d Remove=%d", len(codex.addedServers), len(codex.removedNames))
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}
}

func TestSetMcpServerAgents_ReconcilesChangedHeaders(t *testing.T) {
	t.Parallel()
	codex := &stubMcpAdapter{
		id: "codex", available: true,
		listed: []app.InstalledMcpServer{{
			Name: "x", Transport: "http", URL: "https://mcp.example.com",
			Headers: map[string]string{"X-Key": "old"}, HeadersKnown: true,
		}},
	}
	existing := config.McpServer{Name: "x", Transport: "http", URL: "https://mcp.example.com", Headers: map[string]string{"X-Key": "new"}, Agents: []string{"codex"}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{existing}}, app.WithMcpAdapters([]app.McpAdapter{codex}))
	res, err := a.SetMcpServerAgents(context.Background(), "x", []string{"codex"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Updated) != 1 || len(codex.removedNames) != 1 || len(codex.addedServers) != 1 {
		t.Fatalf("result/remove/add = %+v/%v/%v, want one update", res, codex.removedNames, codex.addedServers)
	}
}

func TestSetMcpServerAgents_ReconcilesMissingSelectedAdapter(t *testing.T) {
	t.Parallel()
	codex := &stubMcpAdapter{id: "codex", available: true}
	existing := config.McpServer{Name: "x", Transport: "stdio", Command: "npx x", Agents: []string{"codex"}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{existing}}, app.WithMcpAdapters([]app.McpAdapter{codex}))
	if _, err := a.SetMcpServerAgents(context.Background(), "x", []string{"codex"}); err != nil {
		t.Fatal(err)
	}
	if len(codex.addedServers) != 1 {
		t.Fatalf("expected missing selected server to be installed, got %d Add calls", len(codex.addedServers))
	}
}

func TestSetMcpServerAgents_AdapterErrorIsNonFatalAndManifestStillPersists(t *testing.T) {
	t.Parallel()
	fails := &stubMcpAdapter{id: "hermes-agent", available: true, removeErr: errors.New("boom")}
	existing := config.McpServer{Name: "x", Transport: "stdio", Command: "npx x", Agents: []string{"hermes-agent"}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{existing}}, app.WithMcpAdapters([]app.McpAdapter{fails}))
	res, err := a.SetMcpServerAgents(context.Background(), "x", []string{"codex"})
	if err != nil {
		t.Fatalf("SetMcpServerAgents must not fail wholly on a per-adapter error: %v", err)
	}
	if len(res.Errors) != 1 || res.Errors[0].AgentID != "hermes-agent" {
		t.Fatalf("expected 1 per-adapter error for hermes-agent, got %v", res.Errors)
	}
	cfg := loadMcpTestConfig(t, a)
	for _, s := range cfg.Agents.McpServers {
		if s.Name == "x" && (len(s.Agents) != 1 || s.Agents[0] != "codex") {
			t.Fatalf("manifest must persist new Agents despite adapter error, got %v", s.Agents)
		}
	}
}

func TestSetMcpServerAgents_TargetedUnavailableAdapterSkips(t *testing.T) {
	t.Parallel()
	codex := &stubMcpAdapter{id: "codex", available: false}
	existing := config.McpServer{Name: "x", Transport: "stdio", Command: "npx x", Agents: []string{}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{existing}}, app.WithMcpAdapters([]app.McpAdapter{codex}))
	res, err := a.SetMcpServerAgents(context.Background(), "x", []string{"codex"})
	if err != nil {
		t.Fatalf("SetMcpServerAgents must not fail wholly on a per-adapter skip: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unavailable adapter must not produce errors, got %v", res.Errors)
	}
	if len(res.SkippedUnavailable) != 1 || res.SkippedUnavailable[0] != "codex/x" {
		t.Fatalf("expected SkippedUnavailable [codex/x], got %v", res.SkippedUnavailable)
	}
}

func TestSetMcpServerAgents_RejectsUnmanaged(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{id: "codex", available: true}
	a := newMcpTestApp(t, config.AgentsConfig{}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	_, err := a.SetMcpServerAgents(context.Background(), "not-in-manifest", []string{"codex"})
	if err == nil {
		t.Fatal("expected error for unmanaged server")
	}
}

func TestImportMcpServers_ReturnsUnmanaged(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{
		id:        "codex",
		available: true,
		listed:    []app.InstalledMcpServer{{Name: "hand-added", Transport: "stdio", Command: "npx y"}},
	}
	a := newMcpTestApp(t, config.AgentsConfig{}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	diff, err := a.ImportMcpServers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Unmanaged["codex"]) != 1 {
		t.Fatalf("expected 1 unmanaged, got %d", len(diff.Unmanaged["codex"]))
	}
	if diff.Unmanaged["codex"][0].Name != "hand-added" {
		t.Fatalf("wrong server: %v", diff.Unmanaged["codex"][0])
	}
}

func TestImportMcpServers_ExcludesManaged(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{
		id:        "codex",
		available: true,
		listed: []app.InstalledMcpServer{
			{Name: "managed", Transport: "stdio"},
			{Name: "unmanaged", Transport: "http"},
		},
	}
	existing := config.McpServer{Name: "managed", Transport: "stdio", Command: "npx x"}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{existing}}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	diff, err := a.ImportMcpServers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Unmanaged["codex"]) != 1 || diff.Unmanaged["codex"][0].Name != "unmanaged" {
		t.Fatalf("unexpected unmanaged: %v", diff.Unmanaged)
	}
}

func TestAdoptUnmanagedMcpServers_PreservesHeaders(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{
		id: "codex", available: true,
		listed: []app.InstalledMcpServer{{
			Name: "remote", Transport: "http", URL: "https://mcp.example.com",
			Headers: map[string]string{"X-Key": "${REMOTE_KEY}"}, HeadersKnown: true,
		}},
	}
	a := newMcpTestApp(t, config.AgentsConfig{}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	adopted, err := a.AdoptUnmanagedMcpServers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if adopted.Adopted != 1 {
		t.Fatalf("adopted = %d, want 1", adopted.Adopted)
	}
	cfg := loadMcpTestConfig(t, a)
	if got := cfg.Agents.McpServers[0].Headers["X-Key"]; got != "${REMOTE_KEY}" {
		t.Fatalf("imported header = %q, want env reference", got)
	}
}

func TestAdoptUnmanagedMcpServers_UnionsAgentsForIdenticalServer(t *testing.T) {
	t.Parallel()
	server := app.InstalledMcpServer{
		Name: "remote", Transport: "http", URL: "https://mcp.example.com",
		Headers: map[string]string{"X-Key": "${REMOTE_KEY}"}, HeadersKnown: true,
	}
	codex := &stubMcpAdapter{id: "codex", available: true, listed: []app.InstalledMcpServer{server}}
	hermes := &stubMcpAdapter{id: "hermes-agent", available: true, listed: []app.InstalledMcpServer{server}}
	a := newMcpTestApp(t, config.AgentsConfig{}, app.WithMcpAdapters([]app.McpAdapter{hermes, codex}))

	adopted, err := a.AdoptUnmanagedMcpServers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if adopted.Adopted != 1 {
		t.Fatalf("adopted = %d, want 1", adopted.Adopted)
	}
	got := loadMcpTestConfig(t, a).Agents.McpServers
	if len(got) != 1 || !slices.Equal(got[0].Agents, []string{"codex", "hermes-agent"}) {
		t.Fatalf("imported servers = %+v, want one server targeted at both agents", got)
	}
	if got[0].Headers["X-Key"] != "${REMOTE_KEY}" {
		t.Fatalf("imported headers = %v, want env reference", got[0].Headers)
	}
}

func TestAdoptUnmanagedMcpServers_RejectsConflictingHeaders(t *testing.T) {
	t.Parallel()
	codex := &stubMcpAdapter{id: "codex", available: true, listed: []app.InstalledMcpServer{{
		Name: "remote", Transport: "http", URL: "https://mcp.example.com",
		Headers: map[string]string{"X-Key": "claude"}, HeadersKnown: true,
	}}}
	hermes := &stubMcpAdapter{id: "hermes-agent", available: true, listed: []app.InstalledMcpServer{{
		Name: "remote", Transport: "http", URL: "https://mcp.example.com",
		Headers: map[string]string{"X-Key": "hermes-agent"}, HeadersKnown: true,
	}}}
	a := newMcpTestApp(t, config.AgentsConfig{}, app.WithMcpAdapters([]app.McpAdapter{codex, hermes}))

	adopted, err := a.AdoptUnmanagedMcpServers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(adopted.Conflicts) != 1 || !strings.Contains(adopted.Conflicts[0], "conflicting configuration") {
		t.Fatalf("conflicts = %v, want one conflicting-configuration line", adopted.Conflicts)
	}
	if adopted.Adopted != 0 || len(loadMcpTestConfig(t, a).Agents.McpServers) != 0 {
		t.Fatal("conflicting unmanaged servers must not enter the manifest")
	}
}
