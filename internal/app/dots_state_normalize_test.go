package app

import (
	"testing"

	"github.com/lkshrk/omni/internal/dots"
)

func TestNormalizeDotState(t *testing.T) {
	cases := map[string]dots.State{
		"":                   "",
		"OK":                 dots.StateSynced,
		"linked":             dots.StateSynced,
		"healthy":            dots.StateSynced,
		"synced":             dots.StateSynced,
		" missing ":          dots.StateMissing,
		"unlinked":           dots.StateMissing,
		"conflict":           dots.StateConflict,
		"modified":           dots.StateModified,
		"broken":             dots.StateBroken,
		"no-source":          dots.StateNoSource,
		"nosource":           dots.StateNoSource,
		"source-missing":     dots.StateNoSource,
		"local-only":         dots.StateLocalOnly,
		"localonly":          dots.StateLocalOnly,
		"repo-only":          dots.StateRepoOnly,
		"repoonly":           dots.StateRepoOnly,
		"untracked-linked":   dots.StateUntrackedLinked,
		"untrackedlinked":    dots.StateUntrackedLinked,
		"untracked-conflict": dots.StateUntrackedConflict,
		"untrackedconflict":  dots.StateUntrackedConflict,
		"ignored":            dots.StateIgnored,
		"inactive":           dots.StateInactive,
		"disabled":           dots.StateDisabled,
		"ambiguous":          dots.StateAmbiguous,
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
