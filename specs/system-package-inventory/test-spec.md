# Test Spec: System Package Inventory

**Scope**: feature
**Source material**: user-reported TUI discovery regression; `internal/app/app_search.go`; `internal/app/app_membership.go`
**Testing posture**: baked-in default
**Test report**: [test-report.md](./test-report.md)

## Testing What

### Product Behaviors

- Newly discovered OS-provider packages are classified into `provider-inventory` and do not remain in the TUI's Out of Sync list.
- Configured OS-provider tools remain user-managed, while non-OS discoveries remain visible.

### Implementation / System Invariants

- The reserved inventory group is created or upgraded with its special marker.
- Repeated and concurrent discovery creates one logical tool and one inventory membership.
- A configuration persistence failure occurs before any discovered DB row is written.

## Evidence Strategy

| What | Priority | Viable How | Selected How | Why | Residual Risk |
| --- | --- | --- | --- | --- | --- |
| Classify apt/dnf/pacman/apk/zypper discoveries | UNKNOWN | integration | integration | Exercises config and DB boundaries | Provider command implementations are covered separately |
| Preserve configured and non-OS tools | UNKNOWN | integration | integration | Exercises mixed discovery input | None identified |
| Concurrent idempotency | UNKNOWN | integration, race | integration + race | Proves serialization and duplicate prevention | Does not simulate multiple processes |
| Persistence failure ordering | UNKNOWN | integration | integration | Deterministic rejected-config fixture | Does not emulate disk-full/rename failure |

Out of scope:

- Multi-process writers to the same config.
- Live package-manager command correctness, covered by provider tests.

## Test Cases

### SPI-T01 OS Provider Classification

- Automated checks:
  ```bash
  go test ./internal/app -run TestRefreshDiscovered_AutoGroupsEverySystemPackageProvider -count=1
  ```
- Pass criteria: each supported OS provider persists the new package in the marked inventory group.

### SPI-T02 Mixed Discovery Safety

- Automated checks:
  ```bash
  go test ./internal/app -run TestRefreshDiscovered_AutoGroupsSystemPackagesAsInventory -count=1
  ```
- Pass criteria: configured apt stays configured, apt inventory is hidden, and brew remains discovered.

### SPI-T03 Concurrent Idempotency

- Automated checks:
  ```bash
  go test -race ./internal/app -run TestRefreshDiscovered_SystemInventoryIsIdempotentUnderConcurrentRefresh -count=1
  ```
- Pass criteria: concurrent refreshes succeed without a race and persist exactly one membership.

### SPI-T04 Persistence Failure Ordering

- Automated checks:
  ```bash
  go test ./internal/app -run TestRefreshDiscovered_InventoryPersistenceFailureDoesNotWriteDiscoveredDB -count=1
  ```
- Pass criteria: rejected inventory persistence returns an error and leaves no discovered DB row.

## Fixtures And Environments

### Local Development

- Data fixtures: temporary config and SQLite DB created by `newImportApp`.
- External service fixtures: deterministic provider stubs.
- Mutation policy: seeded fixtures only.

## Report Expectations

- Record each command and result in `test-report.md`.
- Treat any race detector finding or duplicate membership as a failure.

