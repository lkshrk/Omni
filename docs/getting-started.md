# First Run

## Start The TUI

```sh
omni
```

With no subcommand, Omni launches the interactive TUI. On a new machine it runs
bootstrap first, then opens the dashboard.

Bootstrap does four jobs:

1. Creates or loads `settings.json`.
2. Detects available package managers.
3. Ensures the current host exists.
4. Offers to import installed tools or sync from config.

## CLI-Only Bootstrap

Use this path for servers, automation, or headless environments:

```sh
omni bootstrap
omni doctor
omni reconcile
```

`doctor` is read-only. `reconcile` is the normal "make this machine healthy"
command: it refreshes state, syncs missing tools, repairs dotfile links, runs
available upgrades, claims discovered tools into this machine's host group, and
handles dotfile Git work. See [Safety Model](safety.md) before running it on an
important machine.

## First Tool

For one machine, add the tool directly to the machine group:

```sh
omni tools add ripgrep --provider system --group "$(hostname -s)"
omni tools sync
```

For a tool set you want to reuse across machines, create a reusable group and
assign the logical tool to that group:

```sh
omni groups create dev
omni tools set ripgrep --provider system
omni groups move-tool dev ripgrep
omni tools sync
```

`system` is portable. On macOS it usually resolves to Homebrew; on Linux it
resolves to the first available system manager in the configured priority.

## First Dotfile

Omni stores dotfiles as GNU Stow packages. A Stow package is a directory in the
repo whose contents mirror the paths to link into `$HOME`. Omni owns the Stow
commands, but the package layout stays inspectable in Git.

Set a dotfiles repo, add a config path, and sync links:

```sh
omni settings set dots_repo ~/dotfiles
omni dots add ~/.config/nvim --group dev --adopt
omni dots sync
```

Use `--group "$(hostname -s)"` instead of `--group dev` for a machine-local
dotfile.

The dotfiles repo stores Stow packages under `dotfiles/`. Local files are linked
back to their original paths.

## Add Another Machine

On the existing machine:

```sh
omni hosts list
```

On the new machine:

```sh
omni bootstrap
omni hosts copy old-host new-host
omni reconcile
```

You can also assign reusable groups directly:

```sh
omni hosts add-group new-host dev
omni hosts remove-group new-host old-group
```

## Daily Workflow

Use the TUI dashboard for routine work, or run:

```sh
omni doctor
omni reconcile
```

For targeted operations:

```sh
omni tools sync
omni tools upgrade --all
omni dots status
omni dots sync
```
