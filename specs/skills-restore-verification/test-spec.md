# Test Spec: Skills Restore Verification

**Scope**: stale upstream skills lock entries during restore
**Source material**: user-reported `ShiplightAI/agent-skills` verification failure; `internal/app/agents_skills.go`
**Testing posture**: baked-in default
**Test report**: [test-report.md](./test-report.md)

## Testing What

- Restore succeeds when a package has current global skills on every target agent plus removed names retained only in the lockfile.
- Current skills must still be present on every resolved target agent.
- A package with no current global skill entry must still fail verification.

## Evidence Strategy

| What | Priority | Selected How | Residual Risk |
| --- | --- | --- | --- |
| Stale lock entries do not fail a valid restore | UNKNOWN | unit + integration | Real upstream package mutation is not exercised. |
| Missing current agent link fails | UNKNOWN | unit | None known at the verifier seam. |
| No current global skill fails | UNKNOWN | unit | None known at the verifier seam. |

Out of scope:

- Mutating a live global skills installation.
- Upstream network availability and repository contents.

## Test Cases

### SKILLS-RESTORE-T01 Stale Lock Entry

- Run: `go test -tags integration ./integration_tests -run '^TestCLI/agents-skills-restore-stale-lock$' -count=1`
- Pass: restore reports `1 installed, 0 failed` and invokes add/list once.

### SKILLS-RESTORE-T02 Verifier Invariants

- Run: `go test ./internal/app -run '^TestVerifyRestoredSkillTargets' -count=1`
- Pass: stale entries pass; missing links and absent global entries fail.
