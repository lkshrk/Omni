# Test Spec: MCP server headers

**Scope**: feature
**Source material**: user request; `internal/config/config.go`; MCP adapters
**Testing posture**: baked-in default
**Test report**: [test-report.md](./test-report.md)

## Testing What

- A v18 manifest without `headers` migrates and restores normally.
- Literal remote headers reach each supported agent CLI.
- Real Claude HTTP and SSE connection attempts emit configured headers.
- Claude expands `${VAR}` at request time without persisting the secret value.
- Real Codex and Grok connection attempts emit configured headers without credentials.
- Exact `${VAR}` values become Codex `env_http_headers` entries without writing the secret.
- Codex accepts and reports Omni-generated TOML.
- Claude, Codex, and Grok list/import paths preserve reported headers and `${VAR}` references.
- Changed headers on an installed registration are reported by dry-run and reconciled with rollback.
- `agents mcp add --header 'NAME: VALUE'` validates and persists repeatable headers.
- Repeated header persistence does not create duplicate TOML tables.
- Codex header persistence preserves config symlinks/modes, rejects stale snapshots, and reports rollback failures.
- Concurrent Omni writers serialize each complete Codex remote add/header/rollback transaction and preserve both changes.
- Header names must be valid RFC HTTP field-name tokens.
- Docker verifies pinned per-architecture Grok binary checksums before execution.
- HTTP and SSE transports forward headers through every adapter.
- Explicit removal and restore applies changed headers to an existing registration.

## Evidence Strategy

No upstream priority was declared; all priorities are `UNKNOWN`.

| What | Priority | Selected How | Residual Risk |
| --- | --- | --- | --- |
| Legacy missing key | UNKNOWN | Docker integration + unit migration | None intended |
| Claude headers | UNKNOWN | Real CLI Docker integration with loopback request capture + unit tests | None intended |
| Grok headers | UNKNOWN | Pinned real CLI Docker request capture + parsing/reconciliation tests | None intended |
| Codex literal/env headers | UNKNOWN | Real CLI Docker parsing + credential-free request capture + unit tests | None intended |
| TOML upsert and atomic write | UNKNOWN | Real Codex parsing + concurrent full-transaction unit tests | None intended for CLI-valid server names |
| SSE forwarding | UNKNOWN | Real Claude request capture + adapter unit tests | None intended |
| Header import | UNKNOWN | Real Claude/Codex/Grok list parsing + CLI/TUI/bulk adoption tests | Environment values remain intentionally unavailable except header `${VAR}` references |
| Existing registration | UNKNOWN | Real Claude/Codex/Grok reconciliation + rollback unit tests | None intended for agents that report headers |
| CLI add | UNKNOWN | Testscript integration | None intended |
| Header-name validation | UNKNOWN | Config unit tests + real Codex integration | None intended |
| Grok artifact integrity | UNKNOWN | Docker SHA-256 verification + version check | Checksums are maintained in-repo when the pinned version changes |

Out of scope: CRLF-specific validation, explicitly excluded by the user.

## Test Cases

### MCP-HDR-T01 Fresh Codex restore

- Automated checks: `go test -tags=integration -run 'TestCLI/agents-mcp-headers-codex' ./integration_tests`
- Pass criteria: Codex parses literal, empty, escaped, Unicode, and environment-backed headers; no secret appears in TOML; missing legacy key is accepted.

### MCP-HDR-T02 Changed Codex registration

- Automated checks: same Docker integration test target.
- Pass criteria: restore detects changed headers and replaces the registration without manual removal or duplicate tables.

### MCP-HDR-T03 Adapter contracts

- Automated checks: `go test -race ./internal/app ./internal/config`
- Pass criteria: HTTP/SSE adapter arguments, migration, RFC field-name validation, merge, cancellation-safe rollback, default home, stale-write rejection, complete-add serialization, symlink/mode preservation, and TOML upsert tests pass.

### MCP-HDR-T04 Fresh Claude restore

- Automated checks: `go test -tags=integration -run 'TestCLI/agents-mcp-headers-claude' ./integration_tests`
- Pass criteria: real Claude HTTP and SSE requests emit literal and `${VAR}` headers; persisted config retains the placeholder and not its expanded value.

### MCP-HDR-T05 Import and reconciliation

- Automated checks: real CLI testscript fixtures plus `internal/app`, `internal/cli`, and `internal/tui` tests.
- Pass criteria: imports preserve reported headers; conflicting same-name headers are rejected; dry-run reports drift; updates remove/re-add; failed updates restore the previous registration.

### MCP-HDR-T06 Real Codex and Grok egress

- Automated checks: credential-free loopback request capture using pinned Codex 0.144.5 and Grok 0.2.103.
- Pass criteria: both real clients emit Omni-configured headers; Grok list/reconciliation preserves them.

### MCP-HDR-T07 Docker client integrity

- Automated checks: `BUILDKIT_PROGRESS=plain make test-integration-build`.
- Pass criteria: the pinned Grok artifact matches the in-repo checksum for the target architecture before `grok --version` or tests execute.

## Fixtures And Environments

- Docker image pins Claude Code, Codex CLI, and official Grok CLI versions, verifies Grok per-architecture SHA-256, and uses temporary `HOME`/`CODEX_HOME`.
- Header values are synthetic; Claude request capture uses a loopback test server and no external MCP endpoint.

## Report Expectations

Record Docker, focused race, lint, and schema-generation results in `test-report.md`.
