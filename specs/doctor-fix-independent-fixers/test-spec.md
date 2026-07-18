# Test Spec: Independent Doctor Fixers

**Scope**: `doctor --fix` CLI and TUI orchestration
**Source material**: `design.md`, P2 review finding, user-requested gap closure
**Testing posture**: baked-in default (`TESTING.md` absent)
**Test report**: [test-report.md](./test-report.md)

## Testing What

### Product Behaviors

- A config optimizer failure does not prevent safe `dots.ignore` cleanup.
- CLI and TUI expose the partial outcome instead of implying that every fixer failed.

### Implementation / System Invariants

- Both fixers execute independently on a real config/include chain.
- A partial filesystem mutation refreshes the TUI Doctor snapshot.
- Dry-run does not execute ignore cleanup.

### Risk-Based Behaviors

- A safety-aborted include optimization must not suppress an unrelated safe root-config repair.

### Operational / Release Behaviors

- Focused integration tests and the repository suite remain deterministic under `TERM=dumb`.

### Stakeholder Confidence Goals

- Users can trust `doctor --fix` to apply every independently safe repair and accurately report partial failures.

## Evidence Strategy

| What | Priority | Viable How | Selected How | Why | Residual Risk |
| --- | --- | --- | --- | --- | --- |
| Shared fixer sequencing | P2 | unit, integration | unit | Fast deterministic proof of both callbacks | None |
| CLI partial optimizer failure | P2 | integration | integration | Must prove command output plus persisted root config | None |
| TUI partial optimizer failure | P2 | integration | integration | Must execute the real dispatched command and inspect its message plus disk state | Rendering is covered separately by status-message unit tests |
| TUI partial outcome refresh | P2 | unit | unit | State/message transition is deterministic without a live terminal | None |
| Dry-run skips ignore cleanup | P2 | unit, CLI integration | unit + existing CLI integration | Shared sequencer proves callback exclusion; existing CLI test proves no writes | None |

Out of scope:

- Config optimizer equivalence logic itself.
- Live interactive terminal rendering.
- Fixers other than include optimization and `dots.ignore` cleanup.

## Test Cases

### DFIF-T01 Shared Sequencing

- Testing what: ignore cleanup runs after optimizer failure; dry-run skips it.
- Source refs: `internal/app/config_optimize.go`.
- Automated checks:
  ```bash
  go test ./internal/app -run 'TestRunDoctorFixers' -count=1
  ```
- Pass criteria: real mode calls both callbacks and retains both outcomes; dry-run calls only optimizer.
- Cleanup: none.

### DFIF-T02 CLI Partial Failure Integration

- Testing what: a real equivalence-check abort still permits root ignore cleanup.
- Source refs: `internal/cli/doctor.go`.
- Automated checks:
  ```bash
  go test ./internal/cli -run 'TestDoctorFixOptimizerFailureStillCleansIgnorePatterns' -count=1
  ```
- Pass criteria: command returns optimizer error, prints ignore-cleanup success, and persisted ignore patterns are cleaned.
- Cleanup: temporary config tree and permissions restored by `testing.T` cleanup.

### DFIF-T03 TUI Partial Failure Integration

- Testing what: the real TUI fix command returns separate outcomes after the same equivalence-check abort.
- Source refs: `internal/tui/update_status.go`.
- Automated checks:
  ```bash
  go test ./internal/tui -run 'TestDashboardDoctorFixCommandPartialFailure' -count=1
  ```
- Pass criteria: completion message contains optimizer error plus modified dot name; root config is cleaned.
- Cleanup: temporary config tree and permissions restored by `testing.T` cleanup.

## Fixtures And Environments

### Local Development

- Data fixtures: temporary root config and fragment whose unnamed duplicate group triggers the optimizer's equivalence safety abort, plus a named root dot entry with dead ignore patterns.
- External services: none.
- Mutation policy: temporary files only.
- Known local limitations: none.

## Report Expectations

- Record exact commands and PASS/SKIPPED status in `test-report.md`.
- Report any environment skip as residual risk; never treat it as a pass.

## Coverage Notes

- The real integration fixture is required because synthetic completion messages cannot prove disk routing.
- Unit coverage remains the cheapest capable proof for callback ordering and dry-run exclusion.
