# Test Report: Test-suite overhaul

**Result**: PASS
**Spec**: [test-spec.md](./test-spec.md)
**Cleanup plan**: [cleanup-plan.md](./cleanup-plan.md)

## Summary

The suite now has an explicit 11-flow coverage contract, seven added or extended CLI journeys, and eight real-binary TUI journey families backed by 14 focused cases. Thirty-two unit tests (677 lines) with named stronger integration replacements were removed. The normal target runs shell regressions and the race suite concurrently, preserves valid Go cache entries, and the Docker integration target no longer repeats the full unit suite.

Cold execution remains bounded by the intentionally serial, global-state-heavy `internal/app` package (300.105s). A verified warm `make -j2 test` completed in 5.023s with the heavy packages cached. Broad in-package `t.Parallel()` was rejected because 853 `t.Setenv` calls plus chdir and package-global state make it unsafe without a larger harness refactor.

## Source Material

- User request and repository `AGENTS.md`
- `cmd/omni`, `internal/actions/catalog.go`, CLI/TUI/app/provider/config/database/dots/sync code
- 302 baseline test files, 4,394 baseline top-level tests, 157 baseline txtar journeys
- Existing integration tests and `docs/test-matrix.md`

## Commands Run

| Command / check | Status | Evidence |
| --- | --- | --- |
| `make -j2 test` (cold) | PASS | 5:11.54 wall time; full shell and race suites passed, `internal/app` 300.105s and `internal/tui` 126.381s. |
| `make -j2 test` (warm) | PASS | 5.023s wall time; dominant packages reported `(cached)`. |
| Changed CLI txtar journeys under `-race` | PASS | 16.542s. |
| Full real-terminal TUI slice | PASS | 14 cases plus current-screen regression completed in 21.235s. |
| Full real-terminal TUI slice under `-race` | PASS | 21.435s; no races. |
| Timing stress | PASS | First-run count 10, navigation count 10, dot/admin/Agents/reconcile count 3 all passed. |
| Dot conflict/rollback tests under `-race -count=100` | PASS | 7.820s after deterministic timestamp fixture repair. |
| `go build ./...` | PASS | All packages built. |
| `go vet ./...` | PASS | No findings. |
| `make lint` | PASS | 0 issues. |
| `bash scripts/run-test-safe.sh bash scripts/test-release.sh` | PASS | Shell regression suite passed. |
| `make test-integration` | PASS | Integration-tagged `integration_tests` and provider suites passed in Docker; cached confirmation exited 0. |
| Workflow YAML parse and `git diff --check` | PASS | Valid YAML; no whitespace errors. |
| `make test-package-managers` | NOT RUN | Separate destructive real-manager container lane; unchanged and outside the required host-safe verification. |

## Tests Added Or Updated

- Added `doctor-fix.txtar`, `settings-maintenance.txtar`, and `tools-maintenance.txtar`.
- Extended tool sync/prune, dot unignore, reminder-service, and watch-service journeys.
- Migrated the real-binary harness from accumulated PTY transcripts to `x/vttest` current-screen emulation with one cached binary build.
- Added first-run, resize/help/search, install/delete cancel-confirm, reconcile failure/retry, dot conflict cancel-resolve, Agents plugin install, groups/settings persistence, and nested admin-terminal journeys.
- Made TUI batch-command test helpers reflect Bubble Tea concurrency and made the shared stub race-safe.
- Fixed bulk tool row failures being surfaced as success during reconcile and preserved reconcile outcomes across doctor refreshes.
- Stabilized dot conflict fixtures by pinning competing file timestamps.
- Updated the test guard to protect cache-preserving `test-unit` behavior.

## Tests Removed

Removed 32 exact replacement-backed tests across seven app, CLI, and TUI files. Retained focused parser, validation, rollback, routing, and safety-boundary units where they provide cheaper diagnosis than a journey test.

## Coverage Matrix

| ID | Priority | Coverage | Status | Evidence |
| --- | --- | --- | --- | --- |
| TS-01 Bootstrap and host activation | UNKNOWN | Integration | PASS | Existing CLI bootstrap/host journeys in the full txtar run. |
| TS-02 Single-tool lifecycle | UNKNOWN | Integration | PASS | Existing lifecycle journey plus real PTY install journey. |
| TS-03 Bulk sync and prune | UNKNOWN | Integration | PASS | Extended provider lifecycle journey executes real sync/prune. |
| TS-04 Provider routing and fallback | UNKNOWN | Integration + contract | PASS | Full CLI and tagged provider suites. |
| TS-05 Reconcile | UNKNOWN | Integration | PASS | Existing reconcile journeys and app integration coverage. |
| TS-06 Dotfile lifecycle | UNKNOWN | Integration | PASS | Existing journeys plus added unignore transition. |
| TS-07 Dotfile safety and services | UNKNOWN | Integration + focused unit | PASS | Conflict/rollback stress run plus reminder/watch journeys. |
| TS-08 Hosts, groups, settings, migration | UNKNOWN | Integration | PASS | Added settings lint/migration maintenance journey plus existing host/group flows. |
| TS-09 Agents, skills, MCP, plugins, marketplaces | UNKNOWN | Integration | PASS | Existing CLI and PTY agent/plugin journeys in the full suite. |
| TS-10 TUI parity | UNKNOWN | Real-terminal integration + model tests | PASS | Eight interaction families cover the distinct shell/modal/progress/error/nested-PTY contracts; action permutations remain at cheaper layers. |
| TS-11 Provider contracts | UNKNOWN | Contract + Docker integration | PASS | Tagged provider suites passed; separate real package-manager lane was not run. |
| Runtime invariants | UNKNOWN | Script + static + race | PASS | Cold/warm runs, race suite, test guard, CI YAML, and Docker target. |

## Residual Risks

- Cold time remains high because app/TUI tests use process-global environment, working-directory, and package state. Safe direct parallelism requires a dedicated harness-isolation refactor.
- `x/vttest` is experimental and currently requires a matching explicit `x/vt` revision. Its public `SendText`, `Snapshot`, and `Close` methods have lock/race defects, so the harness uses the concurrency-safe emulator surface and lets package exit reclaim its bounded PTYs.
- Real package-manager container tests were not rerun; provider integration contracts did run in Docker.
- Tool normalization mutation is not expressible from a valid current v17 config because loading migrates or rejects legacy state; the journey covers the valid no-op and NVM migration paths.

## Changed Surface

- Runtime: `Makefile`, CI workflow, `Dockerfile.test`, development docs, and test guard.
- Coverage: CLI txtar fixtures, `x/vttest` real-binary integration, dot conflict fixture, and test matrix.
- Product fix: reconcile now treats per-row bulk failures as errors and preserves the outcome through dashboard refresh.
- Simplification: deletion-only cleanup of replacement-backed unit tests plus one test-only terminal dependency.
