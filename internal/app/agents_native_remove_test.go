package app

import (
	"strings"
	"testing"
)

func TestNativeRemoveCommandCoversEveryKind(t *testing.T) {
	want := map[string]string{
		"claude/plugin":      "claude plugin uninstall demo@official",
		"claude/mcp":         "claude mcp remove -s user demo",
		"claude/marketplace": "claude plugin marketplace remove official",
		"codex/plugin":       "codex plugin remove demo@official",
		"codex/mcp":          "codex mcp remove demo",
		"codex/marketplace":  "codex plugin marketplace remove official",
	}
	ids := map[string]string{"plugin": "demo@official", "mcp": "demo", "marketplace": "official"}
	for key, expect := range want {
		target, kind, _ := strings.Cut(key, "/")
		argv, err := nativeRemoveCommand(target, kind, ids[kind])
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		if got := strings.Join(argv, " "); got != expect {
			t.Fatalf("%s = %q, want %q", key, got, expect)
		}
	}
}

func TestNativeRemoveCommandRejectsUnsafeIdentities(t *testing.T) {
	for name, id := range map[string]string{
		"empty":     "",
		"flaglike":  "--force",
		"dashstart": "-rf",
	} {
		if _, err := nativeRemoveCommand("claude", "plugin", id); err == nil {
			t.Fatalf("%s: accepted identity %q", name, id)
		}
	}
	if _, err := nativeRemoveCommand("gemini", "plugin", "x"); err == nil {
		t.Fatal("accepted a target with no removal command")
	}
}

func TestAgentsNativeRemoveRefusesAnIgnoredRow(t *testing.T) {
	a := newIgnoreApp(t)
	row := AgentsNativeRow{Target: "claude", Kind: "plugin", Identity: "demo@official", Ignored: true}
	err := a.AgentsNativeRemove(t.Context(), row)
	if err == nil {
		t.Fatal("removed a row an ignore entry protects")
	}
	if !strings.Contains(err.Error(), "unignore") {
		t.Fatalf("error = %v; want it to point at unignore", err)
	}
}

func TestAgentsNativeAdoptRefusesIgnoredAndUnadoptableRows(t *testing.T) {
	a := newIgnoreApp(t)
	ignored := AgentsNativeRow{Target: "claude", Kind: "plugin", Identity: "demo@official", Ignored: true, Adoptable: true}
	if _, err := a.AgentsNativeAdopt(t.Context(), driftTestHost, ignored); err == nil {
		t.Fatal("adopted a row an ignore entry protects")
	}
	retained := AgentsNativeRow{Target: "codex", Kind: "plugin", Identity: "x@nosource", Reason: "marketplace has no APM source"}
	_, err := a.AgentsNativeAdopt(t.Context(), driftTestHost, retained)
	if err == nil {
		t.Fatal("adopted a row the classifier does not import")
	}
	if !strings.Contains(err.Error(), "no APM source") {
		t.Fatalf("error = %v; want it to carry the classifier reason", err)
	}
}
