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
| `--import-skills` | Adopt legacy CLI-managed agent skill packages during setup. |
| `--no-import-skills` | Leave legacy CLI-managed agent skill packages alone. |

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
| `omni tools baseline` | yes | Host discovery baseline state | `--dry-run` |
| `omni tools add <package>` | yes | Config | `omni tools search <package>` |
| `omni tools install [tool]` | yes, unless `--group`/`--force` | Packages, cache | `omni tools list [tool]` |
| `omni tools sync [group]` | yes | Packages, cache | `--dry-run` |
| `omni tools sync --prune` | yes | Packages, cache | `--dry-run` |
| `omni tools sync --all` | yes | Config, packages, cache, agent skill/MCP/plugin state | `--dry-run` |
| `omni tools upgrade [tool]` | yes | Packages, cache | `omni tools list [tool]`; `--force` bypasses update quarantine |
| `omni tools upgrade --all` | yes | Packages, cache | `omni tools list`; `--force` bypasses update quarantine |
| `omni tools remove <name>` | yes | Config | `omni tools list <name>` |
| `omni tools remove <tool> --purge` | yes | Config, packages, cache | `omni tools list <tool>` |
| `omni tools import` | yes | Config | `--dry-run` |
| `omni tools reinstall <tool>` | yes | Config, packages, cache | `omni tools list <tool>` |
| `omni tools consolidate <ecosystem> <manager>` | yes | Config, packages, cache | `--dry-run` |
| `omni tools ignore <name>` | yes | Config | `omni tools list <name>` |
| `omni tools unignore <name>` | yes | Config | `omni tools list <name>` |
| `omni tools heal-taps` | yes | Config | `--dry-run` |
| `omni tools migrate-nvm [tool...]` | yes | Config | `omni doctor` |
| `omni tools migrate-nvm --all` | yes | Config | `omni doctor` |

Important flags:

| Flag | Command | Use |
| --- | --- | --- |
| `--dry-run` | `sync`, `import`, `consolidate`, `normalize`, `heal-taps`, `baseline` | Show planned changes without writing config or mutating packages where supported. |
| `--prune` | `sync` | Remove local installations no longer in config. Cannot be combined with `sync --all`. |
| `--all` | `sync`, `migrate-nvm` | For sync, claim discovered installed tools into config, install missing configured tools, then import unmanaged agent skills, MCP servers and plugins, and sync agent skills, MCP servers, and plugins. For migrate-nvm, migrate every nvm-managed system-provider tool. |
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
| `omni dots remove <name>` | no | Config, files, dot repo, cache | `omni dots status <name>` |
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

## Agent Commands

| Command | Host required | State touched | Safer first step |
| --- | --- | --- | --- |
| `omni agents add <source>` | no | Config, files, cache, network | `omni agents find <query>` |
| `omni agents find <query>` | no | Cache | already read-only apart from the catalog cache |
| `omni agents sync` | no | Config, files, cache, network | `--dry-run` |
| `omni agents resolve` | no | Config, agent files and registrations, cache, network | `--dry-run` |
| `omni agents skills sync` | no | Files, cache, network | `--dry-run` |
| `omni agents skills upgrade` | no | Files, cache, network | `--dry-run`, or `--check` to probe sources without refreshing |
| `omni agents skills import [<source>]` | no | Config, files | `--dry-run` |
| `omni agents skills status <source>[@skill]` | no | Local state (records the read) | already read-only |
| `omni agents skills resolve <source>[@skill]` | no | Files or config, cache, network | `--dry-run` |
| `omni agents skills remove <source>` | no | Config | `omni doctor` |
| `omni agents skills remove <source> --purge` | no | Config, files | `omni agents skills remove <source>` |
| `omni agents skills group <source> <group>...` | no | Config | `omni groups` |
| `omni agents mcp list` | no | Read-only | already read-only |
| `omni agents mcp add --name <name> --transport <transport>` | no | Config, agent registrations | `omni agents mcp list` |
| `omni agents mcp import [<name>]` | no | Config | `omni agents mcp list` |
| `omni agents mcp sync` | no | Agent registrations | `--dry-run` |
| `omni agents mcp remove <name>` | no | Config, agent registrations | `omni agents mcp list` |
| `omni agents mcp group <name> <group>...` | no | Config | `omni groups` |
| `omni agents mcp resolve <name>` | no | Agent config or manifest | `--dry-run` |
| `omni agents plugins list` | no | Read-only | already read-only |
| `omni agents plugins add --name <name> --marketplace <marketplace>` | no | Config, agent files, network | `omni agents plugins list` |
| `omni agents plugins import [<name>]` | no | Config | `omni agents plugins list` |
| `omni agents plugins sync` | no | Agent files, network | `--dry-run` |
| `omni agents plugins remove <name>` | no | Config, agent files | `omni agents plugins list` |
| `omni agents plugins group <name> <group>...` | no | Config | `omni groups` |
| `omni agents plugins resolve <name>` | no | Agent files or manifest, network | `--dry-run` |
| `omni agents plugins marketplace list` | no | Read-only | already read-only |
| `omni agents plugins marketplace add <name>` | no | Config, agent registrations, network | `omni agents plugins marketplace list` |
| `omni agents plugins marketplace remove <name>` | no | Config | `omni agents plugins marketplace list` |
| `omni agents plugins marketplace group <name> <group>...` | no | Config | `omni groups` |

`agents skills remove` undeclares a package and leaves the installed content
alone; `--purge` composes the two halves, deleting the target links and any
unreferenced shared content as well, which is how you fully undo an add. The
deprecated `agents skills uninstall` still does only the disk half.

`agents skills sync` and `agents skills upgrade` are the only commands that
write into an Agent Target's skills directory on their own. Neither adopts a
skill directory an older CLI installed; use `agents skills import` for that,
with a source argument to claim exactly one package.

`agents skills status` reads one package end to end — manifest intent, store
content, update state, lockfile attribution, and what every targeted agent
directory actually holds — and names the next step for each entry that is not
a managed link. `agents skills upgrade --check` probes every source and reports
which packages are behind without refreshing any of them.
`agents skills resolve --use-managed` is the one command that overwrites a
directory another tool owns, which is why it confirms first; its `--use-local`
side touches no files and only narrows the manifest.

`agents mcp resolve` and `agents plugins resolve` settle the same kind of
conflict for the other two capabilities and confirm on the same side:
`--use-managed` discards the live registration or the foreign plugin copy.
Their `--use-local` side writes only the manifest — unlike skills, an MCP
server definition and a plugin's marketplace are configuration Omni can adopt
outright (see [CLI](cli.md#agents-commands)).

`agents mcp add`, `agents plugins add`, and `agents plugins marketplace add`
are not declaration-only. Each records manifest intent *and* converges this
host immediately through the targeted agents' own CLIs, so running one on a
machine you meant to leave alone registers a server, installs a plugin, or adds
a marketplace there. `agents mcp remove` and `agents plugins remove` are
symmetric: the live side always goes with the manifest entry, which is why
neither takes a `--purge`. `agents plugins marketplace remove` is the one
exception — it drops the manifest entry only, because pulling a marketplace out
from under plugins an agent still installs from would break them.

`agents resolve` settles every drifted agent resource across all three
capabilities in one pass, so it is the broadest of the resolve verbs; prefer the
per-capability `resolve` when only one row is in question.

`agents sync` runs all three feature syncs in one pass and stays
converge-only for the same reason. `omni tools sync --all` is the command that
also claims: it imports unmanaged skill packages, MCP servers and plugins
before syncing, which is why its confirmation prompt names that scope.

## Group Commands

| Command | Host required | State touched | Safer first step |
| --- | --- | --- | --- |
| `omni groups` | yes | Read-only | already read-only |
| `omni groups create <name>` | yes | Config | `omni groups` |
| `omni groups rename <old> <new>` | yes | Config | `omni groups` |
| `omni groups delete <name>` | yes | Config | `omni groups` |
| `omni groups set-tool <tool> <group>...` | yes | Config | `omni tools list <tool>` |
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
| `omni settings lint` | no | Read-only | already read-only |
| `omni settings extract` | no | Config | `omni settings lint` |
| `omni settings migrate-host-overrides` | no | Config | `omni settings lint` |
| `omni settings reset` | no | Config | `omni settings show` |
| `omni settings reset-cache` | no | Cache | `omni tools refresh` |

`settings reset` preserves tools, groups, hosts, and ignore lists while resetting
global settings and the current host's settings to defaults.
`settings reset-cache` clears derived local state and reinitializes the cache.

`settings extract` rewrites layout, not values: it decomposes `settings.json`
into the canonical `settings.d` layout (`agents.json`, `tools.json`,
`groups.json`, `dots.json`) and removes the moved keys from `settings.json`.
`settings migrate-host-overrides` folds `tools.*.hosts` install overrides into
`providers[]`. Both edit config in place and neither offers a `--dry-run`, so
commit or back up `settings.json` first. `settings lint` is the read-only
hygiene check to run before either.

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
