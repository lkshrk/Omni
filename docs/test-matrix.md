# Test Matrix

This matrix tracks the 107 declared product flows and the 78 user-visible actions
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

<!-- BEGIN GENERATED FLOW CATALOG -->
| Flow ID | Criticality | Surfaces | Parity | Requirements | Gaps | Evidence |
| --- | --- | --- | --- | --- | --- | --- |
| `agents.add` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsAddDelegatesPackageAndSkillsToAPM`; integration: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsAddDelegatesPackageAndSkillsToAPM`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIAgentsAddProduceEquivalentAPMState`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIAgentsAddProduceEquivalentAPMState` |
| `agents.audit` | medium | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsRemainingCommandsDelegateToAPM/agents.audit`; integration: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsRemainingCommandsDelegateToAPM/agents.audit` |
| `agents.deps.info` | medium | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsRemainingCommandsDelegateToAPM/agents.deps.info`; integration: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsRemainingCommandsDelegateToAPM/agents.deps.info` |
| `agents.deps.list` | medium | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsRemainingCommandsDelegateToAPM/agents.deps.list`; integration: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsRemainingCommandsDelegateToAPM/agents.deps.list` |
| `agents.deps.why` | medium | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsRemainingCommandsDelegateToAPM/agents.deps.why`; integration: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsRemainingCommandsDelegateToAPM/agents.deps.why` |
| `agents.marketplace.add` | high | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsRemainingCommandsDelegateToAPM/agents.marketplace.add`; integration: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsRemainingCommandsDelegateToAPM/agents.marketplace.add` |
| `agents.marketplace.remove` | high | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsRemainingCommandsDelegateToAPM/agents.marketplace.remove`; integration: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsRemainingCommandsDelegateToAPM/agents.marketplace.remove` |
| `agents.marketplace.update` | high | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsRemainingCommandsDelegateToAPM/agents.marketplace.update`; integration: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsRemainingCommandsDelegateToAPM/agents.marketplace.update` |
| `agents.marketplaces.browse` | medium | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsRemainingCommandsDelegateToAPM/agents.marketplaces.browse`; integration: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsRemainingCommandsDelegateToAPM/agents.marketplaces.browse` |
| `agents.marketplaces.list` | medium | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsRemainingCommandsDelegateToAPM/agents.marketplaces.list`; integration: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsRemainingCommandsDelegateToAPM/agents.marketplaces.list` |
| `agents.marketplaces.validate` | medium | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsRemainingCommandsDelegateToAPM/agents.marketplaces.validate`; integration: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsRemainingCommandsDelegateToAPM/agents.marketplaces.validate` |
| `agents.migrate` | high | CLI | — | cli_blackbox, integration, unit | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsMigratePreviewDoesNotWriteState`; cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsMigrateWritePublishesTemplateAndWrapper`; integration: `github.com/lkshrk/omni/internal/app.TestAgentsMigrationRealAPMLifecycle`; unit: `github.com/lkshrk/omni/internal/app.TestAgentsMigrateWriteCreatesOnlyMarkedTemplate`; unit: `github.com/lkshrk/omni/internal/app.TestAgentsMigrateWriteRefusesUnmarkedTemplate`; unit: `github.com/lkshrk/omni/internal/app.TestAgentsMigrateWriteRejectsTemplateSymlink` |
| `agents.prune` | high | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsRemainingCommandsDelegateToAPM/agents.prune`; integration: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsRemainingCommandsDelegateToAPM/agents.prune` |
| `agents.refresh` | medium | CLI+TUI | query | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsRefreshDelegatesOutdatedQuery`; integration: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsRefreshDelegatesOutdatedQuery`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIAgentsRefreshProduceEquivalentAPMState`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIAgentsRefreshRunsAnotherOutdatedCheck` |
| `agents.remove` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsRemoveDelegatesEveryPackageToAPM`; integration: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsRemoveDelegatesEveryPackageToAPM`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIAgentsRemoveProduceEquivalentAPMState`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIAgentsRemoveProduceEquivalentAPMState` |
| `agents.search` | medium | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsRemainingCommandsDelegateToAPM/agents.search`; integration: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsRemainingCommandsDelegateToAPM/agents.search` |
| `agents.sync` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox, unit | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsSyncDeploysMCPThroughRealAPM`; integration: `github.com/lkshrk/omni/integration_tests.TestTUIAgentsTabSyncsMCPThroughRealAPM`; integration: `github.com/lkshrk/omni/internal/app.TestAgentsManifestlessRealAPMLifecycle`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIAgentsSyncProduceEquivalentAPMState`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIAgentsTabSyncsMCPThroughRealAPM`; unit: `github.com/lkshrk/omni/internal/app.TestAgentsSyncAllBlocksOwnedExactDuplicateBeforeMutation`; unit: `github.com/lkshrk/omni/internal/app.TestAgentsSyncAllRejectsInvalidUnmarkedTemplateBeforeMutation`; unit: `github.com/lkshrk/omni/internal/app.TestAgentsSyncAllRunsOneGlobalAPMInstall` |
| `agents.targets` | medium | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsRemainingCommandsDelegateToAPM/agents.targets`; integration: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsRemainingCommandsDelegateToAPM/agents.targets` |
| `agents.update` | high | TUI | — | integration, tui_blackbox | — | integration: `github.com/lkshrk/omni/integration_tests.TestTUIAgentsUpdateInvokesSelectedPackage`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIAgentsUpdateInvokesSelectedPackage` |
| `agents.update_all` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsUpdateAllDelegatesGlobalConfirmationToAPM`; integration: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryAgentsUpdateAllDelegatesGlobalConfirmationToAPM`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIAgentsUpdateAllProduceEquivalentAPMState`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIAgentsUpdateAllProduceEquivalentAPMState` |
| `doctor` | medium | CLI+TUI | query | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDoctorReportsPinnedAPM`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/doctor`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIReadOnlyQueriesProduceEquivalentSemanticObservations`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIReadOnlyQueriesProduceEquivalentSemanticObservations` |
| `doctor.fix` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDoctorDryRunPreservesConfig`; cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDoctorFixRemovesOnlyDuplicateDefinition`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/doctor-fix`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIDoctorFixProduceEquivalentSemanticState`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIDoctorFixProduceEquivalentSemanticState` |
| `dots.add` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | parity (Stage 6); tui_blackbox (Stage 5) | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDotsEntryLifecyclePersistsFilesGroupsAndHistory`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/dots-add-no-repo`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/dots-add-success` |
| `dots.commit` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDotsCommitPersistsGitCommitAndHistory`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/dots-commit`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIDotsGitCommitProduceEquivalentSemanticState`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIDotsCommitPersistsLocalGitCommitWithoutPush` |
| `dots.delete` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDotsEntryLifecyclePersistsFilesGroupsAndHistory`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/dots-remove-purge`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/dots-remove-success`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIDotsLifecycleDeleteProduceEquivalentSemanticState`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIDotsDeleteKeepsLocalCopyAndRemovesManagedPackage` |
| `dots.disable` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDotsDisableEnableRoundTripPreservesLocalContent`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/dots-enable-disable`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIDotsLifecycleDisableProduceEquivalentSemanticState`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIDotsDisableKeepsLocalFileAndPersistsSetting` |
| `dots.edit_groups` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDotsEntryLifecyclePersistsFilesGroupsAndHistory`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/dots-groups`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIDotsEditGroupsProduceEquivalentSemanticState`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIDotsEditGroupsPersistsHostMembership` |
| `dots.enable` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDotsDisableEnableRoundTripPreservesLocalContent`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/dots-enable-disable`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIDotsLifecycleEnableProduceEquivalentSemanticState`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIDotsEnableRestoresManagedLinkAndPersistsSetting` |
| `dots.history` | medium | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDotsCommitPersistsGitCommitAndHistory`; cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDotsEntryLifecyclePersistsFilesGroupsAndHistory`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/dots-history` |
| `dots.ignore` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDotsEntryLifecyclePersistsFilesGroupsAndHistory`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/dots-ignore-eject-synced`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/dots-ignore-existing`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIDotsLifecycleIgnoreProduceEquivalentSemanticState`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIDotsIgnoreUntracksEntryAndKeepsLocalCopy` |
| `dots.list` | high | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDotsEntryLifecyclePersistsFilesGroupsAndHistory`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/dots-list-entries` |
| `dots.pull` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | parity (Stage 6); tui_blackbox (Stage 5) | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDotsPullPushSynchronizesLocalBareRemote`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/dots-pull-push` |
| `dots.push` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | parity (Stage 6); tui_blackbox (Stage 5) | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDotsPullPushSynchronizesLocalBareRemote`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/dots-pull-push` |
| `dots.refresh` | medium | CLI+TUI | query | cli_blackbox, integration, parity, tui_blackbox | parity (Stage 6) | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDotsServiceCommands/dots.refresh`; integration: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDotsServiceCommands/dots.refresh`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIDotsRefreshDetectsBrokenLinkWithoutMutatingIt` |
| `dots.reminder` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | parity (Stage 6); tui_blackbox (Stage 5) | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDotsServiceCommands/dots.reminder`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/dots-reminder-service` |
| `dots.reminder.check` | medium | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDotsServiceCommands/dots.reminder.check`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/dots-reminder-service` |
| `dots.reminder.run` | medium | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDotsServiceCommands/dots.reminder.run`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/dots-reminder-service` |
| `dots.reminder.status` | medium | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDotsServiceCommands/dots.reminder.status`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/dots-reminder-service` |
| `dots.resolve_all_use_local` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | parity (Stage 6); tui_blackbox (Stage 5) | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDotsResolveAllUseLocalAdoptsEveryConflict`; integration: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDotsResolveAllUseLocalAdoptsEveryConflict` |
| `dots.resolve_all_use_repo` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | parity (Stage 6); tui_blackbox (Stage 5) | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDotsResolveAllUseRepoRepairsEveryConflict`; integration: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDotsResolveAllUseRepoRepairsEveryConflict` |
| `dots.resolve_use_local` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIDotsUseLocalProduceEquivalentSemanticState`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/dots-resolve-use-local`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIDotsUseLocalProduceEquivalentSemanticState`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIDotsUseLocalProduceEquivalentSemanticState` |
| `dots.resolve_use_repo` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIProduceEquivalentSemanticState/dots.use_repo_produces_the_same_managed_symlink_and_backup`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/dots-resolve-use-repo`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIProduceEquivalentSemanticState/dots.use_repo_produces_the_same_managed_symlink_and_backup`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIProduceEquivalentSemanticState/dots.use_repo_produces_the_same_managed_symlink_and_backup`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIDotConflictCancelThenUseRepo` |
| `dots.services.status` | medium | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDotsServiceCommands/dots.services.status`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/dots-watch-service` |
| `dots.status` | high | CLI+TUI | query | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDotsStatusReportsManagedFileDrift`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/dots-status-entries`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIReadOnlyQueriesProduceEquivalentSemanticObservations`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIDotsRefreshDetectsBrokenLinkWithoutMutatingIt` |
| `dots.sync` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDotsSyncResolvesConflictInsideSandbox`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/dots-sync-creates-links`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/dots-sync-repair-stow-migration`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIDotsSyncProduceEquivalentSemanticState`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIDashboardReconcileFixesDotIgnorePatterns`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIDotsSyncRepairsBrokenManagedLink`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUISyncsDiscoveredDotCandidate` |
| `dots.variant` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | parity (Stage 6); tui_blackbox (Stage 5) | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDotsVariantLifecyclePersistsAndListsHostPackage`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/dots-variant` |
| `dots.variant.list` | medium | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDotsVariantLifecyclePersistsAndListsHostPackage`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/dots-variant` |
| `dots.watch` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | parity (Stage 6); tui_blackbox (Stage 5) | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDotsServiceCommands/dots.watch`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/dots-watch-service` |
| `dots.watch.run` | high | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDotsServiceCommands/dots.watch.run`; integration: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDotsServiceCommands/dots.watch.run` |
| `dots.watch.status` | medium | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryDotsServiceCommands/dots.watch.status`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/dots-watch-service` |
| `groups.create` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryGroupLifecyclePersistsHostReferences`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/groups-management`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIGroupCreateProduceEquivalentSemanticState`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIGroupsCreatePersistsNewGroup` |
| `groups.delete` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryGroupLifecyclePersistsHostReferences`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/groups-management`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIGroupDeleteProduceEquivalentSemanticState`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIGroupsDeleteRemovesEmptyGroup` |
| `groups.edit_dots` | high | TUI | — | integration, tui_blackbox | — | integration: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryGroupsEditDotsPersistsExactMembership`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIGroupsEditDotsPersistsAdditionalDotMembership` |
| `groups.edit_tools` | high | TUI | — | integration, tui_blackbox | — | integration: `github.com/lkshrk/omni/integration_tests.TestCLI/groups-management`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIGroupsEditToolsPersistsToolMembership` |
| `groups.list` | medium | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIGroupRenameProduceEquivalentSemanticState`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/groups-management` |
| `groups.rename` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryGroupLifecyclePersistsHostReferences`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/groups-management`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIGroupRenameProduceEquivalentSemanticState`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIGroupsRenamePersistsNewName` |
| `hosts.copy` | high | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryHostLifecycleCopiesAndEditsGroups`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/hosts-management` |
| `hosts.create` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryHostLifecycleCopiesAndEditsGroups`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/hosts-management`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIHostCreateProduceEquivalentSemanticState`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIHostsCreatePersistsFreshHost` |
| `hosts.delete` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryHostLifecycleCopiesAndEditsGroups`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/hosts-management`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIHostDeleteProduceEquivalentSemanticState`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIHostsDeleteRemovesSelectedHost` |
| `hosts.edit_groups` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryHostLifecycleCopiesAndEditsGroups`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/hosts-management`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIHostEditGroupsProduceEquivalentSemanticState`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIAssignsHostGroupAndPersistsSetting` |
| `hosts.list` | medium | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIHostCreateProduceEquivalentSemanticState`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/hosts-management` |
| `reconcile` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | parity (Stage 6) | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryReconcileConvergesToolDotsAndBackupState`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/reconcile-dot-commit-failure`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/reconcile-yes-tool-dotfile`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIDashboardReconcileRecoversAfterInjectedInstallFailure` |
| `settings.extract` | high | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinarySettingsExtractPreservesEffectiveConfig`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/settings-extract` |
| `settings.get` | medium | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinarySettingsReadCommandsUseIsolatedConfig`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/settings-management` |
| `settings.lint` | high | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinarySettingsReadCommandsUseIsolatedConfig`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/settings-maintenance` |
| `settings.migrate_host_overrides` | high | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryMigrateHostOverridesPromotesProviderCandidates`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/settings-maintenance` |
| `settings.provider` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryProviderTogglePersistsPerHost`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/settings-management`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUISettingsProviderProduceEquivalentSemanticState`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUISettingsProviderProduceEquivalentSemanticState` |
| `settings.provider_priority` | high | TUI | — | component, tui_blackbox | — | component: `github.com/lkshrk/omni/internal/tui.TestModel_PriorityEditor_GrabCarryDown`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUISettingsProviderPersistsPriorityOrder` |
| `settings.reset` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinarySettingsResetPreservesInventory`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/settings-management`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUISettingsResetProduceEquivalentSemanticState`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUISettingsResetPreservesInventoryAndClearsSettings` |
| `settings.reset_cache` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinarySettingsResetCacheRemovesCachedTools`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/settings-management`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUISettingsResetCacheProduceEquivalentSemanticState`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUISettingsResetCacheRemovesCachedToolState` |
| `settings.set` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIProduceEquivalentSemanticState/settings.auto_import_persists_the_same_effective_setting`; cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryBootstrapDiscoversAndPersistsDefaultConfig`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/settings-management`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIProduceEquivalentSemanticState/settings.auto_import_persists_the_same_effective_setting`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIProduceEquivalentSemanticState/settings.auto_import_persists_the_same_effective_setting`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIAssignsHostGroupAndPersistsSetting` |
| `settings.show` | medium | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinarySettingsReadCommandsUseIsolatedConfig`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/settings-management` |
| `setup.init` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryInitCreatesAnIsolatedHostConfig`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/init-import-config`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUISetupProduceEquivalentConfig`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIFirstRunCreatesHostAndReachesDashboard` |
| `tools.baseline_system_inventory` | high | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryToolsFinalMaintenanceFlows/tools.baseline_system_inventory`; integration: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryToolsFinalMaintenanceFlows/tools.baseline_system_inventory` |
| `tools.change_group` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryToolSpecLifecyclePersistsProviderGroupsAndIgnore`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/groups-management`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIToolsFinalChangeGroupProduceEquivalentSemanticState`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIToolsChangeGroupPersistsAdditionalMembership` |
| `tools.claim` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | parity (Stage 6); tui_blackbox (Stage 5) | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryToolsFinalMaintenanceFlows/tools.claim`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/sync-all-claim-install` |
| `tools.consolidate` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | parity (Stage 6); tui_blackbox (Stage 5) | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryToolsFinalMaintenanceFlows/tools.consolidate`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/consolidate-to-brew` |
| `tools.delete` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIToolDeleteProduceEquivalentSemanticState`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/tools-provider-lifecycle`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/tools-remove-purge-no-provider`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIToolDeleteProduceEquivalentSemanticState`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIAdminTerminalCompletesInteractiveBrewCaskUninstall` |
| `tools.delete_spec` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | parity (Stage 6) | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryToolSpecLifecyclePersistsProviderGroupsAndIgnore`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/tools-provider-lifecycle`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIToolsDeleteSpecRemovesMissingTrackedTool` |
| `tools.fallback` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | parity (Stage 6) | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryToolsFinalMaintenanceFlows/tools.fallback_unreachable_api`; cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryToolsFinalMaintenanceFlows/tools.fallback`; integration: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryToolsFinalMaintenanceFlows/tools.fallback_unreachable_api`; integration: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryToolsFinalMaintenanceFlows/tools.fallback`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIFallbackEditorPrefillsConfiguredGitHint`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIFallbackProviderListSmoke` |
| `tools.heal_brew_taps` | high | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryHealBrewTapsPersistsQualifiedPackage`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/tools-heal-taps-dryrun` |
| `tools.ignore` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | parity (Stage 6) | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryToolSpecLifecyclePersistsProviderGroupsAndIgnore`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/tools-ignore-processing`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/tools-ignore`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIToolsIgnorePersistsGlobalToolScope` |
| `tools.import` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | parity (Stage 6); tui_blackbox (Stage 5) | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryToolsFinalMaintenanceFlows/tools.import`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/import-no-group-noninteractive`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/import-node-success` |
| `tools.install` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIProduceEquivalentSemanticState/tools.install_uses_the_same_provider_state_and_mutation`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/install-group`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/tools-provider-lifecycle`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIProduceEquivalentSemanticState/tools.install_uses_the_same_provider_state_and_mutation`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIProduceEquivalentSemanticState/tools.install_uses_the_same_provider_state_and_mutation`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIShellContractShowsTransientInstallProgress` |
| `tools.list` | high | CLI+TUI | query | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryToolLifecycleUsesConfiguredProviderCommands`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/list-empty`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/logical-tools-story`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIReadOnlyQueriesProduceEquivalentSemanticObservations`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIShellContractNavigatesWithTerminalKeysAndMouse` |
| `tools.migrate_nvm` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | parity (Stage 6); tui_blackbox (Stage 5) | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryToolsFinalMaintenanceFlows/tools.migrate_nvm`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/tools-maintenance` |
| `tools.normalize_provider_overrides` | high | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryToolsFinalMaintenanceFlows/tools.normalize_provider_overrides`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/tools-maintenance` |
| `tools.pin_provider` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | parity (Stage 6); tui_blackbox (Stage 5) | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryToolsFinalMaintenanceFlows/tools.pin_provider`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/tools-provider-scope` |
| `tools.providers` | medium | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryToolsProvidersAndSearchUseConfiguredAdapter/tools.providers`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/providers` |
| `tools.refresh` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryToolLifecycleUsesConfiguredProviderCommands`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/refresh-metadata`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIToolsRefreshProduceEquivalentSemanticState`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIToolsRefreshRechecksProviderState` |
| `tools.reinstall_default` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | parity (Stage 6); tui_blackbox (Stage 5) | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryToolsFinalMaintenanceFlows/tools.reinstall_default`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/tools-reinstall-default` |
| `tools.search` | medium | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryToolsProvidersAndSearchUseConfiguredAdapter/tools.search`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/search` |
| `tools.set_spec` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | parity (Stage 6); tui_blackbox (Stage 5) | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryToolSpecLifecyclePersistsProviderGroupsAndIgnore`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/tools-provider-scope` |
| `tools.switch_provider` | high | CLI | — | cli_blackbox, integration | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryToolsFinalMaintenanceFlows/tools.switch_provider`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/tools-provider-lifecycle` |
| `tools.sync` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | parity (Stage 6); tui_blackbox (Stage 5) | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryToolLifecycleUsesConfiguredProviderCommands`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/sync-dryrun`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/tools-sync-configured-provider-preferred` |
| `tools.sync_all` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIToolSyncAllProduceEquivalentSemanticState`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/sync-all-claim-install`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/sync-all-group-flag`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIToolSyncAllProduceEquivalentSemanticState`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIToolSyncAllProduceEquivalentSemanticState`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIToolsSyncInstallsConfiguredTool` |
| `tools.update` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIBinaryToolLifecycleUsesConfiguredProviderCommands`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/tools-provider-lifecycle`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/upgrade-no-args`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIToolUpdateProduceEquivalentSemanticState`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIToolUpdateProduceEquivalentSemanticState`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIToolsUpdateUpgradesOnlySelectedTool` |
| `tools.update_all` | high | CLI+TUI | state | cli_blackbox, integration, parity, tui_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIToolUpdateAllProduceEquivalentSemanticState`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/upgrade-all-quarantine`; integration: `github.com/lkshrk/omni/integration_tests.TestCLI/upgrade-all-success`; parity: `github.com/lkshrk/omni/integration_tests.TestCLIAndTUIToolUpdateAllProduceEquivalentSemanticState`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIToolsUpgradeAllInvokesEveryOutdatedTool` |
| `tui.details` | medium | TUI | — | component, tui_blackbox | — | component: `github.com/lkshrk/omni/internal/tui.TestAgentsDetailsFollowTheCursor`; component: `github.com/lkshrk/omni/internal/tui.TestRenderSettings_OnlySelectedRowShowsDetail`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIShellContractNavigatesWithTerminalKeysAndMouse` |
| `tui.error` | high | TUI | — | component, tui_blackbox | — | component: `github.com/lkshrk/omni/internal/tui.TestModel_ProgressDoneMsg_Error`; component: `github.com/lkshrk/omni/internal/tui.TestRowErrorSummaryNormalizesUnsafeOutput`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIDashboardReconcileRecoversAfterInjectedInstallFailure` |
| `tui.loading` | medium | TUI | — | component, tui_blackbox | — | component: `github.com/lkshrk/omni/internal/tui.TestFlow_UC22_KeysIgnoredWhileLoading`; component: `github.com/lkshrk/omni/internal/tui.TestToolRefresh_ClearsLoadingWhenTheLastLegFails`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIShellContractShowsTransientInstallProgress` |
| `tui.mouse` | medium | TUI | — | component, tui_blackbox | — | component: `github.com/lkshrk/omni/internal/tui.TestAgentsMouseWheelMovesCursor`; component: `github.com/lkshrk/omni/internal/tui.TestFlow_UC64_MouseWheelScroll`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIShellContractNavigatesWithTerminalKeysAndMouse` |
| `tui.navigation` | high | TUI | — | component, tui_blackbox | — | component: `github.com/lkshrk/omni/internal/tui.TestCursorHidden_NavigationRevealsWithoutMoving_ToolsTab`; component: `github.com/lkshrk/omni/internal/tui.TestFlow_UC01_CursorNavigation`; component: `github.com/lkshrk/omni/internal/tui.TestFlow_UC03_PageNavigation`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIShellContractNavigatesWithTerminalKeysAndMouse` |
| `tui.resize` | medium | TUI | — | component, tui_blackbox | — | component: `github.com/lkshrk/omni/internal/tui.TestFlow2_UC136_WindowSizeMsgWithFilePicker`; component: `github.com/lkshrk/omni/internal/tui.TestFlow_UC65_WindowSize`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIShellContractRedrawsAfterPTYResize` |
| `tui.search` | medium | TUI | — | component, tui_blackbox | — | component: `github.com/lkshrk/omni/internal/tui.TestFlow2_UC132_BlurredSearchSlashRefocusesInput`; component: `github.com/lkshrk/omni/internal/tui.TestModel_FilterMode`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIShellContractNavigatesWithTerminalKeysAndMouse` |
| `tui.startup` | high | TUI | — | component, tui_blackbox | — | component: `github.com/lkshrk/omni/internal/tui.TestModel_Init`; component: `github.com/lkshrk/omni/internal/tui.TestStartupErrorDoesNotMaskRepairSettingsTab`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIConfiguredHostStartsDashboard`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIFirstRunCreatesHostAndReachesDashboard`; tui_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIShellContractNavigatesWithTerminalKeysAndMouse` |
| `ui.launch` | high | CLI | — | cli_blackbox | — | cli_blackbox: `github.com/lkshrk/omni/integration_tests.TestTUIShellContractRedrawsAfterPTYResize` |
<!-- END GENERATED FLOW CATALOG -->

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
| TUI-06 Agents mutation | Open Agents -> cancel/reopen -> inspect and resolve targets, secrets, dots/native ownership, conflicts, and unmanaged items -> review/apply -> local status -> preview/confirm cleanup; verify manifest, lock, durable package, completion marker, runtime state, and post-cleanup no-op | `TestTUIAgentsTabSyncsMCPThroughRealAPM`, `TestTUIAgentsOnboardingPreviewConfirmAndApply` | covered |
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

## Non-action workflow coverage

Onboarding is a coordinated CLI/TUI workflow, not an `internal/actions`
catalog entry. Its evidence is tracked here instead of inventing an action ID.

| Workflow | App/protocol | CLI/model | Real integration | Remaining gate |
| --- | --- | --- | --- | --- |
| Host template and pre-APM migration | Offline ownership planning, exact-child suppression, mandatory content-addressed wrappers, source `apm.yml` rebasing, marked-template publication, first-sync/divergence guards, installed-module ownership preflight, exact-source-byte Doctor repair, and canonical-template/APM lock ordering | `agents migrate --host/--snapshot` defaults to preview; `--dry-run` aliases preview; `--write` publishes only the marked template/wrappers; Doctor reports/fixes exact duplicates; `agents sync --force-template` materializes the validated candidate bytes | Focused fixtures cover exact/conflict/multi-owner/relevant-unavailable-evidence classification, strict manifestless skill-only proof, all-or-nothing source-layout refusal, symlink and classification-input identity rechecks, zero-mutation preflight failures, child-health rollup, plus existing wrapper integrity; lifecycle smoke covers exact and manual repair paths | Immutable pinned-APM DinD plus macOS/Windows path and canonicalization jobs; direct isolated-HOME `apm audit --ci` wrapper-path false positive is known |

## APM ownership migration verification

The normal PR gate is:

```sh
go test -count=1 ./internal/config -run 'AgentsSnapshot|LegacyAgent'
go test -count=1 ./internal/app -run 'AgentsMigrate|BundleOwnership|AgentsSync'
go test -count=1 ./internal/cli -run 'AgentsMigrate'
go build ./...
go test -count=1 ./...
go vet ./...
make lint
```

The isolated temporary HOME/state/APM-workspace lifecycle smoke passed this
sequence:

1. Two previews are byte-identical and leave filesystem hashes unchanged.
2. `--write` produces one owner dependency, suppresses owned standalone
   children, updates only the marked template, and leaves the live manifest
   unchanged.
3. `omni agents sync` materializes the guarded live manifest and the pinned APM
   installs it on the empty home.
4. The wrapped MCP handshake succeeds. Global `apm audit --ci` has a known
   APM 0.29.0 false positive for deployed `.agents/**` paths. Until upstream
   path resolution is fixed, use `omni agents sync` plus `omni doctor` as the
   global verification gate. CI may allow only the exact known `.agents/**`
   findings after independently checking those files; all other findings fail.
5. Reinstall is a byte/semantic no-op.
6. Uninstall removes owner-attributed artifacts while unrelated configuration
   survives.
7. A repeat migration over the unchanged snapshot selects the same wrapper
   hash; rerunning migrate after source changes refreshes the wrapper snapshot.
8. `/home/coder/apm` remains unchanged.

Package-owned child reconciliation adds these lifecycle scenarios:

1. An exact standalone MCP/LSP duplicate is reported by Doctor; dry-run changes
   nothing; fix removes only that canonical-template item; sync succeeds; the
   TUI shows the child once under package `provides`.
2. The differing `context-mode` declarations remain unchanged by Doctor and
   block sync before live/APM mutation; the TUI shows a degraded package plus
   the conflicting standalone row until the top-level template entry is
   removed manually.
3. Independent services remain top-level and retain existing health/install
   behavior. Multiple owners block deterministically.
4. A first install with unavailable package evidence and standalone services
   blocks before materialization/APM; package-only first install proceeds.
5. Manifestless DeepWiki `skill_bundle` and Shiplight `claude_skill` packages
   are proven service-free only from complete pinned lock/module evidence; the
   standalone Shiplight MCP remains independent and Doctor removes nothing.
6. Unknown, plugin, hybrid, mixed, incomplete, unsafe, unreadable, and changed
   evidence remains unavailable. Pre-mutation identity changes produce zero
   template/live writes and zero APM calls.

Focused proof:

```sh
go test ./internal/app -run 'ManifestlessSkill|OwnedChild|AgentsStatus|AgentsSyncAll|DoctorAgents'
go test ./internal/cli -run Doctor
go test ./internal/tui -run Agents
```

APM is not modified by this reconciliation. Omni repairs only its canonical
template, then hands runtime ownership back to the pinned APM build. The
manifestless proof does not change TUI rendering, update checks, or versions.

Release remains gated on the focused and full lanes on Linux, the immutable
pinned-APM DinD lane, and the existing macOS/Windows path and canonicalization
jobs. Fixtures and diagnostics must contain no literal secrets.

## Action-level coverage

The detailed action table below records representative app, CLI, and TUI
model/render evidence. Its TUI column does **not** require a separate
real-terminal test; the eight-journey budget above owns that layer.

<!-- BEGIN GENERATED ACTION CATALOG -->
| Action ID | CLI | TUI | Flow |
| --- | --- | --- | --- |
| `agents.add` | yes | yes | `agents.add` |
| `agents.marketplace.add` | yes | — | `agents.marketplace.add` |
| `agents.marketplace.remove` | yes | — | `agents.marketplace.remove` |
| `agents.marketplace.update` | yes | — | `agents.marketplace.update` |
| `agents.migrate` | yes | — | `agents.migrate` |
| `agents.prune` | yes | — | `agents.prune` |
| `agents.refresh` | yes | yes | `agents.refresh` |
| `agents.remove` | yes | yes | `agents.remove` |
| `agents.sync` | yes | yes | `agents.sync` |
| `agents.update` | — | yes | `agents.update` |
| `agents.update_all` | yes | yes | `agents.update_all` |
| `doctor` | yes | yes | `doctor` |
| `doctor.fix` | yes | yes | `doctor.fix` |
| `dots.add` | yes | yes | `dots.add` |
| `dots.commit` | yes | yes | `dots.commit` |
| `dots.delete` | yes | yes | `dots.delete` |
| `dots.disable` | yes | yes | `dots.disable` |
| `dots.edit_groups` | yes | yes | `dots.edit_groups` |
| `dots.enable` | yes | yes | `dots.enable` |
| `dots.history` | yes | — | `dots.history` |
| `dots.ignore` | yes | yes | `dots.ignore` |
| `dots.pull` | yes | yes | `dots.pull` |
| `dots.push` | yes | yes | `dots.push` |
| `dots.refresh` | yes | yes | `dots.refresh` |
| `dots.reminder` | yes | yes | `dots.reminder` |
| `dots.reminder.check` | yes | — | `dots.reminder.check` |
| `dots.reminder.run` | yes | — | `dots.reminder.run` |
| `dots.reminder.status` | yes | — | `dots.reminder.status` |
| `dots.resolve_all_use_local` | yes | yes | `dots.resolve_all_use_local` |
| `dots.resolve_all_use_repo` | yes | yes | `dots.resolve_all_use_repo` |
| `dots.resolve_use_local` | yes | yes | `dots.resolve_use_local` |
| `dots.resolve_use_repo` | yes | yes | `dots.resolve_use_repo` |
| `dots.services.status` | yes | — | `dots.services.status` |
| `dots.sync` | yes | yes | `dots.sync` |
| `dots.variant` | yes | yes | `dots.variant` |
| `dots.watch` | yes | yes | `dots.watch` |
| `dots.watch.run` | yes | — | `dots.watch.run` |
| `dots.watch.status` | yes | — | `dots.watch.status` |
| `groups.create` | yes | yes | `groups.create` |
| `groups.delete` | yes | yes | `groups.delete` |
| `groups.edit_dots` | — | yes | `groups.edit_dots` |
| `groups.edit_tools` | — | yes | `groups.edit_tools` |
| `groups.rename` | yes | yes | `groups.rename` |
| `hosts.copy` | yes | — | `hosts.copy` |
| `hosts.create` | yes | yes | `hosts.create` |
| `hosts.delete` | yes | yes | `hosts.delete` |
| `hosts.edit_groups` | yes | yes | `hosts.edit_groups` |
| `reconcile` | yes | yes | `reconcile` |
| `settings.extract` | yes | — | `settings.extract` |
| `settings.migrate_host_overrides` | yes | — | `settings.migrate_host_overrides` |
| `settings.provider` | yes | yes | `settings.provider` |
| `settings.provider_priority` | — | yes | `settings.provider_priority` |
| `settings.reset` | yes | yes | `settings.reset` |
| `settings.reset_cache` | yes | yes | `settings.reset_cache` |
| `settings.set` | yes | yes | `settings.set` |
| `setup.init` | yes | yes | `setup.init` |
| `tools.baseline_system_inventory` | yes | — | `tools.baseline_system_inventory` |
| `tools.change_group` | yes | yes | `tools.change_group` |
| `tools.claim` | yes | yes | `tools.claim` |
| `tools.consolidate` | yes | yes | `tools.consolidate` |
| `tools.delete` | yes | yes | `tools.delete` |
| `tools.delete_spec` | yes | yes | `tools.delete_spec` |
| `tools.fallback` | yes | yes | `tools.fallback` |
| `tools.heal_brew_taps` | yes | — | `tools.heal_brew_taps` |
| `tools.ignore` | yes | yes | `tools.ignore` |
| `tools.import` | yes | yes | `tools.import` |
| `tools.install` | yes | yes | `tools.install` |
| `tools.migrate_nvm` | yes | yes | `tools.migrate_nvm` |
| `tools.normalize_provider_overrides` | yes | — | `tools.normalize_provider_overrides` |
| `tools.pin_provider` | yes | yes | `tools.pin_provider` |
| `tools.refresh` | yes | yes | `tools.refresh` |
| `tools.reinstall_default` | yes | yes | `tools.reinstall_default` |
| `tools.set_spec` | yes | yes | `tools.set_spec` |
| `tools.switch_provider` | yes | — | `tools.switch_provider` |
| `tools.sync` | yes | yes | `tools.sync` |
| `tools.sync_all` | yes | yes | `tools.sync_all` |
| `tools.update` | yes | yes | `tools.update` |
| `tools.update_all` | yes | yes | `tools.update_all` |
<!-- END GENERATED ACTION CATALOG -->
