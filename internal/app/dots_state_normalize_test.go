package app

import "testing"

func TestNormalizeDotState(t *testing.T) {
	cases := map[string]DotState{
		"":                   "",
		"OK":                 DotStateSynced,
		"linked":             DotStateSynced,
		"healthy":            DotStateSynced,
		"synced":             DotStateSynced,
		" missing ":          DotStateMissing,
		"unlinked":           DotStateMissing,
		"conflict":           DotStateConflict,
		"modified":           DotStateModified,
		"broken":             DotStateBroken,
		"no-source":          DotStateNoSource,
		"nosource":           DotStateNoSource,
		"source-missing":     DotStateNoSource,
		"local-only":         DotStateLocalOnly,
		"localonly":          DotStateLocalOnly,
		"repo-only":          DotStateRepoOnly,
		"repoonly":           DotStateRepoOnly,
		"untracked-linked":   DotStateUntrackedLinked,
		"untrackedlinked":    DotStateUntrackedLinked,
		"untracked-conflict": DotStateUntrackedConflict,
		"untrackedconflict":  DotStateUntrackedConflict,
		"ignored":            DotStateIgnored,
		"inactive":           DotStateInactive,
		"disabled":           DotStateDisabled,
		"ambiguous":          DotStateAmbiguous,
	}
	for raw, want := range cases {
		got, err := normalizeDotState(raw)
		if err != nil || got != want {
			t.Fatalf("normalizeDotState(%q) = %q,%v; want %q,nil", raw, got, err, want)
		}
	}
	if _, err := normalizeDotState("bogus"); err == nil {
		t.Fatal("unknown state must error")
	}
}
