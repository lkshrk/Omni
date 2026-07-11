package app_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

type stubMcpAdapter struct {
	id           string
	available    bool
	addErr       error
	removeErr    error
	listErr      error
	listed       []app.InstalledMcpServer
	addedServers []config.McpServer
	removedNames []string
	listCalls    int
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
func (s *stubMcpAdapter) Add(_ context.Context, srv config.McpServer) error {
	s.addedServers = append(s.addedServers, srv)
	return s.addErr
}
func (s *stubMcpAdapter) Remove(_ context.Context, name string) error {
	s.removedNames = append(s.removedNames, name)
	return s.removeErr
}

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
	a := app.New(cfgPath, opts...)
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

func TestRestoreMcpServers_InstallsTargetedServer(t *testing.T) {
	stub := &stubMcpAdapter{id: "claude-code", available: true}
	srv := config.McpServer{Name: "linear", Transport: "stdio", Command: "npx x", Agents: []string{"claude-code"}}
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

func TestRestoreMcpServers_SkipsNonTargetedAgent(t *testing.T) {
	stub := &stubMcpAdapter{id: "codex", available: true}
	srv := config.McpServer{Name: "linear", Transport: "stdio", Command: "npx x", Agents: []string{"claude-code"}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	_, err := a.RestoreMcpServers(context.Background(), app.RestoreMcpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.addedServers) != 0 {
		t.Fatal("codex should be skipped when not in server's Agents list")
	}
}

func TestRestoreMcpServers_EmptyAgentsMeansAll(t *testing.T) {
	claude := &stubMcpAdapter{id: "claude-code", available: true, listed: []app.InstalledMcpServer{{Name: "x"}}}
	codex := &stubMcpAdapter{id: "codex", available: true}
	srv := config.McpServer{Name: "grafana", Transport: "http", URL: "https://mcp.example.com", Agents: nil}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}}, app.WithMcpAdapters([]app.McpAdapter{claude, codex}))
	_, err := a.RestoreMcpServers(context.Background(), app.RestoreMcpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(claude.addedServers) != 1 {
		t.Fatalf("expected claude-code to receive Add, got %d calls", len(claude.addedServers))
	}
	if len(codex.addedServers) != 1 {
		t.Fatalf("expected codex to receive Add, got %d calls", len(codex.addedServers))
	}
}

func TestRestoreMcpServers_SkipsUnavailableAgent(t *testing.T) {
	stub := &stubMcpAdapter{id: "claude-code", available: false}
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

func TestRestoreMcpServers_PerServerErrorIsNonFatal(t *testing.T) {
	stub := &stubMcpAdapter{id: "claude-code", available: true, addErr: errors.New("env var MISSING not set")}
	srv := config.McpServer{Name: "x", Transport: "stdio", Command: "npx x"}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	res, err := a.RestoreMcpServers(context.Background(), app.RestoreMcpOptions{})
	if err != nil {
		t.Fatalf("restore must not return top-level error for per-server failure: %v", err)
	}
	if len(res.Errors) == 0 {
		t.Fatal("expected per-server error in result")
	}
}

func TestRestoreMcpServers_SkipsAlreadyInstalled(t *testing.T) {
	stub := &stubMcpAdapter{id: "claude-code", available: true, listed: []app.InstalledMcpServer{{Name: "linear"}}}
	srv := config.McpServer{Name: "linear", Transport: "stdio", Command: "npx x", Agents: []string{"claude-code"}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	res, err := a.RestoreMcpServers(context.Background(), app.RestoreMcpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.addedServers) != 0 {
		t.Fatalf("expected no Add call for already-present server, got %v", stub.addedServers)
	}
	if len(res.AlreadyInstalled) != 1 || res.AlreadyInstalled[0] != "claude-code/linear" {
		t.Fatalf("expected already-installed entry, got %v", res.AlreadyInstalled)
	}
	if len(res.Installed) != 0 {
		t.Fatalf("expected no installed entries, got %v", res.Installed)
	}
}

// TestRestoreMcpServers_SkipsShadowedByPlugin covers the restore-skip half of
// the shadow contract: installing a user-scope duplicate of a plugin-provided
// server would be harm, not repair, so restore must skip it and report it
// separately rather than as installed/already-installed/erroring.
func TestRestoreMcpServers_SkipsShadowedByPlugin(t *testing.T) {
	mcpStub := &stubMcpAdapter{id: "claude-code", available: true}
	pluginStub := &stubPluginAdapter{
		id:            "claude-code",
		available:     true,
		listedPlugins: []app.InstalledPlugin{{Name: "context-mode", Marketplace: "some-marketplace"}},
	}
	srv := config.McpServer{Name: "context-mode", Transport: "stdio", Command: "npx x", Agents: []string{"claude-code"}}
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
	if len(res.ShadowedByPlugin) != 1 || res.ShadowedByPlugin[0] != "claude-code/context-mode" {
		t.Fatalf("expected shadowed entry, got %v", res.ShadowedByPlugin)
	}
	if len(res.Installed) != 0 || len(res.AlreadyInstalled) != 0 || len(res.Errors) != 0 {
		t.Fatalf("expected only ShadowedByPlugin populated, got %+v", res)
	}
}

func TestRestoreMcpServers_ListErrorWarnsAndAttemptsInstall(t *testing.T) {
	stub := &stubMcpAdapter{id: "claude-code", available: true, listErr: errors.New("list failed")}
	srv := config.McpServer{Name: "linear", Transport: "stdio", Command: "npx x", Agents: []string{"claude-code"}}
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
	stub := &stubMcpAdapter{id: "claude-code", available: true}
	srv := config.McpServer{Name: "x", Transport: "stdio", Command: "npx x"}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	res, err := a.RestoreMcpServers(context.Background(), app.RestoreMcpOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.addedServers) != 0 {
		t.Fatal("dry-run must not call Add")
	}
	if len(res.WouldInstall) != 1 || res.WouldInstall[0] != "claude-code/x" {
		t.Fatalf("WouldInstall = %v, want [claude-code/x]", res.WouldInstall)
	}
	if len(res.Skipped) != 0 {
		t.Fatalf("Skipped must be empty for a targeted dry-run; got %v", res.Skipped)
	}
}

func TestRestoreMcpServers_DryRun_NonTargetedNotInWouldInstall(t *testing.T) {
	claudeStub := &stubMcpAdapter{id: "claude-code", available: true}
	codexStub := &stubMcpAdapter{id: "codex", available: true}
	// server targets only claude-code; codex must NOT appear in WouldInstall
	srv := config.McpServer{Name: "x", Transport: "stdio", Command: "npx x", Agents: []string{"claude-code"}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{srv}},
		app.WithMcpAdapters([]app.McpAdapter{claudeStub, codexStub}))
	res, err := a.RestoreMcpServers(context.Background(), app.RestoreMcpOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.WouldInstall) != 1 || res.WouldInstall[0] != "claude-code/x" {
		t.Fatalf("WouldInstall = %v, want only [claude-code/x]", res.WouldInstall)
	}
	if len(res.Skipped) != 1 || res.Skipped[0] != "codex/x" {
		t.Fatalf("Skipped = %v, want [codex/x] (non-targeted)", res.Skipped)
	}
}

func TestAddMcpServer_PersistsToManifest(t *testing.T) {
	stub := &stubMcpAdapter{id: "claude-code", available: true}
	a := newMcpTestApp(t, config.AgentsConfig{}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	srv := config.McpServer{Name: "new", Transport: "stdio", Command: "npx new", Agents: []string{"claude-code"}}
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
	stub := &stubMcpAdapter{id: "claude-code", available: true}
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

// TestAddMcpServer_PartialAdapterFailureStillPersistsManifest asserts the fix for
// partial-application drift: a live install succeeding on one adapter and failing
// on another must not leave the manifest missing the server (the manifest is the
// source of intent, not a mirror of adapter success).
func TestAddMcpServer_PartialAdapterFailureStillPersistsManifest(t *testing.T) {
	ok := &stubMcpAdapter{id: "claude-code", available: true}
	fails := &stubMcpAdapter{id: "codex", available: true, addErr: errors.New("boom")}
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
	if len(res.Errors) != 1 || res.Errors[0].AgentID != "codex" {
		t.Fatalf("expected 1 per-adapter error for codex, got %v", res.Errors)
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
	stub := &stubMcpAdapter{id: "claude-code", available: true}
	existing := config.McpServer{Name: "del", Transport: "stdio", Command: "npx del", Agents: []string{"claude-code"}}
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

func TestRemoveMcpServer_RejectsUnmanaged(t *testing.T) {
	stub := &stubMcpAdapter{id: "claude-code", available: true}
	a := newMcpTestApp(t, config.AgentsConfig{}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	_, err := a.RemoveMcpServer(context.Background(), "not-in-manifest")
	if err == nil {
		t.Fatal("expected error: omni must not remove servers it did not add")
	}
	if len(stub.removedNames) != 0 {
		t.Fatal("adapter Remove must not be called for unmanaged server")
	}
}

// TestRemoveMcpServer_PartialAdapterFailureStillPersistsManifest asserts the fix for
// partial-application drift on remove: the manifest must drop the server even when
// one of the targeted adapters fails to remove it live.
func TestRemoveMcpServer_PartialAdapterFailureStillPersistsManifest(t *testing.T) {
	ok := &stubMcpAdapter{id: "claude-code", available: true}
	fails := &stubMcpAdapter{id: "codex", available: true, removeErr: errors.New("boom")}
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
	if len(res.Errors) != 1 || res.Errors[0].AgentID != "codex" {
		t.Fatalf("expected 1 per-adapter error for codex, got %v", res.Errors)
	}
	cfg := loadMcpTestConfig(t, a)
	for _, s := range cfg.Agents.McpServers {
		if s.Name == "del" {
			t.Fatal("manifest must drop the server even though one adapter failed to remove it live")
		}
	}
}

func TestSetMcpServerAgents_NarrowingRemovesFromDeselectedAdapter(t *testing.T) {
	claude := &stubMcpAdapter{id: "claude-code", available: true, listed: []app.InstalledMcpServer{{Name: "x"}}}
	codex := &stubMcpAdapter{id: "codex", available: true}
	existing := config.McpServer{Name: "x", Transport: "stdio", Command: "npx x", Agents: []string{"claude-code", "codex"}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{existing}}, app.WithMcpAdapters([]app.McpAdapter{claude, codex}))
	res, err := a.SetMcpServerAgents(context.Background(), "x", []string{"claude-code"})
	if err != nil {
		t.Fatal(err)
	}
	if len(codex.removedNames) != 1 || codex.removedNames[0] != "x" {
		t.Fatalf("expected codex to receive Remove(x), got %v", codex.removedNames)
	}
	if len(claude.addedServers) != 0 {
		t.Fatalf("claude-code was already targeted and installed; expected no re-Add, got %d", len(claude.addedServers))
	}
	if len(claude.removedNames) != 0 {
		t.Fatal("claude-code stays targeted; must not be removed")
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}
	cfg := loadMcpTestConfig(t, a)
	for _, s := range cfg.Agents.McpServers {
		if s.Name == "x" && (len(s.Agents) != 1 || s.Agents[0] != "claude-code") {
			t.Fatalf("expected manifest Agents=[claude-code], got %v", s.Agents)
		}
	}
}

func TestSetMcpServerAgents_WideningAddsToNewlySelectedAdapter(t *testing.T) {
	claude := &stubMcpAdapter{id: "claude-code", available: true, listed: []app.InstalledMcpServer{{Name: "x"}}}
	codex := &stubMcpAdapter{id: "codex", available: true}
	existing := config.McpServer{Name: "x", Transport: "stdio", Command: "npx x", Agents: []string{"claude-code"}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{existing}}, app.WithMcpAdapters([]app.McpAdapter{claude, codex}))
	res, err := a.SetMcpServerAgents(context.Background(), "x", []string{"claude-code", "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if len(codex.addedServers) != 1 {
		t.Fatalf("expected codex to receive Add, got %d calls", len(codex.addedServers))
	}
	if len(claude.addedServers) != 0 {
		t.Fatal("claude-code was already targeted; must not be re-Added")
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
	claude := &stubMcpAdapter{id: "claude-code", available: true, listed: []app.InstalledMcpServer{{Name: "x"}}}
	existing := config.McpServer{Name: "x", Transport: "stdio", Command: "npx x", Agents: []string{"claude-code"}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{existing}}, app.WithMcpAdapters([]app.McpAdapter{claude}))
	res, err := a.SetMcpServerAgents(context.Background(), "x", []string{"claude-code"})
	if err != nil {
		t.Fatal(err)
	}
	if len(claude.addedServers) != 0 || len(claude.removedNames) != 0 {
		t.Fatalf("expected no adapter calls for unchanged targeting, got Add=%d Remove=%d", len(claude.addedServers), len(claude.removedNames))
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}
}

func TestSetMcpServerAgents_ReconcilesMissingSelectedAdapter(t *testing.T) {
	claude := &stubMcpAdapter{id: "claude-code", available: true}
	existing := config.McpServer{Name: "x", Transport: "stdio", Command: "npx x", Agents: []string{"claude-code"}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{existing}}, app.WithMcpAdapters([]app.McpAdapter{claude}))
	if _, err := a.SetMcpServerAgents(context.Background(), "x", []string{"claude-code"}); err != nil {
		t.Fatal(err)
	}
	if len(claude.addedServers) != 1 {
		t.Fatalf("expected missing selected server to be installed, got %d Add calls", len(claude.addedServers))
	}
}

func TestSetMcpServerAgents_AdapterErrorIsNonFatalAndManifestStillPersists(t *testing.T) {
	fails := &stubMcpAdapter{id: "codex", available: true, removeErr: errors.New("boom")}
	existing := config.McpServer{Name: "x", Transport: "stdio", Command: "npx x", Agents: []string{"codex"}}
	a := newMcpTestApp(t, config.AgentsConfig{McpServers: []config.McpServer{existing}}, app.WithMcpAdapters([]app.McpAdapter{fails}))
	res, err := a.SetMcpServerAgents(context.Background(), "x", []string{"claude-code"})
	if err != nil {
		t.Fatalf("SetMcpServerAgents must not fail wholly on a per-adapter error: %v", err)
	}
	if len(res.Errors) != 1 || res.Errors[0].AgentID != "codex" {
		t.Fatalf("expected 1 per-adapter error for codex, got %v", res.Errors)
	}
	cfg := loadMcpTestConfig(t, a)
	for _, s := range cfg.Agents.McpServers {
		if s.Name == "x" && (len(s.Agents) != 1 || s.Agents[0] != "claude-code") {
			t.Fatalf("manifest must persist new Agents despite adapter error, got %v", s.Agents)
		}
	}
}

func TestSetMcpServerAgents_RejectsUnmanaged(t *testing.T) {
	stub := &stubMcpAdapter{id: "claude-code", available: true}
	a := newMcpTestApp(t, config.AgentsConfig{}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	_, err := a.SetMcpServerAgents(context.Background(), "not-in-manifest", []string{"claude-code"})
	if err == nil {
		t.Fatal("expected error for unmanaged server")
	}
}

func TestImportMcpServers_ReturnsUnmanaged(t *testing.T) {
	stub := &stubMcpAdapter{
		id:        "claude-code",
		available: true,
		listed:    []app.InstalledMcpServer{{Name: "hand-added", Transport: "stdio", Command: "npx y"}},
	}
	a := newMcpTestApp(t, config.AgentsConfig{}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	diff, err := a.ImportMcpServers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Unmanaged["claude-code"]) != 1 {
		t.Fatalf("expected 1 unmanaged, got %d", len(diff.Unmanaged["claude-code"]))
	}
	if diff.Unmanaged["claude-code"][0].Name != "hand-added" {
		t.Fatalf("wrong server: %v", diff.Unmanaged["claude-code"][0])
	}
}

func TestImportMcpServers_ExcludesManaged(t *testing.T) {
	stub := &stubMcpAdapter{
		id:        "claude-code",
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
	if len(diff.Unmanaged["claude-code"]) != 1 || diff.Unmanaged["claude-code"][0].Name != "unmanaged" {
		t.Fatalf("unexpected unmanaged: %v", diff.Unmanaged)
	}
}
