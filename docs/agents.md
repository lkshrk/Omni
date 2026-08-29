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
store. Migration creates one verified content-addressed local APM wrapper for
every selected legacy owner. Those wrappers are APM inputs, not a second
deployment store. APM owns deployment, audit, and lifecycle state.

## Host template

A host may keep its desired manifest as a template at `omni/apm.yml` under the
OS user config directory — `~/.config/omni/apm.yml` on Linux. Sync copies that
template over `~/.apm/apm.yml` before invoking APM install, so the manifest is
a dotfile-managed input rather than something Omni edits field by field.

The template is optional. With no template file, sync installs the live
manifest unchanged.

`omni agents migrate --write` creates a migration-owned template whose first
line is exactly:

```yaml
# omni:agents-migration:v1
```

It can replace a missing template or regenerate a template whose first
non-empty line is that marker. It refuses to overwrite an unmarked, symlinked,
or non-regular template.

Because the template overwrites the live manifest, sync refuses to apply it
silently over work APM or the operator did directly:

- First sync with a template and an existing live manifest warns
  `first sync with a template: verify it matches the live manifest, then
  re-run with --force-template to adopt it`. Compare the two, then adopt the
  template with `omni agents sync --force-template`.
- After adoption, Omni records the live manifest's hash under
  `agents-template-state` in its state directory. A later sync that finds the
  live manifest changed outside Omni warns that it `diverged from last sync`
  and leaves it alone until the next `--force-template`.
- `--dry-run` never materializes the template.

`omni sync` runs the same template step but has no `--force-template` flag; run
`omni agents sync --force-template` for the adoption or override step.

## Package-owned MCP and LSP

A package is authoritative for every MCP or LSP child declared by its installed
`apm.yml`. Omni compares package children with top-level `dependencies.mcp` and
`dependencies.lsp` entries by kind, case-insensitive name, and a canonical
fingerprint. Same-name entries are not enough to prove equivalence.

| State | Behavior |
| --- | --- |
| One package owner, no standalone entry | The child belongs to the package. |
| One owner and an identical standalone fingerprint | The standalone entry is an exact duplicate; `omni doctor --fix` can remove it from the canonical template. |
| One owner and a different standalone fingerprint | Sync blocks. Doctor reports the differing field names without values and preserves both declarations. |
| Multiple package owners | Sync blocks as ambiguous. Doctor never chooses an owner. |
| No package owner | The standalone or unmanaged child remains independent. |

Fingerprint comparison covers the supported command, arguments, working
directory, URL, transport, environment/header names, and LSP fields. It does
not expand environment values or print secret values. Package-relative paths
are normalized only when the installed package root proves their meaning.

Ownership evidence comes from installed package manifests under
`~/.apm/apm_modules/`. On a first install, a remote package may not have local
manifest evidence yet. A package-only template may proceed, but a template that
also declares standalone MCP/LSP entries blocks before the live manifest or APM
changes. Install the package without the standalone entries first, then add
only services the package does not provide.

An installed `apm.yml` remains the normal ownership source. Omni accepts a
missing manifest as proof that a package provides no MCP/LSP children only for
the pinned APM package types `claude_skill` and `skill_bundle`, and only when
all of these facts are proven:

- one installed lock entry identifies the package, with a resolved commit and
  a non-empty deployed-file inventory;
- every deployed file is under `.agents/skills/<name>/` or
  `.claude/skills/<name>/`;
- each inventoried skill has its canonical regular `SKILL.md` in the installed
  module; and
- `apm.yml` and every plugin, MCP, and LSP carrier recognized by pinned APM
  0.29.0 are absent.

This recognizes `sopaco/deepwiki-rs` as a manifestless `skill_bundle` and the
`shiplight` virtual package from `ShiplightAI/agent-skills-v2` as a
manifestless `claude_skill`. Both have an empty `provides` list. A standalone
`shiplight` MCP remains an independent top-level service; matching a package
name never proves ownership.

Unknown, plugin, hybrid, mixed, malformed, unreadable, symlinked, incomplete,
or changed evidence remains unavailable and keeps the existing fail-closed
sync and Doctor behavior. This proof is Omni-side only: it does not change APM,
the TUI, update checks, package versions, or row layout.

`omni doctor` reports package-owned duplicates, conflicts, ambiguities, and
unavailable ownership evidence. `omni doctor --fix --dry-run` previews exact
duplicate removals without writing. `omni doctor --fix` edits only the
canonical host template, preserves a template symlink and its target mode, and
prints `omni agents sync` as the next step. It never edits the live manifest,
lockfile, installed module manifests, package cache, or client configuration.

The fixer locks the canonical template and then the global APM workspace. It
hashes the template, live manifest, lockfile, and installed module manifests
used for classification and rechecks them before atomic replacement. If any
input, symlink component, or target identity changes, repair refuses to write.

Automatic repair is deliberately narrow. It removes exact source-byte ranges
only from unambiguous block-style `dependencies.mcp` or `dependencies.lsp`
sequences. Conflicts, multiple owners, flow-style sequences, anchors, aliases,
merged mappings, same-line sibling content, ambiguous comment ownership, and
unsafe symlink/source layouts are reported but left byte-for-byte unchanged. If
any exact candidate has an unsupported source layout, the fixer removes none.

The current `context-mode` standalone MCP uses `command: node` with
`args: [./start.mjs]`, which does not fingerprint identically to the child
provided by `mksglu/context-mode`. Doctor therefore refuses to remove it. Keep
the `mksglu/context-mode` package, delete only the top-level `context-mode` item
from `dependencies.mcp` in the canonical host template, then run:

```sh
omni doctor
omni agents sync
```

Do not edit `~/.apm/apm.yml` directly when a host template exists; the next
sync would replace that edit.

## Migrating a pre-APM host

`omni agents migrate --host <name>` previews the apm.yml equivalent of the agent
declarations a host had before the migration. Preview is the default;
`--dry-run` is an explicit alias. Both print a deterministic plan, write
nothing, and run no APM command.

```sh
omni agents migrate --host workstation
omni agents migrate --host workstation --write
omni agents sync
```

`--write` and `--dry-run` are mutually exclusive. `--write` publishes any
required wrappers, then atomically updates only the canonical host template. It
does not write `~/.apm/apm.yml` or run APM. On success it prints
`Next: omni agents sync`.

By default Omni resolves the loaded config path through its symlink and looks
for a single `.omni-apm-migration-backup-*` directory next to the real
`settings.json`. Pass `--snapshot <dir>` when there is no such directory or
more than one.

Only groups active for that host are planned: the host's assigned groups plus a
group named after the host itself. Omni resolves selected package/plugin owners
from copied snapshot evidence in this order: an explicit install/source path,
a copied source path, then one selected marketplace and one matching copied
catalog root. A name alone never proves ownership. Missing or ambiguous
evidence blocks the whole migration before any wrapper or template is written.

The snapshot's v22 fields map like this:

| Snapshot field | Rendered as |
| --- | --- |
| `agents.plugins`, selected by a group's `plugins` | One `dependencies.apm` owner entry pointing at a verified content-addressed local wrapper. |
| `agents.packages`, selected by a group's `skills` | One wrapper owner entry, unless the package is proven to be a skill already owned by a selected plugin. |
| `agents.mcp_servers`, selected by a group's `mcp_servers` | An independent `dependencies.mcp` entry, or suppression when its owner path and normalized definition exactly match a selected bundle child. |
| `agents.marketplaces`, selected by a group's `marketplaces` | Trailing `# apm marketplace add <source> --name <name>` comment; apm.yml cannot express marketplaces. |
| An entry's `agents` list | `targets:` on the apm entry, with `claude-code` renamed to `claude`. The union of every entry's targets becomes the manifest's top-level `targets:`. |
| `${VAR}` in any value | `${env:VAR}`, the only placeholder form APM expands. |

Omni inventories conventional skills, hooks, agents, commands, executable
binaries, MCP, and LSP definitions beneath each proven owner root. Exact
owner/fingerprint matches collapse into the owner dependency; unrelated
standalone declarations survive. Preview records each collapse as a
`# suppressed:` comment. Different definitions, a child claimed by multiple
owners, duplicate owner identities, incomplete runtime paths, and unsupported
native behavior produce deterministic blockers that name the declaration and
field without printing sensitive values.

MCP entries deliberately carry no per-entry `targets:`. The MCP surface is
host-global, so every declared server reaches every enabled user-global MCP
target regardless of which agents originally declared it.

The rendered marketplace commands are comments because APM registers
marketplaces outside the manifest. Sync reads them back: every declared
marketplace missing from `~/.apm/marketplaces.json` is registered before the
install step, so a fresh host needs no manual `apm marketplace add`. Sync only
adds — dropping a comment line never unregisters a marketplace, which stays a
manual `apm marketplace remove`.

### Verified wrappers

Omni snapshots every selected owner into one local package at:

```text
<Omni state>/agents-migration/bundles/<sha256>/
  apm.yml
  runtime/...
  skills/...
  hooks/...
  agents/...
  commands/...
```

The SHA-256 covers the normalized manifest plus every copied file's destination,
content, and executable mode. Wrapper preparation uses private temporary
directories and atomic rename. An existing hash directory must match exactly or
migration stops as corruption.

Source `apm.yml` packages are wrapped too. Omni preserves supported manifest
metadata and dependency semantics while rebasing bundle-relative paths into the
wrapper. Unsupported native extensions, dependency forms, or other behavior
that cannot be represented losslessly blocks the owner instead of being
silently dropped.

Migrate never deletes stale wrapper hashes. The live manifest may still point
at the previous template's hash until `omni agents sync` finishes, so old hashes
remain safe inputs. Rerun `omni agents migrate --host <name> --write` after the
copied source snapshot changes to publish and select the refreshed wrapper.

Migration is offline and non-executing. It does not read `apm.lock.yaml`, the
live APM package cache, the network, or APM source, and it does not expand
environment variables or run bundle commands, hooks, package managers, or
clients. It rejects traversal, absolute or NUL-containing child paths,
symlinked/escaping roots, special files, unreadable runtime files, and missing
runtime references. Literal values in authorization, cookie, token, secret,
password, or API-key header/environment fields block migration; symbolic
`${VAR}` and `${env:VAR}` references remain symbolic.

The scan is capped at 256 owners, 4,096 filesystem entries and 512 MiB of
runtime data per owner, depth 32, 1 MiB per manifest/config, 16 MiB total
manifest/config data, 64 MiB per runtime file, and 1 GiB total runtime data.

### Lifecycle handoff

`omni agents sync` is the only command that materializes the live manifest and
invokes APM. Migrate and sync serialize on the same canonical-template lock, so
a sync cannot observe a partially published migration.

Sync performs package-child ownership preflight for marked and unmarked
templates. It acquires the canonical-template lock, then the global APM
workspace lock; under both locks it hashes and parses the candidate template,
reads installed package manifests, classifies duplicates/conflicts/ambiguities,
and rechecks the inspected identities and candidate hash. The exact candidate
bytes that passed preflight are the bytes materialized; a concurrent template
edit makes sync refuse instead. Preflight also retains the migration marker,
owner identity, wrapper path, content hash, file mode, and symlink-boundary
checks. Any failure happens before writing `~/.apm/apm.yml`, registering a
marketplace, or invoking APM. Dry-run uses the same preflight.

Both locks remain held through APM completion for Omni-mediated operations.
Running `apm` directly at the same time as `omni agents sync` is unsupported:
external APM processes do not honor Omni's workspace lock. APM itself is
unchanged and remains the runtime owner after preflight succeeds.

The isolated migration lifecycle smoke covers preview, write, sync/install,
bundled MCP execution, reinstall, and uninstall.

> **Known APM 0.29.0 limitation:** a global `apm audit --ci` may falsely
> report managed `.agents/**` files as missing or unintegrated because audit
> can resolve primitive deployment paths from `~/.apm` instead of the global
> deployment root. Install and runtime behavior are unaffected. Until APM fixes
> the path resolution, use `omni agents sync` plus `omni doctor` for global
> verification. Project-scoped APM audits remain supported. If CI must run the
> global audit, allow only these exact `.agents/**` findings after independently
> confirming the expected files exist; every other audit finding must still
> fail. Do not duplicate the files under `~/.apm/.agents`.

## Pinned APM Build

Omni requires APM `0.29.0`, built from the immutable upstream `microsoft/apm`
commit
[`b75a02b1cfab3ffa5e1952916045b6d5374090ae`](https://github.com/microsoft/apm/commit/b75a02b1cfab3ffa5e1952916045b6d5374090ae).
Installers use this exact source specification:

```text
git+https://github.com/microsoft/apm.git@b75a02b1cfab3ffa5e1952916045b6d5374090ae
```

The `lkshrk/apm` fork Omni previously required is retired. Its capabilities are
recovered by declaring every agent package as a git dependency: git deps accept
per-dependency `targets:` natively, resolve legacy package roots through an
explicit `path:` coordinate, and support `--dry-run`.

Upgrading APM means rerunning Omni's APM contract and integration suites against
the candidate build and moving the pin to that build's immutable commit. Do not
switch to a floating branch or an untested release.
