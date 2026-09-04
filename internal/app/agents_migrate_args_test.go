package app

import (
	"encoding/json"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lkshrk/omni/internal/config"
)

func TestRenderAPMTemplateKeepsStructuredArgsAndSplitsJoinedCommands(t *testing.T) {
	decls := config.LegacyAgentDecls{MCPServers: map[string]json.RawMessage{
		"structured": json.RawMessage(`{"name":"structured","transport":"stdio","command":"node","args":["/opt/My Server/index.js","--flag"],"agents":["claude"]}`),
		"joined":     json.RawMessage(`{"name":"joined","transport":"stdio","command":"npx -y demo-server","agents":["claude"]}`),
	}}
	rendered, _, err := RenderAPMTemplate(decls)
	if err != nil {
		t.Fatal(err)
	}
	var manifest apmManifest
	if err := yaml.Unmarshal([]byte(rendered), &manifest); err != nil {
		t.Fatalf("parse rendered manifest: %v\n%s", err, rendered)
	}
	want := map[string]apmMCPDep{
		"structured": {Command: "node", Args: []string{"/opt/My Server/index.js", "--flag"}},
		"joined":     {Command: "npx", Args: []string{"-y", "demo-server"}},
	}
	if len(manifest.Dependencies.MCP) != len(want) {
		t.Fatalf("mcp dependencies = %#v", manifest.Dependencies.MCP)
	}
	for _, dep := range manifest.Dependencies.MCP {
		expected := want[dep.Name]
		if dep.Command != expected.Command || !slices.Equal(dep.Args, expected.Args) {
			t.Fatalf("%s = %q %#v, want %q %#v", dep.Name, dep.Command, dep.Args, expected.Command, expected.Args)
		}
	}
}
