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
`omni agents migrate --host <name>` previews one from a pre-APM snapshot;
`--write` publishes wrappers and updates the migration-owned template.

Current settings schema version: 24. Configurations containing the removed
top-level `agents` field are rejected with instructions to use APM.

The generated JSON Schema is the authoritative field reference:
[`spec/omni.settings.schema.json`](https://github.com/lkshrk/omni/blob/main/spec/omni.settings.schema.json).
It covers tools, dotfiles, providers, hosts, groups, and ignore lists; agent
resources are deliberately absent.
