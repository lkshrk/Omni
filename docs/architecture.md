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
        +-- syncer
        +-- dots engine
```

The CLI and TUI should not implement separate business rules. They should call
the same app operation and differ only in presentation, prompts, and navigation.

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

At app startup, Omni registers concrete providers first:

- `brew`
- `apt`
- `apk`
- `dnf`
- `pacman`
- `zypper`
- `pip`

Then it registers enabled provider families:

- `system`, backed by available concrete system managers
- `node`, backed by the configured Node manager
- `python`, backed by the configured Python manager

Provider metadata records whether a provider is an ecosystem or concrete
provider, which ecosystem a concrete provider belongs to, display order, manager
options, and default install order.

The concrete Python provider is registered as `pip`, while the executable,
settings value, and observed manager normally use `pip3`. `pip` remains accepted
as an alias and is normalized to `pip3` where manager settings are persisted.

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
