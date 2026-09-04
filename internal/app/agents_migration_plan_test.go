package app

import (
	"math/rand"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type wantDisposition struct {
	kind     string
	identity string
	target   string
	action   string
	owner    string
	targets  []string
	reason   string
}

func TestResolveAgentDispositionsOverFixtures(t *testing.T) {
	for _, test := range []struct {
		fixture string
		wantErr string
		want    []wantDisposition
	}{
		{fixture: "plugin-owned-child", want: []wantDisposition{
			{kind: agentKindMarketplace, identity: "ctx-market", target: "claude", action: agentActionImport},
			{kind: agentKindMCP, identity: "ctx", target: "claude", action: agentActionSuppress, owner: "ctx@ctx-market"},
			{kind: agentKindPlugin, identity: "ctx@ctx-market", target: "claude", action: agentActionImport},
		}},
		{fixture: "claude-plugin-with-mcp-json", want: []wantDisposition{
			{kind: agentKindMarketplace, identity: "acme", target: "claude", action: agentActionImport},
			{kind: agentKindMCP, identity: "tooling", target: "claude", action: agentActionSuppress, owner: "tooling@acme"},
			{kind: agentKindPlugin, identity: "tooling@acme", target: "claude", action: agentActionImport},
		}},
		{fixture: "standalone-matches-package-name", want: []wantDisposition{
			{kind: agentKindMarketplace, identity: "official", target: "claude", action: agentActionRetain, reason: "marketplace sources differ across targets"},
			{kind: agentKindMarketplace, identity: "official", target: "codex", action: agentActionRetain, reason: "marketplace sources differ across targets"},
			{kind: agentKindMCP, identity: "demo", target: "claude", action: agentActionImport, targets: []string{"claude"}},
			{kind: agentKindMCP, identity: "demo", target: "codex", action: agentActionRetain, reason: agentReasonPerTarget},
			{kind: agentKindPlugin, identity: "demo@official", target: "claude", action: agentActionRetain, reason: "marketplace sources differ across targets"},
			{kind: agentKindPlugin, identity: "demo@official", target: "codex", action: agentActionRetain, reason: "marketplace sources differ across targets"},
		}},
		{fixture: "same-name-equivalent", want: []wantDisposition{
			{kind: agentKindMCP, identity: "shared", target: "claude", action: agentActionImport, targets: []string{"claude", "codex"}},
		}},
		{fixture: "same-name-different", want: []wantDisposition{
			{kind: agentKindMCP, identity: "shared", target: "claude", action: agentActionImport, targets: []string{"claude"}},
			{kind: agentKindMCP, identity: "shared", target: "codex", action: agentActionRetain, reason: agentReasonPerTarget},
		}},
		{fixture: "literal-secret", wantErr: `has literal sensitive environment field "TOKEN"`},
		{fixture: "mixed-ambiguous", want: []wantDisposition{
			{kind: agentKindMarketplace, identity: "dup", target: "codex", action: agentActionRetain, reason: "https://example.test/one.git, https://example.test/two.git"},
			{kind: agentKindMarketplace, identity: "dup", target: "codex", action: agentActionRetain, reason: "https://example.test/one.git, https://example.test/two.git"},
			{kind: agentKindMCP, identity: "clean", target: "codex", action: agentActionImport, targets: []string{"codex"}},
		}},
		{fixture: "already-managed", want: []wantDisposition{
			{kind: agentKindMCP, identity: "context-mode", target: "codex", action: agentActionImport, targets: []string{"codex"}},
		}},
		{fixture: "claude-only-stdio", want: []wantDisposition{
			{kind: agentKindMCP, identity: "demo", target: "claude", action: agentActionImport, targets: []string{"claude"}},
		}},
		{fixture: "codex-only-http", want: []wantDisposition{
			{kind: agentKindMCP, identity: "remote", target: "codex", action: agentActionImport, targets: []string{"codex"}},
		}},
		{fixture: "claude-cwd-and-args", want: []wantDisposition{
			{kind: agentKindMCP, identity: "spaced", target: "claude", action: agentActionImport, targets: []string{"claude"}},
		}},
	} {
		t.Run(test.fixture, func(t *testing.T) {
			a, _ := seedNativeFixture(t, filepath.Join("testdata", "agents_native", test.fixture))
			observations, err := a.inventoryNativeAgents(t.Context())
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error = %v, want it to contain %q", err, test.wantErr)
				}
				if strings.Contains(err.Error(), "literal-token") {
					t.Fatalf("error leaked the secret value: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			assertDispositions(t, resolveAgentDispositions(observations), test.want)
		})
	}
}

func assertDispositions(t *testing.T, got []agentDisposition, want []wantDisposition) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("dispositions = %s, want %d entries", formatDispositions(got), len(want))
	}
	for i, expected := range want {
		actual := got[i]
		switch {
		case actual.Observation.Kind != expected.kind,
			actual.Observation.Identity != expected.identity,
			actual.Observation.Target != expected.target,
			actual.Action != expected.action,
			expected.owner != "" && actual.Owner != expected.owner,
			expected.reason != "" && !strings.Contains(actual.Reason, expected.reason),
			expected.targets != nil && !slices.Equal(actual.Observation.Definition.Agents, expected.targets):
			t.Fatalf("disposition %d = %#v, want %#v", i, actual, expected)
		}
		if actual.Action == agentActionImport && actual.Reason != "" {
			t.Fatalf("imported disposition %d carries a reason: %q", i, actual.Reason)
		}
	}
}

func formatDispositions(got []agentDisposition) string {
	lines := make([]string, 0, len(got))
	for _, disposition := range got {
		lines = append(lines, disposition.Observation.Kind+" "+disposition.Observation.Identity+" ["+disposition.Observation.Target+"] "+disposition.Action+" "+disposition.Reason)
	}
	return "\n" + strings.Join(lines, "\n")
}

func TestResolveAgentDispositionsIsOrderIndependent(t *testing.T) {
	a, _ := seedNativeFixture(t, filepath.Join("testdata", "agents_native", "standalone-matches-package-name"))
	observations, err := a.inventoryNativeAgents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	baseline := formatDispositions(resolveAgentDispositions(observations))
	shuffler := rand.New(rand.NewSource(7))
	for i := 0; i < 20; i++ {
		shuffled := slices.Clone(observations)
		shuffler.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
		if got := formatDispositions(resolveAgentDispositions(shuffled)); got != baseline {
			t.Fatalf("shuffle %d changed the result:\ngot: %s\nwant: %s", i, got, baseline)
		}
	}
}

func TestNativeMCPFingerprintIgnoresPluginRootPrefix(t *testing.T) {
	root := filepath.Join("/plugins", "cache", "market", "demo")
	installed := legacyEntry{Transport: "stdio", Command: "node", Args: []string{filepath.Join(root, "start.mjs")}, Cwd: root}
	declared := legacyEntry{Command: "node", Args: []string{"start.mjs"}}
	if nativeMCPFingerprint(installed, root) != nativeMCPFingerprint(declared, root) {
		t.Fatalf("root-relative fingerprints differ:\n%s\n%s", nativeMCPFingerprint(installed, root), nativeMCPFingerprint(declared, root))
	}
	if nativeMCPFingerprint(installed, "") == nativeMCPFingerprint(declared, root) {
		t.Fatal("absolute paths must not match a relative declaration without the owning root")
	}
}
