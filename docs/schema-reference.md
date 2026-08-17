# Schema Reference

This page explains the shape of `settings.json`. For narrative examples, use
[Configuration](configuration.md). For the machine-readable schema, use
[`spec/omni.settings.schema.json`](https://github.com/lkshrk/omni/blob/main/spec/omni.settings.schema.json).

## Root Object

| Key | Type | Required | Description |
| --- | --- | --- | --- |
| `$schema` | string | no | Editor schema URI written by Omni. |
| `version` | integer | yes | Settings format version. Current version is `22`. |
| `settings` | object | no | Global defaults. |
| `host_settings` | object | no | Per-host setting overrides. |
| `tools` | object | no | Logical tool specs keyed by logical name. |
| `hosts` | object | no | Host to reusable-group assignments. |
| `groups` | array | no | Reusable groups and special host groups. |
| `ignore` | object | no | Global ignored tool and dot names. |
| `agents` | object | no | Skill packages, MCP servers, marketplaces, plugins, and agent-specific ignores. |

## `settings`

| Key | Type | Description |
| --- | --- | --- |
| `auto_import` | boolean | Add newly discovered installed tools during scoped plain sync. Defaults to `false` when absent. |
| `update_quarantine` | string | Duration to defer upgrades after PM-reported latest-version availability, such as `2d` or `48h`. Omitted or empty means disabled. |
| `provider_update_quarantine` | object | Duration overrides keyed by logical provider or concrete provider/manager. |
| `provider_priority` | array | Preferred concrete provider order for provider selection flows. |
| `ecosystems` | object | Legacy manager choices migrated to provider lists on load. |
| `dots_repo` | string | Local path to the dotfiles Git repo. |
| `dots_disabled` | boolean | Disable dotfile sync for this settings scope. |
| `agents_disabled` | boolean | Master switch: disable the agent skills/mcp/plugins features for this settings scope. |
| `skills_disabled` | boolean | Disable the agent skills feature for this settings scope. |
| `mcp_disabled` | boolean | Disable the agent mcp feature for this settings scope. |
| `plugins_disabled` | boolean | Disable the agent plugins feature for this settings scope. |
| `dots_git` | object | Dotfiles repo commit/push behavior. |
| `disabled_providers` | array | Providers disabled for this settings scope. |
| `fallback_bin_dir` | string | Default directory for fallback-installed binaries. |
| `agents_use` | array | Agent IDs managed in this scope. Omit to manage every detected agent; an explicit empty list manages none. |

### `settings.ecosystems` (legacy)

`settings.ecosystems` remains in the schema so older configs can load and
migrate. Current config should use `provider_priority`, bootstrap
`settings.providers`, and tool-level `providers[]`. The v8→v9 migration expands
family `disabled_providers` (`system`/`node`/`python`) into concrete providers.

| Ecosystem | Fields | Accepted values |
| --- | --- | --- |
| `system` | `priority` | Any ordering of `apt`, `apk`, `dnf`, `zypper`, `pacman`, `brew`. |
| `node` | `manager` | `bun`, `pnpm`, `npm`. |
| `python` | `manager` | `uv`, `pip3`. |

## `host_settings`

`host_settings` is keyed by short hostname:

```json
{
  "host_settings": {
    "workstation": {
      "dots_repo": "~/src/dotfiles",
      "provider_priority": ["apt", "brew", "npm", "pip"]
    }
  }
}
```

Host settings can override `provider_priority`, `dots_repo`, `dots_disabled`,
`agents_disabled`, `skills_disabled`, `mcp_disabled`, `plugins_disabled`,
`agents_use`, and `disabled_providers`. They do not override `auto_import`,
`update_quarantine`, `provider_update_quarantine`, or `dots_git`.

A nil (absent) value for `agents_disabled`, `skills_disabled`, `mcp_disabled`,
or `plugins_disabled` on a host means "enabled by default" and inherits the
global setting. `agents_disabled` is the master switch: when true, it
disables skills, mcp, and plugins regardless of their individual flags.

An absent host `agents_use` inherits the global list. A present list replaces
it; an explicit empty list manages no agents on that host.

## `tools`

`tools` is keyed by logical tool name:

```json
{
  "tools": {
    "ripgrep": { "providers": [{ "provider": "brew" }], "git": "https://github.com/BurntSushi/ripgrep" },
    "typescript": { "providers": [{ "provider": "npm", "package": "typescript" }] }
  }
}
```

| Field | Type | Description |
| --- | --- | --- |
| `providers` | array | Ordered concrete provider candidates. |
| `providers[].provider` | string | Concrete provider such as `brew`, `apt`, `npm`, `pip`, or `script`. |
| `providers[].package` | string | Provider package name. Defaults to the logical tool name. |
| `providers[].bin` | string | Optional binary name when it differs from package/logical name. |
| `git` | string | Upstream git repository URL. Brew metadata and install-from-search can populate GitHub URLs here for later fallback setup. |
| `quarantine` | string | Tool-specific update quarantine override. Use a duration, `0`, or `exempt`. |
| `options` | object | Provider-specific key-value options. For `script`: `install` (required), `check` or `detect` (one required), optional `uninstall`, `upgrade`, `version`, and `latest`. `version` and `latest` each print one non-empty line; `latest` requires `version`. |
| `taps` | array | Homebrew taps required before install. |
| `ignore` | boolean | Keep the tool in config but skip management. |
| `variants` | array | Alternate install candidates tried in order. |
| `hosts` | object | Host-specific install overrides. |
| `fallback` | object | GitHub fallback recipe used when no configured native provider can provide the package. |

### `tools.<name>.fallback`

Fallbacks are saved on the logical tool. They are generated by
`omni tools fallback <tool> --from-github owner/repo`, or by
`omni tools fallback <tool>` when the tool has a GitHub `git` URL. Omni saves a
usable fallback when it can resolve latest stable GitHub release metadata and a
supported asset for the current platform. If the release exists but no asset
matches the current platform, Omni saves an `unsupported` draft with source and
release provenance so it can be edited later. Resolver/API failures leave the
existing config unchanged.
Accepted GitHub repo forms are `owner/repo`, `github.com/owner/repo`, `https://github.com/owner/repo`,
`https://github.com/owner/repo.git`, and `git@github.com:owner/repo.git`.
Browser URLs with extra paths, queries, or fragments are rejected.
Fallbacks are used only after the configured native provider is known
unavailable for the configured package.

| Field | Type | Description |
| --- | --- | --- |
| `source` | object | Source repository provenance. `source.type` is currently `github`. |
| `status` | string | `unresolved`, `unsupported`, `unverified`, `verified`, or `failed`. |
| `binary` | string | Expected command name after install. |
| `bin_dir` | string | Optional per-tool fallback binary directory override. |
| `release_channel` | string | Optional release channel metadata. |
| `recipe` | object | Structured recipe metadata, such as release asset pattern and checksum. |
| `recipe.checksum_asset_pattern` | string | Optional SHA-256 manifest asset pattern for verified, atomic binary installation. |
| `recipe.release_id` | string | Generated GitHub release id used for provenance and update detection. |
| `recipe.tag_name` | string | Generated GitHub release tag used for display and update detection. |
| `recipe.published_at` | string | Generated GitHub release publish timestamp. Fallback update detection requires this field and compares newer releases strictly by timestamp. |
| `recipe.asset_id` | string | Generated GitHub release asset id for the selected current-platform asset. |
| `recipe.asset_name` | string | Generated GitHub release asset name for the selected current-platform asset. |
| `recipe.asset_download_url` | string | Generated GitHub release asset download URL used by generated install and upgrade commands. |
| `platforms` | object | Optional OS/architecture-specific recipe overrides. |
| `commands.install` | string | Install command for usable fallbacks. |
| `commands.check` | string | Required verification command unless the fallback is `unresolved` or `unsupported`. |
| `commands.uninstall` | string | Optional uninstall command. If absent, uninstall is unavailable. |
| `commands.upgrade` | string | Optional upgrade command. If absent, install is reused for upgrade. |

The `recipe.release_id`, `recipe.tag_name`, `recipe.published_at`,
`recipe.asset_id`, `recipe.asset_name`, and `recipe.asset_download_url` fields
are generated provenance/update fields. Leave them alone unless you are
manually editing the fallback recipe and understand that incomplete metadata
disables GitHub fallback update detection for installed fallback rows.

## `groups`

Groups contain logical tool names and dot entries:

```json
{
  "name": "dev",
  "description": "Daily development tools",
  "tools": ["ripgrep", "typescript"],
  "dots": [{ "name": "nvim", "path": "~/.config/nvim" }]
}
```

Special host groups are protected machine-local groups:

```json
{ "name": "workstation", "special": "host" }
```

| Field | Type | Description |
| --- | --- | --- |
| `name` | string | Group identifier. |
| `special` | string | Reserved marker. `host` means protected host group. |
| `description` | string | Human-readable group description. |
| `tools` | array | Logical tool names. |
| `dots` | array | Dot entries. |
| `taps` | array | Legacy group-level Homebrew taps. Prefer tool-level taps. |
| `skills` | array | Skill package sources active for this group. `@agents.packages` expands to all declared packages. |
| `mcp_servers` | array | MCP server names active for this group. `@agents.mcp_servers` expands to all declared servers. |
| `marketplaces` | array | Marketplace names active for this group. `@agents.marketplaces` expands to all declared marketplaces. |
| `plugins` | array | Plugin names active for this group. `@agents.plugins` expands to all declared plugins. |

## Dot Entries

```json
{
  "name": "nvim",
  "path": "~/.config/nvim",
  "package": "nvim",
  "ignore": ["*.log", "cache/"],
  "hosts": {
    "workstation": { "package": "nvim@workstation" }
  }
}
```

| Field | Type | Description |
| --- | --- | --- |
| `name` | string | Logical dotfile identity and default Stow package. |
| `path` | string | Original filesystem path to manage. |
| `package` | string | Default Stow package. Defaults to `name`. |
| `hosts` | object | Host-specific Stow package variants. |
| `ignored` | boolean | Keep visible but skip sync/discovery management. |
| `ignore` | array | gitignore-style child path patterns. |

`ignored` and `ignore` are different fields. `ignored: true` skips the whole
entry while keeping it visible. `ignore: [...]` contains child path patterns
inside a managed entry.

## `hosts`

`hosts` maps a short hostname to reusable groups:

```json
{
  "hosts": {
    "workstation": ["dev", "work"]
  }
}
```

The matching protected host group is active implicitly and should not appear in
the reusable group list.

## `agents`

`agents` is the desired-state manifest for agent resources:

| Field | Type | Description |
| --- | --- | --- |
| `packages` | array | Skill package sources. The sole durable skill manifest. |
| `mcp_servers` | array | MCP server registrations. |
| `marketplaces` | array | Plugin marketplace sources. |
| `plugins` | array | Plugins installed from a marketplace or direct source. |
| `ignore` | object | Resource identities skipped during restore and sync. |
| `skills` | array | Legacy per-skill entries accepted only for migration into `packages`; never written back. |

### `agents.packages[]`

| Field | Type | Meaning |
| --- | --- | --- |
| `source` | string | Normalized Git, well-known HTTP, `file://`, or local directory locator. Required. |
| `ref` | string | Optional Git branch, tag, or commit. |
| `skills` | array | Selected skill names. Omitted means every discovered skill. |
| `agents` | array | Target Agent IDs. Omitted means the host's enabled targets. |

Source shorthand such as `owner/repo#main@review` is normalized into
`source`, `ref`, and `skills`. Source paths and locators remain portable;
resolved hashes, timestamps, and cache paths are never stored here.

### `agents.mcp_servers[]`

| Field | Type | Meaning |
| --- | --- | --- |
| `name` | string | Server identifier. Required. |
| `transport` | string | `stdio`, `http`, or `sse`. Required. |
| `command` | string | Launch command. Required for `stdio`. |
| `url` | string | Remote endpoint. Required for `http` and `sse`. |
| `env` | array | Environment variable names, deployed as `${env:NAME}` references the agent resolves at runtime; values are never stored or written to an agent config. |
| `env_literal` | object | Literal non-secret environment values. |
| `headers` | object | HTTP headers for remote transports. |
| `agents` | array | Target agent IDs. Omitted or empty means the host's enabled targets. |

### `agents.marketplaces[]`

| Field | Type | Meaning |
| --- | --- | --- |
| `name` | string | Marketplace identifier. Required. |
| `source` | string | Source accepted by the target agent CLI. Required. |
| `agents` | array | Target agent IDs. Omitted or empty means the host's enabled targets. |

### `agents.plugins[]`

| Field | Type | Meaning |
| --- | --- | --- |
| `name` | string | Plugin identifier. Required. |
| `marketplace` | string | Name of a declared marketplace. Mutually exclusive with `source`. |
| `source` | string | Direct plugin source accepted by the target agent CLI. Mutually exclusive with `marketplace`. |
| `agents` | array | Target agent IDs. Omitted or empty means the host's enabled targets. |

Each plugin must set exactly one of `marketplace` or `source`.

## `ignore`

```json
{
  "ignore": {
    "tools": ["python-library"],
    "dots": ["scratch"]
  }
}
```

Global ignore lists keep known-noisy entries visible but unmanaged.

### `agents.ignore`

```json
{
  "agents": {
    "ignore": {
      "skills": ["vercel-labs/agent-skills"],
      "mcp_servers": ["context7"],
      "plugins": ["my-plugin"],
      "marketplaces": ["noisy-marketplace"]
    }
  }
}
```

Lists agent-managed skills, MCP servers, plugins, and marketplaces skipped
during sync, mirroring the top-level `ignore` list for tools and dots.

## Config Version Migrations

Omni applies sequential migrations on load until `version` reaches the current
supported value. Notable steps since older tagged releases:

| Version | Effect |
| --- | --- |
| 6 | Legacy per-tool `provider`/`package` fields become `providers[]`. Hand-edited multi-provider arrays need `version` ≥ 6 before load or the v5→v6 step may rebuild `providers[]` from legacy fields only. |
| 12 | Agent feature flags and agents manifest shape updates. |
| 14 | Drops dot entries tracking agent config directories (for example `.claude`, `.codex`, `.agents/skills`). Discovery no longer surfaces those paths. |
| 15 | Adds optional `groups[].marketplaces` membership refs. |
| 16 | Adds `agents.ignore.marketplaces`. |
| 17 | Adds optional tool source, recipe, and `bin_dir` fields plus `$include` support. |
| 20 | Adds structured `agents.packages[].skills`, makes packages the sole durable skill manifest, and preserves legacy per-skill names during migration. |
| 21 | Allows `agents.plugins[]` to use exactly one of `marketplace` or a direct `source`. |

See [GitHub Releases](https://github.com/lkshrk/omni/releases) for release notes.
