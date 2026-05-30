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

## Upgrade

```sh
omni tools upgrade ripgrep
omni tools upgrade --all
```

Upgrade uses the concrete manager recorded in cache when available. That avoids
uninstalling with one manager after a different manager installed the package.

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
