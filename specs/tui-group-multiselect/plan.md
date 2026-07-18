# Type-Specific Group Multiselect Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let Tools and Agent-kinds (skills/mcp/plugins/marketplaces) belong to any number of groups, while Dots keep the "many host + one reusable" cap; rename the tools hint to "edit groups"; display multiple memberships as host-first pills; support all of it in TUI and CLI.

**Architecture:** One capability, `app.MembershipCapsReusable(kind)`, is the single authority for whether a kind caps reusable groups (dots only). The TUI toggle branches on it; config validation keeps the dots check and drops the tools check. The app write layer already stores full `[]string` sets, so no writer changes are needed. Display becomes a shared host-first pill renderer reused by the dots/tools/agents tables.

**Tech Stack:** Go, bubbletea/lipgloss TUI, cobra CLI. Tests: standard `go test`.

## Global Constraints

- Membership model: Tools + skill/mcp/plugin/marketplace = free multi (any number of any group). Dots = unlimited host groups + at most one reusable group.
- Kind identifiers (existing string constants in `internal/tui/update_group_picker.go`): `tool`, `dot`, `skill`, `mcp`, `plugin`, `marketplace`.
- Host group = `GroupConfig.Special=="host"` (`IsHost()`), name = short hostname. Reusable group = `Special==""`.
- User-facing wording change only ("change groups" → "edit groups"); keep internal key-map field `MoveGroup` and CLI identifiers.
- Work in the worktree at `~/omni/.claude/worktrees/scratch` on branch `feat/group-multiselect-types`. All Read/Edit/Write use that path.
- Run tests with `go test ./internal/<pkg>/...`.

---

## Group A — Membership rule (behavior)

### Task 1: Membership capability + free toggle (app)

**Files:**
- Modify: `internal/app/membership_invariant.go`
- Test: `internal/app/membership_invariant_test.go` (create if absent)

**Interfaces:**
- Produces: `func MembershipCapsReusable(kind string) bool`; `func MembershipToggle(current []string, group string) []string`

- [ ] **Step 1: Write the failing test**

```go
// internal/app/membership_invariant_test.go
package app

import (
	"slices"
	"testing"
)

func TestMembershipCapsReusable(t *testing.T) {
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run 'TestMembershipCapsReusable|TestMembershipToggle_FreeAddRemove'`
Expected: FAIL — `MembershipCapsReusable`/`MembershipToggle` undefined.

- [ ] **Step 3: Write minimal implementation**

Append to `internal/app/membership_invariant.go`:

```go
// MembershipCapsReusable reports whether an item kind caps reusable-group
// membership at one. Only dots cap (their symlinks collide); tools and the
// agent-kinds (skill/mcp/plugin/marketplace) may join any number of groups.
func MembershipCapsReusable(kind string) bool {
	return kind == "dot"
}

// MembershipToggle adds group when absent and removes it when present, with no
// reusable cap. Used by every non-dot kind. The input slice is not mutated.
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/ -run 'TestMembershipCapsReusable|TestMembershipToggle_FreeAddRemove'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/app/membership_invariant.go internal/app/membership_invariant_test.go
git commit -m "feat(groups): add membership capability + free toggle authority"
```

---

### Task 2: TUI toggle branches on capability

**Files:**
- Modify: `internal/tui/update_group_picker.go` (`selectGroupMembership`, and the create-new-group toggle near it)
- Test: `internal/tui/membership_multi_test.go` (append)

**Interfaces:**
- Consumes: `app.MembershipCapsReusable`, `app.MembershipToggle`, `app.MembershipInvariantToggle`

- [ ] **Step 1: Write the failing test**

Append to `internal/tui/membership_multi_test.go`. Build a model with two reusable groups and a tool row; toggle both; assert both retained. Then a dot row; toggle two reusable; assert only the last retained.

```go
func TestSelectGroupMembership_ToolKeepsTwoReusable(t *testing.T) {
	m := membershipToggleTestModel(t, pickerMembershipTool)
	m.groupNames = []string{"work", "base"}
	m.setSelectedMemberships([]string{"work"})
	m.pickerGroups = []string{"work", "base"}
	m.pickerCursor = 1 // "base"
	m.selectGroupMembership()
	_, got, _ := m.selectedMembershipTarget()
	if len(got) != 2 {
		t.Fatalf("tool memberships = %v, want both reusable kept", got)
	}
}

func TestSelectGroupMembership_DotEvictsSecondReusable(t *testing.T) {
	m := membershipToggleTestModel(t, pickerMembershipDot)
	m.groupNames = []string{"work", "base"}
	m.setSelectedMemberships([]string{"work"})
	m.pickerGroups = []string{"work", "base"}
	m.pickerCursor = 1 // "base"
	m.selectGroupMembership()
	_, got, _ := m.selectedMembershipTarget()
	if len(got) != 1 || got[0] != "base" {
		t.Fatalf("dot memberships = %v, want [base] (evicted work)", got)
	}
}
```

Add a helper `membershipToggleTestModel(t, kind)` in the same file that returns a `Model` with `pickerMembershipKind=kind`, `pickerMembershipName="x"` (or `pickerMembershipKey` for tools), and the relevant membership map initialized. Follow the existing setup in `membership_multi_test.go`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run 'TestSelectGroupMembership_ToolKeepsTwoReusable|TestSelectGroupMembership_DotEvictsSecondReusable'`
Expected: FAIL — tool test fails (invariant evicts the second reusable today).

- [ ] **Step 3: Write minimal implementation**

In `selectGroupMembership` (and the create-new-group toggle that also calls `MembershipInvariantToggle`), replace the unconditional invariant with a branch:

```go
reusable := app.ReusablePredicate(m.groupNames, m.pickerCreatedGroups)
var next []string
if app.MembershipCapsReusable(m.pickerMembershipKind) {
	next = app.MembershipInvariantToggle(current, group, reusable)
} else {
	next = app.MembershipToggle(current, group)
}
m.setSelectedMemberships(next)
```

Apply the same branch at the second call site (the newly-created-group toggle).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/ -run 'TestSelectGroupMembership_'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tui/update_group_picker.go internal/tui/membership_multi_test.go
git commit -m "feat(groups): TUI toggle free for tools/agents, capped for dots"
```

---

### Task 3: Config validation — drop tools cap, keep dots

**Files:**
- Modify: `internal/config/config.go` (validation loop, ~lines 764-837)
- Test: `internal/config/validation_test.go` (append)

- [ ] **Step 1: Write the failing test**

```go
func TestValidateRoot_ToolTwoReusableGroupsOK(t *testing.T) {
	cfg := rootWithLogicalTool("eslint")
	cfg.Groups = append(cfg.Groups,
		reusableGroupWithTool("work", "eslint"),
		reusableGroupWithTool("base", "eslint"),
	)
	if errs := ValidateRoot(cfg); len(errs) != 0 {
		t.Fatalf("tool in two reusable groups should validate, got %v", errs)
	}
}

func TestValidateRoot_DotTwoReusableGroupsRejected(t *testing.T) {
	cfg := rootWithDot("nvim")
	cfg.Groups = append(cfg.Groups,
		reusableGroupWithDot("work", "nvim"),
		reusableGroupWithDot("base", "nvim"),
	)
	errs := ValidateRoot(cfg)
	if len(errs) == 0 {
		t.Fatal("dot in two reusable groups must be rejected")
	}
}
```

Add the small `rootWithLogicalTool` / `reusableGroupWithTool` / `rootWithDot` / `reusableGroupWithDot` helpers to the test file if not already present, mirroring existing config test fixtures.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run 'TestValidateRoot_ToolTwoReusableGroupsOK|TestValidateRoot_DotTwoReusableGroupsRejected'`
Expected: FAIL — the tool test fails (tools are currently capped).

- [ ] **Step 3: Write minimal implementation**

In `internal/config/config.go`, delete the tool single-reusable block (the `if !g.IsHost() { if first, ok := memberships[tool.Name]; ok { ... } }` around lines 783-789) and the now-unused `memberships` map declaration for tools. Leave the identical dots block (~830-836) untouched.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/`
Expected: PASS (both new tests and all existing).

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/validation_test.go
git commit -m "feat(groups): allow tools in multiple reusable groups, keep dots capped"
```

---

## Group B — Wording

### Task 4: Rename tools hint to "edit groups"

**Files:**
- Modify: `internal/actions/terms.go` (retire `LabelChangeGroups`), `internal/actions/catalog.go` (`ToolChangeGroup` label)
- Test: `internal/actions/catalog_test.go` or `internal/tui/*_test.go` asserting the tools row hint text

- [ ] **Step 1: Write the failing test**

```go
func TestToolChangeGroupLabelIsEditGroups(t *testing.T) {
	if got := MustTUILabel(ToolChangeGroup); got != LabelEditGroups {
		t.Fatalf("tools group hint = %q, want %q", got, LabelEditGroups)
	}
}
```

Place in `internal/actions/catalog_test.go` (package `actions`).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/actions/ -run TestToolChangeGroupLabelIsEditGroups`
Expected: FAIL — currently `LabelChangeGroups` ("change groups").

- [ ] **Step 3: Write minimal implementation**

In `internal/actions/catalog.go`, the `ToolChangeGroup` action `Label` and its `TUIBinding.Label` become `LabelEditGroups`. In `internal/actions/terms.go`, remove `LabelChangeGroups` and its uses (there should be none after this edit; grep to confirm). Description text may stay "Change the selected tool's group memberships." (sentence, not the hint label).

- [ ] **Step 4: Run test + build**

Run: `go build ./... && go test ./internal/actions/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/actions/terms.go internal/actions/catalog.go internal/actions/catalog_test.go
git commit -m "feat(groups): rename tools 'change groups' hint to 'edit groups'"
```

---

## Group C — Display: host-first pills

### Task 5: Shared host-first pill renderer

**Files:**
- Modify: `internal/tui/view_row_shared.go` (add renderer next to `compactMembershipBadge`)
- Test: `internal/tui/view_row_shared_test.go` (create/append)

**Interfaces:**
- Produces: `func renderGroupPills(p palette, groups []string, info *app.HostInfo, maxW int) string` — host group(s) first (distinct style), reusable groups after; collapses overflow to `[<host> +N]` when the pills exceed `maxW`; empty string when no groups.

- [ ] **Step 1: Write the failing test**

```go
func TestRenderGroupPills_HostFirstAndCollapse(t *testing.T) {
	p := newTestPalette()
	info := testHostInfo("laptop") // host group "laptop"
	groups := []string{"work", "laptop", "base"}

	wide := stripANSI(renderGroupPills(p, groups, info, 80))
	if !strings.HasPrefix(wide, "[laptop]") {
		t.Fatalf("host pill must come first: %q", wide)
	}
	if !strings.Contains(wide, "[work]") || !strings.Contains(wide, "[base]") {
		t.Fatalf("reusable pills missing: %q", wide)
	}

	tight := stripANSI(renderGroupPills(p, groups, info, 12))
	if !strings.Contains(tight, "+") || !strings.HasPrefix(tight, "[laptop") {
		t.Fatalf("tight should collapse to host + count: %q", tight)
	}
}
```

Reuse existing test helpers (`newTestPalette`, `stripANSI`, host-info builder) — grep the tui test files for their current names and match them.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run TestRenderGroupPills_HostFirstAndCollapse`
Expected: FAIL — `renderGroupPills` undefined.

- [ ] **Step 3: Write minimal implementation**

Add to `internal/tui/view_row_shared.go`. Filter to the active host via the same path `app.GroupLabel` uses (`app.FilterGroupsForHost(groups, app.ActiveHostGroupSet(info, ...))`), split into host vs reusable using `info`, order host-first, render each as a styled pill (`p.styleProvider` for host pill, `p.styleHelp`/existing group-pill style for reusable), and when the joined width exceeds `maxW`, emit `[<hostname> +<remaining>]`.

```go
func renderGroupPills(p palette, groups []string, info *app.HostInfo, maxW int) string {
	ordered := app.HostFirstGroups(groups, info) // host group(s), then sorted reusable
	if len(ordered) == 0 {
		return ""
	}
	pill := func(name string, host bool) string {
		style := p.styleHelp
		if host {
			style = p.styleProvider
		}
		return style.Render("[" + name + "]")
	}
	full := renderPillRow(pill, ordered, info)
	if maxW <= 0 || lipgloss.Width(full) <= maxW {
		return full
	}
	head := ordered[0]
	return pill(head, app.IsHostGroup(head, info)) +
		p.styleHelp.Render(" +"+strconv.Itoa(len(ordered)-1))
}
```

Add the small app helpers `HostFirstGroups(groups, info) []string` and `IsHostGroup(name, info) bool` in `internal/app` (next to `GroupLabel`) if not already present; `HostFirstGroups` reuses `FilterGroupsForHost` then stable-sorts reusable names.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/ -run TestRenderGroupPills_HostFirstAndCollapse`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tui/view_row_shared.go internal/tui/view_row_shared_test.go internal/app/
git commit -m "feat(groups): host-first collapsing group-pill renderer"
```

---

### Task 6: Wire dots/tools/agents badges to the pill renderer

**Files:**
- Modify: `internal/tui/view_dots.go` (`dotGroupBadge` call site → pills from `m.dotMemberships[entry.Name]`), `internal/tui/view_list.go` (tool badge → `m.toolMemberships[key]` instead of single `m.toolGroups[key]`), `internal/tui/view_agents_rows.go` (`agentsGroupBadge` → pills)
- Test: `internal/tui/view_dots_test.go`, `internal/tui/view_list_test.go`, `internal/tui/view_more_test.go` (append render assertions)

**Interfaces:**
- Consumes: `renderGroupPills` from Task 5; `m.toolMemberships map[string][]string`, `m.dotMemberships map[string][]string`, per-agent-row `.Groups`.

- [ ] **Step 1: Write the failing test**

Add one render test per table asserting two pills show for a two-group item. Example (dots):

```go
func TestRenderDots_TwoGroupsShowTwoPills(t *testing.T) {
	m := twoGroupDotsModel(t) // entry "nvim" with dotMemberships["nvim"]=["laptop","work"]
	out := stripANSI(renderDots(m))
	if !strings.Contains(out, "[laptop]") || !strings.Contains(out, "[work]") {
		t.Fatalf("dots row lacks both group pills:\n%s", out)
	}
}
```

Mirror for tools (`m.toolMemberships`) and one agent kind.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui/ -run 'TwoGroupsShowTwoPills|TwoGroupsShowTwoPill'`
Expected: FAIL — single badge today.

- [ ] **Step 3: Write minimal implementation**

- Dots: in `view_dots.go`, replace the `dotGroupBadge(entry.Group)` group-column value with `renderGroupPills(p, m.dotMemberships[entry.Name], m.hostInfo, cols.group)`; keep the width computation reading the same source. Keep `fullMembershipDetailLines` for the selected-row detail.
- Tools: in `view_list.go`, change the group cell to read `m.toolMemberships[key]` and render via `renderGroupPills`; update `newColWidthsWithProviderPins` to size the column from `toolMemberships` (`[]string`) rather than `toolGroups` (`string`). Keep `m.toolGroups` if still used elsewhere, or remove if now dead (grep first).
- Agents: in `view_agents_rows.go`, change `agentsGroupBadge` to call `renderGroupPills(p, groups, info, colW)` (thread palette + width through, matching the column function signature).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tui/`
Expected: PASS (new + existing; fix any existing golden-row tests that asserted the old single-badge text).

- [ ] **Step 5: Commit**

```bash
git add internal/tui/view_dots.go internal/tui/view_list.go internal/tui/view_agents_rows.go internal/tui/*_test.go
git commit -m "feat(groups): render multi-group pills in dots/tools/agents tables"
```

---

## Group D — CLI

### Task 7: CLI multi-group set for tools (free)

**Files:**
- Modify: `internal/cli/groups.go` (add `groups set-tool <tool> <group>...` variadic)
- Test: `internal/cli/cmd_test.go` (append)

- [ ] **Step 1: Write the failing test**

```go
func TestGroupsSetTool_MultipleReusableGroups(t *testing.T) {
	// setup: config with logical tool "eslint" and reusable groups work, base
	root := NewRootCmd()
	root.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir,
		"groups", "set-tool", "eslint", "work", "base"})
	if err := root.Execute(); err != nil {
		t.Fatalf("set-tool multi: %v", err)
	}
	cfg, _ := config.Load(cfgPath)
	// assert eslint is a member of BOTH work and base
	// (walk cfg.Groups for both memberships)
}
```

Follow `TestDotsVariantAddListRemove`'s config/harness setup style.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestGroupsSetTool_MultipleReusableGroups`
Expected: FAIL — command missing.

- [ ] **Step 3: Write minimal implementation**

Add `newGroupsSetToolCmd` (registered in `newGroupsCmd`) that takes `<tool> <group>...` and calls `state.app.SetToolGroups(tool, groups, nil, activeHost)`. Add the cataloged flags binding if the command exposes any flags (none needed for positional groups). Register the command's action-catalog entry if the catalog test requires all commands cataloged.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/cli/groups.go internal/cli/cmd_test.go internal/actions/catalog.go
git commit -m "feat(cli): groups set-tool accepts multiple groups"
```

---

### Task 8: CLI multi-group set for dots (reject >1 reusable)

**Files:**
- Modify: `internal/cli/dots.go` (`dots group` — make `--group` repeatable, set full membership; reject multi-reusable)
- Test: `internal/cli/cmd_test.go` (append)

- [ ] **Step 1: Write the failing test**

```go
func TestDotsGroup_MultiHostOneReusableOK_TwoReusableRejected(t *testing.T) {
	// host group "testhost", reusable "work","base", dot "nvim"
	ok := NewRootCmd()
	ok.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir,
		"dots", "group", "nvim", "--group", "testhost", "--group", "work"})
	if err := ok.Execute(); err != nil {
		t.Fatalf("host+one reusable should succeed: %v", err)
	}
	bad := NewRootCmd()
	bad.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir,
		"dots", "group", "nvim", "--group", "work", "--group", "base"})
	if err := bad.Execute(); err == nil {
		t.Fatal("two reusable groups for a dot must be rejected")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestDotsGroup_MultiHostOneReusableOK_TwoReusableRejected`
Expected: FAIL — `--group` not repeatable / no rejection.

- [ ] **Step 3: Write minimal implementation**

In `internal/cli/dots.go`, add a repeatable `--group` (`StringArrayVar`) that sets the full membership via `SetDotGroupsWithState(ctx, name, groups, nil, "")`. Before calling, if more than one of the supplied groups is reusable (not a host group), return an error naming the conflicting reusable groups — mirror the `config.ValidateRoot` message. Determine reusable-ness via the app's reusable group list (`state.app` helper; add `App.ReusableGroupNames()` if none is exported). Keep `--move`/`--remove` for back-compat. Add `--group` to this command's action-catalog flag list.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/cli/dots.go internal/cli/cmd_test.go internal/actions/catalog.go
git commit -m "feat(cli): dots group accepts multiple groups, rejects >1 reusable"
```

---

### Task 9: CLI multi-group set for agent-kinds

The agents CLI (`internal/cli/agents.go`) is organized as per-kind subtrees:
`agents skills …`, `agents mcp …`, `agents plugin …`, `agents marketplace …`
(grep `Use:   "` in that file to confirm exact nouns). Add a `group` subcommand
under each, mirroring the existing subcommands' style.

**Files:**
- Modify: `internal/cli/agents.go` — add `group <name> <group>...` under the skills/mcp/plugin/marketplace subtrees
- Test: `internal/cli/cmd_test.go` (append)

- [ ] **Step 1: Write the failing test**

```go
func TestAgentsSkillsGroup_MultipleGroups(t *testing.T) {
	// setup: config with a skill package source and reusable groups work, base
	root := NewRootCmd()
	root.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir,
		"agents", "skills", "group", skillSource, "work", "base"})
	if err := root.Execute(); err != nil {
		t.Fatalf("skill multi-group set: %v", err)
	}
	cfg, _ := config.Load(cfgPath)
	// assert skillSource appears under both group "work" and group "base"
}
```

Use the existing skill-package test fixtures in `cmd_test.go` for `skillSource`
(grep for how other `agents skills` tests seed a source).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestAgentsSkillsGroup_MultipleGroups`
Expected: FAIL — command missing.

- [ ] **Step 3: Write minimal implementation**

Add `newAgentsSkillsGroupCmd` (registered under the `agents skills` subtree) with
`Use: "group <source> <group>..."`, `Args: cobra.MinimumNArgs(2)`, calling
`state.app.SetSkillGroups(source, groups, nil, activeHostForCLI(state))` with
`groups := args[1:]`. Repeat the pattern for mcp/plugin/marketplace subtrees
using `SetMcpGroups`/`SetPluginGroups`/`SetMarketplaceGroups` (context-taking —
pass `cmd.Context()`). No reusable cap. Add each command's action-catalog entry
if the catalog test requires all commands cataloged.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/cli/ internal/actions/catalog.go internal/cli/cmd_test.go
git commit -m "feat(cli): agents group set accepts multiple groups per kind"
```

---

## Final verification

- [ ] Run full suite: `go test ./internal/...` — expect all green.
- [ ] Run `/simplify` on the accumulated diff; apply fixes.
- [ ] Manual smoke (optional): `omni` TUI — on Tools/Agents pick two reusable groups (both stick); on Dots pick a second reusable (replaces first); confirm host-first pills and the "edit groups" hint.
