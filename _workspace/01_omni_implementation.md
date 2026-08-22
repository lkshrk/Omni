# Omni implementation handoff

Status: producer complete; ready for test/review lanes after the APM producer lands an immutable commit and the leader updates Omni's pin.

Round-2 review repairs complete locally: plan/resolution/operation identity binding, effective resolutions, active/conditional legacy groups, early joined recovery detection, full TUI resolution/recovery/cleanup, filesystem-identity fragment CAS, restart-safe cleanup, descriptor-relative secure Unix I/O, reparse/DACL Windows wiring, and rename/restart/swap matrices.

Round-3 repairs complete locally: grouped conflict origin selection with durable loser exclusion, conditional authorized drop, canonical legacy MCP secret mapping, fixed protocol schema/golden SHA gates, legacy-safe TUI startup, comprehensive terminal cancellation/resolution/apply/status/cleanup, and an explicit Docker onboarding integration shard covering app plus terminal TUI.

## Implemented

- No-database, no-create onboarding initialization and `DefaultStateDir`/`--state-dir`.
- Strict typed APM scan/apply/status/resume/finalize/cleanup JSON client.
- Private finalize capability transported only through stdin and persisted only in a verified private journal.
- Recursive v22/v23 include reader with source preimages, owning JSON pointers, deterministic canonical candidate IDs, target preservation, secret redaction, durable-ignore candidates, and conditional-state blockers.
- Fresh native+legacy replan and candidate/preimage comparison before every apply.
- Config-root cross-process lock held across ordinary read/modify/write transactions and the complete onboarding cutover.
- Recoverable Omni journal with secured backups, structurally scoped fragment CAS, unrelated-edit preservation, idempotent resume, v24-last commit, and external finalize fence.
- POSIX private-root containment/no-follow/0600/0700 writes and Windows protected current-user+SYSTEM DACLs with weakened-ACL detection tests.
- CLI plan/apply/status/resume/cleanup and Agents-tab TUI preview/blocker/confirmation/apply/recovery UX.
- Vendored APM candidate/plan schemas, static onboarding-boundary guard, docs, named race CI job, and macOS/Windows protocol/security subset.

## Changed files

- Protocol/execution: `internal/apm/{client.go,onboard.go,onboard_test.go,protocol_schema_test.go,testdata/*}`, `internal/executor/*`.
- Coordinator/legacy/recovery: `internal/app/agents_onboard*.go`, `internal/app/app.go`.
- Config/security: `internal/config/{loader.go,routed_write.go,writeconfig.go,write_lock*.go,state_dir_test.go}`, `internal/securefile/*`.
- UX: `internal/cli/{root.go,agents.go,agents_onboard.go,action_catalog_test.go}`, `internal/tui/{agents_all.go,agents_apm_test.go,model.go,update.go,view_hints.go}`, `integration_tests/tui_agents_integration_test.go`.
- Shipping/docs: `.github/workflows/ci.yml`, `docs/{agents.md,cli.md,state-and-files.md,test-matrix.md,tui.md}`.

## Verification

- Focused race:
  - `go test -race -count=1 ./internal/securefile ./internal/config ./internal/apm ./internal/app ./internal/cli ./internal/tui -run 'Onboard|Legacy|CommitLegacy|ImportSchema|DefaultStateDir|ActionCatalog|RootPrivate|RootRejects|APMCommand|WriteConfigLocks'`
  - PASS.
- Envelope repair gate:
  - `go test -race -count=1 ./internal/apm ./internal/app ./internal/cli ./internal/tui -run 'Import|Protocol|Onboard|APMCommand'`
  - PASS; raw/missing/wrong/unknown envelopes and missing required result fields are rejected.
- Docs/action contract:
  - `go test -count=1 ./internal/actions ./internal/cli -run 'Test(TestMatrix|ToolActionCatalog|RunnableCLICommands|MutatingCLICommands)|Coverage'` PASS.
  - `uv run --with-requirements docs/requirements.txt mkdocs build --strict` PASS.
  - Onboarding is documented under non-action workflow coverage; no synthetic action catalog ID exists.
- Real local APM coordinator:
  - `PATH=/home/coder/apm/.venv/bin:$PATH go test -count=1 -tags=integration ./internal/app -run TestAgentsOnboardRealPinnedAPM`
  - PASS: read-only plan, legacy+native union, apply, v24 commit, finalize.
- Real terminal TUI journey:
  - `PATH=/home/coder/apm/.venv/bin:$PATH go test -count=1 -tags=integration ./integration_tests -run TestTUIAgentsOnboardingPreviewConfirmAndApply`
  - PASS.
- Compile/static/platform:
  - `go test -run '^$' ./...` PASS.
  - `go vet ./internal/securefile ./internal/config ./internal/executor ./internal/apm ./internal/app ./internal/cli ./internal/tui` PASS.
  - Windows securefile/config test cross-compilation PASS.
  - `git diff --check` and `actionlint .github/workflows/ci.yml` PASS.
  - `make lint` PASS with `0 issues`; config-lock release, private staging cleanup, file close, temporary directory removal, and staged-fragment cleanup errors are propagated with `errors.Join` rather than discarded.
- Round-2 gates:
  - Focused race across APM/app/config/securefile/CLI/TUI PASS.
  - Real local APM test PASS, including stale-plan rejection before journal creation and repeated joined cleanup.
  - Real terminal TUI PASS, including executable inspection/approval, review/apply, joined status, cleanup preview/confirmation, and default StateDir.
  - Windows securefile/app cross-compilation, actionlint, vet, lint, and diff check PASS.
- Round-3 gates:
  - Real coordinator conflict winner + conditional drop + target/env resolution + v24/finalize/cleanup PASS.
  - Real terminal cancellation, target selection, secret mapping, executable approval, conflict origin, conditional exclusion, apply, status, and cleanup PASS.
  - Five vendored protocol artifacts have fixed SHA-256 regression gates; current APM/Omni envelope schema and golden bytes match.
  - Focused race, `make lint` (0), vet, Windows cross-compile, actionlint, strict docs, and diff check PASS.
- Native macOS repair:
  - SecureRoot canonicalizes an existing ancestor chain once (covering macOS `/var` -> `/private/var`) while still rejecting a symlink at the supplied root; all descendant I/O remains descriptor-relative/no-follow under the canonical capability.
  - Exact Linux race regressions and symlinked-parent regression PASS; Darwin amd64 securefile and Darwin arm64 app cross-compilation PASS; lint/vet/diff remain green.
- Native Windows/reconcile repair:
  - Windows atomic private-file replacement now uses Go's long-path-aware `os.Rename`/MoveFileEx wrapper without the problematic `MOVEFILE_WRITE_THROUGH` flag, then verifies destination existence and exact protected DACL before success.
  - Platform regex is narrowed to `CommitLegacyFragments`, avoiding unrelated unsafe-HOME fragment tests.
  - Reconcile test setup owns an isolated HOME with no ambient APM manifest; missing manifest remains a deterministic no-op even with `apm` absent from PATH.
  - Exact clean-PATH reconcile tests PASS; Linux race, Windows securefile/app cross-compilation, lint, vet, actionlint, and diff check PASS.

## Protocol assumptions

- APM emits only strict `{ok,kind,plan|result|error}` envelopes. Omni requires command-specific kinds (`import-plan`, `import-apply-result`, `import-status-result`, `import-resume-result`, `import-finalize-result`, `import-cleanup-result`, `import-error`) and rejects raw/missing/unknown/wrong wrappers before Omni config mutation.
- Candidate and plan schema version is `1`; coordinator is `omni-v24`; operation IDs are 32 lowercase hex.
- Apply success must be `awaiting-external-commit` + `external-commit-then-finalize` + `finalize_token_required:true`; finalize must return `complete`.
- Planning unions secured Omni candidates with `--from claude --from codex`, rewrites the candidate file, and recomputes the canonical candidate set.
- Resume accepts the original operation/candidate/plan/preimage/token tuple and is idempotent.
- `payload.disposition=excluded` is durable APM exclusion state.
- `payload.target_resolution_required=true` must classify as `needs-choice` with reason `legacy-unscoped-targets`; approved targets must be item-specific. Omni also blocks locally when no approved targets exist.

## Remaining leader/test-lane work

- Commit/review APM, update Omni's exact version/source SHA everywhere, then enable/run the DinD pinned-APM onboarding job. Current committed pin predates import.
- Run the wired macOS/Windows jobs for real DACL/reparse evidence; Linux cross-compilation is green but cannot prove Windows runtime ACL behavior.
- Active legacy group references resolve through current-host membership; inactive/conditional groups and host variants remain explicit `needs-choice` items and are never flattened or dropped.
