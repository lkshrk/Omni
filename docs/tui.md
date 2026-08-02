# TUI

Run the TUI with:

```sh
omni
omni ui
```

The TUI is the primary daily interface. It uses the same app behavior as the
CLI, so durable actions should have matching command-line surfaces.

Every tab moves things between the same three stores, and the help overlay
(`?`) repeats the model in one line: manifest = what you want, store = what
Omni holds, live = what agents and machines see. See
[Core Concepts](concepts.md#the-three-stores).

![Omni TUI demo](assets/omni-demo.gif)

## Dashboard

The dashboard answers "what needs attention?" first, split into a Health Check
section (state and actions) and a Data section (at-a-glance stats).

Health Check surfaces tool updates, tool sync issues, dotfile health, agent
upgrades, agent skill issues, service automation gaps, and doctor diagnostics —
each row carries the action needed to resolve it. When both need attention, the
**Agents** row appears before **Services**. Use `reconcile` from the dashboard
when you want Omni to repair the normal set of issues for the active host.

Data always shows exactly four rows — **Tools**, **Dotfiles**, **Agents**, then
**Services** — each with a stat breakdown in the middle column (for example
`12 installed · 3 available · 1 ignored` or `5 skills · 3 mcp · 4 plugins`)
and a right-aligned value. Data is stats-only; it does not duplicate the
actions already offered in Health Check. The Tool Sync row itself lives only
in Health Check, appearing when tools are out of sync with this host.

The dashboard does not offer tool ignore/unignore. Use the Tools tab (`x` and
the ignore scope picker) for tool ignore state.

The Agents row in Data shows the total managed count (or `disabled` when the
`agents_disabled` host setting turns off the master switch), with per-feature
skill/MCP/plugin details on selection. A matching Agents row appears in Health
Check when skills, MCP servers, or plugins are missing or unmanaged; its
action is "sync skills" when skill packages are missing, or "open agents"
otherwise. The attention count adds skills missing/unmanaged, MCP
missing/unmanaged, and plugin missing/unmanaged, deduplicated per item (an
item counts once even if missing across multiple target agents) and with
ignored items excluded. Agents data — skills, MCP rows, and plugin rows —
loads once at TUI launch, so this count reflects live state without needing
the Agents tab to be opened first.

An **Agent Updates** row in Health Check counts the skill packages and plugins
behind their source, with `U` as its action. Being behind a source is not being
out of sync — the package is installed and linked — so those items are excluded
from the Agents row's attention count and reported here instead. The Agents row
in Data appends the same count (`5 skills · 3 mcp · 4 plugins · 1 upgrade
available`). The Agent Updates row is omitted entirely when agents are disabled
for the host, where the muted Agents row already says so.

## Tools Tab

The Tools tab shows configured, installed, out-of-sync, ignored, and discovered
tools. Typical actions include:

- install missing configured tools
- upgrade outdated tools
- add discovered tools to config
- move tools between groups
- reinstall with the default provider
- ignore or remove tools
- refresh provider state
- migrate nvm-managed drift off system providers (`r` on drift rows)

Rows show the configured provider and can mark drift when observed ownership
differs from the configured provider.

Ignore picker scopes:

- **Tracked tools** — tool everywhere, per-group, or this host.
- **Discovered orphans** — this host only (`ignore.tools`), without creating a
  logical tool spec.

Ignored configured tools stay in the **Ignored** section after toggling
ignore/unignore instead of disappearing from the list.

Fallback-capable rows can show GitHub source labels:

| Label | Meaning |
| --- | --- |
| `gh` | Installed through a verified GitHub fallback. |
| `gh?` | A GitHub fallback exists but is unverified. |
| `gh!` | The fallback is unresolved, unsupported on this platform, or failed and needs editing. |

The `f fallback` row action opens the fallback editor for eligible configured
tools. The editor is a structured form for the GitHub repo, binary,
bin dir, asset pattern, install/check/uninstall/upgrade/version commands, and
release channel. Saving from the TUI writes fallback config only; it does not
install immediately. Run sync or install afterward to apply the saved recipe
when the native package manager cannot provide the tool. Native-installed rows
hide fallback labels and actions.

## Dots Tab

The Dots tab shows dot entries, sync health, conflict state, ignored paths, and
repo status. It supports:

- sync one entry or all entries
- adopt local content into the repo
- choose repo or local content for conflicts
- press Space on a file to peek without leaving the TUI; differing repo/local files open as a
  `repo -> local` unified diff with `repo source` and `local source` labels
- move entries between groups
- add and remove host variants
- manage ignored paths

Expanded dot entries keep tracked, untracked, and ignored child paths visible so
you can understand why an entry is healthy or noisy.

## Agents Tab

The Agents tab manages agent skills, MCP servers, and plugins. It opens on the
`all` chip, which flattens skills, MCP servers, and plugins into one row list
under a single shared cursor, using the same grouped layout and icons as the
Tools tab. The `skills`, `mcp`, and `plugin` chips filter the view down to a
single section.

Rows are per-(item x target agent): a skill package, MCP server, or plugin
that targets more than one agent CLI renders one row per agent, with the
agent name in the second column. This makes per-agent drift visible directly
in the row list instead of hiding it behind an expandable summary.

Rows group into sections by status, in this order:

- **Updates Available** — an installed row with a newer version upstream, or
  an installed skill package recorded as behind its source.
- **Out of Sync** — a configured row missing on its target agent, a drifted
  row another tool owns, or an unmanaged/lock-only row installed on an agent
  but not tracked in the manifest.
- **Installed** — installed and up to date.
- **Available** — skill search results (`/` search) not yet added.
- **Ignored** — items on the `agents.ignore` lists, dimmed and excluded from
  installed/out-of-sync accounting. Synthetic rows appear when an ignore-list
  name no longer matches a live item; they support unignore only.

Each row shows a sync-status icon, the item name, its group badges, the
target agent, and a version or date column (with an arrow, e.g. `1.0.0 →
1.2.0`, when an update is available). Rows shadowed by an installed plugin show
`via plugin` in the version column — the plugin already provides that skill or
MCP server on the target agent. Selecting a row expands it with an
item-level detail line (skill names, MCP transport/command, or plugin
marketplace/version).

Row actions match the Tools tab vocabulary:

| Key | Action |
| --- | --- |
| `u` | Upgrade. On a plugin row with an update available, upgrades that one plugin. On a skills row that is installed or missing, runs a skills-wide upgrade (`agents skills upgrade` per package, not per row). |
| `i` | Install a missing row (Out of Sync + missing) onto its target agent. |
| `g` | Move the item between groups. Not available on Available or orphan (unmanaged) rows. |
| `a` | On a managed skills row, choose which agents the package installs to. The checkbox is the selection and the right-hand column is the on-disk state, so an installed agent can be left unselected without the row contradicting itself. Selecting every agent saves as the manifest's "all enabled agents" form rather than freezing today's agent list. |
| `x` | Ignore or unignore the item. Requires a second `x` press to confirm. Available on every non-synthetic row, including Available search results. |
| `d` | Delete the item from the manifest. Requires a second `d` press to confirm; any other key cancels. Not available on orphan or Available rows. |
| `c` | Claim an orphan into the manifest (opens the group picker first). |
| `enter` | On a skills Available row: add a highlighted search result. |

A drifted row — a skill entry another tool owns, an MCP server whose live
registration differs from the manifest's transport/command/URL, or a plugin
installed from a marketplace other than the declared one — sits in Out of Sync
with `drifted` in the version column. There `u` and `l` change meaning: `u`
resolves with Omni's side and `l` with the local one, each armed by a second
press of the same key (`l` still switches chips on every other row). The
resolutions are the `omni agents skills|mcp|plugins resolve` verbs; see
[CLI](cli.md#agents-commands) for what each side does per capability.

A row can only sit in one section, and drift outranks an update for placement.
An item that is both drifted and behind its source therefore stays in Out of
Sync, reading `drifted · upgrade` in the version column with `upgrade
available` in its detail; the Agent Updates dashboard row still counts it, so
the pending upgrade is never hidden behind the drift.

The tab-wide actions run every feature in one pass: `S` imports plugins,
restores plugins, imports skills, restores skills, adopts MCP servers, then
restores MCP servers; `R` reloads the rows. The same projected per-agent plugin
state used by the CLI prevents dry-run from previewing duplicate plugin-provided
skill or MCP installs. The run's outcome goes to the status bar as a one-line
total; the rows themselves are the report, so nothing is stacked above the
table. A per-feature failure still gets its own `error:` line there, since it
names something no row can show.

A run that leaves drift behind ends on a Drift Detected popup naming the first
ten drifted items. That popup holds every keypress while it is up, which is
what lets it use the uppercase batch keys: `U` resolves every currently drifted
item with Omni's side, `L` with the local one, and `esc` dismisses it without
acting. Both are `omni agents resolve --use-managed|--use-local`; see
[CLI](cli.md#agents-commands). Resolving one row at a time with the per-row lowercase
`u`/`l` (above) still works exactly as before; the popup is only ever a
shortcut for doing it to all of them at once.

Legacy lock-only skill packages, unmanaged MCP servers, and unmanaged plugins
— installed on an agent but not tracked in the manifest — appear as orphans in
the Out of Sync section. Orphan rows support claim (`c`, or `i` for MCP/plugins)
before group, ignore, or delete apply. Claiming a plugin can offer to adopt its
undeclared marketplace as well.

Skills, MCP rows, and plugin rows all load once at TUI launch (not lazily on
first tab visit), so the Agents tab and the dashboard's Agents counts reflect
current state as soon as the TUI starts. That load never probes a skill
package's source — it renders the last recorded verdict. `R` (refresh all) is
what re-probes, so an outdated marker appears after an explicit refresh or a
sync, never as a side effect of drawing the table.

If a feature is disabled for this host (via the `agents_disabled`,
`skills_disabled`, `mcp_disabled`, or `plugins_disabled` host settings), its
rows are dropped from the `all` view and its filter chip is dimmed and
disabled.

## Groups Tab

The Groups tab manages reusable groups and host assignments. The current host is
listed first. Each host has a protected local host group plus any reusable groups
assigned to it.

Use groups to share a curated set of tools and dotfiles across machines without
duplicating every entry.

## Settings Tab

Settings covers machine-local preferences:

- concrete provider priority
- disabled providers
- dotfiles repo path
- dotfile sync enablement
- reminder and watch service setup
- cache reset

The Agents section has a master "Agent Skills" toggle row plus three
per-feature toggle rows beside it — Skills, MCP Servers, and Plugins. Enter
toggles each row the same way as the existing pattern.

Settings are intentionally compact. The selected row expands details and action
hints.

## Admin Terminal

Some package operations may require a password. The TUI does not leave you in a
hidden package-manager prompt. It opens an embedded Admin Terminal prompt that
shows the command and reason, then streams the privileged command output.

Bulk operations stay conservative: privileged rows can be queued for explicit
approval instead of silently blocking the interface.
