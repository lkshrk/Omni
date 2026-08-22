# APM implementation handoff

Status: **complete; test, review, and native platform gates approved**

## Implemented

- Strict packaged v1 candidate and reviewed-plan schemas.
- `apm import` scan/apply plus `status`, `resume`, `rollback`, `cleanup`, and `finalize` JSON commands.
- Zero-state-write Claude/Codex planning and read-only `apm --version` probing.
- Union of secured Omni v24 candidates with native Claude/Codex discovery; stable sorted IDs, preimages, candidate-set hash, plan fingerprint, and stale-source rejection.
- Claude: rules, agents, commands, skills, hooks and referenced scripts, plugins, marketplaces, and MCP.
- Codex: `CODEX_HOME` agents, legacy skills, shared `~/.agents` skills, hooks and referenced scripts, plugins, marketplaces, and MCP. `AGENTS.md` remains visible as `codex-instructions-compile-only`.
- Literal MCP env/header/auth/URL-userinfo blocking without secret serialization.
- Legacy Omni entries with omitted agent scope plan as a pre-mutation `needs-choice` blocker (`legacy-unscoped-targets`) when `target_resolution_required:true`; APM never silently narrows or broadens them to Claude/Codex.
- Exact target preservation through per-dependency `targets`; marketplace dependency objects now support target narrowing.
- Bounded immutable local snapshots; traversal, symlink, special-file, size/count, collision, and unapproved-executable rejection.
- Local plugin normalization; marketplace-provenance plugins use normal marketplace dependencies instead of mutable cache snapshots.
- Marketplace registration, in-process APM install, ownership verification, local-plugin activation retirement, and durable exclusion handling including legacy negative state.
- Operation-secured atomic files, POSIX owner-only modes, Windows current-user ACL application, secure backups, durable journal phases, lifecycle fencing, hash-only stdin finalize capability, idempotent finalize/recovery/cleanup.
- Secure roots retain their lexical trust boundary, reject symlink/reparse roots and descendants before writes, and verify resolved containment; journal operation IDs are strict single-component 32-character lowercase hex values and a symlinked journal base is rejected.
- Round-two hardening binds `plan_id`, `resolution_id`, and resolution-derived operation identity; canonical plans are regenerated and immutable bytes compared; terminal journals reject all binding mismatches; approved targets and environment bindings are applied without broadening or secret materialization.
- Durable exclusions now use secured `import-exclusions.yml` identity/scope/fingerprint entries, fail closed when malformed, surface `excluded-changed`, and expose list/remove commands.
- Import apply now accepts only full install success, verifies package ownership, verifies native activation retirement, and runs the real CI baseline audit before any terminal/external-commit state.
- Apply/resume/finalize/rollback/cleanup hold the lifecycle lock before journal/APM reads; resume keeps one outer transaction and refreshes backups after rollback.
- APM state fingerprints cover manifest, lockfile, marketplace registry, exclusion ledger, and imported ownership metadata; target-resolved `CLAUDE.md` and conditional `unsupported` candidates are inventoried.
- Unix secure writes are descriptor-relative with `O_NOFOLLOW`, fsync, and dirfd atomic replacement; Windows ACL hardening grants and verifies protected current-user plus SYSTEM access.
- Round-three repair groups divergent same-key origins into one conflict item, validates `select-origin`, imports only the selected origin, and durably excludes every loser. Conditional group/host state accepts only the explicit Omni `exclude` disposition and is never snapshotted.
- Exclusions match full logical identity, root/scope, normalized targets, and content fingerprint; any changed dimension remains `excluded-changed` until explicit removal, while foreign ledger entries survive list/remove/update operations.
- Snapshot staging and publication route through `SecureRoot`, compare staged bytes with reviewed fingerprints, rehash the source after copy, and reject root/source swaps. Windows writes retain non-reparse component handles and verify an exact protected current-user plus SYSTEM-only DACL.
- Claude hook scripts are discovered per hook file independently of `CLAUDE.md`; `CLAUDE.md` imports as a normal instruction. Structured legacy local/remote packages, skills, and plugins become native APM dependencies rather than snapshots of their Omni fragment.
- Import install/audit is pinned to the global `~/.apm` workspace. The canonical MCP audit view now accepts an on-disk verified local `claude_skill` without `apm.yml`, matching the existing install classifier.
- Reviewed MCP secret mappings retain each environment/header key, replace only its value with `${ENV_VAR}`, canonicalize legacy-only secret containers, and write self-defined servers directly to root `dependencies.mcp`. The import service runs native MCP reconciliation so target config plus lockfile ownership exist before audit; separate secured metadata records the imported candidate without creating a fake local MCP package.
- Native plugin activation is captured before install as byte-exact secured backup content plus original mode. Verification/audit failure restores through a no-follow atomic filesystem capability, verifies bytes/hash/mode exactly, resets to `ownership-verified`, and remains idempotent across repeated resume.
- Codex discovery, configuration, and user-scope primitive deployment share the same `CODEX_HOME` override, defaulting to `~/.codex`; package deployment no longer splits from the runtime’s configured state root.
- Import CI explicitly installs and selects Python 3.12 for sync/build on every OS. Windows DACL verification now uses Win32 security APIs instead of PowerShell module autoload, refuses nonexistent leaves before ACL mutation, and preserves create/write/protect/verify ordering. Windows `file:///C:/...` legacy sources normalize to absolute drive paths.
- Windows SDDL validation parses DACL flags structurally instead of assuming protection is serialized as the literal prefix `D:P`; both `D:P...` and `D:AIP...` are accepted only when `P` is present. It still requires exactly two allow/full-control ACEs for normalized current-user SID plus SYSTEM and emits sanitized ACL metadata on failure.
- Windows ACL hardening no longer shells out to `icacls`/`whoami`: it reads the current process token SID, constructs a protected two-ACE SDDL (current user + SYSTEM, OICI only for directories), converts/applies it through Win32 security APIs, and verifies the exact DACL through the existing strict parser. Pre-existing Local Account, Administrators, Owner Rights, or foreign ACEs are removed rather than tolerated.
- Strict DACL verification also binds ACE inheritance flags to object type: both directory ACEs must contain exactly OI+CI, while file ACEs must contain no inheritance flags. Unknown, missing, duplicated, inherited, or cross-type flags fail closed.
- Win32 SDDL alias normalization accepts `LA` only when the verified current process user SID has RID 500, because Windows canonicalizes that exact local Administrator SID. `LA` for non-500 users and all `BA`/`OW` substitutions remain rejected.
- Required Linux unit/transaction, macOS/Windows platform, clean-wheel smoke CI jobs wired into the merge gate.
- CLI reference documentation.

## Protocol

- Candidate envelope: `schema_version`, `coordinator`, `scope`, `sources`, `candidate_set_id`, `source_preimages`, `candidates`.
- Candidate set ID: SHA-256 of UTF-8 canonical JSON (`sort_keys=true`, separators `,`/`:`, no ASCII escaping) over `{sources,preimages,candidates}` after sorting and deduping by ID.
- CLI JSON is a strict typed envelope: scan uses `{ok:true,kind:"import-plan",plan:{...}}`; apply/status/resume/finalize/rollback/cleanup use their stable `import-*-result` kind with `result`; failures use `{ok:false,kind:"import-error",error:{code,message,operation_id?}}`. Inner results contain exactly `schema_version`, `operation_id`, `coordinator`, `state`, `next_action`, and `finalize_token_required`.
- `standalone` ends at `complete`; `omni-v24` ends at `awaiting-external-commit` and requires `--omni-preimage-set` plus a minimum 256-bit stdin capability. Raw capability bytes never enter JSON, argv, environment, logs, status, or journal.
- Exit 0: success/fence; exit 2: CLI usage; exit 5: protocol incompatibility; exit 6: journaled recoverable partial.
- Canonical schemas: `src/apm_cli/schemas/import-candidates-v1.json`, `import-plan-v1.json`, `import-result-v1.json`, and `import-envelope-v1.json`.

## Changed files

- `.github/workflows/import-onboarding.yml`
- `.github/workflows/merge-gate.yml`
- `docs/src/content/docs/reference/index.md`
- `docs/src/content/docs/reference/cli/import.md`
- `pyproject.toml`
- `src/apm_cli/cli.py`
- `src/apm_cli/commands/import_cmd.py`
- `src/apm_cli/config.py`
- `src/apm_cli/core/auth.py`
- `src/apm_cli/core/experimental.py`
- `src/apm_cli/importing/__init__.py`
- `src/apm_cli/importing/journal.py`
- `src/apm_cli/importing/secure.py`
- `src/apm_cli/importing/service.py`
- `src/apm_cli/install/locking.py`
- `src/apm_cli/integration/mcp_config_view.py`
- `src/apm_cli/integration/targets.py`
- `src/apm_cli/models/dependency/reference.py`
- `src/apm_cli/schemas/import-candidates-v1.json`
- `src/apm_cli/schemas/import-plan-v1.json`
- `src/apm_cli/schemas/import-result-v1.json`
- `src/apm_cli/schemas/import-envelope-v1.json`
- `tests/integration/test_import_cli.py`
- `tests/fixtures/import_protocol/envelopes-v1.json`
- `tests/unit/commands/test_version_read_only.py`
- `tests/unit/importing/test_service.py`
- `tests/unit/importing/test_secure.py`
- `tests/unit/integration/test_mcp_config_view.py`

## Verification

- `uv run pytest tests/unit/importing tests/integration/test_import_cli.py -q --tb=short` -> **11 passed**.
- Post-review security repair: `uv run pytest tests/unit/importing/test_secure.py -q --tb=short` -> **6 passed**; crash/recovery matrix -> **10 passed**; complete importer suite -> **26 passed**.
- Post-review envelope repair: all CLI envelope kinds/goldens/errors -> **9 passed**; importer + integration -> **34 passed**; Ruff -> **pass**; clean installed-wheel strict typed-envelope and four packaged-schema smoke -> **pass**.
- Round-two focused importer/security/crash/tamper/verification suite -> **41 passed**; Ruff -> **pass**.
- Exclusion list/remove CLI smoke -> **pass**, secured `import-exclusions.yml` created; final diff-check -> **pass**.
- Expanded locally runnable evidence: two-sided nine-phase crash/replay matrix, concurrent same-operation apply and resume, lifecycle-fence interleaving, and adversarial root-swap tests -> importer unit suite **44 passed, 2 Windows-only skipped**; Ruff -> **pass**. Windows ACL/reparse runtime tests are wired into the existing `Import Platform Contracts (Windows)` CI job.
- Cross-lane deterministic identity repair sorts resolution entries by `item_id` in generation and validation; a multi-item real CLI fixture plus order-permutation regression passes. Plan reason codes now expose stable `executable:<relative-path>` and `secret-field:</json/pointer>` resolution metadata. Final focused suite: **54 passed, 2 Windows-only skipped**; Ruff -> **pass**.
- Read-only version + importer/shared-seam aggregate -> **140 passed**.
- Full unit command: `uv run pytest tests/unit tests/test_console.py -n auto --dist worksteal -q --tb=short` -> **19,890 passed, 2 skipped, 21 xfailed, 1 docs-link failure**. The only failure was the new public `import` command missing from the reference index; fixed immediately.
- `uv run pytest tests/unit/test_cli_docs_contract.py -q --tb=short` after fix -> **6 passed**.
- `uv run ruff check src/apm_cli tests/unit/importing tests/integration/test_import_cli.py tests/unit/commands/test_version_read_only.py` -> **pass**.
- `actionlint .github/workflows/import-onboarding.yml .github/workflows/merge-gate.yml` -> **pass**.
- `git diff --check` -> **pass**.
- Clean built-wheel install using `uv venv` + `uv pip install`, then installed `apm import --global --from claude --from codex --format json` and packaged-schema checks -> **pass**.
- Exact empty-HOME `.venv/bin/apm --version` probe -> **no `.apm` created**.
- Round-three importer/integration/MCP audit subset -> **88 passed, 4 Windows-only skipped**; subprocess apply/resume contention, grouped conflict selection, conditional exclusion, exclusion identity lifecycle, Claude hook matrix, staged/source tamper, root swap, exact DACL parser, and structured local/remote dependency mapping are covered.
- Real cross-repository coordinator: `go test -tags=integration ./internal/app -run TestAgentsOnboardRealPinnedAPM -count=1 -v` -> **pass**.
- Expanded real terminal coordinator: `PATH=/home/coder/apm/.venv/bin:$PATH go test -tags=integration ./integration_tests -run TestTUIAgentsOnboardingPreviewConfirmAndApply -count=1 -v` -> **pass**, including secret mapping, target choice, conflict selection, conditional exclusion, executable approval, and durable ignore.
- Final MCP/import regression subset -> **91 passed, 4 Windows-only skipped**; direct manifest and lock assertions prove `env_literal`/blocked sentinels/literals do not survive apply. Ruff and diff-check -> **pass**.
- Native MCP reconciliation/lockfile regression subset -> **67 passed**.
- Final activation-restoration/importer subset -> **71 passed, 4 Windows-only skipped**; both post-retirement verification and audit failures restore exact compact bytes/mode and two resumes complete. Ruff and diff-check -> **pass**.
- APM target/scope/Codex focused subset -> **192 passed**, including required custom-`CODEX_HOME` deployment; complete `tests/unit/integration` -> **1,959 passed**; Ruff/diff-check -> **pass**. The Omni tagged fixture’s prior `~/.codex` expectation was stale and is owned by the Omni lane.
- Platform-CI repair subset -> **72 passed, 5 platform-only skipped** on Linux; Ruff, actionlint, and diff-check -> **pass**. Windows runtime coverage includes exact DACL, extra-ACE rejection, reparse/component swaps, nonexistent leaves, and drive-letter file URLs; macOS/Windows jobs are pinned to Python 3.12.
- SDDL parser repair subset -> **8 passed, 5 Windows-only skipped**; Ruff, actionlint, and diff-check -> **pass**.
- Exact Win32 DACL hardening subset -> **62 passed, 5 Windows-only skipped**; extra-ACE injection is rejected before hardening and proven removed afterward. Ruff, actionlint, and diff-check -> **pass**.
- ACE inheritance repair subset -> **63 passed, 5 Windows-only skipped**; pure wrong/missing-flag cases plus Windows directory/file read-back are covered. Ruff, actionlint, and diff-check -> **pass**.
- RID-500 `LA` alias repair subset -> **64 passed, 5 Windows-only skipped**; positive RID-500 and negative non-500/BA/OW cases are covered. Ruff, actionlint, and diff-check -> **pass**.
- Full unit command after CLI help repair -> **19,937 passed, 7 skipped, 21 xfailed, 99 subtests passed**.

## Remaining evidence / blockers

- No known implementation blocker.
- APM Import Onboarding run `32605452767` passed Linux unit/transaction/wheel and native macOS/Windows platform jobs at final commit `fe2d55f37062a9147ae297d7d4c8a034c327661c`.
- Omni CI run `32605540672` passed every unit, lint, vet, native platform, pinned onboarding DinD, and Docker integration job against that commit.
