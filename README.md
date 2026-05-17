# Omni - Manage all your packages and dotfiles

<p align="center">
  <img src="docs/assets/omni-demo.gif" alt="Omni TUI demo" width="900">
</p>

> ⚠️ Warning: Omni is early-stage software. Behavior can change. Use at your own risk.

## What It Does

Omni keeps your development tools and dotfiles in one portable JSON config. It tracks logical tools like `ripgrep`, `typescript`, or `black`, then installs them through the right local manager for each machine.

- **Tools**: portable providers for system (brew, apt, pacman, …), Node (npm, pnpm, bun), and Python (uv, pip) packages.
- **Dotfiles**: symlink sync backed by a git repo, with ignore patterns, host variants, reminders, and automatic watch sync.
- **Multi-machine**: cross-machine host assignments and reusable groups.
- **Health checks**: dashboard with reconcile flow, doctor diagnostics, and actionable repair steps.
- **CLI + TUI**: both surfaces over the same app layer.

## Install

```sh
go install github.com/lkshrk/omni@latest
```

Or via Homebrew:

```sh
brew tap lkshrk/tap
brew install omni
```

Releases also publish cross-platform archives and Linux packages (`.deb`, `.rpm`, `.apk`, Arch Linux).

### Prerequisites

- `go install` requires Go 1.26.2+. Release archives do not require Go.
- Package managers you enable must be on `PATH`.
- Dotfile sync requires a configured repo directory and GNU Stow.

## Quick Start

```sh
omni                    # launch TUI — runs bootstrap on first use
omni bootstrap          # or bootstrap from CLI
```

After bootstrap, Omni scans installed packages and opens the dashboard.

## Commands

### Getting started

| Command | Description |
|---|---|
| `omni` | Open the TUI |
| `omni bootstrap` | Guided host setup and activation |
| `omni doctor` | Read-only health checks |
| `omni reconcile` | Fix findings, sync dots/tools, commit dotfiles, upgrade tools |

### Tools (`omni tools`)

| Command | Description |
|---|---|
| `omni tools sync` | Install missing tools from config |
| `omni tools sync --all` | Add discovered tools + install missing |
| `omni tools upgrade [tool]` | Upgrade one or `--all` outdated tools |
| `omni tools add <pkg>` | Add a tool to config and a group |
| `omni tools delete <tool>` | Uninstall a tool |
| `omni tools install <tool>` | Install one missing tool |
| `omni tools list` | Show tool state |
| `omni tools search <query>` | Search provider registries |
| `omni tools import` | Import installed tools into config |
| `omni tools consolidate` | Move tools to default managers |

### Dotfiles (`omni dots`)

| Command | Description |
|---|---|
| `omni dots sync` | Repair dotfile symlinks |
| `omni dots add <path>` | Add a path to dotfile management |
| `omni dots delete <name>` | Remove a dotfile entry |
| `omni dots commit` | Commit dotfile repo changes |
| `omni dots push` | Commit + push dotfile changes |
| `omni dots pull` | Pull + resync dotfile links |
| `omni dots ignore <name> <pat>` | Add an ignore pattern to a dot entry |
| `omni dots variant add <name>` | Host-specific dotfiles |
| `omni dots history` | Show recent dotfile operations |
| `omni dots reminder install` | Install a native sync reminder timer |
| `omni dots watch install` | Install automatic watch sync service |
| `omni dots services status` | Show native service diagnostics |

### Config management

| Command | Description |
|---|---|
| `omni settings set <key> <val>` | Change a setting |
| `omni groups create <name>` | Create a group |
| `omni groups move-tool <grp> <tool>` | Move a tool to a group |
| `omni dots groups <name> --move <grp>` | Move a dotfile entry to a group |
| `omni hosts copy <src> <dst>` | Seed a new host from an existing one |

Run `omni <command> --help` for full flag details.

### Global flags

```sh
omni --config /path/to/settings.json   # override config path
omni --cache-dir /tmp/omni-cache       # override cache directory
omni -y                                # assume yes for prompts
```

## Config

Omni stores everything in `~/.config/omni/settings.json` ([schema](spec/omni.settings.v1.schema.json)).

```json
{
  "$schema": "https://raw.githubusercontent.com/lkshrk/omni/main/spec/omni.settings.v1.schema.json",
  "version": 1,
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

- **settings**: ecosystem managers, dotfile behavior, provider tracking.
- **tools**: logical tools with a default provider.
- **groups**: organize tools and dotfiles into reusable sets. Each tool and dotfile entry belongs to exactly one group. Assign tools and dotfiles to groups with `omni groups move-tool` and `omni dots groups --move`.
- **hosts**: map hostnames to groups — each host references one or more groups to define what gets synced on that machine. Every host also gets a protected hostname group for machine-local tools.

## Dotfiles

Dotfile entries live in groups, backed by Stow package directories in the configured repo. Each entry has a target path (e.g. `~/.config/nvim`) and a default package name.

### Ignore patterns

Each entry can skip files with an `ignore` list using gitignore-like syntax:

```json
{
  "name": "nvim",
  "path": "~/.config/nvim",
  "ignore": ["*.log", "cache/", "/local.lua", "!/cache/keep"]
}
```

### Host variants

Keep one logical entry with host-specific packages:

```json
{
  "name": "nvim",
  "path": "~/.config/nvim",
  "hosts": { "workstation": { "package": "nvim@workstation" } }
}
```

### Services

- **Reminders**: `omni dots reminder install --interval 24h` creates a native timer (launchd/systemd) that checks if dotfiles need attention.
- **Watch sync**: `omni dots watch install --debounce 5s` creates a native service that auto-syncs after file changes.
- Both configurable from TUI Settings → Dotfiles.

### Backups

Before mutating local dotfiles, Omni creates safety copies under `~/dotfiles.bkp`.

## Development

```sh
make tui-live                             # run TUI with live config
make tui-dev                              # run TUI with isolated dev config
make cli-dev ARGS='tools list'            # run CLI with isolated dev config
make build                                # compile ./bin/omni
make test                                 # unit tests with race detector
make test-integration                     # Docker-isolated integration tests
```

CI runs vet, golangci-lint, unit tests, and Docker integration tests. Releases are CI-gated via GoReleaser with Conventional Commit release notes.

See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution details.
