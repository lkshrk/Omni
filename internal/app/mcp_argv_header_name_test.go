package app

import (
	"strings"
	"testing"
)

const argvHeaderNameSecret = "s3cr3t-do-not-commit-9f2c"

// A header spec that runs to the end of argv with nothing after the colon carries no value, so the
// value-shaped carve-outs adopted it — and copied the name into settings.json verbatim. That let the
// credential ride in the name: "--header s3cr3t-…:" imported clean.
func TestScanMcpCommandRefusesCredentialShapedHeaderName(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		command string
		secret  string
	}{
		{"empty value at end of argv", "npx srv --header " + argvHeaderNameSecret + ":", argvHeaderNameSecret},
		{"short flag form", "npx srv -H " + argvHeaderNameSecret + ":", argvHeaderNameSecret},
		{"inline flag form", "npx srv --header=" + argvHeaderNameSecret + ":", argvHeaderNameSecret},
		{"lowercase hyphenated vendor token", "npx srv --header sk-live-abc:", "sk-live-abc"},
		{"slack style token", "npx srv --header xoxb-abcdef-ghijkl:", "xoxb-abcdef-ghijkl"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			refusal := mcpCommandPassthrough("leaky", tc.command)
			if refusal == "" {
				t.Fatalf("command %q was adopted; want a refusal", tc.command)
			}
			if strings.Contains(refusal, tc.secret) {
				t.Fatalf("refusal echoes the secret it refused: %s", refusal)
			}
			if !strings.Contains(refusal, "argument") {
				t.Fatalf("refusal = %q, want the ordinal in place of the withheld name", refusal)
			}
		})
	}
}

// The name-shape verdict must not start refusing the header names real servers actually send.
func TestScanMcpCommandStillAdoptsConventionalHeaderNames(t *testing.T) {
	t.Parallel()
	for _, header := range []string{"Authorization", "X-Api-Key", "Content-Type", "X-Trace", "X_Underscore"} {
		t.Run(header, func(t *testing.T) {
			t.Parallel()
			command := "npx srv --header " + header + ":"
			if refusal := mcpCommandPassthrough("srv", command); refusal != "" {
				t.Fatalf("command %q refused with %q; want adoption for a valueless conventional header", command, refusal)
			}
		})
	}
}

// A resolved value still refuses, and a conventional name is still named so the refusal stays actionable.
func TestScanMcpCommandStillNamesConventionalHeaderInRefusal(t *testing.T) {
	t.Parallel()
	refusal := mcpCommandPassthrough("srv", "npx srv --header X-Api-Key: sk-live-abcdef")
	if refusal == "" {
		t.Fatal("resolved header value was adopted; want a refusal")
	}
	if !strings.Contains(refusal, "X-Api-Key") {
		t.Fatalf("refusal = %q, want it to name the header", refusal)
	}
	if strings.Contains(refusal, "sk-live-abcdef") {
		t.Fatalf("refusal echoes the value it refused: %s", refusal)
	}
}

// docker's daemon socket is not a header spec, and its scheme is a conventional name, so the url-syntax carve-out must still be reachable after the name verdict.
func TestScanMcpCommandStillAdoptsDaemonSocketFlag(t *testing.T) {
	t.Parallel()
	if refusal := mcpCommandPassthrough("srv", "docker -H unix:///var/run/docker.sock run srv"); refusal != "" {
		t.Fatalf("refusal = %q, want adoption for a daemon socket", refusal)
	}
}

// The reported import: ~/.claude.json spelled the credential as a valueless header name and "omni agents mcp import leaky" exited 0, writing it into settings.json.
func TestMcpAdoptCheckRefusesCredentialShapedHeaderName(t *testing.T) {
	t.Parallel()
	verdict, messages := adoptVerdictFor(t, InstalledMcpServer{
		Name:      "leaky",
		Transport: "stdio",
		Command:   "npx srv --header " + argvHeaderNameSecret + ":",
	})
	if verdict != verdictRefused {
		t.Fatalf("verdict = %s, want %s; the secret rode into settings.json in the header name", verdict, verdictRefused)
	}
	if strings.Contains(messages, argvHeaderNameSecret) {
		t.Fatalf("refusal echoes the secret it refused: %s", messages)
	}
}
