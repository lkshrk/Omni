# Agents Tab v3 Tools Parity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Per-agent rows with exact tools-tab layout/keybinds, agents ignore list, plugin sha-based update detection + per-plugin update, central data load at launch, dashboard reading central state. Per `docs/superpowers/specs/2026-07-10-agents-tab-v3-tools-parity-design.md`.

**Architecture:** App layer grows the data (ignore list schema v13, skills PerAgentStatus, plugin shas + UpdatePlugin, EnabledAgentIDs, mcp/plugin group setters). TUI replaces the grouped flatten with a per-agent flatten feeding a `renderToolRow`-anatomy renderer, extends key handling to tools parity, moves agents loads into `Model.Init()`, and derives dashboard counts from the shared model state.

**Tech Stack:** Go, Bubbletea.

## Global Constraints

- Comments: almost none — only non-obvious WHY. Never `_ =` discard. Conventional Commits, commit per task, explicit `git add` paths (never -a).
- TUI tests via **tui-tester**; txtar via **txtar-writer**. New regression tests that pin a fix must be verified RED on the pre-fix commit (scratch worktree method).
- Icons/styles reused from tools verbatim: `iconInstalled ✓ / iconMissing ✗ / iconOutdated ↑ / iconOrphan + / iconIgnored – / iconWrongProv ⚠` (view.go:15-23) with `styleInstalled/styleMissing/styleOutdated/styleOrphan/styleIgnored`.
- Section labels identical to tools `sectionLabel` strings (view_list.go:241): "Updates Available", "Out of Sync", "Installed", "Available", "Ignored".
- Op scope: `g/x/d` are manifest-level even from per-agent rows.
- gen-schema trap: new config fields must be added to scripts/gen-schema/main.go hand-curated property maps for BOTH relevant defs, and versioned schema regen requires deleting the stale spec/omni.settings.vN.schema.json first.
- Anchors below are as of commit 04a6952 — verify before editing.

---

### Task 1: Config — agents ignore list + schema v13

**Files:**
- Modify: `internal/config/config.go` (AgentsConfig L407-415; CurrentVersion; migration funcs), `internal/config/loader.go` (configMigrations registry)
- Modify: `scripts/gen-schema/main.go` (property maps), regen `spec/*.json`, `docs/schema-reference.md`
- Tests: `internal/config/` beside existing migration/agents tests

**Interfaces (produced):**
```go
type AgentsIgnore struct {
	Skills     []string `json:"skills,omitempty"`
	McpServers []string `json:"mcp_servers,omitempty"`
	Plugins    []string `json:"plugins,omitempty"`
}
// AgentsConfig gains: Ignore AgentsIgnore `json:"ignore,omitzero"`  — check repo precedent for nested-struct tags (omitempty has no effect on structs; GlobalIgnore at config.go:344 is the precedent to mirror exactly, including how it's written back)
```
CurrentVersion 12→13, no-op migration pair mirroring v11→v12 (loader.go registry + migrateConfigV12ToV13/migrateRawConfigV12ToV13). `make gen-schema` after deleting no stale file (v13 is new); update generator property maps for the new `ignore` object under the agents def; docs/schema-reference.md agents section.

- [ ] TDD: round-trip test (save config with ignore lists, reload, fields intact); JSON-tag stability test extension; migration chain auto-covers.
- [ ] `go test ./internal/config/` + `go build ./...`; commit `feat(config): agents ignore lists, schema v13`.

---

### Task 2: App — data layer for v3

**Files:**
- Modify: `internal/app/agents_skills_rows.go` (SkillPackageRow + rows fn), `internal/app/plugin_adapter.go`, `internal/app/plugin_claude_adapter.go`, `internal/app/plugin_codex_adapter.go`, `internal/app/plugin_rows.go`, `internal/app/plugin_ops.go`, `internal/app/agents_enable.go` (EnabledAgentIDs), `internal/app/agents_membership.go` (mcp/plugin group setters), `internal/app/app_snapshot.go` (EnabledAgents)
- New ignore accessors: `internal/app/agents_ignore.go` (+test)

**Interfaces (produced, exact names Task 4/5 consume):**
```go
// skills per-agent
SkillPackageRow.PerAgentStatus map[string]bool // agentID -> installed on disk; keys = targeted agents resolved against EnabledAgentIDs
// enabled agents
func (a *App) EnabledAgentIDs(cfg *config.RootConfig) []string // AgentsUse nil -> all InstalledAgents IDs; sorted
// snapshot
StartupSnapshot.EnabledAgents []string
// ignore
func (a *App) AgentsIgnoreSet(cfg *config.RootConfig) (skills, mcp, plugins map[string]bool)
func (a *App) ToggleAgentsIgnore(ctx context.Context, feature string, name string) (nowIgnored bool, err error) // feature: "skills"|"mcp"|"plugins"; withConfig persistence into cfg.Agents.Ignore
// plugin shas + update
InstalledPlugin.Sha, InstalledPlugin.LatestSha string
PluginRow.Sha, PluginRow.LatestSha string
func (r PluginRow) Outdated() bool // versions differ OR (both shas non-empty and differ); version comparison takes precedence when both versions present
func (a *App) UpdatePlugin(ctx context.Context, name string) (UpdatePluginResult, error) // guarded requirePluginsEnabled; per targeted+available adapter: adapter.UpdatePlugin(ctx, name); result carries per-adapter errors like AddPluginResult
// adapters
PluginAdapter gains UpdatePlugin(ctx context.Context, name string) error // claude: `claude plugin update <name>`; codex: probe `codex plugin update --help` shape, mirror claude args if same, else best-documented form
// group membership (mirror doSetSkillGroupMemberships's app method exactly)
func (a *App) SetMcpGroups(ctx context.Context, name string, groups []string) error
func (a *App) SetPluginGroups(ctx context.Context, name string, groups []string) error // withConfig update of the entry's Groups field; guard per feature
```

Sha sourcing (live-probe FIRST, before writing parsers): run `claude plugin list --json --available` once and inspect for sha fields on installed and available entries (available side documented to carry `source.sha`). If the installed side lacks a sha in CLI output, read `~/.claude/plugins/installed_plugins.json` (`gitCommitSha` per plugin) as the installed-sha source — path via home dir, missing file tolerated (shas stay empty). Codex: probe equivalent; absent → shas empty, no regression. Parsing must be defensive: absent fields → empty strings, never errors.

- [ ] TDD per unit: skills PerAgentStatus (fixture agents dirs), EnabledAgentIDs matrix (nil/empty/explicit AgentsUse), ignore accessors round-trip + toggle, sha outdated matrix (ver-pair differ / ver-pair equal / sha-pair differ / mixed ver-present-sha-differ → version wins / all empty), UpdatePlugin arg assertion via stub adapter + guard test, group setters round-trip.
- [ ] `go test ./internal/app/` + build; commits: `feat(app): skills per-agent status + enabled agents`, `feat(app): plugin sha update detection + per-plugin update`, `feat(app): agents ignore accessors + mcp/plugin group setters` (split as fits).

---

### Task 3: TUI — central load at launch + dashboard from shared state

**Files:**
- Modify: `internal/tui/model.go` (Init L717-725; enabledAgents field from snapshot), `internal/tui/update_keys.go` (switchMainTab gates L330-370 become fallbacks), `internal/tui/view_status_data.go` (statusAgentsCounts + loading treatment), `internal/tui/view_status.go` (attention row uses merged counts), `internal/tui/update.go` (row msgs refresh dashboard state)

**Behavior:**
- `Model.Init()` batch gains, per enabled section: `m.loadSkillsManifestCmd()`, `m.doLoadMcpRows()` (+`m.mcpRunning=true`), `m.doLoadPluginRows()` (+`m.pluginRunning=true`), setting the respective `*Loaded` flags so the tab-entry gates no-op. Init is a value receiver — check how existing Init dispatches stateful cmds (spinner tick etc.); if flags can't be set in Init, set them in the first Update pass via a dedicated bootMsg following the existing pattern for startup work (find how `loadTools` result seeds state and mirror; the flags may simply move into the msg handlers: `skillsLoaded=true` set on `skillsManifestLoadedMsg` — then tab-entry gate checks `skillsLoaded || skillsLoadPending` — pick the smallest correct mechanism and document it in the report).
- `statusAgentsCounts(m) agentsDashCounts` (new, view_status_data.go): merges `m.agentsSummary` (skills+manifest) with live `m.mcpRows/m.mcpUnmanaged/m.pluginRows/m.pluginUnmanaged`: missing counts from PerAgentStatus, unmanaged counts from the maps, ignoring ignored items (Task 2 ignore sets — model needs them: seed from snapshot or config via agentsSummary extension; simplest: extend DashboardAgentsSummary with IgnoredCounts? NO — keep summary as-is; TUI reads ignore sets from a model field seeded at snapshot: add `StartupSnapshot.AgentsIgnore` triple in Task 2 if not already; verify before implementing and note in report).
- Agents Data row + attention row consume the merged counts: breakdown becomes e.g. `7 skills · 3 mcp · 4 plugins` with unmanaged folded into OutOfSync; while mcp/plugin rows not yet loaded, the row shows the tools-style loading value (`statusLoadingValue`) — mirror `statusToolsLoading` shape with a `statusAgentsLoading(m)` (`m.mcpRunning || m.pluginRunning || !m.skillsLoaded` — adjust to the real flags).
- Attention OutOfSync = skills missing+unmanaged + mcp missing+unmanaged + plugin missing+unmanaged (ignored items excluded). Action logic unchanged (restore skills when skills missing, else open agents).

- [ ] Existing suite green (mechanical fixes allowed; judgment-heavy → Task 6 list). Commit `feat(tui): agents data loads at launch; dashboard reads shared state`.

---

### Task 4: TUI — per-agent flatten + tools-parity rendering

**Files:**
- Rewrite: `internal/tui/agents_all.go` (flatten), `internal/tui/agents_status.go` (per-agent classification), `internal/tui/view_agents_rows.go` (renderer)
- Modify: `internal/tui/model.go` (cursor fields unchanged; enabledAgents used)

**Flatten (produced, Task 5/6 consume):**
```go
type agentsAllRow struct {
	feature  agentsSection
	localIdx int    // index into the per-feature source list (dispatch compat)
	agentID  string // "" for find rows
	status   agentsRowStatus // gains agentsStatusIgnored
	mark     agentsSyncMark
	sortName string
}
```
- Expansion: managed items expand over `row.Agents` (empty → `m.enabledAgents`); status per (item, agent): mcp/plugin `PerAgentStatus[agentID]` (missing → OutOfSync ✗; installed → Installed ✓; agent-unavailable rows are SKIPPED — an unavailable agent is not a pairing), skills `PerAgentStatus[agentID]` bool. Plugin outdated (item-level) → that item's rows classify Updates with ↑ icon (all its agent rows — the update applies per agent CLI). Unmanaged entries stay per-agent (they already carry the owner agent) → OutOfSync `+`. Find rows → Available, agentID "". Ignored items (any feature, by name) → all their rows to Ignored, dimmed `–`, sorted last.
- Renderer: `renderAgentsToolRow(...)` mirroring `renderToolRowWithProviderPin`'s cell assembly (view_list.go:483-592): icon+gap+name left cell, agent label in the provider slot (provider styles), version cell (plugin ver/arrow or 7-char sha arrow; skills date; mcp blank), right-aligned group badge. Column widths via the tools `colWidths` approach: name floor 20, agent col floor 8, ver cap 24, group from badges, priv 0 — reuse `fitToolColumnsToScreen`-style shrink if trivially reusable, else mirror it (report which).
- Sections rendered with tools labels + Ignored last; selected-row detail lines carry over (skills/agents detail lines adapt: agent line redundant on per-agent rows — replace with per-item summary: skills list; mcp transport/command; plugin marketplace+version).

- [ ] Existing suite: update mechanical assertions; judgment-heavy failures listed for Task 6. Build clean. Commit `feat(tui): per-agent rows with tools layout for agents tab`.

---

### Task 5: TUI — tools keybind parity

**Files:**
- Modify: `internal/tui/update_keys.go` (agents key handlers), `internal/tui/update_mcp.go` / `update_plugin.go` / `commands_agents.go` (new cmds), `internal/tui/update_group_picker.go` (+ pickerMembershipMcp/Plugin kinds), `internal/tui/view_hints.go` (hint items per row state), `internal/tui/update_list.go` ONLY IF the confirm mechanism is extracted (prefer reusing agents' existing confirm pattern styled like tools)

**Keys (per selected agents row, visibility mirrors toolInlineHints logic view_hints.go:580-639):**
- `u`: plugin row with Outdated() → `m.doUpdatePlugin(name)` (new cmd → App.UpdatePlugin, spinner, reload plugin rows + summary on done); skills row → existing `doUpdateSkills` (all-skills; hint label "update"); hidden otherwise.
- `i`: missing row → per-item install: skills `doAddSkillPackage(source)` (re-add installs), mcp `doInstallMcpServer(entry)` (new cmd wrapping a.AddMcpServer with the existing config entry), plugin equivalent via a.AddPlugin; hidden on installed/unmanaged rows (unmanaged uses enter=adopt as today).
- `g`: opens group membership picker with new kinds `pickerMembershipMcp`/`pickerMembershipPlugin` → `doSetMcpGroupMemberships`/`doSetPluginGroupMemberships` (new cmds → Task 2 setters); skills kind exists.
- `x`: `m.doToggleAgentsIgnore(feature, name)` (new cmd → App.ToggleAgentsIgnore; on done reload ignore sets + reclassify). No scope picker (single list) — hint label "ignore"/"unignore".
- `d`: manifest delete with two-step confirm in the tools style (second press of `d` confirms, Back cancels, timeout) — adapt the existing mcp/plugin delete confirms to this pattern and add skills delete (new cmd → App needs RemoveSkillPackage? NONE exists — add `App.RemoveSkillPackage(source)` manifest-removal-only in this task's app touch, guarded, one-line WHY: uninstall of installed files is out of scope, manifest removal only, mirroring RemoveMcpServer's manifest behavior — VERIFY RemoveMcpServer semantics first and mirror whatever install-side behavior it has).
- Enter on unmanaged = adopt (unchanged); enter on find rows = add (unchanged).
- Hints: extend the `hintCtx*` items for agents rows to the tools vocabulary (`u upgrade · g move group · x ignore · d delete`, `i install` on missing) with the same conditional visibility; confirm-armed footer mirrors tools' danger-styled confirm hint.

- [ ] Suite green (mechanical fixes; rest listed). Commit `feat(tui): tools keybind parity for agents rows` (+ `feat(app): remove skill package from manifest` if split).

---

### Task 6: tui-tester — v3 coverage

Dispatch **tui-tester** with Tasks 3-5 reports. Coverage: per-agent expansion (targeted, all-enabled, agent-unavailable skipped), section feeds incl. Ignored + dimming + sorted-last, icon parity per state (✓✗↑+–), version cell (ver arrow, sha arrow 7-char, skills date, mcp blank), keybind visibility matrix per row state, two-step delete confirm + timeout + cancel, ignore toggle round-trip reclassifies, group picker kinds persist, per-plugin update dispatch, launch loads (Init emits the three cmds; tab-entry no-op after), dashboard merged counts + loading value + unmanaged in OutOfSync, plus all Task 3-5 leftover failures. Pin-tests verified RED where they guard fixes.

---

### Task 7: Integration + docs + full suite

- **txtar-writer**: v13 migration fixture (v12 config migrates, version literal asserts); agents ignore round-trip via config if any CLI surface prints it (else skip); verify existing agents fixtures still green (schema version bump WILL break fixtures asserting `"version": 12` — sweep them, same class as the v10 sweep).
- Docs: tui.md agents tab v3 (per-agent rows, keybinds, ignore), configuration.md agents.ignore, schema-reference.md (Task 1 did), cli.md untouched unless plugins list output changed.
- Full `make test`; commit docs.

## Self-Review Notes

- Dispatch-compat: `localIdx` still indexes per-feature source lists, so existing per-feature handlers keep working; per-agent expansion only multiplies render rows, and cursor→(feature,localIdx,agentID) mapping stays single-sourced in the flatten. The CRITICAL flatten/render order invariant carries over.
- Unverified externals called out for live probing before coding: claude installed-side sha field, codex `plugin update` existence, installed_plugins.json shape. All degrade to empty/absent gracefully.
- Schema bump v13: remember generator maps + fixture version-literal sweep (two known traps from this repo's history).
