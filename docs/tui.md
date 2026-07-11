# TUI

Run the TUI with:

```sh
omni
omni ui
```

The TUI is the primary daily interface. It uses the same app behavior as the
CLI, so durable actions should have matching command-line surfaces.

![Omni TUI demo](assets/omni-demo.gif)

## Dashboard

The dashboard answers "what needs attention?" first, split into a Health Check
section (state and actions) and a Data section (at-a-glance stats).

Health Check surfaces tool updates, tool sync issues, dotfile health, agent
skill issues, service automation gaps, and doctor diagnostics — each row
carries the action needed to resolve it. When both need attention, the
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
action is "restore skills" when skill packages are missing, or "open agents"
otherwise. The attention count adds skills missing/unmanaged, MCP
missing/unmanaged, and plugin missing/unmanaged, deduplicated per item (an
item counts once even if missing across multiple target agents) and with
ignored items excluded. Agents data — skills, MCP rows, and plugin rows —
loads once at TUI launch, so this count reflects live state without needing
the Agents tab to be opened first.

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

- **Updates Available** — an installed row with a newer version upstream.
- **Out of Sync** — either a configured row missing on its target agent, or
  an unmanaged/lock-only row installed on an agent but not tracked in the
  manifest.
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
| `u` | Update. On a plugin row with an update available, upgrades that one plugin. On a skills row that is installed or missing, runs a skills-wide update (skills update per package, not per row). |
| `i` | Install a missing row (Out of Sync + missing) onto its target agent. |
| `g` | Move the item between groups. Not available on Available or orphan (unmanaged) rows. |
| `x` | Ignore or unignore the item. Requires a second `x` press to confirm. Available on every non-synthetic row, including Available search results. |
| `d` | Delete the item from the manifest. Requires a second `d` press to confirm; any other key cancels. Not available on orphan or Available rows. |
| `c` | Claim an orphan into the manifest (opens the group picker first). |
| `enter` | On a skills Available row: add a highlighted search result. |

Lock-only skill packages, unmanaged MCP servers, and unmanaged plugins —
installed on an agent but not tracked in the manifest — appear as orphans in
the Out of Sync section. Orphan rows support claim (`c`, or `i` for MCP/plugins)
before group, ignore, or delete apply. Claiming a plugin can offer to adopt its
undeclared marketplace as well.

Skills, MCP rows, and plugin rows all load once at TUI launch (not lazily on
first tab visit), so the Agents tab and the dashboard's Agents counts reflect
current state as soon as the TUI starts.

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
