# CLI Reference

Run:

```sh
omni <command> --help
```

for command-specific flags.

Use [Command Matrix](command-matrix.md) when you need host requirements,
mutability, dry-run support, and safer first steps.

## Global Flags

| Flag | Description |
| --- | --- |
| `--config <path>` | Override the settings file path. |
| `--cache-dir <path>` | Override the local cache directory. |
| `-y`, `--yes` | Assume yes for confirmation prompts. |

## Top-Level Commands

| Command | Description |
| --- | --- |
| `omni` | Launch the TUI. |
| `omni ui` | Launch the TUI explicitly. |
| `omni bootstrap` | Bootstrap Omni on this machine. |
| `omni reconcile` | Claim discovered tools, sync, upgrade, sync agent resources, repair dotfiles, and back up dotfile changes. |
| `omni doctor` | Run read-only health checks. |
| `omni tools` | Manage logical tool specs and package operations. |
| `omni dots` | Manage dotfile symlinks from a Git repo. |
| `omni trace` | Inspect the rotating command trace log. |
| `omni groups` | Manage reusable groups. |
| `omni hosts` | Manage host group assignments. |
| `omni settings` | Manage effective settings. |
| `omni completion` | Generate shell completions. |
| `omni help [command]` | Show help. |
| `omni --version` | Print the version without running a subcommand. |

`bootstrap`, `doctor`, `hosts`, `dots`, `ui`, `settings`, `--version`, `help`, and
`completion` can run before an active host is configured.

`omni doctor` includes an "Agent features" check covering skills, mcp, and
plugins. For each feature it reports enabled/disabled state, checks required
Git or agent binary reachability, and reports manifest counts. Git is checked
only when a configured skill source needs it. A feature
disabled for this host is reported as disabled rather than actively probed.

`omni doctor` also reports retired `agents` declarations still present in
`settings.json`. It reads the raw file, so it reports them even though every
other command refuses to load such a config.

`omni doctor --fix` repairs Omni-owned tool and dotfile state. For agents, it
can remove exact package-owned MCP/LSP duplicates from the canonical host
template; APM still owns package cleanup and runtime state.

`omni doctor --fix --dry-run` reports the same exact duplicate removals without
writing. The fixer preserves a template symlink and edits only a regular,
unambiguous block-style `dependencies.mcp` or `dependencies.lsp` item. It
refuses conflicts, multiple owners, flow-style sequences, anchors/aliases,
merged mappings, ambiguous comments or same-line content, and unsafe
symlink/source layouts. It never edits `~/.apm/apm.yml`, the lockfile, installed
package manifests, the APM cache, or client configuration. After a repair, run
`omni agents sync`.

Repair locks the canonical template before the global APM workspace and
rechecks the content hashes and file identities of every classification input
before replacement. Any changed input or unsupported exact-candidate layout
refuses the whole repair; it does not apply a partial removal.

When the `apm` executable is missing and agent features are enabled,
`omni doctor --fix` installs the exact APM build required by Omni through the
first available installer (`uv tool install`, `pipx install`, then
`pip3 install --user`). Until APM is installed, `omni agents sync` refuses to
run when the config declares agent packages, and reports how to install APM.

`omni doctor` requires the exact build `0.29.0`; another version or an
unparseable version fails the "APM version" check. `--fix` restores the required
build from the upstream `main` branch through the same installer preference.
See [APM Main Build](agents.md#apm-main-build) for provenance and the
upgrade procedure.

The "APM provenance" check reads the installer's own receipt, so a build of the
required version made from another commit is still reported. `--fix` force
reinstalls in that case; an install with no readable receipt is left alone.

`omni doctor` exits nonzero when any check fails, and `--fix` and `--dry-run`
do not suppress that: `--fix --dry-run` still runs the full diagnostic pass, so
a preview on a host with unrelated failing checks exits nonzero. A `--fix`
error is reported separately as `applying fixes: <error>` and takes precedence,
but the health report is always printed first either way. No flag combination
suppresses the failure exit code, so a CI step that only wants the report
should read `--format json` output and ignore the status.

## Bootstrap

`omni bootstrap` also has the compatibility alias `omni init`.

| Flag | Description |
| --- | --- |
| `--import` | Import installed tools during bootstrap. |
| `--no-import` | Skip import and leave installed tools unclaimed. |
| `--import-config <path>` | Import an existing settings file as part of bootstrap. |

Agent package state is owned by APM and is never imported by bootstrap.

## Tools Commands

| Command | Description |
| --- | --- |
| `omni tools list [tool]` | List tools and install status. |
| `omni tools set <name>` | Create or update a logical tool spec. |
| `omni tools fallback <tool>` | Save or edit a GitHub fallback source for a configured `system` tool. |
| `omni tools remove <name>` | Undeclare a logical tool and its group memberships. Installed packages stay. |
| `omni tools remove <tool> --purge` | Undeclare the tool and uninstall it from this machine. |
| `omni tools delete-spec <name>` | Older spelling of `omni tools remove`. |
| `omni tools add <package>` | Add a tool to config. |
| `omni tools install [tool]` | Install one missing tool. |
| `omni tools sync [group]` | Install missing tools from config. |
| `omni tools sync --all` | Claim and sync tools, then run the APM agent sync. |
| `omni tools sync --prune` | Remove local installations no longer in config. |
| `omni tools upgrade [tool]` | Upgrade one tool or use `--all`. |
| `omni tools import` | Import installed tools into config. |
| `omni tools search <query>` | Search provider registries. |
| `omni tools refresh` | Refresh installed and outdated cache state. |
| `omni tools reinstall <tool>` | Reinstall a tool, optionally moving it to a different provider. |
| `omni tools consolidate <ecosystem> <manager>` | Move tools to one manager. |
| `omni tools providers` | List available providers. |
| `omni tools ignore <name>` | Ignore a logical tool everywhere. |
| `omni tools unignore <name>` | Stop ignoring a logical tool. |
| `omni tools migrate-nvm [tool...]` | Move nvm-managed system-provider tools to the node manager. |
| `omni tools normalize --default-overrides` | Remove redundant default overrides. |
| `omni tools baseline` | Absorb currently installed system packages into the discovery baseline. |

Common tool flags:

| Flag | Commands | Description |
| --- | --- | --- |
| `--provider <provider>` | `add`, `set`, `install`, `list`, `search`, `sync`, `reinstall --reinstall-default` | Select or filter a provider. |
| `--from-github <owner/repo>` | `fallback` | Resolve and save a GitHub fallback recipe from an explicit repo. Omit it only when the tool config has a GitHub `git` URL. |
| `--install-with <manager>` | `add`, `set` | Pin a logical tool to a concrete manager. |
| `--quarantine <duration>` | `set` | Set a tool-level update quarantine duration, `0`, or `exempt`. |
| `--force` | `install`, `upgrade`, `reconcile` | For install, skip bootstrap and host assignment checks. For upgrade and reconcile, bypass update quarantine. |
| `--group <group>` | `add`, `install`, `sync`, `import`, `list` | Assign, target, or filter by group. |
| `--host` | `set` | Boolean flag; write the override for the current active host. It does not take a hostname value. |
| `--global` | `set` | Write the default logical install spec. |
| `--dry-run` | `sync`, `import`, `consolidate`, `normalize`, `heal-taps`, `baseline` | Preview supported changes. |
| `--prune` | `sync` | Remove local installations no longer in config. Cannot be combined with `sync --all`. |
| `--all` | `sync`, `upgrade`, `migrate-nvm` | Bulk mode. For sync, also claims discovered tools and runs the agent leg (see the `sync --all` section below). For migrate-nvm, migrates every nvm-managed system-provider tool. |
| `--force` | `upgrade`, `reconcile` | Bypass update quarantine for upgrades. |
| `--from <provider>`, `--to <provider>` | `reinstall` | Move one tool between providers. |
| `--reinstall-default` | `reinstall` | Reinstall one tool with its configured default provider. |

### sync --all

`omni tools sync --all` reconciles the whole machine in one pass, in this
order:

1. Claim discovered installed tools into config (into the machine group, or
   into `--group` when given).
2. Install configured tools that are missing locally.
3. Run the APM-backed agent sync.

The agent leg is a single APM operation. Use `omni agents sync` directly when
only agent state is needed.

The aggregate command runs the APM agent leg only when `~/.apm/apm.yml` exists.
APM owns agent errors and output; Omni reports the aggregate exit status.

## Agents Commands

Agent desired and runtime state are owned by APM. Omni dispatches the thin
wrappers below in the global APM workspace; APM owns manifests, locks,
resolution, security checks, marketplace metadata, and deployment. Omni does
not provide a native fallback.

| Command | Description |
| --- | --- |
| `omni agents sync [--frozen] [--dry-run] [--force-template]` | Materialize the host template, then dispatch APM install in the global workspace. |
| `omni agents migrate --host <name> [--snapshot <dir>] [--dry-run\|--write]` | Preview a host's migration as one apm.yml plus the replaced, retained, and already-managed lists, or publish wrappers and update the marked host template with `--write`. |
| `omni agents add <package>...` | Dispatch APM package install. |
| `omni agents remove <package>...` | Dispatch APM package removal. |
| `omni agents update` | Dispatch APM dependency update. |
| `omni agents search <query@marketplace>` | Dispatch APM marketplace search. |
| `omni agents audit` | Audit the global APM workspace. |
| `omni agents targets` | Show resolved APM targets. |
| `omni agents outdated` | Show outdated global APM dependencies. |
| `omni agents prune` | Remove unused APM dependencies. |
| `omni agents deps list|why` | Inspect global APM dependencies. |
| `omni agents marketplace ...` | List, browse, update, validate, add, or remove APM marketplaces. |

APM deploys one host-global MCP surface: every declared server reaches every
enabled target that supports user-global MCP configuration. MCP entries do not
accept an `agents` list. Cursor and OpenCode are workspace-only MCP targets and
are rejected from this surface. Hermes is supported as an explicit target and
is not selected by automatic target discovery. APM owns target deployment,
removal, drift reporting, repair, and lifecycle serialization.

Installed package manifests are authoritative for bundled MCP/LSP children.
Before any live-manifest write or APM command, sync classifies top-level
services as independent, exact package duplicates, conflicting definitions, or
ambiguous multi-owner children. Exact duplicates block with an
`omni doctor --fix` hint; conflicts and multiple owners require manual template
repair. If an uninstalled package has no local manifest evidence, package-only
first install is allowed, but combining it with standalone MCP/LSP declarations
blocks until ownership can be proven. Dry-run performs the same preflight.
The exact template bytes that pass preflight are materialized; a concurrent
template edit is rejected rather than substituted after validation.

Sync locks the canonical template before the global APM workspace and holds
both through APM completion. Do not run `apm` directly in parallel with
`omni agents sync`; external APM processes do not participate in Omni's lock.
See [Package-owned MCP and LSP](agents.md#package-owned-mcp-and-lsp), including
the exact-duplicate Doctor repair boundary.

`omni agents outdated` is the read-only update check used by the Agents view.
`omni agents update` applies available dependency updates through APM.

Common agents flags:

| Flag | Command | Use |
| --- | --- | --- |
| `--dry-run` | `sync` | Ask APM to print its plan without deploying or updating files. The template is never materialized. |
| `--dry-run` | `migrate` | Explicit alias for the default migration preview. Writes no wrappers or template. |
| `--frozen` | `sync` | Require `apm.yml` and `apm.lock.yaml` to match; no dependency resolution. |
| `--force-template` | `sync` | Overwrite the live manifest with the host template, adopting it or overriding reported divergence. |
| `--host` | `migrate` | Required. The host whose snapshot declarations join the live native state in the preview. |
| `--snapshot` | `migrate` | Snapshot directory. Defaults to the single `.omni-apm-migration-backup-*` directory next to the resolved config file; with no such directory the preview covers native state only. |
| `--write` | `migrate` | Publish verified local wrappers and atomically update only the migration-owned host template. |


## Trace Commands

| Command | Description |
| --- | --- |
| `omni trace list` | Show recent external commands Omni issued and the reason for each command. |

Common trace flags:

| Flag | Commands | Description |
| --- | --- | --- |
| `--limit <n>` | `list` | Limit the number of trace rows shown. |

## Dots Commands

| Command | Description |
| --- | --- |
| `omni dots status [name]` | Show symlink health and repo status. |
| `omni dots list [name]` | List managed dots entries. |
| `omni dots discover` | List untracked dotfile candidates. |
| `omni dots add <path>` | Add a config path to dots management. |
| `omni dots sync [name]` | Create or repair symlinks. |
| `omni dots remove <name>` | Undeclare a dots entry and drop its repo package, keeping real local files. |
| `omni dots remove <name> --purge` | Also delete the local targets. |
| `omni dots resolve <name>` | Resolve a conflict with `--use-repo` or `--use-local`. |
| `omni dots extract <parent> <subpath>` | Split a subdirectory into its own entry/group. |
| `omni dots ignore <name> [pattern]` | Ignore an entry or path pattern. |
| `omni dots unignore <name> [pattern]` | Include an entry or remove an ignore pattern. |
| `omni dots groups <name>` | Show or move a dots entry group assignment. |
| `omni dots groups <name> --move <group>` | Move a dots entry to one group. |
| `omni dots groups <name> --remove <group>` | Remove one or more group assignments. |
| `omni dots variant` | Manage host-specific package variants. |
| `omni dots pull` | Pull remote changes and resync links. |
| `omni dots commit` | Stage and commit dotfiles repo changes. |
| `omni dots push` | Stage, commit, and push dotfiles repo changes. |
| `omni dots history` | Show recent dotfile operation history. |
| `omni dots enable` | Enable dotfile sync for this host. |
| `omni dots disable` | Disable dotfile sync for this host. |
| `omni dots reminder check` | Check whether dotfiles need attention. |
| `omni dots reminder run` | Run one reminder check, optionally notifying. |
| `omni dots reminder install` | Install a periodic user reminder service. |
| `omni dots reminder uninstall` | Remove the reminder service. |
| `omni dots reminder status` | Show reminder service status. |
| `omni dots watch run` | Run the dotfile watcher in the foreground. |
| `omni dots watch install` | Install automatic watch sync service. |
| `omni dots watch uninstall` | Remove automatic watch sync service. |
| `omni dots watch status` | Show watch service status. |
| `omni dots services status` | Show reminder and watch service status. |

Common dot flags:

| Flag | Commands | Description |
| --- | --- | --- |
| `--adopt` | `dots add` | Copy the existing local path into the dots repo, then link it. |
| `--discovered` | `dots add` | Persist a discovered candidate without adopting or syncing files. |
| `--ignore <pattern>` | `dots add` | Add child ignore patterns while creating the entry. |
| `--move <group>` | `dots groups` | Replace the entry's assignment with one group. |
| `--remove <group>[,<group>]` | `dots groups` | Remove assignments from the entry. |
| `--use-repo`, `--use-local` | `dots resolve`, `dots sync` | Choose the source of truth for a conflict. On `dots sync` they force-resolve every conflict at once. |
| `--group <group>`, `--name <name>` | `dots extract` | Target group for the new entry, and an optional name override. |

## Groups Commands

| Command | Description |
| --- | --- |
| `omni groups` | List groups from `settings.json`. |
| `omni groups create <name>` | Create an empty group. |
| `omni groups rename <old> <new>` | Rename a group. |
| `omni groups delete <name>` | Delete a group. |
| `omni groups move-tool <group> <tool>` | Move a logical tool to a group. |
| `omni groups remove-tool <group> <tool>` | Remove a logical tool membership. |
| `omni groups ignore-tool <group> <tool>` | Ignore a tool in one group. |
| `omni groups unignore-tool <group> <tool>` | Stop ignoring a tool in one group. |

## Hosts Commands

| Command | Description |
| --- | --- |
| `omni hosts list` | List host group assignments. |
| `omni hosts ensure <hostname>` | Create a host entry. |
| `omni hosts set-groups <hostname> [group ...]` | Replace reusable groups for a host. |
| `omni hosts add-group <hostname> <group>` | Assign a reusable group. |
| `omni hosts remove-group <hostname> <group>` | Remove a reusable group. |
| `omni hosts copy <source-host> <target-host>` | Copy host-scoped config. |
| `omni hosts remove <hostname>` | Remove a host assignment. |

## Settings Commands

| Command | Description |
| --- | --- |
| `omni settings show [key]` | Show effective settings for this host. |
| `omni settings get <key>` | Show one effective setting. |
| `omni settings set <key> <value>` | Set an Omni setting. |
| `omni settings disable-provider <provider>` | Disable a provider on this host. |
| `omni settings enable-provider <provider>` | Enable a provider on this host. |
| `omni settings reset` | Reset global and current-host settings while preserving tools, groups, and hosts. |
| `omni settings reset-cache` | Clear and reinitialize the local tool cache. |

Common setting keys:

- `auto_import`
- `provider_priority`
- `dots_repo`
- `dots_disabled`
- `dots_git.auto_commit`
- `dots_git.auto_push`
- `disabled_providers`

## Host Template And Migration

`~/.config/omni/apm.yml` is the optional host template. `omni agents sync` and
`omni sync` copy it over `~/.apm/apm.yml` before running APM install, so the
manifest stays a dotfile-managed input.

Sync also registers the marketplaces the template declares as trailing
`# apm marketplace add` comments, skipping the ones already in
`~/.apm/marketplaces.json`. `--dry-run` reports the pending registrations
without running them. Unregistering is never automatic.

Sync never overwrites a live manifest it has not seen before or one that
changed outside Omni. Both cases print a warning and leave the live manifest
alone; `omni agents sync --force-template` adopts or overrides. Omni tracks the
adopted manifest's hash in `agents-template-state` under its state directory.

`omni agents migrate --host <name>` previews one apm.yml built from both the
snapshot committed in dotfiles, when one is given or found, and the live native
Claude and Codex state, followed by `# apm marketplace add` comment lines for
registrations apm.yml cannot express and `# reach:` lines for servers whose
deployment widens. Below the manifest it prints what the manifest replaces and
the operator must delete by hand, what it retains and why, and what APM already
manages. Preview writes nothing. After review, commit the manifest into the
host's template in dotfiles, delete the replaced native entries, and sync; a
second sync is a no-op. `--write` publishes verified local wrappers and
atomically updates the marked host template instead; it still runs no APM
command:

```sh
omni agents migrate --host workstation
omni agents migrate --host workstation --write
omni agents sync
```

See [Agents](agents.md) for the field-by-field mapping. `--config`,
`--cache-dir`, and `--state-dir` remain global path overrides; `--config`
determines where `migrate` looks for the default snapshot.

## Deprecated Spellings

Every renamed verb keeps its old spelling working with the same flags and the
same behaviour. The old name is hidden from `--help`, prints one note on
stderr, and is not the spelling documentation or the TUI uses.

| Old spelling | Canonical spelling | Note |
| --- | --- | --- |
| `omni agents restore` | `omni agents sync` | Same operation. |
| `omni tools delete <tool>` | `omni tools remove <tool> --purge` | Same operation: `delete` always purged. |
| `omni tools delete-spec <name>` | `omni tools remove <name>` | Same operation. Still visible in `--help`. |
| `omni dots delete <name>` | `omni dots remove <name>` | Same operation; `--keep-local` rides along and `--purge` is the new spelling of `--keep-local=false`. |
| `omni init` | `omni bootstrap` | Pre-existing compatibility alias. |
