# Test Spec: Agent Sync Continuation

**Scope**: agent skills, plugins, and MCP restore/update loops
**Source material**: user request; `internal/app/agents_skills.go`, `internal/app/plugin_ops.go`, `internal/app/mcp_ops.go`
**Testing posture**: baked-in default; no repo-root `TESTING.md`
**Test report**: [test-report.md](./test-report.md)

## Testing What

### Product Behaviors

- A failed agent-content install or update is reported without preventing later independent targets from running.

### Implementation / System Invariants

- Skills, plugin restore, plugin bulk update, and MCP restore retain successful outcomes alongside collected per-target failures.
- Tests place the failure before a succeeding target so loop continuation is observable.

### Risk-Based Behaviors

- One broken manifest entry must not leave unrelated agent content unsynchronized.

### Operational / Release Behaviors

- Focused Go unit tests remain deterministic and require no installed agent CLIs.

### Stakeholder Confidence Goals

- A sync result identifies failures while demonstrating that independent later work completed.

## Evidence Strategy

| What | Priority | Viable How | Selected How | Why | Residual Risk |
| --- | --- | --- | --- | --- | --- |
| Continue after a failed skills package install | UNKNOWN | unit | unit | Directly exercises the shared loop | Adapter CLI behavior remains separately tested |
| Continue after a failed plugin install | UNKNOWN | unit | unit | Directly exercises restore result aggregation | None within the loop contract |
| Continue after a failed plugin update | UNKNOWN | unit | unit | Directly exercises bulk update ordering | None within the loop contract |
| Continue after a failed MCP install | UNKNOWN | unit | unit | Directly exercises restore result aggregation | None within the loop contract |

Out of scope:

- Real agent CLI integration and network marketplace behavior.
- Changing production control flow.

## Test Cases

### ASC-T01 Skills Fail-First Continuation

- Testing what: the second skills package installs after the first fails.
- Source refs: `internal/app/agents_skills.go`
- Preconditions: in-memory stub installer.
- Automated checks: `go test ./internal/app -run 'TestRestoreSkills_ContinuesAfterPackageFailure'`
- Pass criteria: one failure for the first package and one installation for the second.
- Cleanup: none.

### ASC-T02 Plugin Restore Fail-First Continuation

- Testing what: the second plugin installs after the first fails.
- Source refs: `internal/app/plugin_ops.go`
- Preconditions: in-memory plugin adapter.
- Automated checks: `go test ./internal/app -run 'TestRestorePlugins_ContinuesAfterPluginInstallFailure'`
- Pass criteria: the first error is collected and the second plugin is installed.
- Cleanup: none.

### ASC-T03 Plugin Update Fail-First Continuation

- Testing what: the second plugin updates after the first fails.
- Source refs: `internal/app/plugin_ops.go`
- Preconditions: in-memory plugin adapter.
- Automated checks: `go test ./internal/app -run 'TestUpdatePluginsPreRefreshed_ContinuesAfterPluginUpdateFailure'`
- Pass criteria: both updates are attempted and only the first is reported failed.
- Cleanup: none.

### ASC-T04 MCP Restore Fail-First Continuation

- Testing what: the second MCP server installs after the first fails.
- Source refs: `internal/app/mcp_ops.go`
- Preconditions: in-memory MCP adapter.
- Automated checks: `go test ./internal/app -run 'TestRestoreMcpServers_ContinuesAfterServerInstallFailure'`
- Pass criteria: both adds are attempted, the first error is collected, and the second installation is recorded.
- Cleanup: none.

## Fixtures And Environments

### Local Development

- Data fixtures: test-local manifests and stub adapters.
- Environment setup: Go toolchain only.
- Mutation policy: test temporary directories only.
- Known local limitations: no live agent CLI coverage.

## Report Expectations

- Record focused and package-level Go test commands with their actual outcomes.
- Use `PASS`, `FAIL`, or `BLOCKED`; do not infer results.

## Coverage Notes

- Priority is `UNKNOWN` because no upstream PRD or issue declares one.
- Fail-first ordering is required; a final-item failure does not prove continuation.
