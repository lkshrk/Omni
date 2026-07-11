package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func TestAgentsMcpSubcmdsRegistered(t *testing.T) {
	state := &rootState{}
	cmd := newAgentsMcpCmd(state)
	want := map[string]bool{"list": false, "add": false, "remove": false, "restore": false, "import": false}
	for _, sub := range cmd.Commands() {
		want[sub.Name()] = true
	}
	for name, found := range want {
		if !found {
			t.Errorf("subcommand %q not registered", name)
		}
	}
}

type importStubMcpAdapter struct {
	id           string
	listed       []app.InstalledMcpServer
	addedServers []config.McpServer
}

func (s *importStubMcpAdapter) ID() string      { return s.id }
func (s *importStubMcpAdapter) Available() bool { return true }
func (s *importStubMcpAdapter) List(_ context.Context) ([]app.InstalledMcpServer, error) {
	return append([]app.InstalledMcpServer(nil), s.listed...), nil
}
func (s *importStubMcpAdapter) Add(_ context.Context, srv config.McpServer) error {
	s.addedServers = append(s.addedServers, srv)
	return nil
}
func (s *importStubMcpAdapter) Remove(_ context.Context, _ string) error { return nil }

func newImportTestApp(t *testing.T, adapters []app.McpAdapter) *app.App {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	root := config.RootConfig{Version: config.CurrentVersion}
	if err := config.Save(cfgPath, &root); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath, app.WithMcpAdapters(adapters))
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func runAgentsMcpImport(t *testing.T, a *app.App, args ...string) (string, error) {
	t.Helper()
	state := &rootState{app: a}
	cmd := newAgentsMcpImportCmd(state)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return out.String(), err
}

func TestAgentsMcpImport_NoArgListsUnmanaged(t *testing.T) {
	stub := &importStubMcpAdapter{
		id:     "claude-code",
		listed: []app.InstalledMcpServer{{Name: "hand-added", Transport: "stdio", Command: "npx y"}},
	}
	a := newImportTestApp(t, []app.McpAdapter{stub})
	out, err := runAgentsMcpImport(t, a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Contains([]byte(out), []byte("hand-added")) {
		t.Fatalf("expected listing to mention hand-added, got: %s", out)
	}
	if len(stub.addedServers) != 0 {
		t.Fatalf("no-arg import must not adopt anything, got %v", stub.addedServers)
	}
}

func TestAgentsMcpImport_WithNameAdopts(t *testing.T) {
	stub := &importStubMcpAdapter{
		id:     "claude-code",
		listed: []app.InstalledMcpServer{{Name: "hand-added", Transport: "stdio", Command: "npx y"}},
	}
	a := newImportTestApp(t, []app.McpAdapter{stub})
	out, err := runAgentsMcpImport(t, a, "hand-added")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Contains([]byte(out), []byte("imported hand-added")) {
		t.Fatalf("expected confirmation, got: %s", out)
	}
	// AddMcpServer now skips adapter.Add when List already reports the name
	// present (the stub's listed field, above) — re-adding an
	// already-installed server is not a safe assumption for the real
	// claude/codex CLIs, so import must not call Add again here.
	if len(stub.addedServers) != 0 {
		t.Fatalf("expected adapter Add not to be called for an already-installed hand-added, got %v", stub.addedServers)
	}

	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range cfg.Agents.McpServers {
		if s.Name != "hand-added" {
			continue
		}
		found = true
		if s.Transport != "stdio" || s.Command != "npx y" {
			t.Fatalf("manifest entry missing copied fields: %+v", s)
		}
		if len(s.Env) != 0 || len(s.EnvLiteral) != 0 {
			t.Fatalf("import must never carry env values, got env=%v env_literal=%v", s.Env, s.EnvLiteral)
		}
	}
	if !found {
		t.Fatal("expected manifest to contain adopted server hand-added")
	}
}

func TestAgentsMcpImport_UnknownNameErrors(t *testing.T) {
	stub := &importStubMcpAdapter{id: "claude-code"}
	a := newImportTestApp(t, []app.McpAdapter{stub})
	_, err := runAgentsMcpImport(t, a, "does-not-exist")
	if err == nil {
		t.Fatal("expected error for unknown name")
	}
}

func TestAgentsMcpImport_TwoArgsIsUsageError(t *testing.T) {
	stub := &importStubMcpAdapter{id: "claude-code"}
	a := newImportTestApp(t, []app.McpAdapter{stub})
	_, err := runAgentsMcpImport(t, a, "one", "two")
	if err == nil {
		t.Fatal("expected usage error for two args")
	}
}
