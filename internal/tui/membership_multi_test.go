package tui

import (
	"slices"
	"strings"
	"testing"
)

// TestSelectGroupMembership_MultiSelectInvariant verifies the item-membership
// picker allows any number of host groups plus at most one reusable group.
// "web" and "team" are reusable (in groupNames); "laptop" is a host group.
func TestSelectGroupMembership_MultiSelectInvariant(t *testing.T) {
	m := &Model{
		groupNames:           []string{"web", "team"},
		pickerMembershipKind: pickerMembershipSkill,
		pickerMembershipName: "ripgrep",
		skillsMemberships:    map[string][]string{"ripgrep": {}},
		pickerGroups:         []string{"laptop", "web", "team"},
	}

	toggle := func(group string) {
		idx := slices.Index(m.pickerGroups, group)
		if idx < 0 {
			t.Fatalf("group %q not in picker", group)
		}
		m.pickerCursor = idx
		m.selectGroupMembership()
	}
	current := func() []string { return m.skillsMemberships["ripgrep"] }

	toggle("laptop") // add host group
	if !slices.Equal(current(), []string{"laptop"}) {
		t.Fatalf("after +laptop = %v, want [laptop]", current())
	}

	toggle("web") // add reusable group alongside host group
	if !slices.Equal(current(), []string{"laptop", "web"}) {
		t.Fatalf("after +web = %v, want [laptop web]", current())
	}

	toggle("team") // second reusable evicts first, keeps host group
	if !slices.Equal(current(), []string{"laptop", "team"}) {
		t.Fatalf("after +team = %v, want [laptop team] (web evicted)", current())
	}

	toggle("laptop") // toggle host group off
	if !slices.Equal(current(), []string{"team"}) {
		t.Fatalf("after -laptop = %v, want [team]", current())
	}
}

// TestRenderGroupMembershipPicker_MarksEveryMember confirms the picker renders a
// checkbox [x] on every group the item belongs to, not just one (multi-select).
func TestRenderGroupMembershipPicker_MarksEveryMember(t *testing.T) {
	m := baseModel(threeTools())
	m.mode = viewGroupMembership
	m.cursor = 0
	m.pickerGroups = []string{"base", "work", "personal"}
	m.toolMemberships = map[string][]string{
		toolMembershipKey(m.selectedTool()): {"base", "personal"},
	}
	out := renderGroupMembershipPicker(m)
	if got := strings.Count(out, "[x]"); got != 2 {
		t.Fatalf("checked marks = %d, want 2 (base + personal):\n%s", got, out)
	}
	if got := strings.Count(out, "[ ]"); got != 1 {
		t.Fatalf("unchecked marks = %d, want 1 (work):\n%s", got, out)
	}
}

// TestSelectGroupMembership_InvariantAcrossItemKinds runs the same host+reusable
// toggle for every item kind routed through the membership picker, since they
// share selectGroupMembership but store drafts in per-kind maps.
func TestSelectGroupMembership_InvariantAcrossItemKinds(t *testing.T) {
	kinds := []struct {
		kind string
		seed func(m *Model)
		get  func(m *Model) []string
	}{
		{pickerMembershipDot,
			func(m *Model) { m.dotMemberships = map[string][]string{"item": {}} },
			func(m *Model) []string { return m.dotMemberships["item"] }},
		{pickerMembershipMcp,
			func(m *Model) { m.mcpMemberships = map[string][]string{"item": {}} },
			func(m *Model) []string { return m.mcpMemberships["item"] }},
		{pickerMembershipPlugin,
			func(m *Model) { m.pluginMemberships = map[string][]string{"item": {}} },
			func(m *Model) []string { return m.pluginMemberships["item"] }},
		{pickerMembershipMarketplace,
			func(m *Model) { m.marketplaceMemberships = map[string][]string{"item": {}} },
			func(m *Model) []string { return m.marketplaceMemberships["item"] }},
	}
	for _, tc := range kinds {
		t.Run(tc.kind, func(t *testing.T) {
			m := &Model{
				groupNames:           []string{"web"}, // reusable; "laptop" is a host group
				pickerMembershipKind: tc.kind,
				pickerMembershipName: "item",
				pickerGroups:         []string{"laptop", "web"},
			}
			tc.seed(m)
			m.pickerCursor = 0 // laptop (host)
			m.selectGroupMembership()
			m.pickerCursor = 1 // web (reusable)
			m.selectGroupMembership()
			if !slices.Equal(tc.get(m), []string{"laptop", "web"}) {
				t.Fatalf("%s memberships = %v, want [laptop web]", tc.kind, tc.get(m))
			}
		})
	}
}
