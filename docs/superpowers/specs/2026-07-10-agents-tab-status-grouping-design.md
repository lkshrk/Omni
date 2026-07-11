# Agents Tab Status Grouping — Design

UX revision of the agents all-view (user feedback on v2): group rows by
status like the Tools tab — not by feature — and render rows through the
same column layout as the rest of the app.

## Decisions

| Question | Decision |
|---|---|
| Grouping | Tools-tab sections: `Updates Available`, `Out of Sync`, `Installed`, `Available`, `Ignored` — same names, same order (no Quarantined) |
| Out of Sync membership | Both ↓ missing (in manifest, not installed on a targeted+available agent) and + orphan (unmanaged: installed but absent from manifest) — exact tools-tab syncStatus semantics |
| Skills orphans | NEW: skills packages present in the skills-CLI lockfile (`~/.agents/.skill-lock.json`) but not in omni's manifest render as + orphans too (user report: lock-installed packages were invisible); row action adopts the package into the manifest (manifest upsert only — no reinstall) |
| Updates membership | Plugins only: installed version ≠ latest marketplace version. Skills/mcp never (no upstream signal; skills.sh CLI has no check-only mode, MCP has no version concept) |
| Available membership | Skills find results ("Add from skills.sh") move here |
| Ignored | Never renders for now (no ignore concept in agents domain) |
| Manager column slot | Feature type: `skills` / `mcp` / `plugin` |
| Version column slot | Plugins: installed version (→ upgrade-style `current → latest` when outdated); skills: lockfile updated date (YYYY-MM-DD, never the ref); mcp: blank |
| Type chips | Keep `all/skills/mcp/plugin`; a type chip filters the SAME grouped view to one feature (per-type bespoke sections are removed) |
| Row rendering | New shared column-row helper on the existing primitives (`rowCell`/`renderSplitRow`/`fitCellText`/`colWidths` pattern); agents rows use it now, migrating `renderToolRow` onto it is a follow-up, NOT this change |

## Data changes (app layer)

- `PluginRow` gains `Version string` and `LatestVersion string` (empty when
  unknown). Sources:
  - claude adapter: `claude plugin list --json` already yields per-plugin
    `version`; latest requires `--json --available` — extend `ListPlugins`
    to one call with `--available` and split installed/available client-side
    (verify flag behavior against a real invocation; if `--available`
    changes the shape, parse both shapes defensively).
  - codex adapter: `codexPluginListResponse.Available` is already parsed and
    currently discarded — join Available version by name+marketplace.
  - Outdated := Version != "" && LatestVersion != "" && Version != LatestVersion.
- `McpServerRow`: no data changes.
- `SkillPackageRow`: no data changes (`Updated` date exists).
- Per-agent divergence rule: a manifest row missing on ANY targeted, available
  agent is Out of Sync ↓; installed on all → Installed. Agent-unavailable
  statuses don't force Out of Sync.

## TUI changes

- Replace the per-feature stacked sections in the all-view with status
  sections; each row tagged `skills|mcp|plugin` in the manager slot.
- One flatten produces `[]agentsListEntry{feature, kind(managed|unmanaged|find), localIdx, section, sortKey}`
  ordered by (section, feature, name); render and key dispatch both derive
  from it (same single-source invariant as v2).
- Key dispatch unchanged: entry's feature routes to the existing
  handleSkillsKeyMsg/handleMcpKeyMsg/handlePluginKeyMsg with the section
  cursor synced (kind maps unmanaged entries to the section handlers'
  unmanaged index space: localIdx continues to index the per-feature flatten
  the handlers already use).
- Type chips filter the same grouped list; `renderMcpTab`/`renderPluginTab`
  per-type section builders are superseded (skills find/add flows,
  forms, pickers, two-step confirms all unchanged — only grouping/row
  rendering changes).
- Sub-category prefixes in the name cell mirror tools: ↓ missing, + orphan
  (unmanaged), no marker for installed.
- Empty state per feature within a section: rows simply absent; a fully
  empty grouped view shows the existing "none"-style hint.

## Out of scope

Migrating renderToolRow onto the shared helper; ignore-lists for agents
rows; skills update-checking (needs upstream CLI support); mcp health in
rows; reordering sections.

## Testing

- App: PluginRow Version/LatestVersion from both adapters (stub Available
  data), outdated derivation, divergence rule (installed-on-one/missing-on-one).
- TUI (tui-tester): grouped render (section order/names identical to tools
  labels), plugin upgrade-style version cell, skills date cell, mcp blank,
  type-in-manager-slot, chip filtering the grouped view, cursor traversal
  and per-feature dispatch still routing (one action per feature), find
  results under Available.
- Integration (txtar-writer): `agents plugins list` surfacing version/latest
  if the CLI table changes; else none.
