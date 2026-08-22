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

Omni has no native agent adapter or parallel skill/plugin store. APM owns
native discovery, adoption, conflict handling, deployment, audit, and recovery.

## Existing-state onboarding

`omni agents onboard` inventories legacy Omni v22/v23 declarations plus native
Claude and Codex state and prints APM's redacted plan. Planning is read-only.
Persist a reviewed plan with `--plan-json /absolute/path/plan.json`, then apply
it with `omni agents onboard --apply --apply-plan /absolute/path/plan.json`.

Apply holds Omni's config-root lock, asks the pinned APM binary to install and
audit, commits Omni v24 fragments last, then finalizes APM's external-commit
fence. Interrupted operations expose `status`, `resume`, and confirmed
`cleanup` subcommands. Literal secrets, unsupported targets, unresolved
conflicts, and target broadening block mutation.

## Patched APM Build

Omni temporarily requires APM `0.28.0+omni.3`, built from the immutable
`lkshrk/apm` commit
[`fe2d55f37062a9147ae297d7d4c8a034c327661c`](https://github.com/lkshrk/apm/commit/fe2d55f37062a9147ae297d7d4c8a034c327661c).
Installers use this exact source specification:

```text
git+https://github.com/lkshrk/apm.git@fe2d55f37062a9147ae297d7d4c8a034c327661c
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
