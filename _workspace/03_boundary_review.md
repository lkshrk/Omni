# Cross-repository boundary review — round 3

## Verdict

**APPROVE**

All previously rejected cross-repository boundaries are repaired and have fresh local evidence. No local production or test-wiring blocker remains.

## Verified boundaries

| Boundary | Verdict | Evidence |
| --- | --- | --- |
| Exclusion identity/scope | PASS | `/home/coder/apm/src/apm_cli/importing/service.py:448-460` matches ID, kind, name, root, normalized targets, and content fingerprint. Mismatch becomes `excluded-changed` and cannot import until explicit removal. Committed tests cover identity/root/target/fingerprint drift, foreign-entry preservation, list, and remove. |
| Five shared protocol artifacts | PASS | Candidate, envelope, plan, result schemas and `envelopes-v1.json` are byte-identical across APM and Omni. `internal/apm/protocol_schema_test.go:11-27` pins all five SHA-256 values. |
| Durable real TUI journey | PASS | `integration_tests/tui_agents_integration_test.go:93-235` covers cancel-before-mutation, target selection, MCP secret mapping, executable approval, conflict origin selection, conditional exclusion, apply, joined status, cleanup preview/confirmation, and default state directory against the real local APM binary. |
| Durable exclusion tests | PASS | `/home/coder/apm/tests/unit/importing/test_service.py:1046-1091` covers unchanged exclusion, root/target/fingerprint changes, foreign entries, list, and remove. CLI exclusion envelopes are covered by the importer/CLI suite. |
| Real subprocess serialization | PASS | `/home/coder/apm/tests/integration/test_import_cli.py:224-303` starts concurrent real `apm` apply and resume subprocesses for the same operation; all serialize and return terminal `complete`. In-process concurrent apply/resume and lifecycle fencing tests also pass. |
| MCP placeholder boundary | PASS | Legacy literal keys become reviewed blocked JSON pointers; `map-secret` replaces values only with `${VAR}`; canonicalization removes `env_literal`, `headers_literal`, `auth`, and literal-secret markers. `internal/app/agents_onboard_integration_test.go:25-141` proves the final APM manifest and lockfile contain `${API_TOKEN}` and no literal/legacy secret representation. |
| CI wiring | PASS | APM import unit/transaction/platform/wheel jobs run on PR and merge queue and are required by the merge gate. Omni macOS/Windows platform jobs install the immutable pin, onboarding unit runs under race, and DinD `non-cli` executes the real terminal onboarding integration. |

## Fresh verification

- `uv run --frozen pytest tests/unit/importing tests/integration/test_import_cli.py tests/unit/commands/test_version_read_only.py -q --tb=short` — **72 passed, 4 Windows-only skipped**.
- `PATH=/home/coder/apm/.venv/bin:$PATH go test -count=1 -tags=integration ./internal/app -run TestAgentsOnboardRealPinnedAPM` — **PASS**.
- `PATH=/home/coder/apm/.venv/bin:$PATH go test -count=1 -tags=integration ./integration_tests -run TestTUIAgentsOnboardingPreviewConfirmAndApply` — **PASS**.
- Direct `cmp` for all five shared protocol artifacts — **PASS**.
- APM and Omni workflow `actionlint` — **PASS**.

## External completion gates

These are correctly wired but not locally satisfiable in this review:

1. Leader commits the APM feature revision, updates every Omni immutable pin, and runs the pinned DinD onboarding lane.
2. `windows-latest` runs the four ACL/reparse/component-swap contracts currently skipped on Linux.
3. macOS runners provide runtime filesystem evidence.

Approval is for implementation and test wiring. Final shipping remains conditional on those leader/CI gates passing.
