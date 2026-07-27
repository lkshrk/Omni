package app

import (
	"strings"
	"testing"
)

const uniformitySecret = "sk-live-ABC-do-not-commit"

func headerServer(value string) InstalledMcpServer {
	return InstalledMcpServer{
		Name: "srv", Transport: "http", URL: "https://h/mcp", HeadersKnown: true,
		Headers: map[string]string{"Authorization": value},
	}
}

func argvServer(command string) InstalledMcpServer {
	return InstalledMcpServer{Name: "srv", Transport: "stdio", Command: command}
}

func urlServer(rawURL string) InstalledMcpServer {
	return InstalledMcpServer{Name: "srv", Transport: "http", URL: rawURL, HeadersKnown: true}
}

// The same secret used to get two verdicts depending only on how an adapter reported it: a value in a
// reported Headers map was adopted with an advisory, while the identical value behind `--header` in argv
// was refused. The verdict belongs to the value, not to the carrier — a resolved value refuses on every
// adopt surface and a ${VAR} reference adopts clean on every one. Both halves are asserted together,
// because a rule enforced in one direction only is how the split arose in the first place.
func TestMcpAdoptCheckVerdictIsUniformAcrossSurfaces(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		server InstalledMcpServer
		want   adoptVerdict
	}{
		{"reported header, bare reference", headerServer("${TOKEN}"), verdictSilent},
		{"reported header, bearer-prefixed reference", headerServer("Bearer ${TOKEN}"), verdictSilent},
		{"argv header, bearer-prefixed reference",
			argvServer("npx -y mcp-remote https://h/mcp --header Authorization: Bearer ${TOKEN}"), verdictSilent},
		{"url query, reference", urlServer("https://h/mcp?key=${TOKEN}"), verdictSilent},

		{"reported header, bearer-prefixed literal", headerServer("Bearer " + uniformitySecret), verdictRefused},
		{"reported header, bare literal", headerServer(uniformitySecret), verdictRefused},
		{"argv header, bearer-prefixed literal",
			argvServer("npx -y mcp-remote https://h/mcp --header Authorization: Bearer " + uniformitySecret), verdictRefused},
		{"url query, literal", urlServer("https://h/mcp?key=" + uniformitySecret), verdictRefused},

		{"url userinfo", urlServer("https://u:" + uniformitySecret + "@h/mcp"), verdictRefused},
		{"argv credential flag", argvServer("npx -y srv --api-key " + uniformitySecret), verdictRefused},
		{"argv dsn", argvServer("npx -y srv postgresql://u:" + uniformitySecret + "@h/db"), verdictRefused},
		{"argv inline assignment", argvServer("npx -y srv API_KEY=" + uniformitySecret), verdictRefused},
		{"env literal the environment does not hold", InstalledMcpServer{
			Name: "srv", Transport: "stdio", Command: "npx -y srv",
			EnvLiteral: map[string]string{"TOKEN": uniformitySecret}}, verdictRefused},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, messages := adoptVerdictFor(t, tc.server)
			if got != tc.want {
				t.Fatalf("verdict = %s, want %s (messages: %s)", got, tc.want, messages)
			}
			if strings.Contains(messages, uniformitySecret) {
				t.Fatalf("message echoes the secret: %s", messages)
			}
		})
	}
}

// A reference-bearing value must survive adoption verbatim, or the clean verdict would be worthless:
// the manifest has to keep deferring to the environment rather than resolving the value at claim time.
func TestMcpAdoptCheckCarriesReferencesThroughUntouched(t *testing.T) {
	t.Parallel()
	s := InstalledMcpServer{
		Name: "srv", Transport: "http", HeadersKnown: true,
		URL:     "https://h/mcp?key=${TOKEN}",
		Headers: map[string]string{"Authorization": "Bearer ${TOKEN}", "X-Api-Key": "${TOKEN}"},
	}
	if _, refusals := (&App{}).McpAdoptCheck(s); len(refusals) != 0 {
		t.Fatalf("refusals = %v, want a server that defers entirely to the environment adopted clean", refusals)
	}
}
