package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/executor"
)

func driftDoctorCheck(t *testing.T, a *App, ctx context.Context) (DoctorCheck, bool) {
	t.Helper()
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	result := &DoctorResult{}
	a.doctorAgentsDrift(ctx, result, cfg)
	for _, check := range result.Checks {
		if check.ID == "agents-drift" {
			if check.Status == DoctorStatusFail {
				t.Fatalf("native drift must never fail: %#v", check)
			}
			return check, true
		}
	}
	return DoctorCheck{}, false
}

func TestDoctorAgentsDriftWarnsOnUnownedItems(t *testing.T) {
	a := newDriftApp(t, "", driftPluginRules()...)
	check, found := driftDoctorCheck(t, a, t.Context())
	if !found {
		t.Fatal("no agents-drift check registered")
	}
	if check.Status != DoctorStatusWarn {
		t.Fatalf("status = %q, want warn: %#v", check.Status, check)
	}
	if check.Message != "2 native agent item(s) outside APM (0 ignored)" {
		t.Fatalf("message = %q", check.Message)
	}
	if !strings.Contains(strings.Join(check.Details, "\n"), "claude  plugin  demo@official") {
		t.Fatalf("details = %#v", check.Details)
	}
}

func TestDoctorAgentsDriftCountsIgnoredWhenClean(t *testing.T) {
	entry := `{"host":"` + driftTestHost + `","target":"claude","kind":"plugin","id":"demo@official"},` +
		`{"host":"` + driftTestHost + `","target":"claude","kind":"marketplace","id":"official"}`
	a := newDriftApp(t, entry,
		nativeRule("claude plugins list --json", `[{"id":"demo@official"}]`),
		nativeRule("claude plugins marketplace list --json", `[{"name":"official","source":"github","repo":"acme/plugins"}]`),
	)
	check, found := driftDoctorCheck(t, a, t.Context())
	if !found {
		t.Fatal("no agents-drift check registered")
	}
	if check.Status != DoctorStatusOK || check.Message != "no native agent items outside APM (2 ignored)" {
		t.Fatalf("check = %#v", check)
	}
}

func TestDoctorAgentsDriftSkipsWhenNoClientPresent(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", driftTestHost)
	a, _ := newNativeInventoryApp(t, map[string]bool{})
	if _, found := driftDoctorCheck(t, a, t.Context()); found {
		t.Fatal("check registered with neither client installed")
	}
}

func TestDoctorAgentsDriftIsUncheckedWhenAClientFails(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", driftTestHost)
	a, _ := newNativeInventoryApp(t, map[string]bool{"claude": true},
		executor.MatchRule{Pattern: "claude plugins list --json", Response: executor.MockCall{Err: errors.New("claude exploded")}},
	)
	check, found := driftDoctorCheck(t, a, t.Context())
	if !found {
		t.Fatal("no agents-drift check registered")
	}
	if check.Status != DoctorStatusOK || !strings.HasPrefix(check.Message, "native agent state not checked (") {
		t.Fatalf("check = %#v", check)
	}
	if !strings.Contains(check.Message, "claude exploded") {
		t.Fatalf("message hides the reason: %q", check.Message)
	}
}

func TestDoctorAgentsDriftIsUncheckedOnDeadline(t *testing.T) {
	a := newDriftApp(t, "", driftPluginRules()...)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	check, found := driftDoctorCheck(t, a, ctx)
	if !found {
		t.Fatal("no agents-drift check registered")
	}
	if check.Status != DoctorStatusOK || check.Message != "native agent state not checked (timed out)" {
		t.Fatalf("check = %#v", check)
	}
}

func TestHostAgentIgnoreEntriesMatchesShortHostname(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "box.example.test")
	cfg := &config.RootConfig{Agents: &config.AgentsConfig{Ignored: []config.AgentIgnoreEntry{
		{Host: "box", Target: "claude", Kind: "plugin", ID: "a@m"},
		{Host: "other", Target: "claude", Kind: "plugin", ID: "b@m"},
	}}}
	entries := hostAgentIgnoreEntries(cfg)
	if len(entries) != 1 || entries[0].ID != "a@m" {
		t.Fatalf("entries = %#v", entries)
	}
}
