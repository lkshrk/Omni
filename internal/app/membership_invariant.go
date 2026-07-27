package app

import (
	"slices"
	"strings"
)

// Host groups are machine-scoped so at most one is ever active; reusable groups span the fleet and are capped at one.

// MembershipInvariantToggle — Toggling an existing member removes it; adding a reusable group evicts the other reusable membership.
func MembershipInvariantToggle(current []string, group string, reusable func(string) bool) []string {
	group = strings.TrimSpace(group)
	if group == "" {
		return append([]string(nil), current...)
	}
	if slices.Contains(current, group) {
		out := make([]string, 0, len(current))
		for _, g := range current {
			if g != group {
				out = append(out, g)
			}
		}
		return out
	}
	addingReusable := reusable(group)
	out := make([]string, 0, len(current)+1)
	for _, g := range current {
		if addingReusable && reusable(g) {
			continue // evict the existing reusable membership
		}
		out = append(out, g)
	}
	return append(out, group)
}

// EnforceMembershipInvariant — A defensive backstop for write paths that accept an arbitrary set.
func EnforceMembershipInvariant(groups []string, reusable func(string) bool) []string {
	out := make([]string, 0, len(groups))
	seenReusable := false
	for _, g := range groups {
		if reusable(g) {
			if seenReusable {
				continue
			}
			seenReusable = true
		}
		out = append(out, g)
	}
	return out
}

// ReusablePredicate — Names absent from the set are treated as host groups.
func ReusablePredicate(names ...[]string) func(string) bool {
	set := make(map[string]bool)
	for _, list := range names {
		for _, n := range list {
			if n = strings.TrimSpace(n); n != "" {
				set[n] = true
			}
		}
	}
	return func(name string) bool { return set[strings.TrimSpace(name)] }
}

// MembershipCapsReusable — Only dots cap, because their symlinks collide; tools and agent kinds may join any number of groups.
func MembershipCapsReusable(kind string) bool {
	return kind == "dot"
}

// MembershipToggle — No reusable cap; used by every non-dot kind.
func MembershipToggle(current []string, group string) []string {
	group = strings.TrimSpace(group)
	if group == "" {
		return append([]string(nil), current...)
	}
	if slices.Contains(current, group) {
		out := make([]string, 0, len(current))
		for _, g := range current {
			if g != group {
				out = append(out, g)
			}
		}
		return out
	}
	return append(append([]string(nil), current...), group)
}
