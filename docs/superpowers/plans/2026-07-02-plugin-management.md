# Plugin Management (Agents Phase 4 MVP) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the stubbed `plugin` filter chip into real management of Claude Code / Codex agent plugins and their marketplaces, declared once in omni's manifest and restored via the agents' own CLIs — mirroring the just-shipped MCP server feature exactly.

**Architecture:** Same three-layer shape as MCP: `internal/config` gains `Marketplace`/`Plugin` manifest types + validation + a v11 migration; `internal/app` gains a `PluginAdapter` interface with claude/codex implementations that delegate to `claude plugins …` / `codex plugin …`, plus ops (restore/add/remove/retarget/import/status) that are tolerant of per-adapter failures and always persist manifest intent; `internal/cli` adds `agents plugins …` subcommands; `internal/tui` clones the mcp chip behavior onto `skillTypeIdx == 2`.

**Tech Stack:** Go, Cobra CLI, Bubbletea TUI (`charm.land/bubbletea/v2`), `rogpeppe/go-internal/testscript` (txtar), stdlib `os/exec`-injected command execution.

## Global Constraints

- Spec of record: `docs/superpowers/specs/2026-07-02-plugin-management-design.md`. Every decision there is binding; this plan does not relitigate them.
- Write-target agents (MVP): Claude Code and Codex only, behind a `PluginAdapter` interface. No direct config-file writes — omni only ever delegates to the agents' own CLIs.
- No version pinning: the manifest stores plugin/marketplace identity only (name, marketplace, agents / name, source, agents).
- `Plugin.Marketplace` must reference a declared `Marketplace.Name` — this is a HARD validation error (unlike the warn-level group-ref checks used elsewhere), because a dangling reference makes restore impossible.
- omni **never** removes marketplaces from an agent. There is no `RemoveMarketplace` on the adapter interface. `plugins marketplace remove` deletes only the manifest entry.
- Unmanaged plugins: detection lists them; explicit `import <name>` adopts identity only (name + marketplace, never version/enable state). omni never edits or removes what it did not add.
- Enable/disable state is out of scope for MVP (claude-only concept, not reconciled).
- `GroupConfig.Plugins []string` mirrors `Skills`/`McpServers`: named plugins restore only on hosts in that group; ungrouped plugins restore everywhere. Marketplaces are never themselves group-targeted — a marketplace restores wherever a plugin needing it restores.
- Manifest = intent: every mutating op (`add`, `remove`, `restore` is read-only so N/A, `retarget`, `import`) persists the manifest write regardless of individual adapter failures; adapter failures are collected and returned, never silently dropped, never block the manifest write.
- Per-adapter/per-item tolerance: one item or one adapter failing must not abort the rest of a batch operation.
- No `_ = someFunc()` / `_, _ = someFunc()` silent error discards anywhere in production code. Exceptions only for `defer f.Close()`-style cleanup or an explicitly commented no-op.
- Comments: near-zero. One-line WHY-only comments where a non-obvious invariant or workaround needs explaining; never restate what the code says.
- Conventional Commits, one commit per logical change (`feat(config): ...`, `feat(app): ...`, `feat(cli): ...`, `feat(tui): ...`, `test(...): ...`).
- TUI tests are written ONLY by dispatching to the **tui-tester** agent. txtar integration fixtures are written ONLY by dispatching to the **txtar-writer** agent. Every other test (config, app, cli) is written inline as real, running Go test code — full TDD, no "write tests for the above" placeholders.
- CLI tests must be **behavioral**: they execute real Cobra commands against a real `*app.App` built with fake adapters and assert on captured output/manifest state — never registration-only assertions (this was the MCP retro lesson; `agents_test.go`'s `newImportTestApp`/`runAgentsMcpImport` harness is the template).
- The MCP feature shipped with both list parsers matching **zero** real CLI output lines because the live-probe step was skipped. This must not recur: Task 1 is blocked from proceeding past its probe step, and Tasks 2/3's parser tests are built from the literal probe transcript, not assumption.
- Live probes and the live smoke task (Task 9) run with `CLAUDE_CONFIG_DIR` / `CODEX_HOME` (and `HOME`) pointed at fresh temp directories — **never** the real `~/.claude*` or `~/.codex` on the machine running the plan.
- Settings format version bumps from 10 to 11 (additive; no field removed or renamed).

---

## Task 1: Live CLI Probe + Config Types (Marketplace/Plugin, validation, v11, schema)

**Files:**
- Create: `docs/superpowers/research/2026-07-02-plugin-cli-probe.md` (raw probe transcript, committed as project documentation of ground truth)
- Modify: `internal/config/config.go` (add `Marketplace`, `Plugin` types; `AgentsConfig.Marketplaces`/`.Plugins`; `GroupConfig.Plugins`; validation; `CurrentVersion = 11`)
- Modify: `internal/config/loader.go` (register `{from: 10, to: 11, apply: migrateConfigV10ToV11, applyRaw: migrateRawConfigV10ToV11}`, add the two trivial migration functions)
- Test: `internal/config/config_test.go` (or sibling `_test.go` — follow existing file split in that package) for `validateMarketplaces`/`validatePlugins`/group-plugin-ref warnings
- Modify: `spec/omni.settings.schema.json`, create `spec/omni.settings.v11.schema.json` (generated, not hand-edited)

**Interfaces:**
- Consumes: nothing from this plan (first task).
- Produces (used by every later task):
  - `config.Marketplace{Name string, Source string, Agents []string}` (json: `name`, `source`, `agents,omitempty`)
  - `config.Plugin{Name string, Marketplace string, Agents []string}` (json: `name`, `marketplace`, `agents,omitempty`)
  - `cfg.Agents.Marketplaces []config.Marketplace`, `cfg.Agents.Plugins []config.Plugin`
  - `GroupConfig.Plugins []string`
  - `config.CurrentVersion == 11`
  - The probe transcript file, which Tasks 2 and 3 read verbatim to build parser fixtures.

### Step 1: Run the live CLI probe in a fully sandboxed environment

Run every command below from a scratch directory. Do not touch the invoking user's real `~/.claude*` or `~/.codex` — point every relevant env var at fresh temp dirs first:

```bash
PROBE_DIR="$(mktemp -d)"
mkdir -p "$PROBE_DIR/claude-config" "$PROBE_DIR/codex-home" "$PROBE_DIR/home"
export CLAUDE_CONFIG_DIR="$PROBE_DIR/claude-config"
export CODEX_HOME="$PROBE_DIR/codex-home"
export HOME="$PROBE_DIR/home"
echo "$PROBE_DIR" # note this path, you will need it below
```

With those exported in the current shell, run each of the following and capture full stdout+stderr+exit code:

```bash
/opt/homebrew/bin/claude plugins --help
/opt/homebrew/bin/claude plugins list --help
/opt/homebrew/bin/claude plugins list --json
/opt/homebrew/bin/claude plugins install --help
/opt/homebrew/bin/claude plugins uninstall --help
/opt/homebrew/bin/claude plugins marketplace --help
/opt/homebrew/bin/claude plugins marketplace list --help
/opt/homebrew/bin/claude plugins marketplace add --help
/opt/homebrew/bin/claude plugins marketplace remove --help

/opt/homebrew/bin/codex plugin --help
/opt/homebrew/bin/codex plugin list --help
/opt/homebrew/bin/codex plugin add --help
/opt/homebrew/bin/codex plugin remove --help
/opt/homebrew/bin/codex plugin marketplace --help
/opt/homebrew/bin/codex plugin marketplace add --help
/opt/homebrew/bin/codex plugin marketplace list --help
/opt/homebrew/bin/codex plugin marketplace remove --help
/opt/homebrew/bin/codex plugin marketplace upgrade --help
```

Then, still inside the sandbox, actually add one real marketplace and one real plugin to each agent (pick any small public marketplace/plugin you have access to, or the `lkshrk/agent-marketplace` one named in the spec) and re-run the `list`/`list --json` variants against a non-empty state, e.g.:

```bash
/opt/homebrew/bin/claude plugins marketplace add lkshrk/agent-marketplace
/opt/homebrew/bin/claude plugins install caveman@caveman
/opt/homebrew/bin/claude plugins list --json
/opt/homebrew/bin/claude plugins marketplace list

/opt/homebrew/bin/codex plugin marketplace add lkshrk/agent-marketplace
/opt/homebrew/bin/codex plugin add caveman
/opt/homebrew/bin/codex plugin list --json   # if --json exists; else plain list
/opt/homebrew/bin/codex plugin marketplace list
```

Finally probe removal syntax without actually running it destructively where possible (`--help` output is enough to confirm the exact subcommand/flag names for uninstall/remove), then actually remove what you added so the sandbox dirs can be discarded.

### Step 2: Save the transcript

Write every command run in Step 1 and its exact captured output (stdout, stderr, exit code) to `docs/superpowers/research/2026-07-02-plugin-cli-probe.md`, one `### <command>` heading per invocation with a fenced code block containing the literal output. Do not paraphrase or summarize output — Tasks 2 and 3 depend on literal text for parser fixtures. If any command differs from what the spec's "Ground truth" section assumed (e.g., claude's identity separator is not `name@marketplace`, or codex has no `--json` list flag), add a `## Deviations from spec assumptions` section at the top of the file calling it out explicitly — later tasks must follow the transcript over the spec when they conflict.

### Step 3: Add the `Marketplace` and `Plugin` config types

In `internal/config/config.go`, add just below the `McpServer` type (after line 431):

```go
// Marketplace is one plugin marketplace entry in the omni manifest. Source is
// whatever form the agent CLIs accept for `plugins marketplace add` / `plugin
// marketplace add` (owner/repo or URL) — verified against the probe transcript
// in docs/superpowers/research/2026-07-02-plugin-cli-probe.md.
type Marketplace struct {
	Name   string   `json:"name"`
	Source string   `json:"source"`
	Agents []string `json:"agents,omitempty"`
}

// Plugin is one plugin entry in the omni manifest. Marketplace must reference
// a declared Marketplace.Name — validated as a hard error, since a dangling
// reference makes restore impossible.
type Plugin struct {
	Name        string   `json:"name"`
	Marketplace string   `json:"marketplace"`
	Agents      []string `json:"agents,omitempty"`
}
```

Extend `AgentsConfig` (around line 401-407):

```go
type AgentsConfig struct {
	Packages     []SkillPackage `json:"packages,omitempty"`
	McpServers   []McpServer    `json:"mcp_servers,omitempty"`
	Marketplaces []Marketplace  `json:"marketplaces,omitempty"`
	Plugins      []Plugin       `json:"plugins,omitempty"`
	// Skills is the legacy per-skill manifest, retained only so a one-time
	// migration can fold it into Packages. Never written back.
	Skills []ManifestSkill `json:"skills,omitempty"`
}
```

Add `Plugins []string` to `GroupConfig` (around line 352-363), following the `McpServers` field:

```go
type GroupConfig struct {
	Name        string      `json:"name,omitempty"`
	Special     string      `json:"special,omitempty"`
	Description string      `json:"description,omitempty"`
	Taps        []string    `json:"taps,omitempty"`
	Tools       []ToolEntry `json:"tools,omitempty"`
	Dots        []DotEntry  `json:"dots,omitempty"`
	Skills      []string    `json:"skills,omitempty"`
	McpServers  []string    `json:"mcp_servers,omitempty"`
	Plugins     []string    `json:"plugins,omitempty"`
	Ignore      []string    `json:"-"`
}
```

### Step 4: Write failing validation tests

Create/extend a config test file (match the existing split — e.g. add to whichever `_test.go` file already has `TestValidateMcpServers`-style tests, or create `internal/config/plugins_test.go` if no obvious sibling exists):

```go
package config_test

import (
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestValidateRoot_PluginMissingMarketplace_IsHardError(t *testing.T) {
	cfg := &config.RootConfig{
		Agents: config.AgentsConfig{
			Plugins: []config.Plugin{{Name: "caveman", Marketplace: "does-not-exist"}},
		},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{})
	found := false
	for _, e := range errs {
		if e.Warn {
			continue
		}
		if e.Path == `$.agents.plugins[0].marketplace` {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected hard error for unknown plugin marketplace ref, got %+v", errs)
	}
}

func TestValidateRoot_PluginWithDeclaredMarketplace_NoError(t *testing.T) {
	cfg := &config.RootConfig{
		Agents: config.AgentsConfig{
			Marketplaces: []config.Marketplace{{Name: "caveman", Source: "lkshrk/agent-marketplace"}},
			Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman"}},
		},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{})
	for _, e := range errs {
		if !e.Warn {
			t.Fatalf("unexpected hard error: %+v", e)
		}
	}
}

func TestValidateRoot_DuplicateMarketplaceName_IsError(t *testing.T) {
	cfg := &config.RootConfig{
		Agents: config.AgentsConfig{
			Marketplaces: []config.Marketplace{
				{Name: "caveman", Source: "a/b"},
				{Name: "caveman", Source: "c/d"},
			},
		},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{})
	found := false
	for _, e := range errs {
		if !e.Warn && e.Path == `$.agents.marketplaces[1].name` {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected duplicate marketplace name error, got %+v", errs)
	}
}

func TestValidateRoot_GroupPluginRef_UnknownIsWarnOnly(t *testing.T) {
	cfg := &config.RootConfig{
		Groups: []*config.GroupConfig{{Name: "work", Plugins: []string{"ghost"}}},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{})
	found := false
	for _, e := range errs {
		if e.Path == `$.groups[0].plugins[0]` {
			if !e.Warn {
				t.Fatalf("expected warn-level error for group plugin ref, got hard error: %+v", e)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("expected a warn-level error for unknown group plugin ref")
	}
}
```

### Step 5: Run the tests and confirm they fail

Run: `go test ./internal/config/... -run TestValidateRoot_Plugin -v` and `go test ./internal/config/... -run TestValidateRoot_DuplicateMarketplace -v` and `go test ./internal/config/... -run TestValidateRoot_GroupPluginRef -v`
Expected: FAIL — `config.Marketplace`/`config.Plugin`/`GroupConfig.Plugins` compile errors (types don't exist yet) turn into build failures; that counts as "fails" for this step. Confirm the failure is the missing-type compile error, not something else.

### Step 6: Implement validation in `ValidateRoot`

In `internal/config/config.go`, add two new validators and call them from `ValidateRoot` right after the existing `validateMcpServers` call (around line 652):

```go
	errs = append(errs, validateMcpServers(cfg.Agents.McpServers, "$.agents.mcp_servers")...)
	marketplaceNames := make(map[string]struct{}, len(cfg.Agents.Marketplaces))
	errs = append(errs, validateMarketplaces(cfg.Agents.Marketplaces, marketplaceNames, "$.agents.marketplaces")...)
	errs = append(errs, validatePlugins(cfg.Agents.Plugins, marketplaceNames, "$.agents.plugins")...)

	pluginNames := make(map[string]struct{}, len(cfg.Agents.Plugins))
	for _, p := range cfg.Agents.Plugins {
		if strings.TrimSpace(p.Name) != "" {
			pluginNames[p.Name] = struct{}{}
		}
	}
```

Then, in the per-group loop (right after the existing `for mi, name := range g.McpServers { ... }` block, around line 768), add:

```go
		for pi, name := range g.Plugins {
			if _, ok := pluginNames[name]; !ok {
				errs = append(errs, ValidationError{
					Path:    fmt.Sprintf("$.groups[%d].plugins[%d]", gi, pi),
					Message: fmt.Sprintf("group plugin ref %q has no matching plugin in agents.plugins", name),
					Warn:    true,
				})
			}
		}
```

Add the two new validator functions near `validateMcpServers`:

```go
func validateMarketplaces(marketplaces []Marketplace, names map[string]struct{}, path string) []ValidationError {
	var errs []ValidationError
	for i, mkt := range marketplaces {
		p := fmt.Sprintf("%s[%d]", path, i)
		if strings.TrimSpace(mkt.Name) == "" {
			errs = append(errs, ValidationError{Path: p + ".name", Message: "marketplace name must not be empty"})
			continue
		}
		if _, dup := names[mkt.Name]; dup {
			errs = append(errs, ValidationError{Path: p + ".name", Message: fmt.Sprintf("duplicate marketplace name %q", mkt.Name)})
		}
		names[mkt.Name] = struct{}{}
		if strings.TrimSpace(mkt.Source) == "" {
			errs = append(errs, ValidationError{Path: p + ".source", Message: "marketplace source must not be empty"})
		}
	}
	return errs
}

// validatePlugins requires every plugin's Marketplace to reference a declared
// marketplace name — a hard error, unlike the warn-level group refs, because a
// dangling marketplace reference makes restore impossible.
func validatePlugins(plugins []Plugin, marketplaceNames map[string]struct{}, path string) []ValidationError {
	var errs []ValidationError
	seen := make(map[string]struct{}, len(plugins))
	for i, p := range plugins {
		pp := fmt.Sprintf("%s[%d]", path, i)
		if strings.TrimSpace(p.Name) == "" {
			errs = append(errs, ValidationError{Path: pp + ".name", Message: "plugin name must not be empty"})
		} else if _, dup := seen[p.Name]; dup {
			errs = append(errs, ValidationError{Path: pp + ".name", Message: fmt.Sprintf("duplicate plugin name %q", p.Name)})
		}
		seen[p.Name] = struct{}{}
		if strings.TrimSpace(p.Marketplace) == "" {
			errs = append(errs, ValidationError{Path: pp + ".marketplace", Message: "plugin marketplace is required"})
			continue
		}
		if _, ok := marketplaceNames[p.Marketplace]; !ok {
			errs = append(errs, ValidationError{Path: pp + ".marketplace", Message: fmt.Sprintf("plugin marketplace %q has no matching agents.marketplaces entry", p.Marketplace)})
		}
	}
	return errs
}
```

### Step 7: Run the tests and confirm they pass

Run: `go test ./internal/config/... -run 'TestValidateRoot_Plugin|TestValidateRoot_DuplicateMarketplace|TestValidateRoot_GroupPluginRef' -v`
Expected: PASS

### Step 8: Bump `CurrentVersion` and register the v10→v11 migration

In `internal/config/config.go` line 13: `const CurrentVersion = 11`.

In `internal/config/loader.go`, append to `configMigrations` (after the `{from: 9, to: 10, ...}` entry):

```go
	{from: 10, to: 11, apply: migrateConfigV10ToV11, applyRaw: migrateRawConfigV10ToV11},
```

Add the trivial migration functions next to `migrateConfigV9ToV10`/`migrateRawConfigV9ToV10`:

```go
func migrateConfigV10ToV11(cfg *RootConfig) error {
	cfg.Version = 11
	return nil
}

func migrateRawConfigV10ToV11(raw map[string]json.RawMessage) error {
	raw["version"] = json.RawMessage(`11`)
	return nil
}
```

### Step 9: Run the existing generic migration-coverage test

Run: `go test ./internal/config/... -run TestConfigMigrationsCoverCurrentVersion -v`
Expected: PASS — this pre-existing test in `internal/config/migration_test.go` automatically walks the chain from 0 to `CurrentVersion` and fails if any step is missing, so no new dedicated migration test is required for a version-bump-only step.

### Step 10: Regenerate the JSON schema

Run: `make gen-schema`
Expected: creates `spec/omni.settings.v11.schema.json` and updates `spec/omni.settings.schema.json` (the `$id`/`version` const move from 10 to 11; note the `Agents`/`Skills`/`McpServers`/`Plugins`/`Marketplaces` fields are **not** modeled in `scripts/gen-schema/main.go` today — it doesn't emit an `agents` property at all — so no schema property changes are expected beyond the version bump. If the diff shows anything else changed, investigate before committing). Leave `spec/omni.settings.v10.schema.json` untouched (old versioned schemas are kept as history).

### Step 11: Run the full config package test suite

Run: `go test ./internal/config/... -v`
Expected: PASS, no regressions.

### Step 12: Commit

```bash
git add docs/superpowers/research/2026-07-02-plugin-cli-probe.md internal/config/config.go internal/config/loader.go internal/config/*_test.go spec/omni.settings.schema.json spec/omni.settings.v11.schema.json
git commit -m "feat(config): plugin/marketplace manifest types, validation, and v11 migration"
```

---

## Task 2: `PluginAdapter` Interface + Claude Code Adapter

**Files:**
- Create: `internal/app/plugin_adapter.go` (interface + `InstalledPlugin` type)
- Create: `internal/app/plugin_claude_adapter.go`
- Test: `internal/app/plugin_claude_adapter_test.go`

**Interfaces:**
- Consumes: `config.Plugin`, `config.Marketplace` (Task 1); `docs/superpowers/research/2026-07-02-plugin-cli-probe.md` (Task 1) for exact CLI output text.
- Produces (used by Tasks 3, 4, 5, 6, 7):
  - `type PluginAdapter interface { ID() string; Available() bool; ListPlugins(ctx context.Context) ([]InstalledPlugin, error); InstallPlugin(ctx context.Context, p config.Plugin) error; RemovePlugin(ctx context.Context, name string) error; ListMarketplaces(ctx context.Context) ([]string, error); AddMarketplace(ctx context.Context, m config.Marketplace) error }`
  - `type InstalledPlugin struct { Name string; Marketplace string; Version string }`
  - `func NewClaudeCodePluginAdapter(execFn func(context.Context, string, ...string) (string, string, error), lookupEnv func(string) (string, bool)) PluginAdapter`

### Step 1: Read the probe transcript

Open `docs/superpowers/research/2026-07-02-plugin-cli-probe.md` and locate the `claude plugins list --json` and `claude plugins marketplace list` sections. Confirm the exact JSON key names for id/version/scope/enabled (or whatever claude actually emitted) and the exact plugin-identity separator (spec assumed `name@marketplace` — confirm or correct per any `## Deviations` note at the top of the file).

### Step 2: Write the interface and `InstalledPlugin` type

Create `internal/app/plugin_adapter.go`:

```go
package app

import (
	"context"

	"github.com/lkshrk/omni/internal/config"
)

// PluginAdapter manages plugins and marketplaces in one target agent by
// delegating to that agent's own CLI. omni never edits agent config files
// directly, and never removes a marketplace it did not add (see AddMarketplace
// doc comment) — there is deliberately no RemoveMarketplace here.
type PluginAdapter interface {
	ID() string
	Available() bool
	ListPlugins(ctx context.Context) ([]InstalledPlugin, error)
	InstallPlugin(ctx context.Context, p config.Plugin) error
	RemovePlugin(ctx context.Context, name string) error
	ListMarketplaces(ctx context.Context) ([]string, error)
	AddMarketplace(ctx context.Context, m config.Marketplace) error
}

// InstalledPlugin is one plugin as reported by an agent's list output.
// Version is informational only — omni does not pin or reconcile versions.
type InstalledPlugin struct {
	Name        string
	Marketplace string
	Version     string
}
```

### Step 3: Write the failing claude adapter tests

Create `internal/app/plugin_claude_adapter_test.go`. Replace the `<<< paste the exact JSON captured in the probe transcript >>>` fixture block below with the literal output from Step 1 before running the test — this is the one place in this plan where content is intentionally deferred to the live probe rather than assumed:

```go
package app

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestClaudeCodePluginAdapter_ID(t *testing.T) {
	a := NewClaudeCodePluginAdapter(nil, nil)
	if a.ID() != "claude-code" {
		t.Fatalf("got %q", a.ID())
	}
}

func TestClaudeCodePluginAdapter_InstallPlugin(t *testing.T) {
	var gotCmd string
	var gotArgs []string
	exec := func(_ context.Context, cmd string, args ...string) (string, string, error) {
		gotCmd = cmd
		gotArgs = args
		return "", "", nil
	}
	a := NewClaudeCodePluginAdapter(exec, func(string) (string, bool) { return "", false })
	p := config.Plugin{Name: "caveman", Marketplace: "caveman"}
	if err := a.InstallPlugin(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if gotCmd != "claude" {
		t.Fatalf("expected claude binary, got %q", gotCmd)
	}
	want := []string{"plugins", "install", "caveman@caveman"}
	if !mcpSliceEq(gotArgs, want) {
		t.Fatalf("args mismatch\ngot:  %v\nwant: %v", gotArgs, want)
	}
}

func TestClaudeCodePluginAdapter_RemovePlugin(t *testing.T) {
	var gotArgs []string
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		gotArgs = args
		return "", "", nil
	}
	a := NewClaudeCodePluginAdapter(exec, func(string) (string, bool) { return "", false })
	if err := a.RemovePlugin(context.Background(), "caveman"); err != nil {
		t.Fatal(err)
	}
	want := []string{"plugins", "uninstall", "caveman"}
	if !mcpSliceEq(gotArgs, want) {
		t.Fatalf("args mismatch\ngot:  %v\nwant: %v", gotArgs, want)
	}
}

func TestClaudeCodePluginAdapter_AddMarketplace(t *testing.T) {
	var gotArgs []string
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		gotArgs = args
		return "", "", nil
	}
	a := NewClaudeCodePluginAdapter(exec, func(string) (string, bool) { return "", false })
	m := config.Marketplace{Name: "caveman", Source: "lkshrk/agent-marketplace"}
	if err := a.AddMarketplace(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	want := []string{"plugins", "marketplace", "add", "lkshrk/agent-marketplace"}
	if !mcpSliceEq(gotArgs, want) {
		t.Fatalf("args mismatch\ngot:  %v\nwant: %v", gotArgs, want)
	}
}

// claudePluginListFixture is the exact stdout of `claude plugins list --json`
// captured in docs/superpowers/research/2026-07-02-plugin-cli-probe.md.
// <<< paste the exact JSON captured in the probe transcript >>>
const claudePluginListFixture = `[]`

func TestClaudeCodePluginAdapter_ListPlugins_ParsesRealFixture(t *testing.T) {
	exec := func(_ context.Context, _ string, _ ...string) (string, string, error) {
		return claudePluginListFixture, "", nil
	}
	a := NewClaudeCodePluginAdapter(exec, nil)
	plugins, err := a.ListPlugins(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = plugins // exact assertions filled in once the fixture is real; see Step 5
}

func TestClaudeCodePluginAdapter_ListMarketplaces_ParsesRealFixture(t *testing.T) {
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		return "", "", nil
	}
	a := NewClaudeCodePluginAdapter(exec, nil)
	if _, err := a.ListMarketplaces(context.Background()); err != nil {
		t.Fatal(err)
	}
}
```

### Step 4: Run the tests to confirm they fail

Run: `go test ./internal/app/... -run TestClaudeCodePluginAdapter -v`
Expected: FAIL with `NewClaudeCodePluginAdapter` undefined (compile error).

### Step 5: Implement the claude adapter using the literal probe output

Create `internal/app/plugin_claude_adapter.go`. Base the `List`/marketplace-list parsing on the **literal** JSON/text captured in the probe transcript, not on the illustrative shape below — adjust field names and the parsing loop to match exactly what Task 1 captured:

```go
package app

import (
	"context"
	"encoding/json"
	"fmt"
	osExec "os/exec"

	"github.com/lkshrk/omni/internal/config"
)

type claudeCodePluginAdapter struct {
	exec      func(context.Context, string, ...string) (string, string, error)
	lookupEnv func(string) (string, bool)
}

// NewClaudeCodePluginAdapter returns a PluginAdapter that delegates to the claude CLI.
func NewClaudeCodePluginAdapter(
	execFn func(context.Context, string, ...string) (string, string, error),
	lookupEnv func(string) (string, bool),
) PluginAdapter {
	return &claudeCodePluginAdapter{exec: execFn, lookupEnv: lookupEnv}
}

func (a *claudeCodePluginAdapter) ID() string { return "claude-code" }

func (a *claudeCodePluginAdapter) Available() bool {
	_, err := osExec.LookPath("claude")
	return err == nil
}

func (a *claudeCodePluginAdapter) InstallPlugin(ctx context.Context, p config.Plugin) error {
	_, stderr, err := a.exec(ctx, "claude", "plugins", "install", p.Name+"@"+p.Marketplace)
	if err != nil {
		return fmt.Errorf("claude plugins install %s@%s: %w: %s", p.Name, p.Marketplace, err, stderr)
	}
	return nil
}

func (a *claudeCodePluginAdapter) RemovePlugin(ctx context.Context, name string) error {
	_, stderr, err := a.exec(ctx, "claude", "plugins", "uninstall", name)
	if err != nil {
		return fmt.Errorf("claude plugins uninstall %s: %w: %s", name, err, stderr)
	}
	return nil
}

func (a *claudeCodePluginAdapter) AddMarketplace(ctx context.Context, m config.Marketplace) error {
	_, stderr, err := a.exec(ctx, "claude", "plugins", "marketplace", "add", m.Source)
	if err != nil {
		return fmt.Errorf("claude plugins marketplace add %s: %w: %s", m.Source, err, stderr)
	}
	return nil
}

// claudePluginListEntry mirrors one element of `claude plugins list --json`,
// per the fields captured in docs/superpowers/research/2026-07-02-plugin-cli-probe.md.
type claudePluginListEntry struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Scope   string `json:"scope"`
	Enabled bool   `json:"enabled"`
}

func (a *claudeCodePluginAdapter) ListPlugins(ctx context.Context) ([]InstalledPlugin, error) {
	stdout, stderr, err := a.exec(ctx, "claude", "plugins", "list", "--json")
	if err != nil {
		return nil, fmt.Errorf("claude plugins list: %w: %s", err, stderr)
	}
	var entries []claudePluginListEntry
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		return nil, fmt.Errorf("claude plugins list: parse json: %w", err)
	}
	plugins := make([]InstalledPlugin, 0, len(entries))
	for _, e := range entries {
		name, marketplace := splitPluginIdentity(e.ID)
		plugins = append(plugins, InstalledPlugin{Name: name, Marketplace: marketplace, Version: e.Version})
	}
	return plugins, nil
}

func (a *claudeCodePluginAdapter) ListMarketplaces(ctx context.Context) ([]string, error) {
	stdout, stderr, err := a.exec(ctx, "claude", "plugins", "marketplace", "list")
	if err != nil {
		return nil, fmt.Errorf("claude plugins marketplace list: %w: %s", err, stderr)
	}
	return parseClaudeMarketplaceList(stdout), nil
}
```

Add the two small helpers (`splitPluginIdentity`, `parseClaudeMarketplaceList`) in the same file, built from the literal probe output — e.g. if plugin identity is `name@marketplace` as the spec assumed, `splitPluginIdentity` splits on the last `@`; if `claude plugins marketplace list` prints one name per line, `parseClaudeMarketplaceList` trims and splits on newlines skipping blanks (mirror `parseClaudeMcpList`'s trim-and-skip-blank-lines style in `internal/app/mcp_claude_adapter.go`). Write these to match the transcript exactly, not this description.

### Step 6: Fill in the real fixture and exact assertions

Replace `claudePluginListFixture` with the literal `claude plugins list --json` output from the probe transcript (both the empty-list case and the one-plugin case — add a second const/test for the non-empty case), and replace the `_ = plugins // exact assertions...` placeholder with real assertions on `Name`, `Marketplace`, `Version` matching that fixture, following the assertion style of `TestClaudeCodeAdapter_Add_Stdio` in `internal/app/mcp_claude_adapter_test.go` (exact slice/field equality, no substring matching).

### Step 7: Run the tests and confirm they pass

Run: `go test ./internal/app/... -run TestClaudeCodePluginAdapter -v`
Expected: PASS

### Step 8: Commit

```bash
git add internal/app/plugin_adapter.go internal/app/plugin_claude_adapter.go internal/app/plugin_claude_adapter_test.go
git commit -m "feat(app): PluginAdapter interface and claude-code plugin adapter"
```

---

## Task 3: Codex Plugin Adapter

**Files:**
- Create: `internal/app/plugin_codex_adapter.go`
- Test: `internal/app/plugin_codex_adapter_test.go`

**Interfaces:**
- Consumes: `PluginAdapter`, `InstalledPlugin` (Task 2); probe transcript (Task 1).
- Produces (used by Task 4): `func NewCodexPluginAdapter(execFn func(context.Context, string, ...string) (string, string, error), lookupEnv func(string) (string, bool)) PluginAdapter`

### Step 1: Read the probe transcript for codex

Open `docs/superpowers/research/2026-07-02-plugin-cli-probe.md` and locate every `codex plugin ...` section. Confirm: exact subcommand names (`add`/`remove` vs `install`/`uninstall`), whether `codex plugin list` supports `--json` (spec's ground-truth section is unsure — the probe is the source of truth), and the exact marketplace subcommand names (`add`/`list`/`upgrade`/`remove` per spec, confirm literally).

### Step 2: Write the failing codex adapter tests

Create `internal/app/plugin_codex_adapter_test.go`, mirroring `internal/app/mcp_codex_adapter_test.go`'s structure:

```go
package app

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestCodexPluginAdapter_ID(t *testing.T) {
	a := NewCodexPluginAdapter(nil, nil)
	if a.ID() != "codex" {
		t.Fatalf("got %q", a.ID())
	}
}

func TestCodexPluginAdapter_InstallPlugin(t *testing.T) {
	var gotArgs []string
	exec := func(_ context.Context, cmd string, args ...string) (string, string, error) {
		gotArgs = args
		return "", "", nil
	}
	a := NewCodexPluginAdapter(exec, func(string) (string, bool) { return "", false })
	p := config.Plugin{Name: "caveman", Marketplace: "caveman"}
	if err := a.InstallPlugin(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if len(gotArgs) < 2 || gotArgs[0] != "plugin" || gotArgs[1] != "add" {
		t.Fatalf("unexpected start: %v", gotArgs)
	}
}

func TestCodexPluginAdapter_RemovePlugin(t *testing.T) {
	var gotArgs []string
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		gotArgs = args
		return "", "", nil
	}
	a := NewCodexPluginAdapter(exec, func(string) (string, bool) { return "", false })
	if err := a.RemovePlugin(context.Background(), "caveman"); err != nil {
		t.Fatal(err)
	}
	want := []string{"plugin", "remove", "caveman"}
	if !mcpSliceEq(gotArgs, want) {
		t.Fatalf("args mismatch\ngot:  %v\nwant: %v", gotArgs, want)
	}
}

func TestCodexPluginAdapter_AddMarketplace(t *testing.T) {
	var gotArgs []string
	exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
		gotArgs = args
		return "", "", nil
	}
	a := NewCodexPluginAdapter(exec, func(string) (string, bool) { return "", false })
	m := config.Marketplace{Name: "caveman", Source: "lkshrk/agent-marketplace"}
	if err := a.AddMarketplace(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	want := []string{"plugin", "marketplace", "add", "lkshrk/agent-marketplace"}
	if !mcpSliceEq(gotArgs, want) {
		t.Fatalf("args mismatch\ngot:  %v\nwant: %v", gotArgs, want)
	}
}

// codexPluginListFixture is the exact stdout of `codex plugin list --json`
// (or plain `codex plugin list` if no --json flag exists), captured in
// docs/superpowers/research/2026-07-02-plugin-cli-probe.md.
// <<< paste the exact output captured in the probe transcript >>>
const codexPluginListFixture = `[]`

func TestCodexPluginAdapter_ListPlugins_ParsesRealFixture(t *testing.T) {
	exec := func(_ context.Context, _ string, _ ...string) (string, string, error) {
		return codexPluginListFixture, "", nil
	}
	a := NewCodexPluginAdapter(exec, nil)
	plugins, err := a.ListPlugins(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 0 {
		t.Fatalf("expected empty fixture to parse to zero plugins, got %v", plugins)
	}
}
```

### Step 3: Run the tests to confirm they fail

Run: `go test ./internal/app/... -run TestCodexPluginAdapter -v`
Expected: FAIL with `NewCodexPluginAdapter` undefined.

### Step 4: Implement the codex adapter

Create `internal/app/plugin_codex_adapter.go`, structuring `InstallPlugin`/`RemovePlugin`/`AddMarketplace` after `internal/app/mcp_codex_adapter.go`'s `Add`/`Remove` shape, and `ListPlugins`/`ListMarketplaces` after `parseCodexMcpList`'s JSON-unmarshal-into-a-typed-entry-slice pattern — but using the exact field names captured in the probe transcript (do not assume `codex mcp list --json`'s `transport`-nested shape carries over; plugins have no transport concept):

```go
package app

import (
	"context"
	"encoding/json"
	"fmt"
	osExec "os/exec"

	"github.com/lkshrk/omni/internal/config"
)

type codexPluginAdapter struct {
	exec      func(context.Context, string, ...string) (string, string, error)
	lookupEnv func(string) (string, bool)
}

// NewCodexPluginAdapter returns a PluginAdapter that delegates to the codex CLI.
func NewCodexPluginAdapter(
	execFn func(context.Context, string, ...string) (string, string, error),
	lookupEnv func(string) (string, bool),
) PluginAdapter {
	return &codexPluginAdapter{exec: execFn, lookupEnv: lookupEnv}
}

func (a *codexPluginAdapter) ID() string { return "codex" }

func (a *codexPluginAdapter) Available() bool {
	_, err := osExec.LookPath("codex")
	return err == nil
}

func (a *codexPluginAdapter) InstallPlugin(ctx context.Context, p config.Plugin) error {
	_, stderr, err := a.exec(ctx, "codex", "plugin", "add", p.Name)
	if err != nil {
		return fmt.Errorf("codex plugin add %s: %w: %s", p.Name, err, stderr)
	}
	return nil
}

func (a *codexPluginAdapter) RemovePlugin(ctx context.Context, name string) error {
	_, stderr, err := a.exec(ctx, "codex", "plugin", "remove", name)
	if err != nil {
		return fmt.Errorf("codex plugin remove %s: %w: %s", name, err, stderr)
	}
	return nil
}

func (a *codexPluginAdapter) AddMarketplace(ctx context.Context, m config.Marketplace) error {
	_, stderr, err := a.exec(ctx, "codex", "plugin", "marketplace", "add", m.Source)
	if err != nil {
		return fmt.Errorf("codex plugin marketplace add %s: %w: %s", m.Source, err, stderr)
	}
	return nil
}

type codexPluginListEntry struct {
	Name        string `json:"name"`
	Marketplace string `json:"marketplace"`
	Version     string `json:"version"`
}

func (a *codexPluginAdapter) ListPlugins(ctx context.Context) ([]InstalledPlugin, error) {
	stdout, stderr, err := a.exec(ctx, "codex", "plugin", "list", "--json")
	if err != nil {
		return nil, fmt.Errorf("codex plugin list: %w: %s", err, stderr)
	}
	var entries []codexPluginListEntry
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		return nil, fmt.Errorf("codex plugin list: parse json: %w", err)
	}
	plugins := make([]InstalledPlugin, 0, len(entries))
	for _, e := range entries {
		plugins = append(plugins, InstalledPlugin{Name: e.Name, Marketplace: e.Marketplace, Version: e.Version})
	}
	return plugins, nil
}

func (a *codexPluginAdapter) ListMarketplaces(ctx context.Context) ([]string, error) {
	stdout, stderr, err := a.exec(ctx, "codex", "plugin", "marketplace", "list")
	if err != nil {
		return nil, fmt.Errorf("codex plugin marketplace list: %w: %s", err, stderr)
	}
	return parseClaudeMarketplaceList(stdout), nil // reuse: both CLIs print one name per line per probe transcript
}
```

If the probe transcript shows codex has no `--json` flag on `plugin list` (the spec flags this as unverified), replace the JSON unmarshal above with a line-oriented parser mirroring `parseClaudeMcpList`'s regex style in `internal/app/mcp_claude_adapter.go`, built from the literal plain-text output captured in Step 1 — and drop the `--json` flag from the exec call.

### Step 5: Fill in the real fixture

Replace `codexPluginListFixture` with the literal probe output (empty-list and one-plugin cases), add exact-field assertions for the non-empty case following `TestCodexAdapter_Add_Stdio`'s assertion style in `internal/app/mcp_codex_adapter_test.go`.

### Step 6: Run the tests and confirm they pass

Run: `go test ./internal/app/... -run TestCodexPluginAdapter -v`
Expected: PASS

### Step 7: Run the full app package test suite

Run: `go test ./internal/app/... -v`
Expected: PASS, no regressions from Tasks 2/3.

### Step 8: Commit

```bash
git add internal/app/plugin_codex_adapter.go internal/app/plugin_codex_adapter_test.go
git commit -m "feat(app): codex plugin adapter"
```

---

## Task 4: Plugin Ops (restore/add/remove/retarget/import/status) + Rows

**Files:**
- Create: `internal/app/plugin_ops.go` (mirrors `internal/app/mcp_ops.go`)
- Create: `internal/app/plugin_rows.go` (mirrors `internal/app/mcp_rows.go`)
- Test: `internal/app/plugin_ops_test.go`
- Test: `internal/app/plugin_ops_internal_test.go` (package `app`, for `resolvePlugins` group-filter unit tests, mirroring `mcp_ops_internal_test.go`)
- Test: `internal/app/plugin_rows_test.go`

**Interfaces:**
- Consumes: `PluginAdapter`, `InstalledPlugin` (Task 2/3); `config.Plugin`, `config.Marketplace`, `GroupConfig.Plugins` (Task 1); `App.loadConfig`, `App.withConfig`, `App.requireAgentsEnabled`, `currentMachineGroupName()` (existing `internal/app/app.go`).
- Produces (used by Tasks 5, 6, 7):
  - `type RestorePluginOptions struct { DryRun bool }`
  - `type RestorePluginResult struct { Installed, Skipped, WouldInstall, Warnings []string; Errors []PluginError }`
  - `type PluginError struct { AgentID, Name string; Err error }` with `func (e PluginError) Error() string`
  - `type AddPluginResult struct { Errors []PluginError }`, `type RemovePluginResult struct { Errors []PluginError }`
  - `type PluginImportDiff struct { Unmanaged map[string][]InstalledPlugin }`
  - `func (a *App) RestorePlugins(ctx context.Context, opts RestorePluginOptions) (RestorePluginResult, error)`
  - `func (a *App) AddPlugin(ctx context.Context, p config.Plugin) (AddPluginResult, error)`
  - `func (a *App) AddMarketplace(ctx context.Context, m config.Marketplace) (AddPluginResult, error)`
  - `func (a *App) RemovePlugin(ctx context.Context, name string) (RemovePluginResult, error)`
  - `func (a *App) RemoveMarketplace(name string) error` (manifest-only deletion — no adapter calls, per the "never remove a marketplace from an agent" rule)
  - `func (a *App) SetPluginAgents(ctx context.Context, name string, agents []string) (AddPluginResult, error)`
  - `func (a *App) ImportPlugins(ctx context.Context) (PluginImportDiff, error)`
  - `type PluginStatus string` with `PluginStatusInstalled/Missing/Unmanaged/AgentUnavailable`
  - `type PluginRow struct { Name, Marketplace string; Groups, Agents []string; PerAgentStatus map[string]PluginStatus }`
  - `func (a *App) PluginRows(ctx context.Context) (managed []PluginRow, unmanaged map[string][]InstalledPlugin, err error)`
  - `func (a *App) Marketplaces() ([]config.Marketplace, error)` (read-only accessor for `agents plugins marketplace list`)
  - `func WithPluginAdapters(adapters []PluginAdapter) func(*App)` (test hook, mirrors `WithMcpAdapters`)
  - `App.testPluginAdapters []PluginAdapter` field on `internal/app/app.go`'s `App` struct (add next to `testMcpAdapters` at line 52) and `func (a *App) pluginAdapters() []PluginAdapter` (mirrors `mcpAdapters()`)

### Step 1: Add the test-adapter field and accessor to `App`

In `internal/app/app.go`, add next to `testMcpAdapters McpAdapter` (line 52):

```go
	testPluginAdapters []PluginAdapter
```

This alone doesn't compile without `PluginAdapter` existing (Task 2 already added it), so it should build cleanly now.

### Step 2: Write failing restore/add/remove/retarget/import tests

Create `internal/app/plugin_ops_test.go`, following `internal/app/mcp_ops_test.go`'s harness exactly (package `app_test`, `stubPluginAdapter`, `newPluginTestApp`):

```go
package app_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

type stubPluginAdapter struct {
	id              string
	available       bool
	installErr      error
	removeErr       error
	addMarketErr    error
	listedPlugins   []app.InstalledPlugin
	listedMarkets   []string
	installedPlugin []config.Plugin
	removedNames    []string
	addedMarkets    []config.Marketplace
}

func (s *stubPluginAdapter) ID() string      { return s.id }
func (s *stubPluginAdapter) Available() bool { return s.available }
func (s *stubPluginAdapter) ListPlugins(_ context.Context) ([]app.InstalledPlugin, error) {
	return append([]app.InstalledPlugin(nil), s.listedPlugins...), nil
}
func (s *stubPluginAdapter) InstallPlugin(_ context.Context, p config.Plugin) error {
	s.installedPlugin = append(s.installedPlugin, p)
	return s.installErr
}
func (s *stubPluginAdapter) RemovePlugin(_ context.Context, name string) error {
	s.removedNames = append(s.removedNames, name)
	return s.removeErr
}
func (s *stubPluginAdapter) ListMarketplaces(_ context.Context) ([]string, error) {
	return append([]string(nil), s.listedMarkets...), nil
}
func (s *stubPluginAdapter) AddMarketplace(_ context.Context, m config.Marketplace) error {
	s.addedMarkets = append(s.addedMarkets, m)
	return s.addMarketErr
}

func newPluginTestApp(t *testing.T, agents config.AgentsConfig, opts ...func(*app.App)) *app.App {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	root := config.RootConfig{Version: config.CurrentVersion, Agents: agents}
	if err := config.Save(cfgPath, &root); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath, opts...)
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func loadPluginTestConfig(t *testing.T, a *app.App) *config.RootConfig {
	t.Helper()
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestRestorePlugins_AddsMarketplaceBeforePlugin(t *testing.T) {
	stub := &stubPluginAdapter{id: "claude-code", available: true}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "lkshrk/agent-marketplace", Agents: []string{"claude-code"}}},
		Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman", Agents: []string{"claude-code"}}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	res, err := a.RestorePlugins(context.Background(), app.RestorePluginOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.addedMarkets) != 1 || stub.addedMarkets[0].Name != "caveman" {
		t.Fatalf("expected marketplace to be added before plugin, got %v", stub.addedMarkets)
	}
	if len(stub.installedPlugin) != 1 || stub.installedPlugin[0].Name != "caveman" {
		t.Fatalf("expected plugin install, got %v", stub.installedPlugin)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}
}

func TestRestorePlugins_SkipsNonTargetedAgent(t *testing.T) {
	stub := &stubPluginAdapter{id: "codex", available: true}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
		Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman", Agents: []string{"claude-code"}}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	if _, err := a.RestorePlugins(context.Background(), app.RestorePluginOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(stub.installedPlugin) != 0 {
		t.Fatal("codex should be skipped when not in plugin's Agents list")
	}
}

func TestRestorePlugins_DryRun_ReportsWouldInstall(t *testing.T) {
	stub := &stubPluginAdapter{id: "claude-code", available: true}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
		Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman"}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	res, err := a.RestorePlugins(context.Background(), app.RestorePluginOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.installedPlugin) != 0 || len(stub.addedMarkets) != 0 {
		t.Fatal("dry run must not call the adapter")
	}
	if len(res.WouldInstall) != 1 || res.WouldInstall[0] != "claude-code/caveman" {
		t.Fatalf("expected would-install entry, got %v", res.WouldInstall)
	}
}

func TestRestorePlugins_PerPluginErrorIsNonFatal(t *testing.T) {
	stub := &stubPluginAdapter{id: "claude-code", available: true, installErr: errBoom}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
		Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman"}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	res, err := a.RestorePlugins(context.Background(), app.RestorePluginOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("expected 1 collected error, got %v", res.Errors)
	}
}

func TestAddPlugin_PersistsToManifestRegardlessOfAdapterOutcome(t *testing.T) {
	stub := &stubPluginAdapter{id: "claude-code", available: true, installErr: errBoom}
	agents := config.AgentsConfig{Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}}}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	res, err := a.AddPlugin(context.Background(), config.Plugin{Name: "caveman", Marketplace: "caveman", Agents: []string{"claude-code"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("expected adapter error surfaced, got %v", res.Errors)
	}
	cfg := loadPluginTestConfig(t, a)
	if len(cfg.Agents.Plugins) != 1 || cfg.Agents.Plugins[0].Name != "caveman" {
		t.Fatalf("manifest write must persist despite adapter failure, got %+v", cfg.Agents.Plugins)
	}
}

func TestAddPlugin_RejectsUnknownMarketplace(t *testing.T) {
	a := newPluginTestApp(t, config.AgentsConfig{}, app.WithPluginAdapters(nil))
	if _, err := a.AddPlugin(context.Background(), config.Plugin{Name: "caveman", Marketplace: "ghost"}); err == nil {
		t.Fatal("expected error for unknown marketplace ref")
	}
}

func TestRemovePlugin_RemovesFromAdapterAndManifestButKeepsMarketplace(t *testing.T) {
	stub := &stubPluginAdapter{id: "claude-code", available: true}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
		Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman", Agents: []string{"claude-code"}}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	if _, err := a.RemovePlugin(context.Background(), "caveman"); err != nil {
		t.Fatal(err)
	}
	if len(stub.removedNames) != 1 {
		t.Fatalf("expected adapter RemovePlugin call, got %v", stub.removedNames)
	}
	cfg := loadPluginTestConfig(t, a)
	if len(cfg.Agents.Plugins) != 0 {
		t.Fatal("expected plugin removed from manifest")
	}
	if len(cfg.Agents.Marketplaces) != 1 {
		t.Fatal("marketplace must remain in manifest after plugin removal")
	}
}

func TestRemovePlugin_RejectsUnmanaged(t *testing.T) {
	a := newPluginTestApp(t, config.AgentsConfig{}, app.WithPluginAdapters(nil))
	if _, err := a.RemovePlugin(context.Background(), "ghost"); err == nil {
		t.Fatal("expected error removing unmanaged plugin")
	}
}

func TestRemoveMarketplace_DeletesManifestEntryOnly(t *testing.T) {
	stub := &stubPluginAdapter{id: "claude-code", available: true}
	agents := config.AgentsConfig{Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}}}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	if err := a.RemoveMarketplace("caveman"); err != nil {
		t.Fatal(err)
	}
	if len(stub.removedNames) != 0 {
		t.Fatal("marketplace removal must never call any adapter")
	}
	cfg := loadPluginTestConfig(t, a)
	if len(cfg.Agents.Marketplaces) != 0 {
		t.Fatal("expected marketplace removed from manifest")
	}
}

func TestSetPluginAgents_WideningInstallsOnNewlySelectedAdapter(t *testing.T) {
	claude := &stubPluginAdapter{id: "claude-code", available: true}
	codex := &stubPluginAdapter{id: "codex", available: true}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
		Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman", Agents: []string{"claude-code"}}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{claude, codex}))
	if _, err := a.SetPluginAgents(context.Background(), "caveman", []string{"claude-code", "codex"}); err != nil {
		t.Fatal(err)
	}
	if len(codex.installedPlugin) != 1 {
		t.Fatalf("expected codex to install newly-selected plugin, got %v", codex.installedPlugin)
	}
	if len(claude.installedPlugin) != 0 {
		t.Fatal("claude-code was already targeted; must not reinstall")
	}
}

func TestImportPlugins_ReturnsUnmanagedAndKnownMarketplace(t *testing.T) {
	stub := &stubPluginAdapter{
		id:            "claude-code",
		available:     true,
		listedPlugins: []app.InstalledPlugin{{Name: "hand-added", Marketplace: "caveman", Version: "1.0.0"}},
		listedMarkets: []string{"caveman"},
	}
	agents := config.AgentsConfig{Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}}}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{stub}))
	diff, err := a.ImportPlugins(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Unmanaged["claude-code"]) != 1 || diff.Unmanaged["claude-code"][0].Name != "hand-added" {
		t.Fatalf("expected hand-added to be unmanaged, got %v", diff.Unmanaged)
	}
}

var errBoom = fmtErrorf("boom")

func fmtErrorf(s string) error { return &boomErr{s} }

type boomErr struct{ s string }

func (e *boomErr) Error() string { return e.s }
```

### Step 3: Write the failing internal group-filter test

Create `internal/app/plugin_ops_internal_test.go` (package `app`, mirroring `mcp_ops_internal_test.go`):

```go
package app

import (
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func resolvePluginNames(cfg *config.RootConfig, group string) []string {
	plugins := resolvePlugins(cfg, group)
	names := make([]string, len(plugins))
	for i, p := range plugins {
		names[i] = p.Name
	}
	return names
}

func TestResolvePlugins_UngroupedPlugin_AppearsOnAllHosts(t *testing.T) {
	cfg := &config.RootConfig{
		Agents: config.AgentsConfig{Plugins: []config.Plugin{{Name: "global", Marketplace: "m"}}},
	}
	for _, group := range []string{"box", "work", ""} {
		got := resolvePluginNames(cfg, group)
		if len(got) != 1 || got[0] != "global" {
			t.Errorf("group=%q: ungrouped plugin must appear; got %v", group, got)
		}
	}
}

func TestResolvePlugins_GroupedPlugin_OnlyOnMatchingGroup(t *testing.T) {
	cfg := &config.RootConfig{
		Agents: config.AgentsConfig{Plugins: []config.Plugin{{Name: "work-only", Marketplace: "m"}}},
		Groups: []*config.GroupConfig{{Name: "work", Plugins: []string{"work-only"}}},
	}
	if got := resolvePluginNames(cfg, "work"); len(got) != 1 || got[0] != "work-only" {
		t.Fatalf("expected work-only on matching group, got %v", got)
	}
	if got := resolvePluginNames(cfg, "personal"); len(got) != 0 {
		t.Fatalf("expected work-only excluded on non-matching group, got %v", got)
	}
}

func TestPluginsNeededMarketplaces_DedupesAcrossPlugins(t *testing.T) {
	plugins := []config.Plugin{
		{Name: "a", Marketplace: "shared"},
		{Name: "b", Marketplace: "shared"},
		{Name: "c", Marketplace: "other"},
	}
	got := pluginsNeededMarketplaces(plugins)
	if len(got) != 2 {
		t.Fatalf("expected 2 deduped marketplace names, got %v", got)
	}
}
```

### Step 4: Write the failing rows test

Create `internal/app/plugin_rows_test.go`, mirroring `internal/app/mcp_rows_test.go`:

```go
package app_test

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func TestPluginRows_ManagedRowReportsPerAgentStatus(t *testing.T) {
	claude := &stubPluginAdapter{id: "claude-code", available: true}
	codex := &stubPluginAdapter{id: "codex", available: false}
	agents := config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}},
		Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman"}},
	}
	a := newPluginTestApp(t, agents, app.WithPluginAdapters([]app.PluginAdapter{claude, codex}))
	rows, unmanaged, err := a.PluginRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "caveman" || rows[0].Marketplace != "caveman" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
	if rows[0].PerAgentStatus["claude-code"] != app.PluginStatusMissing {
		t.Fatalf("expected missing (not installed on claude), got %v", rows[0].PerAgentStatus)
	}
	if rows[0].PerAgentStatus["codex"] != app.PluginStatusAgentUnavailable {
		t.Fatalf("expected agent-unavailable for codex, got %v", rows[0].PerAgentStatus)
	}
	if len(unmanaged) != 0 {
		t.Fatalf("expected no unmanaged entries, got %v", unmanaged)
	}
}
```

### Step 5: Run all new tests to confirm they fail

Run: `go test ./internal/app/... -run 'TestRestorePlugins|TestAddPlugin|TestRemovePlugin|TestRemoveMarketplace|TestSetPluginAgents|TestImportPlugins|TestResolvePlugins|TestPluginsNeededMarketplaces|TestPluginRows' -v`
Expected: FAIL — compile errors (`app.RestorePlugins` etc. undefined).

### Step 6: Implement `internal/app/plugin_ops.go`

```go
package app

import (
	"context"
	"fmt"
	"os"

	"github.com/lkshrk/omni/internal/config"
)

type RestorePluginOptions struct {
	DryRun bool
}

type RestorePluginResult struct {
	Installed    []string
	Skipped      []string
	WouldInstall []string
	Warnings     []string
	Errors       []PluginError
}

type PluginError struct {
	AgentID string
	Name    string
	Err     error
}

func (e PluginError) Error() string {
	return fmt.Sprintf("agent %s / plugin %s: %v", e.AgentID, e.Name, e.Err)
}

type PluginImportDiff struct {
	Unmanaged map[string][]InstalledPlugin
}

func WithPluginAdapters(adapters []PluginAdapter) func(*App) {
	return func(a *App) { a.testPluginAdapters = adapters }
}

func (a *App) pluginAdapters() []PluginAdapter {
	if a.testPluginAdapters != nil {
		return a.testPluginAdapters
	}
	exec := a.fallbackExecutor().Run
	return []PluginAdapter{
		NewClaudeCodePluginAdapter(exec, os.LookupEnv),
		NewCodexPluginAdapter(exec, os.LookupEnv),
	}
}

func pluginTargetsAdapter(p config.Plugin, adapterID string) bool {
	if len(p.Agents) == 0 {
		return true
	}
	for _, id := range p.Agents {
		if id == adapterID {
			return true
		}
	}
	return false
}

// resolvePlugins returns the plugins active for groupName, mirroring
// resolveMcpServers: ungrouped plugins restore everywhere; group-listed
// plugins restore only when that group is active.
func resolvePlugins(cfg *config.RootConfig, groupName string) []config.Plugin {
	groupedNames := make(map[string]struct{})
	activeNames := make(map[string]struct{})
	for _, g := range cfg.Groups {
		if g == nil {
			continue
		}
		for _, name := range g.Plugins {
			groupedNames[name] = struct{}{}
			if g.Name == groupName {
				activeNames[name] = struct{}{}
			}
		}
	}
	var out []config.Plugin
	for _, p := range cfg.Agents.Plugins {
		if _, grouped := groupedNames[p.Name]; !grouped {
			out = append(out, p)
			continue
		}
		if _, active := activeNames[p.Name]; active {
			out = append(out, p)
		}
	}
	return out
}

// pluginsNeededMarketplaces returns the deduped, order-stable set of
// marketplace names required by plugins.
func pluginsNeededMarketplaces(plugins []config.Plugin) []string {
	seen := make(map[string]struct{}, len(plugins))
	var out []string
	for _, p := range plugins {
		if _, ok := seen[p.Marketplace]; ok {
			continue
		}
		seen[p.Marketplace] = struct{}{}
		out = append(out, p.Marketplace)
	}
	return out
}

func findMarketplace(marketplaces []config.Marketplace, name string) *config.Marketplace {
	for i := range marketplaces {
		if marketplaces[i].Name == name {
			cp := marketplaces[i]
			return &cp
		}
	}
	return nil
}

// ensureMarketplace adds m to adapter if adapter does not already report it
// present. Errors are returned for the caller to collect, never fatal to the
// batch.
func ensureMarketplace(ctx context.Context, adapter PluginAdapter, m config.Marketplace) error {
	existing, err := adapter.ListMarketplaces(ctx)
	if err != nil {
		return err
	}
	for _, name := range existing {
		if name == m.Name {
			return nil
		}
	}
	return adapter.AddMarketplace(ctx, m)
}

// RestorePlugins installs manifest plugins into each targeted agent CLI,
// adding any marketplace a plugin needs before installing the plugin itself.
func (a *App) RestorePlugins(ctx context.Context, opts RestorePluginOptions) (RestorePluginResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return RestorePluginResult{}, err
	}
	if err := a.requireAgentsEnabled(cfg); err != nil {
		return RestorePluginResult{}, err
	}
	plugins := resolvePlugins(cfg, currentMachineGroupName())
	var res RestorePluginResult
	for _, adapter := range a.pluginAdapters() {
		if !adapter.Available() {
			res.Warnings = append(res.Warnings, fmt.Sprintf("agent %s not available, skipping", adapter.ID()))
			continue
		}
		addedMarketplace := make(map[string]struct{})
		for _, p := range plugins {
			if !pluginTargetsAdapter(p, adapter.ID()) {
				res.Skipped = append(res.Skipped, adapter.ID()+"/"+p.Name)
				continue
			}
			if opts.DryRun {
				res.WouldInstall = append(res.WouldInstall, adapter.ID()+"/"+p.Name)
				continue
			}
			if _, done := addedMarketplace[p.Marketplace]; !done {
				if m := findMarketplace(cfg.Agents.Marketplaces, p.Marketplace); m != nil {
					if mErr := ensureMarketplace(ctx, adapter, *m); mErr != nil {
						res.Errors = append(res.Errors, PluginError{AgentID: adapter.ID(), Name: p.Name, Err: fmt.Errorf("marketplace %s: %w", m.Name, mErr)})
						continue
					}
				}
				addedMarketplace[p.Marketplace] = struct{}{}
			}
			if installErr := adapter.InstallPlugin(ctx, p); installErr != nil {
				res.Errors = append(res.Errors, PluginError{AgentID: adapter.ID(), Name: p.Name, Err: installErr})
				continue
			}
			res.Installed = append(res.Installed, adapter.ID()+"/"+p.Name)
		}
	}
	return res, nil
}

type AddPluginResult struct {
	Errors []PluginError
}

type RemovePluginResult struct {
	Errors []PluginError
}

// AddPlugin validates the marketplace ref, upserts the manifest, then
// installs on each target adapter. Manifest write is unconditional on
// adapter outcome (manifest = intent), mirroring AddMcpServer.
func (a *App) AddPlugin(ctx context.Context, p config.Plugin) (AddPluginResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return AddPluginResult{}, err
	}
	if err := a.requireAgentsEnabled(cfg); err != nil {
		return AddPluginResult{}, err
	}
	m := findMarketplace(cfg.Agents.Marketplaces, p.Marketplace)
	if m == nil {
		return AddPluginResult{}, fmt.Errorf("plugin %q references unknown marketplace %q; declare it first", p.Name, p.Marketplace)
	}
	var res AddPluginResult
	for _, adapter := range a.pluginAdapters() {
		if !pluginTargetsAdapter(p, adapter.ID()) || !adapter.Available() {
			continue
		}
		if mErr := ensureMarketplace(ctx, adapter, *m); mErr != nil {
			res.Errors = append(res.Errors, PluginError{AgentID: adapter.ID(), Name: p.Name, Err: fmt.Errorf("marketplace %s: %w", m.Name, mErr)})
			continue
		}
		if installErr := adapter.InstallPlugin(ctx, p); installErr != nil {
			res.Errors = append(res.Errors, PluginError{AgentID: adapter.ID(), Name: p.Name, Err: installErr})
		}
	}
	if err := a.withConfig(func(c *config.RootConfig) error {
		c.Agents.Plugins = upsertPlugin(c.Agents.Plugins, p)
		return nil
	}); err != nil {
		return res, fmt.Errorf("installed %s but failed to save to manifest (re-run to persist): %w", p.Name, err)
	}
	return res, nil
}

// AddMarketplace validates uniqueness, upserts the manifest, then adds the
// marketplace on each target adapter. Manifest write is unconditional.
func (a *App) AddMarketplace(ctx context.Context, m config.Marketplace) (AddPluginResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return AddPluginResult{}, err
	}
	if err := a.requireAgentsEnabled(cfg); err != nil {
		return AddPluginResult{}, err
	}
	var res AddPluginResult
	for _, adapter := range a.pluginAdapters() {
		targeted := len(m.Agents) == 0
		for _, id := range m.Agents {
			if id == adapter.ID() {
				targeted = true
			}
		}
		if !targeted || !adapter.Available() {
			continue
		}
		if mErr := ensureMarketplace(ctx, adapter, m); mErr != nil {
			res.Errors = append(res.Errors, PluginError{AgentID: adapter.ID(), Name: m.Name, Err: mErr})
		}
	}
	if err := a.withConfig(func(c *config.RootConfig) error {
		c.Agents.Marketplaces = upsertMarketplace(c.Agents.Marketplaces, m)
		return nil
	}); err != nil {
		return res, fmt.Errorf("added marketplace %s but failed to save to manifest (re-run to persist): %w", m.Name, err)
	}
	return res, nil
}

// RemovePlugin uninstalls from each target adapter (tolerant), then deletes
// the manifest entry. Marketplaces are never touched by this call.
func (a *App) RemovePlugin(ctx context.Context, name string) (RemovePluginResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return RemovePluginResult{}, err
	}
	if err := a.requireAgentsEnabled(cfg); err != nil {
		return RemovePluginResult{}, err
	}
	target := findPlugin(cfg.Agents.Plugins, name)
	if target == nil {
		return RemovePluginResult{}, fmt.Errorf("plugin %q not found in manifest; omni only removes plugins it added", name)
	}
	var res RemovePluginResult
	for _, adapter := range a.pluginAdapters() {
		if !pluginTargetsAdapter(*target, adapter.ID()) || !adapter.Available() {
			continue
		}
		if removeErr := adapter.RemovePlugin(ctx, name); removeErr != nil {
			res.Errors = append(res.Errors, PluginError{AgentID: adapter.ID(), Name: name, Err: removeErr})
		}
	}
	if err := a.withConfig(func(c *config.RootConfig) error {
		c.Agents.Plugins = deletePlugin(c.Agents.Plugins, name)
		return nil
	}); err != nil {
		return res, fmt.Errorf("removed %s from agents but failed to update manifest (re-run to persist): %w", name, err)
	}
	return res, nil
}

// RemoveMarketplace deletes only the manifest entry. omni never removes a
// marketplace from an agent CLI — it may still serve hand-installed plugins
// omni does not know about.
func (a *App) RemoveMarketplace(name string) error {
	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	if err := a.requireAgentsEnabled(cfg); err != nil {
		return err
	}
	if findMarketplace(cfg.Agents.Marketplaces, name) == nil {
		return fmt.Errorf("marketplace %q not found in manifest", name)
	}
	return a.withConfig(func(c *config.RootConfig) error {
		c.Agents.Marketplaces = deleteMarketplace(c.Agents.Marketplaces, name)
		return nil
	})
}

// SetPluginAgents re-targets an existing manifest plugin's Agents list,
// installing on newly-selected adapters and removing from deselected ones.
func (a *App) SetPluginAgents(ctx context.Context, name string, agents []string) (AddPluginResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return AddPluginResult{}, err
	}
	if err := a.requireAgentsEnabled(cfg); err != nil {
		return AddPluginResult{}, err
	}
	target := findPlugin(cfg.Agents.Plugins, name)
	if target == nil {
		return AddPluginResult{}, fmt.Errorf("plugin %q not found in manifest", name)
	}
	updated := *target
	updated.Agents = agents
	m := findMarketplace(cfg.Agents.Marketplaces, target.Marketplace)

	var res AddPluginResult
	for _, adapter := range a.pluginAdapters() {
		if !adapter.Available() {
			continue
		}
		wasTargeted := pluginTargetsAdapter(*target, adapter.ID())
		nowTargeted := pluginTargetsAdapter(updated, adapter.ID())
		switch {
		case nowTargeted && !wasTargeted:
			if m != nil {
				if mErr := ensureMarketplace(ctx, adapter, *m); mErr != nil {
					res.Errors = append(res.Errors, PluginError{AgentID: adapter.ID(), Name: name, Err: mErr})
					continue
				}
			}
			if installErr := adapter.InstallPlugin(ctx, updated); installErr != nil {
				res.Errors = append(res.Errors, PluginError{AgentID: adapter.ID(), Name: name, Err: installErr})
			}
		case wasTargeted && !nowTargeted:
			if removeErr := adapter.RemovePlugin(ctx, name); removeErr != nil {
				res.Errors = append(res.Errors, PluginError{AgentID: adapter.ID(), Name: name, Err: removeErr})
			}
		}
	}
	if err := a.withConfig(func(c *config.RootConfig) error {
		c.Agents.Plugins = upsertPlugin(c.Agents.Plugins, updated)
		return nil
	}); err != nil {
		return res, fmt.Errorf("updated agents for %s but failed to save to manifest (re-run to persist): %w", name, err)
	}
	return res, nil
}

func findPlugin(plugins []config.Plugin, name string) *config.Plugin {
	for i := range plugins {
		if plugins[i].Name == name {
			cp := plugins[i]
			return &cp
		}
	}
	return nil
}

// ImportPlugins returns plugins installed in agent CLIs that are not in the
// manifest. Callers adopt identity only (name + marketplace); see AddPlugin.
func (a *App) ImportPlugins(ctx context.Context) (PluginImportDiff, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return PluginImportDiff{}, err
	}
	if err := a.requireAgentsEnabled(cfg); err != nil {
		return PluginImportDiff{}, err
	}
	managed := make(map[string]struct{}, len(cfg.Agents.Plugins))
	for _, p := range cfg.Agents.Plugins {
		managed[p.Name] = struct{}{}
	}
	diff := PluginImportDiff{Unmanaged: make(map[string][]InstalledPlugin)}
	for _, adapter := range a.pluginAdapters() {
		if !adapter.Available() {
			continue
		}
		listed, listErr := adapter.ListPlugins(ctx)
		if listErr != nil {
			continue
		}
		for _, plg := range listed {
			if _, ok := managed[plg.Name]; !ok {
				diff.Unmanaged[adapter.ID()] = append(diff.Unmanaged[adapter.ID()], plg)
			}
		}
	}
	return diff, nil
}

// Marketplaces returns the declared manifest marketplaces, for read-only CLI
// display (mirrors how settings.go's newSettingsShowCmd reads via a small
// dedicated App method rather than exposing loadConfig itself).
func (a *App) Marketplaces() ([]config.Marketplace, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	return cfg.Agents.Marketplaces, nil
}

func upsertPlugin(plugins []config.Plugin, p config.Plugin) []config.Plugin {
	for i := range plugins {
		if plugins[i].Name == p.Name {
			plugins[i] = p
			return plugins
		}
	}
	return append(plugins, p)
}

func deletePlugin(plugins []config.Plugin, name string) []config.Plugin {
	out := plugins[:0]
	for _, p := range plugins {
		if p.Name != name {
			out = append(out, p)
		}
	}
	return out
}

func upsertMarketplace(marketplaces []config.Marketplace, m config.Marketplace) []config.Marketplace {
	for i := range marketplaces {
		if marketplaces[i].Name == m.Name {
			marketplaces[i] = m
			return marketplaces
		}
	}
	return append(marketplaces, m)
}

func deleteMarketplace(marketplaces []config.Marketplace, name string) []config.Marketplace {
	out := marketplaces[:0]
	for _, m := range marketplaces {
		if m.Name != name {
			out = append(out, m)
		}
	}
	return out
}
```

Note: `AddPlugin`'s marketplace-ref check duplicates a subset of `config.ValidateRoot`'s hard-error rule intentionally — `withConfig`/`Save` do not re-run full semantic validation before every write (matching existing `AddMcpServer`, which also performs no equivalent pre-check because MCP has no analogous hard-ref rule); this app-layer check is the enforcement point for plugins specifically.

### Step 7: Implement `internal/app/plugin_rows.go`

```go
package app

import (
	"context"
)

type PluginStatus string

const (
	PluginStatusInstalled        PluginStatus = "installed"
	PluginStatusMissing          PluginStatus = "missing"
	PluginStatusUnmanaged        PluginStatus = "unmanaged"
	PluginStatusAgentUnavailable PluginStatus = "agent-unavailable"
)

// PluginRow is a display row for the TUI plugin chip and CLI list.
type PluginRow struct {
	Name           string
	Marketplace    string
	Groups         []string
	Agents         []string
	PerAgentStatus map[string]PluginStatus
}

// PluginRows returns managed rows (from manifest) with per-adapter status,
// and unmanaged entries (from ListPlugins()) not present in the manifest,
// keyed by agent ID. Mirrors McpServerRows.
func (a *App) PluginRows(ctx context.Context) (managed []PluginRow, unmanaged map[string][]InstalledPlugin, err error) {
	cfg, loadErr := a.loadConfig()
	if loadErr != nil {
		return nil, nil, loadErr
	}
	installedByAgent := make(map[string]map[string]InstalledPlugin)
	unmanaged = make(map[string][]InstalledPlugin)
	for _, adapter := range a.pluginAdapters() {
		if !adapter.Available() {
			continue
		}
		listed, listErr := adapter.ListPlugins(ctx)
		if listErr != nil {
			continue
		}
		byName := make(map[string]InstalledPlugin, len(listed))
		for _, p := range listed {
			byName[p.Name] = p
		}
		installedByAgent[adapter.ID()] = byName
	}
	manifestNames := make(map[string]struct{}, len(cfg.Agents.Plugins))
	for _, p := range cfg.Agents.Plugins {
		manifestNames[p.Name] = struct{}{}
		row := PluginRow{
			Name:           p.Name,
			Marketplace:    p.Marketplace,
			Agents:         append([]string(nil), p.Agents...),
			PerAgentStatus: make(map[string]PluginStatus),
		}
		for _, adapter := range a.pluginAdapters() {
			if !adapter.Available() {
				row.PerAgentStatus[adapter.ID()] = PluginStatusAgentUnavailable
				continue
			}
			byName, ok := installedByAgent[adapter.ID()]
			if !ok {
				row.PerAgentStatus[adapter.ID()] = PluginStatusMissing
				continue
			}
			if _, found := byName[p.Name]; found {
				row.PerAgentStatus[adapter.ID()] = PluginStatusInstalled
			} else {
				row.PerAgentStatus[adapter.ID()] = PluginStatusMissing
			}
		}
		managed = append(managed, row)
	}
	for _, adapter := range a.pluginAdapters() {
		byName, ok := installedByAgent[adapter.ID()]
		if !ok {
			continue
		}
		for name, plg := range byName {
			if _, inManifest := manifestNames[name]; !inManifest {
				unmanaged[adapter.ID()] = append(unmanaged[adapter.ID()], plg)
			}
		}
	}
	return managed, unmanaged, nil
}
```

### Step 8: Fix the ad-hoc test error helper

Replace the `errBoom`/`fmtErrorf`/`boomErr` scaffolding in `plugin_ops_test.go` Step 2 with the standard library: add `"errors"` to the imports and change `var errBoom = fmtErrorf("boom")` (plus its two helper declarations) to a single line: `var errBoom = errors.New("boom")`. Delete the `fmtErrorf`/`boomErr` helper functions entirely.

### Step 9: Run all new tests and confirm they pass

Run: `go test ./internal/app/... -run 'TestRestorePlugins|TestAddPlugin|TestRemovePlugin|TestRemoveMarketplace|TestSetPluginAgents|TestImportPlugins|TestResolvePlugins|TestPluginsNeededMarketplaces|TestPluginRows' -v`
Expected: PASS

### Step 10: Run the full app package test suite

Run: `go test ./internal/app/... -v`
Expected: PASS, no regressions.

### Step 11: Commit

```bash
git add internal/app/app.go internal/app/plugin_ops.go internal/app/plugin_rows.go internal/app/plugin_ops_test.go internal/app/plugin_ops_internal_test.go internal/app/plugin_rows_test.go
git commit -m "feat(app): plugin/marketplace operations and rows"
```

---

## Task 5: CLI — `agents plugins …` + `agents plugins marketplace …`

**Files:**
- Modify: `internal/cli/agents.go` (add `newAgentsPluginsCmd` and its subcommands, register into `newAgentsCmd`)
- Modify: `internal/cli/action_catalog_test.go` (`uncatalogedRunnableCLICommandAllowed`: add the new command paths)
- Test: `internal/cli/agents_plugins_test.go` (behavioral, mirrors `agents_test.go`)

**Interfaces:**
- Consumes: `app.RestorePluginOptions/Result`, `app.AddPluginResult`, `app.RemovePluginResult`, `app.PluginError`, `app.PluginImportDiff`, `app.InstalledPlugin`, `app.PluginRow`, `app.PluginStatus*`, `app.PluginAdapter`, `app.WithPluginAdapters` (Task 4); `config.Plugin`, `config.Marketplace` (Task 1).
- Produces (used by Task 8 txtar fixtures):
  - `omni agents plugins list`
  - `omni agents plugins add --name <n> --marketplace <mkt> [--agents <id> ...]`
  - `omni agents plugins remove <name>`
  - `omni agents plugins restore [--dry-run]`
  - `omni agents plugins import [<name>]`
  - `omni agents plugins marketplace list`
  - `omni agents plugins marketplace add <name> --source <src> [--agents <id> ...]`
  - `omni agents plugins marketplace remove <name>`

### Step 1: Register the command tree and stub subcommands (compiles, no logic yet is not acceptable per TDD — write real subcommands directly per this pattern, tests come from Step 2 onward)

In `internal/cli/agents.go`, add `newAgentsPluginsCmd(state)` to `newAgentsCmd`'s `cmd.AddCommand(...)` call (alongside `newAgentsMcpCmd(state)`):

```go
func newAgentsCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "Manage AI-agent resources (skills)",
	}
	cmd.AddCommand(
		newAgentsSkillsCmd(state),
		newAgentsMcpCmd(state),
		newAgentsPluginsCmd(state),
		newAgentsAddCmd(state),
		newAgentsFindCmd(state),
	)
	return cmd
}
```

Add the command tree, following `newAgentsMcpCmd`'s exact shape:

```go
func newAgentsPluginsCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugins",
		Short: "Manage agent plugins and their marketplaces",
	}
	cmd.AddCommand(
		newAgentsPluginsListCmd(state),
		newAgentsPluginsAddCmd(state),
		newAgentsPluginsRemoveCmd(state),
		newAgentsPluginsRestoreCmd(state),
		newAgentsPluginsImportCmd(state),
		newAgentsPluginsMarketplaceCmd(state),
	)
	return cmd
}

func newAgentsPluginsListCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List managed and unmanaged plugins",
		RunE: func(cmd *cobra.Command, _ []string) error {
			managed, unmanaged, err := state.app.PluginRows(cmd.Context())
			if err != nil {
				return err
			}
			w := cmdOut(cmd)
			for _, row := range managed {
				agentIDs := make([]string, 0, len(row.PerAgentStatus))
				for id := range row.PerAgentStatus {
					agentIDs = append(agentIDs, id)
				}
				sort.Strings(agentIDs)
				agentParts := make([]string, 0, len(agentIDs))
				for _, id := range agentIDs {
					marker := "✓"
					if row.PerAgentStatus[id] != app.PluginStatusInstalled {
						marker = "-"
					}
					agentParts = append(agentParts, fmt.Sprintf("%s(%s)", id, marker))
				}
				line := row.Name + "  " + row.Marketplace
				if len(agentParts) > 0 {
					line += "  " + strings.Join(agentParts, " ")
				}
				fmt.Fprintln(w, line)
			}
			agentIDs := make([]string, 0, len(unmanaged))
			for id := range unmanaged {
				agentIDs = append(agentIDs, id)
			}
			sort.Strings(agentIDs)
			for _, id := range agentIDs {
				fmt.Fprintf(w, "\n-- unmanaged (%s) --\n", id)
				for _, p := range unmanaged[id] {
					fmt.Fprintf(w, "%s  %s\n", p.Name, p.Marketplace)
				}
			}
			return nil
		},
	}
}

func newAgentsPluginsAddCmd(state *rootState) *cobra.Command {
	var (
		name        string
		marketplace string
		agents      []string
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a plugin to the manifest and install it",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			if marketplace == "" {
				return fmt.Errorf("--marketplace is required")
			}
			p := config.Plugin{Name: name, Marketplace: marketplace, Agents: agents}
			res, err := state.app.AddPlugin(cmd.Context(), p)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "added %s\n", name)
			for _, e := range res.Errors {
				fmt.Fprintf(cmdOut(cmd), "  ! %s/%s: %v\n", e.AgentID, e.Name, e.Err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "plugin name (required)")
	cmd.Flags().StringVar(&marketplace, "marketplace", "", "declared marketplace name (required)")
	cmd.Flags().StringArrayVar(&agents, "agents", nil, "target agent IDs (repeatable)")
	return cmd
}

func newAgentsPluginsRemoveCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a plugin from the manifest",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := state.app.RemovePlugin(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "removed %s\n", args[0])
			for _, e := range res.Errors {
				fmt.Fprintf(cmdOut(cmd), "  ! %s/%s: %v\n", e.AgentID, e.Name, e.Err)
			}
			return nil
		},
	}
}

func newAgentsPluginsRestoreCmd(state *rootState) *cobra.Command {
	var opts app.RestorePluginOptions
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Install the manifest plugin set onto this host",
		RunE: func(cmd *cobra.Command, _ []string) error {
			res, err := state.app.RestorePlugins(cmd.Context(), opts)
			if err != nil {
				return err
			}
			w := cmdOut(cmd)
			for _, msg := range res.Warnings {
				fmt.Fprintf(w, "warn: %s\n", msg)
			}
			if opts.DryRun {
				for _, p := range res.WouldInstall {
					fmt.Fprintf(w, "would install: %s\n", p)
				}
				return nil
			}
			for _, p := range res.Installed {
				fmt.Fprintf(w, "installed: %s\n", p)
			}
			for _, e := range res.Errors {
				fmt.Fprintf(w, "  ! %s/%s: %v\n", e.AgentID, e.Name, e.Err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "print what would be installed without running")
	return cmd
}

func newAgentsPluginsImportCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "import [<name>]",
		Short: "List unmanaged plugins, or adopt one into the manifest by name",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			diff, err := state.app.ImportPlugins(cmd.Context())
			if err != nil {
				return err
			}
			if len(args) == 1 {
				return importPluginByName(cmd, state, diff, args[0])
			}
			w := cmdOut(cmd)
			agentIDs := make([]string, 0, len(diff.Unmanaged))
			for id := range diff.Unmanaged {
				agentIDs = append(agentIDs, id)
			}
			sort.Strings(agentIDs)
			for _, id := range agentIDs {
				fmt.Fprintf(w, "-- unmanaged (%s) --\n", id)
				for _, p := range diff.Unmanaged[id] {
					fmt.Fprintf(w, "%s\n", p.Name)
				}
			}
			return nil
		},
	}
}

// importPluginByName adopts an unmanaged plugin into the manifest: identity
// only (name + marketplace), never version. If the reported marketplace is
// not yet declared, the caller must add it first — see AddPlugin.
func importPluginByName(cmd *cobra.Command, state *rootState, diff app.PluginImportDiff, name string) error {
	agentIDs := make([]string, 0, len(diff.Unmanaged))
	for id := range diff.Unmanaged {
		agentIDs = append(agentIDs, id)
	}
	sort.Strings(agentIDs)

	var match app.InstalledPlugin
	found := false
	var matchedAgents []string
	for _, id := range agentIDs {
		for _, p := range diff.Unmanaged[id] {
			if p.Name != name {
				continue
			}
			if !found {
				match = p
				found = true
			} else if p.Marketplace != match.Marketplace {
				return fmt.Errorf("plugin %q is unmanaged under multiple agents with conflicting marketplaces; import each manually", name)
			}
			matchedAgents = append(matchedAgents, id)
		}
	}
	if !found {
		return fmt.Errorf("plugin %q is not unmanaged in any agent CLI", name)
	}

	p := config.Plugin{Name: match.Name, Marketplace: match.Marketplace, Agents: matchedAgents}
	res, err := state.app.AddPlugin(cmd.Context(), p)
	if err != nil {
		return err
	}
	for _, e := range res.Errors {
		fmt.Fprintf(cmdOut(cmd), "  ! %s/%s: %v\n", e.AgentID, e.Name, e.Err)
	}
	fmt.Fprintf(cmdOut(cmd), "imported %s\n", p.Name)
	return nil
}

func newAgentsPluginsMarketplaceCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "marketplace",
		Short: "Manage plugin marketplaces in the agent manifest",
	}
	cmd.AddCommand(
		newAgentsPluginsMarketplaceListCmd(state),
		newAgentsPluginsMarketplaceAddCmd(state),
		newAgentsPluginsMarketplaceRemoveCmd(state),
	)
	return cmd
}

func newAgentsPluginsMarketplaceListCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List declared marketplaces",
		RunE: func(cmd *cobra.Command, _ []string) error {
			marketplaces, err := state.app.Marketplaces()
			if err != nil {
				return err
			}
			w := cmdOut(cmd)
			for _, m := range marketplaces {
				fmt.Fprintf(w, "%s  %s\n", m.Name, m.Source)
			}
			return nil
		},
	}
}

func newAgentsPluginsMarketplaceAddCmd(state *rootState) *cobra.Command {
	var (
		source string
		agents []string
	)
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Declare a marketplace and add it to targeted agent CLIs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if source == "" {
				return fmt.Errorf("--source is required")
			}
			m := config.Marketplace{Name: args[0], Source: source, Agents: agents}
			res, err := state.app.AddMarketplace(cmd.Context(), m)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "added %s\n", args[0])
			for _, e := range res.Errors {
				fmt.Fprintf(cmdOut(cmd), "  ! %s/%s: %v\n", e.AgentID, e.Name, e.Err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&source, "source", "", "marketplace source, owner/repo or URL (required)")
	cmd.Flags().StringArrayVar(&agents, "agents", nil, "target agent IDs (repeatable)")
	return cmd
}

func newAgentsPluginsMarketplaceRemoveCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a marketplace from the manifest only (agents keep theirs)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := state.app.RemoveMarketplace(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "removed %s\n", args[0])
			return nil
		},
	}
}
```

`newAgentsPluginsMarketplaceListCmd` calls `state.app.Marketplaces()`, the small read-only accessor added to `internal/app/plugin_ops.go` in Task 4 Step 6 (mirrors how `internal/cli/settings.go`'s `newSettingsShowCmd` reads through `state.app.QuerySettings(...)` rather than reaching into `loadConfig` directly).

### Step 2: Add the action-catalog exemptions

In `internal/cli/action_catalog_test.go`, `uncatalogedRunnableCLICommandAllowed` (around line 349-384), add the new command paths to the `switch` case list right after `"agents mcp import":`:

```go
		"agents mcp import",
		"agents plugins list",
		"agents plugins add",
		"agents plugins remove",
		"agents plugins restore",
		"agents plugins import",
		"agents plugins marketplace list",
		"agents plugins marketplace add",
		"agents plugins marketplace remove":
		return true
```

### Step 3: Run the action-catalog test to confirm it currently fails without the exemption

Before Step 1's code exists this is moot; run this after Step 1/2 land:
Run: `go test ./internal/cli/... -run TestRunnableCLICommandsHaveCatalogCoverageOrExplicitExemption -v`
Expected: PASS once the exemptions are added (it would FAIL if the new commands existed without them).

### Step 4: Write the failing behavioral CLI tests

Create `internal/cli/agents_plugins_test.go`, mirroring `internal/cli/agents_test.go`'s harness:

```go
package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func TestAgentsPluginsSubcmdsRegistered(t *testing.T) {
	state := &rootState{}
	cmd := newAgentsPluginsCmd(state)
	want := map[string]bool{"list": false, "add": false, "remove": false, "restore": false, "import": false, "marketplace": false}
	for _, sub := range cmd.Commands() {
		want[sub.Name()] = true
	}
	for name, found := range want {
		if !found {
			t.Errorf("subcommand %q not registered", name)
		}
	}
}

func TestAgentsPluginsMarketplaceSubcmdsRegistered(t *testing.T) {
	state := &rootState{}
	cmd := newAgentsPluginsMarketplaceCmd(state)
	want := map[string]bool{"list": false, "add": false, "remove": false}
	for _, sub := range cmd.Commands() {
		want[sub.Name()] = true
	}
	for name, found := range want {
		if !found {
			t.Errorf("marketplace subcommand %q not registered", name)
		}
	}
}

type pluginStubAdapter struct {
	id            string
	listedPlugins []app.InstalledPlugin
	listedMarkets []string
	installed     []config.Plugin
	addedMarkets  []config.Marketplace
	removedNames  []string
}

func (s *pluginStubAdapter) ID() string      { return s.id }
func (s *pluginStubAdapter) Available() bool { return true }
func (s *pluginStubAdapter) ListPlugins(_ context.Context) ([]app.InstalledPlugin, error) {
	return append([]app.InstalledPlugin(nil), s.listedPlugins...), nil
}
func (s *pluginStubAdapter) InstallPlugin(_ context.Context, p config.Plugin) error {
	s.installed = append(s.installed, p)
	return nil
}
func (s *pluginStubAdapter) RemovePlugin(_ context.Context, name string) error {
	s.removedNames = append(s.removedNames, name)
	return nil
}
func (s *pluginStubAdapter) ListMarketplaces(_ context.Context) ([]string, error) {
	return append([]string(nil), s.listedMarkets...), nil
}
func (s *pluginStubAdapter) AddMarketplace(_ context.Context, m config.Marketplace) error {
	s.addedMarkets = append(s.addedMarkets, m)
	return nil
}

func newPluginCLITestApp(t *testing.T, adapters []app.PluginAdapter, agents config.AgentsConfig) *app.App {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	root := config.RootConfig{Version: config.CurrentVersion, Agents: agents}
	if err := config.Save(cfgPath, &root); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath, app.WithPluginAdapters(adapters))
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func TestAgentsPluginsAdd_RejectsUnknownMarketplace(t *testing.T) {
	stub := &pluginStubAdapter{id: "claude-code"}
	a := newPluginCLITestApp(t, []app.PluginAdapter{stub}, config.AgentsConfig{})
	state := &rootState{app: a}
	cmd := newAgentsPluginsAddCmd(state)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--name", "caveman", "--marketplace", "ghost"})
	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatal("expected error for unknown marketplace")
	}
}

func TestAgentsPluginsAdd_InstallsAndPersists(t *testing.T) {
	stub := &pluginStubAdapter{id: "claude-code"}
	agents := config.AgentsConfig{Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}}}
	a := newPluginCLITestApp(t, []app.PluginAdapter{stub}, agents)
	state := &rootState{app: a}
	cmd := newAgentsPluginsAddCmd(state)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--name", "caveman", "--marketplace", "caveman", "--agents", "claude-code"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("added caveman")) {
		t.Fatalf("expected confirmation, got: %s", out.String())
	}
	if len(stub.installed) != 1 {
		t.Fatalf("expected adapter install call, got %v", stub.installed)
	}
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.Plugins) != 1 || cfg.Agents.Plugins[0].Name != "caveman" {
		t.Fatalf("expected manifest entry, got %+v", cfg.Agents.Plugins)
	}
}

func TestAgentsPluginsImport_WithNameAdoptsIdentityOnly(t *testing.T) {
	stub := &pluginStubAdapter{
		id:            "claude-code",
		listedPlugins: []app.InstalledPlugin{{Name: "hand-added", Marketplace: "caveman", Version: "9.9.9"}},
	}
	agents := config.AgentsConfig{Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}}}
	a := newPluginCLITestApp(t, []app.PluginAdapter{stub}, agents)
	state := &rootState{app: a}
	cmd := newAgentsPluginsImportCmd(state)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"hand-added"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range cfg.Agents.Plugins {
		if p.Name != "hand-added" {
			continue
		}
		if p.Marketplace != "caveman" {
			t.Fatalf("expected adopted marketplace, got %+v", p)
		}
	}
}

func TestAgentsPluginsMarketplaceRemove_DeletesManifestOnly(t *testing.T) {
	stub := &pluginStubAdapter{id: "claude-code"}
	agents := config.AgentsConfig{Marketplaces: []config.Marketplace{{Name: "caveman", Source: "a/b"}}}
	a := newPluginCLITestApp(t, []app.PluginAdapter{stub}, agents)
	state := &rootState{app: a}
	cmd := newAgentsPluginsMarketplaceRemoveCmd(state)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"caveman"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.Marketplaces) != 0 {
		t.Fatal("expected marketplace removed from manifest")
	}
}
```

### Step 5: Run the tests to confirm they fail, then implement

Run: `go test ./internal/cli/... -run 'TestAgentsPlugins' -v`
Expected: FAIL — compile errors until Step 1's command tree (`newAgentsPluginsCmd` and its subcommands, plus `app.Marketplaces()` from Task 4) is wired up.

### Step 6: Run the tests and confirm they pass

Run: `go test ./internal/cli/... -run 'TestAgentsPlugins' -v`
Expected: PASS

### Step 7: Run the full CLI package test suite, including the action catalog and README checks

Run: `go test ./internal/cli/... -v`
Expected: PASS — pay particular attention to `TestReadmeOmniExamplesReferenceRegisteredCLI`; this plan's work does not add `omni agents plugins ...` examples to `README.md`, and that test only checks lines that already exist there, so no README edit is required to pass it. If a later task or reviewer wants README coverage, add it as a follow-up, not blocking this task's commit.

### Step 8: Commit

```bash
git add internal/cli/agents.go internal/cli/action_catalog_test.go internal/cli/agents_plugins_test.go
git commit -m "feat(cli): omni agents plugins list|add|remove|restore|import|marketplace"
```

---

## Task 6: TUI — Plugin Chip List + Keys

**Files:**
- Create: `internal/tui/update_plugin.go` (mirrors `internal/tui/update_mcp.go`)
- Modify: `internal/tui/model.go` (add plugin tab/picker fields next to the mcp block, lines ~507-530)
- Modify: `internal/tui/view_skills.go` (replace the `skillTypeIdx == 2` stub at lines 151-160 with `renderPluginTab`, add `renderPluginTab`/`pluginAgentBadges` near `renderMcpTab`/`mcpAgentBadges`)
- Modify: `internal/tui/update_keys.go` (dispatch to `handleMcpKeyMsg`'s sibling, extend the Tab-guard exclusion, extend `switchMainTab`'s first-load trigger)
- Modify: `internal/tui/view_hints.go` (add `hintCtxPluginRow`, `hintCtxPluginUnmanagedRow` to the const block, add their cases to the hint-lookup function)

**Interfaces:**
- Consumes: `app.PluginRow`, `app.PluginStatus*`, `app.InstalledPlugin`, `app.RestorePlugins/AddPlugin/RemovePlugin/SetPluginAgents/ImportPlugins/PluginRows` (Task 4); `app.SkillAgentRow` (existing, reused by the agents picker exactly as mcp does).
- Produces (used by Task 7):
  - `type pluginRowsMsg struct { rows []app.PluginRow; unmanaged map[string][]app.InstalledPlugin; err error }`
  - `type pluginRestoreDoneMsg struct{ err error }`, `pluginRemoveDoneMsg{ name string; err error }`, `pluginImportAdoptDoneMsg{ pluginName string; err error }`, `pluginAgentsSavedMsg{ err error }`, `pluginAddDoneMsg{ err error }`
  - `func (m *Model) doLoadPluginRows() tea.Cmd`, `doRestorePlugin() tea.Cmd`, `doRemovePlugin(name string) tea.Cmd`, `doImportPlugin(agentID string, p app.InstalledPlugin) tea.Cmd`, `doSetPluginAgents(row app.PluginRow, ids []string) tea.Cmd`
  - `func pluginUnmanagedFlat(...) []pluginUnmanagedEntry`, `pluginTotalRows(m Model) int`, `pluginHighlightedUnmanaged(m Model) (app.InstalledPlugin, string, bool)`, `clampPluginCursor(m *Model)`
  - Model fields: `pluginRows []app.PluginRow`, `pluginUnmanaged map[string][]app.InstalledPlugin`, `pluginCursor int`, `pluginErr error`, `pluginRunning bool`, `pluginLoaded bool`, `pluginDeleteConfirm bool`, `pluginDeleteName string`, `pluginAgentsPicker bool`, `pluginAgentsRow app.PluginRow`
  - `func (m *Model) handlePluginKeyMsg(msg tea.KeyPressMsg) []tea.Cmd`, `func (m *Model) handlePluginAgentsPickerKeyMsg(msg tea.KeyPressMsg) []tea.Cmd`
  - `func renderPluginTab(m Model, p palette, topLines []string) string`

### Step 1: Add plugin fields to `Model`

In `internal/tui/model.go`, right after the existing mcp-agents-picker block (after `mcpAgentsRow app.McpServerRow` and before the mcp add-form block, i.e. inserted between the two, so plugin's own picker/tab state sits together), add:

```go
	// plugin tab
	pluginRows          []app.PluginRow
	pluginUnmanaged     map[string][]app.InstalledPlugin
	pluginCursor        int
	pluginErr           error
	pluginRunning       bool
	pluginLoaded        bool
	pluginDeleteConfirm bool
	pluginDeleteName    string

	// plugin per-item agents picker popup (reuses skillAgentsRows/skillAgentsCursor)
	pluginAgentsPicker bool
	pluginAgentsRow    app.PluginRow
```

(No add-form fields yet — that is Task 7's job, mirroring how mcp's form fields are a separate block from its tab/picker fields.)

### Step 2: Add the hint contexts

In `internal/tui/view_hints.go`, extend the const block right after `hintCtxMcpUnmanagedRow`:

```go
	hintCtxMcpUnmanagedRow
	hintCtxPluginRow
	hintCtxPluginUnmanagedRow
)
```

Add the corresponding cases right after the `hintCtxMcpUnmanagedRow` case in the hint-lookup function, matching `hintCtxMcpRow`'s exact hint set (`a`/`g`/delete) for parity — `GroupConfig.Plugins` exists for restore filtering, mirrored here as a display-only `g` hint exactly like mcp's:

```go
	case hintCtxPluginRow:
		return []hintItem{
			rawHint("a", "agents"),
			rawHint("g", "groups"),
			dangerHintFromBindingDesc(m.keys.Delete, "delete"),
		}
	case hintCtxPluginUnmanagedRow:
		return []hintItem{
			rawHint("i", "import"),
		}
```

### Step 3: Write `internal/tui/update_plugin.go`

```go
package tui

import (
	"errors"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
)

// combinePluginErrors folds a top-level error with per-adapter errors,
// mirroring combineMcpErrors in update_mcp.go.
func combinePluginErrors(err error, adapterErrs []app.PluginError) error {
	if err == nil && len(adapterErrs) == 0 {
		return nil
	}
	all := make([]error, 0, len(adapterErrs)+1)
	if err != nil {
		all = append(all, err)
	}
	for _, e := range adapterErrs {
		all = append(all, e)
	}
	return errors.Join(all...)
}

type pluginRowsMsg struct {
	rows      []app.PluginRow
	unmanaged map[string][]app.InstalledPlugin
	err       error
}

type pluginRestoreDoneMsg struct{ err error }

type pluginRemoveDoneMsg struct {
	name string
	err  error
}

type pluginImportAdoptDoneMsg struct {
	pluginName string
	err        error
}

type pluginAgentsSavedMsg struct{ err error }

func (m *Model) doLoadPluginRows() tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		rows, unmanaged, err := a.PluginRows(ctx)
		return pluginRowsMsg{rows: rows, unmanaged: unmanaged, err: err}
	}
}

func (m *Model) doRestorePlugin() tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		_, err := a.RestorePlugins(ctx, app.RestorePluginOptions{})
		return pluginRestoreDoneMsg{err: err}
	}
}

func (m *Model) doRemovePlugin(name string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		res, err := a.RemovePlugin(ctx, name)
		return pluginRemoveDoneMsg{name: name, err: combinePluginErrors(err, res.Errors)}
	}
}

// doImportPlugin adopts one unmanaged plugin (already installed via the
// agent's own CLI) into the manifest, targeted at the agent it was found on.
func (m *Model) doImportPlugin(agentID string, p app.InstalledPlugin) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		res, err := a.AddPlugin(ctx, pluginFromInstalled(p, agentID))
		return pluginImportAdoptDoneMsg{pluginName: p.Name, err: combinePluginErrors(err, res.Errors)}
	}
}

func (m *Model) doSetPluginAgents(row app.PluginRow, ids []string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		res, err := a.SetPluginAgents(ctx, row.Name, ids)
		return pluginAgentsSavedMsg{err: combinePluginErrors(err, res.Errors)}
	}
}

type pluginUnmanagedEntry struct {
	agentID string
	plugin  app.InstalledPlugin
}

func pluginUnmanagedFlat(unmanaged map[string][]app.InstalledPlugin) []pluginUnmanagedEntry {
	agentIDs := make([]string, 0, len(unmanaged))
	for id := range unmanaged {
		agentIDs = append(agentIDs, id)
	}
	sort.Strings(agentIDs)
	var out []pluginUnmanagedEntry
	for _, id := range agentIDs {
		plugins := append([]app.InstalledPlugin(nil), unmanaged[id]...)
		sort.Slice(plugins, func(i, j int) bool { return plugins[i].Name < plugins[j].Name })
		for _, p := range plugins {
			out = append(out, pluginUnmanagedEntry{agentID: id, plugin: p})
		}
	}
	return out
}

func pluginTotalRows(m Model) int {
	return len(m.pluginRows) + len(pluginUnmanagedFlat(m.pluginUnmanaged))
}

func pluginHighlightedUnmanaged(m Model) (app.InstalledPlugin, string, bool) {
	if m.pluginCursor < len(m.pluginRows) {
		return app.InstalledPlugin{}, "", false
	}
	flat := pluginUnmanagedFlat(m.pluginUnmanaged)
	idx := m.pluginCursor - len(m.pluginRows)
	if idx < 0 || idx >= len(flat) {
		return app.InstalledPlugin{}, "", false
	}
	e := flat[idx]
	return e.plugin, e.agentID, true
}

func clampPluginCursor(m *Model) {
	n := pluginTotalRows(*m)
	if n == 0 {
		m.pluginCursor = 0
		return
	}
	if m.pluginCursor >= n {
		m.pluginCursor = n - 1
	}
	if m.pluginCursor < 0 {
		m.pluginCursor = 0
	}
}

func pluginGroupsStatusText(row app.PluginRow) string {
	if len(row.Groups) == 0 {
		return row.Name + ": no group memberships"
	}
	return row.Name + " groups: " + strings.Join(row.Groups, ", ")
}

// openPluginAgentsPicker builds an agents picker from a managed row's current
// per-adapter status, reusing the skill-agents popup fields, exactly as
// openMcpAgentsPicker does.
func (m *Model) openPluginAgentsPicker(row app.PluginRow) tea.Cmd {
	ids := make([]string, 0, len(row.PerAgentStatus))
	for id := range row.PerAgentStatus {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	targeted := func(id string) bool {
		if len(row.Agents) == 0 {
			return true
		}
		for _, a := range row.Agents {
			if a == id {
				return true
			}
		}
		return false
	}
	rows := make([]app.SkillAgentRow, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, app.SkillAgentRow{
			ID:        id,
			Display:   id,
			Targeted:  targeted(id),
			Installed: row.PerAgentStatus[id] == app.PluginStatusInstalled,
		})
	}
	m.skillAgentsSource = row.Name
	m.skillAgentsRows = rows
	m.skillAgentsCursor = 0
	m.pluginAgentsRow = row
	m.pluginAgentsPicker = true
	return nil
}

// pluginFromInstalled builds a config.Plugin for AddPlugin from an unmanaged
// InstalledPlugin found on agentID, mirroring how the mcp import path in
// update_keys.go's "i" case builds a config.McpServer inline.
func pluginFromInstalled(p app.InstalledPlugin, agentID string) config.Plugin {
	return config.Plugin{Name: p.Name, Marketplace: p.Marketplace, Agents: []string{agentID}}
}
```

This needs `"github.com/lkshrk/omni/internal/config"` imported for `config.Plugin` — add it to the import block.

### Step 4: Wire the plugin chip into `view_skills.go`

Replace the `skillTypeIdx == 2` stub (currently lines 151-160):

```go
	if m.skillTypeIdx == 2 {
		return renderSectionedTab(m, sectionedTab{
			leadingBlank: false,
			top:          topLines,
			sections: []sectionedTabSection{{
				rows:  nil,
				empty: []string{p.styleHelp.Render(pad + "No plugins tracked yet.")},
			}},
		})
	}
```

with:

```go
	if m.skillTypeIdx == 2 {
		return renderPluginTab(m, p, topLines)
	}
```

Add `renderPluginTab` and `pluginAgentBadges` right after `mcpAgentBadges` (after line 445), cloning `renderMcpTab`/`mcpAgentBadges` field-for-field:

```go
// renderPluginTab renders the plugin chip: managed rows (name, marketplace,
// per-agent status badges) followed by an unmanaged section, mirroring
// renderMcpTab exactly.
func renderPluginTab(m Model, p palette, topLines []string) string {
	pad := screenEdgeInset()
	hintPrefix := listHintPrefix()

	managedCount := len(m.pluginRows)
	var managedRows []sectionedTabRow
	for i, row := range m.pluginRows {
		selected := i == m.pluginCursor && !m.cursorHidden
		line := listRowPrefix(p, selected) + "  " + p.styleNormal.Render(row.Name) +
			"  " + p.styleHelp.Render(row.Marketplace) + "  " + pluginAgentBadges(p, row)
		var details []string
		if selected {
			details = append(details, renderContextHints(m, hintCtxPluginRow, hintPrefix))
		}
		managedRows = append(managedRows, sectionedTabRow{selected: selected, line: line, details: details})
	}

	unmanagedFlat := pluginUnmanagedFlat(m.pluginUnmanaged)
	var unmanagedRows []sectionedTabRow
	for i, e := range unmanagedFlat {
		idx := managedCount + i
		selected := idx == m.pluginCursor && !m.cursorHidden
		line := listRowPrefix(p, selected) + "  " + p.styleHelp.Render(e.agentID) +
			"  " + p.styleNormal.Render(e.plugin.Name) + "  " + p.styleHelp.Render(e.plugin.Marketplace)
		var details []string
		if selected {
			details = append(details, renderContextHints(m, hintCtxPluginUnmanagedRow, hintPrefix))
		}
		unmanagedRows = append(unmanagedRows, sectionedTabRow{selected: selected, line: line, details: details})
	}

	var sections []sectionedTabSection
	if managedCount == 0 {
		var empty []string
		switch {
		case m.pluginRunning:
			empty = []string{p.styleHelp.Render(pad + "  Loading...")}
		case m.pluginErr != nil:
			empty = []string{p.styleErr.Render(pad + "  " + m.pluginErr.Error())}
		default:
			empty = []string{p.styleHelp.Render(pad + "No plugins tracked yet.")}
		}
		sections = append(sections, sectionedTabSection{rows: nil, empty: empty})
	} else {
		sections = append(sections, sectionedTabSection{rows: managedRows})
	}
	if len(unmanagedRows) > 0 {
		sections = append(sections, sectionedTabSection{title: "unmanaged", rows: unmanagedRows})
	}

	return renderSectionedTab(m, sectionedTab{
		leadingBlank: false,
		top:          topLines,
		sections:     sections,
	})
}

// pluginAgentBadges renders "<agentID>(<marker>)" per agent, sorted by ID,
// mirroring mcpAgentBadges.
func pluginAgentBadges(p palette, row app.PluginRow) string {
	ids := make([]string, 0, len(row.PerAgentStatus))
	for id := range row.PerAgentStatus {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		marker := iconInstalled
		style := p.styleInstalled
		switch row.PerAgentStatus[id] {
		case app.PluginStatusMissing:
			marker = "-"
			style = p.styleMissing
		case app.PluginStatusAgentUnavailable:
			marker = "?"
			style = p.styleHelp
		}
		parts = append(parts, id+"("+style.Render(marker)+")")
	}
	return strings.Join(parts, " ")
}
```

### Step 5: Wire key dispatch in `update_keys.go`

In the `viewSkills` case's dispatch chain, add a `pluginAgentsPicker` gate right after the `mcpAgentsPicker` gate (around line 158-161), and a `skillTypeIdx == 2` dispatch right after the `skillTypeIdx == 1` dispatch (around line 171-174):

```go
		if m.mcpAgentsPicker {
			cmds = append(cmds, m.handleMcpAgentsPickerKeyMsg(msg)...)
			break
		}
		if m.pluginAgentsPicker {
			cmds = append(cmds, m.handlePluginAgentsPickerKeyMsg(msg)...)
			break
		}
		if m.skillAgentsPicker {
			cmds = append(cmds, m.handleSkillAgentsPickerKeyMsg(msg)...)
			break
		}
		// Search mode intercepts keys while the input is focused.
		if m.skillsSearchActive && m.filter.Focused() {
			cmds = append(cmds, m.handleSkillsSearchKeyMsg(msg)...)
			break
		}
		if m.skillTypeIdx == 1 {
			cmds = append(cmds, m.handleMcpKeyMsg(msg)...)
			break
		}
		if m.skillTypeIdx == 2 {
			cmds = append(cmds, m.handlePluginKeyMsg(msg)...)
			break
		}
```

Extend the Tab-guard exclusion in `handleTabKeyMsg` (line 286) to also exclude `m.pluginAgentsPicker` (the plugin add-form guard, `m.pluginFormOpen`, is added in Task 7 alongside its form fields):

```go
	if !key.Matches(msg, m.keys.Tab) || m.mode == viewSearch || m.mode == viewCommand || m.mode == viewGroupPicker || m.mode == viewGroupMembership || m.mode == viewGroupTools || m.mode == viewGroupDots || m.mode == viewIgnoreScope || m.mode == viewProviderScope || m.mode == viewAdminTerminal || m.hostRequired || m.mcpFormOpen || m.mcpAgentsPicker || m.skillAgentsPicker || m.pluginAgentsPicker {
		return false
	}
```

Extend `switchMainTab`'s first-load trigger, right after the existing `mcpLoaded` block:

```go
	if target == viewSkills && m.agentsEnabled && !m.mcpLoaded {
		m.mcpLoaded = true
		m.mcpRunning = true
		*cmds = append(*cmds, m.spinner.Tick, m.doLoadMcpRows())
	}
	if target == viewSkills && m.agentsEnabled && !m.pluginLoaded {
		m.pluginLoaded = true
		m.pluginRunning = true
		*cmds = append(*cmds, m.spinner.Tick, m.doLoadPluginRows())
	}
```

Add `handlePluginKeyMsg` and `handlePluginAgentsPickerKeyMsg` near `handleMcpKeyMsg`/the mcp-agents-picker handler, cloning their bodies field-for-field (substituting `plugin*`/`Plugin*` names for `mcp*`/`Mcp*`, and dropping the `"n"` add-form case — that arrives in Task 7):

```go
// handlePluginKeyMsg dispatches keys for the plugin chip (skillTypeIdx == 2):
// tab switching, cursor movement across managed+unmanaged rows, and
// r/i/a/d/g actions. Mirrors handleMcpKeyMsg exactly minus the "n" add-form
// case, which Task 7 adds once the plugin form fields exist.
func (m *Model) handlePluginKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd
	s := msg.String()

	if m.pluginDeleteConfirm {
		switch {
		case key.Matches(msg, m.keys.Confirm) || key.Matches(msg, m.keys.Delete):
			m.cancelConfirmationTimeout()
			name := m.pluginDeleteName
			m.pluginDeleteConfirm = false
			m.pluginDeleteName = ""
			m.pluginRunning = true
			m.pluginErr = nil
			cmds = append(cmds, m.spinner.Tick, m.doRemovePlugin(name))
		case key.Matches(msg, m.keys.Back):
			m.cancelConfirmationTimeout()
			m.pluginDeleteConfirm = false
			m.pluginDeleteName = ""
		}
		return cmds
	}

	switch s {
	case "left", "h":
		if m.skillTypeIdx > 0 {
			m.skillTypeIdx--
			m.pluginCursor = 0
		}
	case "right", "l":
		if m.skillTypeIdx < 2 {
			m.skillTypeIdx++
			m.pluginCursor = 0
		}
	case "up", "k":
		if n := pluginTotalRows(*m); n > 0 {
			m.pluginCursor = (m.pluginCursor - 1 + n) % n
		}
	case "down", "j":
		if n := pluginTotalRows(*m); n > 0 {
			m.pluginCursor = (m.pluginCursor + 1) % n
		}
	case "r":
		m.pluginRunning = true
		m.pluginErr = nil
		cmds = append(cmds, m.spinner.Tick, m.doRestorePlugin())
	case "i":
		if plg, agentID, ok := pluginHighlightedUnmanaged(*m); ok {
			m.pluginRunning = true
			m.pluginErr = nil
			cmds = append(cmds, m.spinner.Tick, m.doImportPlugin(agentID, plg))
		}
	case "d":
		if m.pluginCursor < len(m.pluginRows) {
			m.pluginDeleteConfirm = true
			m.pluginDeleteName = m.pluginRows[m.pluginCursor].Name
			cmds = append(cmds, m.armConfirmationTimeout())
		}
	case "a":
		if m.pluginCursor < len(m.pluginRows) {
			cmds = append(cmds, m.openPluginAgentsPicker(m.pluginRows[m.pluginCursor]))
		}
	case "g":
		if m.pluginCursor < len(m.pluginRows) {
			cmds = append(cmds, setStatus(m, pluginGroupsStatusText(m.pluginRows[m.pluginCursor]), false))
		}
	}
	return cmds
}

func (m *Model) handlePluginAgentsPickerKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd
	s := msg.String()
	up := s == "k" || key.Matches(msg, m.keys.Up)
	down := s == "j" || key.Matches(msg, m.keys.Down)
	switch {
	case up:
		if m.skillAgentsCursor > 0 {
			m.skillAgentsCursor--
		}
	case down:
		if m.skillAgentsCursor < len(m.skillAgentsRows)-1 {
			m.skillAgentsCursor++
		}
	case key.Matches(msg, m.keys.Toggle):
		if m.skillAgentsCursor >= 0 && m.skillAgentsCursor < len(m.skillAgentsRows) {
			m.skillAgentsRows[m.skillAgentsCursor].Targeted = !m.skillAgentsRows[m.skillAgentsCursor].Targeted
		}
	case key.Matches(msg, m.keys.Confirm):
		ids := make([]string, 0, len(m.skillAgentsRows))
		for _, r := range m.skillAgentsRows {
			if r.Targeted {
				ids = append(ids, r.ID)
			}
		}
		row := m.pluginAgentsRow
		m.pluginAgentsPicker = false
		m.pluginRunning = true
		m.pluginErr = nil
		cmds = append(cmds, m.spinner.Tick, m.doSetPluginAgents(row, ids))
	case key.Matches(msg, m.keys.Back):
		m.pluginAgentsPicker = false
	}
	return cmds
}
```

Also handle `pluginRowsMsg`/`pluginRestoreDoneMsg`/`pluginRemoveDoneMsg`/`pluginImportAdoptDoneMsg`/`pluginAgentsSavedMsg` in whichever `Update` switch already handles `mcpRowsMsg`/`mcpRestoreDoneMsg`/etc. (grep `internal/tui/update.go` — or wherever the top-level `case mcpRowsMsg:` lives — for the exact file and add sibling `case pluginRowsMsg:` etc. that set `m.pluginRows`/`m.pluginUnmanaged`/`m.pluginErr`/`m.pluginRunning = false` and, on the done-msgs, re-trigger `m.doLoadPluginRows()` the same way the mcp done-msgs do (find that exact reload-after-mutation pattern in the mcp case block and mirror it one-for-one, including clamping via `clampPluginCursor`)).

### Step 6: Build

Run: `go build ./...`
Expected: succeeds with no compile errors (this task adds no new `_test.go` files of its own — TUI tests are the tui-tester agent's job in Step 7).

### Step 7: Dispatch to the tui-tester agent for tests

Dispatch a **tui-tester** agent with this brief: "Write Bubbletea TUI unit tests for the new plugin chip (`skillTypeIdx == 2`) in `internal/tui`. Cover, mirroring the existing mcp-chip test file's structure and helper conventions exactly: (1) `renderPluginTab` renders managed rows with name/marketplace/agent badges and an unmanaged section; (2) cursor movement (`up`/`down`/`k`/`j`) across combined managed+unmanaged rows via `pluginTotalRows`/`clampPluginCursor`; (3) `r` triggers `doRestorePlugin` and sets `pluginRunning`; (4) `i` on a highlighted unmanaged row triggers `doImportPlugin` with the correct agent ID; (5) `d` arms `pluginDeleteConfirm` and Confirm/Back resolve it, calling `doRemovePlugin` only on confirm; (6) `a` opens the agents picker (`pluginAgentsPicker`) populated from `PerAgentStatus`, toggle/confirm/back behavior via `handlePluginAgentsPickerKeyMsg`; (7) `left`/`right`/`h`/`l` moves `skillTypeIdx` between 0/1/2 and resets `pluginCursor`; (8) the Tab-guard excludes `pluginAgentsPicker`; (9) entering the skills view for the first time triggers `doLoadPluginRows` exactly once (`pluginLoaded` latch). Use only real `PluginAdapter`/`app.PluginRow`/`app.InstalledPlugin` fixtures via a fake `app.App` built the same way the existing mcp chip tests build theirs — find and reuse that exact test-app constructor rather than inventing a new one. Do not touch mcp or skills tests; only add plugin coverage."

### Step 8: Commit

```bash
git add internal/tui/update_plugin.go internal/tui/model.go internal/tui/view_skills.go internal/tui/update_keys.go internal/tui/view_hints.go internal/tui/*_test.go
git commit -m "feat(tui): plugin chip list, cursor, restore/import/remove/agents keys"
```

---

## Task 7: TUI — Add-Plugin Form

**Files:**
- Create: `internal/tui/view_plugin_form.go` (mirrors `internal/tui/view_mcp_form.go`)
- Modify: `internal/tui/model.go` (add plugin form fields next to the plugin tab block from Task 6)
- Modify: `internal/tui/update_plugin.go` (add `resetPluginForm`, `focusPluginFormField`, `buildPluginFromForm`, `doAddPlugin`, `pluginAddDoneMsg` — the message type was declared in Task 6's Interfaces list but its `tea.Cmd` producer belongs here since it depends on form state)
- Modify: `internal/tui/update_keys.go` (add the `"n"` case to `handlePluginKeyMsg`, add `handlePluginFormKeyMsg`, extend the `viewSkills` dispatch and Tab-guard)
- Modify: `internal/tui/view_skills.go` or wherever the top-level popup-render switch lives (find where `mcpFormOpen` triggers `renderMcpFormPopup`/`mcpFormPopupFrame` and add the plugin sibling)

**Interfaces:**
- Consumes: Task 6's `pluginRows`/`pluginAgentsRow` state, `app.AddPlugin`, `config.Plugin`, `config.Marketplace`, `app.Marketplaces()` (Task 4).
- Produces (used by Task 8's txtar smoke and Task 9's live smoke, indirectly — the CLI path is what those exercise, not the TUI form directly, but the form must build the identical `config.Plugin` shape the CLI's `add` command does):
  - Model fields: `pluginFormOpen bool`, `pluginFormField int` (0=name,1=marketplace,2=agents-text — 3 fields total, simpler than mcp's 5 since plugins have no transport/env), `pluginFormName textinput.Model`, `pluginFormMarketplace textinput.Model`, `pluginFormAgents textinput.Model`, `pluginFormErr error`
  - `func (m *Model) resetPluginForm()`, `func (m *Model) focusPluginFormField()`, `func (m *Model) buildPluginFromForm() (config.Plugin, error)`
  - `func (m *Model) doAddPlugin(p config.Plugin) tea.Cmd`, `type pluginAddDoneMsg struct{ err error }`
  - `func (m *Model) handlePluginFormKeyMsg(msg tea.KeyPressMsg) []tea.Cmd`
  - `func pluginFormPopupFrame(m Model) popupFrame`, `func renderPluginFormPopup(m Model) string`

### Step 1: Add plugin form fields to `Model`

In `internal/tui/model.go`, right after the plugin tab/picker block added in Task 6 (after `pluginAgentsRow app.PluginRow`), add:

```go
	// plugin add-form popup
	pluginFormOpen        bool
	pluginFormField       int // 0=name,1=marketplace,2=agents
	pluginFormName        textinput.Model
	pluginFormMarketplace textinput.Model
	pluginFormAgents      textinput.Model
	pluginFormErr         error
```

Find where the mcp form textinputs are constructed (`mfn`, `mfc`, `mfu`, `mfe`, `mfl` around lines 590-626 in the existing code) and add sibling construction for the plugin form fields in the same constructor function, following the exact `textinput.New()` + `.Placeholder =` + assignment-into-struct-literal pattern used there:

```go
	pfn := textinput.New()
	pfn.Placeholder = "caveman"
	pfm := textinput.New()
	pfm.Placeholder = "caveman"
	pfa := textinput.New()
	pfa.Placeholder = "claude-code,codex"
```

and add `pluginFormName: pfn, pluginFormMarketplace: pfm, pluginFormAgents: pfa,` to the struct literal alongside `mcpFormName: mfn, ...`.

### Step 2: Add form helpers to `update_plugin.go`

Append to `internal/tui/update_plugin.go`:

```go
type pluginAddDoneMsg struct{ err error }

// doAddPlugin registers a new plugin built from the add-plugin form.
func (m *Model) doAddPlugin(p config.Plugin) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		res, err := a.AddPlugin(ctx, p)
		return pluginAddDoneMsg{err: combinePluginErrors(err, res.Errors)}
	}
}

// resetPluginForm clears the add-plugin form back to its initial state.
func (m *Model) resetPluginForm() {
	m.pluginFormField = 0
	m.pluginFormName.SetValue("")
	m.pluginFormMarketplace.SetValue("")
	m.pluginFormAgents.SetValue("")
	m.pluginFormName.Blur()
	m.pluginFormMarketplace.Blur()
	m.pluginFormAgents.Blur()
}

// focusPluginFormField blurs every add-plugin field then focuses the one at
// m.pluginFormField, mirroring focusMcpFormField.
func (m *Model) focusPluginFormField() {
	m.pluginFormName.Blur()
	m.pluginFormMarketplace.Blur()
	m.pluginFormAgents.Blur()
	switch m.pluginFormField {
	case 0:
		m.pluginFormName.Focus()
	case 1:
		m.pluginFormMarketplace.Focus()
	case 2:
		m.pluginFormAgents.Focus()
	}
}

// buildPluginFromForm validates and constructs a config.Plugin from the
// add-plugin form's current field values. Name and marketplace are required;
// agents is an optional comma-separated list (empty means all MVP agents).
func (m *Model) buildPluginFromForm() (config.Plugin, error) {
	name := strings.TrimSpace(m.pluginFormName.Value())
	if name == "" {
		return config.Plugin{}, errors.New("name is required")
	}
	marketplace := strings.TrimSpace(m.pluginFormMarketplace.Value())
	if marketplace == "" {
		return config.Plugin{}, errors.New("marketplace is required")
	}
	var agents []string
	raw := strings.TrimSpace(m.pluginFormAgents.Value())
	if raw != "" {
		for _, part := range strings.Split(raw, ",") {
			id := strings.TrimSpace(part)
			if id != "" {
				agents = append(agents, id)
			}
		}
	}
	return config.Plugin{Name: name, Marketplace: marketplace, Agents: agents}, nil
}
```

This requires adding `"github.com/lkshrk/omni/internal/config"` to the file's imports if Task 6's `pluginFromInstalled` helper did not already add it (it did — reuse the same import line, do not duplicate it).

### Step 3: Add the `"n"` case and form key handler in `update_keys.go`

Add to the end of `handlePluginKeyMsg`'s `switch s` block (after the `"g"` case, mirroring `handleMcpKeyMsg`'s `"n"` case exactly):

```go
	case "n":
		m.pluginFormOpen = true
		m.pluginFormErr = nil
		m.resetPluginForm()
		m.focusPluginFormField()
		cmds = append(cmds, textinput.Blink)
```

Add `handlePluginFormKeyMsg` right after `handleMcpFormKeyMsg`, with a 3-field cycle instead of mcp's 5, and no transport left/right toggle (plugins have no transport concept):

```go
func (m *Model) handlePluginFormKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	var cmds []tea.Cmd
	switch {
	case key.Matches(msg, m.keys.Back):
		m.pluginFormOpen = false
		m.pluginFormErr = nil
		m.resetPluginForm()
	case key.Matches(msg, m.keys.Tab):
		if msg.Mod.Contains(tea.ModShift) {
			m.pluginFormField = (m.pluginFormField + 2) % 3
		} else {
			m.pluginFormField = (m.pluginFormField + 1) % 3
		}
		m.focusPluginFormField()
	case key.Matches(msg, m.keys.Confirm):
		p, err := m.buildPluginFromForm()
		if err != nil {
			m.pluginFormErr = err
			return cmds
		}
		m.pluginFormErr = nil
		m.pluginRunning = true
		cmds = append(cmds, m.spinner.Tick, m.doAddPlugin(p))
	default:
		var cmd tea.Cmd
		switch m.pluginFormField {
		case 0:
			m.pluginFormName, cmd = m.pluginFormName.Update(msg)
		case 1:
			m.pluginFormMarketplace, cmd = m.pluginFormMarketplace.Update(msg)
		case 2:
			m.pluginFormAgents, cmd = m.pluginFormAgents.Update(msg)
		}
		cmds = append(cmds, cmd)
	}
	return cmds
}
```

Before writing the `default` branch above, open `internal/tui/update_keys.go`'s existing `handleMcpFormKeyMsg` in full (it was only partially shown during research, cut off at its `Confirm` case) and confirm the exact pattern it uses to forward unmatched keys into the focused `textinput.Model` — mirror that pattern exactly rather than the sketch above if it differs (e.g. it may dispatch through a shared helper instead of a per-field switch).

Wire the dispatch gate in the `viewSkills` case, right after the existing `if m.mcpFormOpen { ... }` block (before the `mcpAgentsPicker` gate added in Task 6):

```go
		if m.mcpFormOpen {
			cmds = append(cmds, m.handleMcpFormKeyMsg(msg)...)
			break
		}
		if m.pluginFormOpen {
			cmds = append(cmds, m.handlePluginFormKeyMsg(msg)...)
			break
		}
```

Extend the Tab-guard exclusion (from Task 6) to also exclude `m.pluginFormOpen`:

```go
	if !key.Matches(msg, m.keys.Tab) || m.mode == viewSearch || m.mode == viewCommand || m.mode == viewGroupPicker || m.mode == viewGroupMembership || m.mode == viewGroupTools || m.mode == viewGroupDots || m.mode == viewIgnoreScope || m.mode == viewProviderScope || m.mode == viewAdminTerminal || m.hostRequired || m.mcpFormOpen || m.mcpAgentsPicker || m.skillAgentsPicker || m.pluginAgentsPicker || m.pluginFormOpen {
		return false
	}
```

Handle `pluginAddDoneMsg` in the same top-level `Update` switch found for Task 6's other plugin done-msgs, mirroring `mcpAddDoneMsg`'s handling exactly (close the form on success, reload rows, surface `m.pluginErr` on failure — find `mcpAddDoneMsg`'s case and mirror it one-for-one).

### Step 4: Write `internal/tui/view_plugin_form.go`

Clone `internal/tui/view_mcp_form.go` with 3 fields instead of 5, no transport row:

```go
package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
)

func pluginFormPopupFrame(m Model) popupFrame {
	paddingX := 2
	contentW := popupContentWidth(m, 52, 40, 60)
	contentH := 3 + 2 + popupFooterHeight // 3 fields + blank + optional error line
	if m.pluginFormErr != nil {
		contentH++
	}
	return popupFrame{
		Title:          "Add Plugin",
		PaddingY:       1,
		PaddingX:       paddingX,
		Width:          popupFrameWidthForContent(contentW, paddingX),
		ContentHeight:  contentH,
		NoTitleDivider: true,
	}
}

func renderPluginFormPopup(m Model) string {
	p := m.palette
	contentW := popupContentWidth(m, 52, 40, 60)

	var sb strings.Builder
	sb.WriteString(renderPluginFormRow(m, contentW, "Name:", 0))
	sb.WriteString("\n")
	sb.WriteString(renderPluginFormRow(m, contentW, "Marketplace:", 1))
	sb.WriteString("\n")
	sb.WriteString(renderPluginFormRow(m, contentW, "Agents:", 2))
	sb.WriteString("\n\n")

	if m.pluginFormErr != nil {
		sb.WriteString(p.styleErr.Render(m.pluginFormErr.Error()))
		sb.WriteString("\n")
	}

	if m.pluginRunning {
		sb.WriteString(p.styleHelp.Render(m.spinner.View() + " adding…"))
		sb.WriteString("\n")
	}

	sb.WriteString(renderPickerHintItems(m, contentW, pluginFormHintItems(m)))
	return lipgloss.NewStyle().Width(contentW).Render(sb.String())
}

func pluginFormHintItems(m Model) []hintItem {
	return []hintItem{
		hintFromBindingDesc(m.keys.Tab, "next/prev field"),
		hintFromBindingDesc(m.keys.Confirm, "add"),
		hintFromBindingDesc(m.keys.Back, "cancel"),
	}
}

const pluginFormLabelWidth = 13

func renderPluginFormRow(m Model, width int, label string, field int) string {
	p := m.palette
	labelStyle := p.styleHelp
	if m.pluginFormField == field {
		labelStyle = p.styleActiveText
	}
	paddedLabel := label + strings.Repeat(" ", max(pluginFormLabelWidth-lipgloss.Width(label), 1))
	inputWidth := max(width-lipgloss.Width(paddedLabel)-4, 1)

	var input textinput.Model
	switch field {
	case 0:
		input = m.pluginFormName
	case 1:
		input = m.pluginFormMarketplace
	case 2:
		input = m.pluginFormAgents
	}
	inputView := renderEmptyAwareTextInputView(p, input, input.Placeholder, inputWidth)

	return labelStyle.Render(paddedLabel) + p.styleHelp.Render("[ ") + inputView + p.styleHelp.Render(" ]")
}
```

### Step 5: Wire the popup into the render switch

Find where `m.mcpFormOpen` triggers rendering `renderMcpFormPopup`/`mcpFormPopupFrame` in the main view-render path (search `internal/tui` for `mcpFormPopupFrame(` to locate the exact call site — likely a top-level `View()`/popup-overlay function) and add a sibling branch for `m.pluginFormOpen` calling `renderPluginFormPopup`/`pluginFormPopupFrame`, in the same position relative to other popup checks (e.g. before or after the `mcpAgentsPicker`/`skillAgentsPicker` popup branches — match their exact ordering, since popup overlays are mutually exclusive and the first match typically wins).

### Step 6: Build

Run: `go build ./...`
Expected: succeeds with no compile errors.

### Step 7: Dispatch to the tui-tester agent for tests

Dispatch a **tui-tester** agent with this brief: "Write Bubbletea TUI unit tests for the new add-plugin form (`pluginFormOpen`) in `internal/tui`, mirroring the existing mcp add-form test file's structure and helper conventions exactly. Cover: (1) `n` from the plugin chip opens the form and resets/focuses field 0; (2) Tab/Shift+Tab cycles through the 3 fields (name, marketplace, agents) and wraps; (3) typing into the focused field updates the right `textinput.Model`; (4) Confirm with an empty name or empty marketplace sets `pluginFormErr` and does not call `doAddPlugin`; (5) Confirm with valid name+marketplace (and optional comma-separated agents parsed into a slice) calls `doAddPlugin` with the exact expected `config.Plugin`, sets `pluginRunning`; (6) Back/Esc closes the form and resets it; (7) the Tab-guard (main-tab switching) is blocked while `pluginFormOpen` is true; (8) `pluginAddDoneMsg` success closes the form and triggers a rows reload, failure surfaces `pluginErr`/`pluginFormErr` (check `handlePluginFormKeyMsg`'s and the top-level Update's actual behavior to know which field is set, and assert that one). Use the same fake `app.App` / `PluginAdapter` fixtures the Task 6 plugin-chip tests used. Do not touch mcp or skills tests; only add plugin-form coverage."

### Step 8: Commit

```bash
git add internal/tui/view_plugin_form.go internal/tui/model.go internal/tui/update_plugin.go internal/tui/update_keys.go internal/tui/*_test.go
git commit -m "feat(tui): add-plugin form"
```

---

## Task 8: Integration Txtar Fixtures

**Files:**
- Create: `integration_tests/testdata/scripts/agents-plugins-list.txtar`
- Create: `integration_tests/testdata/scripts/agents-plugins-add.txtar`
- Create: `integration_tests/testdata/scripts/agents-plugins-remove.txtar`
- Create: `integration_tests/testdata/scripts/agents-plugins-restore.txtar`
- Create: `integration_tests/testdata/scripts/agents-plugins-marketplace.txtar`

**Interfaces:**
- Consumes: the CLI surface from Task 5 (`omni agents plugins list|add|remove|restore|import`, `omni agents plugins marketplace list|add|remove`) and the fake-binary pattern from `integration_tests/testdata/scripts/agents-mcp-*.txtar`.
- Produces: nothing further downstream — this is leaf-level coverage.

### Step 1: Dispatch to the txtar-writer agent

Do not write these fixtures inline. Dispatch a **txtar-writer** agent with this brief: "Write txtar integration test fixtures for the new `omni agents plugins ...` and `omni agents plugins marketplace ...` CLI commands (added in `internal/cli/agents.go` by `newAgentsPluginsCmd`/`newAgentsPluginsMarketplaceCmd`, see git history on this branch for the exact flag names: `add --name --marketplace --agents`, `remove <name>`, `restore [--dry-run]`, `import [<name>]`, `marketplace add <name> --source --agents`, `marketplace remove <name>`, `marketplace list`). Follow the existing fake-binary pattern exactly as shown in `integration_tests/testdata/scripts/agents-mcp-add.txtar` and its siblings (`agents-mcp-list.txtar`, `agents-mcp-remove.txtar`, `agents-mcp-restore.txtar`): a `-- bin/claude --` (and where relevant `-- bin/codex --`) fake shell script that logs invocations to `${OMNI_CACHE_DIR}` and echoes deterministic success text for the specific subcommand/flag combination under test, `env PATH=$WORK/bin:$PATH` to shadow any real agent CLI on the host, and `--agents claude-code` (or the equivalent new flag) to keep tests deterministic and non-flaky regardless of what's actually on the test runner's PATH.

Cover at minimum, one txtar file per bullet:
- `agents-plugins-list.txtar`: manifest has one declared marketplace + one declared plugin; `list` shows the plugin with its marketplace and an agent status marker; a second, unmanaged plugin reported by the fake `claude` binary's `plugins list --json` shows up under an `-- unmanaged --` section.
- `agents-plugins-add.txtar`: `marketplace add` a marketplace, then `plugins add` a plugin referencing it, confirm success text and that `plugins add` referencing an undeclared marketplace fails with a non-zero exit and an error message; confirm re-adding the same plugin name is an upsert (no duplicate manifest entry, verified via a subsequent `plugins restore --dry-run` showing exactly one `would install:` line for that name, mirroring `agents-mcp-add.txtar`'s upsert-idempotency check).
- `agents-plugins-remove.txtar`: add then remove a plugin, confirm the fake claude binary's uninstall/remove path was invoked (grep the calls log) and the manifest no longer lists it, while `marketplace list` still shows the marketplace it depended on.
- `agents-plugins-restore.txtar`: declare marketplace + plugin directly in `-- settings.json --`, run `restore --dry-run` and assert the exact `would install: claude-code/<name>` line, then run `restore` for real against the fake binary and assert `installed: claude-code/<name>`.
- `agents-plugins-marketplace.txtar`: `marketplace add`, `marketplace list` shows it, `marketplace remove` deletes only the manifest entry (assert the fake claude binary's marketplace-remove path — if any — is never invoked, since omni must never remove a marketplace from an agent).

Do not touch the existing `agents-mcp-*.txtar` files. Run `make test-integration` (or the project's documented integration test command — check the Makefile) after writing the fixtures and confirm they pass before finishing."

### Step 2: Verify the fixtures were added and pass

Run: `go test ./integration_tests/... -run TestScripts/agents-plugins -v` (or whatever invocation the txtar-writer agent's own verification step used — confirm via `make test-integration` if unsure of the exact test entrypoint)
Expected: PASS for all 5 new fixture files.

### Step 3: Commit

```bash
git add integration_tests/testdata/scripts/agents-plugins-list.txtar integration_tests/testdata/scripts/agents-plugins-add.txtar integration_tests/testdata/scripts/agents-plugins-remove.txtar integration_tests/testdata/scripts/agents-plugins-restore.txtar integration_tests/testdata/scripts/agents-plugins-marketplace.txtar
git commit -m "test(integration): agents plugins CLI txtar fixtures"
```

---

## Task 9: Live Smoke Test (Sandboxed, Real Binaries)

**Files:** none (verification-only task; no source changes)

**Interfaces:**
- Consumes: the built `omni` binary (Tasks 1-7), the real `claude`/`codex` CLIs.
- Produces: a pass/fail signal gating Task 10. If this task fails, stop and fix the root cause in the relevant earlier task before proceeding — do not patch around it here.

This task exists because the MCP feature shipped with both list parsers matching zero real CLI output lines; the live probe in Task 1 reduces that risk but does not eliminate it, since parsers were written from a point-in-time transcript that could still have been misread. This is the final check against the actual binaries before considering the feature done.

### Step 1: Build the omni binary

Run: `go build -o /tmp/omni-plugin-smoke ./cmd/omni` (adjust the module's actual `main` package path if it differs — check `Makefile`'s `build` target for the exact `go build` invocation and mirror its package path and ldflags).

### Step 2: Set up a fresh sandbox

```bash
SMOKE_DIR="$(mktemp -d)"
mkdir -p "$SMOKE_DIR/claude-config" "$SMOKE_DIR/codex-home" "$SMOKE_DIR/home"
export CLAUDE_CONFIG_DIR="$SMOKE_DIR/claude-config"
export CODEX_HOME="$SMOKE_DIR/codex-home"
export HOME="$SMOKE_DIR/home"
cat > "$SMOKE_DIR/settings.json" <<'EOF'
{}
EOF
```

Confirm `$CLAUDE_CONFIG_DIR`/`$CODEX_HOME`/`$HOME` all point inside `$SMOKE_DIR` before running anything — never against the real `~/.claude*` or `~/.codex`.

### Step 3: Marketplace add round-trip

```bash
/tmp/omni-plugin-smoke --config "$SMOKE_DIR/settings.json" agents plugins marketplace add caveman --source lkshrk/agent-marketplace --agents claude-code
/tmp/omni-plugin-smoke --config "$SMOKE_DIR/settings.json" agents plugins marketplace list
```
Expected: `added caveman`, then `caveman  lkshrk/agent-marketplace` printed by `list`. Confirm (via `claude plugins marketplace list` run directly, same env) that the marketplace actually landed in claude's own state, not just the manifest.

### Step 4: Plugin add/list round-trip

```bash
/tmp/omni-plugin-smoke --config "$SMOKE_DIR/settings.json" agents plugins add --name caveman --marketplace caveman --agents claude-code
/tmp/omni-plugin-smoke --config "$SMOKE_DIR/settings.json" agents plugins list
```
Expected: `added caveman`, no `! claude-code/caveman: ...` error lines, and `list` shows `caveman  caveman  claude-code(✓)`. Cross-check with `claude plugins list --json` run directly in the same sandboxed env to confirm the plugin is really installed, not just recorded in the manifest.

### Step 5: Remove round-trip

```bash
/tmp/omni-plugin-smoke --config "$SMOKE_DIR/settings.json" agents plugins remove caveman
/tmp/omni-plugin-smoke --config "$SMOKE_DIR/settings.json" agents plugins list
```
Expected: `removed caveman`, no error lines, plugin no longer in `list`'s managed section, and `claude plugins list --json` run directly confirms it is actually uninstalled. Confirm the marketplace is still present in `agents plugins marketplace list` (marketplaces are never removed by plugin removal).

### Step 6: Import round-trip

```bash
claude plugins install caveman@caveman   # hand-install, bypassing omni entirely
/tmp/omni-plugin-smoke --config "$SMOKE_DIR/settings.json" agents plugins import
/tmp/omni-plugin-smoke --config "$SMOKE_DIR/settings.json" agents plugins import caveman
/tmp/omni-plugin-smoke --config "$SMOKE_DIR/settings.json" agents plugins list
```
Expected: the no-arg `import` lists `caveman` under `-- unmanaged (claude-code) --`; `import caveman` prints `imported caveman`; the subsequent `list` shows it as a managed row with `claude-code(✓)`.

### Step 7: Marketplace remove leaves agent state untouched

```bash
/tmp/omni-plugin-smoke --config "$SMOKE_DIR/settings.json" agents plugins remove caveman
/tmp/omni-plugin-smoke --config "$SMOKE_DIR/settings.json" agents plugins marketplace remove caveman
claude plugins marketplace list
```
Expected: `claude plugins marketplace list` run directly still shows `caveman` — confirming omni's `marketplace remove` only touched the manifest.

### Step 8: Codex round-trip

Repeat Steps 3-5 targeted at `--agents codex` instead of `--agents claude-code`, cross-checking with `codex plugin list`/`codex plugin marketplace list` run directly, using whatever exact subcommand names Task 1's probe and Task 3's adapter settled on.

### Step 9: Tear down the sandbox

```bash
rm -rf "$SMOKE_DIR"
```
Never leave a smoke-test sandbox pointed at env vars that could leak into a later shell session — unset `CLAUDE_CONFIG_DIR`/`CODEX_HOME`/`HOME` overrides (or close the shell) once done.

### Step 10: Record the result

If every step above matched its expected output, this task passes with no code changes — proceed to Task 10. If any step diverged (wrong flag shape, parser mismatch, marketplace-ordering bug, etc.), open a fix in the relevant earlier task's files, re-run that task's own unit tests, then re-run this entire Task 9 from Step 2 before proceeding. Do not commit anything for this task itself unless a fix was needed elsewhere, in which case that fix gets its own Conventional Commit in the task it belongs to (e.g. `fix(app): correct codex plugin remove flag order`).

---

## Task 10: Final Review + Full Test Suites

**Files:** none (verification-only task; may produce fix-up commits in whichever task's files needed correction)

### Step 1: Run the full unit test suite

Run: `go test ./... -v`
Expected: PASS across every package, including `internal/config`, `internal/app`, `internal/cli`, `internal/tui`.

### Step 2: Run the full integration suite

Run: `make test-integration` (or `make test-all` if that is the documented combined target — check the `Makefile`'s `.PHONY` line for the exact name)
Expected: PASS, including all 5 new `agents-plugins-*.txtar` fixtures from Task 8 and all pre-existing `agents-mcp-*.txtar` fixtures (regression check).

### Step 3: Run `go vet` and any configured linter

Run: `go vet ./...` and, if the project has one configured (check `Makefile`'s `lint` target), the linter command it runs.
Expected: clean.

### Step 4: Confirm no `_ =` silent error discards were introduced

Run: `git diff main --name-only -- internal/config internal/app internal/cli internal/tui | xargs grep -n '_ = ' ` (adjust the base ref to whatever this branch was created from)
Expected: any hits are either `defer f.Close()`-style cleanup or carry an explicit one-line comment explaining why the error is intentionally discarded. Fix any that don't.

### Step 5: Spec coverage self-check

Re-read `docs/superpowers/specs/2026-07-02-plugin-management-design.md` section by section and confirm each decision has a corresponding implemented task:
- Decisions table → Tasks 1-7 (scope, write-target agents, delegate-to-CLI mechanism, no version pinning, unmanaged-detection/import, group mirroring, enable/disable out of scope).
- Ground truth / re-verification requirement → Task 1 (probe) + Tasks 2/3 (fixtures built from it) + Task 9 (live smoke).
- Manifest schema → Task 1.
- Adapter interface → Tasks 2/3.
- Operations → Task 4.
- CLI → Task 5.
- TUI → Tasks 6/7.
- Error handling rules → threaded through Tasks 4/5/6/7 (`PluginError`, tolerant batch loops, idempotent removes, no `_ =` discards — reconfirmed in Step 4 above).
- Testing section (unit/TUI/integration/live smoke) → Tasks 1-9.

If any spec line lacks a corresponding task, stop and add a task before merging — do not silently ship a gap.

### Step 6: Merge decision

Explicitly out of scope for this task: whether/when to merge this branch to `main` is the user's decision, not an automatic step of this plan. Do not open a PR or merge as part of executing this plan unless separately instructed.

