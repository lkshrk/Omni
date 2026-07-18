# Test Spec: Test-suite overhaul

**Scope**: repository test strategy, runtime, and program-flow coverage
**Source material**: user request; `cmd/omni`; `internal/actions/catalog.go`; CLI/TUI/app/provider/config/database/dots/sync code; existing tests and docs
**Testing posture**: baked-in default; no repo-root `TESTING.md`
**Test report**: [test-report.md](./test-report.md)

This contract describes the actual user journeys that must remain trusted while low-value duplicate tests are removed. No upstream artifact declares priorities, so priority is recorded as `UNKNOWN` rather than invented.

## Testing What

### Product Behaviors

| ID | Actual program flow | Observable result |
| --- | --- | --- |
| TS-01 | Bootstrap and host activation | Config and DB initialize; the selected host reloads as active. |
| TS-02 | Single-tool lifecycle | Set/group/install/list/upgrade/reinstall/delete persists provider and DB state correctly. |
| TS-03 | Bulk sync and prune | Dry-run is non-mutating; claim/install/prune/failure/retry change only intended tools. |
| TS-04 | Provider routing and fallback | Priority, disabled/unavailable providers, weak matches, fallback, and switching select the intended executor. |
| TS-05 | Reconcile | Tools, dots, and Git steps run in order and preserve partial-failure evidence. |
| TS-06 | Dotfile lifecycle | Adopt/discover/sync/status/extract/variant/unignore/delete persist config and filesystem state. |
| TS-07 | Dotfile safety and services | Conflicts, nested ignores, rollback, pull/commit/push, reminder, and watch paths preserve user data. |
| TS-08 | Hosts, groups, settings, and migration | Mutations persist across reload; lint and migrations preserve unrelated config. |
| TS-09 | Agents, skills, MCP, plugins, and marketplaces | Add/import/group/remove/restore/update reflect adapter and manifest state across agents. |
| TS-10 | TUI parity | Representative tool mutation, dot conflict, agent restore, navigation, progress, confirmation, and error paths reach the same app behavior as CLI. |
| TS-11 | Provider contracts | Provider families translate install/query/upgrade/uninstall and privilege behavior consistently. |

### Implementation / System Invariants

- Normal test runs do not forcibly invalidate the Go test cache.
- Integration CI does not repeat the full unit race suite.
- Parallel tests own isolated temp/config/cache/DB state and do not mutate process-global state.
- Race detection remains a release gate.
- Unit tests retained after cleanup own focused logic that integration journeys cannot diagnose cheaply.
- Every removed unit test has a named stronger replacement.

### Risk-Based Behaviors

- Destructive tool and dotfile operations do not delete unrelated state.
- Failed, cancelled, or partially completed multi-step mutations persist actionable failure state.
- Provider fallback and privilege routing do not execute an unintended command.
- Config and DB migrations preserve unrelated fields and rows.

### Operational / Release Behaviors

- Local repeated tests reuse valid cached results.
- CI unit shards and integration work run independently.
- Package-manager container tests remain separately invokable and auditable.
- Docker integration runs only integration-tagged coverage.

### Stakeholder Confidence Goals

- A contributor can identify which journey protects a user-visible behavior.
- A failing integration test names the broken program flow, not an internal helper shape.
- The common edit-test loop completes without a forced cold suite.

## Evidence Strategy

| What | Priority | Viable How | Selected How | Why | Residual Risk |
| --- | --- | --- | --- | --- | --- |
| TS-01 through TS-09 | UNKNOWN | integration, contract, unit | txtar + integration Go tests | Real CLI, config, DB, filesystem, and provider seams at low fixture cost. | External package managers stay mocked except container lane. |
| TS-10 | UNKNOWN | e2e, integration | PTY binary integration + focused model tests | Only a real terminal proves rendered routing and persistence. | Full TUI action parity is intentionally sampled. |
| TS-11 | UNKNOWN | contract, integration | provider table/unit contracts + container tests | Provider output parsing needs diagnostics; real managers need isolation. | Container lane may be unavailable locally. |
| Runtime invariants | UNKNOWN | script, static, race | timed commands + `-race` | Directly measures the claimed speed/safety change. | CI runner variance. |

Out of scope:

- Live production telemetry or third-party service verification.
- Exhaustive terminal rendering snapshots.
- Running destructive package-manager tests on the developer host.

## Test Cases

### TSO-T01 Core CLI journeys

- Testing what: TS-01 through TS-09 state-changing CLI flows.
- Source refs: `integration_tests/cli_test.go`, `integration_tests/testdata/scripts`.
- Preconditions: GNU Stow and integration build dependencies in the isolated environment.
- Automated checks:

  ```bash
  go test -tags=integration -race -count=1 ./integration_tests -run TestCLI
  ```

- Steps:
  1. Run the txtar journey set through real `cli.Execute` subprocesses.
  2. Assert command output and persisted config/DB/filesystem state.
- Pass criteria: all selected journey fixtures pass.
- Cleanup: testscript removes isolated work directories.
- If not executable: mark `BLOCKED` when required integration dependencies are unavailable.

### TSO-T02 App/provider/DB integration

- Testing what: TS-02 through TS-05 and TS-11.
- Source refs: `integration_tests/sync_test.go`, provider integration tests.
- Preconditions: isolated temp directories.
- Automated checks:

  ```bash
  go test -tags=integration -race -count=1 ./integration_tests ./internal/provider/...
  ```

- Steps:
  1. Execute app operations through registered providers.
  2. Verify provider calls and SQLite state.
- Pass criteria: operations and persisted state match each scenario.
- Cleanup: Go test cleanup closes DBs and removes temp directories.
- If not executable: mark `BLOCKED`.

### TSO-T03 Binary TUI parity

- Testing what: TS-10 representative mutations and recovery.
- Source refs: `integration_tests/tui_*_integration_test.go`.
- Preconditions: PTY support and isolated config/cache/home.
- Automated checks:

  ```bash
  go test -tags=integration -race -count=1 ./integration_tests -run 'TestTUI'
  ```

- Steps:
  1. Build and start the real binary in a PTY.
  2. Drive keys, wait for rendered state, and verify persisted results.
- Pass criteria: representative tool, dot, and agent flows reach the expected screen and durable state.
- Cleanup: terminate PTY processes and remove temp directories.
- If not executable: mark `BLOCKED`.

### TSO-T04 Parallel runtime safety

- Testing what: cache retention, selected test parallelism, and race freedom.
- Source refs: `Makefile`, changed app/TUI tests.
- Preconditions: Go toolchain.
- Automated checks:

  ```bash
  bash scripts/run-test-safe.sh go test -race -count=1 ./internal/app ./internal/tui
  make test
  make test
  ```

- Steps:
  1. Run fresh race verification for parallelized tests.
  2. Run the normal target twice and compare elapsed/cache signals.
- Pass criteria: no races or failures; the second normal run does not start by clearing the test cache.
- Cleanup: wrapper removes its isolated root.
- If not executable: mark `BLOCKED`.

### TSO-T05 Full release checks

- Testing what: repository compilation, static checks, unit/integration compatibility.
- Source refs: `Makefile`, `.github/workflows/ci.yml`, `Dockerfile.test`.
- Preconditions: Go; Docker and lint tool when available.
- Automated checks:

  ```bash
  go build ./...
  go vet ./...
  bash scripts/run-test-safe.sh go test -race -trimpath ./...
  make test-integration
  make lint
  ```

- Steps:
  1. Run targeted checks first.
  2. Run the full race suite and available release checks.
- Pass criteria: every executed command reports `PASS`; unavailable capabilities are recorded, not assumed.
- Cleanup: prune only task-owned temporary artifacts.
- If not executable: mark unavailable Docker/lint checks `BLOCKED` or `SKIPPED` with reason.

## Fixtures And Environments

### Local Development

- Use `scripts/run-test-safe.sh` for isolated HOME/XDG/config/cache state.
- Never run integration package-manager or dotfile mutations against the real host.

### Dev

- Not required.

### Staging

- Not required.

### Production

- No production execution or data is required.

## Report Expectations

- Record each command as `PASS`, `FAIL`, `PARTIAL`, `BLOCKED`, `SKIPPED`, `NOT RUN`, `DEFERRED`, `ABORTED`, or `UNKNOWN`.
- Put blocking failures first.
- Record changed tests, exact replacement evidence, cleanup, and intentionally unrun capabilities.
- Do not include credentials, tokens, private paths, or customer data.

## Coverage Notes

- Map every TS behavior to at least one row in `test-report.md`.
- Treat priorities as `UNKNOWN` until an owner supplies a declared P0-P3 source.
- Prefer one journey that proves a state transition over many helper-shape assertions.
- Keep focused units where they give faster diagnosis for pure logic or safety boundaries.
