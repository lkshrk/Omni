package app

import (
	"reflect"
	"slices"
	"testing"
)

func TestMembershipInvariantToggle(t *testing.T) {
	t.Parallel(
	// reusable groups: "team", "web"; host groups: anything else ("laptop", "server").
	)

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
	t.Parallel()
	reusable := ReusablePredicate([]string{"team", "web"})
	current := []string{"team", "laptop"}
	_ = MembershipInvariantToggle(current, "web", reusable)
	if !reflect.DeepEqual(current, []string{"team", "laptop"}) {
		t.Fatalf("input mutated: %v", current)
	}
}

func TestEnforceMembershipInvariant(t *testing.T) {
	t.Parallel()
	reusable := ReusablePredicate([]string{"team", "web", "infra"})
	got := EnforceMembershipInvariant([]string{"laptop", "team", "server", "web", "infra"}, reusable)
	want := []string{"laptop", "team", "server"} // all host groups kept, only first reusable survives
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EnforceMembershipInvariant = %v, want %v", got, want)
	}
}

func TestMembershipCapsReusable(t *testing.T) {
	t.Parallel()
	if !MembershipCapsReusable("dot") {
		t.Error("dot should cap reusable")
	}
	for _, kind := range []string{"tool", "skill", "mcp", "plugin", "marketplace"} {
		if MembershipCapsReusable(kind) {
			t.Errorf("%s should not cap reusable", kind)
		}
	}
}

func TestMembershipToggle_FreeAddRemove(t *testing.T) {
	t.Parallel()
	got := MembershipToggle([]string{"a"}, "b")
	if !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("add: got %v, want [a b]", got)
	}
	got = MembershipToggle([]string{"a", "b"}, "a")
	if !slices.Equal(got, []string{"b"}) {
		t.Fatalf("remove: got %v, want [b]", got)
	}
	// No reusable eviction: two reusable groups coexist.
	got = MembershipToggle([]string{"work"}, "base")
	if !slices.Equal(got, []string{"work", "base"}) {
		t.Fatalf("free: got %v, want [work base]", got)
	}
}
