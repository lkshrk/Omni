# Test Report: System Package Inventory

**Test spec**: [test-spec.md](./test-spec.md)
**Branch / commit**: `main` / `5b7616e` plus this test change
**Last updated**: 2026-07-18
**Tester**: Codex

## Summary

- Overall session status: `PASS`
- Added provider-matrix, concurrent idempotency, and persistence-failure integration coverage.
- Blocking findings: none.
- Known gaps: multi-process config writers and physical filesystem failure injection.

## Source Material

- User-reported Out of Sync system-package regression.
- `internal/app/app_search.go`, `internal/app/app_membership.go`, and existing inventory tests.

## Commands Run

| Command | Result | Notes |
| --- | --- | --- |
| `go test ./internal/app -run 'TestRefreshDiscovered_(AutoGroupsEverySystemPackageProvider\|SystemInventoryIsIdempotentUnderConcurrentRefresh\|InventoryPersistenceFailureDoesNotWriteDiscoveredDB)' -count=1` | `PASS` | All three added integration tests passed |
| `go test -race ./internal/app -run TestRefreshDiscovered_SystemInventoryIsIdempotentUnderConcurrentRefresh -count=1` | `PASS` | No race detected; membership remained unique |
| `go test ./internal/app -run 'TestRefreshDiscovered_(AutoGroupsSystemPackagesAsInventory\|AutoGroupsEverySystemPackageProvider\|SystemInventoryIsIdempotentUnderConcurrentRefresh\|InventoryPersistenceFailureDoesNotWriteDiscoveredDB)\|TestSetToolGroups(CreatesProviderInventoryMarker\|RemovesProviderInventoryAndRestoresHost)' -count=1` | `PASS` | Complete inventory regression set passed |

## Tests Added Or Updated

| Type | Files | Tests |
| --- | ---: | ---: |
| Integration | 1 | 3 |
| **Total** | **1** | **3** |

### File List

- `internal/app/orphan_test.go` (3 tests, including 5 provider subtests)

## Coverage Matrix

| Behavior (from test-spec) | Priority | Test type | Coverage | Session result | Notes / gap |
| --- | --- | --- | --- | --- | --- |
| Classify apt/dnf/pacman/apk/zypper discoveries | UNKNOWN | integration | COVERED | PASS | Five provider subtests |
| Preserve configured and non-OS tools | UNKNOWN | integration | COVERED | PASS | Existing mixed apt/brew test |
| Concurrent idempotency | UNKNOWN | integration | COVERED | PASS | Also passed under race detector |
| Persistence failure ordering | UNKNOWN | integration | COVERED | PASS | Rejected reserved-group fixture leaves DB empty |

## Findings

- None.

## Deferred / Residual Risk

- [ ] Multi-process writes are not simulated. Retest if config locking expands beyond one `App` process.
- [ ] Physical disk-full/rename failures are not injected; deterministic config rejection proves the same write-order invariant.

## Cleanup

- Temporary configs and databases are owned and removed by Go test fixtures.
- No external resources remain.

## Coverage Summary

- Total testing whats: 4
- COVERED: 4
- PARTIAL: 0
- IMPLICIT: 0
- NOT COVERED: 0
- NOT MEASURED: 0
- MANUAL: 0
- BLOCKED: 0
- DEFERRED: 0
