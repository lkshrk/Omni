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

Omni has no native agent import, adopt, resolve, skill store, plugin adapter,
marketplace registry, or per-agent assignment configuration.

## Patched APM Build

Omni temporarily requires APM `0.28.0+omni.2`, built from the immutable
`lkshrk/apm` commit
[`ea3f74ae5547059aca214e7a395d09e874205dce`](https://github.com/lkshrk/apm/commit/ea3f74ae5547059aca214e7a395d09e874205dce).
Installers use this exact source specification:

```text
git+https://github.com/lkshrk/apm.git@ea3f74ae5547059aca214e7a395d09e874205dce
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
