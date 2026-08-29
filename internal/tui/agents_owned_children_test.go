package tui

import (
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
)

func agentsOwnedChildrenModel(t *testing.T) Model {
	t.Helper()
	m := agentsRowsModel(t)
	m.agentsRows[0].Provides = []app.AgentsProvidedChild{
		{Kind: "lsp", Name: "gopls", Status: app.AgentsPackageInstalled},
		{Kind: "mcp", Name: "context-mode", Status: app.AgentsPackageInstalled},
	}
	m.agentsRows[0].Issues = []string{"MCP context-mode conflicts with a standalone declaration"}
	return m
}

func TestAgentsPackageDetailsShowOwnedChildrenAndIssues(t *testing.T) {
	m := agentsOwnedChildrenModel(t)
	view := m.viewSkillsBody()

	provides := "provides: MCP context-mode, LSP gopls"
	issues := "issues: MCP context-mode conflicts with a standalone declaration"
	for _, want := range []string{provides, issues} {
		if !strings.Contains(stripANSIEscapeSequences(view), want) {
			t.Fatalf("active package details missing %q:\n%s", want, stripANSIEscapeSequences(view))
		}
	}
	if !strings.Contains(view, m.palette.styleHelp.Render(provides)) {
		t.Fatalf("provides is not informationally styled:\n%s", view)
	}
	if !strings.Contains(view, m.palette.styleOutdated.Render(issues)) {
		t.Fatalf("issues is not warning styled:\n%s", view)
	}
}

func TestAgentsPackageSearchMatchesOwnedChildrenAndIssues(t *testing.T) {
	for _, query := range []string{"context-mode", "gopls", "standalone declaration"} {
		t.Run(query, func(t *testing.T) {
			m := agentsOwnedChildrenModel(t)
			m.openAgentsFilter()
			m.filter.SetValue(query)
			visible := m.agentsVisiblePackages()
			if len(visible) != 1 || visible[0].Name != "alpha" {
				t.Fatalf("visible packages for %q = %#v", query, visible)
			}
		})
	}
}

func TestAgentsOwnedChildDetailsWrapWithExistingIndent(t *testing.T) {
	m := agentsOwnedChildrenModel(t)
	m.width = 42
	lines := m.agentsDetailBlock(m.agentsRows[0].Description, agentsPackageDetails(m.agentsRows[0]), hintCtxAgentsRow)

	plain := stripANSIEscapeSequences(strings.Join(lines, "\n"))
	if strings.Count(plain, "\n") < 2 || !strings.Contains(plain, "standalone") || !strings.Contains(plain, "declaration") {
		t.Fatalf("owned child details did not wrap without losing content:\n%s", plain)
	}
	for _, line := range strings.Split(plain, "\n") {
		if line != "" && !strings.HasPrefix(line, listTextPrefix()) && !strings.HasPrefix(line, listHintPrefix()) {
			t.Fatalf("wrapped detail lost existing indent: %q", line)
		}
	}
}
