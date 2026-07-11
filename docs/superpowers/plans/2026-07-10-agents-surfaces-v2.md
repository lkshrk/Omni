# Agents Surfaces v2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Per-host feature toggles for skills/mcp/plugins, a stacked "all" default view on the Agents tab, and an agents health block in doctor — per `docs/superpowers/specs/2026-07-02-agents-surfaces-v2-design.md`.

**Architecture:** Three new `*bool` host-settings flags mirror `agents_disabled` (schema v11→v12). App layer grows per-feature guards wrapping `requireAgentsEnabled` and per-feature `SaveXDisabled` persistence. TUI Agents tab gains chip index 0 = `all` composing the three existing sections through one flatten function that feeds both render and key dispatch. Doctor gains one `agents` check built from cheap probes only (LookPath + manifest + disk), never adapter `List()`.

**Tech Stack:** Go, Bubbletea (charm.land v2 fork), go-sqlite3, rogpeppe/go-internal testscript.

## Global Constraints

- Comments: almost none — only non-obvious WHY. No step narration, no restating code, no task references.
- Error handling: never `_ =` discard (CLAUDE.md exceptions only).
- TUI tests MUST be written by the **tui-tester** agent; txtar fixtures MUST be written by the **txtar-writer** agent. Never inline.
- Unit tests for a package: `go test ./internal/<pkg>/`. Full suite: `make test` (needs sandbox escalation via `scripts/run-test-safe.sh` if provider `httptest` listeners are blocked).
- Conventional Commits; one commit per task, immediately when green.
- Guard error strings (exact, used by CLI/tests): master `"agent skills are disabled for this host"` (existing, unchanged); new: `"skills are disabled for this host"`, `"mcp servers are disabled for this host"`, `"plugins are disabled for this host"`.
- Chip constants (used across Tasks 5–6): `agentsChipAll=0, agentsChipSkills=1, agentsChipMcp=2, agentsChipPlugin=3`.

## Known bug fixed by Task 2

`SaveAgentsDisabled` never persists: `hostSettingsPatch` (internal/app/settings_persist.go:42) omits `AgentsDisabled`, and `patchCurrentHostSettings` rebuilds the entire top-level `host_settings` key from `hostSettingsPatchDoc`, so the flag is dropped on write and any hand-written `agents_disabled` is erased by any host-settings save (including `SaveSettings`). Verified by failing round-trip test on main. Logged as `.wolf/buglog.json` bug (silent-data-loss). Task 2 fixes it and adds the three new flags to the same struct.

---

### Task 1: Config — three per-host flags + schema v12

**Files:**
- Modify: `internal/config/config.go` (Settings struct ~line 260; `CurrentVersion` line 13; `EffectiveSettings` override block lines 515–522)
- Modify: `internal/config/loader.go` (`configMigrations` registry lines 245–257; migration funcs after line 861)
- Modify: `internal/config/config_test.go` (`TestSettings_JSONTagsRemainStableAcrossUILabelRenames` line 868)
- Modify: `internal/config/effective_settings_test.go`
- Create: `spec/omni.settings.v12.schema.json` (generated)
- Modify: `spec/omni.settings.schema.json` (generated)
- Modify: `docs/schema-reference.md`

**Interfaces:**
- Produces: `Settings.SkillsDisabled/McpDisabled/PluginsDisabled *bool` with JSON tags `skills_disabled`/`mcp_disabled`/`plugins_disabled`; `CurrentVersion == 12`. Later tasks resolve them via `EffectiveSettings` + `config.BoolVal`.

- [ ] **Step 1: Write failing tests**

In `internal/config/effective_settings_test.go`, mirror the existing `DotsDisabled` host-override test (read the file first; copy its shape) with a new test:

```go
func TestEffectiveSettings_AgentFeatureFlagsHostOverride(t *testing.T) {
	tr := BoolPtr(true)
	cfg := &RootConfig{
		Settings: Settings{SkillsDisabled: BoolPtr(false)},
		HostSettings: map[string]Settings{
			"h1": {SkillsDisabled: tr, McpDisabled: tr, PluginsDisabled: tr},
		},
	}
	s := cfg.EffectiveSettings("h1")
	if !BoolVal(s.SkillsDisabled) || !BoolVal(s.McpDisabled) || !BoolVal(s.PluginsDisabled) {
		t.Errorf("host overrides not applied: %+v", s)
	}
	other := cfg.EffectiveSettings("other")
	if BoolVal(other.SkillsDisabled) || other.McpDisabled != nil || other.PluginsDisabled != nil {
		t.Errorf("non-matching host must keep globals: %+v", other)
	}
}
```

(Adapt the `EffectiveSettings` call signature to the real one in config.go:491 — if it takes no hostname or a different form, follow the existing DotsDisabled test's invocation exactly.)

In `config_test.go:868` `TestSettings_JSONTagsRemainStableAcrossUILabelRenames`, add to the checked-keys list: `"agents_disabled"`, `"skills_disabled"`, `"mcp_disabled"`, `"plugins_disabled"`.

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/config/ -run 'TestEffectiveSettings_AgentFeatureFlags|TestSettings_JSONTags'`
Expected: FAIL (unknown fields).

- [ ] **Step 3: Add fields + resolution + migration**

`config.go` Settings struct, directly after `AgentsDisabled` (line ~262), same comment style as that field:

```go
	// SkillsDisabled / McpDisabled / PluginsDisabled turn one agent feature
	// off on this machine; agents_disabled remains the master switch.
	SkillsDisabled  *bool `json:"skills_disabled,omitempty"`
	McpDisabled     *bool `json:"mcp_disabled,omitempty"`
	PluginsDisabled *bool `json:"plugins_disabled,omitempty"`
```

`EffectiveSettings` override block (beside the `AgentsDisabled` case, lines 515–522):

```go
	if hs.SkillsDisabled != nil {
		s.SkillsDisabled = hs.SkillsDisabled
	}
	if hs.McpDisabled != nil {
		s.McpDisabled = hs.McpDisabled
	}
	if hs.PluginsDisabled != nil {
		s.PluginsDisabled = hs.PluginsDisabled
	}
```

`config.go:13`: `CurrentVersion = 12`.

`loader.go`: register in `configMigrations` (lines 245–257) a new final entry mirroring the v10→v11 entry exactly, and add after `migrateRawConfigV10ToV11` (line 861):

```go
func migrateConfigV11ToV12(cfg *RootConfig) error {
	cfg.Version = 12
	return nil
}

func migrateRawConfigV11ToV12(raw map[string]json.RawMessage) error {
	raw["version"] = json.RawMessage(`12`)
	return nil
}
```

- [ ] **Step 4: Regen schema**

Run: `make gen-schema`
Expected: creates `spec/omni.settings.v12.schema.json`, rewrites `spec/omni.settings.schema.json`. (Fails if `CurrentVersion` not bumped first.)

- [ ] **Step 5: Run tests**

Run: `go test ./internal/config/`
Expected: PASS, including migration chain tests (`migration_test.go` covers the new step automatically).

- [ ] **Step 6: Update docs/schema-reference.md**

Update the stated current version (stale "v9") to v12 and document the three new `host_settings` keys beside `agents_disabled` (nil = enabled by default; `agents_disabled` masters all three).

- [ ] **Step 7: Commit**

```bash
git add internal/config spec docs/schema-reference.md
git commit -m "feat(config): per-host skills/mcp/plugins disable flags, schema v12"
```

---

### Task 2: App — host-settings patch fix + per-feature Save/guards

**Files:**
- Modify: `internal/app/settings_persist.go` (`hostSettingsPatch` line 42; `hostSettingsPatchDoc` line 50)
- Modify: `internal/app/agents_enable.go`
- Create: `internal/app/settings_persist_agents_test.go`
- Modify: `internal/app/agents_enable_test.go`

**Interfaces:**
- Consumes: Task 1 flags.
- Produces (later tasks depend on these exact names):
  - `func (a *App) SkillsEnabled(cfg *config.RootConfig) bool` (and `McpEnabled`, `PluginsEnabled`) — feature flag AND master.
  - `func (a *App) requireSkillsEnabled(cfg *config.RootConfig) error` (and `requireMcpEnabled`, `requirePluginsEnabled`).
  - `func (a *App) SaveSkillsDisabled(_ context.Context, disabled bool) error` (and `SaveMcpDisabled`, `SavePluginsDisabled`).

- [ ] **Step 1: Write failing persistence regression test**

`internal/app/settings_persist_agents_test.go` (package `app_test`; mirror `newMcpTestApp` setup from `mcp_ops_test.go:41`):

```go
package app_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func newPersistTestApp(t *testing.T) (*app.App, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	root := config.RootConfig{Version: config.CurrentVersion}
	if err := config.Save(cfgPath, &root); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath)
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a, cfgPath
}

func TestSaveAgentFeatureFlags_PersistToHostSettings(t *testing.T) {
	a, cfgPath := newPersistTestApp(t)
	ctx := context.Background()
	for name, save := range map[string]func(context.Context, bool) error{
		"agents_disabled":  a.SaveAgentsDisabled,
		"skills_disabled":  a.SaveSkillsDisabled,
		"mcp_disabled":     a.SaveMcpDisabled,
		"plugins_disabled": a.SavePluginsDisabled,
	} {
		if err := save(ctx, true); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		data, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), name) {
			t.Errorf("%s not persisted", name)
		}
	}
}

func TestSaveSettings_PreservesAgentFeatureFlags(t *testing.T) {
	a, cfgPath := newPersistTestApp(t)
	ctx := context.Background()
	if err := a.SaveAgentsDisabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveSettings(ctx, config.Settings{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "agents_disabled") {
		t.Error("SaveSettings erased agents_disabled from host_settings")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/app/ -run 'TestSaveAgentFeatureFlags|TestSaveSettings_Preserves'`
Expected: FAIL — compile error (SaveSkillsDisabled undefined); after stubbing, `agents_disabled not persisted`.

- [ ] **Step 3: Fix hostSettingsPatch + add Save funcs**

`settings_persist.go:42` — extend struct:

```go
type hostSettingsPatch struct {
	Ecosystems        map[string]config.EcosystemSettings `json:"ecosystems,omitempty"`
	DotsRepo          string                              `json:"dots_repo,omitempty"`
	DotsDisabled      *bool                               `json:"dots_disabled,omitempty"`
	DisabledProviders *[]string                           `json:"disabled_providers,omitempty"`
	ProviderPriority  []string                            `json:"provider_priority,omitempty"`
	AgentsDisabled    *bool                               `json:"agents_disabled,omitempty"`
	SkillsDisabled    *bool                               `json:"skills_disabled,omitempty"`
	McpDisabled       *bool                               `json:"mcp_disabled,omitempty"`
	PluginsDisabled   *bool                               `json:"plugins_disabled,omitempty"`
}
```

`hostSettingsPatchDoc` (line 50) — clone the four pointers beside the existing `DotsDisabled` clone:

```go
		patch.AgentsDisabled = cloneBoolPtr(settings.AgentsDisabled)
		patch.SkillsDisabled = cloneBoolPtr(settings.SkillsDisabled)
		patch.McpDisabled = cloneBoolPtr(settings.McpDisabled)
		patch.PluginsDisabled = cloneBoolPtr(settings.PluginsDisabled)
```

Add helper (and refactor the existing `DotsDisabled` clone to use it):

```go
func cloneBoolPtr(v *bool) *bool {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}
```

`agents_enable.go` — append (mirror `SaveAgentsDisabled` at lines 24–30):

```go
func (a *App) SkillsEnabled(cfg *config.RootConfig) bool {
	s := a.effectiveSettings(cfg)
	return !config.BoolVal(s.AgentsDisabled) && !config.BoolVal(s.SkillsDisabled)
}

func (a *App) McpEnabled(cfg *config.RootConfig) bool {
	s := a.effectiveSettings(cfg)
	return !config.BoolVal(s.AgentsDisabled) && !config.BoolVal(s.McpDisabled)
}

func (a *App) PluginsEnabled(cfg *config.RootConfig) bool {
	s := a.effectiveSettings(cfg)
	return !config.BoolVal(s.AgentsDisabled) && !config.BoolVal(s.PluginsDisabled)
}

func (a *App) requireSkillsEnabled(cfg *config.RootConfig) error {
	if err := a.requireAgentsEnabled(cfg); err != nil {
		return err
	}
	if config.BoolVal(a.effectiveSettings(cfg).SkillsDisabled) {
		return fmt.Errorf("skills are disabled for this host")
	}
	return nil
}

func (a *App) requireMcpEnabled(cfg *config.RootConfig) error {
	if err := a.requireAgentsEnabled(cfg); err != nil {
		return err
	}
	if config.BoolVal(a.effectiveSettings(cfg).McpDisabled) {
		return fmt.Errorf("mcp servers are disabled for this host")
	}
	return nil
}

func (a *App) requirePluginsEnabled(cfg *config.RootConfig) error {
	if err := a.requireAgentsEnabled(cfg); err != nil {
		return err
	}
	if config.BoolVal(a.effectiveSettings(cfg).PluginsDisabled) {
		return fmt.Errorf("plugins are disabled for this host")
	}
	return nil
}

func (a *App) SaveSkillsDisabled(_ context.Context, disabled bool) error {
	return a.patchCurrentHostSettings(func(hs *config.Settings) error {
		hs.SkillsDisabled = config.BoolPtr(disabled)
		return nil
	})
}

func (a *App) SaveMcpDisabled(_ context.Context, disabled bool) error {
	return a.patchCurrentHostSettings(func(hs *config.Settings) error {
		hs.McpDisabled = config.BoolPtr(disabled)
		return nil
	})
}

func (a *App) SavePluginsDisabled(_ context.Context, disabled bool) error {
	return a.patchCurrentHostSettings(func(hs *config.Settings) error {
		hs.PluginsDisabled = config.BoolPtr(disabled)
		return nil
	})
}
```

- [ ] **Step 4: Guard unit tests**

Extend `agents_enable_test.go` with one test per guard, same three-case shape as `TestAgentsEnabledDefaultAndDisabled` (line 10) plus the master case:

```go
func TestSkillsEnabledMatrix(t *testing.T) {
	a := &App{}
	if !a.SkillsEnabled(&config.RootConfig{}) {
		t.Error("skills must be enabled by default")
	}
	flagOff := &config.RootConfig{Settings: config.Settings{SkillsDisabled: config.BoolPtr(true)}}
	if a.SkillsEnabled(flagOff) {
		t.Error("skills must be disabled when skills_disabled=true")
	}
	if err := a.requireSkillsEnabled(flagOff); err == nil || !strings.Contains(err.Error(), "skills are disabled") {
		t.Errorf("requireSkillsEnabled = %v, want skills-disabled error", err)
	}
	masterOff := &config.RootConfig{Settings: config.Settings{AgentsDisabled: config.BoolPtr(true)}}
	if a.SkillsEnabled(masterOff) {
		t.Error("master off must disable skills")
	}
	if err := a.requireSkillsEnabled(masterOff); err == nil || !strings.Contains(err.Error(), "agent skills are disabled") {
		t.Errorf("master-off error = %v, want master message", err)
	}
	explicit := &config.RootConfig{Settings: config.Settings{SkillsDisabled: config.BoolPtr(false)}}
	if err := a.requireSkillsEnabled(explicit); err != nil {
		t.Errorf("explicit false = %v, want nil", err)
	}
}
```

Repeat as `TestMcpEnabledMatrix` (flag `McpDisabled`, message `"mcp servers are disabled"`) and `TestPluginsEnabledMatrix` (flag `PluginsDisabled`, message `"plugins are disabled"`). Note: this test file is package `app` (internal) — check the existing file's package clause and match it.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/app/ -run 'TestSaveAgentFeatureFlags|TestSaveSettings_Preserves|TestSkillsEnabledMatrix|TestMcpEnabledMatrix|TestPluginsEnabledMatrix'`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/app
git commit -m "fix(app): persist agents_disabled in host-settings patch; add per-feature flags"
```

---

### Task 3: App — wire guards into ops + restore skip-with-warning

**Files:**
- Modify: `internal/app/agents_add.go:34`, `internal/app/skill_find.go:41`, `internal/app/agents_skills.go` (lines 66–71 result struct, 135, 230, 319), `internal/app/mcp_ops.go` (108, 171, 201, 234, 287), `internal/app/plugin_ops.go` (134, 202, 238, 272, 305, 324, 385)
- Modify: `internal/cli/agents.go` (skills restore output, ~line 104–120)
- Create: `internal/app/agents_feature_gate_test.go`

**Interfaces:**
- Consumes: Task 2 guards.
- Produces: `RestoreSkillsResult.Warnings []string` (new field). Restore semantics: master disabled → hard error (unchanged); feature flag disabled → skip, warning in result, nil error.

- [ ] **Step 1: Write failing gating tests**

`internal/app/agents_feature_gate_test.go` (package `app_test`, reuse `newMcpTestApp` from `mcp_ops_test.go:41` and the plugin equivalent from `plugin_ops_test.go`; for skills use the same save-config + `app.New` + `InitTestMode` shape). Global `Settings` flags flow through `effectiveSettings`, so set them on `RootConfig.Settings` — extend the test-app helpers with a variant accepting `config.Settings` if needed:

```go
func TestOpsGatedByFeatureFlags(t *testing.T) {
	ctx := context.Background()

	t.Run("skills add", func(t *testing.T) {
		a := newFeatureGateApp(t, config.Settings{SkillsDisabled: config.BoolPtr(true)})
		_, err := a.AddSkillPackage(ctx, "owner/repo")
		wantDisabledErr(t, err, "skills are disabled")
	})
	t.Run("skills find", func(t *testing.T) {
		a := newFeatureGateApp(t, config.Settings{SkillsDisabled: config.BoolPtr(true)})
		_, err := a.FindSkillPackages(ctx, "q")
		wantDisabledErr(t, err, "skills are disabled")
	})
	t.Run("mcp add", func(t *testing.T) {
		a := newFeatureGateApp(t, config.Settings{McpDisabled: config.BoolPtr(true)})
		_, err := a.AddMcpServer(ctx, config.McpServer{Name: "x", Command: "y"})
		wantDisabledErr(t, err, "mcp servers are disabled")
	})
	t.Run("plugins add", func(t *testing.T) {
		a := newFeatureGateApp(t, config.Settings{PluginsDisabled: config.BoolPtr(true)})
		_, err := a.AddPlugin(ctx, config.Plugin{Name: "x"})
		wantDisabledErr(t, err, "plugins are disabled")
	})
}

func TestRestoreSkipsDisabledFeatureWithWarning(t *testing.T) {
	ctx := context.Background()

	t.Run("skills", func(t *testing.T) {
		a := newFeatureGateApp(t, config.Settings{SkillsDisabled: config.BoolPtr(true)})
		res, _, err := a.RestoreSkills(ctx, app.RestoreSkillsOptions{})
		if err != nil {
			t.Fatalf("want skip not error, got %v", err)
		}
		wantWarning(t, res.Warnings, "skills are disabled")
	})
	t.Run("mcp", func(t *testing.T) {
		a := newFeatureGateApp(t, config.Settings{McpDisabled: config.BoolPtr(true)})
		res, err := a.RestoreMcpServers(ctx, app.RestoreMcpOptions{})
		if err != nil {
			t.Fatalf("want skip not error, got %v", err)
		}
		wantWarning(t, res.Warnings, "mcp servers are disabled")
	})
	t.Run("plugins", func(t *testing.T) {
		a := newFeatureGateApp(t, config.Settings{PluginsDisabled: config.BoolPtr(true)})
		res, err := a.RestorePlugins(ctx, app.RestorePluginOptions{})
		if err != nil {
			t.Fatalf("want skip not error, got %v", err)
		}
		wantWarning(t, res.Warnings, "plugins are disabled")
	})
	t.Run("master still hard error", func(t *testing.T) {
		a := newFeatureGateApp(t, config.Settings{AgentsDisabled: config.BoolPtr(true)})
		_, err := a.RestoreMcpServers(ctx, app.RestoreMcpOptions{})
		wantDisabledErr(t, err, "agent skills are disabled")
	})
}
```

Helpers in the same file:

```go
func newFeatureGateApp(t *testing.T, s config.Settings) *app.App {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	root := config.RootConfig{Version: config.CurrentVersion, Settings: s}
	if err := config.Save(cfgPath, &root); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath)
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func wantDisabledErr(t *testing.T, err error, substr string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), substr) {
		t.Errorf("err = %v, want containing %q", err, substr)
	}
}

func wantWarning(t *testing.T, warnings []string, substr string) {
	t.Helper()
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return
		}
	}
	t.Errorf("warnings %v missing %q", warnings, substr)
}
```

(Adjust `RestoreSkills` return arity to the real signature `(RestoreSkillsResult, []string, error)` at agents_skills.go:130.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/app/ -run 'TestOpsGatedByFeatureFlags|TestRestoreSkipsDisabled'`
Expected: FAIL — no `Warnings` field on `RestoreSkillsResult`; gating tests fail with wrong/nil errors.

- [ ] **Step 3: Wire guards**

Mechanical swap of `a.requireAgentsEnabled(cfg)` → per-feature guard at these exact sites (leave every other line untouched):

| File:line | Method | New guard |
|---|---|---|
| agents_add.go:34 | AddSkillPackage | requireSkillsEnabled |
| skill_find.go:41 | FindSkillPackages | requireSkillsEnabled |
| agents_skills.go:230 | ImportSkills | requireSkillsEnabled |
| agents_skills.go:319 | UpdateSkills | requireSkillsEnabled |
| mcp_ops.go:171 | AddMcpServer | requireMcpEnabled |
| mcp_ops.go:201 | RemoveMcpServer | requireMcpEnabled |
| mcp_ops.go:234 | SetMcpServerAgents | requireMcpEnabled |
| mcp_ops.go:287 | ImportMcpServers | requireMcpEnabled |
| plugin_ops.go:202 | AddPlugin | requirePluginsEnabled |
| plugin_ops.go:238 | AddMarketplace | requirePluginsEnabled |
| plugin_ops.go:272 | RemovePlugin | requirePluginsEnabled |
| plugin_ops.go:305 | RemoveMarketplace | requirePluginsEnabled |
| plugin_ops.go:324 | SetPluginAgents | requirePluginsEnabled |
| plugin_ops.go:385 | ImportPlugins | requirePluginsEnabled |

Restore methods keep `requireAgentsEnabled` and add the feature-flag skip directly after it:

`agents_skills.go` — add `Warnings []string` to `RestoreSkillsResult` (lines 66–71), then in `RestoreSkills` after the guard at line 135:

```go
	if config.BoolVal(a.effectiveSettings(cfg).SkillsDisabled) {
		return RestoreSkillsResult{Warnings: []string{"skills are disabled for this host, skipping restore"}}, nil, nil
	}
```

`mcp_ops.go` in `RestoreMcpServers` after line 108's guard:

```go
	if config.BoolVal(a.effectiveSettings(cfg).McpDisabled) {
		return RestoreMcpResult{Warnings: []string{"mcp servers are disabled for this host, skipping restore"}}, nil
	}
```

`plugin_ops.go` in `RestorePlugins` after line 134's guard:

```go
	if config.BoolVal(a.effectiveSettings(cfg).PluginsDisabled) {
		return RestorePluginResult{Warnings: []string{"plugins are disabled for this host, skipping restore"}}, nil
	}
```

`internal/cli/agents.go` skills-restore command (~line 104–120): print warnings before the summary, matching the mcp/plugins restore style already at lines 320/550:

```go
	for _, w := range res.Warnings {
		fmt.Fprintf(cmd.OutOrStdout(), "warn: %s\n", w)
	}
```

(Match the exact writer the surrounding code uses — if it uses `cmd.Printf`, use that.)

- [ ] **Step 4: Run tests**

Run: `go test ./internal/app/ ./internal/cli/`
Expected: PASS, including all pre-existing ops tests (they use nil flags → enabled).

- [ ] **Step 5: Commit**

```bash
git add internal/app internal/cli
git commit -m "feat(app): per-feature guards gate agent ops; restore skips disabled features"
```

---

### Task 4: Doctor — agents health check

**Files:**
- Create: `internal/app/doctor_agents.go`
- Modify: `internal/app/doctor.go` (`Doctor()` lines 59–72)
- Create: `internal/app/doctor_agents_test.go`

**Interfaces:**
- Consumes: Task 2 `SkillsEnabled/McpEnabled/PluginsEnabled`; existing `skillRunner`/`nodeManager` (agents_skills.go:21–26, 125–127), `SkillPackageRows` (agents_skills_rows.go:120), `a.mcpAdapters()` (mcp_ops.go:47), `a.pluginAdapters()` (plugin_ops.go:42), `DoctorCheck`/`DoctorDetailGroup`/`addCheck` (doctor.go).
- Produces: check ID `"agents"`, label `"Agent features"`, one `DoctorDetailGroup` per feature.

**Probe policy (deliberate deviations from the design doc, per its own no-slow-probes constraint):** never call adapter `List()`/`ListPlugins()` — `claude mcp list` health-checks every server, and doctor runs synchronously in one `tea.Cmd`. So: skills installed/missing from `SkillPackageRows` (disk-only, cheap); mcp/plugins report adapter `Available()` (LookPath only) + manifest counts, with no installed/unmanaged probing. Unmanaged counts omitted (need `List()`; design hedges "if cheaply available" — they are not).

- [ ] **Step 1: Write failing tests**

`internal/app/doctor_agents_test.go`, package matching `doctor_test.go`, reusing its `doctorCheck(result, id)` helper (doctor_test.go:223) and config-save pattern (`saveAppConfig` / `newImportApp` — read doctor_test.go first and mirror its app construction):

```go
func TestDoctorAgents_MasterDisabled(t *testing.T) {
	a := newFeatureGateApp(t, config.Settings{AgentsDisabled: config.BoolPtr(true)})
	result, err := a.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check := doctorCheck(result, "agents")
	if check == nil {
		t.Fatal("agents check missing")
	}
	if check.Status != app.DoctorStatusOK || !strings.Contains(check.Message, "agents_disabled") {
		t.Errorf("check = %+v, want ok + disabled message", check)
	}
	if len(check.Groups) != 0 {
		t.Errorf("disabled master must not probe features, got groups %+v", check.Groups)
	}
}

func TestDoctorAgents_FeatureDisabledSingleLine(t *testing.T) {
	a := newFeatureGateApp(t, config.Settings{McpDisabled: config.BoolPtr(true)})
	result, err := a.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check := doctorCheck(result, "agents")
	var mcpGroup *app.DoctorDetailGroup
	for i := range check.Groups {
		if check.Groups[i].Header == "mcp servers" {
			mcpGroup = &check.Groups[i]
		}
	}
	if mcpGroup == nil || len(mcpGroup.Items) != 1 || !strings.Contains(mcpGroup.Items[0], "disabled (mcp_disabled)") {
		t.Errorf("mcp group = %+v, want single disabled line", mcpGroup)
	}
}

func TestDoctorAgents_AdapterUnavailableWarns(t *testing.T) {
	stub := &stubMcpAdapter{id: "codex", available: false}
	a := newFeatureGateApp(t, config.Settings{}, app.WithMcpAdapters([]app.McpAdapter{stub}))
	result, err := a.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	check := doctorCheck(result, "agents")
	if check.Status != app.DoctorStatusWarn {
		t.Errorf("status = %s, want warn for unavailable adapter", check.Status)
	}
	found := false
	for _, g := range check.Groups {
		for _, item := range g.Items {
			if strings.Contains(item, "codex") && strings.Contains(item, "not found") {
				found = true
			}
		}
	}
	if !found {
		t.Error("missing 'codex ... not found' item")
	}
}
```

(`stubMcpAdapter` exists in `mcp_ops_test.go:13` — reuse; extend `newFeatureGateApp` from Task 3 to pass through `opts ...func(*app.App)`. Stub adapters make `List()` a test-visible call — add a `listCalls int` counter to the stub if absent and assert doctor made **zero** `List` calls.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/app/ -run TestDoctorAgents`
Expected: FAIL — `doctorCheck(result, "agents")` returns nil.

- [ ] **Step 3: Implement**

`internal/app/doctor_agents.go`:

```go
package app

import (
	"fmt"
	osExec "os/exec"

	"github.com/lkshrk/omni/internal/config"
)

func (a *App) doctorAgents(result *DoctorResult, cfg *config.RootConfig) {
	if !a.AgentsEnabled(cfg) {
		result.addCheck("agents", "Agent features", DoctorStatusOK, "disabled (agents_disabled)")
		return
	}
	check := DoctorCheck{ID: "agents", Label: "Agent features", Status: DoctorStatusOK}
	var summary []string
	appendGroup := func(name string, g DoctorDetailGroup, healthy bool) {
		check.Groups = append(check.Groups, g)
		state := "ok"
		if !healthy {
			state = "warn"
			check.Status = DoctorStatusWarn
		}
		summary = append(summary, name+" "+state)
	}

	g, healthy := a.doctorAgentsSkills(cfg)
	appendGroup("skills", g, healthy)
	g, healthy = a.doctorAgentsMcp(cfg)
	appendGroup("mcp", g, healthy)
	g, healthy = a.doctorAgentsPlugins(cfg)
	appendGroup("plugins", g, healthy)

	check.Message = strings.Join(summary, ", ")
	result.Checks = append(result.Checks, check)
}

func (a *App) doctorAgentsSkills(cfg *config.RootConfig) (DoctorDetailGroup, bool) {
	g := DoctorDetailGroup{Header: "skills"}
	if !a.SkillsEnabled(cfg) {
		g.Items = append(g.Items, "disabled (skills_disabled)")
		return g, true
	}
	healthy := true
	runner := skillRunner(nodeManager(cfg))
	if _, err := osExec.LookPath(runner); err != nil {
		g.Items = append(g.Items, fmt.Sprintf("runner %s: not found on PATH", runner))
		healthy = false
	} else {
		g.Items = append(g.Items, fmt.Sprintf("runner %s: ok", runner))
	}
	rows, err := a.SkillPackageRows()
	if err != nil {
		g.Items = append(g.Items, fmt.Sprintf("packages: %v", err))
		return g, false
	}
	installed := 0
	for _, r := range rows {
		if r.Installed {
			installed++
		}
	}
	missing := len(rows) - installed
	g.Items = append(g.Items, fmt.Sprintf("packages: %d in manifest, %d installed, %d missing", len(rows), installed, missing))
	if missing > 0 {
		healthy = false
	}
	return g, healthy
}

func (a *App) doctorAgentsMcp(cfg *config.RootConfig) (DoctorDetailGroup, bool) {
	g := DoctorDetailGroup{Header: "mcp servers"}
	if !a.McpEnabled(cfg) {
		g.Items = append(g.Items, "disabled (mcp_disabled)")
		return g, true
	}
	healthy := doctorAdapterItems(&g, adapterAvailability(a.mcpAdapters()))
	g.Items = append(g.Items, fmt.Sprintf("servers: %d in manifest", len(cfg.Agents.McpServers)))
	return g, healthy
}

func (a *App) doctorAgentsPlugins(cfg *config.RootConfig) (DoctorDetailGroup, bool) {
	g := DoctorDetailGroup{Header: "plugins"}
	if !a.PluginsEnabled(cfg) {
		g.Items = append(g.Items, "disabled (plugins_disabled)")
		return g, true
	}
	healthy := doctorAdapterItems(&g, adapterAvailability(a.pluginAdapters()))
	g.Items = append(g.Items, fmt.Sprintf("plugins: %d in manifest, marketplaces: %d", len(cfg.Agents.Plugins), len(cfg.Agents.Marketplaces)))
	return g, healthy
}

type adapterProbe struct {
	id        string
	available bool
}

func adapterAvailability[T interface {
	ID() string
	Available() bool
}](adapters []T) []adapterProbe {
	out := make([]adapterProbe, 0, len(adapters))
	for _, ad := range adapters {
		out = append(out, adapterProbe{id: ad.ID(), available: ad.Available()})
	}
	return out
}

func doctorAdapterItems(g *DoctorDetailGroup, probes []adapterProbe) bool {
	healthy := true
	for _, p := range probes {
		if p.available {
			g.Items = append(g.Items, fmt.Sprintf("agent %s: ok", p.id))
		} else {
			g.Items = append(g.Items, fmt.Sprintf("agent %s: binary not found on PATH", p.id))
			healthy = false
		}
	}
	return healthy
}
```

(Add `"strings"` to imports. Generic `adapterAvailability` works because `McpAdapter` and `PluginAdapter` both declare `ID()/Available()`; if the compiler rejects the constraint against the slice element types, fall back to two small loops.)

`doctor.go` `Doctor()` — inside the `if configOK` block, after `a.doctorDotsIgnorePatterns(result, cfg)`:

```go
		a.doctorAgents(result, cfg)
```

Field names `cfg.Agents.McpServers` / `cfg.Agents.Plugins` / `cfg.Agents.Marketplaces` / `cfg.Agents.Packages`: verify against `internal/config` (AgentsConfig) and adjust.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/app/ -run 'TestDoctor'`
Expected: PASS (new tests + all existing doctor tests — the new check must not break `doctor_test.go` expectations about check counts if any assert exact lists; fix such assertions by including `"agents"`).

- [ ] **Step 5: Commit**

```bash
git add internal/app
git commit -m "feat(app): doctor agents health block (enabled state, binaries, manifest counts)"
```

---

### Task 5: TUI — snapshot fields + Settings toggle rows

**Files:**
- Modify: `internal/app/app_snapshot.go` (fields ~line 43, populate ~line 184)
- Modify: `internal/tui/model.go` (fields beside `agentsEnabled`; defaults ~line 649; snapshot assign ~line 784)
- Modify: `internal/tui/update_setup.go` (snapshot assigns at lines 59, 102)
- Modify: `internal/tui/view_settings.go` (row consts ~line 33; `settingsRows` table ~line 175)
- Modify: `internal/tui/update_settings.go` (confirm list ~line 64; edit dispatch ~line 286)
- Modify: `internal/tui/commands_agents.go`, `internal/tui/messages.go`, `internal/tui/update.go` (~line 371), `internal/tui/view_hints.go` (~line 294)
- Test: delegated to **tui-tester**

**Interfaces:**
- Consumes: Task 2 `SaveSkillsDisabled/SaveMcpDisabled/SavePluginsDisabled`.
- Produces: Model fields `skillsEnabled/mcpEnabled/pluginsEnabled bool` holding **feature-flag state only** (master NOT folded in — avoids staleness when the master toggles); helpers `skillsSectionEnabled()/mcpSectionEnabled()/pluginsSectionEnabled()` combining with `m.agentsEnabled` (Task 6 consumes these).

- [ ] **Step 1: Snapshot fields (flag-only semantics)**

`app_snapshot.go` beside `AgentsEnabled bool` (line 43):

```go
	SkillsEnabled  bool
	McpEnabled     bool
	PluginsEnabled bool
```

Populate beside line 184 (flag-only, master intentionally not folded in — the TUI combines at use sites):

```go
		SkillsEnabled:  !config.BoolVal(a.effectiveSettings(cfg).SkillsDisabled),
		McpEnabled:     !config.BoolVal(a.effectiveSettings(cfg).McpDisabled),
		PluginsEnabled: !config.BoolVal(a.effectiveSettings(cfg).PluginsDisabled),
```

(Hoist `s := a.effectiveSettings(cfg)` if the surrounding code doesn't already have it.)

- [ ] **Step 2: Model fields + helpers**

`model.go`: add `skillsEnabled, mcpEnabled, pluginsEnabled bool` beside `agentsEnabled`; default all three `true` where `agentsEnabled: true` is set (~line 649); assign from snapshot where `agentsEnabled: snapshot.AgentsEnabled` is (~line 784). Mirror the same assignments at `update_setup.go:59` and `:102`.

Add helpers (in `model.go` or new `internal/tui/agents_all.go` — Task 6 creates that file; put them there if doing tasks in order is inconvenient, otherwise model.go is fine):

```go
func (m Model) skillsSectionEnabled() bool  { return m.agentsEnabled && m.skillsEnabled }
func (m Model) mcpSectionEnabled() bool     { return m.agentsEnabled && m.mcpEnabled }
func (m Model) pluginsSectionEnabled() bool { return m.agentsEnabled && m.pluginsEnabled }
```

- [ ] **Step 3: Settings rows**

`view_settings.go`: insert three consts directly after `settingsRowAgentsEnabled` in the iota block (line ~33): `settingsRowSkillsEnabled`, `settingsRowMcpEnabled`, `settingsRowPluginsEnabled`. Add `settingsRows` entries mirroring the `settingsRowAgentsEnabled` entry (line 175):

```go
	settingsRowSkillsEnabled: {
		label:   "Skills",
		section: "Agents",
		hint:    hintCtxSettingsAgents,
		valueFn: func(m Model) string { return settingsOnOff(m.palette, m.skillsEnabled) },
		helpFn: func(m Model) string {
			if m.skillsEnabled {
				return m.palette.styleHelp.Render("Disable skill-package management for this machine.")
			}
			return m.palette.styleHelp.Render("Re-enable skill-package management.")
		},
	},
	settingsRowMcpEnabled: {
		label:   "MCP Servers",
		section: "Agents",
		hint:    hintCtxSettingsAgents,
		valueFn: func(m Model) string { return settingsOnOff(m.palette, m.mcpEnabled) },
		helpFn: func(m Model) string {
			if m.mcpEnabled {
				return m.palette.styleHelp.Render("Disable MCP server management for this machine.")
			}
			return m.palette.styleHelp.Render("Re-enable MCP server management.")
		},
	},
	settingsRowPluginsEnabled: {
		label:   "Plugins",
		section: "Agents",
		hint:    hintCtxSettingsAgents,
		valueFn: func(m Model) string { return settingsOnOff(m.palette, m.pluginsEnabled) },
		helpFn: func(m Model) string {
			if m.pluginsEnabled {
				return m.palette.styleHelp.Render("Disable plugin management for this machine.")
			}
			return m.palette.styleHelp.Render("Re-enable plugin management.")
		},
	},
```

(Check `hintCtxSettingsAgents` copy at view_hints.go:294 — if its text is agents-master-specific, generalize the label there rather than adding three new hint contexts.)

- [ ] **Step 4: Toggle wiring**

`update_settings.go`: add the three consts to the `handleSettingsConfirmAction` case list (line 64 area) and to `handleSettingsEditAction` (line 286 area):

```go
	case settingsRowSkillsEnabled:
		*cmds = append(*cmds, m.doToggleSkillsFeature())
	case settingsRowMcpEnabled:
		*cmds = append(*cmds, m.doToggleMcpFeature())
	case settingsRowPluginsEnabled:
		*cmds = append(*cmds, m.doTogglePluginsFeature())
```

`commands_agents.go`, mirroring `doToggleAgents` (lines 42–52):

```go
func (m *Model) doToggleSkillsFeature() tea.Cmd {
	if m.app == nil {
		return nil
	}
	a, ctx := m.app, m.ctx
	disable := m.skillsEnabled
	return func() tea.Msg {
		err := a.SaveSkillsDisabled(ctx, disable)
		return skillsFeatureToggledMsg{enabled: !disable, err: err}
	}
}

func (m *Model) doToggleMcpFeature() tea.Cmd {
	if m.app == nil {
		return nil
	}
	a, ctx := m.app, m.ctx
	disable := m.mcpEnabled
	return func() tea.Msg {
		err := a.SaveMcpDisabled(ctx, disable)
		return mcpFeatureToggledMsg{enabled: !disable, err: err}
	}
}

func (m *Model) doTogglePluginsFeature() tea.Cmd {
	if m.app == nil {
		return nil
	}
	a, ctx := m.app, m.ctx
	disable := m.pluginsEnabled
	return func() tea.Msg {
		err := a.SavePluginsDisabled(ctx, disable)
		return pluginsFeatureToggledMsg{enabled: !disable, err: err}
	}
}
```

`messages.go`:

```go
type skillsFeatureToggledMsg struct {
	enabled bool
	err     error
}

type mcpFeatureToggledMsg struct {
	enabled bool
	err     error
}

type pluginsFeatureToggledMsg struct {
	enabled bool
	err     error
}
```

`update.go` beside the `agentsToggledMsg` handler (lines 371–382) — mirror its error handling exactly, set the flag field, and on re-enable force that section's data reload (mirror how `agentsToggledMsg` sets `m.skillsLoaded = false` + `m.loadSkillsManifestCmd()`; for mcp/plugins use the loaded-flags + load commands referenced at update_keys.go:337–346):

```go
	case skillsFeatureToggledMsg:
		// mirror agentsToggledMsg err path
		m.skillsEnabled = msg.enabled
		if msg.enabled && m.agentsEnabled {
			m.skillsLoaded = false
			cmds = append(cmds, m.loadSkillsManifestCmd())
		}
	case mcpFeatureToggledMsg:
		m.mcpEnabled = msg.enabled
		if msg.enabled && m.agentsEnabled {
			m.mcpLoaded = false
			cmds = append(cmds, /* mcp load cmd used at update_keys.go:341 */)
		}
	case pluginsFeatureToggledMsg:
		m.pluginsEnabled = msg.enabled
		if msg.enabled && m.agentsEnabled {
			m.pluginLoaded = false
			cmds = append(cmds, /* plugin load cmd used at update_keys.go:346 */)
		}
```

- [ ] **Step 5: Build + existing tests**

Run: `go build ./... && go test ./internal/tui/ ./internal/app/`
Expected: PASS.

- [ ] **Step 6: Delegate tests to tui-tester**

Dispatch **tui-tester** with: "Cover the new Settings-tab agent feature toggles: (a) the three rows render under the Agents section with on/off values from `m.skillsEnabled/mcpEnabled/pluginsEnabled`; (b) Enter on each row dispatches the toggle command and the resulting `xFeatureToggledMsg` flips the model field; (c) re-enable triggers the section reload cmd; (d) toggle msg with err does not flip the field (mirror agentsToggledMsg semantics). Use `baseModel` + `m.mode = viewSettings`, drive `Update()` per cerebrum rule (Update-driven, not helper-only)."

Run: `go test ./internal/tui/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/app internal/tui
git commit -m "feat(tui): per-feature agent toggles in Settings tab"
```

---

### Task 6: TUI — Agents tab "all" view

**Files:**
- Create: `internal/tui/agents_all.go`
- Modify: `internal/tui/model.go` (`skillTypeIdx` comment line 498; new `agentsAllCursor int`)
- Modify: `internal/tui/update_keys.go` (viewSkills case lines 150–260; chip-switch blocks at 197–206, 494–503, 573–582; tab-entry loads 337–346)
- Modify: `internal/tui/view_skills.go` (`viewSkillsBody` 125–163; skills section build 165–320; `renderMcpTab` 368; `renderPluginTab` 453)
- Modify: `internal/tui/view_list.go` (pill bar dim variant, beside `renderPillBarNoAll` line 175)
- Modify: `internal/tui/view_hints.go` (viewSkills hint dispatch)
- Test: delegated to **tui-tester**

**Interfaces:**
- Consumes: Task 5 `skillsSectionEnabled()/mcpSectionEnabled()/pluginsSectionEnabled()`; existing `skillsVisibleRows` (view_skills.go:61), `mcpTotalRows` (update_mcp.go:140), `pluginTotalRows` (update_plugin.go:114), `clampSkillsCursor/clampMcpCursor/clampPluginCursor`, `handleMcpKeyMsg` (update_keys.go:471), `handlePluginKeyMsg` (update_keys.go:550), `sectionedTab*` types (view_sectioned.go:5–23), `renderSectionedTab` (view_sectioned.go:25).
- Produces: chip consts `agentsChipAll/Skills/Mcp/Plugin` (see Global Constraints); `agentsAllRowsList(m Model) []agentsAllRow`; `handleAgentsAllKeyMsg`; `renderAgentsAllTab`; `handleSkillsKeyMsg` (extraction of the inline skills key block).

**Ordering within this task matters: A (mechanical remap) → B (extraction, no behavior change) → C (new all-view). Commit after each sub-stage if green.**

- [ ] **Step A1: Chip constants + index remap**

In `agents_all.go`:

```go
package tui

const (
	agentsChipAll = iota
	agentsChipSkills
	agentsChipMcp
	agentsChipPlugin
)
```

`model.go:498`: comment becomes `// 0=all, 1=skills, 2=mcp, 3=plugin`. Add field `agentsAllCursor int` beside `skillsCursor` (line 491 area).

Remap every `skillTypeIdx` literal comparison/assignment (16 sites in update_keys.go, 3 in view_skills.go — grep `skillTypeIdx`): `== 1` → `== agentsChipMcp`, `== 2` → `== agentsChipPlugin`, `0` (skills-meaning) → `agentsChipSkills`. Existing tests that set `m.skillTypeIdx = 1/2` (mcp_tab_test.go:52, plugin_tab_test.go:54, others via grep in `internal/tui/*_test.go`) get bumped to the new consts — do this in the same pass so the suite stays green.

- [ ] **Step A2: Centralized chip navigation with disabled-skip**

In `agents_all.go`:

```go
func agentsChipEnabled(m Model, chip int) bool {
	switch chip {
	case agentsChipSkills:
		return m.skillsSectionEnabled()
	case agentsChipMcp:
		return m.mcpSectionEnabled()
	case agentsChipPlugin:
		return m.pluginsSectionEnabled()
	default:
		return true
	}
}

func (m *Model) agentsChipMove(delta int) {
	for next := m.skillTypeIdx + delta; next >= agentsChipAll && next <= agentsChipPlugin; next += delta {
		if agentsChipEnabled(*m, next) {
			m.skillTypeIdx = next
			m.resetAgentsChipCursor()
			return
		}
	}
}

func (m *Model) resetAgentsChipCursor() {
	switch m.skillTypeIdx {
	case agentsChipAll:
		clampAgentsAllCursor(m)
	case agentsChipSkills:
		clampSkillsCursor(m)
	case agentsChipMcp:
		m.mcpCursor = 0
	case agentsChipPlugin:
		m.pluginCursor = 0
	}
}
```

Replace the three duplicated left/right chip-switch blocks (update_keys.go:197–206, 494–503, 573–582) with `m.agentsChipMove(-1)` / `m.agentsChipMove(1)`.

- [ ] **Step A3: Tab-entry default**

At the tab-switch site (update_keys.go ~337, where `target == viewSkills` triggers loads): set `m.skillTypeIdx = agentsChipAll` and `clampAgentsAllCursor(&m)` (adjust receiver form to context) so the tab always opens on `all`. Keep the three load-kick guards as-is but gate each on its section: `m.agentsEnabled && m.skillsEnabled && !m.skillsLoaded` etc. — a disabled feature must not load (design: disabled ⇒ no probing).

- [ ] **Step A4: Build + tests green**

Run: `go build ./... && go test ./internal/tui/`
Expected: PASS — pure remap so far, except tests that assumed tab-entry keeps the previous chip; fix those by setting `m.skillTypeIdx` explicitly in their arrange step.

- [ ] **Step B1: Extract `handleSkillsKeyMsg`**

Move the skills-chip `switch msg.String()` block from `handleKeyPressMsg`'s `case viewSkills:` (update_keys.go:150–260, the code that runs when the chip is skills) into:

```go
func (m *Model) handleSkillsKeyMsg(msg tea.KeyPressMsg) []tea.Cmd
```

No behavior change; the case body becomes chip dispatch only (popup/search early-outs stay in the case body — they must keep running before any chip dispatch, including the future all-view):

```go
	switch m.skillTypeIdx {
	case agentsChipMcp:
		return m.handleMcpKeyMsg(msg)   // adjust to the existing call/append pattern
	case agentsChipPlugin:
		return m.handlePluginKeyMsg(msg)
	default:
		return m.handleSkillsKeyMsg(msg)
	}
```

- [ ] **Step B2: Extract parameterized section builders**

From `renderMcpTab` (view_skills.go:368): extract the row-building into

```go
func mcpSections(m Model, p palette, cursor int) []sectionedTabSection
```

producing exactly today's sections (managed rows; `unmanaged` sub-section when present; empty-state when none) with `selected` computed as `i == cursor` (pass `-1` for no selection). `renderMcpTab` becomes:

```go
func renderMcpTab(m Model, p palette, topLines []string) string {
	return renderSectionedTab(m, sectionedTab{top: topLines, sections: mcpSections(m, p, m.mcpCursor)})
}
```

(Match the real `sectionedTab` construction currently in the function — keep `leadingBlank` etc. identical.)

Same for `pluginSections(m Model, p palette, cursor int)` from `renderPluginTab` (view_skills.go:453).

For skills: extract from `viewSkillsBody`'s inline build (lines 274–317) into

```go
func skillsSections(m Model, p palette, cursor int) []sectionedTabSection
```

covering Installed / Not Installed / find-results exactly as today (row order must equal `skillsVisibleRows` order — it already does, both derive from the same data).

Run: `go build ./... && go test ./internal/tui/` — PASS (pure extraction).

- [ ] **Step C1: Flatten + cursor**

In `agents_all.go`:

```go
type agentsSection int

const (
	agentsSectionSkills agentsSection = iota
	agentsSectionMcp
	agentsSectionPlugins
)

type agentsAllRow struct {
	section  agentsSection
	localIdx int
}

func agentsAllRowsList(m Model) []agentsAllRow {
	var out []agentsAllRow
	if m.skillsSectionEnabled() {
		rows, _ := skillsVisibleRows(m)
		for i := range rows {
			out = append(out, agentsAllRow{agentsSectionSkills, i})
		}
	}
	if m.mcpSectionEnabled() {
		for i := 0; i < mcpTotalRows(m); i++ {
			out = append(out, agentsAllRow{agentsSectionMcp, i})
		}
	}
	if m.pluginsSectionEnabled() {
		for i := 0; i < pluginTotalRows(m); i++ {
			out = append(out, agentsAllRow{agentsSectionPlugins, i})
		}
	}
	return out
}

func agentsAllSectionAt(m Model, cursor int) (agentsSection, int, bool) {
	rows := agentsAllRowsList(m)
	if cursor < 0 || cursor >= len(rows) {
		return 0, 0, false
	}
	return rows[cursor].section, rows[cursor].localIdx, true
}

func clampAgentsAllCursor(m *Model) {
	total := len(agentsAllRowsList(*m))
	if m.agentsAllCursor >= total {
		m.agentsAllCursor = total - 1
	}
	if m.agentsAllCursor < 0 {
		m.agentsAllCursor = 0
	}
}
```

- [ ] **Step C2: All-view key handler**

```go
func (m *Model) handleAgentsAllKeyMsg(msg tea.KeyPressMsg) []tea.Cmd {
	switch msg.String() {
	case "up", "k":
		if m.agentsAllCursor > 0 {
			m.agentsAllCursor--
		}
		return nil
	case "down", "j":
		if m.agentsAllCursor < len(agentsAllRowsList(*m))-1 {
			m.agentsAllCursor++
		}
		return nil
	case "left", "h":
		m.agentsChipMove(-1)
		return nil
	case "right", "l":
		m.agentsChipMove(1)
		return nil
	}
	section, localIdx, ok := agentsAllSectionAt(*m, m.agentsAllCursor)
	if !ok {
		return nil
	}
	var cmds []tea.Cmd
	switch section {
	case agentsSectionSkills:
		m.skillsCursor = localIdx
		cmds = m.handleSkillsKeyMsg(msg)
	case agentsSectionMcp:
		m.mcpCursor = localIdx
		cmds = m.handleMcpKeyMsg(msg)
	default:
		m.pluginCursor = localIdx
		cmds = m.handlePluginKeyMsg(msg)
	}
	clampAgentsAllCursor(m)
	return cmds
}
```

Wire into the chip dispatch from Step B1 as `case agentsChipAll: return m.handleAgentsAllKeyMsg(msg)`. Note key-name strings: check what the existing handlers match on (`key.Matches(msg, m.keys.Up)` vs `msg.String()`) and use the same mechanism for up/down/left/right so bindings stay consistent.

Also: everywhere update paths call `clampMcpCursor`/`clampPluginCursor`/`clampSkillsCursor` after data loads or removals (grep call sites), append `clampAgentsAllCursor(&m)` (adjust receiver) so the all-cursor survives row-count changes.

- [ ] **Step C3: All-view render**

In `view_skills.go` (or `agents_all.go`, either — keep render helpers where the sibling renderers live):

```go
func renderAgentsAllTab(m Model, p palette, topLines []string) string {
	section, localIdx, ok := agentsAllSectionAt(m, m.agentsAllCursor)
	sel := func(s agentsSection) int {
		if ok && section == s {
			return localIdx
		}
		return -1
	}
	var sections []sectionedTabSection
	if m.skillsSectionEnabled() {
		sections = append(sections, retitleSections(skillsSections(m, p, sel(agentsSectionSkills)), "Skills")...)
	}
	if m.mcpSectionEnabled() {
		sections = append(sections, retitleSections(mcpSections(m, p, sel(agentsSectionMcp)), "MCP Servers")...)
	}
	if m.pluginsSectionEnabled() {
		sections = append(sections, retitleSections(pluginSections(m, p, sel(agentsSectionPlugins)), "Plugins")...)
	}
	return renderSectionedTab(m, sectionedTab{top: topLines, sections: sections})
}

// retitleSections renames the first (primary) section to the feature title so
// the stacked view reads Skills / MCP Servers / Plugins; sub-sections
// (unmanaged, find results) keep their own headers.
func retitleSections(secs []sectionedTabSection, title string) []sectionedTabSection {
	if len(secs) == 0 {
		return []sectionedTabSection{{title: title, empty: []string{"none"}}}
	}
	secs[0].title = title
	return secs
}
```

Constraint check: the visible row order produced here MUST equal `agentsAllRowsList` order (skills visible rows, then mcp managed+unmanaged, then plugins managed+unmanaged). `skillsSections` spans Installed/Not Installed/find sub-sections whose concatenated row order equals `skillsVisibleRows` — verify while implementing; if skills sub-section boundaries reorder rows relative to `skillsVisibleRows`, drive both from `skillsVisibleRows` directly instead.

Empty-but-enabled feature: `retitleSections` fallback renders header + dim "none" line — match the empty-state styling `mcpSections` already uses for its no-rows case (reuse its exact `empty:` strings/style rather than bare `"none"` if styled).

`viewSkillsBody` dispatch (view_skills.go:158–163):

```go
	switch m.skillTypeIdx {
	case agentsChipAll:
		return renderAgentsAllTab(m, p, topLines)
	case agentsChipMcp:
		return renderMcpTab(m, p, topLines)
	case agentsChipPlugin:
		return renderPluginTab(m, p, topLines)
	}
	// skills chip falls through to the existing inline render
```

- [ ] **Step C4: Pill bar with dimmed disabled chips**

`view_skills.go:139`: chip names become `[]string{"all", "skills", "mcp", "plugin"}`. Add a dim-capable variant beside `renderPillBarNoAll` (view_list.go:175) — copy its body, add `disabled map[int]bool`, render disabled pills with `pal.styleHelp` (the established dim style) and never as active:

```go
func renderPillBarDim(pal palette, names []string, activeIdx, maxW int, disabled map[int]bool) string
```

Call it with:

```go
	disabled := map[int]bool{
		agentsChipSkills: !m.skillsSectionEnabled(),
		agentsChipMcp:    !m.mcpSectionEnabled(),
		agentsChipPlugin: !m.pluginsSectionEnabled(),
	}
```

(`agentsChipMove` already skips disabled chips, so they are non-selectable.)

Edge: all three features disabled but master on — all-view renders no sections; return the same help-text pattern as the master-disabled screen (view_skills.go:129–135) with copy `"All agent features are disabled for this machine."` / `"Toggle them in Settings to enable."`.

- [ ] **Step C5: Hints**

`view_hints.go` viewSkills dispatch: when `m.skillTypeIdx == agentsChipAll`, resolve the hint context from the section under the cursor (`agentsAllSectionAt`) so footer hints match the row the user is on; fall back to the skills hint context when no rows.

- [ ] **Step C6: Build + full TUI suite**

Run: `go build ./... && go test ./internal/tui/`
Expected: PASS.

- [ ] **Step C7: Delegate all-view tests to tui-tester**

Dispatch **tui-tester** with the design's test list: "(1) all-view render: three sections in order Skills/MCP Servers/Plugins, empty-state 'none' line for an enabled-but-empty feature, disabled feature's section absent and its chip dimmed; (2) cursor traversal: down from last skills row lands on first mcp row (assert via rendered selection or agentsAllSectionAt); (3) key dispatch: one mutating action per section proves routing — e.g. 'n' opens the mcp add form only when cursor is on an mcp row, 'd' starts plugin delete confirm only on a plugin row, skills action key on a skills row; (4) chips: left/right from all skips a disabled chip; selecting 'skills' chip filters to skills-only render; (5) tab entry resets to all chip; (6) all-features-disabled help screen. Build models with baseModel + mcpModelWithRows/pluginModelWithRows patterns; per cerebrum, dispatch keys through Update(), and use baseModel (not modelForCmds) for render tests."

Run: `go test ./internal/tui/`
Expected: PASS.

- [ ] **Step C8: Commit**

```bash
git add internal/tui
git commit -m "feat(tui): agents tab all-view — stacked sections, single cursor, dimmed disabled chips"
```

(If A/B stages were committed separately: `refactor(tui): agents chip constants + section builder extraction` for A+B, then this for C.)

---

### Task 7: Integration fixtures (txtar-writer)

**Files:**
- Create: `integration_tests/testdata/scripts/agents-feature-gates.txtar`
- Create: `integration_tests/testdata/scripts/doctor-agents.txtar`

**Interfaces:**
- Consumes: CLI behavior from Tasks 3–4; existing fixture conventions (`agents-add.txtar` shows the `host_settings` + `testhost` gate pattern).

- [ ] **Step 1: Delegate to txtar-writer**

Dispatch **txtar-writer** with: "Two new fixtures. (1) `agents-feature-gates.txtar`: per feature — config with `{"host_settings":{"testhost":{"skills_disabled":true}}}` → `omni agents add owner/repo` fails with 'skills are disabled for this host'; same shape for `mcp_disabled` → `omni agents mcp add` fails with 'mcp servers are disabled for this host', and `plugins_disabled` → `omni agents plugins add` fails with 'plugins are disabled for this host'; restore commands (`agents skills restore`, `agents mcp restore`, `agents plugins restore`) with the feature disabled exit 0 and print `warn: ... disabled for this host, skipping restore`; master `agents_disabled` still hard-fails all of them. Follow agents-add.txtar for hostname/env setup. (2) `doctor-agents.txtar`: `omni doctor` output contains the `Agent features` check; with `agents_disabled` it shows `disabled (agents_disabled)`; with `mcp_disabled` the block shows the single mcp `disabled (mcp_disabled)` line. Check how existing doctor txtar fixtures (if any) assert check output; JSON mode via `omni doctor --json` may be steadier to grep."

- [ ] **Step 2: Run integration tests**

Run: `go test -tags=integration ./integration_tests/ -run 'TestCLI/agents-feature-gates|TestCLI/doctor-agents'`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add integration_tests
git commit -m "test(agents): feature-gate and doctor txtar fixtures"
```

---

### Task 8: Docs sweep + full verification

**Files:**
- Modify: `docs/configuration.md` (host_settings flags), `docs/tui.md` (Agents tab all-view + Settings rows), `docs/cli.md` (restore warn lines, doctor block), `docs/schema-reference.md` (verify Task 1 edit landed)

- [ ] **Step 1: Update docs**

Document: three new host_settings keys (nil = enabled; `agents_disabled` masters all three); Agents tab opens on `all` with stacked sections and chips as filters; Settings tab Agents section rows; doctor `Agent features` block and its no-probe policy for mcp/plugins installed state.

- [ ] **Step 2: Full suite**

Run: `make test`
Expected: PASS (use `scripts/run-test-safe.sh` if sandbox blocks httptest listeners).

- [ ] **Step 3: Commit**

```bash
git add docs
git commit -m "docs: agents surfaces v2 — toggles, all-view, doctor block"
```

---

## Self-Review Notes

- Spec coverage: sections+chips (Task 6), toggles config/app/settings (Tasks 1, 2, 5), guards+CLI errors+restore skip (Task 3), doctor block (Task 4), all four testing bullets (Tasks 1–6 inline + tui-tester + txtar-writer in 7). Out-of-scope list respected (no reordering, no per-agent toggles, no doctor JSON shape changes beyond the added check).
- Deliberate deviations from design doc, both forced by its own no-slow-probe rule: doctor omits mcp/plugins installed-vs-missing and unmanaged counts (need adapter `List()`); skills lockfile parity count omitted (not cheaply reachable without duplicating lock-path logic — revisit if trivial during Task 4).
- Type consistency: guard names, Save names, chip consts, section helpers cross-referenced in Interfaces blocks; line numbers are anchors as of commit a415321 — verify before editing, code may have drifted.
