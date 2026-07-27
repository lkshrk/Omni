package app

import (
	"strings"
	"testing"
)

const labelSecret = "sk-live-51ABCDEFGHIJKLMN"

// The charset guard treated any short run of name characters as a name, so a key that was itself the credential got echoed by the very message refusing to copy it.
func TestSafeKeyLabelWithholdsCredentialShapedKeys(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		key  string
		want string
	}{
		{"lowercase hyphenated vendor token", "sk-live-abc", "fallback"},
		{"vendor token at the length bound", labelSecret, "fallback"},
		{"slack style token", "xoxb-abcdef-ghijkl", "fallback"},
		{"digits are not part of a name", "s3cr3t-do-not", "fallback"},
		{"mixed separators are not a convention", "sk_live-abc", "fallback"},
		{"empty word between separators", "X--Api", "fallback"},
		{"trailing separator", "X-Api-", "fallback"},
		{"dashes with no name", "--", "fallback"},
		{"twenty-five characters", strings.Repeat("a", 25), "fallback"},

		{"header name", "Authorization", "Authorization"},
		{"hyphenated header name", "X-Api-Key", "X-Api-Key"},
		{"screaming snake env name", "GITHUB_TOKEN", "GITHUB_TOKEN"},
		{"snake query parameter", "access_token", "access_token"},
		{"camel query parameter", "apiKey", "apiKey"},
		{"flags are argv syntax a value never has", "--access-token", "--access-token"},
		{"twenty-four characters", strings.Repeat("a", 24), strings.Repeat("a", 24)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := safeKeyLabel(tc.key, "fallback"); got != tc.want {
				t.Fatalf("safeKeyLabel(%q) = %q, want %q", tc.key, got, tc.want)
			}
		})
	}
}

// The flag exemption exists for argv alone. Carried to a url query key or a header name it would become a
// one-character bypass of the rule above, since neither surface spells a name with a leading dash.
func TestSafeNameLabelRejectsTheFlagForm(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"-sk-live-abc", "--sk-live-abc", "--access-token"} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			if got := safeNameLabel(key, "fallback"); got != "fallback" {
				t.Fatalf("safeNameLabel(%q) = %q, want the ordinal", key, got)
			}
		})
	}
	refusal := mcpURLPassthrough("srv", "https://mcp.example.com/mcp?-sk-live-abc=1")
	if refusal == "" || strings.Contains(refusal, "sk-live-abc") {
		t.Fatalf("refusal = %q, want a refusal that withholds the dashed key", refusal)
	}
}

// The label is reached through two surfaces; a guard that only holds in isolation would still leak.
func TestCredentialShapedKeyNeverReachesARefusal(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		refusal string
		secret  string
	}{
		{
			name:    "url query key is the credential",
			refusal: mcpURLPassthrough("srv", "https://mcp.example.com/mcp?"+labelSecret+"=1"),
			secret:  labelSecret,
		},
		{
			name:    "header spec names the credential",
			refusal: mcpCommandPassthrough("srv", "npx -y mcp-remote -H sk-live-abc:x"),
			secret:  "sk-live-abc",
		},
		{
			name:    "inline assignment key is the credential",
			refusal: mcpCommandPassthrough("srv", "npx -y srv "+labelSecret+"=1"),
			secret:  labelSecret,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.refusal == "" {
				t.Fatalf("surface adopted a credential-shaped key; want a refusal")
			}
			if strings.Contains(tc.refusal, tc.secret) {
				t.Fatalf("refusal echoes the key it refused: %s", tc.refusal)
			}
		})
	}
}

// Header names arrive from an adapter parsing another CLI's report, so the header surface needs the
// same dashed-name guard the url surface has: the argv flag exemption would print the token here.
func TestHeaderRefusalWithholdsDashLeadingHeaderNames(t *testing.T) {
	t.Parallel()
	refusal := mcpHeaderPassthrough("srv", map[string]string{"-sk-live-abc": "literal"})
	if refusal == "" {
		t.Fatal("resolved header with a dashed name was adopted; want a refusal")
	}
	if strings.Contains(refusal, "sk-live-abc") {
		t.Fatalf("refusal echoes the dashed header name it reported: %s", refusal)
	}
	if !strings.Contains(refusal, "#1") {
		t.Fatalf("refusal = %q, want the ordinal in place of the withheld name", refusal)
	}
}

// getopt gives the token after -H to -H whatever it starts with, so a dash-leading header name used to
// end the span before it began: the spec came out empty, the credential pass saw no "=" and no "://",
// and "Bearer sk-live-xyz" was adopted into settings.json without a refusal or a warning.
func TestScanMcpCommandRefusesDashLeadingHeaderNameAfterFlag(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		command string
		secret  string
	}{
		{"short flag", "npx -y srv -H -Authorization: Bearer sk-live-xyz", "sk-live-xyz"},
		{"long flag", "npx -y srv --header -Authorization: Bearer sk-live-xyz", "sk-live-xyz"},
		{"dashed name carries the value inline", "npx -y srv -H -X-Api-Key:sk-live-xyz", "sk-live-xyz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			refusal := mcpCommandPassthrough("srv", tc.command)
			if refusal == "" {
				t.Fatalf("command %q was adopted verbatim; want a refusal", tc.command)
			}
			if strings.Contains(refusal, tc.secret) {
				t.Fatalf("refusal echoes the secret it refused: %s", refusal)
			}
		})
	}
}

// The span fix must not start refusing a header spec that defers to the environment.
func TestScanMcpCommandStillAdoptsEnvReferencedDashLeadingHeader(t *testing.T) {
	t.Parallel()
	if refusal := mcpCommandPassthrough("srv", "npx -y srv -H -Authorization: Bearer ${API_TOKEN}"); refusal != "" {
		t.Fatalf("refusal = %q, want adoption for a header that defers to the environment", refusal)
	}
}

// A resolved header still has to name itself, or the refusal stops being actionable.
func TestSafeKeyLabelStillNamesOrdinaryHeaders(t *testing.T) {
	t.Parallel()
	refusal := mcpHeaderPassthrough("srv", map[string]string{"Authorization": "Bearer literal"})
	if !strings.Contains(refusal, "Authorization") {
		t.Fatalf("refusal = %q, want it to name the header", refusal)
	}
}
