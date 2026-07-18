package app

import (
	"reflect"
	"testing"
)

func TestMembershipInvariantToggle(t *testing.T) {
	// reusable groups: "team", "web"; host groups: anything else ("laptop", "server").
	reusable := ReusablePredicate([]string{"team", "web"})

	tests := []struct {
		name    string
		current []string
		group   string
		want    []string
	}{
		{"add host group is additive", []string{"team"}, "laptop", []string{"team", "laptop"}},
		{"add second host group keeps first", []string{"laptop"}, "server", []string{"laptop", "server"}},
		{"add reusable evicts other reusable", []string{"team", "laptop"}, "web", []string{"laptop", "web"}},
		{"add reusable keeps all host groups", []string{"laptop", "server"}, "team", []string{"laptop", "server", "team"}},
		{"toggle off existing host group", []string{"team", "laptop"}, "laptop", []string{"team"}},
		{"toggle off existing reusable group", []string{"team", "laptop"}, "team", []string{"laptop"}},
		{"add first reusable to empty", nil, "team", []string{"team"}},
		{"blank group is a no-op", []string{"team"}, "  ", []string{"team"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := MembershipInvariantToggle(tc.current, tc.group, reusable)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("MembershipInvariantToggle(%v, %q) = %v, want %v", tc.current, tc.group, got, tc.want)
			}
		})
	}
}

func TestMembershipInvariantToggleDoesNotMutateInput(t *testing.T) {
	reusable := ReusablePredicate([]string{"team", "web"})
	current := []string{"team", "laptop"}
	_ = MembershipInvariantToggle(current, "web", reusable)
	if !reflect.DeepEqual(current, []string{"team", "laptop"}) {
		t.Fatalf("input mutated: %v", current)
	}
}

func TestEnforceMembershipInvariant(t *testing.T) {
	reusable := ReusablePredicate([]string{"team", "web", "infra"})
	got := EnforceMembershipInvariant([]string{"laptop", "team", "server", "web", "infra"}, reusable)
	want := []string{"laptop", "team", "server"} // all host groups kept, only first reusable survives
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EnforceMembershipInvariant = %v, want %v", got, want)
	}
}
