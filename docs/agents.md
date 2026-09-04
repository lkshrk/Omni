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

## Migrating a pre-APM host

Migration is an explicit operator flow; the TUI only inspects. Run `omni agents
migrate --host <name>` and review the printed manifest, commit it into that
host's template in dotfiles, delete the native entries it replaces by hand, then
run `omni agents sync`. A missing or off-pin APM build is repaired by `omni
doctor --fix`, never by opening the TUI. Ambiguous or local bundle evidence is
never guessed; Omni leaves it unchanged and asks for review.

`omni agents migrate --host <name>` previews the apm.yml equivalent of the agent
declarations a host had before the migration, or of the live native Claude and
Codex state when no snapshot is present. Preview is the default; `--dry-run` is
an explicit alias. Both print a deterministic plan, write nothing, and run no
APM command.

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
`settings.json`. With no such directory the preview covers native state only.
Pass `--snapshot <dir>` when there is more than one, or to point at another.

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

With `--write`, Omni snapshots each selected legacy owner into one
content-addressed local APM package under:

```text
<Omni state>/agents-migration/bundles/<sha256>/
```

The package preserves supported source metadata and runtime files while
rebasing relative paths. Migration is offline and non-executing; unsafe paths,
literal secrets, unreadable files, ambiguous ownership, or unsupported native
behavior block the whole write. Rerun `--write` after the snapshot changes;
old hashes remain available until no live manifest references them.

### Lifecycle handoff

`omni agents sync` is the only command that materializes the live manifest and
invokes APM. Migrate and sync share the template/workspace locks and recheck the
candidate before mutation, so a concurrent edit fails before the live manifest
or APM changes. Do not run `apm` directly in parallel with sync; external APM
processes do not participate in Omni's lock.

> **Known APM 0.29.0 limitation:** a global `apm audit --ci` may falsely
> report managed `.agents/**` files as missing or unintegrated because audit
> can resolve primitive deployment paths from `~/.apm` instead of the global
> deployment root. Install and runtime behavior are unaffected. Until APM fixes
> the path resolution, use `omni agents sync` plus `omni doctor` for global
> verification. Project-scoped APM audits remain supported. If CI must run the
> global audit, allow only these exact `.agents/**` findings after independently
> confirming the expected files exist; every other audit finding must still
> fail. Do not duplicate the files under `~/.apm/.agents`.

## APM Main Build

Omni requires APM `0.29.0` from the upstream `microsoft/apm` `main` branch.
Installers use this source specification:

```text
git+https://github.com/microsoft/apm.git@main
```

The `lkshrk/apm` fork Omni previously required is retired. Its capabilities are
recovered by declaring every agent package as a git dependency: git deps accept
per-dependency `targets:` natively, resolve legacy package roots through an
explicit `path:` coordinate, and support `--dry-run`.

Upgrading APM means rerunning Omni's APM contract and integration suites against
the current upstream `main` build before changing Omni's required version.
