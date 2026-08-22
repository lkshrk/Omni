# State and Files

Omni stores tools, dotfiles, and host settings. Agent desired and runtime state
belongs to APM.

## Agent state

APM owns the global manifest, lockfile, package cache, marketplace metadata, and
deployed agent files:

- `~/.apm/apm.yml` — desired package/runtime declarations.
- `~/.apm/apm.lock.yaml` — resolved dependency state.
- `~/.apm/marketplaces.json` — marketplace registry/cache.
- APM target directories and agent configuration files under `$HOME`.

Omni does not import, adopt, or maintain a parallel skill/plugin store. It does
not assign agent resources through groups or host-specific fleet lists. Use APM
commands or `omni agents sync` to change agent state.

## Omni state

Omni configuration remains in `settings.json` and optional `settings.d/*.json`
fragments. SQLite state lives under the normal Omni data directory. Dotfiles
remain owned by Omni and tools remain owned by their configured providers.

Legacy `.skill-lock.json`, native plugin registries, and old Omni agent stores
are not read or migrated. Remove them manually if they are no longer needed.

## Environment variables

`HOME`, `XDG_DATA_HOME`, and provider-specific environment variables control
Omni paths and provider behavior. APM resolves its own runtime environment.

## Cache contents

Omni caches discovery and provider metadata. APM owns package and marketplace
cache contents under `~/.apm/`.
