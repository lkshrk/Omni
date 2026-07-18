# Test Spec: Dotfile Child Conflict Resolution

**Scope**: feature
**Source material**: user reports and coverage follow-up; `internal/tui/update_dots.go`; `internal/tui/view_dots_peek.go`; `internal/app/dots_state.go`
**Testing posture**: baked-in default; no repo-root `TESTING.md`
**Test report**: [test-report.md](./test-report.md)

## Testing What

### Product Behaviors

- A child-row `use repo` confirmation executes and refreshes the selected nested path as synced.
- A child-row `use local` confirmation adopts local content, relinks it, and refreshes the nested path as synced.
- The first action key visibly asks for a second press and names the nested child.
- Timeout or cancellation clears both the pending action and its prompt.
- The prompt reflects the active key binding.
- A missing child installs from the repo with `i`, refreshes as synced, and leaves conflicting siblings untouched.
- Missing and repo-only top-level entries install with `i` through configured and discovered paths.
- A missing local source renders as concise red `local source: missing` text in the complete peek popup.
- Including an ignored entry from the TUI adopts it and refreshes it as synced instead of local-only.
- Including a nested file below either a rooted or basename ignored directory adopts only that file; ignored siblings remain local and unmanaged.
- Including a nested local directory below a broad ignore adopts and links its complete subtree while leaving sibling subtrees unmanaged.
- Including a repo-only nested directory below a broad ignore installs its complete subtree without installing ignored siblings.

### Implementation / System Invariants

- Nested resolution works with many sibling conflicts.
- Default and configured ignored local-only paths survive both strategies.
- A successful resolution leaves the local managed path linked to the repo source.
- Install and include operations return refreshed TUI state that matches their filesystem result.
- Directory ignores continue to apply to descendants when traversal opens the directory for an explicit include.
- Basename-ignore ancestry is bounded by the real walk root so ejection copies from an explicitly selected ignored directory retain their contents.

### Risk-Based Behaviors

- Confirmation remains mandatory before destructive conflict resolution.
- Ignored local state is not deleted or adopted accidentally.

### Operational / Release Behaviors

- TUI tests, repository tests, and Go static analysis pass.
- The compiled TUI waits for initial dots sync before arming an ignored-entry include, then persists the confirmed result.

### Stakeholder Confidence Goals

- The reported `.claude/agents/explore.md` workflow has executable filesystem and refreshed-state evidence.

## Evidence Strategy

No upstream artifact declares priorities; all priorities remain `UNKNOWN`.

| What | Priority | Viable How | Selected How | Why | Residual Risk |
| --- | --- | --- | --- | --- | --- |
| Use repo nested resolution | UNKNOWN | integration | integration | Exercises real app command, filesystem, and TUI refresh | None known |
| Use local nested resolution | UNKNOWN | integration | integration | Proves adoption and relink side effects | None known |
| Confirmation prompt and active key | UNKNOWN | unit | unit | Deterministic model state | None known |
| Timeout and cancellation cleanup | UNKNOWN | unit | unit | Deterministic message/key handling | Timer scheduling itself remains Bubble Tea/runtime-owned |
| Ignored local-only preservation under conflict load | UNKNOWN | integration | integration | Real 24-conflict package with default and explicit ignores | Host-specific production data is not copied into tests |
| Missing child install | UNKNOWN | integration | integration | Exercises the `i` key, app command, filesystem, refresh, and sibling isolation | None known |
| Top-level missing/repo-only install | UNKNOWN | integration | integration | Covers both configured and discovered dispatch paths | None known |
| Missing local popup presentation | UNKNOWN | unit | unit | Full deterministic popup render proves text, omission, and style | Terminal palette support remains runtime-owned |
| Ignored-entry include | UNKNOWN | integration | integration | Exercises two-key TUI confirmation through adoption and refreshed state | None known |
| Nested ignored include matrix | UNKNOWN | integration | integration | Real config, filesystem, sync, refreshed state, subtree traversal, and sibling isolation | None known |
| Compiled TUI ignored-entry include | UNKNOWN | e2e | e2e | Exercises the real binary, PTY, asynchronous initial sync, confirmation, config persistence, temporary HOME, and real git dots repo | None known |

Out of scope:

- Manual mutation of the user's live dotfiles.
- Bubble Tea scheduler timing internals.

## Test Cases

### DCCR-T01 Use Repo

- Testing what: confirmed nested `use repo` repairs and refreshes state.
- Source refs: `TestFlow_DotsChildConflictUseRepoExecutesAndRefreshes`.
- Preconditions: temporary HOME and dotfiles repo; GNU Stow available through test mode.
- Automated checks:

  ```bash
  go test ./internal/tui -run TestFlow_DotsChildConflictUseRepoExecutesAndRefreshes -count=1
  ```

- Pass criteria: child is synced, local path resolves to repo source, ignored state survives.
- Cleanup: automatic temporary-directory cleanup.

### DCCR-T02 Use Local

- Testing what: confirmed nested `use local` adopts, relinks, and refreshes state.
- Source refs: `TestFlow_DotsChildConflictUseLocalExecutesAndRefreshes`.
- Automated checks:

  ```bash
  go test ./internal/tui -run TestFlow_DotsChildConflictUseLocalExecutesAndRefreshes -count=1
  ```

- Pass criteria: repo contains local content, child is synced, ignored state survives.
- Cleanup: automatic temporary-directory cleanup.

### DCCR-T03 Confirmation Lifecycle

- Testing what: visible prompt, active binding, timeout, and escape behavior.
- Source refs: `TestFlow_DotsChildConflictResolve`.
- Automated checks:

  ```bash
  go test ./internal/tui -run TestFlow_DotsChildConflictResolve -count=1
  ```

- Pass criteria: prompt names child and active key; timeout/escape clear prompt and pending action.
- Cleanup: none.

### DCCR-T04 Missing Child Install

- Testing what: `i` installs only the selected missing child and refreshes it as synced.
- Source refs: `TestFlow_DotsMissingChildInstallExecutesAndRefreshes`.
- Automated checks:

  ```bash
  go test ./internal/tui -run TestFlow_DotsMissingChildInstallExecutesAndRefreshes -count=1
  ```

- Pass criteria: selected child is linked and synced; unrelated conflict content is unchanged.
- Cleanup: automatic temporary-directory cleanup.

### DCCR-T05 Top-Level Install

- Testing what: `i` installs configured missing and discovered repo-only entries.
- Source refs: `TestFlow_DotsTopLevelInstallExecutesAndRefreshes`.
- Automated checks:

  ```bash
  go test ./internal/tui -run TestFlow_DotsTopLevelInstallExecutesAndRefreshes -count=1
  ```

- Pass criteria: both pre-states are asserted; each target links to its repo source and refreshes as synced.
- Cleanup: automatic temporary-directory cleanup.

### DCCR-T06 Missing Local Popup

- Testing what: the full popup renders concise red missing-local metadata without the absent path.
- Source refs: `TestDotsPeekSourceLine_MissingLocalIsConciseAndRed`.
- Automated checks:

  ```bash
  go test ./internal/tui -run TestDotsPeekSourceLine_MissingLocalIsConciseAndRed -count=1
  ```

- Pass criteria: popup contains `local source: missing`, omits the local path, and uses the missing style.
- Cleanup: none.

### DCCR-T07 Ignored Entry Include

- Testing what: confirmed TUI include adopts local content and returns synced state.
- Source refs: `TestFlow_DotsIgnoredEntryIncludeExecutesAndRefreshes`.
- Automated checks:

  ```bash
  go test ./internal/tui -run TestFlow_DotsIgnoredEntryIncludeExecutesAndRefreshes -count=1
  ```

- Pass criteria: refreshed entry is synced and the local file resolves to the adopted repo source.
- Cleanup: automatic temporary-directory cleanup.

### DCCR-T08 Nested Ignored Include Matrix

- Testing what: nested file and directory includes traverse ignored ancestors, sync the complete selected scope, refresh as synced, and preserve sibling isolation.
- Source refs: `TestDotsNestedIgnoredIncludeCriticalPaths`; `TestShouldIgnoreAnyPath_DirectoryIgnoreAppliesToDescendants`; `TestShouldIgnoreAnyPath_BasenameAncestorDoesNotEscapeLogicalRoot`; `TestShouldIgnoreAnyPathChecked_RejectsPathsOutsideLogicalRoot`; `TestFlow_DotsNestedIgnoredChildIncludeExecutesAndRefreshes`.
- Automated checks:

  ```bash
  go test -tags=integration ./integration_tests -run '^TestDotsNestedIgnoredIncludeCriticalPaths$' -count=1
  go test ./internal/dots -run 'TestShouldIgnoreAnyPath_(DirectoryIgnoreAppliesToDescendants|BasenameAncestorDoesNotEscapeLogicalRoot)$|TestShouldIgnoreAnyPathChecked_RejectsPathsOutsideLogicalRoot' -count=1
  go test ./internal/tui -run 'TestFlow_DotsNestedIgnoredChildIncludeExecutesAndRefreshes' -count=1
  ```

- Pass criteria: rooted and basename ignored ancestors include only the chosen file; a local selected directory adopts and links every descendant; a repo-only selected directory installs every descendant; ignored sibling targets remain absent or unchanged; refreshed entry state is synced, never local-only.
- Cleanup: automatic temporary-directory cleanup.

### DCCR-T09 Compiled TUI Ignored-Entry Include

- Testing what: the real TUI binary waits until initial dots sync is complete, arms include confirmation, confirms it, and persists the entry.
- Source refs: `TestTUIIncludesStaticIgnoredDotCandidate`.
- Automated checks:

  ```bash
  go test -tags=integration ./integration_tests -run '^TestTUIIncludesStaticIgnoredDotCandidate$' -count=10
  ```

- Pass criteria: the current Dots screen reports `dots synced`; `x` renders `confirm include`; the second `x` persists the non-ignored dot entry.
- Cleanup: automatic temporary HOME, cache, config, git repo, process, and PTY cleanup.

## Fixtures And Environments

### Local Development

- Data fixtures: temporary `.claude` packages with conflict files, rooted/basename/broad ignores, nested selected subtrees, and ignored sibling subtrees.
- Environment setup: Go test mode plus a compiled-binary PTY fixture with temporary HOME/cache/config and a real temporary git dots repo.
- Mutation policy: `seeded_fixtures_only`.
- Known local limitations: live user dotfiles are intentionally outside the mutation boundary.

## Report Expectations

- Record targeted tests, full repository tests, and static analysis.
- Report only observed results and remaining gaps.

## Coverage Notes

- Tagged app integration tests verify the nested include matrix; the compiled-binary PTY E2E separately verifies asynchronous TUI readiness and confirmation.
