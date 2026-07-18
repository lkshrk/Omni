package app

import (
	"slices"
	"strings"
)

// A membership set for an item (tool, dot, skill, mcp server, plugin,
// marketplace) may contain any number of host groups but at most one reusable
// ("normal") group. Host groups are machine-scoped, so at most one is ever
// active on a given host; reusable groups span the fleet and are the only ones
// that can collide, so they are capped at one. Callers supply a `reusable`
// predicate; every name it rejects (the machine host group and any other host
// groups already stored for other machines) is treated as a host group and
// preserved.

// MembershipInvariantToggle applies a single toggle of group against current,
// enforcing the invariant. Toggling a group that is already a member removes
// it. Adding a reusable group evicts any other reusable membership; adding a
// host group is purely additive. The input slice is not mutated.
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

// EnforceMembershipInvariant reduces groups so it satisfies the invariant: all
// host groups are kept in order, and only the first reusable group survives.
// It is a defensive backstop for write paths that accept an arbitrary set.
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

// ReusablePredicate builds a `reusable` predicate from the reusable group names
// known to the caller (e.g. the TUI's ordered reusable list plus any groups
// created during the current picker session). Names absent from the set are
// treated as host groups.
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
