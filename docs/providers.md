# Providers

Providers are how Omni turns a logical tool into package-manager operations on
the current machine.

## Provider Types

| Type | Examples | Role |
| --- | --- | --- |
| Concrete provider | `brew`, `apt`, `apk`, `dnf`, `pacman`, `zypper`, `bun`, `pnpm`, `npm`, `uv`, `pip`, `cargo` | Actual package manager or registry client. |
| Script provider | `script` | User-authored shell install/check/uninstall commands. |
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

When a configured tool has no provider entries yet, explicit install and sync
commands may search enabled providers for package matches. High-confidence
matches are saved automatically and the highest-priority provider is installed.
Weak matches are ignored by default. Use `--allow-weak` only when you want Omni
to save and install the best weak match returned by provider priority:

```sh
omni tools install prettier --allow-weak
omni tools sync --allow-weak
```

## Built-In Providers

| Provider family | Providers |
| --- | --- |
| System packages | `apt`, `apk`, `dnf`, `zypper`, `pacman`, `brew` |
| Node packages | `bun`, `pnpm`, `npm` |
| Python packages | `uv`, `pip` |
| Rust binary crates | `cargo` |
| Script installs | `script` |

## Script Provider

Use `script` when no OS or ecosystem package manager carries a tool but an
upstream installer exists (curl/bash, vendor script, or fixed command sequence).
List it as a fallback candidate after native providers:

> **Security — the `script` provider executes arbitrary shell.** Every `install`,
> `check`, `detect`, `uninstall`, `upgrade`, `version`, and `latest` command is run
> verbatim through `sh -c`, and these commands come from your `settings.json`.
> Because `settings.json` is typically synced through your dotfiles repository,
> **anyone who can write to that repository can run arbitrary code on every machine
> that syncs it** — with `sudo` if a command escalates. Omni does not sandbox or
> confirm these commands. Treat your dotfiles/config repository as a trusted,
> access-controlled source: review changes to `script` recipes the way you would
> review a shell script you are about to run as yourself. Prefer a native or
> `github_release_asset` provider when one exists; reserve `script` for installers
> you already trust.

```json
{
  "tools": {
    "grok": {
      "providers": [
        { "provider": "brew", "package": "grok" },
        {
          "provider": "script",
          "options": {
            "install": "curl -fsSL https://x.ai/cli/install.sh | bash",
            "check": "command -v grok || test -x $HOME/.grok/bin/grok",
            "uninstall": "rm -f $HOME/.grok/bin/grok",
            "upgrade": "grok update 2>/dev/null || curl -fsSL https://x.ai/cli/install.sh | bash",
            "version": "grok --version | awk '{print $NF}'",
            "latest": "curl -fsSL https://x.ai/cli/latest-version"
          }
        }
      ]
    }
  }
}
```

| Option | Required | Role |
| --- | --- | --- |
| `install` | yes | Shell command Omni runs to install the tool. |
| `check` or `detect` | one required | `check` is a full shell probe (exit 0 = installed). `detect` is a binary name passed to `command -v`. |
| `uninstall` | no | Removal command. Omit when uninstall should be unavailable. |
| `upgrade` | no | Upgrade command. Falls back to `install` when omitted. |
| `version` | no | Installed-version command. Must print exactly one non-empty line. |
| `latest` | no | Latest-version command. Requires `version`, uses the same output format, and overrides source-based detection. |

Omni marks a command-backed script outdated when `latest` is a strictly newer
numeric release than `version`; a lowercase `v` prefix is ignored. Failed or
incomparable commands preserve the previous cached state.
Configured `github_release_asset` recipes use Omni's native GitHub installer,
even when their configured provider identity is `script`. They do not turn into
shell download commands. The native path supplies GitHub authentication,
retries, checksum verification, detailed download errors, and HTTPS-only
redirect protection.

For an unpinned recipe, Omni uses its shared GitHub latest-release lookup. A
recipe `tag_name` or `options.release_tag` pins the tool and disables automatic
release tracking.

Use `checksum_asset_pattern` to verify a release binary against a standard
SHA-256 manifest before atomically replacing the installed executable:

```json
{
  "provider": "script",
  "bin": "actionlint",
  "options": { "arch_map": "x86_64:amd64,aarch64:arm64" },
  "source": { "type": "github", "owner": "rhysd", "repo": "actionlint" },
  "recipe": {
    "type": "github_release_asset",
    "asset_pattern": "actionlint_{version}_linux_{arch}.tar.gz",
    "checksum_asset_pattern": "actionlint_{version}_checksums.txt"
  }
}
```

The native extractor selects the configured executable at any archive depth,
so existing `extract_dir` and `strip_components` recipes remain supported
without extracting unrelated files. The verified form cannot be combined with
`options.extract_dir`.

Routing behavior:

- Omni tries provider candidates in order and skips providers that are not
  available on the current machine (for example `brew` on Linux).
- Use `--provider script` with `tools install` or `tools delete` when you need
  to target the script owner explicitly on hosts where an earlier candidate is
  also available.

See [Recipes — Grok](recipes.md#grok-homebrew-on-macos-xai-script-on-linux) for
a full macOS/Linux example.

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

Disable concrete providers per host when a machine should never use them:

```sh
omni settings disable-provider pip
```

Equivalent host setting:

```json
{
  "host_settings": {
    "server": {
      "disabled_providers": ["uv", "pip"]
    }
  }
}
```

`disabled_providers` holds concrete provider names (e.g. `brew`, `apt`, `bun`,
`pnpm`, `npm`, `uv`, `pip`, `cargo`). Legacy family names (`system`/`node`/`python`) are
migrated to their concrete members automatically. Disabled providers are skipped
both as install targets and during discovery on that host. In the TUI, the
**Provider Priority** settings row edits the per-host order and toggles
providers on/off (space); the top-ranked available provider in each ecosystem
becomes that ecosystem's effective manager.

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
which cargo
```

After installing or removing a manager, rebuild observed state:

```sh
omni tools refresh
```
