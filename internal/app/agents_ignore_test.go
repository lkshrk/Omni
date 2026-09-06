package app

import (
	"strings"
	"testing"
)

func newIgnoreApp(t *testing.T) *App {
	t.Helper()
	return newDriftApp(t, "")
}

func ignoreSel(id string) AgentIgnoreSelector {
	return AgentIgnoreSelector{Host: driftTestHost, Target: "claude", Kind: "plugin", ID: id}
}

func TestAgentIgnoreRecordsAnEntry(t *testing.T) {
	a := newIgnoreApp(t)

	if err := a.AgentIgnore(ignoreSel("work@private")); err != nil {
		t.Fatalf("AgentIgnore: %v", err)
	}
	entries, err := a.AgentIgnoreEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ID != "work@private" || entries[0].Host != driftTestHost {
		t.Fatalf("entries = %+v; want one entry for work@private on the test host", entries)
	}
}

func TestAgentIgnoreUpdatesReasonInsteadOfDuplicating(t *testing.T) {
	a := newIgnoreApp(t)
	sel := ignoreSel("work@private")
	sel.Reason = "first"
	if err := a.AgentIgnore(sel); err != nil {
		t.Fatal(err)
	}
	sel.Reason = "second"
	if err := a.AgentIgnore(sel); err != nil {
		t.Fatal(err)
	}
	entries, err := a.AgentIgnoreEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %+v; want the entry replaced, not duplicated", entries)
	}
	if entries[0].Reason != "second" {
		t.Fatalf("reason = %q, want %q", entries[0].Reason, "second")
	}
}

func TestAgentUnignoreRemovesOnlyTheNamedEntry(t *testing.T) {
	a := newIgnoreApp(t)
	for _, id := range []string{"one@mkt", "two@mkt"} {
		if err := a.AgentIgnore(ignoreSel(id)); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.AgentUnignore(ignoreSel("one@mkt")); err != nil {
		t.Fatalf("AgentUnignore: %v", err)
	}
	entries, err := a.AgentIgnoreEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ID != "two@mkt" {
		t.Fatalf("entries = %+v; want only two@mkt left", entries)
	}
}

func TestAgentUnignoreFailsWhenNothingWasProtected(t *testing.T) {
	a := newIgnoreApp(t)
	err := a.AgentUnignore(ignoreSel("never@mkt"))
	if err == nil {
		t.Fatal("AgentUnignore reported success for an entry that was never recorded")
	}
	if !strings.Contains(err.Error(), "never@mkt") {
		t.Fatalf("error = %v; want it to name the entry", err)
	}
}

func TestAgentIgnoreRejectsAnUnaddressableSelector(t *testing.T) {
	a := newIgnoreApp(t)
	cases := map[string]AgentIgnoreSelector{
		"no host":     {Target: "claude", Kind: "plugin", ID: "x"},
		"bad target":  {Host: driftTestHost, Target: "gemini", Kind: "plugin", ID: "x"},
		"bad kind":    {Host: driftTestHost, Target: "claude", Kind: "skill", ID: "x"},
		"no identity": {Host: driftTestHost, Target: "claude", Kind: "plugin"},
	}
	for name, sel := range cases {
		if err := a.AgentIgnore(sel); err == nil {
			t.Fatalf("%s: AgentIgnore accepted an unaddressable selector", name)
		}
	}
	entries, err := a.AgentIgnoreEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %+v; a rejected selector must not be written", entries)
	}
}

func TestAgentIgnoreEntryIsHostScopedForDrift(t *testing.T) {
	a := newIgnoreApp(t)
	other := ignoreSel("elsewhere@mkt")
	other.Host = "coder"
	if err := a.AgentIgnore(other); err != nil {
		t.Fatal(err)
	}
	cfg, err := a.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OMNI_HOSTNAME", "some-other-host")
	if got := hostAgentIgnoreEntries(cfg); len(got) != 0 {
		t.Fatalf("hostAgentIgnoreEntries = %+v; another host's entry must not filter this host's drift", got)
	}
	t.Setenv("OMNI_HOSTNAME", "coder")
	got := hostAgentIgnoreEntries(cfg)
	if len(got) != 1 || got[0].ID != "elsewhere@mkt" {
		t.Fatalf("hostAgentIgnoreEntries = %+v; want the coder entry", got)
	}
}
