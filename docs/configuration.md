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
[spec/omni.settings.v3.schema.json](https://github.com/lkshrk/omni/blob/main/spec/omni.settings.v3.schema.json).

## Smallest Valid File

The smallest legal file is:

```json
{
  "version": 3
}
```

`bootstrap` normally creates the host and group scaffolding around that minimum
shape. `settings` is optional when every setting should use the default. The
`$schema` key is optional for Omni, but useful for editors and written
automatically by Omni config writes.

## Minimal Example

```json
{
  "$schema": "https://raw.githubusercontent.com/lkshrk/omni/main/spec/omni.settings.v3.schema.json",
  "version": 3,
  "settings": {
    "fallback_bin_dir": "~/.local/share/omni/fallback/bin",
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
    "update_quarantine": "2d",
    "provider_update_quarantine": {
      "npm": "1d",
      "python": "3d"
    },
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
| `update_quarantine` | Defer upgrades until the PM-reported latest-version availability date is older than this duration. Empty or omitted disables quarantine. |
| `provider_update_quarantine` | Duration overrides keyed by logical provider (`node`) or concrete provider/manager (`npm`, `uv`, `pip3`). Concrete keys win. |
| `dots_repo` | Local path to the Git-backed dotfiles repo. |
| `dots_git.auto_commit` | Commit dotfile repo changes after add/remove flows. |
| `dots_git.auto_push` | Push dotfile repo changes after add/remove flows. |
| `fallback_bin_dir` | Default directory for fallback-installed binaries. Omni warns if it is not on `PATH`; it does not edit shell files automatically. |
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
`disabled_providers`. Global fields such as `auto_import`,
`update_quarantine`, `provider_update_quarantine`, and `dots_git` are not host
overrides.

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
    "rg": {
      "provider": "system",
      "package": "ripgrep",
      "fallback": {
        "source": {
          "type": "github",
          "owner": "BurntSushi",
          "repo": "ripgrep",
          "url": "https://github.com/BurntSushi/ripgrep"
        },
        "status": "unverified",
        "binary": "rg",
        "commands": {
          "install": "install rg",
          "check": "command -v rg",
          "uninstall": "rm -f ~/.local/share/omni/fallback/bin/rg"
        }
      }
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
| `quarantine` | Tool-specific update quarantine override. Use `2d`/`48h`, `0`, or `exempt`. |
| `taps` | Homebrew taps required before install. |
| `variants` | Alternate install candidates tried in order. |
| `hosts` | Host-specific install overrides. |
| `ignore` | Keep the spec but skip management. |
| `fallback` | GitHub fallback recipe for `system` tools when the current concrete system manager cannot provide the package. |

Fallback fields:

| Field | Description |
| --- | --- |
| `source.type` | Currently `github`. |
| `source.owner`, `source.repo`, `source.url` | Source repository provenance. |
| `status` | `unresolved`, `unverified`, `verified`, or `failed`. |
| `binary` | Expected command name after install. |
| `bin_dir` | Optional per-tool override for `settings.fallback_bin_dir`. |
| `release_channel` | Optional release channel metadata for future resolver/editor use. |
| `recipe` and `platforms` | Structured release asset metadata when available. |
| `commands.install` | Shell command used by fallback install. Required for usable fallbacks. |
| `commands.check` | Shell command used to verify install. Required unless status is `unresolved`. |
| `commands.uninstall` | Optional shell command for fallback uninstall. If absent, uninstall is unavailable. |
| `commands.upgrade` | Optional shell command for fallback upgrade. If absent, Omni reuses `install`. |

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
