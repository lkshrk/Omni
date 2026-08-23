# Agent migration

Agent desired and runtime state is now owned by APM. Omni provides a reviewed,
one-time onboarding flow for machines that still have legacy Omni agent config,
agent resources managed through Omni dots, or recognizable native filesystem
primitives such as packages, skills, plugins, agents, commands, and hooks.

## Interactive migration

Launch `omni`, open Agents, and press `O`. Review each item, resolve target or
secret blockers, then confirm apply. Dots-owned items can move to APM or remain
in dots. Native items can move to APM or remain unmanaged.

## CLI migration

Persist a read-only plan:

```sh
omni agents onboard --json --plan-json /tmp/omni-onboard-plan.json
```

Review the plan, then apply it with the required decisions:

```sh
omni agents onboard \
  --apply \
  --apply-plan /tmp/omni-onboard-plan.json \
  --approve-targets ITEM=TARGET \
  --move-to-apm ITEM
```

Use `--map-secret ITEM:FIELD=ENV_VAR`, `--keep-in-dots ITEM`, or
`--exclude ITEM` where appropriate. Item IDs and allowed targets come from the
reviewed plan; replace both `ITEM` and `TARGET` from that plan. Apply revalidates
the source preimages before mutation.

Apply stages ordinary packages under `~/.apm/omni-imports`, updates
`~/.apm/apm.yml`, runs APM install and audit, and commits Omni schema v24 last.
Use `status` and `resume` after interruption. `rollback` is available only
before dots/native materialization starts; later failures resume forward.
Confirmed `cleanup` removes the private migration journal and backups but keeps
APM state and the durable completion marker.

Adapter-specific state that cannot be represented losslessly—such as some
client databases or native MCP formats—stays unmanaged. Onboarding never
silently broadens such state into an APM dependency.
