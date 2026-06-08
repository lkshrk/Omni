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
| `omni reconcile` | Claim discovered tools, sync, upgrade, repair dotfiles, and commit dotfile changes. |
| `omni doctor` | Run read-only health checks. |
| `omni tools` | Manage logical tool specs and package operations. |
| `omni dots` | Manage dotfile symlinks from a Git repo. |
| `omni groups` | Manage reusable groups. |
| `omni hosts` | Manage host group assignments. |
| `omni settings` | Manage effective settings. |
| `omni completion` | Generate shell completions. |
| `omni help [command]` | Show help. |
| `omni --version` | Print the version without running a subcommand. |

`bootstrap`, `doctor`, `hosts`, `dots`, `ui`, `settings`, `--version`, `help`, and
`completion` can run before an active host is configured.

## Bootstrap

`omni bootstrap` also has the compatibility alias `omni init`.

| Flag | Description |
| --- | --- |
| `--import` | Import installed tools during bootstrap. |
| `--no-import` | Skip import and leave installed tools unclaimed. |
| `--import-config <path>` | Import an existing settings file as part of bootstrap. |

## Tools Commands

| Command | Description |
| --- | --- |
| `omni tools list [tool]` | List tools and install status. |
| `omni tools set <name>` | Create or update a logical tool spec. |
| `omni tools fallback <tool>` | Save or edit a GitHub fallback source for a configured `system` tool. |
| `omni tools delete-spec <name>` | Delete a logical tool spec and memberships. |
| `omni tools add <package>` | Add a tool to config. |
| `omni tools install [tool]` | Install one missing tool. |
| `omni tools sync [group]` | Install missing tools from config. |
| `omni tools sync --all` | Claim discovered tools and install missing tools. |
| `omni tools sync --prune` | Remove local installations no longer in config. |
| `omni tools upgrade [tool]` | Upgrade one tool or use `--all`. |
| `omni tools delete <tool>` | Uninstall and remove a tool. |
| `omni tools import` | Import installed tools into config. |
| `omni tools search <query>` | Search provider registries. |
| `omni tools refresh` | Refresh installed and outdated cache state. |
| `omni tools reinstall <tool>` | Reinstall a tool, optionally moving it to a different provider. |
| `omni tools consolidate <ecosystem> <manager>` | Move tools to one manager. |
| `omni tools providers` | List available providers. |
| `omni tools ignore <name>` | Ignore a logical tool everywhere. |
| `omni tools unignore <name>` | Stop ignoring a logical tool. |
| `omni tools normalize --default-overrides` | Remove redundant default overrides. |

Common tool flags:

| Flag | Commands | Description |
| --- | --- | --- |
| `--provider <provider>` | `add`, `set`, `install`, `list`, `search`, `sync`, `reinstall --reinstall-default` | Select or filter a provider. |
| `--from-github <owner/repo>` | `fallback` | Resolve and save a GitHub fallback recipe from an explicit repo. Omit it only when the tool config has a GitHub `git` URL. |
| `--install-with <manager>` | `add`, `set` | Pin a logical tool to a concrete manager. |
| `--quarantine <duration>` | `set` | Set a tool-level update quarantine duration, `0`, or `exempt`. |
| `--group <group>` | `add`, `install`, `sync`, `import`, `list` | Assign, target, or filter by group. |
| `--host` | `set` | Boolean flag; write the override for the current active host. It does not take a hostname value. |
| `--global` | `set` | Write the default logical install spec. |
| `--dry-run` | `sync`, `import`, `consolidate`, `normalize` | Preview supported changes. |
| `--prune` | `sync` | Remove local installations no longer in config. Cannot be combined with `sync --all`. |
| `--all` | `sync`, `upgrade` | Bulk mode for the command. For sync, also claims discovered tools. |
| `--force` | `upgrade`, `reconcile` | Bypass update quarantine for upgrades. |
| `--from <provider>`, `--to <provider>` | `reinstall` | Move one tool between providers. |
| `--reinstall-default` | `reinstall` | Reinstall one tool with its configured default provider. |

## Dots Commands

| Command | Description |
| --- | --- |
| `omni dots status [name]` | Show symlink health and repo status. |
| `omni dots list [name]` | List managed dots entries. |
| `omni dots discover` | List untracked dotfile candidates. |
| `omni dots add <path>` | Add a config path to dots management. |
| `omni dots sync [name]` | Create or repair symlinks. |
| `omni dots delete <name>` | Delete a dots entry from management. |
| `omni dots resolve <name>` | Resolve a conflict with `--use-repo` or `--use-local`. |
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
| `--use-repo`, `--use-local` | `dots resolve` | Choose the source of truth for a conflict. |

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
