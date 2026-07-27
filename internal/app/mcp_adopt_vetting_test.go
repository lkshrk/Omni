package app

import (
	"context"
	"os"
	"strings"
	"testing"
)

const vettingSecret = "s3cr3t-do-not-commit-9f2c"

// Shape heuristics were wrong in both directions at once: "X-Auth: alice:s3cr3t!" was adopted silently
// while "X-Workspace: engineering-platform-team-us" was refused. No guess is made now — every resolved
// header refuses alike, and only a value deferring to the environment is clean.
func TestMcpHeaderPassthrough(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		header  string
		value   string
		refused bool
	}{
		{"short credential under an unconventional name", "X-Auth", "alice:s3cr3t!", true},
		{"unconventional name, no punctuation", "X-Auth", "hunter2", true},
		{"trailing space in the name no longer matters", "Authorization ", "hunter2", true},
		{"composite value with an embedded token", "X-Custom", "user=admin;token=SUPERSECRET1234567890abcd", true},
		{"long benign value refuses by the same rule, not by entropy", "X-Workspace", "engineering-platform-team-us", true},
		{"aki prefix no longer decides anything", "X-Locale", "akita-jp", true},
		{"ordinary config header", "X-Client-Name", "omni-desktop-integration", true},
		{"version string", "X-Client-Version", "2024.11.03-stable-build", true},

		{"bearer-prefixed reference is the common mcp shape", "Authorization", "Bearer ${GITHUB_TOKEN}", false},
		{"bare reference", "Authorization", "${GITHUB_TOKEN}", false},
		{"single-letter variable", "X-Api-Key", "${K}", false},
		{"reference with an opaque tail could be carrying anything", "Authorization", "Bearer ${TOKEN}-suffix-abc123", true},
		{"one reference does not launder the rest of the value", "Cookie", "session=" + vettingSecret + "; theme=${THEME}", true},
		{"empty value carries nothing", "X-Empty", "", false},

		{"malformed empty reference is not a reference", "Authorization", "${}", true},
		{"variable may not start with a digit", "Authorization", "${1BAD}", true},
		{"unterminated reference", "Authorization", "${TOKEN", true},
		{"bare dollar is not the manifest syntax", "Authorization", "$TOKEN", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			refusal := mcpHeaderPassthrough("srv", map[string]string{tc.header: tc.value})
			if tc.refused != (refusal != "") {
				t.Fatalf("header %q = %q gave refusal %q, refused want %v", tc.header, tc.value, refusal, tc.refused)
			}
			if label := safeKeyLabel(tc.header, "#1"); tc.refused && !strings.Contains(refusal, label) {
				t.Fatalf("refusal = %q, want it to name %q", refusal, label)
			}
			if strings.Contains(refusal, tc.value) && tc.value != "" {
				t.Fatalf("refusal = %q, want the value left out", refusal)
			}
		})
	}
}

// A reported header used to be the one surface that adopted a literal with only an advisory, on the
// theory that `agents mcp add --header` was an equally unvetted door. `--command` is unvetted too, so
// the split matched no principle: adoption is done to the user and must never copy a value they did
// not type, whichever surface an adapter happened to report it on.
func TestMcpAdoptCheckRefusesReportedLiteralHeaders(t *testing.T) {
	t.Parallel()
	_, refusals := (&App{}).McpAdoptCheck(InstalledMcpServer{
		Name: "srv", Transport: "http", URL: "https://x.example.com/mcp", HeadersKnown: true,
		Headers: map[string]string{"X-Import": "literal-value", "X-Env": "${TOKEN}"},
	})
	if len(refusals) != 1 || !strings.Contains(refusals[0], "X-Import") {
		t.Fatalf("refusals = %v, want the resolved header refused and named", refusals)
	}
	if strings.Contains(refusals[0], "X-Env") {
		t.Fatalf("refusals = %v, want the reference-bearing header left out", refusals)
	}
	if strings.Contains(refusals[0], "literal-value") {
		t.Fatalf("refusals = %v, want the value left out", refusals)
	}
}

// Whole-value-strict refused "Bearer ${TOKEN}", the very form the refusal message asks for; plain
// substring matching then adopted "session=<secret>; theme=${THEME}", laundering a resolved credential
// past the carve-out because one valid reference sat somewhere in it. The line is drawn on the
// remainder: strip the references and what is left must be structure that cannot hold a value.
func TestValueDefersToEnvironment(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		value string
		want  bool
	}{
		{"bare reference", "${TOKEN}", true},
		{"the standard mcp auth shape", "Bearer ${TOKEN}", true},
		{"lowercase scheme keyword", "bearer ${TOKEN}", true},
		{"basic scheme", "Basic ${B}", true},
		{"token scheme", "Token ${T}", true},
		{"digest scheme", "Digest ${D}", true},
		{"references joined by a separator", "${A}/${B}", true},
		{"references joined by a hyphen", "${A}-${B}", true},
		{"surrounding whitespace is structure", "  ${TOKEN} ", true},
		{"a malformed reference beside a real one carries nothing", "${}${REAL}", true},

		{"cookie pair carrying a resolved credential", "session=" + vettingSecret + "; theme=${THEME}", false},
		{"credential attached to a bearer reference", "Bearer " + vettingSecret + " ${PATH}", false},
		{"reference with an opaque tail", "Bearer ${TOKEN}-suffix-abc123", false},
		{"reference with an opaque head", "abc123-${TOKEN}", false},
		{"a bare word is not a scheme keyword", "theme ${THEME}", false},
		{"digits are content, not structure", "${HOST}:8080", false},
		{"non-ascii glue is refused rather than reasoned about", "${A}雪", false},

		{"malformed empty reference", "${}", false},
		{"variable may not start with a digit", "${1BAD}", false},
		{"hyphen is not a variable name character", "${BAD-NAME}", false},
		{"unterminated reference", "${TOKEN", false},
		{"truncated syntax", "${", false},
		{"bare dollar is not the manifest syntax", "$TOKEN", false},
		{"no reference at all", "plain-value", false},
		{"empty carries no reference", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := valueDefersToEnvironment(tc.value); got != tc.want {
				t.Fatalf("valueDefersToEnvironment(%q) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

// The scheme vocabulary was exactly bearer|basic|token|digest, so "DPoP ${TOKEN}" and "Negotiate
// ${TOKEN}" — fully referenced values carrying nothing at all — refused as though they held a secret.
// Only the word list widened: a run must still match one of the words in its entirety, which is why
// "oauth2" and "apikeybearer" are single runs that match nothing and refuse exactly as before.
// SCRAM-SHA-256 stays refused by decision, not oversight: admitting it means admitting the bare digit
// run "256", and refusing digit runs is what keeps hex and base64 chunks out.
func TestMcpAdoptCheckAcceptsTheWiderSchemeVocabulary(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		value string
		want  adoptVerdict
	}{
		{"dpop scheme", "DPoP ${TOKEN}", verdictSilent},
		{"negotiate scheme", "Negotiate ${TOKEN}", verdictSilent},
		{"ntlm scheme", "NTLM ${TOKEN}", verdictSilent},
		{"oauth scheme", "OAuth ${TOKEN}", verdictSilent},
		{"apikey scheme", "ApiKey ${TOKEN}", verdictSilent},

		{"a digit fuses into the run, which is then not the scheme word", "oauth2 ${T}", verdictRefused},
		{"apikey with a digit is one run, not the keyword", "apikey2 ${T}", verdictRefused},
		{"adjacent keywords are one run, not two words", "${A}basicbearer", verdictRefused},
		{"apikey fused to a keyword is still one run", "${A}apikeybearer", verdictRefused},
		{"digit-bearing scheme stays out so bare digit runs stay refused", "SCRAM-SHA-256 ${T}", verdictRefused},
		{"a port is a digit run, not structure", "${HOST}:8080", verdictRefused},
		{"an opaque tail is content whichever scheme precedes it", "Bearer ${TOKEN}-suffix-abc123", verdictRefused},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, messages := adoptVerdictFor(t, headerServer(tc.value))
			if got != tc.want {
				t.Fatalf("value %q gave verdict %s, want %s (messages: %s)", tc.value, got, tc.want, messages)
			}
		})
	}
}

// The predicate decides adoption on every surface the manifest copies, so the laundering shapes are
// pinned end to end as well: a value that reaches settings.json is the only thing that matters.
func TestMcpAdoptCheckRefusesLaunderedCredentials(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		server InstalledMcpServer
		want   adoptVerdict
	}{
		{"cookie pair beside a reference", headerServer("session=" + vettingSecret + "; theme=${THEME}"), verdictRefused},
		{"credential beside a reference", headerServer("Bearer " + vettingSecret + " ${PATH}"), verdictRefused},
		{"url query pair beside a reference",
			urlServer("https://h/mcp?key=" + vettingSecret + "-${SUFFIX}"), verdictRefused},
		{"url fragment pair beside a reference",
			urlServer("https://h/mcp#session=" + vettingSecret + "&x=${Y}"), verdictRefused},
		{"argv header beside a reference",
			argvServer("npx -y mcp-remote --header Cookie: session=" + vettingSecret + ";theme=${THEME}"), verdictRefused},
		{"argv assignment beside a reference",
			argvServer("npx -y srv --access-token=" + vettingSecret + "-${SUFFIX}"), verdictRefused},
		{"argv credential flag beside a reference",
			argvServer("npx -y srv --api-key " + vettingSecret + "-${SUFFIX}"), verdictRefused},

		{"bearer reference stays the carve-out", headerServer("Bearer ${TOKEN}"), verdictSilent},
		{"url query reference", urlServer("https://h/mcp?key=${TOKEN}"), verdictSilent},
		{"url fragment reference", urlServer("https://h/mcp#${FRAGMENT}"), verdictSilent},
		{"argv header reference",
			argvServer("npx -y mcp-remote --header Authorization: Bearer ${TOKEN}"), verdictSilent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, messages := adoptVerdictFor(t, tc.server)
			if got != tc.want {
				t.Fatalf("verdict = %s, want %s (messages: %s)", got, tc.want, messages)
			}
			if strings.Contains(messages, vettingSecret) {
				t.Fatalf("message echoes the secret: %s", messages)
			}
		})
	}
}

// An empty value carries no secret, so it adopts — but the four surfaces disagreed, argv alone calling
// it "a resolved value". Uniformity is the whole point of the policy, so all four are asserted together.
func TestMcpAdoptCheckEmptyValueVerdictIsUniformAcrossSurfaces(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		server InstalledMcpServer
	}{
		{"reported header", headerServer("")},
		{"url query parameter", urlServer("https://h/mcp?Authorization=")},
		{"argv long header flag", argvServer("npx -y mcp-remote https://h/mcp --header Authorization:")},
		{"argv short header flag", argvServer("npx -y srv -H Authorization:")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got, messages := adoptVerdictFor(t, tc.server); got != verdictSilent {
				t.Fatalf("verdict = %s, want %s (messages: %s)", got, verdictSilent, messages)
			}
		})
	}
}

// "--header=X: …" is an assignment before it is a header spec, so argv's assignment rule refuses it for
// the same reason it refuses "--port=3000", whatever the value after the colon is. That is stricter than
// the spaced spelling and is pinned rather than fixed: excluding header flags from the assignment scan is
// how the scan was twice made to swallow the tokens after it and stop refusing real credentials.
func TestScanArgvCredentialsRefusesTheInlineHeaderSpellingWhateverItCarries(t *testing.T) {
	t.Parallel()
	for _, command := range []string{
		"npx -y srv --header=Authorization:",
		"npx -y srv --header=Authorization: Bearer ${TOK}",
	} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			if refusal := mcpCommandPassthrough("srv", command); refusal == "" {
				t.Fatalf("command %q was adopted; want the assignment rule to refuse it", command)
			}
		})
	}
}

// The verdict is only worth as much as the file it protects: the laundering shape reached settings.json
// verbatim because the adopt write copies the reported headers map, so the write is asserted too.
func TestAdoptUnmanagedMcpServers_RefusesACredentialLaunderedByAReference(t *testing.T) {
	a := adoptEnvTestApp(t,
		[]InstalledMcpServer{{
			Name: "leaky", Transport: "http", HeadersKnown: true, URL: "https://h/mcp",
			Headers: map[string]string{"Cookie": "session=" + vettingSecret + "; theme=${THEME}"},
		}},
		nil)

	res, err := a.AdoptUnmanagedMcpServers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Adopted != 0 || len(res.Skipped) != 1 {
		t.Fatalf("result = %+v, want the laundered credential refused", res)
	}
	assertManifestFreeOfSecret(t, a)
}

// A spec the next flag cut short is not an empty value: the dash that ended it is the secret's own leading character, so the empty-value carve-out must not reach it.
func TestScanArgvHeaderSpecsRefusesATruncatedSpec(t *testing.T) {
	t.Parallel()
	for _, command := range []string{
		"npx mcp-remote --header X-Api-Key: -" + vettingSecret,
		"npx mcp-remote --header X-Api-Key: -- " + vettingSecret,
		"npx -y srv -H X-Api-Key: --api-key " + vettingSecret,
	} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			refusal := mcpCommandPassthrough("srv", command)
			if refusal == "" {
				t.Fatalf("command %q was adopted verbatim; want a refusal", command)
			}
			if strings.Contains(refusal, vettingSecret) {
				t.Fatalf("refusal echoes the secret: %s", refusal)
			}
		})
	}
}

// A query-string api key is the single most common mcp credential carrier and used to be copied into settings.json untouched, because only headers and env were ever vetted.
func TestMcpURLPassthrough(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		url     string
		refused bool
	}{
		{"bare url carries nothing", "https://mcp.linear.app/mcp", false},
		{"no url at all", "", false},
		{"api key in the query string", "https://mcp.linear.app/mcp?apiKey=lin_api_9fK2xyz", true},
		{"unconventional parameter name is still refused", "https://mcp.example.com/mcp?w=abc", true},
		{"userinfo credentials", "https://user:hunter2@mcp.example.com/mcp", true},
		{"userinfo without a password", "https://tokenvalue@mcp.example.com/mcp", true},
		{"env reference in the query string", "https://mcp.example.com/mcp?apiKey=${LINEAR_KEY}", false},
		{"empty parameter value", "https://mcp.example.com/mcp?trace=", false},
		{"undecodable query is refused rather than silently dropped", "https://mcp.example.com/mcp?%zz=abc", true},
		{"token hidden in the fragment", "https://mcp.example.com/mcp#tok=abc123", true},
		{"valueless query key is the credential itself", "https://mcp.example.com/sse?sk-live-abc", true},
		{"valueless env reference", "https://mcp.example.com/sse?${LINEAR_KEY}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			refusal := mcpURLPassthrough("srv", tc.url)
			if tc.refused != (refusal != "") {
				t.Fatalf("mcpURLPassthrough(%q) = %q, refused want %v", tc.url, refusal, tc.refused)
			}
		})
	}
}

// A token passed as argv is the other carrier the filter never looked at.
func TestMcpCommandPassthrough(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		command string
		refused bool
	}{
		{"plain npx invocation", "npx -y @modelcontextprotocol/server-filesystem /srv/data", false},
		{"no command at all", "", false},
		{"docker passing a variable by name", "docker run -i --rm -e GITHUB_TOKEN mcp/github", false},
		{"assignment deferring to the environment", "npx -y srv --access-token=${SENTRY_TOKEN}", false},
		{"empty assignment", "npx -y srv --trace=", false},

		{"bearer flag", "npx -y supergateway --sse https://h/sse --oauth2Bearer sk-live-abc", true},
		{"jwt flag", "npx -y srv --jwt eyJhbGciOiJIUzI1NiJ9.abc", true},
		{"bearer flag deferring to the environment", "npx -y srv --oauth2Bearer ${TOK}", false},
		{"inline flag assignment", "npx -y @sentry/mcp-server --access-token=sntrys_abc123", true},
		{"inline env assignment", "docker run -i --rm -e GITHUB_TOKEN=ghp_abc123 mcp/github", true},
		{"credential flag with a separate value", "npx -y srv --api-key sk-live-abc123", true},
		{"dsn with embedded userinfo", "npx -y server-postgres postgresql://u:hunter2@localhost/db", true},
		{"ordinary flag assignment is refused by the same rule", "npx -y srv --port=3000", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			refusal := mcpCommandPassthrough("srv", tc.command)
			if tc.refused != (refusal != "") {
				t.Fatalf("mcpCommandPassthrough(%q) = %q, refused want %v", tc.command, refusal, tc.refused)
			}
		})
	}
}

// A refusal is printed to the user and may be pasted into an issue, so naming the offending key is the
// most it may ever do. A dsn is the trap: cutting it on "=" yields a key that still holds the password.
func TestMcpAdoptCheckRefusalsNeverEchoTheValue(t *testing.T) {
	t.Parallel()
	for _, s := range []InstalledMcpServer{
		{Name: "q", Transport: "http", URL: "https://x.example.com/mcp?apiKey=" + vettingSecret, HeadersKnown: true},
		{Name: "u", Transport: "http", URL: "https://user:" + vettingSecret + "@x.example.com/mcp", HeadersKnown: true},
		{Name: "c", Transport: "stdio", Command: "npx -y srv --access-token=" + vettingSecret},
		{Name: "d", Transport: "stdio", Command: "npx -y srv postgresql://u:" + vettingSecret + "@h/db?sslmode=require"},
		{Name: "f", Transport: "stdio", Command: "npx -y srv --api-key " + vettingSecret},
		{Name: "e", Transport: "stdio", Command: "npx -y srv", EnvLiteral: map[string]string{"TOKEN": vettingSecret}},
		{Name: "h", Transport: "http", URL: "https://x.example.com/mcp", HeadersKnown: true,
			Headers: map[string]string{"Authorization": "Bearer " + vettingSecret}},
	} {
		t.Run(s.Name, func(t *testing.T) {
			t.Parallel()
			_, refusals := (&App{}).McpAdoptCheck(s)
			if len(refusals) == 0 {
				t.Fatalf("server %+v was adopted; want a refusal", s)
			}
			joined := strings.Join(refusals, "\n")
			if strings.Contains(joined, vettingSecret) {
				t.Fatalf("refusal echoes the secret: %s", joined)
			}
		})
	}
}

// No refusal may quote the value it is complaining about.
func TestMcpAdoptCheckVetsEverySurfaceTheManifestCopies(t *testing.T) {
	t.Parallel()
	_, refusals := (&App{}).McpAdoptCheck(InstalledMcpServer{
		Name: "everything", Transport: "http", HeadersKnown: true,
		URL:        "https://x.example.com/mcp?apiKey=" + vettingSecret,
		Command:    "npx -y srv --access-token=" + vettingSecret,
		Headers:    map[string]string{"Authorization": vettingSecret},
		EnvLiteral: map[string]string{"TOKEN": vettingSecret},
	})
	if len(refusals) != 4 {
		t.Fatalf("refusals = %v, want one per copied surface (headers, url, command, env)", refusals)
	}
	if joined := strings.Join(refusals, "\n"); strings.Contains(joined, vettingSecret) {
		t.Fatalf("message echoes the secret: %s", joined)
	}
}

// The credential filter guarded headers and env while the manifest write copied URL verbatim, so a token in the query string reached settings.json with no refusal at all.
func TestAdoptUnmanagedMcpServers_RefusesTokenInTheURL(t *testing.T) {
	a := adoptEnvTestApp(t,
		[]InstalledMcpServer{{
			Name: "linear", Transport: "http", HeadersKnown: true,
			URL: "https://mcp.linear.app/mcp?apiKey=" + vettingSecret,
		}},
		nil)

	res, err := a.AdoptUnmanagedMcpServers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Adopted != 0 || len(res.Skipped) != 1 {
		t.Fatalf("result = %+v, want the url token refused", res)
	}
	assertManifestFreeOfSecret(t, a)
}

func TestAdoptUnmanagedMcpServers_RefusesTokenInTheCommand(t *testing.T) {
	a := adoptEnvTestApp(t,
		[]InstalledMcpServer{{
			Name: "sentry", Transport: "stdio",
			Command: "npx -y @sentry/mcp-server --access-token=" + vettingSecret,
		}},
		nil)

	res, err := a.AdoptUnmanagedMcpServers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Adopted != 0 || len(res.Skipped) != 1 {
		t.Fatalf("result = %+v, want the argv token refused", res)
	}
	assertManifestFreeOfSecret(t, a)
}

func TestAdoptUnmanagedMcpServers_AdoptsEnvReferencingHeaderAndURL(t *testing.T) {
	a := adoptEnvTestApp(t,
		[]InstalledMcpServer{{
			Name: "gh", Transport: "http", HeadersKnown: true,
			URL:     "https://api.githubcopilot.com/mcp?profile=${GH_PROFILE}",
			Headers: map[string]string{"Authorization": "Bearer ${GITHUB_TOKEN}"},
		}},
		nil)

	res, err := a.AdoptUnmanagedMcpServers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Adopted != 1 || len(res.Skipped) != 0 {
		t.Fatalf("result = %+v, want the reference-bearing server adopted", res)
	}
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	adopted := findMcpServer(cfg.Agents.McpServers, "gh")
	if adopted == nil || adopted.Headers["Authorization"] != "Bearer ${GITHUB_TOKEN}" {
		t.Fatalf("adopted = %+v, want the reference carried through unchanged", adopted)
	}
}

// A dry run that previewed a claim the real path would refuse would be a licence to commit a secret.
func TestAdoptUnmanagedMcpServers_DryRunAppliesTheSameRefusals(t *testing.T) {
	servers := []InstalledMcpServer{
		{Name: "linear", Transport: "http", HeadersKnown: true,
			URL: "https://mcp.linear.app/mcp?apiKey=" + vettingSecret},
		{Name: "sentry", Transport: "stdio", Command: "npx -y srv --access-token=" + vettingSecret},
		{Name: "ok", Transport: "http", HeadersKnown: true, URL: "https://ok.example.com/mcp"},
	}
	dry, err := adoptEnvTestApp(t, servers, nil).adoptUnmanagedMcpServers(context.Background(), mcpAdoptOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	wet, err := adoptEnvTestApp(t, servers, nil).AdoptUnmanagedMcpServers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(dry.WouldAdopt) != 1 || wet.Adopted != 1 {
		t.Fatalf("dry = %+v, wet = %+v, want only the clean server claimed on both paths", dry, wet)
	}
	if strings.Join(dry.Skipped, "\n") != strings.Join(wet.Skipped, "\n") {
		t.Fatalf("dry refusals = %v, wet refusals = %v, want them identical", dry.Skipped, wet.Skipped)
	}
}

func assertManifestFreeOfSecret(t *testing.T, a *App) {
	t.Helper()
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.McpServers) != 0 {
		t.Fatalf("manifest = %+v, want nothing written on refusal", cfg.Agents.McpServers)
	}
	raw, err := os.ReadFile(a.ConfigPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), vettingSecret) {
		t.Fatalf("settings.json holds the secret: %s", raw)
	}
}

// "--header Authorization: Bearer …" is how stdio clients such as mcp-remote carry remote auth. argv is
// the surface a user is least likely to inspect, and it is the one whose recognition has been rewritten
// most often, so the five shapes stay pinned by name rather than only inside the differential table.
func TestMcpAdoptCheckTreatsArgvHeaderFlagsAsHeaders(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		command string
		refused bool
	}{
		{"mcp-remote long flag", `npx -y mcp-remote https://h/mcp --header Authorization: Bearer ` + vettingSecret, true},
		{"curl-style short flag", `npx -y srv -H X-Api-Key: ` + vettingSecret, true},
		{"inline assignment form", `npx -y srv --header=Authorization: Bearer ` + vettingSecret, true},
		{"header deferring to the environment", `npx -y mcp-remote https://h/mcp --header Authorization: Bearer ${TOK}`, false},
		{"bare -h stays a help flag", `npx -y srv -h`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, refusals := (&App{}).McpAdoptCheck(
				InstalledMcpServer{Name: "srv", Transport: "stdio", Command: tc.command})
			if tc.refused != (len(refusals) == 1) {
				t.Fatalf("refusals = %v, refused want %v", refusals, tc.refused)
			}
			joined := strings.Join(refusals, "\n")
			if strings.Contains(joined, vettingSecret) {
				t.Fatalf("refusal echoes the secret: %s", joined)
			}
			if tc.refused && !strings.Contains(joined, "Authorization") && !strings.Contains(joined, "X-Api-Key") {
				t.Fatalf("refusal = %q, want the header named", joined)
			}
		})
	}
}

// The url surface declared the same never-echo invariant the argv surface enforces, but applied no guard at all: a query key can be the credential outright.
func TestMcpURLPassthroughNeverEchoesACredentialBearingKey(t *testing.T) {
	t.Parallel()
	for _, rawURL := range []string{
		"https://mcp.example.com/sse?" + vettingSecret + "=1",
		"https://mcp.example.com/sse?" + vettingSecret,
		"https://mcp.example.com/sse?apiKey=" + vettingSecret,
	} {
		t.Run(rawURL, func(t *testing.T) {
			t.Parallel()
			refusal := mcpURLPassthrough("srv", rawURL)
			if refusal == "" {
				t.Fatalf("url %q was adopted; want a refusal", rawURL)
			}
			if strings.Contains(refusal, vettingSecret) {
				t.Fatalf("refusal echoes the secret: %s", refusal)
			}
		})
	}
}

// The header, url and argv surfaces all route a name through the display guard before printing it; env
// was the one that echoed the name raw, and an adapter reads that name out of another CLI's report just
// like the others, so a server whose env map is keyed by the credential itself leaked it into the message.
func TestMcpAdoptCheckNeverEchoesACredentialShapedEnvName(t *testing.T) {
	t.Parallel()
	_, refusals := (&App{}).McpAdoptCheck(InstalledMcpServer{
		Name: "srv", Transport: "stdio", Command: "npx -y srv",
		EnvLiteral: map[string]string{vettingSecret: "1", uniformitySecret: "1"},
	})
	joined := strings.Join(refusals, "\n")
	if len(refusals) != 1 {
		t.Fatalf("refusals = %v, want the env surface refused", refusals)
	}
	if strings.Contains(joined, vettingSecret) || strings.Contains(joined, uniformitySecret) {
		t.Fatalf("refusal echoes a credential-shaped env name: %s", joined)
	}
	if !strings.Contains(joined, "#1") || !strings.Contains(joined, "#2") {
		t.Fatalf("refusal = %q, want each withheld name replaced by its ordinal", joined)
	}
}

// An ordinary variable name is the actionable half of the message and must survive the guard.
func TestMcpEnvPassthroughStillNamesOrdinaryVariables(t *testing.T) {
	t.Parallel()
	_, refusal := mcpEnvPassthrough("srv", map[string]string{"GITHUB_TOKEN": "x"}, func(string) string { return "" })
	if !strings.Contains(refusal, "GITHUB_TOKEN") {
		t.Fatalf("refusal = %q, want it to name the variable", refusal)
	}
}

// A refusal is a disclosure, not advice: it has to say omni wrote nothing. The tail was once advisory
// ("settings.json is often committed, so prefer ${ENV_VAR} references") on a path that adopted the value
// anyway, and reverting to that wording left every surface's assertions green because only the shared
// prefix was ever pinned. Both halves — what omni refused to do, and what the user should do instead —
// are pinned here on every surface that can produce a passthrough refusal.
func TestMcpAdoptCheckRefusalsDiscloseThatNothingWasWritten(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		server InstalledMcpServer
	}{
		{"header", headerServer(vettingSecret)},
		{"url query", urlServer("https://h/mcp?key=" + vettingSecret)},
		{"url userinfo", urlServer("https://u:" + vettingSecret + "@h/mcp")},
		{"url fragment", urlServer("https://h/mcp#tok=" + vettingSecret)},
		{"command line", argvServer("npx -y srv --access-token=" + vettingSecret)},
		{"env", InstalledMcpServer{Name: "srv", Transport: "stdio", Command: "npx -y srv",
			EnvLiteral: map[string]string{"TOKEN": vettingSecret}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, refusals := (&App{}).McpAdoptCheck(tc.server)
			if len(refusals) == 0 {
				t.Fatalf("server %+v was adopted; want a refusal", tc.server)
			}
			for _, refusal := range refusals {
				if !strings.Contains(refusal, "omni will not copy that into settings.json") {
					t.Fatalf("refusal = %q, want it to disclose that nothing was written", refusal)
				}
				if !strings.Contains(refusal, "declare the server manually") {
					t.Fatalf("refusal = %q, want it to name the consented path", refusal)
				}
			}
		})
	}
}

// Pins the display guard itself: it is the only thing standing between a payload-shaped key and a user-facing message, on both the url and argv surfaces.
func TestSafeKeyLabel(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		key  string
		want string
	}{
		{"apiKey", "apiKey"},
		{"--access-token", "--access-token"},
		{"GITHUB_TOKEN", "GITHUB_TOKEN"},
		{"", "fallback"},
		{"sk-proj-AbCdEf0123456789AbCdEf0123456789", "fallback"},
		{"postgresql://u:pass@h/db?sslmode", "fallback"},
		{"has space", "fallback"},
		{"tok:en", "fallback"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			t.Parallel()
			if got := safeKeyLabel(tc.key, "fallback"); got != tc.want {
				t.Fatalf("safeKeyLabel(%q) = %q, want %q", tc.key, got, tc.want)
			}
		})
	}
}
