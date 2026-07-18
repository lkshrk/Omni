# Test Report: Dotfile Child Conflict Resolution

**Test spec**: [test-spec.md](./test-spec.md)
**Branch / commit**: `fix/dots-deep-include`
**Last updated**: 2026-07-18
**Tester**: Codex

## Summary

- Overall session status: `PASS`.
- What this session added or strengthened: stable tagged integration coverage for the nested include matrix; deterministic compiled-PTY confirmation; restored failed-row `e` error-log behavior; corrected one stale provider-detail expectation.
- Blocking findings: none.
- Finding closed during coverage work: traversing an ignored ancestor lost the ancestor's ignored state for siblings. Basename propagation initially crossed a synthetic copy root; a second regression test bounded it to the real walk-relative path.
- Known gaps left for follow-up: none for the declared scope.

## Source Material

- Source material used: user reports and coverage follow-up, implementation, existing TUI/app tests, baked-in default testing strategy.
- Source material not found or not available: upstream PRD/feature priority; repo-root `TESTING.md`.

## Commands Run

| Command | Result | Notes |
| --- | --- | --- |
| `go test -tags=integration ./integration_tests -run '^TestDotsNestedIgnoredIncludeCriticalPaths$' -count=3` | `PASS` | Four nested-include scenarios passed repeatedly in 3.628s, then 3.333s after the final matcher boundary fix. |
| `go test ./internal/dots -run 'TestShouldIgnoreAnyPath_(DirectoryIgnoreAppliesToDescendants\|BasenameAncestorDoesNotEscapeLogicalRoot)$' -count=1` | `PASS` | Ancestor propagation and logical-root boundary regressions passed. |
| `go test ./internal/dots -count=1` | `PASS` | Final complete dots package, including absolute/upward-relative input rejection, passed in 0.106s. |
| `go test -tags=integration ./integration_tests -run '^TestCLI/dots-ignore-eject-dir$' -count=5` | `PASS` | Ejection-copy regression passed 5/5 after bounding basename ancestry. |
| `go test -tags=integration ./integration_tests -run '^TestTUIIncludesStaticIgnoredDotCandidate$' -count=10` | `PASS` | Compiled PTY include passed 10/10 after the readiness fix; the producing agent also recorded 30/30. |
| `go test -tags=integration ./integration_tests -count=1` | `PASS` | Complete tagged integration package passed after all gap fixes in 124.637s. |
| `go test ./internal/app -run 'TestDotsMutationWithStateReturnsRefreshedSnapshots/(include_path_below_ignored_directory\|include_directory_below_ignored_ancestor\|include_repo-only_directory_below_ignored_ancestor)$\|TestDotsSync_PurgeMovesIgnoredRepoSourceToTrashWithSnapshot' -count=1` | `PASS` | Selected local/repo-only include and ignored-source purge paths passed in 1.260s. |
| `go test ./internal/app -count=1` | `PASS` | Complete app package passed after the final matcher fix in 199.194s. |
| `go test ./internal/tui -run 'TestDotsExpand_MultipleLayersDeep\|TestFlow_DotsExpandCollapse_MultipleLayersDeep\|TestFlow_DotsExpandCollapse_IgnoredDirectory\|TestFlow_DotsNestedIgnoredChildIncludeExecutesAndRefreshes' -count=1` | `PASS` | Depth, ignored-directory expansion, nested selection/action, filesystem, and refresh passed in 0.645s. |
| `go test ./internal/tui -run 'TestModel_ToolsLoadedMsg/startup_snapshot_provider_candidates_reach_selected_row_details\|TestTraceLog_EFrom(FailedTool\|BlurredSearchResult)OpensPopup' -count=20` | `PASS` | Former baseline failures passed 20/20. |
| `go test ./internal/tui -count=1` | `PASS` | Complete TUI package passed after all gap fixes in 201.396s. |
| `golangci-lint run ./...` | `PASS` | 0 issues; emitted one non-failing warning for a stale deleted worktree path. |
| `go vet ./...` | `PASS` | No findings. |
| `git diff --check` | `PASS` | No whitespace errors. |

## Tests Added Or Updated

| Type | Files | Tests / scenarios |
| --- | ---: | ---: |
| Unit | 2 | 4 |
| Contract | 0 | 0 |
| Integration | 1 | 4 |
| E2E | 1 | 1 |
| Agent | 0 | 0 |
| Script / static | 0 | 0 |
| **Total** | **4** | **9** |

### File List

- `internal/dots/ignore_test.go` (ignored-directory descendant semantics, logical-root boundary, and escaping-path rejection)
- `integration_tests/dots_nested_include_test.go` (four stable tagged integration scenarios)
- `integration_tests/tui_integration_test.go` (compiled PTY readiness and confirmation E2E)
- `internal/tui/model_test.go` (provider-detail startup snapshot expectation)

## Coverage Matrix

| Behavior (from test-spec) | Priority | Test type | Coverage | Session result | Notes / gap |
| --- | --- | --- | --- | --- | --- |
| Nested `use repo` executes and refreshes | UNKNOWN | integration | COVERED | PASS | Existing deterministic TUI integration. |
| Nested `use local` adopts, relinks, and refreshes | UNKNOWN | integration | COVERED | PASS | Existing deterministic TUI integration. |
| Prompt names child and active key | UNKNOWN | unit | COVERED | PASS | Existing default and remapped key assertions. |
| Timeout and escape clear action and prompt | UNKNOWN | unit | COVERED | PASS | Existing deterministic message/key handling. |
| Many conflicts preserve ignored local-only paths | UNKNOWN | integration | COVERED | PASS | Existing multi-conflict fixture. |
| Missing child installs only the selected path | UNKNOWN | integration | COVERED | PASS | Existing `i` command/filesystem/refresh coverage. |
| Configured missing and discovered repo-only entries install | UNKNOWN | integration | COVERED | PASS | Existing dispatch-path coverage. |
| Missing local source renders concise red popup text | UNKNOWN | unit | COVERED | PASS | Existing complete-popup assertion. |
| Ignored entry include adopts and refreshes as synced | UNKNOWN | integration | COVERED | PASS | Existing deterministic two-key TUI integration. |
| Nested ignored file/directory include matrix | UNKNOWN | integration | COVERED | PASS | Rooted and basename ancestors, local and repo-only directories, subtree traversal, sibling isolation, filesystem links, and refreshed state asserted. |
| Compiled TUI ignored-entry include | UNKNOWN | e2e | COVERED | PASS | Real binary, PTY, initial async sync, confirmation, config persistence, temporary HOME, and real git repo; repeated 10/10 and 30/30. |

## Agent Test Evidence

- None; deterministic Go tests cover this local terminal workflow.

## Manual Verification Log

- No manual/live-dotfiles mutation performed.

## Findings

- **Closed**: a non-trailing-slash directory ignore matched the directory itself but not descendants after an explicit include reopened traversal. Ignore matching now retains ignored ancestor state and allows later explicit includes to override it.
- **Closed**: basename ancestor propagation initially considered a synthetic rooted candidate during directory ejection, causing copied content to be skipped. Basename ancestry now follows only the primary walk-relative path.
- **Closed**: exported ignore matching could walk forever on absolute or upward-relative inputs. Such paths are now rejected, with defensive non-progress termination in ancestor walks.
- **Closed**: cumulative PTY output exposed the candidate before initial dots sync completed; the initial result then cleared confirmation mid-`x,x`. The E2E now waits for `dots synced` and observable confirmation state.
- **Closed**: failed-row `e` was consumed by the ignored-row edit branch because both actions share the key. Error-log handling now takes precedence when the selected row has an error.
- **Closed**: provider startup snapshot test expected legacy `provider/package` strings while the intentional UI renders provider badges.

## Deferred / Residual Risk

- None for the declared scope. Live user-dotfile mutation remains intentionally out of scope; the E2E uses the real binary and git workflow against isolated temporary state.

## Cleanup

- Cleanup performed: all HOME/repository fixtures use automatic Go temporary-directory cleanup.
- Resources intentionally left behind: none.
- Follow-up cleanup required: none.

## Coverage Summary

- Total testing whats: 11
- COVERED: 11
- PARTIAL: 0
- IMPLICIT: 0
- NOT COVERED: 0
- NOT MEASURED: 0
- MANUAL: 0
- BLOCKED: 0
- DEFERRED: 0
