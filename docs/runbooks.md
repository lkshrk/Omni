# Runbooks

Runbooks are ordered procedures for common high-impact workflows. Each one has
a preflight, an apply step, and verification.

Use [Command Matrix](command-matrix.md) to check command risk, host requirements,
and dry-run availability before adapting a runbook.

## Read-Only Triage

Use this before any broad repair command.

Preflight:

```sh
omni doctor
omni settings show
omni hosts list
omni tools providers
omni dots status
```

Interpretation:

| Signal | Meaning | Next step |
| --- | --- | --- |
| No active host | Omni cannot expand desired tool state. | Run `omni bootstrap` or `omni hosts ensure`. |
| Provider unavailable | A package manager is missing or not on `PATH`. | Install or enable the manager, then refresh. |
| Dot conflict | Local target and repo package disagree. | Resolve with `--use-repo` or `--use-local`. |
| Stale cache | Displayed state does not match package managers. | Reset or refresh cache. |

Apply only after reading the diagnostics:

```sh
omni reconcile
```

Verify:

```sh
omni doctor
omni tools list
omni dots status
```

## New Machine From Existing Config

Use this when `settings.json` already exists or has been imported.

Preflight:

```sh
omni bootstrap --no-import
omni hosts list
```

If another host is the template:

```sh
omni hosts copy old-host new-host
```

If you want to assign groups manually:

```sh
omni hosts ensure new-host
omni hosts add-group new-host dev
omni hosts add-group new-host work
```

Apply:

```sh
omni doctor
omni reconcile
```

Verify:

```sh
omni hosts list
omni tools list
omni dots status
```

Stop when the active host has the expected reusable groups, required tools are
installed, and dotfile entries are healthy.

## Move An Ecosystem To One Manager

Use this when moving Node tools to `bun` or Python tools to `uv`.

Preflight:

```sh
omni tools refresh
omni tools list
omni settings show
```

Preview:

```sh
omni tools consolidate python uv --dry-run
```

Apply:

```sh
omni settings set provider_priority uv,pip,brew,apt
omni tools set black --provider uv
```

Verify:

```sh
omni tools refresh
omni tools list
```

If one tool must stay on another manager, keep that tool's provider entry on
the concrete provider it should use.

## Install Or Prune A Group Safely

Use this when adding or removing a reusable group from a machine.

Preflight:

```sh
omni hosts list
omni tools sync --group dev --dry-run
```

Assign:

```sh
omni hosts add-group "$(hostname -s)" dev
```

Apply:

```sh
omni tools sync --group dev
```

If you are intentionally removing local packages no longer in config, preview
the prune first:

```sh
omni tools sync --prune --dry-run
```

Then apply:

```sh
omni tools sync --prune
```

Verify:

```sh
omni tools list
```

## Recover From Manual Package Changes

Use this after installing, uninstalling, or upgrading tools outside Omni.

Preflight:

```sh
omni tools refresh
omni tools list
```

If Omni now shows provider drift, repair one tool:

```sh
omni tools reinstall <tool> --reinstall-default
```

Or repair a whole ecosystem:

```sh
omni tools consolidate node bun --dry-run
omni tools consolidate node bun
```

Verify:

```sh
omni tools refresh
omni tools list <tool>
```

## Resolve Dotfile Drift

Use this when a dot entry is missing, conflicted, or no longer linked through
Stow.

Preflight:

```sh
omni dots status <name>
```

If the repo version is correct:

```sh
omni dots resolve <name> --use-repo
```

If the local version is correct:

```sh
omni dots resolve <name> --use-local
```

Repair links:

```sh
omni dots sync <name>
```

Verify:

```sh
omni dots status <name>
```

Push only after inspecting the dotfiles repo diff:

```sh
omni dots push
```

## Isolated Reproduction

Use this for docs examples, bug reports, and local experiments.

Setup:

```sh
mkdir -p /tmp/omni-repro
export OMNI_HOSTNAME=reprohost
export OMNI_CONFIG=/tmp/omni-repro/settings.json
export OMNI_CACHE_DIR=/tmp/omni-repro/cache
```

Run:

```sh
omni bootstrap --no-import
omni hosts ensure reprohost
omni settings show
omni doctor
```

Clean up by removing `/tmp/omni-repro` after the reproduction. Do not point
integration tests at a real home directory or real package-manager state.

## Report A Bug

Include:

- Omni version
- operating system
- install channel
- `omni doctor` output
- relevant `settings.json` excerpt with private paths removed
- command run
- actual output
- expected output

Prefer an isolated reproduction when possible:

```sh
OMNI_HOSTNAME=reprohost \
OMNI_CONFIG=/tmp/omni-repro/settings.json \
OMNI_CACHE_DIR=/tmp/omni-repro/cache \
omni doctor
```

## Manual TUI QA In A Browser

`scripts/tui-e2e.sh up` builds a static `omni`, seeds an isolated Alpine
container (manifest skill package, a legacy CLI-managed install for
import/claim flows, a planted drifted skill, and a stubbed catalog for
`find`), and serves the TUI at `http://127.0.0.1:7681/` through `ttyd`. Each
browser connection spawns a fresh TUI process against the container's
persistent state, so flows that mutate state carry over between reloads.

Requirements: `docker` and `ttyd` on the host. `scripts/tui-e2e.sh down`
removes the container and stops the bridge. The page can be driven manually
or by any browser-automation tool for screenshot-verified flow sweeps.

The rig forces `NO_EMOJI=1`: browser terminals (xterm.js) render some
status glyphs (`✓`, `⚠`, `◷`) two cells wide while Go's width tables count
one, which corrupts frame diffing. ASCII symbol mode removes the mismatch;
native terminals are unaffected.
