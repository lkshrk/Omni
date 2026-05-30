# Configuration

Omni stores configuration in JSON:

```text
~/.config/omni/settings.json
```

Override the path with:

```sh
omni --config /path/to/settings.json
```

See [State And Files](state-and-files.md) for config path priority, cache path
priority, environment variables, backups, and disposable cache behavior.

The schema lives in
[spec/omni.settings.v1.schema.json](https://github.com/lkshrk/omni/blob/main/spec/omni.settings.v1.schema.json).

## Smallest Valid File

The smallest legal file is:

```json
{
  "version": 1
}
```

`bootstrap` normally creates the host and group scaffolding around that minimum
shape. `settings` is optional when every setting should use the default. The
`$schema` key is optional for Omni, but useful for editors and written
automatically by Omni config writes.

## Minimal Example

```json
{
  "$schema": "https://raw.githubusercontent.com/lkshrk/omni/main/spec/omni.settings.v1.schema.json",
  "version": 1,
  "settings": {
    "ecosystems": {
      "node": { "manager": "bun" },
      "python": { "manager": "uv" }
    }
  },
  "tools": {
    "ripgrep": { "provider": "system" },
    "typescript": { "provider": "node" },
    "black": { "provider": "python" }
  },
  "hosts": {
    "workstation": ["dev"]
  },
  "groups": [
    {
      "name": "dev",
      "tools": ["ripgrep", "typescript", "black"],
      "dots": [
        { "name": "nvim", "path": "~/.config/nvim" }
      ]
    },
    { "name": "workstation", "special": "host" }
  ]
}
```

## Top-Level Keys

| Key | Purpose |
| --- | --- |
| `$schema` | Editor schema URI. Omni injects it on writes. |
| `version` | Settings format version. |
| `settings` | Global defaults for ecosystems, dotfiles, Git behavior, and imports. |
| `host_settings` | Per-host overrides for selected settings. |
| `tools` | Logical tool specs keyed by logical tool name. |
| `groups` | Reusable and special host groups. |
| `hosts` | Host to reusable-group assignments. |
| `ignore` | Global tool and dotfile ignore lists. |

## Settings

```json
{
  "settings": {
    "auto_import": false,
    "dots_repo": "~/dotfiles",
    "dots_git": {
      "auto_commit": false,
      "auto_push": false
    },
    "ecosystems": {
      "system": { "priority": ["apt", "apk", "dnf", "zypper", "pacman", "brew"] },
      "node": { "manager": "bun" },
      "python": { "manager": "uv" }
    }
  }
}
```

Common keys:

| Key | Description |
| --- | --- |
| `auto_import` | Add newly discovered installed tools during scoped plain sync. Defaults to `false`. |
| `dots_repo` | Local path to the Git-backed dotfiles repo. |
| `dots_git.auto_commit` | Commit dotfile repo changes after add/remove flows. |
| `dots_git.auto_push` | Push dotfile repo changes after add/remove flows. |
| `ecosystems.node.manager` | `bun`, `pnpm`, or `npm`. |
| `ecosystems.python.manager` | `uv` or `pip3`. |
| `ecosystems.system.priority` | Concrete manager resolution order for `system`. |

Use CLI settings commands when possible:

```sh
omni settings show
omni settings get python.manager
omni settings set python.manager uv
omni settings set dots_repo ~/dotfiles
```

See [Providers](providers.md) for the difference between ecosystem defaults,
host manager overrides, and tool-level `install_with` pins.

`auto_import` does not control `omni tools sync --all` or `omni reconcile`.
Those broad commands explicitly claim discovered installed tools into the
machine group. Leave `auto_import` at `false` when you want discovered tools to
stay visible until you choose a claim path.

## Host Settings

Host settings override selected global settings for one machine:

```json
{
  "host_settings": {
    "workstation": {
      "dots_repo": "~/src/dotfiles",
      "ecosystems": {
        "node": { "manager": "pnpm" }
      },
      "disabled_providers": ["python"]
    }
  }
}
```

Host-specific fields include `ecosystems`, `dots_repo`, `dots_disabled`, and
`disabled_providers`. Global fields such as `auto_import` and `dots_git` are not
host overrides.

## Tool Specs

```json
{
  "tools": {
    "node": {
      "provider": "system",
      "package": "nodejs",
      "install_with": "apt"
    },
    "typescript": {
      "provider": "node",
      "package": "typescript"
    },
    "black": {
      "provider": "python"
    }
  }
}
```

Fields:

| Field | Description |
| --- | --- |
| `provider` | Portable provider such as `system`, `node`, or `python`. |
| `package` | Package name when it differs from the logical name. |
| `install_with` | Concrete manager override for this tool. |
| `taps` | Homebrew taps required before install. |
| `variants` | Alternate install candidates tried in order. |
| `hosts` | Host-specific install overrides. |
| `ignore` | Keep the spec but skip management. |

## Groups And Hosts

```json
{
  "hosts": {
    "workstation": ["dev"]
  },
  "groups": [
    {
      "name": "dev",
      "description": "Daily development tools",
      "tools": ["ripgrep", "typescript"],
      "dots": [{ "name": "nvim", "path": "~/.config/nvim" }]
    },
    { "name": "workstation", "special": "host" }
  ]
}
```

Manage group and host assignments through the CLI:

```sh
omni groups create dev
omni groups move-tool dev ripgrep
omni hosts add-group workstation dev
omni hosts copy workstation laptop
```

Dot entries use two similarly named fields:

| Field | Meaning |
| --- | --- |
| `ignored` | Skip the whole dot entry while keeping it visible. |
| `ignore` | Child path patterns to skip inside that entry. |

## Global Ignore

```json
{
  "ignore": {
    "tools": ["python-library"],
    "dots": ["scratch"]
  }
}
```

Ignore state keeps noisy or intentionally unmanaged items out of normal sync
and refresh flows.
