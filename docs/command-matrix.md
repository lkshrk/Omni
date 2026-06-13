# Command Matrix

This matrix is the operational view of the CLI. Use it to answer three
questions before running a command:

- Does it need an active host?
- Which state can it change?
- Is there a preview or safer first step?

For exact flags, run:

```sh
omni <command> --help
```

## Legend

| Mark | Meaning |
| --- | --- |
| Read-only | Inspects state without intentionally changing config, packages, dotfiles, or services. |
| Config | Can write `settings.json`. |
| Cache | Can write local `omni.db` state. |
| Packages | Can install, upgrade, uninstall, or otherwise call package managers. |
| Files | Can create, remove, replace, or relink local files. |
| Dot repo | Can modify the configured dotfiles Git repo. |
| Services | Can install, remove, or run native dotfile helper services. |

Host requirement means the command normally needs the current hostname to be
configured so Omni can expand host settings and groups. Commands that target an
explicit group with `--group`, or expose an explicit `--force` path, can bypass
the normal active-host gate.

## Top-Level Commands

| Command | Host required | State touched | Safer first step |
| --- | --- | --- | --- |
| `omni` | no | TUI-driven | `omni doctor` |
| `omni ui` | no | TUI-driven | `omni doctor` |
| `omni bootstrap` | no | Config, cache, optional packages/files/dot repo | `omni bootstrap --no-import` |
| `omni init` | no | Alias for `bootstrap` | `omni bootstrap --no-import` |
| `omni doctor` | no | Read-only | already read-only |
| `omni reconcile` | yes | Config, cache, packages, files, dot repo | `omni doctor` |
| `omni trace list` | no | Read-only | already read-only |
| `omni completion` | no | Read-only | already read-only |
| `omni help [command]` | no | Read-only | already read-only |
| `omni --version` | no | Read-only | already read-only |

`reconcile` is intentionally broad. Use `--skip-privileged` when package
manager work should report privileged operations instead of prompting for
credentials:

```sh
omni reconcile --skip-privileged
```

Its tool phase claims discovered installed tools into the current machine group,
then installs missing configured tools.

Bootstrap flags:

| Flag | Use |
| --- | --- |
| `--import` | Import installed tools during setup. |
| `--no-import` | Create/load config and host state without claiming installed tools. |
| `--import-config <path>` | Seed setup from an existing config file. |

## Tool Commands

| Command | Host required | State touched | Preview or safer first step |
| --- | --- | --- | --- |
| `omni tools list [tool]` | yes | Read-only | already read-only |
| `omni tools search <query>` | yes | Read-only | already read-only |
| `omni tools providers` | yes | Read-only | already read-only |
| `omni tools refresh` | yes | Cache | `omni tools list` |
| `omni tools set <name>` | yes | Config | `omni settings show` |
| `omni tools fallback <tool>` | yes | Config | `omni tools list <tool>` |
| `omni tools delete-spec <name>` | yes | Config | `omni tools list <name>` |
| `omni tools normalize --default-overrides` | yes | Config | `--dry-run` |
| `omni tools add <package>` | yes | Config | `omni tools search <package>` |
| `omni tools install [tool]` | yes, unless `--group`/`--force` | Packages, cache | `omni tools list [tool]` |
| `omni tools sync [group]` | yes | Packages, cache | `--dry-run` |
| `omni tools sync --prune` | yes | Packages, cache | `--dry-run` |
| `omni tools sync --all` | yes | Config, packages, cache | `--dry-run` |
| `omni tools upgrade [tool]` | yes | Packages, cache | `omni tools list [tool]`; `--force` bypasses update quarantine |
| `omni tools upgrade --all` | yes | Packages, cache | `omni tools list`; `--force` bypasses update quarantine |
| `omni tools delete <tool>` | yes | Config, packages, cache | `omni tools list <tool>` |
| `omni tools import` | yes | Config | `--dry-run` |
| `omni tools reinstall <tool>` | yes | Config, packages, cache | `omni tools list <tool>` |
| `omni tools consolidate <ecosystem> <manager>` | yes | Config, packages, cache | `--dry-run` |
| `omni tools ignore <name>` | yes | Config | `omni tools list <name>` |
| `omni tools unignore <name>` | yes | Config | `omni tools list <name>` |

Important flags:

| Flag | Command | Use |
| --- | --- | --- |
| `--dry-run` | `sync`, `import`, `consolidate`, `normalize` | Show planned changes without writing config or mutating packages where supported. |
| `--prune` | `sync` | Remove local installations no longer in config. Cannot be combined with `sync --all`. |
| `--all` | `sync` | Claim discovered installed tools into config, then install missing configured tools. |
| `--group` | `install`, `sync`, `import`, `list`, `add` | Target, filter, or assign a reusable group explicitly. |
| `--force` | `install`, `upgrade`, `reconcile` | For install, skip bootstrap and host assignment checks for an explicit install path. For upgrade and reconcile, bypass update quarantine. |
| `--allow-weak` | `install`, `sync` | Permit the best weak provider discovery match when no high-confidence match exists. |
| `--provider` | `add`, `set`, `install`, `list`, `search`, `sync`, `reinstall --reinstall-default` | Scope command behavior to one provider where supported. |
| `--from-github` | `fallback` | Resolve and save a GitHub fallback recipe from `owner/repo`. Optional when the tool config has a GitHub `git` URL. |
| `--install-with` | `add`, `set` | Pin one logical tool to a concrete manager. |
| `--host` | `set` | Boolean flag; write a host-specific tool override for the current host. |
| `--from`, `--to` | `reinstall` | Move one tool between concrete managers or providers. |
| `--reinstall-default` | `reinstall` | Reinstall a tool with its configured default provider. Cannot be combined with `--from`/`--to`. |

## Dotfile Commands

| Command | Host required | State touched | Preview or safer first step |
| --- | --- | --- | --- |
| `omni dots status [name]` | no | Read-only | already read-only |
| `omni dots list [name]` | no | Read-only | already read-only |
| `omni dots discover` | no | Read-only | already read-only |
| `omni dots history` | no | Read-only | already read-only |
| `omni dots sync [name]` | no | Files, cache | `--dry-run` |
| `omni dots add <path>` | no | Config, files, dot repo, cache | `omni dots discover` |
| `omni dots delete <name>` | no | Config, files, dot repo, cache | `omni dots status <name>` |
| `omni dots resolve <name>` | no | Files, dot repo, cache | `omni dots status <name>` |
| `omni dots extract <parent> <subpath>` | no | Config, files, dot repo, cache | `omni dots status <parent>` |
| `omni dots ignore <name> [pattern]` | no | Config | `omni dots status <name>` |
| `omni dots unignore <name> [pattern]` | no | Config | `omni dots list <name>` |
| `omni dots groups <name>` | no | Config | `omni dots list <name>` |
| `omni dots variant list <name>` | no | Read-only | already read-only |
| `omni dots variant add <name>` | no | Config, files when syncing | `omni dots variant list <name>` |
| `omni dots variant remove <name>` | no | Config, files when syncing | `omni dots variant list <name>` |
| `omni dots pull` | no | Dot repo, files, cache | `omni dots status` |
| `omni dots commit` | no | Dot repo | `git -C <dots_repo> status` |
| `omni dots push` | no | Dot repo, network | `omni dots status` |
| `omni dots enable` | no | Config, files | `omni dots status` |
| `omni dots disable` | no | Config, files, dot repo | `omni dots status` |
| `omni dots reminder check` | no | Read-only | already read-only |
| `omni dots reminder run` | no | Read-only, optional notification | `omni dots reminder check` |
| `omni dots reminder status` | no | Read-only | already read-only |
| `omni dots reminder install` | no | Services | `omni dots reminder status` |
| `omni dots reminder uninstall` | no | Services | `omni dots reminder status` |
| `omni dots watch run` | no | Files, cache | `omni dots status` |
| `omni dots watch status` | no | Read-only | already read-only |
| `omni dots watch install` | no | Services | `omni dots watch status` |
| `omni dots watch uninstall` | no | Services | `omni dots watch status` |
| `omni dots services status` | no | Read-only | already read-only |

Conflict resolution must be explicit:

```sh
omni dots resolve <name> --use-repo
omni dots resolve <name> --use-local
```

Use `--use-repo` when the dotfiles repo is correct. Use `--use-local` when the
current local files are correct. Force-resolve every conflict in one pass with
`omni dots sync --use-repo` or `--use-local` (per-entry `on_conflict` still
wins); in the TUI, `U`/`L` on the dots tab do the same.

Split a subdirectory into its own entry/group:

```sh
omni dots extract nvim lua/plugins --group work
```

The parent stops managing the subtree and it is adopted as a new entry in
`--group`.

Group assignment edits are also explicit:

```sh
omni dots groups nvim
omni dots groups nvim --move dev
omni dots groups nvim --remove old-host
```

## Group Commands

| Command | Host required | State touched | Safer first step |
| --- | --- | --- | --- |
| `omni groups` | yes | Read-only | already read-only |
| `omni groups create <name>` | yes | Config | `omni groups` |
| `omni groups rename <old> <new>` | yes | Config | `omni groups` |
| `omni groups delete <name>` | yes | Config | `omni groups` |
| `omni groups move-tool <group> <tool>` | yes | Config | `omni tools list <tool>` |
| `omni groups remove-tool <group> <tool>` | yes | Config | `omni groups` |
| `omni groups ignore-tool <group> <tool>` | yes | Config | `omni tools list <tool>` |
| `omni groups unignore-tool <group> <tool>` | yes | Config | `omni tools list <tool>` |

Deleting a group is a config operation, but it can have broad later effects
because future syncs use group membership to compute desired state.

## Host Commands

| Command | Host required | State touched | Safer first step |
| --- | --- | --- | --- |
| `omni hosts list` | no | Read-only | already read-only |
| `omni hosts ensure <hostname>` | no | Config | `omni hosts list` |
| `omni hosts set-groups <hostname> [group ...]` | no | Config | `omni hosts list` |
| `omni hosts add-group <hostname> <group>` | no | Config | `omni hosts list` |
| `omni hosts remove-group <hostname> <group>` | no | Config | `omni hosts list` |
| `omni hosts copy <source-host> <target-host>` | no | Config | `omni hosts list` |
| `omni hosts remove <hostname>` | no | Config | `omni hosts list` |

Host commands can run before an active host exists because they are the tools
used to create and repair host assignments.

## Settings Commands

| Command | Host required | State touched | Safer first step |
| --- | --- | --- | --- |
| `omni settings show [key]` | no | Read-only | already read-only |
| `omni settings get <key>` | no | Read-only | already read-only |
| `omni settings set <key> <value>` | no | Config | `omni settings show` |
| `omni settings disable-provider <provider>` | no | Config | `omni tools providers` |
| `omni settings enable-provider <provider>` | no | Config | `omni tools providers` |
| `omni settings reset` | no | Config | `omni settings show` |
| `omni settings reset-cache` | no | Cache | `omni tools refresh` |

`settings reset` preserves tools, groups, hosts, and ignore lists while resetting
global settings and the current host's settings to defaults.
`settings reset-cache` clears derived local state and reinitializes the cache.

## Automation Defaults

For scripts and CI:

```sh
omni --config "$PWD/settings.json" \
     --cache-dir "$PWD/.omni-cache" \
     --yes \
     doctor
```

Use `OMNI_HOSTNAME` when host detection is unstable:

```sh
OMNI_HOSTNAME=testhost omni --config settings.json hosts ensure testhost
```

Prefer read-only commands and dry-runs in CI unless the job owns the machine
state it is about to mutate.
