# Configuration

Omni `settings.json` configures tools, dotfiles, providers, hosts, and groups.
Agent resources are not stored in Omni configuration.

Agent desired/runtime state is owned by APM:

- `~/.apm/apm.yml`
- `~/.apm/apm.lock.yaml`
- `~/.apm/marketplaces.json`

Use `omni agents sync` and its thin APM wrappers for agent operations. There is
no steady-state Omni skill store, native plugin lifecycle, or per-agent/group
assignment model.

A host may keep its desired APM manifest as a template at
`~/.config/omni/apm.yml`, which sync copies over `~/.apm/apm.yml`. It is an
ordinary dotfile-managed file, not Omni configuration.
`omni agents migrate --host <name>` renders one from a pre-APM snapshot.

Current settings schema version: 24. Configurations containing the removed
top-level `agents` field are rejected with instructions to use APM.
