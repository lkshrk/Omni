# APM Native Onboarding Bug-Hunt Harness

## Goal

Find and repair onboarding failures at the Omni/APM boundary. A repair is complete only when a flow test reproduces the original command and state sequence, fails before the fix, and passes afterward.

## Inputs and outputs

- Inputs: failure transcript, `.omx/plans/apm-native-onboarding.md`, both repository states, and existing tests.
- Outputs: `_workspace/00_input/request-summary.md`, `_workspace/01_root-cause.md`, `_workspace/02_flow-test.md`, `_workspace/03_implementation.md`, `_workspace/04_test-report.md`, `_workspace/05_correctness-review.md`, `_workspace/05_boundary-review.md`, and `_workspace/06_final-summary.md`.
- The approved plan defines invariants and acceptance criteria. Resolve runnable commands against the current repositories and record any stale-path substitution in `_workspace/04_test-report.md`.

## Expert pool

| Role | Owns | Writes |
| --- | --- | --- |
| Leader | scope, synthesis, assignments, final verification | final summary |
| Debugger | state-machine trace and all callers of proposed change points | root-cause evidence |
| Dependency expert | exact APM version/install/protocol contract across both repos | root-cause evidence |
| Test engineer | executable user-flow regression and adjacent failure matrix | flow test and test report |
| Executor | smallest production fix in one assigned repository | implementation handoff |
| Verifier | independent correctness and cross-boundary review | review artifacts |

Read-only experts may run in parallel. Writers start only after the failing flow is locked. Stateful tests run sequentially in isolated temporary homes.

## Flow contract

The primary recovery flow is:

1. Start with an incomplete onboarding journal and the previously pinned APM build.
2. Run `omni doctor --fix`; repair must be reachable and install the exact current APM pin.
3. Run `omni agents onboard resume --operation ID`; recovery must be reachable with the new pin.
4. Run a normal Omni command; initialization must succeed after the journal reaches a terminal phase.

Tests use local stubs or repository fixtures only. No network, user home, or live APM state.

## Failure policy

- Production edits cannot start until the reported flow fails. If it cannot be reproduced, stop as `UNCONFIRMED` and record the environment delta.
- Conflicting expert conclusions return to the leader with cited evidence; no writer chooses silently.
- A failed writer returns ownership to the leader; another repository is not edited without reassignment.
- Stop after three repair/review loops and report the remaining blocker.
- Preserve incomplete journals and test homes on unexpected failures when the test harness supports it.

## Validation

- Normal flow: current pin, no incomplete journal, all commands remain usable.
- Reported failure flow: stale pin plus `preflighted` journal reaches terminal recovery.
- Boundary checks: doctor repair does not mutate onboarding state; resume still rejects stale APM; normal startup still blocks non-terminal journals.
- Focused tests precede full Go tests and race/static checks applicable to touched packages.
