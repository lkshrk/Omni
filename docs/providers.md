# Providers

Providers are how Omni turns a portable logical tool into package-manager
operations on the current machine.

## Provider Types

| Type | Examples | Role |
| --- | --- | --- |
| Ecosystem provider | `system`, `node`, `python` | Portable identity used in config. |
| Concrete provider | `brew`, `apt`, `bun`, `uv`, `pip3` | Actual package manager or registry client. |

Prefer ecosystem providers in `settings.json`:

```json
{
  "tools": {
    "ripgrep": { "provider": "system" },
    "typescript": { "provider": "node" },
    "black": { "provider": "python" }
  }
}
```

That keeps the config portable across macOS and Linux machines.

## Built-In Providers

| Ecosystem | Concrete managers | Default behavior |
| --- | --- | --- |
| `system` | `apt`, `apk`, `dnf`, `zypper`, `pacman`, `brew` | Uses the first available manager in system priority order. |
| `node` | `bun`, `pnpm`, `npm` | Uses `settings.ecosystems.node.manager`. |
| `python` | `uv`, `pip3` | Uses `settings.ecosystems.python.manager`. |

`pip` is accepted as a Python manager alias for compatibility, but persisted
settings use `pip3`.

## Update Date Metadata

Update quarantine uses only package-manager or package-registry metadata. Omni
currently records latest-version availability dates from:

| Provider path | Metadata source |
| --- | --- |
| `node` via `bun`, `pnpm`, or `npm` | npm registry `time[version]` |
| `python` via `uv`, `pip3`, or `pip` | PyPI release file `upload_time_iso_8601` |
| `pip` concrete provider | PyPI release file `upload_time_iso_8601` |

Other providers can still report outdated versions, but if quarantine is
enabled and no PM date is available, Omni treats that update as blocked until
you run the upgrade with `--force`.

## System Resolution

The built-in `system` provider checks concrete managers in this order:

```text
apt, apk, dnf, zypper, pacman, brew
```

This favors native Linux package managers on Linux and falls back to Homebrew
when no native manager is available.

You can document your preferred order in config:

```json
{
  "settings": {
    "ecosystems": {
      "system": {
        "priority": ["apt", "apk", "dnf", "zypper", "pacman", "brew"]
      }
    }
  }
}
```

## Manager Defaults

Use settings for ecosystem-wide choices:

```sh
omni settings set node.manager bun
omni settings set python.manager uv
```

Equivalent config:

```json
{
  "settings": {
    "ecosystems": {
      "node": { "manager": "bun" },
      "python": { "manager": "uv" }
    }
  }
}
```

Use host settings when one machine needs a different manager:

```json
{
  "host_settings": {
    "workstation": {
      "ecosystems": {
        "node": { "manager": "pnpm" }
      }
    }
  }
}
```

## `install_with`

`install_with` pins one logical tool to a concrete manager inside its ecosystem:

```json
{
  "tools": {
    "typescript": {
      "provider": "node",
      "package": "typescript",
      "install_with": "pnpm"
    }
  }
}
```

Use it when one package is unreliable or unavailable through the default
manager. Do not use it as a substitute for setting the ecosystem manager.

Decision table:

| Need | Use |
| --- | --- |
| All Python tools should use `uv` | `omni settings set python.manager uv` |
| One Python tool must use `pip3` | tool-level `install_with: "pip3"` |
| One host should use `pnpm` while others use `bun` | `host_settings.<host>.ecosystems.node.manager` |
| A Linux package name differs from the logical name | `package` |
| A tool can be installed several ways | `variants` |

## Concrete Ownership

Omni records the concrete manager that actually owns an installed tool in the
cache. That value is shown as `InstalledWith` in internal state and appears in
TUI/provider displays.

This matters because uninstall and upgrade operations should use the same
manager that installed the package. For example, if `black` is configured as
`python` but was discovered under `pip3`, Omni should not blindly ask `uv` to
remove it.

Refresh ownership after changing package managers manually:

```sh
omni tools refresh
```

## Import Normalization

Import scans concrete managers, then writes portable config where possible:

```sh
omni tools import
```

Typical normalization:

| Discovered from | Written provider |
| --- | --- |
| `brew`, `apt`, `apk`, `dnf`, `pacman`, `zypper` | `system` |
| `bun`, `pnpm`, `npm` | `node` |
| `uv`, `pip3`, `pip` | `python` |

When a discovered package is not a CLI tool, provider support can mark it
ignored so it stays visible without participating in normal sync.

## Disabled Providers

Disable ecosystem providers per host when a machine should never use them:

```sh
omni settings disable-provider python
```

Equivalent host setting:

```json
{
  "host_settings": {
    "server": {
      "disabled_providers": ["python"]
    }
  }
}
```

Concrete providers remain registered so Omni can still reason about installed
state and migrations. Disabled ecosystem providers are skipped as portable
install targets on that host.

## Privilege Metadata

Some providers can tell Omni that an operation needs elevated privileges before
running it. Omni caches that requirement with the tool row and uses it to route
interactive TUI operations through the Admin Terminal.

CLI scripts can avoid privileged package work in broad repair flows:

```sh
omni reconcile --skip-privileged
```

Use that when a noninteractive job should report privileged work instead of
blocking on a password prompt.

## Provider Troubleshooting

Inspect the effective provider set:

```sh
omni tools providers
omni settings show
```

Then check the concrete manager directly:

```sh
which brew
which apt
which bun
which uv
```

After installing or removing a manager, rebuild observed state:

```sh
omni tools refresh
```
