# Safety Model

Omni manages package managers, symlinks, config files, and a dotfiles Git repo.
Know which commands only inspect state and which commands can mutate it.

## Read-Only Commands

These commands are safe first checks:

```sh
omni doctor
omni tools list
omni tools search <query>
omni dots status
omni dots list
omni dots history
omni settings show
omni hosts list
omni agents drift
omni agents adopt --host <name>
```

`doctor` is the best first command when a machine looks wrong because it reports
diagnostics before normal startup repair paths run.

## Mutating Commands

These commands can change package state, local files, the cache, config, or the
dotfiles repo:

```sh
omni reconcile
omni tools sync
omni tools sync --all
omni tools upgrade --all
omni tools remove <tool> --purge
omni tools consolidate <ecosystem> <manager>
omni dots sync
omni dots add <path>
omni dots remove <name>
omni dots resolve <name> --use-repo
omni dots resolve <name> --use-local
omni dots push
omni agents sync --force-template
omni agents ignore ...
omni agents unignore ...
omni settings reset
omni settings reset-cache
```

If update quarantine is enabled, `tools upgrade --all` skips quarantined updates
as a non-error. Use `--force` only when you intentionally want to bypass the
quarantine or missing PM-date block.

Use `--dry-run` where the command supports it:

```sh
omni tools sync --dry-run
omni tools sync --prune --dry-run
omni tools consolidate python uv --dry-run
omni dots sync --dry-run
```

## Agent manifest

`omni agents migrate` previews by default: it parses a snapshot, prints a
manifest, writes no file, and runs no APM command. `--write` publishes verified
local wrappers and atomically updates only the migration-owned host template;
it still does not write the live manifest or run APM.

The host template is the one place Omni overwrites APM state. Sync replaces
`~/.apm/apm.yml` wholesale with `~/.config/omni/apm.yml`, and the install that
follows can then add, remove, or redeploy agent files accordingly. Two guards
stand in front of that copy, and both are advisory warnings rather than
failures:

- A live manifest Omni has never applied a template over is left alone on the
  first sync.
- A live manifest that changed outside Omni since the last applied template is
  left alone and reported as diverged.

`--force-template` overrides both. Diff the template against the live manifest
before using it, because the overwrite discards direct `apm` edits. `--dry-run`
never materializes the template.

`omni agents drift` and the `agents-drift` doctor check only report native
artifacts APM does not own; neither removes anything, and the check never
fails a machine over an operator's deliberate install. `agents ignore` and
`agents unignore` write only `agents.ignored` in `settings.json`. Removing a
native artifact is a TUI-only action on the Agents view, guarded by a second
key press, and it runs the client's own uninstall command by identity rather
than deleting any path Omni derived.

## Reconcile And Discovery

`omni reconcile` is intentionally broad. Its tool phase calls the same shared
path as `omni tools sync --all`: it refreshes discovered installed tools, claims
them into the current machine group, then syncs missing configured tools.

`settings.auto_import` defaults to `false` and only affects scoped plain sync
paths. It is not a safety switch for `reconcile` or `tools sync --all`; those
commands are explicit broad repair/claim commands.

## Dotfile Backups

Before replacing local dotfiles, Omni writes safety copies under:

```text
~/dotfiles.bkp
```

Keep that directory until you have verified the synced files.

## Conflict Choices

Dotfile conflict resolution is explicit:

| Choice | Meaning |
| --- | --- |
| `--use-repo` | The repo version is correct. Replace the local target and relink it. |
| `--use-local` | The local target is correct. Copy it into the repo and relink it. |

Inspect first:

```sh
omni dots status <name>
```

Then choose one side:

```sh
omni dots resolve <name> --use-repo
omni dots resolve <name> --use-local
```

## Privileged Package Managers

System managers such as `apt`, `apk`, `dnf`, `pacman`, and `zypper` can require
elevated privileges. Homebrew casks can also require a password when they
install `.pkg` payloads.

In the TUI, Omni routes those operations through the embedded Admin Terminal so
the command and reason are visible before a password prompt can happen.

## Network Behavior

Omni does not send telemetry. Network activity comes from the commands you run
and the package managers or Git remotes they invoke:

- provider search, refresh, install, upgrade, and delete commands can contact
  package registries or OS package mirrors
- `dots pull` and `dots push` contact the configured Git remote
- APM agent add/sync/update/search and marketplace operations can contact
  configured package sources and marketplaces; APM owns validation, artifact
  handling, and network failures
- release downloads happen only through your chosen install channel
- the `$schema` URI in `settings.json` is editor metadata; Omni writes it but
  does not fetch it as part of normal config writes

## Cache Reset

The local SQLite cache is disposable:

```sh
omni settings reset-cache
omni tools refresh
```

This does not remove `settings.json` or the dotfiles repo. It only clears and
rebuilds observed tool state.

## Config Source Of Truth

The durable source of truth is:

- `settings.json`
- the dotfiles Git repo

The cache, rendered TUI rows, and provider scan results are derived state.
