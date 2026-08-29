package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
)

func TestPrintAgentsOwnedChildrenFixReport(t *testing.T) {
	report := app.AgentsOwnedChildrenFixReport{
		Removed: []app.AgentsOwnedChildFix{{Kind: "MCP", Name: "owned-mcp", Owner: "bundle-a"}},
		Kept:    []app.AgentsOwnedChildFix{{Kind: "MCP", Name: "context-mode", Owner: "context-mode", Fields: []string{"command"}}},
	}
	var out bytes.Buffer
	printAgentsOwnedChildrenFixReport(&out, report, true)
	got := out.String()
	for _, want := range []string{
		"would remove standalone MCP owned-mcp: provided identically by bundle-a",
		"kept standalone MCP context-mode: conflicts with package context-mode (command differs)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}
