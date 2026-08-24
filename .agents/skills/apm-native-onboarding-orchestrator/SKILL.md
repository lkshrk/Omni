---
name: apm-native-onboarding-orchestrator
description: Hunt and repair APM-native onboarding failures across Omni and APM with flow-based integration tests and independent review.
---

# APM Native Onboarding Orchestrator

## When to use

Use for implementation or repair work governed by `.omx/plans/apm-native-onboarding.md` across `/home/coder/apm` and `/home/coder/omni`. Do not use for unrelated agent lifecycle work.

## Required inputs

- Approved onboarding plan.
- Failure transcript or reproducible flow.
- Current APM and Omni worktree state.
- `docs/harness/apm-native-onboarding/team-spec.md`.

## Workflow

1. Record the reported command flow in `_workspace/00_input/request-summary.md`.
2. Fan out read-only root-cause, version-contract, and test-design lanes; synthesize `_workspace/01_root-cause.md`.
3. Write `_workspace/02_flow-test.md` and add the smallest flow test that fails for the reported sequence before editing production code.
4. Assign one implementation owner per repository actually implicated by evidence; write `_workspace/03_implementation.md`.
5. Run the flow test, focused package tests, then applicable full suites; record `_workspace/04_test-report.md`.
6. Run independent correctness and boundary reviews into `_workspace/05_correctness-review.md` and `_workspace/05_boundary-review.md`. Repair and repeat at most three times.
7. The leader performs final protocol, flow, and worktree verification in `_workspace/06_final-summary.md`.

## Validation

- No writer crosses repository ownership without leader reassignment.
- The regression test exercises a command/state flow, not only an isolated helper.
- APM protocol goldens and Omni consumer fixtures agree byte-for-byte.
- Focused tests precede full suites; DinD uses the workspace Docker host.
- No completion claim until implementation, test, and both review artifacts approve.
