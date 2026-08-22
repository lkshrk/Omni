# APM Native Onboarding Harness

## Goal

Implement the approved APM-native onboarding plan across APM and Omni with inspectable producer, test, and review handoffs.

## Pattern

Pipeline with a parallel producer phase and a producer-reviewer loop:

```text
APM producer ─┐
              ├─ integration synthesis ─ test agent ─ correctness review ─ boundary review ─ final verification
Omni producer ┘
```

The leader owns synthesis, shared protocol decisions, branch state, and final acceptance.

## Ownership

| Role | Exclusive write scope | Handoff |
| --- | --- | --- |
| APM implementation | `/home/coder/apm` | `_workspace/01_apm_implementation.md` |
| Omni implementation | `/home/coder/omni` production/tests, excluding other handoffs | `_workspace/01_omni_implementation.md` |
| Test | Tests only after producer handoff; no production edits | `_workspace/02_test_report.md` |
| Correctness review | Read-only | `_workspace/03_correctness_review.md` |
| Boundary review | Read-only | `_workspace/03_boundary_review.md` |

Agents share the filesystem. They must not revert another lane, change branches, commit, or modify another lane’s handoff. Stateful test suites run serially unless isolated.

## Phases

1. Producers implement the smallest complete APM and Omni slices from `.omx/plans/apm-native-onboarding.md`.
2. Leader reconciles protocol names, schemas, and pinned-binary consumption.
3. Test agent runs focused unit/integration tests and adds only missing regression tests.
4. Review agents independently check correctness and cross-repository boundary coherence.
5. Producers repair owned blockers; test and review repeat up to three rounds.
6. Leader runs full suites, DinD, lint, build, docs, schema, and worktree checks.

## Failure policy

- Compile/protocol blockers return immediately to the owning producer.
- A failing stateful test is rerun serially before being classified flaky.
- Conflicting reviews are resolved by the approved plan and executable evidence.
- Any security, data-loss, or recovery finding blocks completion.
- After three review rounds, unresolved blockers are reported rather than waived.

## Acceptance

- Both repository handoffs list changed files and focused passing tests.
- Cross-repository JSON golden fixtures match.
- Dry-run is byte/path read-only.
- Apply, crash recovery, exclusion durability, locking, secret redaction, and idempotency gates pass.
- Linux DinD and required platform-contract tests are wired into CI.
- Correctness and boundary reviewers approve.

