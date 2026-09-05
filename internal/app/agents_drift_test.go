package app

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
)

const driftTestHost = "drift-host"

func driftPluginRules() []executor.MatchRule {
	return []executor.MatchRule{
		nativeRule("claude plugins list --json", `[{"id":"demo@official"},{"id":"orphan@nosource"}]`),
		nativeRule("claude plugins marketplace list --json", `[{"name":"official","source":"github","repo":"acme/plugins"},{"name":"nosource","source":"github","repo":""}]`),
	}
}

func newDriftApp(t *testing.T, ignored string, rules ...executor.MatchRule) *App {
	t.Helper()
	t.Setenv("OMNI_HOSTNAME", driftTestHost)
	a, _ := newNativeInventoryApp(t, map[string]bool{"claude": true}, rules...)
	body := "{}\n"
	if ignored != "" {
		body = `{"agents":{"ignored":[` + ignored + "]}}\n"
	}
	if err := os.WriteFile(a.ConfigPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return a
}

func driftOutput(t *testing.T, a *App, all bool) string {
	t.Helper()
	out, err := a.AgentsDrift(t.Context(), all)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func assertContains(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func assertNotContains(t *testing.T, out string, unwanted ...string) {
	t.Helper()
	for _, bad := range unwanted {
		if strings.Contains(out, bad) {
			t.Fatalf("output unexpectedly contains %q:\n%s", bad, out)
		}
	}
}

func TestAgentsDriftListsReplacedAndOmitsRetained(t *testing.T) {
	a := newDriftApp(t, "", driftPluginRules()...)
	out := driftOutput(t, a, false)
	assertContains(t, out, driftSectionTitle, "claude  plugin  demo@official", "claude  marketplace  official", "Ignored: 0")
	assertNotContains(t, out, "orphan@nosource", retainedSectionTitle)
}

func TestAgentsDriftAllListsRetainedReasons(t *testing.T) {
	a := newDriftApp(t, "", driftPluginRules()...)
	out := driftOutput(t, a, true)
	assertContains(t, out, retainedSectionTitle, "claude  plugin  orphan@nosource  "+agentReasonNoSource)
}

func TestAgentsDriftFiltersIgnoredForThisHost(t *testing.T) {
	entry := `{"host":"` + driftTestHost + `","target":"claude","kind":"plugin","id":"demo@official","reason":"kept native on purpose"},` +
		`{"host":"` + driftTestHost + `","target":"claude","kind":"marketplace","id":"official"}`
	a := newDriftApp(t, entry, driftPluginRules()...)

	out := driftOutput(t, a, false)
	assertContains(t, out, "No native agent drift.", "Ignored: 2")
	assertNotContains(t, out, driftSectionTitle, driftStaleEntriesTitle)

	all := driftOutput(t, a, true)
	assertContains(t, all, driftIgnoredEntriesTitle, "claude  plugin  demo@official  kept native on purpose")
}

func TestAgentsDriftKeepsOtherHostIgnoreEntries(t *testing.T) {
	entry := `{"host":"somewhere-else","target":"claude","kind":"plugin","id":"demo@official"}`
	a := newDriftApp(t, entry, driftPluginRules()...)
	out := driftOutput(t, a, true)
	assertContains(t, out, "claude  plugin  demo@official", "Ignored: 0")
	assertNotContains(t, out, driftStaleEntriesTitle, driftIgnoredEntriesTitle)
}

func TestAgentsDriftReportsStaleIgnoreEntry(t *testing.T) {
	entry := `{"host":"` + driftTestHost + `","target":"claude","kind":"mcp","id":"gone"}`
	a := newDriftApp(t, entry, driftPluginRules()...)
	out := driftOutput(t, a, false)
	assertContains(t, out, driftStaleEntriesTitle, "claude  mcp  gone", "Ignored: 0")
}

func TestAgentsDriftReportsIncompleteCoverage(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", driftTestHost)
	a, _ := newNativeInventoryApp(t, map[string]bool{"claude": true, "codex": true},
		append(driftPluginRules(),
			executor.MatchRule{Pattern: "codex plugin list --json", Response: executor.MockCall{Err: errors.New("codex timed out")}},
		)...,
	)
	out := driftOutput(t, a, false)
	assertContains(t, out, driftCoverageTitle, "codex: ", "codex timed out", "claude  plugin  demo@official")
	assertNotContains(t, out, "No native agent drift.")
}
