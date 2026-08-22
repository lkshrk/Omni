# TUI

The Agents view reports APM state and exposes three actions: sync/install (`S`),
update (`U`), and dependency-list inspection (`R`). Use the CLI for the other
APM wrapper commands.

Agent desired/runtime state is owned by APM (`~/.apm/apm.yml`, lockfile, and
`~/.apm/marketplaces.json`). The TUI does not import, adopt, resolve, or assign
resources per agent. MCP is host-global for APM-supported user-global targets.
