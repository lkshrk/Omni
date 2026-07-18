# Test Spec: Local-Only Dotfile Variants

**Scope**: feature
**Source material**: user request; `internal/app/dots_state.go`; `internal/tui/update_dots.go`
**Testing posture**: baked-in default (`TESTING.md` is absent)
**Test report**: [test-report.md](./test-report.md)

## Testing What

### Product Behaviors

- A top-level local-only dotfile exposes the inline `variant` action.
- Confirming the action stores local file or directory content in `<name>@<host>` and restows the local target from that package.
- `auto_commit` commits the new variant package; `auto_push` commits and pushes it.

### Implementation / System Invariants

- The host group records the variant package for the active host.
- A failed restow restores the original local content and leaves the seeded variant available for a retry.

### Risk-Based Behaviors

- Local content is not lost when stow fails after the variant source is created.
- Git automation uses the host-variant commit and does not silently omit the new package.

### Operational / Release Behaviors

- The focused variant tests, full Go suite, vet, and diff checks pass.

### Stakeholder Confidence Goals

- Users can choose `variant` for a local-only file or directory without data loss and with configured Git automation honored.

## Evidence Strategy

No upstream artifact declares P0-P3 priorities, so every behavior is `UNKNOWN`.

| What | Priority | Viable How | Selected How | Why | Residual Risk |
| --- | --- | --- | --- | --- | --- |
| Inline action and routing | UNKNOWN | unit, integration | unit | Deterministic TUI state and hint logic | None |
| File and directory variant adoption | UNKNOWN | integration | integration | Requires real filesystem and stow behavior | None |
| Failed restow preserves local data | UNKNOWN | integration | integration with controlled failing stow | Exercises the real compound operation | Staged config/package intentionally remain for retry |
| Auto-commit | UNKNOWN | integration | integration with local Git repo | Proves the actual Git boundary | Signing is disabled in the fixture |
| Auto-push | UNKNOWN | integration | integration with local bare remote | Proves commit reaches a remote without network flake | Hosted remote authentication is out of scope |
| Release regression gate | UNKNOWN | static, integration | full Go tests + vet + diff check | Matches repository verification practice | None |

Out of scope:

- Host-variant removal, already covered by existing tests.
- Hosted Git authentication and network availability.
- Platform behavior where GNU Stow is unavailable; affected integration tests skip explicitly.

## Test Cases

### DOTVAR-T01 Inline Action And Confirmation Routing

- Testing what: local-only rows expose and route `v variant`.
- Source refs: `internal/tui/dots_child_actions_test.go`, `internal/tui/cmd_test.go`.
- Automated checks:

  ```bash
  go test ./internal/tui -run 'TestDotsRowHints_DiscoveredLocalOnlyIncludesVariant|TestHandleDotsVariantKeyMsg_UsesAppActiveHostVariant' -count=1
  ```

- Pass criteria: hint contains `v`; first keypress arms create mode for the local-only candidate.
- Cleanup: Go test temporary directories.

### DOTVAR-T02 File And Directory Adoption

- Testing what: local content becomes an active host package and the target resolves to it.
- Source refs: `internal/app/dots_test.go`.
- Automated checks:

  ```bash
  go test ./internal/app -run TestDotsAddDiscoveredHostVariant_TracksLocalContentAsHostSpecificAndRestows -count=1
  ```

- Pass criteria: file and directory content match, config maps the active host, and local targets resolve to variant sources.
- Cleanup: Go test temporary directories.

### DOTVAR-T03 Restow Failure Recovery

- Testing what: a controlled stow failure does not destroy the original local file.
- Source refs: `internal/app/dots_test.go`.
- Automated checks:

  ```bash
  go test ./internal/app -run TestDotsAddDiscoveredHostVariant_RestowFailurePreservesLocalContent -count=1
  ```

- Pass criteria: operation returns the injected error; local content is restored as a real file; seeded source and host mapping remain available for retry.
- Cleanup: Go test temporary directories.

### DOTVAR-T04 Git Auto Actions

- Testing what: `auto_commit` commits and `auto_push` reaches a local bare remote.
- Source refs: `internal/app/dots_test.go`.
- Automated checks:

  ```bash
  go test ./internal/app -run TestDotsAddDiscoveredHostVariant_GitAutoActions -count=1
  ```

- Pass criteria: local and remote logs contain `dots: add gitconfig variant for work`.
- Cleanup: Go test temporary repositories.

### DOTVAR-T05 Repository Regression Gate

- Testing what: feature changes do not regress the repository.
- Automated checks:

  ```bash
  go test ./... -count=1
  go vet ./...
  git diff --check
  ```

- Pass criteria: every command exits zero.
- Cleanup: none.

## Fixtures And Environments

### Local Development

- Data fixtures: temporary HOME, dots repo, config, Git repo, and bare remote.
- External service fixtures: GNU Stow and Git binaries from PATH.
- Environment setup: tests isolate all writes under `t.TempDir()`.
- Mutation policy: `seeded_fixtures_only`.
- Known local limitations: stow-dependent cases skip when Stow is unavailable.

### Dev / Staging / Production

- Not used; all selected proofs are deterministic local integration tests.

## Report Expectations

- Record each command and result in [test-report.md](./test-report.md).
- Record skipped stow-dependent tests as `SKIPPED`, never as passes.

## Coverage Notes

- The failure test verifies the existing retryable staged-state contract rather than inventing full transaction rollback across config and Git.
- Local bare-remote push proves Git orchestration without adding network or credential flake.
