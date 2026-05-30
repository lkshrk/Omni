# State And Files

Omni has two durable sources of truth and one disposable local cache.

| State | Default location | Durable | Purpose |
| --- | --- | --- | --- |
| Config | `~/.config/omni/settings.json` | yes | Tools, groups, hosts, settings, and dotfile declarations. |
| Dotfiles repo | configured by `settings.dots_repo` | yes | Git repo containing GNU Stow packages. |
| Cache | `~/.cache/omni/omni.db` | no | Observed installed tools, outdated state, privilege metadata, and local history. |

If those sources disagree, prefer `settings.json` and the dotfiles repo. The
cache can be rebuilt.

## Path Resolution

Omni resolves the config file in this order:

1. `OMNI_CONFIG`
2. `$XDG_CONFIG_HOME/omni/settings.json`
3. `$HOME/.config/omni/settings.json`

The CLI flag overrides the default path for one invocation:

```sh
omni --config /path/to/settings.json settings show
```

Omni resolves the cache directory in this order:

1. `OMNI_CACHE_DIR`
2. `$XDG_CACHE_HOME/omni`
3. `$HOME/.cache/omni`

The CLI flag overrides the default cache directory for one invocation:

```sh
omni --cache-dir /tmp/omni-cache doctor
```

## Environment Variables

| Variable | Effect |
| --- | --- |
| `OMNI_CONFIG` | Full path to `settings.json`. |
| `OMNI_CACHE_DIR` | Directory where `omni.db` is stored. |
| `OMNI_HOSTNAME` | Overrides host detection for config lookup and tests. |
| `OMNI_PROFILE` | Enables timing output. Use `stderr`, `stdout`, a file path, or a truthy value. |
| `NO_EMOJI` | Forces ASCII-only CLI/TUI output when set. |
| `XDG_CONFIG_HOME` | Base directory used when `OMNI_CONFIG` is unset. |
| `XDG_CACHE_HOME` | Base directory used when `OMNI_CACHE_DIR` is unset. |

Host names are stored as short hostnames. Set `OMNI_HOSTNAME` in tests,
containers, or CI when `hostname -s` is unstable:

```sh
OMNI_HOSTNAME=testhost omni --config settings.json hosts ensure testhost
```

`OMNI_PROFILE` values:

| Value | Behavior |
| --- | --- |
| unset, empty, `0`, `false`, `off`, `no` | disabled |
| `1`, `true`, `yes`, `on`, `stderr` | write timing lines to stderr |
| `stdout` | write timing lines to stdout |
| any other value | append timing lines to that file with mode `0600` |

Omni uses ASCII-only status glyphs when `TERM=dumb`, when the active locale is
not UTF-8, or when `NO_EMOJI` is set for terminals whose font misses common
symbols.

## Config Writes

Config writes are designed to avoid partial `settings.json` files:

- writes go through a temporary file in the target directory
- the temporary file is renamed into place after a successful write
- config directories are created when needed
- when `settings.json` is a symlink, Omni writes to the resolved target

On normal app startup, Omni also writes a best-effort backup next to the config:

```text
settings.json.bak
```

This backup is local safety state, not a portable sync mechanism.

## Settings Reset Scope

`omni settings reset` replaces the `settings` block with defaults. It preserves
the rest of `settings.json`.

| Preserved | Reset to default |
| --- | --- |
| logical tools | `settings.auto_import` |
| groups and host groups | ecosystem manager and priority settings |
| host assignments | current host ecosystem manager and priority settings |
| global ignore lists | `settings.dots_git` |
| top-level schema/version metadata | current host `dots_repo`, `dots_disabled`, and disabled providers |

Use `omni settings show` before and after the reset when you need to audit the
effective values.

## Cache Contents

The SQLite cache stores observed state that would be expensive or noisy to
derive on every render:

- installed tool rows
- installed versions
- outdated markers
- concrete manager attribution such as `brew`, `uv`, or `pip3`
- privilege requirements reported by package managers
- dotfile operation history
- bootstrap completion markers

Resetting the cache does not remove config or dotfiles:

```sh
omni settings reset-cache
omni tools refresh
```

Use this when a package manager was changed outside Omni and the TUI looks
stale.

## Back Out Or Uninstall

Omni has no background agent by default. To back out local Omni state:

| Goal | Command or action |
| --- | --- |
| Stop managing dotfiles on this host | `omni dots disable` |
| Remove reminder or watch services | `omni dots reminder uninstall` and `omni dots watch uninstall` |
| Clear derived local state | `omni settings reset-cache` |
| Remove Omni config | move or delete `settings.json` after saving any wanted entries |
| Remove dotfile repo state | inspect and remove the repo configured by `settings.dots_repo` |
| Remove the binary | uninstall through the same channel used to install Omni |

When disabling dots, inspect `omni dots status` first. If managed links point to
important repo content, choose whether local copies should be materialized before
removing or replacing links.

## Source-Of-Truth Rules

| Question | Source |
| --- | --- |
| Should this tool exist on this host? | `settings.json` host and group membership. |
| Which provider should install a logical tool? | `settings.json` tool spec plus host overrides. |
| Which concrete manager currently owns a tool? | Cache, refreshed from providers. |
| Should this dotfile path be managed? | `settings.json` dot entry and group membership. |
| What files should a dotfile contain? | Dotfiles repo Stow package. |
| Is a package actually installed right now? | Provider scan stored in cache. |

`settings.json` is portable policy. The cache is local evidence.

## Read-Only Diagnostics

Use `doctor` before repair work:

```sh
omni doctor
```

`doctor` uses a read-only initialization path. It can report broken config,
cache, provider, dotfile, and service state without first normalizing config or
repairing host entries.

## Isolated Runs

Use explicit config and cache paths for demos, tests, and local experiments:

```sh
mkdir -p /tmp/omni-demo
OMNI_HOSTNAME=demo \
  omni --config /tmp/omni-demo/settings.json \
       --cache-dir /tmp/omni-demo/cache \
       bootstrap --no-import
```

This keeps experimental state out of the real home directory.
