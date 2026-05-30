# Schema Reference

This page explains the shape of `settings.json`. For narrative examples, use
[Configuration](configuration.md). For the machine-readable schema, use
[`spec/omni.settings.v1.schema.json`](https://github.com/lkshrk/omni/blob/main/spec/omni.settings.v1.schema.json).

## Root Object

| Key | Type | Required | Description |
| --- | --- | --- | --- |
| `$schema` | string | no | Editor schema URI written by Omni. |
| `version` | integer | yes | Settings format version. Current version is `1`. |
| `settings` | object | no | Global defaults. |
| `host_settings` | object | no | Per-host setting overrides. |
| `tools` | object | no | Logical tool specs keyed by logical name. |
| `hosts` | object | no | Host to reusable-group assignments. |
| `groups` | array | no | Reusable groups and special host groups. |
| `ignore` | object | no | Global ignored tool and dot names. |

## `settings`

| Key | Type | Description |
| --- | --- | --- |
| `auto_import` | boolean | Add newly discovered installed tools during scoped plain sync. Defaults to `false` when absent. |
| `ecosystems` | object | Manager choices for `system`, `node`, and `python`. |
| `dots_repo` | string | Local path to the dotfiles Git repo. |
| `dots_disabled` | boolean | Disable dotfile sync for this settings scope. |
| `dots_git` | object | Dotfiles repo commit/push behavior. |
| `disabled_providers` | array | Ecosystem providers disabled for this settings scope. |

### `settings.ecosystems`

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
      "ecosystems": {
        "node": { "manager": "pnpm" }
      }
    }
  }
}
```

Host settings can override `ecosystems`, `dots_repo`, `dots_disabled`, and
`disabled_providers`. They do not override `auto_import` or `dots_git`.

## `tools`

`tools` is keyed by logical tool name:

```json
{
  "tools": {
    "ripgrep": { "provider": "system" },
    "typescript": { "provider": "node", "package": "typescript" }
  }
}
```

| Field | Type | Description |
| --- | --- | --- |
| `provider` | string | Portable ecosystem provider or concrete provider. Prefer `system`, `node`, or `python`. |
| `package` | string | Provider package name. Defaults to the logical tool name. |
| `install_with` | string | Concrete manager override for this tool. |
| `options` | object | Provider-specific key-value options. |
| `taps` | array | Homebrew taps required before install. |
| `ignore` | boolean | Keep the tool in config but skip management. |
| `variants` | array | Alternate install candidates tried in order. |
| `hosts` | object | Host-specific install overrides. |

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
