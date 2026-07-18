# Test Report: Local-Only Dotfile Variants

**Test spec**: [test-spec.md](./test-spec.md)
**Branch / commit**: `main` at `8d5a60c` plus the test changes recorded here
**Last updated**: 2026-07-17
**Tester**: Codex

## Summary

- Overall session status: `PASS`
- What this session added or strengthened: controlled restow-failure recovery, exact auto-commit subject, and exact bare-remote auto-push subject.
- Blocking findings: none.
- Known gaps left for follow-up: none within the scoped local behavior; hosted Git authentication remains out of scope.

## Source Material

- Source material used: user requests; `internal/app/dots_state.go`; `internal/tui/update_dots.go`; existing dots, rollback, stow, and Git tests.
- Source material not found or not available: no PRD or priority declaration; behavior priorities remain `UNKNOWN`.

## Commands Run

| Command | Result | Notes |
| --- | --- | --- |
| `go test ./internal/app -run 'TestDotsAddDiscoveredHostVariant_(RestowFailurePreservesLocalContent\|GitAutoActions)' -count=1 -v` | `PASS` | Failure recovery, auto-commit, and auto-push passed in 0.765s. |
| `go test ./internal/app -run 'TestDotsAddDiscoveredHostVariant\|TestDotStatusVariantEligible' -count=1` | `PASS` | All app-level variant and eligibility cases passed in 1.314s. |
| `go test ./internal/tui -run 'TestDotsRowHints_DiscoveredLocalOnlyIncludesVariant\|TestHandleDotsVariantKeyMsg_UsesAppActiveHostVariant' -count=1` | `PASS` | Inline action and routing passed in 0.239s. |
| `go test ./... -count=1` | `PASS` | Full repository suite passed; app 199.361s, TUI 106.376s. |
| `go vet ./...` | `PASS` | No findings. |
| `git diff --check` | `PASS` | No whitespace errors. |

## Tests Added Or Updated

| Type | Files | Tests |
| --- | ---: | ---: |
| Unit | 0 | 0 |
| Contract | 0 | 0 |
| Integration | 1 | 3 |
| E2E | 0 | 0 |
| Agent | 0 | 0 |
| Script / static | 0 | 0 |
| **Total** | **1** | **3** |

### File List

- `internal/app/dots_test.go` (3 integration scenarios: restow failure, auto-commit, auto-push)

## Coverage Matrix

| Behavior (from test-spec) | Priority | Test type | Coverage | Session result | Notes / gap |
| --- | --- | --- | --- | --- | --- |
| Inline action and routing | UNKNOWN | unit | COVERED | PASS | Existing focused TUI tests rerun. |
| File and directory variant adoption | UNKNOWN | integration | COVERED | PASS | Existing real-stow file/directory test rerun. |
| Failed restow preserves local data | UNKNOWN | integration | COVERED | PASS | Controlled stow shim fails only the restow; local bytes and retry state verified. |
| Auto-commit | UNKNOWN | integration | COVERED | PASS | Exact local commit subject verified. |
| Auto-push | UNKNOWN | integration | COVERED | PASS | Exact commit subject verified in a local bare remote. |
| Release regression gate | UNKNOWN | integration, static | COVERED | PASS | Full Go suite, vet, and diff check passed. |

## Agent Test Evidence

- None; no live environment or browser proof was required.

## Manual Verification Log

- None; selected behaviors are fully automated locally.

## Findings

- None.

## Deferred / Residual Risk

- None within scope. Hosted remote authentication and network behavior are explicitly out of scope.

## Cleanup

- Cleanup performed: Go test cleanup removed temporary HOME directories, repositories, bare remotes, and the failing stow shim.
- Resources intentionally left behind: none.
- Follow-up cleanup required: none.

## Coverage Summary

- Total testing whats: 6
- COVERED: 6
- PARTIAL: 0
- IMPLICIT: 0
- NOT COVERED: 0
- NOT MEASURED: 0
- MANUAL: 0
- BLOCKED: 0
- DEFERRED: 0
