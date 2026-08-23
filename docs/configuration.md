# Configuration

Omni `settings.json` configures tools, dotfiles, providers, hosts, and groups.
Agent resources are not stored in Omni configuration.

Agent desired/runtime state is owned by APM:

- `~/.apm/apm.yml`
- `~/.apm/apm.lock.yaml`
- `~/.apm/marketplaces.json`

Use `omni agents sync` and its thin APM wrappers for agent operations. There is
no steady-state Omni skill store, native plugin lifecycle, or per-agent/group
assignment model. `omni agents onboard` is the one-time exception: it converts
legacy Omni declarations, active dots resources, and recognizable native
filesystem primitives into ordinary APM dependencies.

Current settings schema version: 24. Configurations containing the removed
top-level `agents` field are rejected with instructions to use APM.
