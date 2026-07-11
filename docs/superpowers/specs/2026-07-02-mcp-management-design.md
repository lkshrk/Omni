# MCP Server Management — Design (Agents Phase 3 MVP)

Turns the stubbed `mcp` filter chip into real management: declare MCP servers
once in omni's manifest, restore them into each target agent's config, detect
and adopt hand-added ones.

## Decisions

| Question | Decision |
|---|---|
| Scope | Full manage: manifest + add/remove/restore + detection |
| Write-target agents (MVP) | Claude Code, Codex — behind an adapter interface |
| Write mechanism | Delegate to agent CLIs (`claude mcp …`, `codex mcp …`); no direct config-file writes |
| Secrets | Manifest stores env var *names*; values resolved from omni's environment at restore. Non-secret literals allowed inline. No secret ever lands in settings.json |
| Unmanaged servers | Detection lists them; explicit import adopts into the manifest. omni never edits/removes what it didn't add |
| Discovery | Manual add only (name + command/URL). Registry search deferred |
| Host groups | Mirror skills: `GroupConfig.McpServers`; ungrouped servers restore everywhere |

## Out of scope (MVP)

Registry search, project-scope servers (user/global scope only), direct
config-file writing, adapters beyond claude-code and codex, plugin management
(Phase 4).

## Manifest schema (settings.json, additive)

```json
"agents": {
  "packages": [ ... ],
  "mcp_servers": [
    {
      "name": "linear",
      "transport": "stdio",
      "command": "npx -y @linear/mcp",
      "env": ["LINEAR_API_KEY"],
      "env_literal": { "LOG_LEVEL": "info" },
      "agents": ["claude-code", "codex"]
    },
    {
      "name": "grafana",
      "transport": "http",
      "url": "https://mcp.example.com/sse",
      "agents": []
    }
  ]
}
```

- `transport`: `stdio` | `http` | `sse`. `stdio` requires `command`; `http`/`sse` require `url`. Validation rejects mixed/missing fields.
- `agents`: skills-CLI agent IDs. Empty = all MVP write-target agents.
- `env`: variable names passed through from omni's environment at restore/add time. A missing variable is a per-server error; omni never writes an empty value.
- `env_literal`: inline non-secret values.
- `GroupConfig` gains `McpServers []string` (server names), same semantics as `Skills`: named servers restore only on hosts in that group; servers in no group restore everywhere.

## Adapter interface (internal/app)

```go
type McpAdapter interface {
    ID() string                                        // "claude-code", "codex"
    Available() bool                                   // binary on PATH + agent config dir exists
    List(ctx context.Context) ([]InstalledMcpServer, error)
    Add(ctx context.Context, s config.McpServer) error
    Remove(ctx context.Context, name string) error
}
```

- Adapters exec the agent's own CLI with the injected executor (same pattern
  as `findSkillPackages`): `claude mcp add -s user …` / `claude mcp list` /
  `claude mcp remove -s user`, and `codex mcp add|list|remove`.
- `InstalledMcpServer`: name, transport, command/url as reported by the
  agent's `list` output; used for status and import.
- Everything above the interface is agent-agnostic. A future agent = one new
  adapter file (Cursor, Gemini CLI, …).
- Agent whose binary is missing → status `agent-unavailable`; operations
  against it fail with a clear error, restore skips it with a warning line.

## Operations (app layer, mirrors skills verbs)

- **restore** — for each manifest server × target agent (group-filtered):
  `Add` if not present in the adapter's `List`. Per-server errors (missing env
  var, adapter failure) are collected and reported; the rest continues.
- **add** — validate, upsert manifest entry (by name), then `Add` on each
  target adapter immediately. Manifest persist follows the install-then-save
  order used by `AddSkillPackage`, including the "installed but not saved —
  re-run to persist" error wrap.
- **remove** — `Remove` on each target adapter, then delete the manifest
  entry. Only ever removes servers present in omni's manifest.
- **import** — diff adapter `List` against the manifest; unmanaged servers are
  offered for adoption. Import copies name/transport/command/url and env var
  *names* only — never values.
- **status** — per server × agent: `installed` | `missing` | `unmanaged` |
  `agent-unavailable`.

## TUI

- `mcp` chip in the Agents tab becomes a real list. Rows = manifest servers:
  name, transport, target-agent badges with install markers — same visual
  language as the skills package rows.
- An "unmanaged" section below lists detected-but-unmanifested servers;
  `i` imports the highlighted one.
- Keys on manifest rows: `a` agents picker (reuse the existing per-package
  picker), `r` restore, `d` remove (confirm prompt), `g` group.
- Add flow: small form popup — name, transport (cycle), command or URL,
  env var names. The only new TUI component; validation errors inline.
- Spinner/status wiring reuses the skills add machinery (`m.searching` /
  running flags → statusbar).

## CLI

```
omni agents mcp list                       # manifest + status per agent
omni agents mcp add <name> --transport stdio --command "npx -y @x/mcp" \
    [--env KEY ...] [--env-literal K=V ...] [-a agent ...]
omni agents mcp remove <name>
omni agents mcp restore [--dry-run]
omni agents mcp import [<name>]            # no arg: list unmanaged
```

Same command shape as `omni agents skills`.

## Error handling

- Adapter exec failures wrap stderr into the returned error (skills pattern).
- Missing env var: error names the variable and the server; restore continues
  with other servers.
- `remove` of a manifest server that an adapter no longer has = success
  (idempotent), not an error.
- No `_ =` error discards (project rule).

## Testing

- Adapters take the injected exec func — unit tests cover arg construction and
  `list` output parsing with canned strings, no binaries needed.
- App-level tests: restore/import/status matrix over fake adapters.
- Integration: txtar fixtures ship fake `claude` and `codex` scripts (same
  mechanism as the fake `npx` skills runner), covering add → list → status,
  restore idempotency, import adoption, missing-env failure.
- TUI tests via the tui-tester agent; txtar via txtar-writer (project rules).
