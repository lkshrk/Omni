# Troubleshooting

## Start With Doctor

```sh
omni doctor
```

`doctor` is read-only. It reports config, cache, provider, dotfiles, and native
service problems before you run mutating repair commands.

For ordered procedures with preflight and verification steps, see
[Runbooks](runbooks.md).

## No Active Host

Symptoms:

- commands fail before doing package work
- Omni asks you to bootstrap
- an error includes `no host configuration for "<host>" - run 'omni bootstrap'`

Fix:

```sh
omni bootstrap
omni hosts ensure "$(hostname -s)"
omni hosts list
```

For tests or scripted use, set:

```sh
OMNI_HOSTNAME=testhost omni --config settings.json hosts ensure testhost
```

## Provider Not Available

Symptoms:

- sync cannot install through `system`, `node`, or `python`
- provider list omits the expected concrete manager
- output includes `provider unavailable` for one or more tools

Inspect:

```sh
omni tools providers
omni settings show
```

Fix the manager on `PATH`, then refresh:

```sh
omni tools refresh
```

For ecosystem defaults:

```sh
omni settings set provider_priority brew,apt,npm,pip
```

## Stow Missing

Dotfile sync requires GNU Stow:

```sh
omni dots sync
```

Interactive CLI and TUI flows can offer to install Stow through the system
package manager when Omni can plan that install. Noninteractive commands fail
with a concrete install message.

Typical message:

```text
GNU Stow (stow) is required for dotfile sync. Install stow with your system package manager, then rerun this command.
```

## Dotfile Conflict

Inspect the entry:

```sh
omni dots status nvim
```

Choose one side:

```sh
omni dots resolve nvim --use-repo
omni dots resolve nvim --use-local
```

Use repo when the dotfiles repo is correct. Use local when the current machine's
files are correct.

Typical message:

```text
choose exactly one of --use-repo or --use-local
```

## Stale Tool State

Refresh provider state:

```sh
omni tools refresh
omni tools list
```

If the cache itself looks corrupted:

```sh
omni settings reset-cache
omni tools refresh
```

The cache is local and disposable. `settings.json` is the source of truth.
See [State And Files](state-and-files.md) for the exact source-of-truth model.

## Wrong Concrete Manager

If a tool is installed with a manager that is no longer the default:

```sh
omni tools consolidate node bun
omni tools consolidate python uv
```

For one tool:

```sh
omni tools reinstall <tool> --reinstall-default
omni tools reinstall <tool> --from <old-provider> --to <new-provider>
```

See [Providers](providers.md) for provider drift and concrete ownership rules.

## Missing Group Or Assignment Target

Noninteractive add flows need an explicit group:

```text
missing assignment target: pass --group <group> for non-interactive add
```

Fix:

```sh
omni tools add ripgrep --provider brew --group "$(hostname -s)"
omni dots add ~/.config/nvim --group "$(hostname -s)" --adopt
```

If sync targets a group that does not exist, create or choose the group first:

```text
group "dev" not found
```

## Privileged Package Operations

Some system packages require a password. In the TUI, Omni uses an embedded Admin
Terminal prompt for those operations. In CLI scripts, run commands in a terminal
that can prompt or use the system package manager manually first.

## Reconcile Everything

When you want the normal repair pass:

```sh
omni reconcile
```

This may mutate tools, dotfile links, the local cache, and the dotfiles repo.
Run `doctor` first when you need a read-only diagnosis.
