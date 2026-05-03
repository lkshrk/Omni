# Omni - Manage all your packages and dotfiles

<p align="center">
  <img src="docs/assets/omni-demo.gif" alt="Omni TUI demo" width="900">
</p>

> ⚠️ Warning: Omni is early-stage software. Behavior can change. Use at your own risk.

## What It Does

Omni keeps your development tools and dotfiles in one portable JSON config. It tracks logical tools like `ripgrep`, `typescript`, or `black`, then installs them through the right local manager for each machine.

Main features:

- Portable providers for system, Node, and Python tools.
- Cross-machine profiles and groups.
- Dotfile sync/discovery backed by a git repo.
- CLI and TUI surfaces over the same app behavior.
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

Create or edit `~/.config/omni/settings.json` (schema in one place: [spec/omni.settings.schema.json](spec/omni.settings.schema.json)):

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
  "groups": [
    { "tools": ["ripgrep", "typescript", "black"] }
  ]
}
```

Common CLI commands:

```sh
omni init                                   # onboard machine and optionally import tools
omni init --import                          # example init variant

omni sync                                   # sync tools for current profile/group
omni sync --all                             # example: sync everything

omni list                                   # list tools and install state
omni list --provider system                 # example filter by provider

omni import                                 # import currently installed tools
omni import --dry-run                       # example: preview import

omni install fd --provider brew             # install one tool
omni delete rg --provider brew              # delete one tool
omni upgrade --all                          # upgrade outdated tools
omni search ripgrep                         # search provider registries
omni add fd --provider system               # add a tool spec
omni switch black --from pip3 --to uv

omni providers                              # list available providers
omni profile                                # manage profiles (list/add/remove/assign)
omni tools                                  # manage logical tool specs
omni groups                                 # manage tool groups
omni settings                               # inspect/mutate settings
omni consolidate python uv                  # convert ecosystem managers
omni dots discover                          # discover unmanaged dotfile candidates
omni dots sync --dry-run                    # example: sync dot links dry-run
omni ui
```

For exact, current options, use:

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

Launch the TUI:

```sh
omni                                        # starts the TUI directly (same as `omni ui`)
omni ui
```

Use the TUI for interactive tool management, profile/group edits, dotfile sync, settings, search, and the command palette.

## Development

```sh
make build                                 # compile ./bin/omni
make test                                  # unit tests with race detector
make test-integration                      # Docker-isolated integration tests
make demo-gif                              # regenerate the README demo GIF
```

CI runs vet, golangci-lint, unit tests, and Docker integration tests. Releases are CI-gated: if a successful CI commit has a `vX.Y.Z` tag, GoReleaser publishes the release artifacts.

See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution details.
