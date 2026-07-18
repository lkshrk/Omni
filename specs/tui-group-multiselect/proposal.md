# TUI/CLI group multiselect proposal

Supersedes the earlier uniform-invariant version of this document
(see git history). Group assignment is now **type-specific**.

## Scope

Item group assignment across Tools, Dots, Skills, MCP servers, plugins, and
marketplaces, in both the TUI and the CLI.

## Membership model (type-specific)

Two group kinds exist, distinguished only by `GroupConfig.Special`: **host
group** (`Special=="host"`, one per machine) and **reusable/"normal" group**
(`Special==""`).

- **Tools** and **Agents** (Skills, MCP servers, plugins, marketplaces): an item
  may belong to **any number of groups of any kind** — free multi-select. No cap
  on reusable groups.
- **Dots**: an item may belong to **any number of host groups plus at most one
  reusable group** (the existing invariant). Host groups are machine-scoped;
  reusable groups are the fleet-wide collision risk, so dots — which produce
  real symlink conflicts — keep the single-reusable cap. Adding a second
  reusable group to a dot replaces the first.

Rationale for the split: dot conflicts resolve host-first for free only while at
most one reusable group can claim a path. Tools and agent-kinds have no such
filesystem collision, so they may fan out freely.

## Single authority

One capability decides the rule, consulted by every layer so they cannot drift:

- `app.MembershipCapsReusable(kind) bool` — true for `dot`, false for `tool`,
  `skill`, `mcp`, `plugin`, `marketplace`.
- TUI toggle (`selectGroupMembership`): dots → `MembershipInvariantToggle`
  (evict-second-reusable); all other kinds → plain add/remove toggle, no
  eviction.
- `config.ValidateRoot`: keep the dots single-reusable check; **remove** the
  tools single-reusable check (agent-kinds already have none).
- Write paths (`SetToolGroups`/`SetDotGroups`/agent equivalents) already persist
  the full set; dots additionally get `EnforceMembershipInvariant` as a
  defensive backstop before write.

## Bugs this fixes

- **Agents cannot multi-select**: the shared toggle evicted a second reusable
  group even though config allowed it. Removing the cap for agent-kinds lets the
  picker accumulate freely; ensure every agent-kind row (skill/mcp/plugin/
  marketplace) opens the multi picker via `g`.
- **Tools multi-select bugged**: config validation rejected a second reusable
  group, so a saved multi-membership failed validation. Removing that check plus
  the free toggle makes tools multi work.
- **Dots**: already correct — keep as-is, now explicitly the only capped kind.

## Wording

- Rename the tools inline hint / action label from `change groups`
  (`LabelChangeGroups`) to `edit groups` (`LabelEditGroups`, already defined).
  Retire `LabelChangeGroups`. Dots and Host already say "edit groups".
- Keep internal key-map (`MoveGroup`) and CLI identifiers where renaming breaks
  compatibility; only user-facing text changes.

## Table display: multiple groups

One trailing Groups column (no per-group columns), same formatter for all item
tables:

- Render each group as its own **pill**, **host group(s) first**, then reusable
  groups sorted: `[host] [work]`.
- The **host pill is styled distinctly** from reusable pills.
- **Auto-collapse** under width pressure: when the pills do not fit, show the
  first (host) pill plus an overflow count: `[host +2]`. Never overflow the row.
- Show only the current host and its assigned reusable groups in the badge;
  other hosts' memberships remain visible in the picker and the selected-row
  detail area.
- The selected row's detail area shows the **full ordered list**, uncompacted,
  so truncation never hides information.
- The Groups-tab host-assignment summary keeps its existing
  `first, second +N` form — it describes host configuration, not one item's
  membership.

## CLI

- Group-set commands accept **multiple groups** (repeatable flag or CSV) for
  Tools, Dots, and Agent-kinds.
- Dots: reject a set containing more than one reusable group with a clear error
  that names the conflicting groups (mirrors `config.ValidateRoot`).
- Tools/Agents: accept any set.
- Add the missing Agent-kind membership set command; keep single-relocate
  `move-tool` / `dots group --move` for back-compat.
- `... group <name>` with no mutation flag prints the current full membership.

## Regression seams (tests)

- App: tool/agent in two reusable groups persists and validates; dot in two
  reusable groups is rejected/clamped; a dot in many host groups + one reusable
  persists.
- TUI toggle: on tools/agents a second reusable pick accumulates (both checked);
  on dots it replaces the first.
- TUI render: host-first pills, host pill distinct, auto-collapse to `+N` at the
  computed minimum width, full list in detail.
- Hint/label reads "edit groups" on the tools row.
- CLI: multi-group set for each type; dots multi-reusable error; membership read
  round-trips.
