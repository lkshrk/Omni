# Test Matrix

This matrix tracks the 11 actual program flows and the 79 user-visible actions
in `internal/actions/catalog.go`. It separates cheap model/render checks from
real-terminal journeys so `TUI: yes` does not imply an expensive binary test for
every action.

This is a contributor release checklist, not an end-user guide. For operational
command risk, use [Command Matrix](command-matrix.md).

Status meanings:

- `yes`: representative happy path and important routing/error behavior exist.
- `partial`: some coverage exists, but a realistic use-case or error branch is missing.
- `gap`: no meaningful coverage at this layer yet.
- `n/a`: this layer intentionally does not expose the action.

Use this as a release checklist. App/shared tests own durable behavior; CLI
integration covers realistic command flows and permutations; real-terminal TUI
tests cover only distinct interaction contracts.

No upstream artifact declares P0-P3 priorities, so every flow remains
`UNKNOWN` rather than assigning invented priorities.

## Layer policy

| Layer | Owns | Runtime lane |
| --- | --- | --- |
| Focused unit/model/contract | Pure branches, parsers, reducers, safety boundaries, provider command contracts, and failure-state diagnostics | Fast; normal PR gate |
| CLI integration | Real config, DB, filesystem, Git, command routing, flags, dry runs, migrations, and feature permutations | Normal PR gate |
| Real-terminal TUI | Startup, navigation, current-screen rendering, modal input, confirmation/cancel, async progress/error recovery, and durable state after UI actions | Eight hermetic journey families; normal PR gate |
| Container integration | Real package managers, privilege behavior, and platform service integration | Release/scheduled lane |
| Static/race/timing | Test isolation, cache behavior, race safety, CI wiring, and runtime regressions | Normal PR gate; do not repeat the unit suite in containers |

## Program-flow coverage

`covered` means the selected layer already has representative evidence.
`partial` names the remaining distinct behavior. `n/a` means another layer is
both cheaper and sufficient.

| ID | Priority | Program flow | Focused unit/contract proof | CLI integration proof | Real-terminal TUI proof | Container/external proof |
| --- | --- | --- | --- | --- | --- | --- |
| TS-01 | UNKNOWN | Bootstrap and host activation | Config migration, DB initialization, host-selection branches | Bootstrap, ensure/copy/delete host, reload active host | covered: first-run wizard creates and activates a host through the dashboard | n/a |
| TS-02 | UNKNOWN | Single-tool lifecycle | Provider resolution, validation, rollback, and failure branches | Set/group/install/list/upgrade/reinstall/delete with persisted config and DB state | covered: fake-provider install plus cancel and confirm delete with config/provider evidence | One representative real package-manager lifecycle |
| TS-03 | UNKNOWN | Bulk sync and prune | Planner, ordering, quarantine, retry, and partial-failure branches | Dry-run, claim, install, prune, failure, and retry permutations | covered: progress and row-level failure/retry are exercised by tool and reconcile journeys | n/a |
| TS-04 | UNKNOWN | Provider routing and fallback | Provider contracts, priority, availability, weak-match, and fallback selection | Disabled/unavailable provider, fallback, switch, and recovery permutations | covered: provider list plus durable fallback editor save | Provider command/privilege contracts |
| TS-05 | UNKNOWN | Reconcile | Step ordering and partial-failure preservation | Tools, dots, and Git plan/execution flows | covered: injected provider failure stays visible, the UI remains usable, and retry persists success | n/a |
| TS-06 | UNKNOWN | Dotfile lifecycle | Classification, path validation, and config mutation branches | Adopt/discover/sync/status/extract/variant/unignore/delete filesystem journeys | covered: discovered sync and ignored-candidate include persist state | GNU Stow remains in the integration environment |
| TS-07 | UNKNOWN | Dotfile safety and services | Conflict detection, nested ignores, rollback, and service-state branches | Conflict resolution, pull/commit/push, reminder, watch, and data-preservation journeys | covered: destructive resolution is cancelled, then confirmed with backup/symlink filesystem proof | Platform service checks only where host isolation is available |
| TS-08 | UNKNOWN | Hosts, groups, settings, and migration | Migration and unrelated-config preservation | Mutations persist across reload; lint/extract/migration flows | covered: host-group assignment and a setting toggle persist through config reload | n/a |
| TS-09 | UNKNOWN | Agents, skills, MCP, plugins, and marketplaces | Adapter parsing, identity, status, and feature-gate contracts | Add/import/group/remove/sync/upgrade for each resource family | covered: fake-Claude plugin install persists adapter and config state; real-Git fixture covers versionless PATH-outdated; skill outdated detection covered per source type (local dir, Git branch, pinned tag, well-known index) with a request-counting stub for the recheck cadence | Fake agent CLIs, real local Git fixture, and a local HTTP index stub; no network |
| TS-10 | UNKNOWN | TUI shell and parity | Reducer branches, layout, key routing, modal, progress, and error state | n/a | covered: `x/vttest` current-screen checks exercise resize, help/search, cancel/confirm, async failure/recovery, nested PTY, and clean quit | n/a |
| TS-11 | UNKNOWN | Provider families | Install/query/upgrade/uninstall parsing and command contracts | Routing through fake executors | n/a: provider permutations do not become safer through the TUI | Tagged Docker package-manager lane |

## Real-terminal TUI target

These eight journey families are the complete binary-TUI budget. They cover
interaction shapes, not every catalog action. Supporting variations share the
same binary and harness. A new family should replace or consolidate an existing
one unless it proves a new interaction contract.

| Journey | User path and durable assertion | Current evidence | State |
| --- | --- | --- | --- |
| TUI-01 First run | Bootstrap wizard -> activate host -> dashboard; config reload sees the same host | `TestTUIFirstRunCreatesHostAndReachesDashboard` | covered |
| TUI-02 Shell navigation | Launch -> navigate -> search/help -> resize -> clean quit; current screen remains valid | `TestTUIConfiguredHostStartsDashboard`, current-screen overwrite regression | covered |
| TUI-03 Tool mutation | Edit fallback -> install with fake provider -> progress -> persisted state -> cancel then confirm delete | Durable fallback save and fake-Brew lifecycle tests | covered |
| TUI-04 Reconcile recovery | Open plan -> run failure -> retain error -> retry -> durable success | Injected fake-Brew failure/retry plus dot-ignore reconcile | covered |
| TUI-05 Dot safety | Discover/sync candidate -> conflict -> cancel -> confirm resolution; verify config and filesystem | Candidate include/sync and conflict backup/symlink tests | covered |
| TUI-06 Agents mutation | Render/filter -> install missing plugin; verify adapter/config state and versionless PATH-outdated classification | Fake-Claude install and real-Git PATH-outdated tests | covered |
| TUI-07 Groups/settings | Assign current host group -> toggle one setting -> reload config -> verify persistence | `TestTUIAssignsHostGroupAndPersistsSetting` | covered |
| TUI-08 Admin terminal | Run fake privileged command -> exchange input/output -> observe completion/dismissal without corrupting the parent UI | Real nested PTY plus component-level nonzero-exit coverage | covered |

## TUI runtime controls

- Build `omni` once per integration package; every journey launches that binary.
- Give every journey its own HOME, config, cache, repository, PATH, and fake executors.
- Use `x/vttest`'s current emulator screen; no fixed key delays or accumulated ANSI-history assertions.
- Run isolated sessions with CI `-parallel 4`; keep race shards separate.
- Use no network or real package manager in the PR TUI lane.
- Target two seconds per transition and ten seconds per journey, but measure before enforcing a suite budget.
- Assert semantic text/cells and durable state. Use full-screen goldens only when layout itself is the behavior.
- On failure retain the current screen, command/fake-executor log, config, and DB diagnostics.

## Action-level coverage

The detailed action table below records representative app, CLI, and TUI
model/render evidence. Its TUI column does **not** require a separate
real-terminal test; the eight-journey budget above owns that layer.

| Action ID | App/shared | CLI unit | CLI integration | TUI model/render | Gap/next fixture |
| --- | --- | --- | --- | --- | --- |
| `reconcile` | yes | yes | yes | yes | - |
| `tools.sync` | yes | yes | yes | yes | - |
| `tools.install` | yes | yes | yes | yes | - |
| `tools.delete` | yes | yes | yes | yes | CLI command is `tools remove --purge`; `delete` remains a deprecated alias |
| `tools.update` | yes | yes | yes | yes | - |
| `tools.update_all` | yes | yes | yes | yes | - |
| `tools.sync_all` | yes | yes | yes | yes | - |
| `tools.claim` | yes | yes | yes | yes | - |
| `tools.ignore` | yes | yes | yes | yes | - |
| `tools.change_group` | yes | yes | yes | yes | - |
| `tools.pin_provider` | yes | yes | yes | yes | - |
| `tools.reinstall_default` | yes | yes | yes | yes | - |
| `tools.migrate_nvm` | yes | yes | yes | yes | - |
| `tools.refresh` | yes | yes | yes | yes | - |
| `tools.consolidate` | yes | yes | yes | yes | - |
| `tools.set_spec` | yes | yes | yes | yes | - |
| `tools.fallback` | yes | yes | yes | yes | Covers config save, TUI edit/render, native-unavailable sync/install, native recovery after failed fallback, retry-failed, upgrade, and uninstall-unavailable routing. |
| `tools.delete_spec` | yes | yes | yes | yes | - |
| `tools.normalize_provider_overrides` | yes | yes | yes | n/a | - |
| `tools.heal_brew_taps` | yes | partial | yes | n/a | CLI integration covers dry-run healing. |
| `tools.baseline_system_inventory` | yes | yes | gap | n/a | Add a CLI integration fixture with a fake system provider inventory. |
| `tools.import` | yes | yes | yes | yes | - |
| `tools.switch_provider` | yes | yes | yes | n/a | - |
| `dots.sync` | yes | yes | yes | yes | - |
| `dots.refresh` | yes | yes | yes | yes | - |
| `dots.add` | yes | yes | yes | yes | - |
| `dots.edit_groups` | yes | yes | yes | yes | - |
| `dots.variant` | yes | yes | yes | yes | - |
| `dots.delete` | yes | yes | yes | yes | CLI command is `dots remove`; `delete` remains a deprecated alias |
| `dots.resolve_use_repo` | yes | yes | yes | yes | - |
| `dots.resolve_use_local` | yes | yes | yes | yes | - |
| `dots.resolve_all_use_repo` | yes | yes | yes | yes | - |
| `dots.resolve_all_use_local` | yes | yes | yes | yes | - |
| `dots.ignore` | yes | yes | yes | yes | - |
| `dots.enable` | yes | yes | yes | yes | - |
| `dots.disable` | yes | yes | yes | yes | - |
| `dots.pull` | yes | yes | yes | n/a | - |
| `dots.commit` | yes | yes | yes | yes | - |
| `dots.push` | yes | yes | yes | n/a | - |
| `dots.reminder` | yes | yes | yes | yes | - |
| `dots.reminder.check` | yes | yes | yes | n/a | - |
| `dots.reminder.run` | yes | yes | yes | n/a | - |
| `dots.reminder.status` | yes | yes | yes | n/a | - |
| `dots.watch` | yes | yes | yes | yes | - |
| `dots.watch.run` | yes | yes | yes | n/a | - |
| `dots.watch.status` | yes | yes | yes | n/a | - |
| `dots.services.status` | yes | yes | yes | yes | - |
| `dots.history` | yes | yes | yes | yes | - |
| `groups.create` | yes | yes | yes | yes | - |
| `groups.rename` | yes | yes | yes | yes | - |
| `groups.delete` | yes | yes | yes | yes | - |
| `groups.edit_tools` | yes | yes | yes | yes | - |
| `groups.edit_dots` | yes | yes | yes | yes | - |
| `hosts.create` | yes | yes | yes | n/a | - |
| `hosts.copy` | yes | yes | yes | yes | TUI coverage is onboarding-only |
| `hosts.delete` | yes | yes | yes | yes | - |
| `hosts.edit_groups` | yes | yes | yes | yes | - |
| `settings.set` | yes | yes | yes | yes | - |
| `settings.provider` | yes | yes | yes | yes | - |
| `settings.reset` | yes | yes | yes | yes | - |
| `settings.reset_cache` | yes | yes | yes | yes | - |
| `settings.migrate_host_overrides` | yes | n/a | n/a | n/a | CLI-only config migration |
| `settings.extract` | yes | n/a | n/a | n/a | CLI-only config layout migration |
| `setup.init` | yes | yes | yes | yes | CLI command is `bootstrap`; `init` remains an alias |
| `agents.restore` | yes | yes | yes | yes | CLI command is `agents sync`; `restore` remains a deprecated alias |
| `agents.sync_all` | yes | yes | yes | yes | - |
| `agents.skills_import` | yes | yes | yes | yes | - |
| `agents.skills_update` | yes | yes | yes | yes | CLI command is `agents skills upgrade`; `update` remains a deprecated alias |
| `agents.skills_resolve_use_managed` | yes | yes | yes | yes | - |
| `agents.skills_resolve_use_local` | yes | yes | yes | yes | - |
| `agents.mcp_resolve_use_managed` | yes | yes | yes | yes | - |
| `agents.mcp_resolve_use_local` | yes | yes | yes | yes | - |
| `agents.plugins_resolve_use_managed` | yes | yes | yes | yes | - |
| `agents.plugins_resolve_use_local` | yes | yes | yes | yes | - |
| `agents.resolve_all_use_managed` | yes | yes | yes | yes | - |
| `agents.resolve_all_use_local` | yes | yes | yes | yes | - |
| `agents.skills_status` | yes | yes | yes | n/a | Read-only CLI view; the TUI shows the same state in the agents rows. |
| `doctor` | yes | yes | yes | yes | - |
| `doctor.fix` | yes | yes | yes | yes | Covers include-chain dedupe, dry-run, catalog routing, TUI execution, and doctor refresh. |
