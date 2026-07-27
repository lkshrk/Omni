# Omni

Portable package and dotfile management for developer machines.

<p align="center">
  <img src="docs/assets/omni-demo.gif" alt="Omni TUI demo" width="900">
</p>

Omni keeps logical tools, host assignments, and dotfiles in one JSON config. It
installs tools through local package managers, syncs dotfiles through a
Git-backed GNU Stow repo, and exposes the same behavior through a CLI and TUI.

> Omni is early-stage software. Review plans and dry-run output before using it
> on important machines.

## Install

Package-manager installs declare GNU Stow as a runtime dependency, so Homebrew
and Linux packages install `stow` with Omni. The install script and release
archives install only the `omni` binary; install `stow` separately if you want
dotfile sync from those channels.

Linux (and other Unix) — download the latest release binary into `~/.local/bin`:

```sh
curl -fsSL https://raw.githubusercontent.com/lkshrk/omni/main/scripts/linux-install.sh | bash
```

Set `DIR` to choose a different destination (e.g. `DIR=/usr/local/bin`).

Or:

```sh
brew tap lkshrk/tap
brew trust --tap lkshrk/tap
brew install omni
```

Homebrew 6 requires trust for non-official taps before it will load their
formulae. Trust can also be narrowed to the Omni formula with
`brew trust --formula lkshrk/tap/omni`.

Linux packages and release archives are published on
[GitHub Releases](https://github.com/lkshrk/omni/releases).

## Start

```sh
omni
```

CLI-only setup:

```sh
omni bootstrap
omni doctor
omni reconcile
```

Before broad repairs, read the [Safety Model](docs/safety.md).

## Agent skills

Omni natively acquires and syncs AI-agent skills from Git, well-known HTTP
catalogs, or local directories. Declare desired packages under
`agents.packages`; `skills` selectors are optional, and omission installs every
discovered skill. Legacy lockfile-only installs can be imported explicitly.
Also available from the **Skills** TUI tab.

```sh
omni agents find react               # search the skills.sh catalog
omni agents skills import            # capture legacy lockfile installs
omni agents skills sync              # install the manifest skill set on this host
omni agents skills sync --dry-run    # preview native network/filesystem actions
```

## Docs

Full documentation can be found at <https://lkshrk.github.io/omni/>. Local docs commands use `uv`:

```sh
python3 -m venv .tmp/docs-venv
uv pip install --python .tmp/docs-venv/bin/python -r docs/requirements.txt
.tmp/docs-venv/bin/mkdocs serve
```

## Development

```sh
make build
make test
make test-integration
```

See [Development](docs/development.md) and
[CONTRIBUTING.md](CONTRIBUTING.md) for details.
