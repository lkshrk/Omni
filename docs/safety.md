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
omni agents onboard --apply --apply-plan <path>
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

## Agent onboarding

`omni agents onboard` is a read-only inventory. Its APM probes use a disposable
HOME/XDG environment, so preview does not initialize the user's APM state.

Apply is intentionally broad: it can change Omni config, the dots repo, native
agent files, `~/.apm/apm.yml`, the APM lockfile, marketplaces, MCP registrations,
and deployed target files. Persist and review the JSON plan first. Unsafe paths,
unresolved targets/secrets, and manifest conflicts block mutation.

Before dots/native materialization, an incomplete operation may be rolled back.
After materialization starts, recovery is forward-only with `status` and
`resume`; this avoids restoring a mixed ownership state. Confirmed cleanup
removes private migration backups, not the resulting APM state.

For a native item, `move-to-apm` stages the reviewed resource, then removes that
original native source before APM deployment. After this materialization starts,
recovery is forward-only.

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
