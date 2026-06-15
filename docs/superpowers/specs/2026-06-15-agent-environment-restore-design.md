# Omni Agent Environment Restore — Vision & Decomposition

**Date:** 2026-06-15
**Intake:** [HCL-25](https://linear.app/h-cloud/issue/HCL-25/add-omni-managed-restore-for-claudecodex-agent-environment) (team H-cloud, project Omni)
**Status:** Vision doc — defines the whole, decomposes into per-subsystem specs.

> This is a multi-subsystem vision, not a single implementation spec. Each
> subsystem below gets its own `spec → plan → build` cycle. Subsystem 1 (merge
> engine) is specced first; everything else depends on it.

## Problem

Claude Code and Codex agent state is restored today through a mix of dotfiles,
tool-specific config files, plugin caches, UI-written MCP state, and Coder
bootstrap scripts. No single source owns MCP servers, plugins, skills, or
Coder-only permission policy. This causes drift and breakage:

- MCP state is split between tracked `~/.claude/mcp.json` and local
  `~/.claude/.mcp.json`; UI-added MCP servers never land in tracked config.
- Codex keeps MCP/plugin state separately in `~/.codex/config.toml` (TOML).
- Coder startup hits `dots sync: claude: requires choosing use repo version or
  use local version` — whole-file Stow can't reconcile structured config.
- "Reinstall all Claude/Codex stuff" is not one Omni-owned operation.

## Desired outcome

Omni has first-class support for restoring the Claude/Codex agent environment
from tracked desired state, host/profile-aware (local machine vs Coder
workspace), as a single command.

## Scope

**In:** Claude Code + Codex, concretely. JSON (Claude) + TOML (Codex) structured
merge. MCP desired-state. Plugins/skills/marketplaces as versioned units from a
lockfile. Host/profile policy (Coder repo-wins sync, Coder-only YOLO). Optional
post-restore health checks.

**Out (for now):** Generic abstraction over other agents (Cursor, aider, etc.) —
adapters are designed so adding one later is cheap, but only `claude` + `codex`
are built. Community registry discovery — deferred.

## Design principle: extend, don't replace

Omni already splits **declarative state** (JSON config: tools, taps, settings,
per-host assignment) from **file payloads** (Git/Stow repo: dotfile content).
Agent support uses the same split:

- **omni JSON config** declares: which MCP servers, which config-key overrides,
  which bundles/plugins are assigned to which host/profile. Small, structured,
  diffable, per-host.
- **Git/Stow repo** holds: bundle/plugin/skill file payloads under a dedicated
  `agents/` subtree, referenced from the config by a lockfile.

This mirrors the existing tool-vs-dotfile pattern. The agent CLIs themselves
(`claude`, `codex` binaries) are ordinary tools — reuse the provider model, no
new mechanism.

## Domain model

A new top-level domain: **agents**. Each agent target is a small built-in
**adapter** declaring:

- **config root** — `~/.claude`, `~/.codex`
- **registries** — structured files Omni merges into, with format:
  `~/.claude/.mcp.json` (JSON), `~/.codex/config.toml` (TOML)
- **bundle dirs** — where unit-bundles drop: Claude `skills/`, `agents/`,
  `plugins/`; Codex plugins + marketplaces

Everything Omni manages for an agent is one of four **managed resource kinds**:

| Kind | Example | Mechanic |
|------|---------|----------|
| CLI | `claude` / `codex` binary | existing tool/provider model — reuse as-is |
| Config | settings keys, permissions | owned-block regenerate (JSON+TOML), per-host overlay |
| MCP server | context7, github | install + register (regenerate owned block in registry file) |
| Bundle | a skill / plugin / marketplace | versioned multi-file unit, restored from lockfile |

## Subsystem decomposition (dependency-ordered)

### 1. Agent adapters *(foundation — specced first)*

Declarative adapter layer for `claude` + `codex` (config roots, registry paths +
formats, bundle dirs). Thin, data-driven, so a third agent is a new table entry,
not new code. No behavior — pure description the other subsystems consume.
**Easy.**

### 2. Block-ownership writer

Omni **owns a namespaced block** in each registry file (Claude `mcpServers` in
`.mcp.json`, Codex MCP section in `config.toml`) and regenerates *that block
only* from tracked desired state, leaving the rest of the file untouched.
Block-replace, not key-level merge — so no general merge engine, no two-way
reconciliation. Marshal JSON + TOML for the owned block; splice it into the
existing file. Kills the `mcp.json` / `.mcp.json` split and the repo-vs-local
prompt for registry files. **Med-easy.** Depends on 1.

### 3. MCP desired-state

Install an MCP server (reuse provider/CLI model) **and** register it by writing
the owned block (subsystem 2). Tracked config is source of truth. **Med.**
Depends on 1 + 2.

### 4. Bundles + lockfile

Plugins / skills / Codex marketplaces as versioned units. A lockfile pins
versions; restore brings a host to the locked set (add/remove/list as a unit).
Payloads live in the `agents/` repo subtree. **Med.** Depends on 1.

### 5. Profile policy

Host/profile-aware behavior layered on Omni's existing per-host assignment:
- Coder workspaces: repo-wins dotfile sync, workspace-only YOLO agent
  permissions.
- Local machines: never default YOLO.
Encodes the "externally sandboxed → YOLO allowed" constraint as policy, not
ad-hoc scripts. **Easy–Med.** Depends on 1.

### 6. Drift import *(follow-up)*

One-way: read UI-added MCP servers out of the live registry file back into
tracked config, so UI changes can be captured deliberately instead of silently
clobbered by subsystem 2. This is the only place two-way state is touched, and
it is an explicit user-run command — not automatic merge. **Med.** Depends on 2.

### 7. Discovery *(deferred / out of scope now)*

Pull community bundles + MCP servers from a registry. Last; may be its own
future intake.

## Cross-cutting

- **Health checks:** optional post-restore verification (binary present, MCP
  server reachable, registry file valid). Small, can land with subsystem 3/4.
- **Single command:** the end-state UX is one Omni-owned restore/sync operation
  that drives all subsystems for the active host/profile.
- **Safety:** local machines must not enable YOLO by default — enforced in
  subsystem 5 and asserted in tests.

## Build order

Easy → hard: 1 (adapters) → 2 (block writer) → 3 (MCP), 4 (bundles), 5 (policy)
in parallel → 6 (drift import) → [7 deferred]. Each ships behind its own spec +
plan + tests, ≥80% coverage per project convention.

## Open questions (resolve in per-subsystem specs)

- Block writer: how is the owned block delimited inside a user-edited file —
  a known top-level key (`mcpServers`), or a fenced/commented region? JSON has a
  natural key; TOML needs the answer pinned for `config.toml`.
- Codex TOML: does Codex tolerate Omni rewriting the MCP section of
  `config.toml` in place, preserving surrounding keys/comments?
- Lockfile: format and location — inline in omni JSON, or a separate
  `agents.lock` in the repo?
