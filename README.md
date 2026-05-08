# Omni - Manage all your packages and dotfiles

<p align="center">
  <img src="docs/assets/omni-demo.gif" alt="Omni TUI demo" width="900">
</p>

> ⚠️ Warning: Omni is early-stage software. Behavior can change. Use at your own risk.

## What It Does

Omni keeps your development tools and dotfiles in one portable JSON config. It tracks logical tools like `ripgrep`, `typescript`, or `black`, then installs them through the right local manager for each machine.

Main features:

- Portable providers for system, Node, and Python tools.
- Cross-machine host assignments and reusable groups.
- Dotfile sync/discovery backed by a git repo.
- CLI and TUI surfaces over the same app behavior.
- Privilege-aware package actions that avoid blocking TUI password prompts.
- Provider override repair and cleanup flows for machine-specific managers.
- Install, delete, upgrade, import, search, consolidate, and sync flows.

Supported managers include Homebrew, apt, apk, dnf, pacman, zypper, npm, pnpm, bun, uv, pip3, and pip.

## Install

```sh
go install github.com/lkshrk/omni@latest
```

Releases also publish cross-platform archives, Linux package artifacts (`.deb`, `.rpm`, `.apk`, Arch Linux), and a Homebrew formula.

```sh
brew tap lkshrk/tap
brew install omni
```

Dotfile sync uses GNU Stow (`stow`) to manage links. When dotfile sync is enabled from onboarding, settings, the Dots tab, or interactive CLI commands, Omni checks for Stow and can install it through the detected system package manager. Noninteractive CLI runs fail with install guidance instead of prompting.

## Usage

Run `omni` to start the TUI. On first launch, onboarding creates `~/.config/omni/settings.json`, lets you choose package ecosystems, creates this machine's host assignment, optionally imports installed tools, and can enable dotfile sync. After onboarding, Omni performs the first package scan; this can take a while on a fresh cache.

You can also create or edit `~/.config/omni/settings.json` directly. The schema lives at [spec/omni.settings.schema.json](spec/omni.settings.schema.json).

Omni's config model is intentionally small:

- `settings` choose ecosystem managers, provider tracking, and dotfile behavior.
- `tools` define logical tools and their default provider.
- `groups` contain single-owner tool and dotfile assignments.
- `hosts` assign groups to hostnames.
- Each host also gets a protected hostname group for machine-local tools.

```json
{
  "$schema": "https://raw.githubusercontent.com/lkshrk/omni/main/spec/omni.settings.schema.json",
  "settings": {
    "ecosystems": {
      "node": { "manager": "bun" },
      "python": { "manager": "uv" }
    }
  },
  "tools": {
    "ripgrep": { "provider": "system" },
    "typescript": { "provider": "node" },
    "black": { "provider": "python" }
  },
  "hosts": {
    "workstation": ["dev"]
  },
  "groups": [
    {
      "name": "dev",
      "tools": ["ripgrep", "typescript", "black"],
      "dots": [
        { "name": "nvim", "path": "~/.config/nvim" }
      ]
    },
    { "name": "workstation", "special": "host" }
  ]
}
```

Base CLI workflow:

```sh
omni                                      # open the TUI
omni init                                 # run onboarding from the CLI
omni sync                                 # install/sync current host groups
omni sync --all                           # add discovered local tools and install missing tools
omni refresh                              # rescan installed/outdated tools and metadata
omni list                                 # show tool state
omni import                               # add installed tools to config
omni dots sync                            # sync dotfile links
```

Examples:

```sh
omni add fd --provider system             # add a logical tool
omni groups move-tool dev fd              # move a tool to a group
omni dots groups nvim --move dev          # move a dotfile entry to a group
omni dots sync --dry-run                  # preview dotfile links
omni settings set node.manager pnpm       # choose a host-local ecosystem manager
omni tools normalize --default-overrides --dry-run
                                          # preview cleanup of no-op provider overrides
```

### Dotfile Backups

Before Omni mutates an existing local dotfile target, it creates a safety copy under `~/dotfiles.bkp`. The backup mirrors the target's home-relative path, so `~/.config/nvim/init.lua` becomes `~/dotfiles.bkp/.config/nvim/init.lua`. If that backup path already exists, Omni keeps the old copy and writes the next backup with a numeric suffix such as `.1`.

Backups are created for destructive local dotfile flows such as adopt/add, replacing a broken or conflicting target, resolving conflicts, deleting or disabling managed links, and unstowing. Dry-runs and no-op synced entries do not create backups. The backup covers local files, directories, and symlinks in your home directory; repo-side dotfile history is handled by the configured git repo and optional dots auto-commit/push settings.

For exact options, use:

```sh
omni --help
omni sync --help
omni dots --help
```

Global flags:

```sh
omni --config /path/to/settings.json        # override settings path
omni --cache-dir /tmp/omni-cache            # override tool cache path
omni -y | --yes                             # assume yes for prompts
```

Provider choices are stored as portable ecosystem providers such as `system`, `node`, and `python`. A concrete provider or manager is only stored through `install_with` when it is an intentional pin, for example `system` via `apt` on a host where the default would be `brew`. To clean older configs that explicitly pin the current default manager, preview first and then apply:

```sh
omni tools normalize --default-overrides --dry-run
omni tools normalize --default-overrides -y
```

Bulk TUI sync/upgrade skips package actions that need sudo/root. Single Homebrew cask actions that may prompt for an admin password open an embedded Admin Terminal prompt inside the TUI, then refresh the row when Brew finishes.

Use the TUI for interactive tool management, host/group assignments, dotfile sync, settings, search, admin cask prompts, and the command palette.

## Development

```sh
make tui-live                             # run TUI with live/default config and cache
make tui-dev                              # run TUI with isolated dev config/cache
make cli-live ARGS='--help'               # run CLI with live/default config and cache
make cli-dev ARGS='tools normalize --default-overrides --dry-run'
                                          # run CLI with isolated dev config/cache
make build                                 # compile ./bin/omni
make test                                  # unit tests with race detector
make test-integration                      # Docker-isolated integration tests
make demo-gif                              # regenerate the README demo GIF
```

`make run` aliases `make tui-live`; `make cli` aliases `make cli-dev`. Dev targets default to `DEV_DIR=/private/tmp/omni-dev` and `DEV_HOST=devhost`.

The demo GIF target uses [VHS](https://github.com/charmbracelet/vhs) and records `demo/omni-demo.tape` against an isolated temp config, cache, home directory, fake package managers, and fake Stow binary.

CI runs vet, golangci-lint, unit tests, and Docker integration tests. Releases are CI-gated: if a successful CI commit has a `vX.Y.Z` tag, GoReleaser publishes the release artifacts and generates release notes from Conventional Commit subjects.

See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution details.
