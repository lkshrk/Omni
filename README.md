# Omni

Portable tool, dotfile, and AI-agent resource management for developer machines.

<p align="center">
  <img src="docs/assets/omni-demo.gif" alt="Omni TUI demo" width="900">
</p>

Omni keeps tools and dotfiles portable through its JSON config. AI-agent
packages, skills, MCP servers, plugins, and marketplaces live in Microsoft APM;
Omni provides the shared CLI/TUI workflows over them.

> Omni is early-stage software. Review plans and dry-run output before using it
> on important machines.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/lkshrk/omni/main/scripts/linux-install.sh | bash
```

The script installs only `omni`; dotfile sync also needs GNU Stow. Homebrew,
Linux packages, release archives, and destination overrides are covered in the
[installation guide](docs/installation.md).

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

Before broad repairs, read the [Safety Model](docs/safety.md). For agent
resources, run `omni agents migrate --host <name>` to render a pre-APM host's
declarations as an APM manifest, then follow the
[Agents guide](docs/agents.md).

## Docs

Full documentation: <https://lkshrk.github.io/omni/>

## Development

```sh
make build
make test
make test-all
```

See [Development](docs/development.md) and
[CONTRIBUTING.md](CONTRIBUTING.md) for details.
