# Providers

Providers are how Omni turns a logical tool into package-manager operations on
the current machine.

## Provider Types

| Type | Examples | Role |
| --- | --- | --- |
| Concrete provider | `brew`, `apt`, `apk`, `dnf`, `pacman`, `zypper`, `bun`, `pnpm`, `npm`, `uv`, `pip` | Actual package manager or registry client. |
| Bootstrap provider tool | `uv` installed by `brew` | A provider executable managed before dependent tools. |

Tool config stores concrete provider candidates:

```json
{
  "tools": {
    "ripgrep": { "providers": [{ "provider": "brew" }, { "provider": "apt" }] },
    "typescript": { "providers": [{ "provider": "npm", "package": "typescript" }] },
    "black": { "providers": [{ "provider": "pip" }] }
  }
}
```

The first provider is the normal install target. Additional entries are
high-confidence alternatives discovered from search/import metadata.

## Built-In Providers

| Provider family | Providers |
| --- | --- |
| System packages | `apt`, `apk`, `dnf`, `zypper`, `pacman`, `brew` |
| Node packages | `bun`, `pnpm`, `npm` |
| Python packages | `uv`, `pip` |

## Update Date Metadata

Update quarantine uses only package-manager or package-registry metadata. Omni
currently records latest-version availability dates from:

| Provider path | Metadata source |
| --- | --- |
| `bun`, `pnpm`, or `npm` | npm registry `time[version]` |
| `uv` or `pip` | PyPI release file `upload_time_iso_8601` |
| `pip` concrete provider | PyPI release file `upload_time_iso_8601` |

Other providers can still report outdated versions, but if quarantine is
enabled and no PM date is available, Omni treats that update as blocked until
you run the upgrade with `--force`.

## Provider Priority

Provider priority is used by bootstrap/search selection screens and by
high-confidence auto-selection. It is host-specific so one machine can prefer
`apt` while another prefers `brew`.

```json
{
  "settings": {
    "provider_priority": ["brew", "apt", "dnf", "zypper", "pacman", "apk", "npm", "pip"]
  }
}
```

Host override:

```json
{
  "host_settings": {
    "server": {
      "provider_priority": ["apt", "brew", "npm", "pip"]
    }
  }
}
```

Decision table:

| Need | Use |
| --- | --- |
| Prefer `apt` on one host | `host_settings.<host>.provider_priority` |
| One Python tool should use `pip` | `tools.<name>.providers[0].provider = "pip"` |
| A package name differs from the logical name | `providers[].package` |
| A tool can be installed several ways | multiple `providers[]` entries |

## Concrete Ownership

Omni records the concrete manager that actually owns an installed tool in the
cache. That value is shown as `InstalledWith` in internal state and appears in
TUI/provider displays.

This matters because uninstall and upgrade operations should use the same
manager that installed the package. For example, if a tool is configured for
`pip` but was discovered under `brew`, Omni should not blindly ask `pip` to
remove it.

Refresh ownership after changing package managers manually:

```sh
omni tools refresh
```

## Import Normalization

Import scans concrete managers, then writes concrete provider entries:

```sh
omni tools import
```

Typical normalization:

When a discovered package is not a CLI tool, provider support can mark it
ignored so it stays visible without participating in normal sync.

## Disabled Providers

Disable providers per host when a machine should never use them:

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

Disabled providers are skipped as install targets on that host.

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
