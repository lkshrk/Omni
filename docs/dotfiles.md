# Dotfiles

Omni syncs dotfiles through a Git-backed GNU Stow repo. Config declares logical
dot entries; Stow creates symlinks from the original filesystem locations to the
repo package directories.

Stow packages are plain directories. If package `nvim` contains
`.config/nvim/init.lua`, Stow links that file back to `~/.config/nvim/init.lua`.
Omni keeps this layout so the repo is auditable without running Omni.

## Configure The Repo

```sh
omni settings set dots_repo ~/dotfiles
omni dots status
```

The repo stores managed content under:

```text
~/dotfiles/dotfiles/<package>/...
```

The outer `~/dotfiles` path is the Git repository root. The inner `dotfiles/`
directory is the Stow package root. Git operations such as pull, commit, and
push run at the outer root; sync/adopt/link operations use the inner package
root.

For a logical entry named `nvim` with path `~/.config/nvim`, the default source
path is:

```text
~/dotfiles/dotfiles/nvim/.config/nvim
```

## Add A Dot Entry

Adopt an existing local path:

```sh
omni dots add ~/.config/nvim --group dev --adopt
omni dots sync
```

Discover candidates:

```sh
omni dots discover
omni dots add nvim --group dev --discovered
```

Choose the add mode deliberately:

| Command | Use when | Mutates local files? | Mutates repo files? |
| --- | --- | --- | --- |
| `omni dots add <path> --group <group>` | You want Omni to validate the path and stop before adoption. Current behavior requires `--adopt` for an existing local path. | no | no |
| `omni dots add <path> --group <group> --adopt` | The current local path is the desired source of truth. | yes | copies/adopts into repo package |
| `omni dots add <name-or-path> --group <group> --discovered` | `dots discover` found an existing repo/local candidate and you only want to persist it. | no | no |

`dots discover` reads three sources: packages under `<dots_repo>/dotfiles/`,
non-hidden children of `~/.config`, and well-known home-level files such as
shell and tool config files. It filters entries already tracked by config.

## Sync And Status

```sh
omni dots status
omni dots list
omni dots sync
omni dots sync nvim
omni dots sync --dry-run
```

`status` includes symlink health and dotfiles repo Git state. `sync` creates or
repairs Stow links.

## Ignore Patterns

Ignore an entire logical dot entry:

```sh
omni dots ignore nvim
omni dots unignore nvim
```

Ignore files inside an entry:

```sh
omni dots ignore nvim '*.log'
omni dots ignore nvim 'cache/'
omni dots unignore nvim '*.log'
```

Config shape:

```json
{
  "name": "nvim",
  "path": "~/.config/nvim",
  "ignore": ["*.log", "cache/", "/local.lua", "!/cache/keep"]
}
```

Patterns are gitignore-style and apply within the dot entry.

## Host Variants

Use variants when the same logical dotfile needs different content on different
machines:

```sh
omni dots variant add nvim --host workstation --package nvim@workstation
omni dots variant list nvim
omni dots variant remove nvim --host workstation
```

Config shape:

```json
{
  "name": "nvim",
  "path": "~/.config/nvim",
  "hosts": {
    "workstation": { "package": "nvim@workstation" }
  }
}
```

## Resolve Conflicts

When local files and repo files disagree, choose the source explicitly:

```sh
omni dots resolve nvim --use-repo
omni dots resolve nvim --use-local
```

Use repo when the dotfiles repo should replace local content. Use local when
the current machine has the desired content and the repo should be updated.

Force-resolve every conflict in one pass instead of per entry:

```sh
omni dots sync --use-repo    # keep the repo version for all conflicts
omni dots sync --use-local   # adopt the local version for all conflicts
```

A per-entry `on_conflict` policy still wins over the sync-wide flag. In the TUI,
press `U` (use repo) or `L` (use local) on the dots tab to force-resolve all
conflicts at once; both prompt for confirmation.

## Extract A Subdirectory

Split a subdirectory out of a tracked directory entry into its own entry so it
can belong to a different group than its parent:

```sh
omni dots extract nvim lua/plugins --group work
omni dots extract nvim lua/plugins --group work --name nvim-plugins
```

The parent stops managing the subtree (an `ignore` is added) and the subtree is
adopted as a new entry assigned to `--group` (default: this host's group). In
the TUI, expand an entry and press `g` on a child sub-path row to do the same.

## Git Workflow

```sh
omni dots pull
omni dots commit
omni dots push
omni dots history
```

`pull` fetches remote dotfile changes and then syncs links. `push` stages,
commits, and pushes all dotfiles repo changes.

## Native Services

Reminders check whether dotfiles need attention:

```sh
omni dots reminder install --interval 24h
omni dots reminder status
omni dots reminder check
```

Watch sync runs a local service that syncs after file changes:

```sh
omni dots watch install --debounce 5s
omni dots watch status
```

Inspect both:

```sh
omni dots services status
```

Native service support depends on the current platform. Omni uses user-level
services such as launchd or systemd where available.

## Backups

Before replacing local dotfiles, Omni creates safety copies under:

```text
~/dotfiles.bkp
```

Review backups before deleting them.
