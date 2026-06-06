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

The dashboard answers "what needs attention?" first. It summarizes:

- tool health and update availability
- dotfile health and repo state
- doctor diagnostics
- optional reminder and watch services

Use `reconcile` from the dashboard when you want Omni to repair the normal set
of issues for the active host.

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

Rows show the configured provider and can mark drift when observed ownership
differs from the configured provider.

Fallback-capable rows can show GitHub source labels:

| Label | Meaning |
| --- | --- |
| `gh` | Installed through a verified GitHub fallback. |
| `gh?` | A GitHub fallback exists but is unverified. |
| `gh!` | The fallback is unresolved or failed and needs editing. |

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

## Groups Tab

The Groups tab manages reusable groups and host assignments. The current host is
listed first. Each host has a protected local host group plus any reusable groups
assigned to it.

Use groups to share a curated set of tools and dotfiles across machines without
duplicating every entry.

## Settings Tab

Settings covers machine-local preferences:

- ecosystem managers
- system manager priority
- disabled providers
- dotfiles repo path
- dotfile sync enablement
- reminder and watch service setup
- cache reset

Settings are intentionally compact. The selected row expands details and action
hints.

## Admin Terminal

Some package operations may require a password. The TUI does not leave you in a
hidden package-manager prompt. It opens an embedded Admin Terminal prompt that
shows the command and reason, then streams the privileged command output.

Bulk operations stay conservative: privileged rows can be queued for explicit
approval instead of silently blocking the interface.
