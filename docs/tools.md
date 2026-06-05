# Tools

Tools are logical names in `settings.json`. Omni resolves each logical tool to
a concrete package manager when it syncs the active host.

For the full provider model, see [Providers](providers.md).

## Add A Tool

Use `tools add` when you want one command to create the logical spec and assign
it to a group:

```sh
omni tools add ripgrep --provider system --group "$(hostname -s)"
omni tools add typescript --provider node --group dev
```

Use `tools set` when you only want to create or update the logical install
spec:

```sh
omni tools set ripgrep --provider system
omni tools set typescript --provider node --package typescript
omni tools set black --provider python
omni tools set ripgrep --quarantine exempt
```

Assign the logical tool to a group:

```sh
omni groups create dev
omni groups move-tool dev ripgrep
```

Decision rule:

| Need | Command |
| --- | --- |
| Add a new package and assign it to a group now | `omni tools add <package> --provider <provider> --group <group>` |
| Change package/provider metadata without touching groups | `omni tools set <name> --provider <provider>` |
| Add an already-defined logical tool to a group | `omni groups move-tool <group> <tool>` |

Install missing tools for the active host:

```sh
omni tools sync
```

## Search Before Adding

```sh
omni tools search rg
omni tools search typescript --provider node
```

Search uses provider registries where available. Results are best-effort because
each package manager exposes different metadata.

## Import Installed Tools

```sh
omni tools import
```

Import writes logical tool specs to config. It does not populate installed cache
state by itself; run refresh or sync afterward:

```sh
omni tools refresh
omni tools list
```

## Sync

```sh
omni tools sync
omni tools sync dev
omni tools sync --all
omni tools sync --prune --dry-run
```

`sync` installs missing configured tools. `--all` also claims discovered
installed tools into config before syncing missing tools. `--prune` removes local
installations that are no longer in config; preview with `--dry-run` first.

Plain `tools sync` does not run the full discovered-tool claim pass. For the
active host it can keep already-observed orphan tools in the machine group so
they are not lost. For scoped group sync, `settings.auto_import` controls
whether an import into the machine group runs after the group sync.

Use `tools install <tool>` for a single explicit install. Use `tools sync` when
you want the active host's configured desired state applied.

## Fallbacks

Fallbacks let a configured `system` tool install from GitHub when the current
system package manager cannot provide it. They are not a general package search
path and they are only used for logical tools that already exist in config.

Save a GitHub fallback source explicitly:

```sh
omni tools fallback rg --from-github BurntSushi/ripgrep
```

This writes `settings.json` only. It does not install the tool immediately.
Later `tools install <tool>` and `tools sync` still try the native system
manager first. They use the saved fallback only when Omni has explicit cached
evidence that the concrete manager, such as `apt` or `dnf`, cannot provide the
configured package.

Use `f fallback` in the TUI to edit the materialized recipe after choosing a
GitHub source. The TUI editor exposes the repo, binary, bin dir, asset pattern,
install/check/uninstall/upgrade/version commands, and release channel directly.
Saving remains config-only.

Fallback states:

| State | Meaning |
| --- | --- |
| `unresolved` | Source is known, but no usable install/check recipe is saved yet. Sync skips it. |
| `unverified` | A usable recipe exists, but it has not passed install/check on this host. |
| `verified` | The fallback install/check succeeded. |
| `failed` | The fallback install or check failed. Normal sync does not retry it. |

`tools sync --dry-run` can show a planned fallback install when native package
availability is known to be missing. `tools sync --retry-failed` can rerun a
previously failed fallback recipe unchanged. If the native manager becomes able
to install the package again, native install remains the preferred path.

Fallback uninstall is available only when the recipe has an uninstall command.
Without one, Omni reports that uninstall is not available instead of guessing
how to remove files.

## Upgrade

```sh
omni tools upgrade ripgrep
omni tools upgrade --all
omni tools upgrade --all --force
```

Upgrade uses the concrete manager recorded in cache when available. That avoids
uninstalling with one manager after a different manager installed the package.
Fallback-installed `system(gh)` tools use the saved fallback upgrade command,
or the install command when no separate upgrade command exists, then run the
required check command again.
When `settings.update_quarantine` is set, upgrades are skipped until the package
manager's own metadata says the latest version is older than the configured
duration. Missing PM date metadata blocks the update; `--force` is the explicit
bypass for both single-tool and `--all` upgrades.

If ownership looks wrong after manual package-manager work, run:

```sh
omni tools refresh
```

## Provider Drift

Use `reinstall with default` behavior when a logical tool is installed through a
non-default manager inside the same ecosystem:

```sh
omni tools switch ripgrep --reinstall-default
omni tools switch black --from brew --to pip
omni tools consolidate python uv
omni tools consolidate node bun
```

Use `consolidate` when moving a whole ecosystem to one manager. Use `switch`
with `--from` and `--to` for a targeted provider change, or
`--reinstall-default` when the configured default is already correct.

## Ignore Tools

```sh
omni tools ignore python-library
omni tools unignore python-library
```

Ignored tools stay in config but do not participate in normal management. This
is useful for imported libraries or packages that should not be installed,
upgraded, or deleted by Omni.

## Host Overrides

Use a host override when one machine needs a different install spec:

```sh
omni tools set node --provider system --package nodejs --install-with apt --host
```

`--host` is a boolean flag; it writes an override for the current active host.
Without `--host`, `tools set` writes the default global logical tool spec.

## Inspect State

```sh
omni tools list
omni tools list ripgrep
omni tools refresh
```

`refresh` asks providers for installed and outdated state, then updates the
local cache. It does not change `settings.json`.
