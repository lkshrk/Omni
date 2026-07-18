# Test Report: MCP server headers

**Test spec**: [test-spec.md](./test-spec.md)
**Branch / commit**: `feat/mcp-server-headers` / uncommitted
**Last updated**: 2026-07-18
**Tester**: Codex

## Summary

- Overall session status: `PASS`
- Added: real Claude Code 2.1.214, Codex CLI 0.144.5, and checksum-verified official Grok CLI 0.2.103 loopback request capture; import, reconciliation, rollback, validation, and concurrent-write coverage.
- Blocking findings: none.
- Known gaps: none within scope; CRLF validation was explicitly excluded.

## Commands Run

| Command | Result | Notes |
| --- | --- | --- |
| `go test -count=1 -race ./internal/app ./internal/config` | `PASS` | Includes cancellation-safe rollback, adoption conflicts, concurrent complete Codex add transactions, pre-side-effect validation, and RFC header names |
| `go test -count=1 -race ./internal/cli ./internal/tui` | `PASS` | CLI 60.6s; TUI 140.9s |
| `go test -count=1 -tags=integration -run 'TestCLI/agents-mcp-headers-claude' -v ./integration_tests` | `PASS` | Real Claude HTTP/SSE loopback egress |
| `go test -count=1 -tags=integration -run 'TestCLI/agents-mcp-headers-codex$' -v ./integration_tests` | `PASS` | Real Codex parsing, migration, valid quoted TOML key, and update flow; 2.3s |
| focused lifecycle + real-client integration commands | `PASS` | Import, CLI add, Claude/Codex/Grok egress, and reconciliation |
| initial `BUILDKIT_PROGRESS=plain make test-integration-build` after RFC validation | `FAIL` | Exposed an invalid `X-\"Quoted\"` test fixture; replaced with valid non-bare token `X~Quoted` and reran |
| final `BUILDKIT_PROGRESS=plain make test-integration-build` | `PASS` | Grok SHA-256/version gate, Docker untagged race suite, and all tagged integration/provider tests; completed in 386.4s |
| `make gen-schema` | `PASS` | v19 and current schemas regenerated |
| `make lint` | `PASS` | 0 issues |
| `git diff --check` | `PASS` | No whitespace errors |

## Tests Added Or Updated

| Type | Files | Tests |
| --- | ---: | ---: |
| Unit | 9 | Adapter parsing, lifecycle rollback, import conflicts, validation, locking, and CLI/TUI flows |
| Integration | 9 | Harness, CLI add/import, version expectations, and real Claude/Codex/Grok scenarios |
| **Total** | **18** | Files containing behavior-focused cases and scenarios |

### File List

- `integration_tests/testdata/scripts/agents-mcp-headers-codex.txtar` (1 testscript)
- `integration_tests/testdata/scripts/agents-mcp-headers-claude.txtar` (1 testscript)
- `internal/app/mcp_codex_adapter_test.go` (TOML, rollback, stale-write, and concurrent-writer tests)
- `integration_tests/testdata/scripts/agents-mcp-headers-codex-wire.txtar` (1 testscript)
- `integration_tests/testdata/scripts/agents-mcp-headers-grok.txtar` (1 testscript)
- `integration_tests/testdata/scripts/agents-mcp-add.txtar` and `agents-mcp-import.txtar` (CLI lifecycle coverage)
- `internal/app/mcp_ops_test.go`, `internal/cli/agents_test.go`, and `internal/tui/claim_scope_test.go` (reconciliation/import coverage)
- `internal/app/mcp_claude_adapter_test.go` (HTTP/SSE cases updated)
- `internal/app/mcp_grok_adapter_test.go` (HTTP case updated; SSE case added)
- `internal/config/migration_test.go` (1 test)
- Existing current-version expectations updated in two fixtures; stale dots-state integration constant repaired.

## Coverage Matrix

| Behavior | Priority | Test type | Coverage | Session result | Notes / gap |
| --- | --- | --- | --- | --- | --- |
| Legacy missing key | UNKNOWN | integration | COVERED | PASS | v18 manifest migrated and restored in Docker |
| Claude headers | UNKNOWN | integration | COVERED | PASS | Real Claude HTTP/SSE requests emitted literal and expanded env headers; secret absent from persisted config |
| Grok headers | UNKNOWN | unit + integration | COVERED | PASS | Real pinned Grok emitted, listed, and reconciled headers without login |
| Codex literal/env headers | UNKNOWN | integration | COVERED | PASS | Real Codex parsed and emitted headers without credentials; secrets absent from TOML |
| TOML upsert and atomic write | UNKNOWN | unit + integration | COVERED | PASS | Quoted-table replacement, stale snapshot, concurrent full-add serialization, symlink/mode, default home, and rollback paths covered |
| SSE forwarding | UNKNOWN | unit + integration | COVERED | PASS | Real Claude SSE egress plus positive Claude/Grok/Codex adapter tests |
| Header import | UNKNOWN | unit + integration | COVERED | PASS | Claude/Codex/Grok list parsing and CLI/TUI/bulk adoption preserve headers |
| Existing registration | UNKNOWN | unit + integration | COVERED | PASS | Dry-run, update, remove failure, add failure rollback, and real agent flows covered |
| CLI add | UNKNOWN | integration | COVERED | PASS | Repeatable headers plus malformed/duplicate/stdio rejection covered |
| Header-name validation | UNKNOWN | unit + integration | COVERED | PASS | RFC field-name tokens accepted; colon/space names rejected before adapter calls; valid non-bare TOML key parsed by real Codex |
| Grok artifact integrity | UNKNOWN | Docker | COVERED | PASS | AMD64/ARM64 digests pinned; target digest verified before execution |

## Deferred / Residual Risk

- CRLF-specific header validation is excluded by explicit user instruction.

## Cleanup

- Testscript uses temporary directories only.

## Coverage Summary

- Total testing whats: 11
- COVERED: 11
- PARTIAL: 0
- NOT COVERED: 0
