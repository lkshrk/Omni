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

`omni doctor --fix` also repairs Omni's own skill store: artifacts an
interrupted operation left behind, links into a package that no longer exists,
canonical packages nothing references any more, and missing local install
metadata. The unreferenced-package cleanup judges every package against the
active config's manifest, so running `--fix` with a different `--config` than
the one that installed a package deletes that package's copy from the shared
store. Run it with the config that declares your packages, or preview it with
`--fix --dry-run` first.

When the `apm` executable is missing and agent features are enabled,
`omni doctor --fix` installs `apm-cli` through the first available installer
(`uv tool install`, `pipx install`, then `pip3 install --user`). Until APM is
installed, `omni agents sync|add|remove|update` refuse to migrate legacy agent
config so declarations never move into a manifest no installed tool can act on.

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
| `--import-skills` | Import existing agent skill packages during bootstrap. |
| `--no-import-skills` | Skip the agent skill package import. |

When a legacy skill CLI installed skill packages that the manifest does not
track, bootstrap prints `Import N existing agent skill package(s)?` and
defaults to yes. Accepting runs the same work as `omni agents skills import`:
the lockfile packages are merged into `agents.packages` and their legacy
CLI-managed directories are replaced with links into Omni's package store.
The prompt is skipped when skills are disabled for the host or when nothing is
unmanaged, and either flag answers it without asking.

Because adoption rewrites real directories, it never happens implicitly. When
stdin is not a terminal — a provisioning script, a CI step, `omni bootstrap <
/dev/null` — bootstrap prints how many packages it found and leaves them alone
instead of taking the prompt's default. Pass `--import-skills` (or the global
`--yes`) to adopt them unattended.

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
| `omni tools sync --all` | Claim and sync tools, then import and restore agent plugins, skills, and MCP servers in dependency order. |
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
3. Import unmanaged plugins.
4. Restore plugins. Missing marketplaces are installed before their plugins;
   marketplaces already present on an agent are not reinstalled.
5. Import unmanaged skill packages.
6. Restore skills.
7. Adopt unmanaged MCP servers.
8. Restore MCP servers.

Steps 3–8 keep the master and per-feature enablement gates: a disabled feature
warns and installs nothing, while a failure in one feature does not stop later
features. Plugin state is tracked per agent. Skills and MCP servers supplied by
an installed plugin are skipped only for that agent; an MCP server shadowed for
Claude can still be adopted for Codex. Dry-run includes plugins it would
install in that projected state, so it does not preview duplicate skill or MCP
installs from those plugins. Drift is reported and never resolved
automatically. Use `omni agents sync` for converge-only restore without the
import phases.

Exit code: `omni tools sync --all` exits nonzero when either leg reports a
failure — a tool that could not be installed or whose provider is
unavailable, a skill source that could not be acquired, an agent CLI that
errored. Both legs run to completion first, so a single failure never
short-circuits the rest, and a run that fails in both legs reports both. A
clean exit means every step succeeded; scripts can check it directly rather
than parsing the printed lines. Plain `omni tools sync` applies the same rule
to the tool leg alone.

## Agents Commands

Agent desired state lives in APM's user manifest at `~/.apm/apm.yml`.
`apm.lock.yaml`, package resolution, security checks, and harness deployment are
owned by APM; Omni is only a command front end.

| Command | Description |
| --- | --- |
| `omni agents sync [--frozen] [--dry-run]` | Run `apm install --global` against the user manifest. `--frozen` gives lockfile-only, CI-style installation. |
| `omni agents add <package>...` | Run APM's positional install form, updating the user manifest and installing the packages. |
| `omni agents remove <package>...` | Uninstall packages through APM. `uninstall` is an alias. |
| `omni agents update [package]... [--dry-run]` | Refresh all or selected locked dependencies through APM. |
| `omni agents search <query@marketplace>` | Search one registered APM marketplace. `find` is an alias. |

Common agents flags:

| Flag | Command | Use |
| --- | --- | --- |
| `--dry-run` | `sync`, `update` | Ask APM to print its plan without deploying or updating files. |
| `--frozen` | `sync` | Require `apm.yml` and `apm.lock.yaml` to match; no dependency resolution. |


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

## Deprecated Spellings

Every renamed verb keeps its old spelling working with the same flags and the
same behaviour. The old name is hidden from `--help`, prints one note on
stderr, and is not the spelling documentation or the TUI uses.

| Old spelling | Canonical spelling | Note |
| --- | --- | --- |
| `omni agents restore` | `omni agents sync` | Same operation. |
| `omni agents skills restore` | `omni agents skills sync` | Same operation. |
| `omni agents mcp restore` | `omni agents mcp sync` | Same operation. |
| `omni agents plugins restore` | `omni agents plugins sync` | Same operation. |
| `omni agents skills update` | `omni agents skills upgrade` | Same operation; `--check` and `--dry-run` ride along. |
| `omni agents skills uninstall <source>` | `omni agents skills remove <source> --purge` | Not an alias: bare `uninstall` still removes only the installed side and leaves the manifest entry. The canonical spelling does both. |
| `omni tools delete <tool>` | `omni tools remove <tool> --purge` | Same operation: `delete` always purged. |
| `omni tools delete-spec <name>` | `omni tools remove <name>` | Same operation. Still visible in `--help`. |
| `omni dots delete <name>` | `omni dots remove <name>` | Same operation; `--keep-local` rides along and `--purge` is the new spelling of `--keep-local=false`. |
| `omni init` | `omni bootstrap` | Pre-existing compatibility alias. |
