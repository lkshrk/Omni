# Test-suite cleanup plan

## Baseline

- 302 test files, 130,577 test lines, 4,394 top-level tests.
- `internal/app`: 1,384 serial tests; fresh race run about 275 seconds.
- `internal/tui`: 1,633 serial tests; fresh race run about 124 seconds.
- No repository test calls `t.Parallel()` directly.
- The 157 txtar CLI journeys already run in parallel through `testscript.Run`.
- `make test` deletes the test cache before every run.
- Docker integration repeats the full race unit suite already run by CI.

## Behavior lock

Before deleting a unit test, require one of:

1. An exact stronger integration test that exercises the same observable behavior.
2. A new integration journey added and passing in this change.
3. A retained focused unit test for pure parsing, validation, migration, rollback, or provider-output logic.

Do not delete broad TUI flow coverage while binary TUI coverage remains sparse.

## Pass 1: remove avoidable runtime

1. Stop clearing the Go test cache in `make test`.
2. Stop rerunning the entire unit suite in the Docker integration stage.
3. Keep race detection in CI and full verification.
4. Add `t.Parallel()` only to profiler-selected isolated tests; exclude tests using process-global environment, working directory, standard streams, or mutable package globals.
5. Run the selected tests with `-race -count=1` before and after parallelization.

## Pass 2: fill actual journey gaps

Add or extend txtar journeys for:

- `tools sync --prune`.
- `tools normalize` and `tools migrate-nvm` maintenance.
- `doctor --dry-run` and `doctor --fix` with persisted reload.
- `settings lint` and `settings migrate-host-overrides`.
- dots unignore and reminder/watch run/check paths.

Add one binary TUI tool-mutation journey if it can reuse the existing PTY harness without a new abstraction.

P3 completion and explicit `ui` alias smokes are optional if they add measurable runtime without protecting state-changing behavior.

## Pass 3: delete exact duplicates

- Remove six app lifecycle units superseded by `integration_tests/sync_test.go` app/provider/SQLite tests.
- Remove weak CLI smoke tests superseded by named txtar fixtures.
- Remove five TUI/app units superseded by real-binary PTY or real-git integration tests.
- Collapse only obviously repetitive render/helper clusters when one table-driven invariant test preserves the observable contract.

## Pass 4: verify

1. Run new/changed integration fixtures.
2. Run affected app, CLI, and TUI packages with `-race -count=1`.
3. Run `go test -race -trimpath ./...` through the isolated wrapper.
4. Run the integration-tagged Docker suite when Docker is available.
5. Run `go vet ./...`, build, lint if installed, and the shell regression test.
6. Record exact commands and results in `test-report.md`.

## Stop condition

- Actual flow inventory is documented.
- Added journeys pass.
- Deleted tests have named stronger replacements.
- Fresh race verification passes.
- Incremental `make test` retains cache behavior.
- Docker integration no longer duplicates the unit suite.
