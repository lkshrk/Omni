# Agents Tab Status Grouping Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Regroup the Agents tab into tools-style status sections (Updates Available / Out of Sync / Installed / Available) with type-in-manager-slot and version/date column, per `docs/superpowers/specs/2026-07-10-agents-tab-status-grouping-design.md`.

**Architecture:** The v2 flatten (`agentsAllRowsList`) keeps its `(feature, localIdx)` entry universe — key dispatch is untouched — but entries gain a status classification and are ordered by (section, feature, name) instead of feature blocks. Rendering goes through a new shared column-row helper built on the existing `rowCell`/`renderSplitRow` primitives. `PluginRow` gains `Version`/`LatestVersion` sourced from both adapters to feed the Updates section.

**Tech Stack:** Go, Bubbletea (charm.land v2 fork).

## Global Constraints

- Comments: almost none — only non-obvious WHY.
- Error handling: never `_ =` discard.
- TUI tests via **tui-tester**; txtar via **txtar-writer**. Never inline.
- Conventional Commits; commit per task.
- Section labels EXACTLY match the tools tab (`sectionLabel`, internal/tui/view_list.go:241): "Updates Available", "Out of Sync", "Installed", "Available". "Ignored"/"Quarantined" never render for agents rows.
- Sub-category markers mirror tools `syncStatus`: ↓ missing, + orphan (check the exact glyph/style constants used by the tools renderer and reuse them).
- Manager-slot text exactly: `skills` / `mcp` / `plugin`.
- Version slot: plugins → installed version, upgrade-style `current→latest` when outdated (reuse `compactVersion`/`fitUpgradeVersionText` from view_list.go); skills → `SkillPackageRow.Updated` (YYYY-MM-DD, never Ref); mcp → blank.
- CRITICAL INVARIANT (carried from v2): the flatten and the rendered row order must be identical — one function feeds both. `(feature, localIdx)` pairs must remain exactly the per-feature index spaces the key handlers already use.
- Key dispatch, forms, pickers, two-step confirms, find/add flows: unchanged behavior.

---

### Task 1: App — plugin versions + latest (Updates feed) + skills unmanaged rows

**Additional scope (user report, verified on this host):** lock-installed skill packages absent from the manifest are invisible today. Add:
- `func (a *App) UnmanagedSkillPackages() ([]SkillPackageRow, error)` in `internal/app/agents_skills_rows.go` — lock sources (unique, via the same `config.LoadSkillLock` path `SkillPackageRows` uses) minus manifest sources; each row: Source, Name (repo part), `Updated` from max lock UpdatedAt for that source, `Installed: true`. Reuse/refactor `packageLockStatus`'s iteration.
- `func (a *App) AdoptSkillPackage(source string) (config.SkillPackage, error)` — guard `requireSkillsEnabled`, then `withConfig` + `upsertPackage` ONLY (the package is already installed; no skills-CLI invocation — one-line WHY comment). Mirror the persistence shape of `AddSkillPackage` (agents_add.go) minus the runner call.
- Tests: lock with 2 sources / manifest with 1 → one unmanaged row with correct Updated + name; adopt round-trips into saved config and disappears from UnmanagedSkillPackages; adopt gated by skills_disabled.

**Files:**
- Modify: `internal/app/plugin_adapter.go` (interface + InstalledPlugin), `internal/app/plugin_claude_adapter.go` (ListPlugins), `internal/app/plugin_codex_adapter.go` (ListPlugins), `internal/app/plugin_rows.go` (PluginRow + PluginRows)
- Modify: `internal/app/plugin_rows_test.go`, adapter tests

**Interfaces:**
- Produces: `InstalledPlugin.LatestVersion string` (empty = unknown); `PluginRow.Version string`, `PluginRow.LatestVersion string`, `func (r PluginRow) Outdated() bool`.

- [ ] **Step 1: Failing tests**

Adapter tests (mirror existing ListPlugins tests in each adapter's test file):
- claude: stub exec returning `claude plugin list --json --available` output where an installed entry (`id: "foo@mkt", version: "1.0.0", enabled: true`) coexists with an available entry for the same identity at `2.0.0` → `InstalledPlugin{Name: "foo", Marketplace: "mkt", Version: "1.0.0", LatestVersion: "2.0.0"}`. IMPORTANT: the real `--available` JSON shape is undocumented — the implementer runs `claude plugin list --json --available` live ONCE to capture the true shape before writing the stub (if claude is unavailable on this host, use the flag-less shape for installed and mark available-parsing defensive with a shape-mismatch fallback to LatestVersion="").
- codex: the existing stubbed `codexPluginListResponse` gains an `available` entry matching an installed plugin by name+marketplace → LatestVersion joined; unmatched available entries ignored.
- rows: `TestPluginRows_VersionAndOutdated` — stub adapter returns InstalledPlugin with Version/LatestVersion; PluginRow carries both; `Outdated()` true iff both non-empty and different.

- [ ] **Step 2: RED**

Run: `go test ./internal/app/ -run 'TestPluginRows_Version|ListPlugins'`
Expected: FAIL (unknown fields).

- [ ] **Step 3: Implement**

`plugin_adapter.go`: add `LatestVersion string` to `InstalledPlugin` (keep the existing "informational only" comment truthful — update it: version is informational; LatestVersion feeds the Updates section).

claude adapter: change the list invocation to `claude plugin list --json --available`; parse installed entries as today; join available entries (same `splitPluginIdentity` on `id`) by name+marketplace onto installed entries' LatestVersion. Defensive: if the payload has no available entries (older CLI or flag rejected → non-zero exit), fall back to the plain `claude plugin list --json` call so listing never regresses:

```go
out, stderr, err := a.exec.Run(ctx, "claude", "plugin", "list", "--json", "--available")
if err != nil {
	out, stderr, err = a.exec.Run(ctx, "claude", "plugin", "list", "--json")
	if err != nil {
		return nil, fmt.Errorf("claude plugin list: %w: %s", err, stderr)
	}
}
```

(Adapt to the adapter's real exec field/signature; keep parse of a bare array of entries; entries carrying `enabled=false`… follow current filtering behavior unchanged.)

codex adapter: after parsing `resp`, join `resp.Available` by (name, marketplace) into the installed entries' LatestVersion. `Available[].Version` is `*string` — nil-safe deref.

`plugin_rows.go`: add `Version`, `LatestVersion` to `PluginRow`; in `PluginRows()` populate from the first agent's InstalledPlugin that has a non-empty Version (agents report the same plugin; first-non-empty wins, LatestVersion likewise). Add:

```go
func (r PluginRow) Outdated() bool {
	return r.Version != "" && r.LatestVersion != "" && r.Version != r.LatestVersion
}
```

- [ ] **Step 4: GREEN + suite**

Run: `go test ./internal/app/` → PASS; `go build ./...`.

- [ ] **Step 5: Commit**

```bash
git add internal/app
git commit -m "feat(app): plugin installed/latest versions feed update detection"
```

---

### Task 2: TUI — status classification + grouped flatten

**Files:**
- Modify: `internal/tui/agents_all.go`
- Create: `internal/tui/agents_status.go`

**Interfaces:**
- Consumes: `agentsAllRow{section agentsSection, localIdx int}` universe from v2 (field `section` here means FEATURE — rename for clarity, see below), `skillsVisibleRows`, `mcpUnmanagedFlat`/`pluginUnmanagedFlat`, `PerAgentStatus` maps, Task 1 `PluginRow.Outdated()`.
- Produces (Task 3 renders from these):
  - `type agentsRowStatus int` with `agentsStatusUpdates / agentsStatusOutOfSync / agentsStatusInstalled / agentsStatusAvailable`
  - `type agentsSyncMark int` with `agentsMarkNone / agentsMarkMissing / agentsMarkOrphan`
  - `agentsAllRow` gains `status agentsRowStatus`, `mark agentsSyncMark`, `sortName string`
  - `agentsAllRowsList(m Model) []agentsAllRow` — same entries as today, ordered by (status, feature, sortName); disabled features still excluded
  - `func agentsRowStatusFor(...)` classification helpers per feature

- [ ] **Step 1: Rename for clarity (mechanical)**

`agentsAllRow.section` (holds `agentsSection` = feature) renames to `feature`; `agentsAllSectionAt` renames to `agentsAllEntryAt` returning the full `agentsAllRow`. Update the ~6 call sites (handleAgentsAllKeyMsg, renderAgentsAllTab, tests). `go test ./internal/tui/` stays green before proceeding.

- [ ] **Step 2: Classification (in agents_status.go)**

```go
type agentsRowStatus int

const (
	agentsStatusUpdates agentsRowStatus = iota
	agentsStatusOutOfSync
	agentsStatusInstalled
	agentsStatusAvailable
)

type agentsSyncMark int

const (
	agentsMarkNone agentsSyncMark = iota
	agentsMarkMissing
	agentsMarkOrphan
)

// missingOnTargetedAgent: a manifest row is out of sync when any targeted,
// available agent lacks it; agent-unavailable statuses do not count.
func mcpRowStatus(r app.McpServerRow) (agentsRowStatus, agentsSyncMark) {
	for _, st := range r.PerAgentStatus {
		if st == app.McpStatusMissing {
			return agentsStatusOutOfSync, agentsMarkMissing
		}
	}
	return agentsStatusInstalled, agentsMarkNone
}

func pluginRowStatus(r app.PluginRow) (agentsRowStatus, agentsSyncMark) {
	for _, st := range r.PerAgentStatus {
		if st == app.PluginStatusMissing {
			return agentsStatusOutOfSync, agentsMarkMissing
		}
	}
	if r.Outdated() {
		return agentsStatusUpdates, agentsMarkNone
	}
	return agentsStatusInstalled, agentsMarkNone
}

func skillRowStatus(r app.SkillPackageRow) (agentsRowStatus, agentsSyncMark) {
	if !r.Installed {
		return agentsStatusOutOfSync, agentsMarkMissing
	}
	return agentsStatusInstalled, agentsMarkNone
}
```

(Verify the exact `McpStatus`/`PluginStatus` constant names — `installed/missing/agent-unavailable` variants exist per mcp_rows.go/plugin_rows.go. Unmanaged flat entries are always `agentsStatusOutOfSync, agentsMarkOrphan`. Skills find rows — indices ≥ findStart from `skillsVisibleRows` — are always `agentsStatusAvailable`. A plugin row that is BOTH missing somewhere and outdated goes to Out of Sync — missing wins, matching tools-tab precedence where sync problems outrank updates; state this in a one-line WHY comment.)

- [ ] **Step 3: Reorder flatten (incl. skills unmanaged)**

`agentsAllRowsList` builds the same entry set as today (per-feature localIdx spaces unchanged) but each entry now carries status/mark/sortName; sort with `sort.SliceStable` by (status, feature, sortName). Skills entries: rows/findStart from `skillsVisibleRows` — sortName = row name; mcp managed = row.Name; mcp unmanaged = flat entry server name; plugins likewise.

Skills unmanaged (Task 1's `UnmanagedSkillPackages`): extend `skillsVisibleRows` to append unmanaged rows after find results, returning an additional `unmanagedStart int` marker (all existing callers updated mechanically; cursor math in `clampSkillsCursor` and the skills key handler must treat unmanaged rows as valid cursor targets). Model stores them beside `skillsRows` (loaded in the same manifest-load cmd). Classification: indices ≥ unmanagedStart → `agentsStatusOutOfSync, agentsMarkOrphan`. Skills key handler: on an unmanaged row, the add/enter action dispatches a new `doAdoptSkillPackage(source)` cmd → `App.AdoptSkillPackage` → reload manifest on success (mirror an existing skills cmd/msg pair); destructive keys (d etc.) no-op on unmanaged rows.

- [ ] **Step 4: Tests inline (unit, not tui-tester — pure functions)**

Table tests in `agents_status_test.go` (package tui): each classifier (missing beats outdated; unavailable ignored; find rows Available), plus one flatten-order test: given mixed rows across features, entries come out grouped Updates→OutOfSync→Installed→Available and (feature, localIdx) pairs are preserved vs the v2 universe.

Run: `go test ./internal/tui/` → PASS (existing all-view tests will break in Task 3 when render changes — they should still pass after this task since order changes only… they WILL break here if they assert feature-block order; fix those assertions in this task and note each in the report).

- [ ] **Step 5: Commit**

```bash
git add internal/tui
git commit -m "feat(tui): status classification and grouped ordering for agents rows"
```

---

### Task 3: TUI — shared column row + grouped render

**Files:**
- Create: `internal/tui/view_agents_rows.go`
- Modify: `internal/tui/view_skills.go` (renderAgentsAllTab, retire per-type section bodies for mcp/plugin/skills grouping), `internal/tui/agents_all.go` (chip filter)

**Interfaces:**
- Consumes: Task 2 flatten; `renderSectionHeader`, `rowCell`/`rightCell`/`fitCellText`/`renderSplitRow`/`listColumnGap` (view_list.go / view_helpers.go / view_sectioned.go), `compactVersion`/`fitUpgradeVersionText`.
- Produces: `func renderAgentsGroupedTab(m Model, p palette, topLines []string, only agentsSection, filtered bool) string` — the one renderer for BOTH the all chip (`filtered=false`) and type chips (`filtered=true, only=<feature>`); `func agentsRowCells(m Model, p palette, e agentsAllRow, selected bool) (left, right []rowCell)` — the shared column layout: `[mark+name] [group badge] | [type] [version/date]`.

- [ ] **Step 1: Row helper**

`agentsRowCells` resolves per feature:
- name: skills → row.Name (find rows: the find-result label used today); mcp → server name (unmanaged: name + agent attribution as today); plugins → plugin name. Mark prefix: ↓ / + styled with the same styles the tools rows use for syncMissing/syncOrphan (locate them in renderToolRow's syncStatus handling and reuse the style, not a copy of the glyph string).
- group badge `[group]` right-aligned like tools (skills/mcp/plugins rows all carry Groups).
- type cell: `skills|mcp|plugin` in the provider-label style.
- version cell: plugins outdated → `fitUpgradeVersionText(compactVersion(Version), compactVersion(LatestVersion), w)` with the same outdated styling as tools; plugins current → Version; skills → Updated; mcp → "".

Column widths: compute a local `agentsColWidths(m)` (name flexes, type fixed ~7, version fitted like tools `cols.ver` — read how `computeColWidths` sizes `ver` and mirror the approach, do NOT reuse tools' ToolCache-coupled function).

- [ ] **Step 2: Grouped renderer**

`renderAgentsGroupedTab`: walk the Task 2 flatten (optionally filtered to one feature), emit a section header (tools `sectionLabel` strings — reuse the exact literals; do not import the tools `section` enum, agents has its own status enum) whenever status changes, then each row via `agentsRowCells` + `renderSplitRow`, selected = cursor match. Selected row resolution: all chip → `m.agentsAllCursor` indexes the (possibly filtered) list — see Step 3. Empty view → existing "none" hint style.

- [ ] **Step 3: Chip filtering on the same list**

Type chips stop dispatching to `renderMcpTab`/`renderPluginTab`/inline skills sections; `viewSkillsBody` calls `renderAgentsGroupedTab(m, p, topLines, feature, chip != agentsChipAll)`. Cursor per chip stays the existing per-feature cursor (`skillsCursor`/`mcpCursor`/`pluginCursor`) mapped through the filtered flatten (filtered list entries carry localIdx — selected row = entry whose localIdx equals that feature's cursor). All-chip cursor unchanged (`agentsAllCursor` over the full list). Key handling per chip unchanged (handlers already operate on per-feature index spaces).

Delete now-dead code (`renderMcpTab`, `renderPluginTab`, `mcpSections`/`pluginSections`/`skillsSections`, `retitleSections`) ONLY if nothing else references them — grep first; tests referencing them get updated in Task 4.

- [ ] **Step 4: Suite green**

`go build ./... && go test ./internal/tui/` — fix compilation in existing tests; behavior assertions that encoded the old per-type sections are updated to the new grouped expectations ONLY where mechanical; anything judgment-heavy is left failing and listed for Task 4's tui-tester (report them).

- [ ] **Step 5: Commit**

```bash
git add internal/tui
git commit -m "feat(tui): tools-style status grouping and shared row layout for agents tab"
```

---

### Task 4: tui-tester — grouped view coverage

Dispatch **tui-tester** with Task 2/3 report file paths and this coverage list:
- Section order/labels exactly "Updates Available" → "Out of Sync" → "Installed" → "Available"; sections with no rows absent.
- Plugin with Version≠LatestVersion renders under Updates Available with `old→new` version cell; equal versions → Installed with plain version.
- Manifest-missing skill/mcp/plugin rows under Out of Sync with ↓; unmanaged mcp/plugin rows with +.
- Skills date in version slot; mcp blank; type names in manager slot.
- Type chip filters to that feature's rows, same section grouping; find results under Available on the skills chip and the all chip.
- Cursor traversal in grouped order; one action per feature still routes (dispatch unchanged).
- Any tests Task 3 left failing.

Run `go test ./internal/tui/` green; commit `test(tui): agents status grouping coverage`.

---

### Task 5: Docs + CLI surface check

- `docs/tui.md`: rewrite the Agents Tab paragraph for status grouping.
- Check `omni agents plugins list` CLI output (internal/cli/agents.go): if it prints rows, surface Version/LatestVersion there too (small column add) and have **txtar-writer** update/extend the plugins-list fixture; if not applicable, skip.
- Full `make test`.
- Commit: `docs: agents tab status grouping`.

---

## Self-Review Notes

- Design decisions (user-confirmed): Out of Sync = missing + orphan; Ignored empty; version slot plugin-version/skills-date/mcp-blank; Updates = plugins only (research: skills.sh CLI has no check-only mode; MCP has no version concept; claude `--available` shape needs live verification — Task 1 Step 1 handles).
- Dispatch untouched by design: the (feature, localIdx) universe is stable; only ordering and rendering change.
- Risk: existing v2 all-view tests assert feature-block order — Tasks 2/3 explicitly own updating them; ones needing judgment go to Task 4's tui-tester.
