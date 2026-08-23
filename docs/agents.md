# Agents

APM is the sole owner of agent packages, skills, MCP, plugins, marketplaces,
locks, and deployed runtime state.

`omni agents sync` is the primary integration command. The remaining agent
commands are thin APM wrappers: `add`, `remove`, `update`, `search`, `audit`,
`targets`, `outdated`, `prune`, `deps`, and `marketplace`.

APM state:

- `~/.apm/apm.yml` — desired dependencies.
- `~/.apm/apm.lock.yaml` — resolved dependencies.
- `~/.apm/marketplaces.json` — marketplace metadata.

MCP is host-global across enabled APM targets that support user-global MCP.
Cursor and OpenCode are workspace-only and are rejected by global sync.

Omni has no steady-state agent adapter or parallel skill/plugin store. APM owns
deployment, audit, and lifecycle state after onboarding.

## Existing-state onboarding

`omni agents onboard` inventories legacy Omni v22/v23 declarations and active
Omni dotfile entries. Planning is read-only. Persist a reviewed plan with
`--plan-json /absolute/path/plan.json`, then apply it with
`omni agents onboard --apply --apply-plan /absolute/path/plan.json`.

For a dots-managed item, choose `move-to-apm` to materialize the real file and
remove it from dotfile sync, or `keep-in-dots` to leave ownership unchanged.
Other items can migrate, map secrets to environment variables, or remain
unmanaged. Target choices come from live `apm targets --json` output; Omni does
not hard-code target names.

Apply holds Omni's config-root lock, stages ordinary APM packages and manifest
changes, runs APM install and audit, and commits Omni v24 fragments last.
Interrupted migrations expose `status`, `resume`, `rollback`, and confirmed
`cleanup` subcommands. Unknown targets, missing secret mappings, unsafe dots
sources, and manifest conflicts block mutation.

## Patched APM Build

Omni temporarily requires APM `0.28.0+omni.6`, built from the immutable
`lkshrk/apm` commit
[`44d9233646017610feb6b293ebebcbc259aa7c26`](https://github.com/lkshrk/apm/commit/44d9233646017610feb6b293ebebcbc259aa7c26).
Installers use this exact source specification:

```text
git+https://github.com/lkshrk/apm.git@44d9233646017610feb6b293ebebcbc259aa7c26
```

The patch makes Hermes a stable explicit target, fixes global Antigravity MCP
cleanup, resolves user-scope audit paths correctly, and serializes APM lifecycle
mutations. The changes are proposed upstream in
[`microsoft/apm#2655`](https://github.com/microsoft/apm/pull/2655).

This fork is temporary. After the pull request is merged and an official APM
release contains the fixes, maintainers must run Omni's APM contract and
integration suites against that release, replace the fork source and exact
version check, then remove this section. Do not switch to a floating branch or
an untested official release.
