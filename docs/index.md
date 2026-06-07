# Omni

Omni keeps development tools and dotfiles in sync across machines from one
portable JSON config.

It manages logical tools such as `ripgrep`, `typescript`, and `black`, then
installs them with the right package manager for the current machine. It also
syncs dotfiles through a Git-backed GNU Stow repo.

![Omni TUI demo](assets/omni-demo.gif)

!!! warning "Early-stage software"
    Omni is still changing quickly. Review generated plans and dry-run output
    before using it on important machines.

## What Omni Manages

- **Tools**: system packages, Node packages, and Python packages through
  provider families.
- **Dotfiles**: Stow package directories, symlink repair, ignored paths,
  host-specific variants, Git commit/push flows, and optional native services.
- **Hosts and groups**: per-machine activation of reusable tool and dotfile
  groups.
- **Health**: dashboard checks, `doctor`, `reconcile`, cache refreshes, and
  repair actions.
- **Two surfaces**: the interactive TUI and a scriptable CLI share the same app
  boundary.

## Fast Path

```sh
go install github.com/lkshrk/omni/cmd/omni@latest
omni
```

If you install through Homebrew or a Linux package, Omni pulls in GNU Stow for
dotfile sync. If you use `go install` or a release archive, install `stow`
separately before syncing dotfiles.

The first launch opens the TUI and guides the current host through setup. For a
CLI-only setup:

```sh
omni bootstrap
omni doctor
omni reconcile
```

## Documentation Map

| Goal | Start here |
| --- | --- |
| Install Omni | [Installation](installation.md) |
| Avoid destructive surprises | [Safety Model](safety.md) |
| Set up the first machine | [First Run](getting-started.md) |
| Understand the data model | [Core Concepts](concepts.md) |
| Know where state lives | [State And Files](state-and-files.md) |
| Copy a working workflow | [Recipes](recipes.md) |
| Edit `settings.json` directly | [Configuration](configuration.md) and [Schema Reference](schema-reference.md) |
| Understand provider resolution | [Providers](providers.md) |
| Manage tools | [Tools](tools.md) |
| Manage dotfiles | [Dotfiles](dotfiles.md) |
| Use the terminal UI | [TUI](tui.md) |
| Script Omni | [CLI Reference](cli.md) and [Command Matrix](command-matrix.md) |
| Follow operational procedures | [Runbooks](runbooks.md) |
| Learn internal architecture | [Architecture](architecture.md) |
| Look up terminology | [Glossary](glossary.md) |
| Answer common questions | [FAQ](faq.md) |
| Fix a broken setup | [Troubleshooting](troubleshooting.md) |
| Work on Omni itself | [Development](development.md), [Test Matrix](test-matrix.md), and [Documentation Maintenance](documentation-maintenance.md) |
