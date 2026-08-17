package app_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func inferenceManifestServer() config.McpServer {
	return config.McpServer{
		Name:      "docs",
		Transport: "sse",
		URL:       "https://mcp.example.com/sse",
		Headers:   map[string]string{"Authorization": "Bearer ${DOCS_TOKEN}"},
		Agents:    []string{"hermes-agent"},
	}
}

func inferenceLiveServer(inferred bool) app.InstalledMcpServer {
	return app.InstalledMcpServer{
		Name:              "docs",
		Transport:         "http",
		URL:               "https://mcp.example.com/sse",
		Headers:           map[string]string{"Authorization": "Bearer stale-token"},
		HeadersKnown:      true,
		TransportInferred: inferred,
	}
}

// A cli that names no transport leaves omni inferring http for a server the manifest declares sse.
// Calling that drift is wrong twice over: the identity never differed, and the drift branch returns
// before the header pass, so a rotated ${DOCS_TOKEN} stops reaching the agent for good. The starvation
// is the damage, so the test pins convergence as well as the absent drift line.
func TestRestoreMcpServers_InferredTransportConvergesHeadersInsteadOfDrifting(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{id: "hermes-agent", available: true, listed: []app.InstalledMcpServer{inferenceLiveServer(true)}}
	a := newMcpTestApp(t,
		config.AgentsConfig{McpServers: []config.McpServer{inferenceManifestServer()}},
		app.WithMcpAdapters([]app.McpAdapter{stub}),
		app.WithPluginAdapters(nil))

	res, err := a.RestoreMcpServers(context.Background(), app.RestoreMcpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Drift) != 0 {
		t.Fatalf("drift = %v, want none for a transport the agent never reported", res.Drift)
	}
	if len(res.Updated) != 1 || res.Updated[0] != "hermes-agent/docs" {
		t.Fatalf("updated = %v, want the header pass to have run", res.Updated)
	}
	if len(stub.addedServers) != 1 || stub.addedServers[0].Headers["Authorization"] != "Bearer ${DOCS_TOKEN}" {
		t.Fatalf("added = %+v, want the manifest header reinstalled", stub.addedServers)
	}
	if len(stub.removedNames) != 1 || stub.removedNames[0] != "docs" {
		t.Fatalf("removed = %v, want the stale registration replaced", stub.removedNames)
	}
}

// The exemption is on whether the live side knows the value, never on the http/sse pair: an agent that
// does report sse against a manifest declaring http is still drift, and is still left untouched.
func TestRestoreMcpServers_ReportedTransportStillDrifts(t *testing.T) {
	t.Parallel()
	stub := &stubMcpAdapter{id: "hermes-agent", available: true, listed: []app.InstalledMcpServer{inferenceLiveServer(false)}}
	a := newMcpTestApp(t,
		config.AgentsConfig{McpServers: []config.McpServer{inferenceManifestServer()}},
		app.WithMcpAdapters([]app.McpAdapter{stub}),
		app.WithPluginAdapters(nil))

	res, err := a.RestoreMcpServers(context.Background(), app.RestoreMcpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Drift) != 1 || !strings.Contains(res.Drift[0], "transport") {
		t.Fatalf("drift = %v, want a reported transport mismatch called out", res.Drift)
	}
	if len(res.Updated) != 0 || len(stub.addedServers) != 0 || len(stub.removedNames) != 0 {
		t.Fatalf("drifted server was modified: updated=%v added=%+v removed=%v",
			res.Updated, stub.addedServers, stub.removedNames)
	}
}

// An inferred transport excuses only the flavour of a url server; a manifest entry pointing somewhere
// else entirely is still a genuine difference the url comparison must keep catching.
func TestRestoreMcpServers_InferredTransportDoesNotMaskOtherDrift(t *testing.T) {
	t.Parallel()
	live := inferenceLiveServer(true)
	live.URL = "https://elsewhere.example.com/sse"
	stub := &stubMcpAdapter{id: "hermes-agent", available: true, listed: []app.InstalledMcpServer{live}}
	a := newMcpTestApp(t,
		config.AgentsConfig{McpServers: []config.McpServer{inferenceManifestServer()}},
		app.WithMcpAdapters([]app.McpAdapter{stub}),
		app.WithPluginAdapters(nil))

	res, err := a.RestoreMcpServers(context.Background(), app.RestoreMcpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Drift) != 1 || !strings.Contains(res.Drift[0], "url") {
		t.Fatalf("drift = %v, want the url difference reported", res.Drift)
	}
	if strings.Contains(res.Drift[0], "transport") {
		t.Fatalf("drift = %v, want the unreported transport left out", res.Drift)
	}
}
