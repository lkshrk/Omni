# Dashboard Agents Rows + Data Stat Columns Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Data section = stats only (breakdown middle column, Tool Sync row removed, Agents row added); Health Check gains a tools/dots-consistent agents attention row. Per `docs/superpowers/specs/2026-07-10-dashboard-agents-and-data-columns-design.md`.

**Architecture:** New `app.DashboardAgentsSummary` (config + skills lock, zero exec) rides the startup snapshot like `DashboardToolSummary` and refreshes on the existing skills-state messages. TUI: `statusOverviewRows` drops Tool Sync, gains Agents; middle summaries of Data rows become stat breakdowns; new conditional `statusAgentsAttentionRow` copied structurally from `statusToolSyncAttentionRow`.

**Tech Stack:** Go, Bubbletea.

## Global Constraints

- Comments: almost none. Never `_ =`. Conventional Commits. TUI tests via **tui-tester** only.
- Consistency mandate (user): agents rows mirror tools/dots row semantics — icons, conditional appearance, action wording patterns (`open agents` / `restore skills` mirroring `open tools` / `sync tools`).
- Dashboard stays exec-free: agents signal = skills manifest-missing + skills lock-only counts ONLY.
- Middle-column breakdowns derive from data already on the model: `DashboardToolSummary{Installed, Available, Ignored, ...}` (reconcile_plan.go:75), `DotFileCounts{Synced, OutOfSync, Ignored}` (dots.go:95), automation service status, agents manifest counts.
- Value column right-aligned (existing renderer `statusRowLine` unchanged).

---

### Task 1: App — DashboardAgentsSummary

**Files:**
- Create: `internal/app/dashboard_agents.go`, `internal/app/dashboard_agents_test.go`
- Modify: `internal/app/app_snapshot.go` (field + populate, beside `DashboardToolSummary`-analogous fields — check what the snapshot actually carries for tools and mirror the mechanism)

**Interfaces:**
- Produces:
```go
type DashboardAgentsSummary struct {
	SkillPackages   int // manifest
	SkillsInstalled int
	SkillsMissing   int
	SkillsUnmanaged int // lock-only sources
	McpServers      int // manifest
	Plugins         int // manifest
	Marketplaces    int // manifest
	AgentsEnabled   bool
}

func (a *App) DashboardAgentsSummary(cfg *config.RootConfig) (DashboardAgentsSummary, error)
func (s DashboardAgentsSummary) Managed() int      // SkillPackages + McpServers + Plugins
func (s DashboardAgentsSummary) OutOfSync() int    // SkillsMissing + SkillsUnmanaged
```
- Derivation: manifest counts from `cfg.Agents.*`; skills installed/missing via the same resolve+lock join `SkillPackageRows` uses (reuse/refactor, don't duplicate); unmanaged via `UnmanagedSkillPackages` logic (count only — extract a shared count helper if cheaper than building rows).

- [ ] TDD: fixtures = config with packages/mcp/plugins + lock with extra source → all counts asserted; disabled master → zero-value summary with `AgentsEnabled: false` (no lock read).
- [ ] Snapshot carry + populate; extend snapshot tests if they enumerate fields.
- [ ] `go test ./internal/app/` green; commit `feat(app): dashboard agents summary`.

---

### Task 2: TUI — Data stat columns + agents rows

**Files:**
- Modify: `internal/tui/view_status.go` (`statusOverviewRows` 373; new attention row beside `statusToolSyncAttentionRow` 435; action kinds), `internal/tui/view_status_data.go` (breakdown value fns; agents row fns), `internal/tui/model.go` (summary field), `internal/tui/update_setup.go`/`update.go` (snapshot assign + refresh on skills msgs), `internal/tui/update_status.go` (action handling)
- Test: mechanical fixes only; behavior coverage → Task 3

**Interfaces:**
- Consumes: Task 1 summary; existing `statusListRow`, `statusAction` kinds (`statusActionOpenTools` precedent for `statusActionOpenAgents`; `statusActionSyncTools` precedent for `statusActionRestoreSkills` → dispatches the existing `doRestoreSkills` cmd, commands_agents.go:26).

- [ ] `statusOverviewRows` → `[Tools, Dotfiles, Automation, Agents]` (Tool Sync overview removed; its attention row untouched).
- [ ] Middle-column breakdowns (replace prose `summary` on Data rows only):
  - Tools: `fmt.Sprintf("%d installed · %d available · %d ignored", c.Installed, c.Available, c.Ignored)`
  - Dotfiles: `fmt.Sprintf("%d synced · %d out-of-sync · %d ignored", ...)` (omit zero out-of-sync segment)
  - Automation: compact `reminder …/watch …` stats from the parts `statusAutomationSummary` already computes (extract shared helper; `[ON/OFF/WARN]` value unchanged)
  - Agents: `fmt.Sprintf("%d skills · %d mcp · %d plugins", ...)`
  - Keep activity/loading override mechanism exactly as-is (`statusStaleSummary`/`statusLoadingValue`).
- [ ] Agents Data row: value `N managed` (`summary.Managed()`), details lines (skills installed/missing/unmanaged, mcp, plugins, marketplaces), action `open agents`; icon fn mirrors `statusToolsOverviewIcon` shape (ok/warn by OutOfSync, quiet when disabled: value `disabled`, muted).
- [ ] `statusAgentsAttentionRow(m) (statusListRow, bool)`: visible iff `AgentsEnabled && summary.OutOfSync() > 0`; label `Agents`, value `N issues` via `statusCountValue`, summary sample (`statusSampleSummary` over missing names — carry `SkillsMissingNames []string` in the summary struct if needed for parity with Tool Sync; add to Task 1 if used), action `restore skills` when `SkillsMissing > 0` and not busy else `open agents`. Wire into `statusAttentionRows` beside the dots/tool-sync rows.
- [ ] Action handling: `statusActionOpenAgents` → switch to `viewSkills` (find how `statusActionOpenTools` switches tabs and mirror); `statusActionRestoreSkills` → `m.doRestoreSkills()` + spinner (mirror `statusActionSyncTools` handling).
- [ ] Refresh: set `m.agentsSummary` from snapshot; recompute on `skillsManifestLoadedMsg`/`skillAddedMsg`/restore-done/`agentsToggledMsg`/feature-toggle msgs — cheapest correct wiring: a `tea.Cmd` calling `App.DashboardAgentsSummary` appended wherever those handlers already reload skills state.
- [ ] `go build ./... && go test ./internal/tui/` — fix mechanical breaks (old summary-prose assertions on Data rows); list judgment-heavy failures for Task 3.
- [ ] Commit `feat(tui): dashboard agents rows + data stat columns`.

---

### Task 3: tui-tester + docs

- [ ] Dispatch **tui-tester**: Data section = exactly Tools/Dotfiles/Automation/Agents rows (no Tool Sync); each breakdown renders (fixture-driven); agents attention row appears iff OutOfSync>0 and offers restore-vs-open action correctly; agents Data row disabled state muted; details expansion; any Task 2 leftovers.
- [ ] `docs/tui.md` dashboard section update.
- [ ] Full `make test`; commit `docs: dashboard agents rows + data stat columns`.

## Self-Review Notes

- Consistency: every agents row reuses an existing row's shape (`statusToolsOverviewRow` / `statusToolSyncAttentionRow`); no new renderer.
- Exec-free constraint keeps dashboard latency unchanged; mcp/plugin sync state intentionally absent (documented in design).
- `SkillsMissingNames` addition to the summary struct is conditional — only if the attention summary uses it (Task 2 decides; update Task 1 tests accordingly).
