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
and Linux packages install `stow` with Omni. `go install` and release archives
install only the `omni` binary; install `stow` separately if you want dotfile
sync from those channels.

```sh
go install github.com/lkshrk/omni/cmd/omni@latest
```

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

Omni restores AI-agent skills from a tracked manifest by driving the
[`skills`](https://github.com/vercel-labs/skills) CLI (`npx`/`bunx`). Declare
skills under `agents.skills` in your config, or capture already-installed ones,
then restore the set on any machine. Also available as the **Skills** TUI tab.

```sh
omni agents skills import            # capture installed skills into the manifest
omni agents skills restore           # install the manifest skill set on this host
omni agents skills restore --dry-run # preview the skills add commands
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
