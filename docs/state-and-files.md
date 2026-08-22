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

Omni does not maintain a parallel skill/plugin store. Existing-state onboarding
converts only legacy Omni declarations; APM discovers and adopts native state.

## Omni state

Omni configuration remains in `settings.json` and optional `settings.d/*.json`
fragments. SQLite state lives under the normal Omni data directory. Dotfiles
remain owned by Omni and tools remain owned by their configured providers.

Durable onboarding journals and private backups live under
`$OMNI_STATE_DIR/onboarding/<operation-id>/`, or
`$XDG_STATE_HOME/omni/onboarding/<operation-id>/` by default. They remain until
an explicit confirmed cleanup after both Omni and APM are terminal.

## Environment variables

`HOME`, `XDG_DATA_HOME`, `XDG_STATE_HOME`, `OMNI_STATE_DIR`, and
provider-specific environment variables control Omni paths and provider
behavior. APM resolves its own runtime environment.

## Cache contents

Omni caches discovery and provider metadata. APM owns package and marketplace
cache contents under `~/.apm/`.
