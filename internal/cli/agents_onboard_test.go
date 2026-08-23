package cli

import "testing"

func TestParseOnboardResolutions(t *testing.T) {
	got, err := parseOnboardResolutions([]string{"item=future/agent"}, []string{"item:TOKEN=API_TOKEN"}, []string{"skip"}, []string{"move"}, []string{"keep"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ApprovedTargets["item"][0] != "future/agent" || got.EnvBindings["item"]["TOKEN"] != "API_TOKEN" || !got.Excluded["skip"] || !got.MoveToAPM["move"] || !got.KeepInDots["keep"] {
		t.Fatalf("got=%#v", got)
	}
}
func TestParseOnboardResolutionsRejectsUnsafeValues(t *testing.T) {
	for _, args := range [][]string{{"item="}, {"=codex"}} {
		if _, err := parseOnboardResolutions(args, nil, nil, nil, nil); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if _, err := parseOnboardResolutions(nil, []string{"item:x=bad-name"}, nil, nil, nil); err == nil {
		t.Fatal("bad env accepted")
	}
}

func TestParseOnboardResolutionsPreservesChoicesForAppValidation(t *testing.T) {
	got, err := parseOnboardResolutions(nil, nil, []string{"same"}, []string{"same"}, []string{"same"})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Excluded["same"] || !got.MoveToAPM["same"] || !got.KeepInDots["same"] {
		t.Fatalf("CLI dropped a conflicting choice before app validation: %#v", got)
	}
}

func TestAgentsOnboardOnlyExposesLocalMigrationFlags(t *testing.T) {
	flags := newAgentsOnboardCmd(&rootState{}).Flags()
	for _, removed := range []string{"project-root", "from", "approve-executable"} {
		if flags.Lookup(removed) != nil {
			t.Fatalf("obsolete --%s still exposed", removed)
		}
	}
	for _, added := range []string{"move-to-apm", "keep-in-dots", "exclude"} {
		if flags.Lookup(added) == nil {
			t.Fatalf("missing --%s", added)
		}
	}
}
