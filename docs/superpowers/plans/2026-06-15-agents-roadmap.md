# Agents Feature Roadmap

Status snapshot and phased plan for omni's AI-agent resource management.

## Done

- **Skills as package-level, group-targeted (P1)** — merged to `main`, released in v0.8.8's lineage. `SkillPackage{Source,Ref,Agents}` flat manifest, `GroupConfig.Skills` membership, ungrouped-everywhere restore, legacy migration.
- **Find/add + tools-style Agents tab (P2)** — on branch `feat/skill-find-add` (NOT merged): `skills find`/add, dual pill filter (type stub + agent), shared sectioned-list/search/hint components.
- **Recent live-testing fixes (P2 branch)** — no preselected row on activate (cursorHidden), search box above pills, shared `m.searching` spinner, intent-based agent dimension (configured `-a` targets, not filesystem).

## Open threads → phases

### Phase 1 — Harden & merge the Agents tab (ready; no new design)
1. **Tests for the recent fixes** (tui-tester): cursorHidden honored in render (nothing selected on activate); search box renders above the pills; `m.searching` drives the footer spinner on find; `row.Agents` reflects `effectiveSkillAgents` (configured targets), not fs.
2. **Per-row skill list** — under the highlighted package row, list its individual skill names (from the lockfile, entries whose `source == package.Source`), like the Tools description line. Add `Skills []string` to `SkillPackageRow` (lockfile-derived); render as `details` above the keybind hint on the selected row. Installed-only (missing packages show none).
3. **Final review** of the P2 branch; **rebase + merge** `feat/skill-find-add` → `main`.

### Phase 2 — Find/add polish (small)
- End-to-end check against the real `skills find`/`add` (npx): empty results, errors, ref handling, dedup vs existing packages.
- Add-in-flight animation (currently `skillAddRunning` has no spinner; reuse `m.searching`/a status while `skills add` runs).
- Decide whether the find section auto-clears after a successful add (it does) and whether to keep the query.

### Phase 3 — MCP management (needs a design round / brainstorm)
- **Unknowns:** does the `skills` CLI cover MCP servers, or is MCP per-agent config (e.g. `claude mcp add`, codex config, mcp.json)? How does omni track an MCP manifest and write each agent's MCP config? Detection of installed MCP servers.
- Turns the stubbed `mcp` filter chip into a real list. Own spec → plan → implement cycle.

### Phase 4 — Plugin management (needs a design round / brainstorm)
- Same shape as MCP: how plugins are defined/installed per agent, omni's manifest, the `plugin` filter chip. Own spec → plan → implement.

### Phase 5 — Release
- Bundle Phases 1–2 (skills complete) into a release; MCP/plugin land in later releases as they complete.

## Sequencing

Phase 1 → 2 are execution-ready now (subagent-driven). Phases 3 and 4 each need their own brainstorming (design) before planning, because how omni manages MCP servers and plugins per agent is genuinely undetermined. Recommend: finish 1 + 2, merge, release; then brainstorm MCP (3) as a fresh design.
