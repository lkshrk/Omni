# APM-native onboarding test report

Status: PASS — all local and immutable-pin DinD gates green; Windows/macOS runtime jobs remain CI-pending

## Leader release gate

- APM fork commit: `20eb25d98fc73bb688f846d499b035de2b2fb325` (`0.28.0+omni.3`), pushed to `lkshrk/apm`.
- Omni source, Docker, CI, tests, and docs pin that exact commit/version.
- Real DinD onboarding shard passed against the remote immutable commit:
  `TestAgentsOnboardRealPinnedAPM` and `TestTUIAgentsOnboardingPreviewConfirmAndApply` executed under `-race -trimpath`.
- The first quoted build-arg attempt ran zero tests; CI quoting was corrected and the unquoted rerun executed both packages successfully.
- Remaining external evidence: the wired Windows ACL/reparse and macOS filesystem runtime jobs must run on their native CI runners.

## Execution round 8 — final round-three verification

Activation restoration repair:

- Retained exact-byte/mode activation restoration regression passes for both post-retirement verification failure and audit failure: `2 passed`.
- Final APM focused importer/CLI/version matrix: `72 passed, 4 skipped` in 9.11s.
- Four skips are Windows ACL/reparse/component-swap runtime cases; tests exist and the entire importer suite is wired on `windows-latest`.
- APM Ruff, actionlint, and diff-check pass.

Remaining review blockers independently covered:

- Conflict resolution selects one reviewed origin and excludes losers; unknown winners fail.
- Conditional candidates can be explicitly excluded.
- Exclusions require matching identity, scope, targets, and fingerprint; unchanged/changed plus list/remove were also exercised black-box.
- Snapshot copy rejects source mutation and imported-root replacement swaps.
- Real subprocess apply/resume locking and in-process concurrent apply/resume pass.
- `CLAUDE.md` inventory and multiple independent hook/script candidates pass.
- Canonical MCP normalization passes: placeholder fields use `${VAR}`, while `env_literal`, `literal-secret`, and raw literal values are absent from manifest and lockfile.
- Real Omni/APM integration now asserts that canonical MCP invariant across the repository boundary.

Five shared protocol artifacts match byte-for-byte:

```text
import-candidates-v1.json c624ff8586da7effa458c2a07d0433fc222bacf5c2801da82070cfff13811038
import-envelope-v1.json   0dd40f94af044a537157b9985a97d66e5d5f13e3947783c476436d06b7c7a4e0
import-plan-v1.json       72cb19bb870db9f824f1f558104d31a440f78827b6a0829d89009d973d649901
import-result-v1.json     38d3c17a11c4c7375c67a6983b09bfac634128b21ffbb0eabe10771d63b0dc15
envelopes-v1.json         c9f41d6e445835926ede647f5b48942c41b2f2421589aa1dbbc665626d123dbf
```

Omni final evidence:

- Focused race+trimpath across securefile/config/APM/app/CLI/TUI passes.
- Real local APM coordinator passes with target, secret, executable/conflict/conditional/exclusion resolution, stale-plan rejection, exact MCP placeholder output, recovery, and repeated cleanup.
- Real terminal TUI journey passes with cancellation-before-mutation, full resolution review/apply, joined status, resume/recovery, cleanup preview/confirmation, and default StateDir.
- `make lint`, `go vet ./...`, actionlint, and diff-check pass.

Round-eight verdict: PASS. No local production blocker remains. Leader/CI still own the immutable APM commit and Omni pin, pinned-onboarding DinD, Windows runtime ACL/reparse/component-swap execution, and macOS runtime filesystem execution.

## Execution round 7 — activation restoration blocker

Initial round-three APM focused matrix passed `70`, with `4` Windows-runtime skips. Collected coverage includes MCP canonical placeholder normalization, conflict winner/loser resolution, conditional exclusion, exclusion identity/scope/targets/fingerprint, snapshot source/root swaps, subprocess lock serialization, multi-hook plus `CLAUDE.md` inventory, and Windows ACL/reparse/component-swap tests.

The existing generic verification-failure test asserted only journal state. Test ownership added the missing explicit activation-byte restoration regression:

```bash
cd /home/coder/apm
uv run --frozen pytest tests/unit/importing/test_service.py \
  -k plugin_activation_is_restored -q --tb=short
```

Result: `2 failed` for both `_verify_post_retirement` and `_audit_import` injection.

Observed behavior:

- The plugin activation entry is restored semantically.
- `~/.claude/plugins/installed_plugins.json` is not restored byte-for-byte; compact original JSON becomes normalized pretty JSON.
- Journal returns to `ownership-verified`, but the native activation file remains mutated after the failed operation.

This violates failure immutability and the reviewed requirement to restore the backed-up activation config atomically. Required APM repair: capture the pre-operation native activation bytes/mode before any install/retirement mutation and restore those exact bytes/mode on post-retirement verification or audit failure. Keep the new test; both parameter cases must pass before continuing round-three verification.

## Execution round 6 — prior-review blocker regression

APM independent evidence:

- Full importer/CLI/version matrix: `55 passed, 2 skipped` in 5.83s. The two skips are real Windows ACL/reparse runtime tests.
- Three injected deployment verification, post-retirement verification, and audit failures remain recoverable-partial and never terminal.
- Immutable-plan tamper variants, exact target preservation, resolution identity/order, same-operation concurrent apply/resume, and before/after crash replay across all nine durable phases pass.
- Secure root, symlinked journal base, operation-ID traversal, hostile umask, and adversarial root-swap tests pass locally.
- Independent black-box smoke passes for target-resolved `~/.claude/CLAUDE.md`, durable exclusion unchanged/changed classification, exclusion list/remove, malformed ownership fail-closed, and APM-state drift rejection before imported writes.
- Full APM Ruff and diff-check pass.

Omni independent evidence:

- Focused `go test -race -trimpath` across securefile/config/APM/app/CLI/TUI passes.
- Covered active and conditional legacy groups, default StateDir, joined startup recovery before mutation, plan/resolution identity, safe resolution parsing, fragment CAS, mode/symlink swaps, every rename restart boundary, cleanup, status/resume, and full TUI resolution flows.
- Real local APM coordinator passes, including stale-plan rejection before journal creation and repeated joined cleanup.
- Real terminal TUI journey passes, including executable inspection/approval, target/secret/exclusion resolution, review/apply, joined status, cleanup preview/confirmation, and default StateDir.
- `make lint`, `go vet ./...`, compile-only `go test -run '^$' ./...`, actionlint, and diff-check pass.

Round-6 verdict: PASS. No new production blocker. Remaining external gates are unchanged: immutable APM commit/pin plus pinned DinD belongs to the leader; Windows DACL/reparse and macOS filesystem runtime evidence belongs to their wired CI runners.

## Execution round 5 — final test-lane result

- `make lint` -> pass after producer error-boundary repairs.
- Affected `go test -race -trimpath` securefile/config/app onboarding and lock tests -> pass.
- `make gen-schema` -> pass and byte-for-byte reproducible; focused schema/config tests pass.
- `DOCKER_HOST=tcp://localhost:2375 make docs-build` -> strict MkDocs build passes through workspace DinD.
- Windows amd64 compile contract passes for securefile/config/executor/APM/app/CLI/TUI.
- macOS amd64 and arm64 compile contracts pass for the same packages.
- Linux arm64 Omni build passes.
- Final actionlint and diff-check pass.

Final protocol fixture hashes match between APM and Omni:

```text
import-candidates-v1.json 5ef21951fc4e9284d29a4ff546b7820ec0afbe885a0cd9af38ba79140b543e54
import-envelope-v1.json   5cea7b4de4a2afc21110384db14db014524889d345f19d26254aae547c07e21f
import-plan-v1.json       344f22b9910906a16f3af408337d39623e495ab4d4638000f1584854ed56a6db
import-result-v1.json     38d3c17a11c4c7375c67a6983b09bfac634128b21ffbb0eabe10771d63b0dc15
```

Final fields:

```text
APM base revision tested: ea3f74ae plus current feature worktree
Omni base revision tested: 1f0d6d1 plus current feature worktree
Focused APM result: 35 passed
APM security/crash result: 15 selected passed; nine durable crash phases replayed
Full APM result: 19,907 passed, 2 skipped, 21 xfailed, 99 subtests
Wheel smoke result: PASS; clean install, strict envelopes, four schemas, source isolation, idempotent second scan
Focused Omni/race result: PASS
Real local APM coordinator result: PASS
Real terminal TUI onboarding result: PASS
Full Omni result: make test PASS
Omni lint/build/vet/schema/docs result: PASS
Platform compile result: Windows amd64, macOS amd64/arm64, Linux arm64 PASS
Test-owner edits: APM crash/security regressions; Omni securefile guard import and trimpath-safe static guard
Known local skips: immutable-pin onboarding DinD only; requires leader to commit APM, update every Omni pin, then run
Verdict: PASS (test lane)
```

Overall shipping completion remains gated on the leader-owned immutable APM commit/pin and pinned-onboarding DinD run. Real Windows DACL/reparse behavior and macOS runtime filesystem behavior still require their wired CI runners; local cross-compilation does not substitute for those CI jobs.

## Execution round 4 — 2026-08-22

- Fake `agents.onboard` action row removed; targeted action coverage passes.
- Added the required testguard import to `internal/securefile/securefile_test.go`.
- Repaired `TestOnboardingLegacyReaderIsNotReachableFromLifecycleCode` to use `os.Getwd()`; `runtime.Caller` returns an import path under `-trimpath` and was not filesystem-safe.
- Exact `go test -race -trimpath` regression for the guard passes.
- Fresh `make test` passes completely, including script regressions and `go test -race -trimpath ./...`.
- `make build`, `go vet ./...`, compile-only `go test -run '^$' ./...`, `actionlint .github/workflows/ci.yml`, and `git diff --check` pass.

Blocking command: `make lint`.

Findings:

```text
internal/app/agents_onboard.go:55     unchecked os.RemoveAll
internal/app/agents_onboard.go:108    unchecked lock.Close
internal/app/agents_onboard.go:186    unchecked os.RemoveAll
internal/app/agents_onboard.go:230    unchecked lock.Close
internal/config/loader.go:34          unchecked lock.Close
internal/config/loader.go:556         unchecked lock.Close
internal/config/loader.go:591         unchecked lock.Close
internal/config/loader.go:729         unchecked lock.Close
internal/config/routed_write.go:78    unchecked lock.Close
internal/config/writeconfig.go:22     unchecked lock.Close
internal/securefile/securefile.go:110 unchecked f.Close
internal/securefile/securefile_unix.go:27 unchecked os.Remove
internal/app/agents_onboard_journal.go:137 ineffectual assignment to err
```

Result: 12 `errcheck`, 1 `ineffassign`. Production repair required before docs/schema/platform evidence is final.

## Execution round 3 — 2026-08-22

Envelope repair verified:

- `uv run --frozen pytest tests/integration/test_import_cli.py tests/unit/importing tests/unit/commands/test_version_read_only.py -q --tb=short` -> `35 passed`.
- Rebuilt wheel in a clean temporary virtualenv outside the source checkout.
- Installed `apm --version` created no `~/.apm` state.
- Installed scan emitted `{ok:true, kind:"import-plan", plan:{...}}`; apply emitted `{ok:true, kind:"import-apply-result", result:{...}}`.
- Packaged schemas present: `import-candidates-v1.json`, `import-envelope-v1.json`, `import-plan-v1.json`, `import-result-v1.json`.
- Installed second scan reported no importable/local-package work; source-checkout path was absent from `sys.path`.
- Omni repaired-consumer focused race passed in securefile/config/apm/app/cli/tui.
- Real local APM coordinator and real terminal TUI onboarding integration both passed.

`make test` then failed three guards. Two are resolved as test-only fixes:

- Current `TestOnboardingLegacyReaderIsNotReachableFromLifecycleCode` uses `runtime.Caller` and now passes.
- Added the required blank testguard import to `internal/securefile/securefile_test.go`; `TestEveryTestPackageImportsGuard` now passes.

Remaining exact blocker:

```bash
go test -count=1 ./internal/actions -run TestCoverageMatrixListsEveryAction
```

Result:

```text
docs/test-matrix.md lists unknown action agents.onboard
```

`internal/actions/catalog.go` has no `agents.onboard` action ID. Producer repair must either register and route a real shared action or remove the matrix row if onboarding intentionally remains a direct CLI/TUI coordinator surface. The latter is the smaller consistent repair. Broad lint/build/docs/platform gates pause until `make test` is green.

## Execution round 2 — 2026-08-22

Containment repairs verified:

- `uv run --frozen pytest tests/unit/importing/test_secure.py tests/unit/importing/test_service.py -k 'secure or crash_phase_replay' -q --tb=short` -> `15 passed, 10 deselected`.
- Full focused importer command -> `27 passed in 1.52s`.
- Full APM Ruff, `git diff --check`, and both changed workflows under `actionlint` -> pass.
- Full APM unit regression -> `19,907 passed, 2 skipped, 21 xfailed, 20 warnings, 99 subtests passed in 211.69s`; exit `0`.
- `uv build` -> pass; built `dist/apm_cli-0.28.0+omni.2-py3-none-any.whl`.

Installed-wheel isolation exposed a new protocol blocker. The installed CLI successfully performed read-only version probing, scan, and apply from a clean temporary virtualenv outside the source checkout, and packaged schemas were available. Its JSON shapes were:

```text
scan keys:  blockers,candidate_set_id,coordinator,inventory_fingerprint,items,operation_id,schema_version,scope,sources,summary,warnings
apply keys: coordinator,finalize_token_required,next_action,operation_id,schema_version,state
```

Both omit the approved shared envelopes:

```text
PlanSuccess  {ok:true, kind:"plan", plan:{...}}
ApplySuccess {ok:true, kind:"result", result:{...}}
```

Regression coverage was added to `/home/coder/apm/tests/integration/test_import_cli.py`. Exact failure:

```bash
cd /home/coder/apm
uv run --frozen pytest tests/integration/test_import_cli.py -q --tb=short
```

Result: `1 failed`; `test_real_cli_scan_apply_status_and_finalize` raises `KeyError: 'ok'` on scan output. Apply was independently observed to omit `ok`/`kind` as well.

Required production repair: wrap JSON scan and apply success output exactly as the canonical protocol specifies, then update the Omni consumer to unwrap `plan`/`result` while preserving error envelopes and exit codes. Rebuild the wheel and rerun the installed-wheel smoke.

Per harness failure policy, Omni full/lint/build/docs/platform gates remain paused until this cross-repository protocol blocker is repaired.

## Execution round 1 — 2026-08-22

Fresh passing evidence:

- `cd /home/coder/apm && uv run --frozen pytest tests/unit/importing tests/integration/test_import_cli.py tests/unit/commands/test_version_read_only.py -q --tb=short` -> `12 passed in 0.92s`.
- `cd /home/coder/omni && go test -race -count=1 ./internal/securefile ./internal/config ./internal/apm ./internal/app ./internal/cli ./internal/tui -run 'Onboard|Legacy|CommitLegacy|ImportSchema|DefaultStateDir|ActionCatalog|RootPrivate|RootRejects|APMCommand|WriteConfigLocks'` -> six packages passed under race.
- Candidate schema SHA-256 matches across APM/Omni: `5ef21951fc4e9284d29a4ff546b7820ec0afbe885a0cd9af38ba79140b543e54`.
- Plan schema SHA-256 matches across APM/Omni: `344f22b9910906a16f3af408337d39623e495ab4d4638000f1584854ed56a6db`.
- `PATH=/home/coder/apm/.venv/bin:$PATH go test -count=1 -tags=integration ./internal/app -run TestAgentsOnboardRealPinnedAPM` -> pass.
- `PATH=/home/coder/apm/.venv/bin:$PATH go test -count=1 -tags=integration ./integration_tests -run TestTUIAgentsOnboardingPreviewConfirmAndApply` -> pass.
- Added explicit pre-install rollback/post-install resume phase matrix in `/home/coder/apm/tests/unit/importing/test_service.py`.
- `uv run --frozen pytest tests/unit/importing/test_service.py -k crash_phase_replay -q --tb=short` -> `9 passed` for `planned`, `backed-up`, `packages-staged`, `manifest-prepared`, `installed`, `ownership-verified`, `activation-retired`, `post-retirement-verified`, and `audited`.

Blocking command:

```bash
cd /home/coder/apm
uv run --frozen pytest tests/unit/importing/test_secure.py -q --tb=short
```

Result: `2 failed, 3 passed`.

Production findings:

1. `test_secure_root_rejects_symlink_root` — `SecureRoot.__init__` resolves the supplied root before `ensure`/`verify`, erasing the symlink evidence. A symlinked operation root is accepted and writes through to its external target. Required repair: retain and no-follow validate the logical root and every ancestor before canonical containment is granted.
2. `test_journal_root_rejects_operation_id_traversal` — `journal_root("../escape", create=True)` creates `~/.apm/escape`, outside `~/.apm/import-journal`. Required repair: validate operation IDs as one safe path component before constructing the root.

Test-only edits:

- `/home/coder/apm/tests/unit/importing/test_service.py` — nine-phase crash/replay recovery matrix.
- `/home/coder/apm/tests/unit/importing/test_secure.py` — traversal, symlink-root/ancestor, operation-ID traversal, and hostile-umask contracts.

Broad suite note: the APM full-unit process started before these test additions. Its result is not accepted as fresh evidence and must be rerun after the containment repair. Per harness failure policy, remaining broad/lint/build/docs/platform/DinD stages are paused.

## Baseline

Observed 2026-08-22 before producer edits:

- Omni: `feat/apm-native-onboarding` at `1f0d6d1`; two untracked harness/workspace entries; no tracked product diff.
- APM: `feat/native-import-onboarding` at `ea3f74ae`; clean.
- `_workspace/01_apm_implementation.md` and `_workspace/01_omni_implementation.md` existed only as pending placeholders.
- No tests were run in this phase because both producers own mutable repositories and stateful suites must run serially after their handoffs.
- Last inherited Omni-main evidence at `1f0d6d1`: compile, lint, race tests, build, strict docs, focused DinD integration, and APM/app/CLI integration passed. This is historical comparison evidence, not a fresh onboarding result.
- Current pre-onboarding pin is `git+https://github.com/lkshrk/apm.git@ea3f74ae5547059aca214e7a395d09e874205dce` in `internal/app/apm_version.go`, `Dockerfile.test`, and CI. The implementation must replace all three together with one immutable importer-capable revision.

## Execution rules

1. Do not start until both producer handoffs list changed files, focused tests, and `Status: complete`.
2. Run stages in order. Stop at the first failure; record command, exit status, and the smallest failing test name here.
3. Run stateful integration suites serially. Give every test an isolated temporary HOME/APM/config/state root.
4. A passing test may not depend on either repository being importable from the source checkout unless that is the behavior under test.
5. Add regression tests only for uncovered acceptance behavior. Production repairs go back to the owning producer.
6. Never print secret canary values in this report or captured logs.

## Stage 0 — handoff and static contract gate

Commands:

```bash
test "$(grep -c '^Status: complete' _workspace/01_apm_implementation.md)" -eq 1
test "$(grep -c '^Status: complete' _workspace/01_omni_implementation.md)" -eq 1
git -C /home/coder/apm diff --check
git -C /home/coder/omni diff --check
```

Pass criteria:

- Both producer artifacts are complete and enumerate their exact changed files/tests.
- No whitespace errors.
- APM owns discovery, classification, schemas, plan resolution, import/apply, lifecycle locking, audit, ownership, and APM recovery.
- Omni owns only legacy config extraction, read-only initialization, the APM client/service/CLI/TUI, secure v24 fragment commit, and Omni recovery.
- No onboarding parser enters normal APM lifecycle or normal Omni config-load paths except journal recovery detection.

## Stage 1 — protocol goldens and package data

Run the producer-named focused golden tests first. At minimum the tests must prove:

```text
APM canonical schemas:
  src/apm_cli/schemas/import-candidates-v1.json
  src/apm_cli/schemas/import-plan-v1.json
Omni vendored fixtures:
  byte-for-byte identical to the canonical APM files
```

Then run:

```bash
cd /home/coder/apm
uv run --frozen pytest tests/unit/importing -n auto --dist worksteal

cd /home/coder/omni
go test -count=1 ./internal/apm/... ./internal/app/... ./internal/cli/... -run 'Protocol|Golden|Schema|Candidate|Plan'
```

Pass criteria:

- Candidate and plan fixtures agree byte-for-byte across repositories.
- Golden success envelopes cover plan and apply; golden failure envelopes cover exit codes `2`, `3`, `4`, `5`, and `6` where applicable.
- Unknown schema versions, unknown fields, unknown classifications, and unknown reason codes fail before discovery or mutation.
- `plan_id`, `resolution_id`, `candidate_set_id`, and `operation_id` are deterministic and their binding is checked.
- Omni passes absolute candidate/plan paths and preserves APM JSON stdout, stderr, and exit status exactly.
- `importlib.resources` loads both canonical schemas from the built package.

## Stage 2 — focused APM unit coverage

```bash
cd /home/coder/apm
uv run --frozen ruff check src/ tests/
uv run --frozen pytest tests/unit/importing -n auto --dist worksteal
```

Each behavior below needs a named, independently failing test:

- Discovery inventories Claude/Codex instructions/rules, agents, commands, skills, hooks, plugins, marketplaces, and MCP from every documented global/project root.
- Existing APM ownership is `already-managed`; exact duplicate content deduplicates; distinct collisions block.
- APM-deployed native artifacts are never re-imported.
- Current and proposed target subsets remain distinct; broadening requires an explicit reviewed resolution.
- Unsupported known primitives stay visible and untouched.
- Candidate ordering, source handles, fingerprints, and plans are stable across repeated scans and path-root relocation.
- Literal MCP environment/header/auth/URL-userinfo secrets become `{ "blocked": "literal-secret" }`; no literal reaches object repr, stdout/stderr, logs, plans, manifests, journals, or backups created by APM.
- Placeholder mapping accepts environment variable names only.
- Local snapshots reject traversal, symlink/reparse escape, device/socket inputs, changed preimages, and size/count limits; accepted snapshots are immutable and source-preserving.
- Existing manifest declarations and unknown keys survive round-trip writes.

Pass criteria: every selected test passes once, with no retry, warning-based skip, or live-network dependency.

## Stage 3 — APM transaction, recovery, and locking integration

```bash
cd /home/coder/apm
uv run --frozen pytest \
  tests/integration/test_import_scan_e2e.py \
  tests/integration/test_import_apply_e2e.py \
  tests/integration/test_import_conflicts_e2e.py \
  tests/integration/test_import_transaction_e2e.py \
  tests/integration/test_import_workspace_lock.py -v
```

If producer filenames differ, substitute the handoff paths but retain every case:

- Default scan is read-only: snapshot and compare bytes, modes/ACL fingerprints, existence, and mtimes across native roots, `~/.apm`, cache, logs, and state before/after.
- Stale source preimage, stale manifest/ownership state, mismatched candidate/plan IDs, or replay under a different environment fails before writes.
- Crash/failure injection at each durable phase: `planned`, `backed-up`, `packages-staged`, `manifest-prepared`, `installed`, `ownership-verified`, `activation-retired`, `post-retirement-verified`, `audited`, `complete`.
- Before `installed`, recovery rolls back staged APM metadata deterministically. At/after `installed`, recovery is resume-only.
- Activation-retirement failure restores only the backed-up activation config and resumes from `ownership-verified`.
- Unrecoverable state remains journaled, returns exit `6`, and does not claim rollback.
- One lifecycle lock serializes import against install/uninstall/reconcile/audit. No nested mutating APM subprocess is spawned.
- Destination takeover matrix covers missing, identical/adopt, reviewed replacement, and foreign/ambiguous/changed/block.
- Apply runs install, ownership verification, native retirement verification, and audit before success.
- A second identical import reports zero importable work and does not rewrite files.

Pass criteria: all subprocess failpoints recover to the documented state; no orphan staging paths; no unlocked mutation; all byte/hash assertions exact.

## Stage 4 — private-file and secret boundary

APM and Omni focused tests must cover the same capability contract:

```bash
cd /home/coder/apm
uv run --frozen pytest tests/unit/importing tests/integration -k 'secure or permission or symlink or reparse or secret or import_journal'

cd /home/coder/omni
go test -race -count=1 ./internal/securefile/... ./internal/app/... -run 'Secure|Secret|Permission|Symlink|Traversal|Journal'
```

Pass criteria:

- POSIX directories are `0700`, files `0600`, independent of hostile umask.
- Absolute destinations, empty components, `..`, separator tricks, symlink ancestors/destinations, root swaps, non-regular sources, and capability crossover fail before secret-bearing writes.
- Atomic replacement is same-directory and `Verify` detects deliberate weakening.
- Every Omni plan, index, journal, and backup uses an operation-scoped `securefile.Root`; a static test forbids direct file-write APIs in onboarding packages.
- A generated unique secret canary is absent from captured stdout/stderr, JSON plans/results, manifests, journals, and logs. The test reports only redacted location/count evidence.
- Real Windows CI verifies protected current-user/SYSTEM DACLs, disabled broad inheritance, reparse refusal, private atomic replacement, and weakened-ACL detection. Mocks are insufficient.

## Stage 5 — installed-wheel smoke

```bash
cd /home/coder/apm
rm -rf dist
uv build
bash scripts/smoke_import_wheel.sh dist/apm_cli-*.whl
```

If the producer uses a differently named script, its handoff must provide an equivalent single command.

Pass criteria:

- Create a clean virtual environment outside `/home/coder/apm`; install only the built wheel and runtime dependencies.
- Run the installed `apm import` CLI with an isolated HOME containing Claude and Codex fixtures.
- Scan, resolve/apply, install, verify ownership/native retirement, and audit succeed.
- Both schemas load from wheel package data.
- The smoke fails if `src/`, repository root, or editable-install paths appear on `sys.path`.
- Second run is idempotent; no importable work and no rewrites.

## Stage 6 — focused Omni behavior

```bash
cd /home/coder/omni
go test -race -count=1 ./internal/securefile/... ./internal/config/... ./internal/apm/... ./internal/app/... ./internal/cli/...
go test -race -count=1 -tags=integration ./internal/apm/... ./integration_tests/... -run 'Onboard|APM'
```

Required independent cases:

- v22/v23 monolith, includes, nested includes, include cycle, and missing include extraction preserve unrelated config.
- Default command and TUI entry use read-only initialization: no settings backup, host repair, database open/migration, cache timestamp, APM version check/write, or APM/native mutation.
- Unsupported legacy fields remain loadable only through onboarding/recovery; normal v24 config remains strict.
- Existing APM overlap/foreign state, target subset preservation, collision, secret, broadening, stale plan, and every APM error envelope map to actionable Omni results without mutation.
- `import --global` runs from APM state/workspace and requires absolute candidate/plan paths.
- APM failure, install failure, ownership failure, or audit failure prevents any v24 fragment rename.
- Omni fragment failpoints before and after every rename recover deterministically: before APM commit may restore all bytes; after APM commit must resume v24.
- Standard config load detects an incomplete Omni journal before rejecting legacy fields.
- Cleanup is explicit, confirmed, complete-operation-only, containment checked, and never automatic.
- Second onboarding reports no work.
- TUI covers inspect -> resolve -> review -> apply -> recovery, including keyboard cancellation before mutation.

Pass criteria: race detector clean; each failure test asserts exact unchanged bytes and expected journal phase.

## Stage 7 — cross-repository pinned binary and mixed-state E2E

Before running, assert one immutable APM revision is present in all Omni consumers:

```bash
cd /home/coder/omni
go test -count=1 ./internal/app -run 'TestAPM.*(Version|Package|Pin|Target|User)'
go test -count=1 ./internal/testguard/... -run 'APM|Onboard|Lifecycle'
```

Then install exactly that pin into an isolated environment and run the producer-named mixed-state E2E. It must include:

- existing APM declarations plus unmanaged Claude/Codex artifacts;
- one duplicate, conflict, unsupported primitive, target-subset item, environment-placeholder MCP item, and local-only package;
- plan-only byte/path read-only assertion;
- reviewed apply, APM audit, v24 write, native activation retirement, and ownership verification;
- crash after APM commit but before first/last Omni fragment rename, followed by automatic resume;
- second run with zero pending work.

Pass criteria: Omni consumes the installed pinned artifact, never the APM checkout; protocol goldens match; all final ownership/config/audit assertions pass.

## Stage 8 — full local regression and DinD

Run serially:

```bash
cd /home/coder/apm
uv run --frozen pytest

cd /home/coder/omni
make test
make lint
make docs-build
make test-integration
```

DinD pass criteria:

- Docker uses the workspace DinD host.
- `Dockerfile.test` installs the exact immutable importer-capable APM pin used by Omni source and CI.
- The `integration-test` target runs `go test -count=1 -tags=integration -race -trimpath` across integration, APM, app, CLI, and provider packages.
- BuildKit cache does not mask the APM pin change; the install layer and onboarding tests execute.
- Zero failures and zero unexpected skips.

## Stage 9 — CI-only platform contracts

Required gates before completion:

- Omni PR CI: Linux unit/shards and DinD integration.
- Omni APM platform-contract matrix: `macos-latest` and `windows-latest`, installing the same immutable APM pin; includes onboarding path/target/JSON contracts.
- APM PR CI: Ubuntu unit/integration plus the real `windows-latest` compatibility gate.
- APM release CI: Ubuntu x86_64/ARM, macOS Intel/ARM, and Windows integration/release validation with onboarding tests selected.
- Real Windows securefile/DACL/reparse tests and macOS case/symlink/atomic-write tests pass; cross-compilation alone does not satisfy this gate.

Pass criteria: required checks are wired, non-optional, and green on the feature revisions.

## Final report fields

After execution replace the status at top and append:

```text
APM revision tested:
Omni revision tested:
Protocol fixture hashes:
Focused APM result:
APM transaction/recovery result:
Wheel smoke result:
Focused Omni/race result:
Pinned mixed-state E2E result:
Full APM result:
Full Omni result:
DinD result:
CI platform result/links:
Tests added by test owner:
Known skips or gaps:
Verdict: APPROVE | REJECT
```

`APPROVE` requires every local stage green, no unresolved blocker, exact protocol agreement, no secret leakage, and required platform CI evidence. Otherwise use `REJECT` and list the first actionable owner/file/test.
