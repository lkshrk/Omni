# Agents

APM is the sole owner of steady-state agent packages, skills, MCP, plugins,
marketplaces, dependency locks, and deployed runtime state.

`omni agents sync` is the primary integration command. The remaining agent
commands are thin APM wrappers: `add`, `remove`, `update`, `search`, `audit`,
`targets`, `outdated`, `prune`, `deps`, and `marketplace`.

APM state:

- `~/.apm/apm.yml` — desired dependencies.
- `~/.apm/apm.lock.yaml` — resolved dependencies.
- `~/.apm/marketplaces.json` — marketplace metadata.

MCP is host-global across enabled APM targets that support user-global MCP.
Cursor and OpenCode are workspace-only and are rejected by global sync.

Omni has no native steady-state agent implementation or parallel skill/plugin
store. Its thin APM adapter keeps no manifest or deployment state; APM owns
deployment, audit, and lifecycle state after onboarding.

## Existing-state onboarding

`omni agents onboard` inventories legacy Omni v22/v23 declarations, active
Omni dotfile entries, and recognizable filesystem primitives under deploy roots
reported by APM. Planning is read-only and runs APM probes in a disposable
HOME/XDG environment. Persist a reviewed plan with
`--plan-json /absolute/path/plan.json`, then apply it with
`omni agents onboard --apply --apply-plan /absolute/path/plan.json`.
See [Agent Migration](migrating-skills.md) for the complete interactive and CLI
walkthrough.

For a dots-managed item, choose `move-to-apm` to materialize the real file and
remove it from dotfile sync, or `keep-in-dots` to leave ownership unchanged.
Other items can migrate, map secrets to environment variables, or remain
unmanaged. Native filesystem items can move to APM or remain unmanaged, but
cannot be marked as dots-owned. Target choices and deploy roots come from live
`apm targets --json --all` output; Omni does not hard-code target names.

Apply uses Omni's config-root lock while revalidating and journaling the plan.
It then stages ordinary APM packages under `~/.apm/omni-imports`, runs APM
install and audit, reacquires the lock to commit Omni v24 fragments last, and
finally writes `~/.apm/.omni-onboarding-complete`.
Interrupted migrations expose `status`, `resume`, `rollback`, and confirmed
`cleanup` subcommands. Unknown targets, missing secret mappings, unsafe dots
sources, and manifest conflicts block mutation.

Adapter-specific native state that cannot be represented losslessly, including
some client databases and native MCP formats, remains unmanaged rather than
being guessed or broadened.

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
