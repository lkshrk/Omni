# Test Spec: Dotfile Child Conflict Resolution

**Scope**: feature
**Source material**: user report; `internal/tui/update_dots.go`; `internal/tui/confirm_timeout.go`
**Testing posture**: baked-in default; no repo-root `TESTING.md`
**Test report**: [test-report.md](./test-report.md)

## Testing What

### Product Behaviors

- A child-row `use repo` confirmation executes and refreshes the selected nested path as synced.
- A child-row `use local` confirmation adopts local content, relinks it, and refreshes the nested path as synced.
- The first action key visibly asks for a second press and names the nested child.
- Timeout or cancellation clears both the pending action and its prompt.
- The prompt reflects the active key binding.

### Implementation / System Invariants

- Nested resolution works with many sibling conflicts.
- Default and configured ignored local-only paths survive both strategies.
- A successful resolution leaves the local managed path linked to the repo source.

### Risk-Based Behaviors

- Confirmation remains mandatory before destructive conflict resolution.
- Ignored local state is not deleted or adopted accidentally.

### Operational / Release Behaviors

- TUI tests, repository tests, and Go static analysis pass.

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

## Fixtures And Environments

### Local Development

- Data fixtures: temporary `.claude` package with 24 conflicting agent files, `.cache/state.json`, and `projects/session.json`.
- Environment setup: Go test mode and temporary HOME.
- Mutation policy: `seeded_fixtures_only`.
- Known local limitations: no live user dotfiles are mutated.

## Report Expectations

- Record targeted tests, full repository tests, and static analysis.
- Report only observed results and remaining gaps.

## Coverage Notes

- Integration tests are the lowest-cost proof capable of verifying resolver side effects and refreshed TUI state together.
