# Correctness review — round 3

Status: approved

Verdict: **APPROVE**

Re-read both complete onboarding diffs, both producer handoffs, and `_workspace/02_test_report.md` through round 8. No remaining local production blocker found.

## Prior blockers verified closed

1. **Activation restoration:** APM captures exact activation bytes, mode, and hash before retirement; post-retirement verification/audit failure atomically restores and verifies them, resets the journal to `ownership-verified`, and repeated resume completes idempotently. The retained regression covers both failure gates and exact bytes/mode.
2. **Conflict/conditional paths:** conflict candidates are grouped, the selected origin is membership-validated, only the winner imports, and losers become durable exclusions. Conditional group/host candidates accept explicit exclusion through APM, Omni validation, CLI/TUI, and real coordinator/terminal tests.
3. **Secure snapshots/platform boundary:** snapshots use capability-created/published directories, compare staged content with the reviewed fingerprint, and revalidate the source after copy. Source mutation and imported-root replacement tests pass. Windows code now holds reparse-safe component handles, verifies exact protected current-user plus `SYSTEM` ACL entries, and has extra-ACE/reparse/component-swap tests wired on `windows-latest`.
4. **Claude inventory:** `CLAUDE.md` is an importable local instruction candidate. Referenced scripts are discovered inside every hook iteration; tests cover multiple hooks with and without `CLAUDE.md`.
5. **MCP placeholders:** reviewed secret bindings become canonical `${VAR}` values in supported `env`/`headers` fields. Legacy literal containers are removed; unit and real Omni/APM integration assert that manifest and lockfile contain placeholders and no literal/blocked markers.

## Verification reviewed

- Round-8 APM importer/CLI/version matrix: `72 passed, 4 Windows-runtime tests skipped locally`; all four are present and wired into the required Windows CI job.
- Omni focused race/trimpath, real local APM coordinator, and real terminal TUI onboarding journeys: pass.
- Shared protocol artifacts match byte-for-byte across repositories.
- Fresh reviewer diagnostics: focused Omni `go vet` pass; APM importer Ruff pass.

## External release gates

Approval is for the implementation under review. The leader still must complete the already-declared release gates: commit APM, update every immutable Omni pin, run pinned-onboarding DinD, and obtain the wired Windows/macOS runtime results. Those are verification/shipping gates, not unresolved code-review findings.
