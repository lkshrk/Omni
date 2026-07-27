# State And Files

Omni has two durable sources of truth and one disposable local cache.

| State | Default location | Durable | Purpose |
| --- | --- | --- | --- |
| Config | `~/.config/omni/settings.json` | yes | Tools, groups, hosts, settings, and dotfile declarations. |
| Dotfiles repo | configured by `settings.dots_repo` | yes | Git repo containing GNU Stow packages. |
| Cache | `~/.cache/omni/omni.db` | no | Observed installed tools, outdated state, PM update-date metadata, privilege metadata, and local history. |
| Skill content | `$XDG_DATA_HOME/omni/skills`, else `~/.local/share/omni/skills` | no | Reconstructible canonical skill content linked into Agent Target directories. The `.lock` file inside it is an advisory flock serializing concurrent omni processes; it is empty, held only while a process runs, and safe to leave in place. |

If those sources disagree, prefer `settings.json` and the dotfiles repo. The
cache can be rebuilt.

For agent skills, `agents.packages` is desired state. Resolved fingerprints,
content hashes, catalog cache entries, the recorded source probe behind the
outdated verdict, and check timestamps stay machine-local. Deleting local skill
content or cache data does not change portable settings; the next sync
reacquires missing content, and the outdated verdict resets to unknown until
something reacquires the package or an explicit check probes its source.

The legacy skill lockfile is read only to surface explicit import/adopt
candidates. Omni does not mutate, delete, or dual-write it. It lives at
`$XDG_STATE_HOME/skills/.skill-lock.json` when `XDG_STATE_HOME` is set and at
`~/.agents/.skill-lock.json` otherwise; the `XDG_STATE_HOME` branch wins
outright, so inspect that path first on a machine that exports the variable.

Adopting a legacy CLI-managed installation — replacing those directories with
links into the canonical package store — happens only through
`omni agents skills import`. Sync, upgrade, and add never overwrite a
legacy-managed directory. One lossless exception: when a directory at a target
path is byte-identical to the manifest package Omni would install, reconcile
converges it into a managed link (reported as `adopted identical unmanaged
copy`). If a legacy directory holds content that differs from the package Omni
would install, the reconcile skips that target and reports it as drift rather
than overwriting it; only an explicit `agents add` of a new package fails with
`skill "<name>" already exists for target <id>`.
`omni agents skills resolve --use-managed` is the way through for content any
tool owns (it stages the directory aside, so a failed install rewinds it), and
`omni agents skills import` for a legacy install the lockfile still attributes.
`--use-local` writes only to the manifest, narrowing it so Omni stops expecting
its own content at that entry. Bootstrap offers the import when it detects
legacy installs, so a migrating machine can adopt them during setup.

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

Omni resolves the canonical skill store in this order:

1. `$XDG_DATA_HOME/omni/skills`
2. `$HOME/.local/share/omni/skills`

Omni resolves the legacy skill lockfile it imports from in this order:

1. `$XDG_STATE_HOME/skills/.skill-lock.json`
2. `$HOME/.agents/.skill-lock.json`

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
| `XDG_DATA_HOME` | Base directory for the canonical skill store (`$XDG_DATA_HOME/omni/skills`); defaults to `~/.local/share`. |
| `XDG_STATE_HOME` | When set, relocates the legacy skill lockfile Omni reads to `$XDG_STATE_HOME/skills/.skill-lock.json` instead of `~/.agents/.skill-lock.json`. |

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

Read-only commands never write `settings.json`. Reporting commands — `status`,
`upgrade --check`, every `--dry-run`, `doctor` — leave the file byte-identical
even when it is in an older format. Older formats are migrated in memory on
every load, and that migrated form is written out by the first command that
changes something. A config left untouched for several releases stays on its
old version on disk until you actually edit it, and reads keep working the
whole time.

Startup still repairs a host entry that names this machine but is not assigned
to it; that is a real change, so it does write.

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
| groups and host groups | provider priority settings |
| host assignments | current host provider priority, dotfile, and disabled-provider settings |
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
- concrete provider attribution such as `brew`, `uv`, or `pip`
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
