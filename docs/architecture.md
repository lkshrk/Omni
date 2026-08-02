# Architecture

Omni has one application boundary shared by the CLI and TUI. User-visible
behavior should live behind that boundary first, then each surface should wire
the same operation.

## Module Map

| Area | Package | Responsibility |
| --- | --- | --- |
| CLI | `cmd/omni`, `internal/cli` | Cobra commands, flags, confirmation prompts, shell completion. |
| TUI | `internal/tui` | Bubble Tea models, views, key handling, modal flows, admin terminal UX. |
| App boundary | `internal/app` | High-level use cases used by both CLI and TUI. |
| Agent targets | `internal/agent` | Compiled target registry, native skill acquisition/inventory, MCP adapters, and plugin adapters. |
| Config | `internal/config` | JSON schema types, migrations, normalization, validation, patch writes. |
| Cache | `internal/database` | SQLite persistence for discovered tool and dotfile state. |
| Providers | `internal/provider` | Package manager interfaces and concrete implementations. |
| Tool sync | `internal/sync` | Resolve desired tools, install missing tools, prune, privilege handling. |
| Dotfiles | `internal/dots` | GNU Stow operations, adoption, conflict detection, backups. |
| Actions | `internal/actions` | Shared action metadata used by confirmation and reconcile flows. |

## Runtime Shape

```text
CLI command or TUI key
        |
        v
internal/app operation
        |
        +-- config load/patch/validate
        +-- provider registry
        +-- SQLite cache
        +-- Agent Target registry and native skills service
        +-- syncer
        +-- dots engine
```

The CLI and TUI should not implement separate business rules. They should call
the same app operation and differ only in presentation, prompts, and navigation.

## Agent Skills Lifecycle

```text
agents.packages desired state
        |
        v
active groups + target resolution + plugin shadow checks (internal/app)
        |
        v
Git / well-known HTTP / local acquisition (internal/agent)
        |
        v
temporary validation and atomic canonical replacement
        |
        v
target-specific links + machine-local inventory metadata
```

Discovery over an acquired source looks at the conventional skill containers
(`skills/`, `.agents/skills/`, per-target skill directories, and similar), and
additionally harvests any `skills` paths declared by a plugin manifest —
`.claude-plugin/plugin.json`, `.claude-plugin/marketplace.json`, or
`.plugin/plugin.json` — so plugin-shaped repositories resolve without a
bespoke layout. A directory whose `SKILL.md` frontmatter is unusable is
skipped with a warning instead of failing the package.

`agents.packages` is the only durable skill manifest. Omni stores canonical
content under the XDG data directory and records hashes/check times in local
SQLite state. Both are recoverable from settings and source acquisition.
Legacy `.skill-lock.json` is read only for explicit unmanaged import/adopt; it
is never updated or deleted. The skills.sh search catalog is independent of
sync, upgrade, remove, and inventory.

## Startup

Normal command startup:

1. Resolve config path.
2. Initialize `App`.
3. Resolve cache directory and open `omni.db`.
4. Normalize and validate config.
5. Write a best-effort `settings.json.bak`.
6. Repair the current host entry when needed.
7. Initialize provider registry from effective settings.
8. Enforce active host for host-scoped commands.

`doctor` uses a read-only startup path so it can diagnose broken state without
normalization, host repair, or cache migrations changing the machine first.

## Host Gate

Most tool commands require an active host because Omni needs to expand host and
group membership before deciding desired state.

Commands that can run before host setup include:

- `bootstrap`
- `doctor`
- `hosts`
- `dots`
- `ui`
- `settings`
- `help`
- `--version`
- `completion`

Tool commands can also bypass active-host expansion when they explicitly target
a group with `--group`, or when a command exposes a deliberate `--force` path.

## Provider Registry

At startup, Omni registers the concrete package managers listed in
[Providers](providers.md#provider-types). Registry metadata records each
provider's package family, display order, manager options, and default install
order. The legacy provider-family names `system`, `node`, and `python` remain
internal compatibility metadata for old configs and consolidation flows, but
current tool config stores concrete providers in `tools[].providers[]`.

## Tool Lifecycle

```text
settings.json logical tools
        |
        v
active host + reusable groups
        |
        v
resolved desired tool specs
        |
        v
provider installed/outdated scan
        |
        v
sync, install, upgrade, prune, or mark ignored
        |
        v
cache rows refreshed with observed state
```

Important invariants:

- groups store logical tool names, not full install objects
- the `tools` map stores install specs for logical tools
- `providers[].package` defaults to the logical tool name
- tool providers are concrete provider candidates
- cache `installed_with` records observed ownership

## Dotfile Lifecycle

```text
settings.json dot entry
        |
        v
active host package selection
        |
        v
Stow package under dots repo
        |
        v
target path in HOME
        |
        v
symlink health, conflict, or missing-source status
```

Dotfiles are Stow-package based. Omni should not treat arbitrary symlinks as
equivalent to a managed Stow link. When local content would be overwritten,
conflict resolution requires an explicit `--use-repo` or `--use-local` choice.

## Reconcile

`omni reconcile` is the broad repair path. It can:

- sync missing configured tools
- claim discovered installed tools into the current machine group
- upgrade configured tools
- import and restore agent plugins, skills, and MCP servers in dependency order
- repair dotfile links
- commit dotfile repo changes

Because it crosses multiple state boundaries, run `doctor` first when you need a
read-only diagnosis.

## Design Rule For Contributors

When adding a user-visible capability:

1. Implement the durable behavior in `internal/app`.
2. Add focused app-level tests for config/cache/provider effects.
3. Wire CLI flags or subcommands to the app operation.
4. Wire the TUI action to the same app operation.
5. Keep CLI/TUI tests focused on routing, prompts, key handling, and rendering.

This keeps CLI and TUI behavior aligned and avoids TUI-only mutations.
