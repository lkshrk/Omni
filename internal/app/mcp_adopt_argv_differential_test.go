package app

import (
	"context"
	"strings"
	"testing"
)

type adoptVerdict string

// There is no third verdict: an adopt surface either copies the value or refuses it. A WARNED state
// existed while a reported literal header was adopted with advice, and its absence is the contract.
const (
	verdictSilent  adoptVerdict = "SILENT"
	verdictRefused adoptVerdict = "REFUSED"
)

func adoptVerdictFor(t *testing.T, s InstalledMcpServer) (adoptVerdict, string) {
	t.Helper()
	_, refusals := (&App{}).McpAdoptCheck(s)
	messages := strings.Join(refusals, "\n")
	if len(refusals) > 0 {
		return verdictRefused, messages
	}
	return verdictSilent, messages
}

// Recognising a header flag in argv has twice been implemented as a scan that consumed the tokens after
// it, which downgraded refusals the plain scan used to make: adding a benign "--header X-Trace: 1" to any
// command turned off credential detection for the rest of argv. Verdicts are asserted outright, not just
// "some message was produced", so a future edit cannot quietly demote one of these to a warning again.
func TestMcpAdoptCheckArgvVerdicts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		command string
		want    adoptVerdict
	}{
		{"header flag does not disable dsn detection",
			"npx srv --header X-Trace: 1 postgresql://admin:" + vettingSecret + "@db.example.com/app", verdictRefused},
		{"header flag does not disable url query detection",
			"npx mcp-remote --header X-Trace: 1 https://api.example.com/mcp?apikey=" + vettingSecret, verdictRefused},
		{"header flag does not disable inline assignment detection",
			"npx srv --header X-Trace: 1 API_KEY=" + vettingSecret, verdictRefused},
		{"header flag does not disable credential flag detection",
			"npx srv --header X-Trace: 1 --api-key " + vettingSecret, verdictRefused},
		{"header deferring to the environment does not disable dsn detection",
			"npx srv --header Authorization: ${TOK} postgresql://admin:" + vettingSecret + "@db/app", verdictRefused},

		{"dsn with no header flag present",
			"npx srv postgresql://admin:" + vettingSecret + "@db/app", verdictRefused},
		{"url query with no header flag present",
			"npx mcp-remote https://api.example.com/mcp?apikey=" + vettingSecret, verdictRefused},
		{"inline assignment with no header flag present",
			"npx srv API_KEY=" + vettingSecret, verdictRefused},
		{"credential flag with no header flag present",
			"npx srv --api-key " + vettingSecret, verdictRefused},

		{"mcp-remote remote-auth recipe",
			"npx -y mcp-remote https://h/mcp --header Authorization: Bearer " + vettingSecret, verdictRefused},
		{"inline header assignment",
			"npx -y srv --header=Authorization: Bearer " + vettingSecret, verdictRefused},
		{"curl-style short flag",
			"npx -y srv -H X-Api-Key: " + vettingSecret, verdictRefused},
		{"dash-prefixed header value is not read as carrying nothing",
			"npx mcp-remote --header X-Api-Key: -" + vettingSecret, verdictRefused},
		{"double dash before the header value",
			"npx mcp-remote --header X-Api-Key: -- " + vettingSecret, verdictRefused},
		{"a clean first header does not end the scan",
			"npx srv --header X-A: ${TOK} --header X-B: " + vettingSecret, verdictRefused},

		{"header deferring to the environment",
			"npx -y mcp-remote https://h/mcp --header Authorization: Bearer ${TOK}", verdictSilent},
		{"bare -h is a host flag far more often than a header flag",
			"mysql-mcp -h db.example.com:3306 -u root", verdictSilent},
		{"bare -h with no argument stays a help flag",
			"npx -y srv -h", verdictSilent},
		{"docker -H names a daemon socket rather than a header",
			"docker -H unix:///var/run/docker.sock run -i --rm mcp/github", verdictSilent},
		{"a header value that is a url is still a header",
			"npx -y srv -H X-Origin: https://app.example.com", verdictRefused},
		{"plain invocation",
			"npx -y @modelcontextprotocol/server-filesystem /srv/data", verdictSilent},
		{"variable passed by name",
			"docker run -i --rm -e GITHUB_TOKEN mcp/github", verdictSilent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, messages := adoptVerdictFor(t, InstalledMcpServer{
				Name: "srv", Transport: "stdio", Command: tc.command})
			if got != tc.want {
				t.Fatalf("command %q gave %s, want %s (messages: %s)", tc.command, got, tc.want, messages)
			}
			if strings.Contains(messages, vettingSecret) {
				t.Fatalf("command %q echoed the secret: %s", tc.command, messages)
			}
		})
	}
}

// A header name reaches the user in a message on both surfaces, and on the argv surface it is parsed out of unstructured text, so it can be the payload itself rather than a name.
func TestMcpAdoptCheckNeverEchoesACredentialShapedHeaderName(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		server InstalledMcpServer
		want   adoptVerdict
	}{
		{"parsed out of argv", InstalledMcpServer{
			Name: "argv", Transport: "stdio", Command: "npx -y srv -H " + vettingSecret + ":x"}, verdictRefused},
		{"reported by an agent", InstalledMcpServer{
			Name: "reported", Transport: "http", URL: "https://x.example.com/mcp", HeadersKnown: true,
			Headers: map[string]string{vettingSecret: "literal-value"}}, verdictRefused},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, messages := adoptVerdictFor(t, tc.server)
			if got != tc.want {
				t.Fatalf("verdict = %s, want %s (messages: %s)", got, tc.want, messages)
			}
			if strings.Contains(messages, vettingSecret) {
				t.Fatalf("message echoes the credential-shaped header name: %s", messages)
			}
		})
	}
}

// Each surface is vetted independently, so a server carrying a benign header next to a credential must
// still be refused whole rather than adopted for the surface that happened to look clean.
func TestAdoptUnmanagedMcpServers_OneRefusedSurfaceRefusesTheServer(t *testing.T) {
	for _, tc := range []struct {
		name        string
		server      InstalledMcpServer
		wantSkipped int
	}{
		{"argv header alongside a reported one", InstalledMcpServer{
			Name: "remote", Transport: "stdio", HeadersKnown: true,
			Headers: map[string]string{"X-Import": "literal-value"},
			Command: "npx -y mcp-remote https://h/mcp --header Authorization: Bearer " + vettingSecret}, 2},
		{"benign header alongside a dsn", InstalledMcpServer{
			Name: "remote", Transport: "stdio",
			Command: "npx srv --header X-Trace: 1 postgresql://admin:" + vettingSecret + "@db/app"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := adoptEnvTestApp(t, []InstalledMcpServer{tc.server}, nil)
			res, err := a.AdoptUnmanagedMcpServers(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if res.Adopted != 0 || len(res.Skipped) != tc.wantSkipped {
				t.Fatalf("result = %+v, want the server refused with %d refusal(s)", res, tc.wantSkipped)
			}
			if len(res.Warnings) != 0 {
				t.Fatalf("Warnings = %v, want no adopt path to produce an advisory", res.Warnings)
			}
			assertManifestFreeOfSecret(t, a)
		})
	}
}
