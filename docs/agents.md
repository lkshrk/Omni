# Agents

Omni manages skills, MCP servers, plugin marketplaces, and plugins across the
agent installations detected on a host. Desired state lives under `agents` in
`settings.json`; installed state remains in each agent's own directories and
configuration.

For commands and flags, see [CLI Reference](cli.md#agents-commands). For the
interactive view, see [TUI](tui.md#agents-tab).

## Choose Target Agents

By default, Omni manages every detected agent. Limit that set globally or for
one host with `agents_use`:

```json
{
  "settings": {
    "agents_use": ["claude-code", "codex"]
  },
  "host_settings": {
    "workstation": {
      "agents_use": ["codex"]
    }
  }
}
```

A host entry replaces the global list. Omit it to inherit; use an explicit
empty list to manage no agents on that host. An individual package, server,
marketplace, or plugin can target specific adapters with its own `agents`
list; omitting that list uses the resource's enabled targets.

## Declare Desired State

```json
{
  "agents": {
    "packages": [
      {
        "source": "vercel-labs/agent-skills",
        "skills": ["frontend-design"],
        "agents": ["claude-code", "codex"]
      }
    ],
    "mcp_servers": [
      {
        "name": "context7",
        "transport": "stdio",
        "command": "npx -y @upstash/context7-mcp",
        "env": ["CONTEXT7_API_KEY"]
      }
    ],
    "marketplaces": [
      {
        "name": "team",
        "source": "example/agent-marketplace"
      }
    ],
    "plugins": [
      {
        "name": "review-tools",
        "marketplace": "team"
      }
    ]
  }
}
```

- Skill packages accept Git, well-known HTTP catalog, `file://`, and local
  directory sources. `ref` pins a Git ref; omitting `skills` selects every
  discovered skill. See [Upgrading From CLI-Managed Skills](migrating-skills.md)
  for storage, legacy adoption, and drift details.
- MCP `stdio` servers require `command`; `http` and `sse` servers require
  `url`. `env` stores variable names, deployed as `${env:NAME}` references the
  agent resolves when it starts the server. `env_literal` stores non-secret
  literal values, and `headers` configures remote-server headers; a `${VAR}`
  reference in either is deployed the same way.
- A marketplace has a stable manifest `name` and the `source` accepted by the
  target agent CLI.
- A plugin uses exactly one installation source: a declared `marketplace`, or
  a direct `source` for agents that support direct-source plugins.

The exact field shapes are in [Schema Reference](schema-reference.md#agents).

## Scope Resources With Groups

Groups can activate agent resources alongside tools and dotfiles:

```json
{
  "groups": [
    {
      "name": "work",
      "skills": ["vercel-labs/agent-skills"],
      "mcp_servers": ["context7"],
      "marketplaces": ["team"],
      "plugins": ["review-tools"]
    }
  ]
}
```

The values reference package sources and resource names declared under
`agents`. The special values `@agents.packages`, `@agents.mcp_servers`,
`@agents.marketplaces`, and `@agents.plugins` expand to every corresponding
manifest entry.

## Sync And Import

`omni agents sync` is converge-only: it restores declared plugins, skills, and
MCP servers without claiming unmanaged resources. Marketplaces required by
plugins are added before their plugins; marketplaces already present on an
agent are not added again.

The broad sync-all flow used by `omni tools sync --all` and the TUI's `S`
action also claims unmanaged resources. Its dependency order is:

1. Import unmanaged plugins.
2. Restore plugins.
3. Import unmanaged skills.
4. Restore skills.
5. Adopt unmanaged MCP servers.
6. Restore MCP servers.

A failure in one feature is reported without preventing later features from
running. An unavailable adapter is reported or skipped according to the
feature operation; Omni does not edit an agent's configuration as a fallback.
Drift is left untouched until explicitly resolved with the managed or local
side. See [CLI Reference](cli.md#agents-commands) for import and resolve
commands.

## Dry Runs And Plugin Shadowing

Use `--dry-run` before a broad sync or feature sync to preview actions without
changing the manifest, package store, or agent configuration.

Plugins can provide skills and MCP servers themselves. Omni treats shadowing
as a per-agent, per-name relationship: a plugin-provided server on Claude Code
does not suppress an unshadowed Codex server with the same name. During a dry
run, Omni projects each agent's installed plugins plus plugins that would be
installed, then uses that projected state for skill import/restore and MCP
adoption/restore. The preview therefore does not propose duplicate installs
that an earlier planned plugin supplies.

## Feature Gates And Ignore Lists

`agents_disabled` disables all agent management for a settings scope.
`skills_disabled`, `mcp_disabled`, and `plugins_disabled` disable individual
features. Host overrides inherit when absent; an explicit value overrides the
global value. The master gate always wins.

Use `agents.ignore.skills`, `agents.ignore.mcp_servers`,
`agents.ignore.marketplaces`, and `agents.ignore.plugins` to keep known items
visible but outside normal sync. See [Configuration](configuration.md#host-settings)
for gate behavior and [CLI Reference](cli.md#agents-commands) for the exact
exit behavior of sync versus management commands.
