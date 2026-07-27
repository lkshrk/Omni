package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

type advisoryMcpAdapter struct {
	id     string
	listed []app.InstalledMcpServer
}

func (s *advisoryMcpAdapter) ID() string      { return s.id }
func (s *advisoryMcpAdapter) Available() bool { return true }

func (s *advisoryMcpAdapter) List(context.Context) ([]app.InstalledMcpServer, error) {
	return append([]app.InstalledMcpServer(nil), s.listed...), nil
}

func (s *advisoryMcpAdapter) Add(context.Context, config.McpServer) error { return nil }

func (s *advisoryMcpAdapter) Remove(context.Context, string) error { return nil }

func setupImportModel(t *testing.T, adapters ...app.McpAdapter) Model {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{}); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath, app.WithMcpAdapters(adapters))
	a.CacheDir = dir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("App.InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	m := baseModel(nil)
	m.app = a
	m.ctx = context.Background()
	m.mode = viewSetup
	m.setupStep = 4
	return m
}

func setupViewText(m Model) string {
	return m.View().Content
}

func confirmKeyMsg() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: tea.KeyEnter}
}

// The setup wizard runs the same adoption the CLI does, which now refuses a resolved header outright
// rather than copying it into settings.json with a warning. Refusing silently is its own failure: the
// server stays unmanaged and the count alone reads as "nothing to import".
func TestSetupImport_ResolvedHeaderRefusalReachesTheUser(t *testing.T) {
	adapter := &advisoryMcpAdapter{id: "claude-code", listed: []app.InstalledMcpServer{{
		Name: "docs", Transport: "http", URL: "https://docs.example.com/mcp",
		Headers: map[string]string{"Authorization": "Bearer sk-live-secret"}, HeadersKnown: true,
	}}}
	m := setupImportModel(t, adapter)

	msg, ok := m.doSetupAgentsImportAll()().(setupAgentsImportDoneMsg)
	if !ok {
		t.Fatal("import should complete with setupAgentsImportDoneMsg")
	}
	if msg.err != nil {
		t.Fatalf("import failed: %v", msg.err)
	}
	if msg.mcp != 0 {
		t.Fatalf("mcp adopted = %d, want the resolved header to refuse the server", msg.mcp)
	}
	written, err := os.ReadFile(m.app.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(written), "Bearer sk-live-secret") {
		t.Fatalf("the resolved header was written into settings.json; file =\n%s", written)
	}

	if len(m.groupNames) != 0 {
		t.Fatalf("precondition: a fresh config has no reusable groups, got %v", m.groupNames)
	}

	m.handleSetupAgentsImportDoneMsg(msg)

	view := setupViewText(m)
	if !strings.Contains(view, "Authorization") {
		t.Fatalf("the user is never told which header caused the refusal; view =\n%s", view)
	}
	if !strings.Contains(view, "docs") {
		t.Fatalf("the refusal does not name the server; view =\n%s", view)
	}
	if strings.Contains(view, "sk-live-secret") {
		t.Fatalf("the refusal echoes the credential it refused to store; view =\n%s", view)
	}
	if m.setupReloading {
		t.Fatal("the wizard started the post-setup reload, which paints over the disclosure")
	}
}

// The advisory must outlive the post-setup reload it used to lose a race with: the reload budget is
// setupReloadTimeout, an order of magnitude longer than the status line's error duration.
func TestSetupImport_AdvisoryPersistsUntilAcknowledged(t *testing.T) {
	adapter := &advisoryMcpAdapter{id: "claude-code", listed: []app.InstalledMcpServer{{
		Name: "docs", Transport: "http", URL: "https://docs.example.com/mcp",
		Headers: map[string]string{"Authorization": "Bearer sk-live-secret"}, HeadersKnown: true,
	}}}
	m := setupImportModel(t, adapter)

	msg := m.doSetupAgentsImportAll()().(setupAgentsImportDoneMsg)
	m.handleSetupAgentsImportDoneMsg(msg)

	if m.setupStep != setupStepImportAdvisories {
		t.Fatalf("setupStep = %d, want the wizard to stop on the advisory step", m.setupStep)
	}
	m.statusMsg = ""
	m.statusIsErr = false
	if !strings.Contains(setupViewText(m), "Authorization") {
		t.Fatal("the disclosure vanished once the transient status expired")
	}

	if _, _ = m.handleSetupKeyMsg(confirmKeyMsg()); m.setupStep == setupStepImportAdvisories {
		t.Fatal("confirming did not dismiss the advisory step")
	}
	if strings.Contains(setupViewText(m), "Authorization") {
		t.Fatal("the advisory step stayed up after acknowledgement")
	}
}

// Credential advisories must survive when four entries exceed the status line's 157-rune cap.
func TestSetupImport_CredentialAdvisorySurvivesAmongFour(t *testing.T) {
	m := setupImportModel(t)
	advisories := setupImportAdvisories(
		app.ImportDiff{Warnings: []string{"skill package \"legacy-notes\" was already managed elsewhere, left untouched"}},
		app.McpAdoptResult{
			Conflicts: []string{"mcp server \"docs\" is declared differently by two agents, declined"},
			Skipped:   []string{"mcp server \"search\" embeds an api key in its url, refused"},
			Warnings:  []string{"mcp server \"linear-mcp-server\" sets header(s) Authorization to a resolved value; settings.json is often committed, so prefer ${ENV_VAR} references"},
		},
		app.PluginAdoptResult{},
	)
	if len(advisories) != 4 {
		t.Fatalf("advisories = %d, want 4", len(advisories))
	}
	if !strings.Contains(advisories[0], "Authorization") {
		t.Fatalf("the credential advisory must sort first so truncation cannot reach it; got %q", advisories[0])
	}

	m.handleSetupAgentsImportDoneMsg(setupAgentsImportDoneMsg{advisories: advisories})

	view := setupViewText(m)
	for _, want := range []string{"Authorization", "linear-mcp-server"} {
		if !strings.Contains(view, want) {
			t.Fatalf("%q was truncated away; view =\n%s", want, view)
		}
	}
}

// A declined server needs its advisory because an "imported 0" count alone implies nothing needs attention.
func TestSetupImport_DeclinedServerAdvisoryReachesTheUser(t *testing.T) {
	m := setupImportModel(t,
		&advisoryMcpAdapter{id: "claude-code", listed: []app.InstalledMcpServer{{
			Name: "docs", Transport: "http", URL: "https://docs.example.com/mcp",
		}}},
		&advisoryMcpAdapter{id: "codex", listed: []app.InstalledMcpServer{{
			Name: "docs", Transport: "http", URL: "https://other.example.com/mcp",
		}}},
	)

	msg, ok := m.doSetupAgentsImportAll()().(setupAgentsImportDoneMsg)
	if !ok {
		t.Fatal("import should complete with setupAgentsImportDoneMsg")
	}
	if msg.err != nil {
		t.Fatalf("import failed: %v", msg.err)
	}
	if msg.mcp != 0 {
		t.Fatalf("mcp adopted = %d, want the conflicting server to be declined", msg.mcp)
	}

	m.handleSetupAgentsImportDoneMsg(msg)

	if view := setupViewText(m); !strings.Contains(view, "docs") {
		t.Fatalf("the user is never told a server was declined; view =\n%s", view)
	}
}

func TestSetupImportAdvisories_CarriesEveryChannel(t *testing.T) {
	got := setupImportAdvisories(
		app.ImportDiff{Warnings: []string{"skills-warning"}},
		app.McpAdoptResult{
			Conflicts: []string{"mcp-conflict"},
			Skipped:   []string{"mcp-skipped"},
			Warnings:  []string{"mcp-warning"},
		},
		app.PluginAdoptResult{Skipped: []string{"plugin-skipped"}},
	)

	for _, want := range []string{"skills-warning", "mcp-conflict", "mcp-skipped", "mcp-warning", "plugin-skipped"} {
		if !slicesContains(got, want) {
			t.Errorf("advisory %q was dropped; got %v", want, got)
		}
	}
}
