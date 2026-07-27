# Tools

Tools are logical names in `settings.json`. Omni resolves each logical tool to
one configured provider candidate when it syncs the active host.

For the full provider model, see [Providers](providers.md).

## Add A Tool

Use `tools add` when you want one command to create the logical spec and assign
it to a group:

```sh
omni tools add ripgrep --provider brew --group "$(hostname -s)"
omni tools add typescript --provider npm --group dev
```

Use `tools set` when you only want to create or update the logical install
spec:

```sh
omni tools set ripgrep --provider brew
omni tools set typescript --provider npm --package typescript
omni tools set black --provider pip
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
omni tools search typescript --provider npm
```

Search uses provider registries where available. Results are best-effort because
each provider exposes different metadata.

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
installed tools into config before syncing missing tools, and then — when agent
features are enabled for this host — imports unmanaged agent skill packages
into the manifest and syncs agent skills, MCP servers, and plugins.
`--prune` removes local installations that are no longer in config; preview
with `--dry-run` first.

Plain `tools sync` does not run the full discovered-tool claim pass. For the
active host it can keep already-observed orphan tools in the machine group so
they are not lost. For scoped group sync, `settings.auto_import` controls
whether an import into the machine group runs after the group sync.

Use `tools install <tool>` for a single explicit install. Use `tools sync` when
you want the active host's configured desired state applied.

If a configured tool has no provider entries, explicit install/sync can search
enabled providers and save high-confidence matches automatically. Weak matches
are skipped unless you pass `--allow-weak`, which saves and installs the best
weak provider match by provider priority.

## Fallbacks

Fallbacks let a configured tool install from GitHub when its native provider
cannot provide it. They are not a general package search path and they are only
used for logical tools that already exist in config.

Generate a GitHub fallback explicitly:

```sh
omni tools fallback rg --from-github BurntSushi/ripgrep
```

If the tool has `git: "https://github.com/owner/repo"` in `settings.json`, the
repo argument can be omitted:

```sh
omni tools fallback rg
```

Accepted GitHub repo forms are `owner/repo`, `github.com/owner/repo`, `https://github.com/owner/repo`,
`https://github.com/owner/repo.git`, and `git@github.com:owner/repo.git`.
Browser URLs with extra paths, queries, or fragments are rejected.

This resolves the latest stable GitHub release, selects an asset for the
current OS/architecture when possible, and writes `settings.json` only. It does
not install the tool immediately. Resolver/API failures leave the existing
config unchanged. If the release exists but has no supported current-platform
asset, Omni saves an `unsupported` draft with source and release metadata so the
recipe can be edited later.

Later `tools install <tool>` and `tools sync` still try the native system
manager first. They use the saved fallback only when Omni has explicit cached
evidence that the concrete manager, such as `apt` or `dnf`, cannot provide the
configured package. GitHub fallback is not a normal background search path and
does not make `gh` the preferred provider for native-owned rows.

Omni may populate `tools.<name>.git` from Brew metadata refresh/import/install
or from cached install-from-search metadata. That field is only upstream
metadata; it does not create or run a fallback until the explicit fallback
command is used.

Use `f fallback` in the TUI to edit the materialized recipe after choosing a
GitHub source. The TUI editor exposes the repo, binary, bin dir, asset pattern,
install/check/uninstall/upgrade/version commands, and release channel directly.
Saving remains config-only.

Fallback states:

| State | Meaning |
| --- | --- |
| `unresolved` | Source is known, but no usable install/check recipe is saved yet. Sync skips it. |
| `unsupported` | Source metadata exists, but no current-platform recipe is usable. Sync skips it. |
| `unverified` | A usable recipe exists, but it has not passed install/check on this host. |
| `verified` | The fallback install/check succeeded. |
| `failed` | The fallback install or check failed. Normal sync does not retry it. |

`tools sync --dry-run` can show a planned fallback install when native package
availability is known to be missing. `tools sync --retry-failed` can rerun a
previously failed fallback recipe unchanged. If the native manager becomes able
to install the package again, native install remains the preferred path.

Refresh/update detection applies only to fallback-installed `gh` rows
with complete saved GitHub release metadata. Omni marks the tool outdated only
when the latest GitHub release has a strictly newer `published_at` timestamp and
also has a supported asset for the current platform. Same, older, incomplete,
or unsupported latest releases do not mark the row outdated. Native-owned rows
are still handled by their native providers.

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
Fallback-installed `gh` tools marked outdated re-resolve the latest
GitHub release in memory before upgrade, then use the refreshed upgrade command
or install command and run the required check command again. Omni persists the
refreshed recipe only after the upgrade and check both succeed. If release
lookup, upgrade, or check fails, the old recipe stays in config, status becomes
`failed`, and the outdated marker remains so an explicit retry can use the same
state.
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
different provider than the configured default:

```sh
omni tools reinstall ripgrep --reinstall-default
omni tools reinstall black --from brew --to pip
```

Use `reinstall` with `--from` and `--to` for a targeted provider change, or
`--reinstall-default` when the configured default is already correct.

## Ignore Tools

```sh
omni tools ignore python-library
omni tools unignore python-library
```

Ignored tools stay in config but do not participate in normal management. This
is useful for imported libraries or packages that should not be installed,
upgraded, or deleted by Omni.

Ignore scopes:

| Scope | Config effect | Typical use |
| --- | --- | --- |
| Tool everywhere | `tools.<name>.ignore = true` | Stop managing a configured logical tool on every host. |
| Per group | group ignore membership | Skip one tool inside a reusable group. |
| This host | `ignore.tools` entry | Suppress a discovered orphan or noisy package on one machine without creating a logical spec. |

`omni tools ignore` applies the tool-wide flag and requires the logical tool to
already exist in config. For discovered-only packages, use the Tools tab ignore
picker (`x`) and choose **this host**, or add the name to `ignore.tools`
directly.

## System Inventory Baseline

System package managers report every image-baked package as manually installed,
so Omni records the inventory it sees on its first scan and treats that snapshot
as the baseline. Packages present at that point are never reported as discovered.

Anything a later `apt-get install` pulls in — Playwright's and Chrome's runtime
dependencies, for example — lands outside that baseline and fills the Tools tab
**Out of Sync** section. Re-snapshot the current inventory to absorb them:

```sh
omni tools baseline --dry-run
omni tools baseline
```

The command lists what it is absorbing and asks for confirmation, because the
absorbed packages stop being reported permanently on this host. It covers apt,
dnf, pacman, apk, and zypper only; configured and explicitly ignored tools keep
their own state, and packages installed after the re-snapshot still surface.

## NVM-Managed Provider Drift

When nvm owns the Node runtime, JS globals should use the configured node manager
(`pnpm`, `npm`, or `bun`), not a system provider (`brew`, `apt`, …). Omni's
doctor reports **nvm-managed binary (configured for system provider)** when the
active binary resolves through nvm but config still points at a system provider.

Fix from the CLI:

```sh
omni tools migrate-nvm --all
omni tools migrate-nvm node pnpm
omni doctor
```

`migrate-nvm` rewrites affected tool specs to the configured node manager or
removes the Node runtime from config when nvm owns it. The Tools tab offers the
same repair on drift rows (`r` / migrate confirmation). The dashboard Tool Sync
row can route bulk nvm fixes when doctor reports drift.

See [Recipes — Use Node Via nvm](recipes.md#use-node-via-nvm) and
[Troubleshooting](troubleshooting.md#node-or-pnpm-still-listed-under-a-system-provider-after-switching-to-nvm).

## Host Overrides

Use a host override when one machine needs a different install spec:

```sh
omni tools set node --provider apt --package nodejs --host
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
