# Recipes

These recipes are copy-paste starting points. Replace hostnames, group names,
tool names, and paths with your real values.

## Set Up A New Laptop From An Existing Host

On the old machine:

```sh
omni hosts list
```

On the new machine:

```sh
omni bootstrap
omni hosts copy old-host new-laptop
omni doctor
omni reconcile
```

Expected result: the new host has its own protected host group plus the reusable
groups copied from `old-host`.

## Add A Portable System Tool

```sh
omni groups create dev
omni tools set ripgrep --provider system
omni groups move-tool dev ripgrep
omni tools sync
```

Expected result: Omni installs `ripgrep` through the current machine's concrete
system manager, such as Homebrew on macOS or `apt` on Debian/Ubuntu.

## Add A Tool Whose Package Name Differs

```sh
omni tools set node --provider system --package nodejs
omni groups move-tool dev node
omni tools sync
```

Expected result: the logical tool is `node`, but the package manager receives
`nodejs`.

## Use A Specific Manager For One Tool

```sh
omni tools set typescript --provider node --package typescript --install-with pnpm
omni groups move-tool dev typescript
omni tools sync
```

Expected result: this one tool uses `pnpm`, even if the default Node manager is
`bun` or `npm`.

## Move Python Tools To uv

Preview first:

```sh
omni tools consolidate python uv --dry-run
```

Apply:

```sh
omni tools consolidate python uv
```

Expected result: Python tools move to the `uv` manager and stale installs are
removed best-effort from the old manager.

## Adopt Neovim Config Into Dotfiles

```sh
omni settings set dots_repo ~/dotfiles
omni groups create dev
omni dots add ~/.config/nvim --group dev --adopt
omni dots sync
omni dots status nvim
```

Expected result: local Neovim files are copied into the dotfiles repo and the
target path is linked through Stow.

## Ignore Machine-Local Dotfile Noise

```sh
omni dots ignore nvim 'cache/'
omni dots ignore nvim '*.log'
omni dots sync
```

Expected result: cache and log files stop appearing as dotfile drift.

## Use Host-Specific Dotfile Content

```sh
omni dots variant add nvim --host workstation --package nvim@workstation --sync
omni dots variant list nvim
```

Expected result: the logical dot entry stays `nvim`, but `workstation` uses the
`nvim@workstation` Stow package.

## Recover From A Dotfile Conflict

Inspect:

```sh
omni dots status nvim
```

Keep the repo version:

```sh
omni dots resolve nvim --use-repo
```

Keep the local version:

```sh
omni dots resolve nvim --use-local
```

Expected result: the conflict is resolved explicitly, and the entry can sync
again.

## Rebuild Stale Tool State

```sh
omni settings reset-cache
omni tools refresh
omni tools list
```

Expected result: Omni discards local cache rows and rebuilds observed tool state
from providers.

## Headless Health Check

```sh
omni doctor
omni reconcile
```

Expected result: `doctor` reports issues without changing state; `reconcile`
then applies the normal repair pass.
