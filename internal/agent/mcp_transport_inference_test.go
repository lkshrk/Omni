package agent

import "testing"

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
