# Architecture

Omni owns tool and dotfile configuration. APM owns all agent desired and
runtime state.

The agent boundary is a thin adapter around APM: Omni selects and invokes APM
commands, then presents their output in the CLI/TUI. APM owns manifests,
resolution, lockfiles, package installation, marketplaces, plugins, MCP, and
target deployment.

APM state lives under `~/.apm/` (`apm.yml`, `apm.lock.yaml`, and
`marketplaces.json`). Omni keeps no parallel agent manifest, ownership ledger,
or rollback snapshot.
