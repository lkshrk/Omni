# Agents Tab v3 — Tools Parity (Design)

User direction: the Agents tab uses the EXACT tools-tab layout with the
second column showing the agent name; same keybind hints/operations as
tools; agents data loads centrally at launch like everything else; the
dashboard reads the same central state (unmanaged counts included);
plugins get real update detection (versions where declared, git-sha drift
otherwise, per Claude's documented version-resolution order).

## Decisions

| Question | Decision |
|---|---|
| Row model | Per-agent rows: row = (item × agent), exactly as tools rows are per-provider. Items with empty `Agents` expand over the enabled-agent set. Find-result rows are not per-agent (agent cell blank) |
| Row layout | `renderToolRow` anatomy verbatim: status icon, name, agent label (in the provider slot), version cell, right-aligned `[group]` badge. Same icon constants (✓ ✗ ↑ + – ⚠) and styles; no privilege column (width 0) |
| Sections | Same enum semantics as tools: Updates Available (plugin update on that agent), Out of Sync (missing on that agent ✗; unmanaged orphan +), Installed, Available (find results), Ignored (dimmed, last) |
| Keybinds | Tools parity: `u` update (outdated plugin rows → per-plugin update via agent CLI; skills rows → skills update), `i` install (missing rows → item re-add/install), `g` group picker (extend picker kinds to mcp/plugin), `x` ignore (new agents ignore list), `d` delete from manifest (two-step confirm, tools style). Hint visibility rules mirror `toolInlineHints` logic |
| Op scope | `g`/`x`/`d` operate on the ITEM (manifest-level) even from a per-agent row — same as tools rows where the row is one installation but delete targets the entry. Confirm copy says so |
| Ignore | New `AgentsConfig.Ignore` — `{skills: [], mcp_servers: [], plugins: []}` (name lists). Ignored items render in the Ignored section, dimmed; `x` toggles; schema v12→v13 additive bump (+ gen-schema property maps — known trap) |
| Central load | `Model.Init()` batches the three agents loads (skills manifest+lock, mcp rows, plugin rows) beside `loadTools`, gated on section-enabled; tab-entry gates remain as no-op fallbacks (Loaded flags set at Init dispatch). Tab and dashboard read the same model fields |
| Dashboard counts | TUI-side `statusAgentsCounts(m)` merges `agentsSummary` (skills/manifest, snapshot-seeded) with live mcp/plugin row state (missing + unmanaged counts) once loaded — mirrors how tools counts derive from `m.allTools`. While loading: the same loading-value/spinner treatment tools rows use. Attention OutOfSync broadens to skills missing+unmanaged + mcp/plugin missing+unmanaged |
| Plugin updates | Claude's documented resolution order: explicit `version` (plugin.json / marketplace entry) else git commit sha. Adapters capture available-side `version`+`source.sha` and installed-side sha (CLI field if present, else `~/.claude/plugins/installed_plugins.json` `gitCommitSha`). `Outdated` = versions differ OR shas differ. Version cell: `1.0.0 → 2.0.0` when versioned, `abc1234 → def5678` (7-char shas) on sha drift |
| Per-plugin update | New adapter method `UpdatePlugin(ctx, name)` (`claude plugin update <name>` / codex equivalent) + `App.UpdatePlugin(name)` (guarded by requirePluginsEnabled) + TUI `u` wiring |
| Skills per-agent status | `SkillPackageRow` gains `PerAgentStatus map[string]bool` (installed-on-disk per targeted agent, from the existing per-agent dir checks) so skills rows expand per-agent with real state |
| Enabled agents | App exposes `EnabledAgentIDs(cfg)` (AgentsUse nil → all installed agents) — the expansion universe for empty-`Agents` items; carried on the startup snapshot |

## Out of scope

Per-agent-scoped delete (removing one agent from an item's Agents list via
`d`); mcp version/update detection (no upstream concept); marketplace
update operations; CLI surfaces for ignore (TUI-only this pass); reordering
sections.

## Testing

- Config: ignore-list round-trip, v13 migration, schema regen incl. generator maps.
- App: per-agent skills status, sha-drift outdated matrix (version-pair / sha-pair / mixed), UpdatePlugin guard + adapter args, EnabledAgentIDs resolution, mcp/plugin group-membership setters.
- TUI (tui-tester): per-agent expansion (targeted vs all-enabled), section feeds incl. Ignored, tools-icon parity, keybind visibility rules per row state, two-step delete, ignore toggle round-trip, group picker kinds, central-load-at-launch (Init cmds), dashboard counts incl. unmanaged + loading states.
- Integration (txtar-writer): schema v13 migration fixture; plugins list sha-drift output if CLI surface changes.
