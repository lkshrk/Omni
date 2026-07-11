# Plugin Management — Design (Agents Phase 4 MVP)

Turns the stubbed `plugin` filter chip into real management: declare agent
plugins and their marketplaces once in omni's manifest, restore them into
Claude Code and Codex via their CLIs, detect and adopt hand-installed ones.
Third instance of the skills→MCP pattern; deviations from the MCP design are
called out explicitly, everything else mirrors it.

## Decisions

| Question | Decision |
|---|---|
| Scope | Marketplaces AND plugins, full manage (add/remove/restore/import/status) |
| Write-target agents (MVP) | Claude Code, Codex — behind a PluginAdapter interface |
| Write mechanism | Delegate to agent CLIs (`claude plugins …`, `codex plugin …`); no direct config-file writes |
| Versions | No pinning — manifest stores identity only; installs get whatever the marketplace serves |
| Unmanaged plugins | Detection lists them; explicit import adopts identity only. omni never edits/removes what it didn't add |
| Host groups | Mirror skills/MCP: `GroupConfig.Plugins`; marketplaces follow their dependent plugins at restore |
| Enable/disable state | Out of scope (claude-only concept; not reconciled) |

## Ground truth (probed live, 2026-07-02)

- claude: `claude plugins install <name>[@marketplace]`, `list --json`
  (structured: id, version, scope, enabled), `marketplace` subcommand,
  enable/disable, uninstall. Plugin identity is `name@marketplace`.
- codex: `codex plugin add|list|remove` from "configured marketplace
  snapshots", `codex plugin marketplace add|list|upgrade|remove`.
- Exact flag shapes, uninstall/remove syntax, and list output formats for
  BOTH CLIs must be re-verified against the live binaries (sandboxed
  CLAUDE_CONFIG_DIR / CODEX_HOME) at the START of implementation — the MCP
  round proved assumptions here fail silently. `--json` variants preferred
  wherever they exist.

## Out of scope (MVP)

Version pinning, enable/disable reconciliation, plugin scaffolding/tagging
(`claude plugins init|tag|details`), prune/autoremove, adapters beyond
claude-code and codex, registry/marketplace discovery search.

## Manifest schema (settings.json, additive, version 11)

```json
"agents": {
  "packages": [ ... ],
  "mcp_servers": [ ... ],
  "marketplaces": [
    { "name": "caveman", "source": "lkshrk/agent-marketplace",
      "agents": ["claude-code", "codex"] }
  ],
  "plugins": [
    { "name": "caveman", "marketplace": "caveman",
      "agents": ["claude-code"] }
  ]
}
```

- `Marketplace{Name, Source, Agents}` — Source is whatever the agent CLIs
  accept (owner/repo or URL; exact accepted forms verified live in Task 1).
- `Plugin{Name, Marketplace, Agents}` — `Marketplace` must reference a
  declared marketplace name: HARD validation error if missing (a dangling
  reference makes restore impossible — stricter than the warn-level group
  refs, deliberately).
- `Agents` empty = all MVP write-target agents (same semantics as MCP).
- `GroupConfig.Plugins []string` (plugin names) mirrors `Skills`/`McpServers`:
  named plugins restore only on hosts in that group; ungrouped restore
  everywhere. Marketplaces are not group-targeted themselves — a marketplace
  restores wherever a plugin needing it restores.
- Names validated non-empty/unique per list; group plugin refs warn-level
  like MCP.

## Adapter interface (internal/app)

```go
type PluginAdapter interface {
    ID() string
    Available() bool
    ListPlugins(ctx context.Context) ([]InstalledPlugin, error)
    InstallPlugin(ctx context.Context, p config.Plugin) error
    RemovePlugin(ctx context.Context, name string) error
    ListMarketplaces(ctx context.Context) ([]string, error)
    AddMarketplace(ctx context.Context, m config.Marketplace) error
}
```

- Injected-exec pattern identical to the MCP adapters; unit tests use real
  captured CLI output as fixtures.
- `InstalledPlugin{Name, Marketplace, Version}` — version informational only.
- Marketplace removal is NOT in the interface: omni never removes
  marketplaces (they may serve hand-installed plugins omni doesn't know
  about). Follow-up if ever needed.
- Unavailable agent → status `agent-unavailable`; restore skips with warning.

## Operations (app layer, mirrors mcp_ops.go)

- **restore** — group-filter plugins; compute the marketplace set they need;
  per target agent: add missing marketplaces first, then install missing
  plugins. Per-adapter/per-item tolerant: errors collected
  (`PluginError{Agent, Name, Err}`), rest continues, manifest never modified
  by restore.
- **add** — validate (marketplace ref exists), upsert manifest, then install
  on each target adapter (adding the marketplace first if the agent lacks
  it). Manifest persists regardless of adapter outcomes (manifest = intent);
  result carries per-adapter errors. Same for marketplace add.
- **remove** — uninstall from each target adapter (tolerant), then delete
  the manifest entry. Marketplaces stay (see above). Unmanaged names
  rejected.
- **retarget** (`SetPluginAgents`) — diff old vs new targets, install on
  newly selected, uninstall from deselected. Same semantics as
  SetMcpServerAgents.
- **import** — diff adapter ListPlugins against manifest; adopt copies
  name + marketplace only. If the plugin's marketplace is unknown to the
  manifest, import adds a marketplace entry too (source read from the
  agent's marketplace list when the CLI exposes it; otherwise the import
  errors and tells the user to declare the marketplace first — verified
  capability in Task 1).
- **status** — per plugin × agent: installed | missing | unmanaged |
  agent-unavailable.

## CLI

```
omni agents plugins list
omni agents plugins add <name> --marketplace <mkt> [-a agent ...]
omni agents plugins remove <name>
omni agents plugins restore [--dry-run]     # WouldInstall vs Skipped split, like mcp
omni agents plugins import [<name>]         # no arg: list unmanaged; name: adopt
omni agents plugins marketplace list|add <name> --source <src> [-a agent ...]|remove <name>
```

`marketplace remove` deletes only the manifest entry (agents keep theirs).
Behavioral CLI tests required — not registration-only (MCP retro lesson).

## TUI

- `plugin` chip (skillTypeIdx == 2) becomes a clone of the mcp chip: managed
  rows `name  marketplace  agent-badges`, unmanaged section, `r/i/a/d` keys
  (same confirm + picker patterns), `n` opens an add form (name, marketplace
  pick-or-text, agents).
- Marketplaces surface as a detail line on the selected plugin row, not as
  their own rows (YAGNI; revisit if marketplace-only management matters).
- All spinner/error surfacing through the existing running/status machinery.

## Error handling

Identical rules to MCP: adapter exec failures wrap stderr; per-item errors
non-fatal in batch operations; idempotent removes; no `_ =` discards.

## Testing

1. Unit: adapters with injected exec + REAL captured output fixtures; ops
   matrix over fake adapters (restore/add/remove/retarget/import/status,
   partial-failure manifest assertions, group filtering).
2. TUI: tui-tester agent (chip, keys, form, picker, error surfacing).
3. Integration: txtar fixtures with fake `claude`/`codex` binaries emitting
   the real formats.
4. **Live smoke task (explicit plan task, before final review):** sandboxed
   CLAUDE_CONFIG_DIR/CODEX_HOME run of the real binaries through the omni
   binary — add/list/remove/import round-trip, flag shapes, list parsing.
   The MCP round shipped with both list parsers matching zero real lines;
   this task exists so that cannot recur.
