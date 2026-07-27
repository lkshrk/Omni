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
| `omni reconcile` | Claim discovered tools, sync, upgrade, sync agent resources, repair dotfiles, and commit dotfile changes. |
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
| `omni tools sync --all` | Claim discovered tools, install missing tools, then import unmanaged agent skills, MCP servers and plugins, and sync agent skills, MCP servers, and plugins. |
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
3. Import unmanaged agent skill packages into the manifest, adopting their
   installed directories.
4. Sync agent skills, MCP servers, and plugins.

Steps 3 and 4 run only when agent features are enabled for this host, and each
agent feature keeps its own gate: a disabled one warns and installs nothing.
Skill packages an installed plugin already provides are skipped rather than
claimed, and drift — an agent's skill entry another tool took over with
different content — is reported in the summary and never resolved
automatically. Use `--dry-run` to preview both phases, and `omni agents sync`
when you want the convergence without the claim.

Exit code: `omni tools sync --all` exits nonzero when either leg reports a
failure — a tool that could not be installed or whose provider is
unavailable, a skill source that could not be acquired, an agent CLI that
errored. Both legs run to completion first, so a single failure never
short-circuits the rest, and a run that fails in both legs reports both. A
clean exit means every step succeeded; scripts can check it directly rather
than parsing the printed lines. Plain `omni tools sync` applies the same rule
to the tool leg alone.

## Agents Commands

| Command | Description |
| --- | --- |
| `omni agents add <source>` | Add and install a skill package from Git, a well-known HTTP catalog, or a local directory. `#ref` and `@skill` selectors are supported. |
| `omni agents find <query>` | Search skills.sh. Results are cached for one hour; stale results are returned with a warning when refresh fails. |
| `omni agents sync` | Install the manifest's skills, MCP servers, and plugins in one pass. Converge only: it never claims unmanaged packages into the manifest. |
| `omni agents resolve` | Settle every drifted skill, MCP server, and plugin at once with one side: `--use-managed` or `--use-local`. A per-item refusal is reported and never blocks the rest. |
| `omni agents skills sync` | Install the manifest skill set onto this host through Omni's native skills engine. |
| `omni agents skills import [<source>]` | Explicitly import legacy `.skill-lock.json` entries into the manifest, adopting their installed directories. With a source, claim only that package. |
| `omni agents skills upgrade` | Refresh Omni's stored copies of the manifest skills from upstream, then relink. `--check` reports what is behind without refreshing. |
| `omni agents skills status <source>[@skill]` | Show one package's manifest intent, canonical store, update state, lockfile attribution, and per-agent entry states with their next steps. |
| `omni agents skills resolve <source>[@skill]` | Settle a drifted skill entry with an explicit side: `--use-managed` replaces the foreign content with Omni's link, `--use-local` keeps it and narrows the manifest. |
| `omni agents skills remove <source>` | Undeclare a package from the manifest. Installed links and store content stay. |
| `omni agents skills remove <source> --purge` | Undeclare it and remove the target links plus unreferenced store content. |
| `omni agents skills group <source> <group>...` | Set a skill package's full group membership. |
| `omni agents mcp list` | List managed and unmanaged MCP servers. |
| `omni agents mcp add` | Add an MCP server to the manifest and install it. |
| `omni agents mcp remove <name>` | Remove an MCP server from the manifest. |
| `omni agents mcp sync` | Install the manifest MCP servers onto this host. |
| `omni agents mcp import [<name>]` | List unmanaged MCP servers, or adopt one into the manifest by name. |
| `omni agents mcp resolve <name>` | Settle a drifted MCP server with an explicit side: `--use-managed` reinstalls the manifest definition, `--use-local` adopts the live one. |
| `omni agents plugins list` | List managed and unmanaged plugins, with installed version and, for outdated plugins, an arrow to the latest available version (e.g. `1.0.0 → 1.2.0`). |
| `omni agents plugins add` | Add a plugin to the manifest and install it. |
| `omni agents plugins remove <name>` | Remove a plugin from the manifest. |
| `omni agents plugins sync` | Install the manifest plugin set onto this host. |
| `omni agents plugins import [<name>]` | List unmanaged plugins, or adopt one into the manifest by name. |
| `omni agents plugins resolve <name>` | Settle a plugin installed from the wrong marketplace: `--use-managed` reinstalls from the declared one, `--use-local` repoints the manifest. |
| `omni agents plugins marketplace list` | List declared marketplaces. |
| `omni agents plugins marketplace add <name>` | Declare a marketplace and add it to targeted agent CLIs. |
| `omni agents plugins marketplace remove <name>` | Remove a marketplace from the manifest only. |

Common agents flags:

| Flag | Command | Use |
| --- | --- | --- |
| `--owner <owner>` | `find` | Limit catalog results to one GitHub owner. Filtered and unfiltered searches are cached separately. |
| `--dry-run` | `sync`, `skills sync`, `skills import`, `skills upgrade`, `skills resolve` | Print the planned actions, including packages skipped because a plugin already provides them, without changing anything. |
| `--check` | `skills upgrade` | Probe every package's source and report which are behind, without refreshing anything. Mutually exclusive with `--dry-run`, which prints planned actions offline instead. |
| `--use-managed` / `--use-local` | `skills resolve` | Choose which side of a drifted entry wins. Exactly one is required; `dots resolve` and `dots sync` accept `--use-managed` as an alias for `--use-repo`. |
| `--agent <id>` | `skills resolve` | Limit the resolution to one of the package's target agents. Repeatable; defaults to every agent the package is drifted on. |

A relative `source` (`./skills`, `../shared/skills`) resolves against the
directory holding `settings.json`, never the current working directory, so the
same manifest entry names the same package from any shell. Passing the
absolute path to `skills remove` or `skills group` matches a relative manifest
entry and vice versa.

Sync and upgrade never take over a skill directory an older CLI installed:
they warn and leave it alone. Run `omni agents skills import` to adopt those
installations into the manifest and the canonical package store, or
`omni agents skills import <source>` to claim just one. A source that is not a
candidate fails with the reason: it is absent from the lockfile, already in the
manifest, or provided by an installed plugin of the same name.

Omni tracks whether a package is behind its source. It records a cheap source
identity at install time — the commit a Git remote's ref points at, the content
hash of a local directory, or the digests in a well-known HTTP index — and
compares a later probe against it. Sources with no cheap identity (a Git
subpath, whose repository HEAD moves for commits that never touch the subpath)
and sources that cannot be reached report as unknown rather than guessing.
Checks are never run while rendering: `omni agents skills upgrade --check` and
the agents tab's refresh key probe on demand, `omni agents skills upgrade`
derives the answer from the content its refresh landed, and a sync
re-probes at most once every six hours. Outdated packages get the tools tab's
`↑` marker, count toward the dashboard's out-of-sync total, and are named by
`omni doctor` with the command that refreshes them.

When another tool owns an entry Omni expects to manage and the content differs,
sync reports drift and stops. `omni agents skills resolve <source>
--use-managed` stages the foreign content aside, installs Omni's link in its
place, and only discards the staged copy once the install succeeded — it is
destructive to local edits, so it asks for confirmation (`--yes` answers it).
`--use-local` keeps that content and narrows the manifest instead: naming a
skill (`<source>@skill`) drops it from the package's selectors, and omitting
one drops the selected agents from the package's target list. Omni refuses a
narrowing that would leave the package with no skills or no agents and points
at `omni agents skills remove` instead.

MCP servers and plugins drift too, and settle with the same two flags. An MCP
server is drifted when an agent's live registration differs from the manifest
on an identity field — transport, the stdio command, or the URL. Headers are
the documented exception: they derive from environment variables and secrets
whose rotation is routine, so sync keeps converging them from the manifest
without asking. Env is manifest-authoritative for the same reason: an agent
that reports env at all reports one merged map of resolved values, in which
`env` names and inline `env_literal` pairs look alike, and Codex reports none,
so neither side can be compared faithfully. Adoption is the one place that map
is interrogated: claiming a server compares every reported value against the
ambient environment, records the variables that match as `env` names and never
their values, and refuses the whole server when a value has no match, naming
the variables — not the values — in the warning. A plugin is drifted when an
agent has that plugin name installed from a marketplace other than the one the
manifest declares; a plugin merely behind its marketplace is *outdated*, not
drifted, and shows the update marker instead.

`omni agents mcp resolve <name> --use-managed` and `omni agents plugins resolve
<name> --use-managed` reinstall the manifest's definition through the agent's
own CLI, discarding what it currently holds — destructive, so both ask for
confirmation (`--yes` answers it). Both `--agent <id>` (repeatable) and
`--dry-run` work as they do for skills.

`--use-local` reads the same on all three surfaces — the local side wins — but
what that means differs by what the local side actually is. A skill package's
content is owned upstream, so Omni cannot adopt a hand-edited copy as desired
state and only narrows the manifest to stop expecting its own content there.
An MCP server and a plugin marketplace are pure configuration, so Omni can
record them: `agents mcp resolve --use-local` overwrites the manifest server's
identity fields with the live ones (leaving headers and env alone, since they
never drift), and `agents plugins resolve --use-local` repoints the manifest
entry at the installed marketplace. Both refuse rather than guess when the
agents disagree — different live definitions across agents need `--agent` to
pick one — and the plugin verb additionally refuses a marketplace that is not
declared, the same guard adoption applies.

`omni agents resolve --use-managed` / `--use-local` applies the side above to
every currently drifted resource across all three capabilities in one pass,
for when a sync leaves more drift than is worth settling one name at a time.
It takes no argument and no `--agent`: it resolves each drifted item on every
agent that item drifted on. `--dry-run` previews the whole set, and
`--use-managed` asks for confirmation once for the batch (`--yes` answers it).
Items that refuse — an undeclared plugin marketplace, agents disagreeing on an
MCP definition — are reported and skipped, and the exit code is nonzero if any
did, so the rest of the batch still lands.

Agent skills, MCP servers, and plugins are gated by per-host settings:
`agents_disabled` is the master switch, and `skills_disabled`, `mcp_disabled`,
and `plugins_disabled` gate each feature individually (see
[Configuration](configuration.md#host-settings)).

Omni detects installed agent CLIs from binary and config-dir signals. Supported
agents include Claude Code, Codex, Cursor, and Grok (`grok` on `PATH` with
`~/.grok`). Grok plugin and MCP sync flows use the Grok CLI adapters when Grok
is among the enabled agents for the host.

`agents sync`, `agents skills sync`, `agents mcp sync`, and `agents plugins
sync` special-case their own feature flag: if the feature is
disabled for this host, each command exits `0` and prints a warning instead of
erroring:

```text
warn: skills are disabled for this host, skipping sync
warn: mcp servers are disabled for this host, skipping sync
warn: plugins are disabled for this host, skipping sync
```

All other `agents` subcommands (`add`, `find`, `skills import`, `skills
upgrade`, `mcp add`/`remove`/`import`, `plugins add`/`remove`/`import`/
`marketplace *`) error out when their feature is disabled for this host,
whether disabled individually or via the `agents_disabled` master switch. The
three `sync` commands also still error when `agents_disabled` (the master
switch) is what's disabling them — the warn-and-exit-0 behavior applies only
to their own individual `*_disabled` flag.

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
