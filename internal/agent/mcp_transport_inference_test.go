package agent

import "testing"

// grok's Add installs --transport sse verbatim, so a version whose list output names no transport at all
// leaves omni inferring http. Recording that as a report would make every such server drift forever.
func TestParseGrokMcpList_MarksUnreportedTransportInferred(t *testing.T) {
	t.Parallel()
	got, err := parseGrokMcpList(`[
	  {"name":"unreported","url":"https://d.example.com/mcp"},
	  {"name":"blank","type":"  ","transport":"","url":"https://e.example.com/mcp"},
	  {"name":"typed-http","type":"http","url":"https://c.example.com/mcp"},
	  {"name":"typed-sse","type":"sse","url":"https://a.example.com/mcp"},
	  {"name":"transport-sse","transport":"SSE","url":"https://b.example.com/mcp"},
	  {"name":"stdio-srv","command":"npx","args":["-y","pkg"]}
	]`)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []bool{true, true, false, false, false, false} {
		if got[i].TransportInferred != want {
			t.Fatalf("%s TransportInferred = %v, want %v", got[i].Name, got[i].TransportInferred, want)
		}
	}
	for i, want := range []string{"http", "http", "http", "sse", "sse", "stdio"} {
		if got[i].Transport != want {
			t.Fatalf("%s transport = %q, want %q", got[i].Name, got[i].Transport, want)
		}
	}
}

func TestParseClaudeConfigMcpServers_MarksUnreportedTransportInferred(t *testing.T) {
	t.Parallel()
	got, err := parseClaudeConfigMcpServers([]byte(`{"mcpServers":{
	  "no-type": {"url":"https://a.example.com/mcp"},
	  "typed-http": {"type":"http","url":"https://b.example.com/mcp"},
	  "typed-sse": {"type":"sse","url":"https://c.example.com/sse"},
	  "stdio": {"command":"npx","args":["-y","pkg"]}
	}}`))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"no-type": true, "typed-http": false, "typed-sse": false, "stdio": false}
	for _, s := range got {
		if s.TransportInferred != want[s.Name] {
			t.Fatalf("%s TransportInferred = %v, want %v", s.Name, s.TransportInferred, want[s.Name])
		}
	}
}

// codex names the transport in every entry and the parser passes an unrecognised one through verbatim rather than guessing, so nothing it reports may claim the exemption.
func TestParseCodexMcpList_NeverInfersTransport(t *testing.T) {
	t.Parallel()
	got, err := parseCodexMcpList(`[
	  {"name":"http-srv","transport":{"type":"streamable_http","url":"https://a.example.com/mcp"}},
	  {"name":"stdio-srv","transport":{"type":"stdio","command":"echo","args":["hi"]}},
	  {"name":"other","transport":{"type":"sse","url":"https://b.example.com/sse"}}
	]`)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range got {
		if s.TransportInferred {
			t.Fatalf("%s claims an inferred transport; codex reports every type", s.Name)
		}
	}
}
