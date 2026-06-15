# Skill Packages + Groups Design

**Goal:** Make agent-skill *packages* (source repos) first-class, group-targeted resources in omni — installable individually from the TUI (typed source or `skills find` discovery), grouped like tools/dots, and restored per active host group.

**Status:** Design approved 2026-06-15. Next step: implementation plan via writing-plans.

---

## Background

Today agent skills live in a flat per-skill manifest `agents.skills` (`config.AgentsConfig.Skills []ManifestSkill`), each entry `{Name, Source, Ref, SkillPath, Agents}`. Restore/import/update drive the upstream vercel-labs `skills` CLI. A per-host "agents to use" selection (`Settings.AgentsUse`) and feature toggle (`Settings.AgentsDisabled`) already exist and are unchanged by this work.

Two gaps motivate this design:
1. No way to add a single skill package from the TUI — only bulk restore/import/update.
2. Skills aren't integrated into the group concept that gates tools/dots per host.

## Key decisions (locked)

- **Unit = package.** The source repo (`owner/repo`) is the unit of reference. We do NOT split a package into individual skills. One row per package. `skills add <source>` installs the whole package.
- **Flat definition manifest, group-side membership.** Definitions stay in a flat list; group membership is stored in the group (like tools), not on the package.
- **Ungrouped restores everywhere; groups scope.** A package referenced by no group restores on every host. A package referenced by one or more groups restores only where one of those groups is active. This DIVERGES from tools (where ungrouped = never installs); the divergence is intentional — skills default to global, groups opt into per-host scoping.
- **Discovery via `skills find`.** The CLI command is `find` (not `search`); hits skills.sh; no `--json`.
- **Migration as ungrouped.** Legacy flat `agents.skills` were effectively global, so they migrate, deduped by source, into `Agents.Packages` with NO group membership — preserving their restore-everywhere behavior.

## Data model

Replace per-skill `ManifestSkill` with a package type:

```go
// SkillPackage is a source repo of agent skills, tracked as one unit.
type SkillPackage struct {
    Source string   `json:"source"`           // owner/repo — identity
    Ref    string   `json:"ref,omitempty"`
    Agents []string `json:"agents,omitempty"`  // per-package agent override
}
```

- Flat definitions: `config.AgentsConfig.Packages []SkillPackage` (rename of `Skills`, now package-level). `AgentsConfig` keeps only this; the feature toggle and `AgentsUse` remain in `Settings`.
- Membership: `config.GroupConfig.Skills []string` — package source references the group contains, alongside `Tools []ToolEntry` and `Dots []DotEntry`.
- Display name: repo segment of `Source` (`vercel-labs/agent-skills` → `agent-skills`).
- `ValidateRoot`: validate each package `Source` non-empty; validate `GroupConfig.Skills` entries reference a known package source (warn/skip, do not hard-fail — match group validation leniency).

## Resolution & restore

Adapts `resolveTools` (`internal/app/app_resolver.go`) but with an ungrouped-everywhere rule instead of group-gating:

1. Compute active host groups (`activeHostGroupNames` / `effectiveHostGroups`) — host's own group always included.
2. Compute the set of grouped sources = every source listed in any `GroupConfig.Skills` across ALL groups. A package whose source is in this set is "grouped"; otherwise "ungrouped".
3. Resolve the set to restore = (all ungrouped packages) ∪ (packages referenced by an ACTIVE group). Dedup by source, preserve first-seen order; record per-source active-group memberships (for the table badge).
4. For each resolved package:
   - agents = `effectiveSkillAgents(hostAgentsUse, pkg.Agents)` (existing intersection rule: host `AgentsUse`, narrowed by `pkg.Agents` when set; nil host list → omit `-a`, CLI auto-detects).
   - command: `skills add <source>[#ref] -g -a <agents…> -y` (no `-s`).
5. Installed/updated detection per package: a package is *installed* if any `.skill-lock.json` entry's source matches `pkg.Source`; *updated* = latest timestamp across its matching entries. Source matching uses the lockfile entry `source` field (and `sourceURL` containing `owner/repo`).

`skills update` semantics unchanged (`skills update -g -y [names…]`); the CLI has no `-a`. Target names derived from the resolved packages' lockfile skill entries.

## Migration

On config load, if legacy `agents.skills` (old `Skills`) is present:
1. Dedup legacy entries by `Source` → `SkillPackage{Source, Ref, Agents}` appended to `Agents.Packages`.
2. No group membership is added — legacy skills were global, and ungrouped packages restore everywhere, preserving that behavior.
3. Drop the legacy `Skills` field.

Idempotent: once `Skills` is gone the migration is a no-op. Runs in the same load/normalize path as other config migrations.

## TUI — Agents tab

### Table
- Rows = packages resolved from active groups, deduped by source, grouped by status (Installed / Not Installed) — current split-row styling kept.
- Columns: `package  source  ref  [group badge]  status  updated`. Group badge mirrors `renderToolRow`'s group cell.
- Footer keys: `a add · g group · r restore · i import · u update`.

### Add / find flow
New `a` keybind opens an input with two paths:
- **Typed source**: `owner/repo`, github URL (normalized to `owner/repo`), optional `#ref`.
- **Find**: type a query → run `skills find <query>` (background command), ANSI-strip stdout, parse result lines (`owner/repo@skill  [N installs]`, URL on the following line) into a selectable list. On pick, strip `@skill` → `owner/repo` (whole package). Fallback: registry API `GET https://skills.sh/api/search?q=<query>&limit=10` is documented as an alternative but not required for P1.

After a source is chosen: create the `SkillPackage` in `Agents.Packages` (if new) and run `skills add <source> -a <agents> -g -y`. The package is ungrouped by default → restores everywhere. Grouping is optional and done later via the `g` keybind; the add flow does not force the group picker.

### Group editing
`g`/`MoveGroup` keybind on a package row opens the shared group picker → `SetSkillGroups(source, groups, createdGroups, host)` (+ `…WithState`), which adds/removes the source ref across `cfg.Groups[*].Skills`, mirroring `setToolGroupsInConfig` / `app_membership.go`. New groups created via the existing `groupPickerNewSentinel`.

## Import / update

- `import`: read `.skill-lock.json`, collapse entries by source into packages, add new packages to `Agents.Packages` ungrouped (→ restore everywhere). Dedup by source. Existing packages: update `Ref` when changed.
- `update`: unchanged.

## App API

New/changed methods (mirror `app_membership.go`, `app_resolver.go`):
- `SkillPackageRows() ([]SkillPackageRow, error)` — resolved packages with status, ref, updated, group memberships.
- `SetSkillGroups(source string, groups, createdGroups []string, activeHost string) error` and `SetSkillGroupsWithState(...)`.
- `AddSkillPackage(ctx, source, ref string, groups []string) (...)` — register + group + `skills add`.
- `FindSkillPackages(ctx, query string) ([]FindResult, error)` — run `skills find`, parse.
- Package-aware `RestoreSkills` / `ImportSkills` (replace the per-skill manifest reads).

## Error handling

- Follow CLAUDE.md: never discard errors with `_ =`; return or surface (TUI status / row error).
- `skills find` failure → surface as an add-flow error, do not crash the tab.
- Lockfile absent → packages render as not-installed (existing `LoadSkillLock` returns empty on missing).
- Source-ref with no matching package definition → skipped on restore, logged.

## Testing

- **config**: migration (legacy → ungrouped packages), resolution dedup, ungrouped-restores-everywhere + active-group scoping. `EffectiveSettings` AgentsUse already covered.
- **app**: restore effective-agents per package (extend existing), installed/updated detection by source, `skills find` output parser, `SetSkillGroups` mutation.
- **TUI** (tui-tester): table render with group badge + status grouping, add input flow, find list + pick, group picker save. 
- **integration** (txtar-writer): any new CLI surface (e.g. `omni agents add <source>`), restore/import package behavior.

## Phasing

- **P1** — data model + migration + group-gated restore + table (with group badge) + group editing. Makes packages first-class.
- **P2** — add/find flow (typed source + `skills find` discovery).

Each phase ships working, testable software.
