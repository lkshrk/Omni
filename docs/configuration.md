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
[spec/omni.settings.v8.schema.json](https://github.com/lkshrk/omni/blob/main/spec/omni.settings.v8.schema.json).

## Smallest Valid File

The smallest legal file is:

```json
{
  "version": 8
}
```

`bootstrap` normally creates the host and group scaffolding around that minimum
shape. `settings` is optional when every setting should use the default. The
`$schema` key is optional for Omni, but useful for editors and written
automatically by Omni config writes.

## Minimal Example

```json
{
  "$schema": "https://raw.githubusercontent.com/lkshrk/omni/main/spec/omni.settings.v8.schema.json",
  "version": 8,
  "settings": {
    "fallback_bin_dir": "~/.local/share/omni/fallback/bin",
    "provider_priority": ["brew", "apt", "dnf", "zypper", "pacman", "apk", "npm", "pip"]
  },
  "tools": {
    "ripgrep": { "providers": [{ "provider": "brew" }] },
    "typescript": { "providers": [{ "provider": "npm", "package": "typescript" }] },
    "black": { "providers": [{ "provider": "pip" }] }
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
| `settings` | Global defaults for providers, dotfiles, Git behavior, and imports. |
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
      "pip": "3d"
    },
    "provider_priority": ["brew", "apt", "dnf", "zypper", "pacman", "apk", "npm", "pip"],
    "dots_repo": "~/dotfiles",
    "dots_git": {
      "auto_commit": false,
      "auto_push": false
    },
    "providers": [
      { "name": "uv", "provider": "brew" }
    ]
  }
}
```

Common keys:

| Key | Description |
| --- | --- |
| `auto_import` | Add newly discovered installed tools during scoped plain sync. Defaults to `false`. |
| `update_quarantine` | Defer upgrades until the PM-reported latest-version availability date is older than this duration. Empty or omitted disables quarantine. |
| `provider_update_quarantine` | Duration overrides keyed by provider (`npm`, `pip`, `brew`). |
| `provider_priority` | Preferred provider order for search/bootstrap choices and high-confidence auto selection. |
| `dots_repo` | Local path to the Git-backed dotfiles repo. |
| `dots_git.auto_commit` | Commit dotfile repo changes after add/remove flows. |
| `dots_git.auto_push` | Push dotfile repo changes after add/remove flows. |
| `fallback_bin_dir` | Default directory for fallback-installed binaries. Omni warns if it is not on `PATH`; it does not edit shell files automatically. |
| `providers` | Bootstrap provider tools installed before dependent tools. |

Use CLI settings commands when possible:

```sh
omni settings show
omni settings set dots_repo ~/dotfiles
```

See [Providers](providers.md) for provider priority, candidate selection, and
concrete ownership.

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
      "provider_priority": ["apt", "brew", "npm", "pip"],
      "disabled_providers": ["pip"]
    }
  }
}
```

Host-specific fields include `provider_priority`, `dots_repo`, `dots_disabled`,
and `disabled_providers`. Global fields such as `auto_import`,
`update_quarantine`, `provider_update_quarantine`, and `dots_git` are not host
overrides.

## Tool Specs

```json
{
  "tools": {
    "node": {
      "providers": [{ "provider": "apt", "package": "nodejs" }]
    },
    "typescript": {
      "providers": [{ "provider": "npm", "package": "typescript" }]
    },
    "rg": {
      "providers": [{ "provider": "apt", "package": "ripgrep" }],
      "git": "https://github.com/BurntSushi/ripgrep",
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
      "providers": [{ "provider": "pip" }]
    }
  }
}
```

Fields:

| Field | Description |
| --- | --- |
| `providers` | Ordered concrete provider candidates for this logical tool. |
| `providers[].provider` | Concrete provider such as `brew`, `apt`, `npm`, or `pip`. |
| `providers[].package` | Package name when it differs from the logical name. |
| `providers[].bin` | Optional binary name when the command differs from the package. |
| `git` | Upstream git repository URL. Brew metadata refresh/import/install and install-from-search may populate GitHub URLs here for later fallback setup. |
| `quarantine` | Tool-specific update quarantine override. Use `2d`/`48h`, `0`, or `exempt`. |
| `taps` | Homebrew taps required before install. |
| `variants` | Alternate install candidates tried in order. |
| `hosts` | Host-specific install overrides. |
| `ignore` | Keep the spec but skip management. |
| `fallback` | GitHub fallback recipe used when no configured native provider can provide the package. |

Fallback fields:

| Field | Description |
| --- | --- |
| `source.type` | Currently `github`. |
| `source.owner`, `source.repo`, `source.url` | Source repository provenance. |
| `status` | `unresolved`, `unsupported`, `unverified`, `verified`, or `failed`. |
| `binary` | Expected command name after install. |
| `bin_dir` | Optional per-tool override for `settings.fallback_bin_dir`. |
| `release_channel` | Optional release channel metadata for future resolver/editor use. |
| `recipe` and `platforms` | Structured release asset metadata when available. |
| `recipe.release_id`, `recipe.tag_name`, `recipe.published_at` | Generated GitHub release provenance and update-detection metadata. Leave alone unless editing manually. |
| `recipe.asset_id`, `recipe.asset_name`, `recipe.asset_download_url` | Generated current-platform asset provenance and download metadata. Leave alone unless editing manually. |
| `commands.install` | Shell command used by fallback install. Required for usable fallbacks. |
| `commands.check` | Shell command used to verify install. Required unless status is `unresolved` or `unsupported`. |
| `commands.uninstall` | Optional shell command for fallback uninstall. If absent, uninstall is unavailable. |
| `commands.upgrade` | Optional shell command for fallback upgrade. If absent, Omni reuses `install`. |

`omni tools fallback <tool> --from-github owner/repo` writes a usable fallback
after resolving latest stable release metadata and a supported asset for the
current platform. If `--from-github` is omitted, Omni uses the tool's `git`
value when it is a GitHub URL. If the release exists but has no current-platform
asset, Omni saves an `unsupported` draft with source and release provenance for
later editing. Resolver/API failures leave the existing config unchanged. The
command is config-only; install, sync, and upgrade decide later whether to use
the saved GitHub fallback recipe.
Accepted GitHub repo forms are `owner/repo`, `github.com/owner/repo`, `https://github.com/owner/repo`,
`https://github.com/owner/repo.git`, and `git@github.com:owner/repo.git`.
Browser URLs with extra paths, queries, or fragments are rejected.

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
