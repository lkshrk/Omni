# Glossary

## Active Host

The short hostname Omni uses to decide which host settings and reusable groups
are active. `OMNI_HOSTNAME` overrides detected hostnames.

See [Core Concepts](concepts.md#hosts) and [State And Files](state-and-files.md#environment-variables).

## Available

A provider can run on this machine. For example, `brew` is available when the
Homebrew binary exists and responds as expected.

## Cache

Local SQLite state under the cache directory. It records observations such as
installed versions, outdated markers, concrete manager ownership, privilege
requirements, and dotfile operation history. It is disposable.

See [State And Files](state-and-files.md#cache-contents).

## Concrete Manager

The real package manager that performs the operation: `brew`, `apt`, `bun`,
`uv`, `pip`, and similar names.

## Concrete Provider

The Omni provider implementation for a concrete manager.

## Desired State

The tools and dotfiles Omni expects for the active host after expanding
settings, host assignments, groups, tool specs, host overrides, and ignores.

## Discovered Tool

A package found installed by a provider scan. It may or may not be tracked in
`settings.json`.

## Dots Entry

A logical dotfile declaration in a group. It names the target path, Stow
package, optional host variants, and ignore patterns.

See [Dotfiles](dotfiles.md).

## Provider

A concrete package manager, registry client, or script installer in config,
such as `brew`, `apt`, `npm`, `pip`, or `script`.

See [Providers](providers.md).

## Script Provider

A provider candidate that runs user-authored shell commands from
`providers[].options` (`install`, `check`/`detect`, optional `uninstall` and
`upgrade`). Used when no native package manager carries the tool.

See [Providers — Script Provider](providers.md#script-provider).

## Effective Package

The package identifier passed to a provider. It is `package` when specified,
otherwise the logical tool name.

## Effective Settings

Global settings merged with the current host's `host_settings`.

## Group

A named collection of logical tool memberships and dotfile entries. Reusable
groups can be assigned to multiple hosts.

## Host Group

The protected machine-local group whose name matches the short hostname and has
`"special": "host"`. It acts as the local inbox for machine-specific entries.

See [Core Concepts](concepts.md#groups).

## Host Settings

Per-host overrides for selected settings such as managers, disabled providers,
dots repo, and dotfile sync enablement.

## Ignored

Configured but intentionally skipped by normal sync and refresh flows. Ignored
entries stay visible so the user can reverse the decision later.

## Installed With

The concrete manager recorded in cache as the owner of an installed package.
Upgrade and uninstall paths prefer this value when available.

## Logical Tool

The portable name Omni manages, independent of package-manager naming. For
example, logical tool `node` can install package `nodejs`.

## Provider Drift

A tool is installed through a different concrete manager than the current
default for its ecosystem. `reinstall` and `consolidate` are the normal repair
commands.

## Reconcile

The broad repair command that can sync missing tools, upgrade tools, repair
dotfile links, claim discovered tools, and commit dotfile repo changes.

See [Safety Model](safety.md#reconcile-and-discovery) and [Runbooks](runbooks.md#read-only-triage).

## Refresh

A provider scan that updates observed cache state. It does not edit
`settings.json`.

## Stow Package

A directory inside the dotfiles repo whose contents are linked into their target
locations by GNU Stow.

See [Dotfiles](dotfiles.md#configure-the-repo).

## Taps

Homebrew tap declarations required before installing a formula or cask.

## Tracked Tool

A logical tool present in `settings.json` and active through the current host's
group expansion.

## Variant

An alternate install spec for a logical tool, or a host-specific Stow package
for a dot entry. Context decides which kind of variant is meant.
