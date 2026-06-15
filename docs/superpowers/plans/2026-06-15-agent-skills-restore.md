# Agent Skills Restore Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `omni agents restore skills` and `omni agents import skills` so a machine converges on an omni-owned skills manifest by driving the `vercel-labs/skills` CLI.

**Architecture:** An omni-owned manifest (`RootConfig.Agents.Skills`) is the declarative source of truth. `restore` replays each manifest skill via `<runner> skills add` (runner = npx/bunx per node_manager). `import` reads the live `~/.agents/.skill-lock.json` and upserts manifest entries (dropping runtime fields). The CLI invocation sits behind a `SkillInstaller` interface so a Go reimplementation can replace it later.

**Tech Stack:** Go, Cobra CLI, omni `internal/config` + `internal/app` + `internal/executor`, testscript/txtar integration tests.

**Spec:** `docs/superpowers/specs/2026-06-15-agent-skills-restore-design.md`

---

## File Structure

- `internal/config/config.go` — add `AgentsConfig`, `ManifestSkill` types + `Agents` field on `RootConfig`; validate in `ValidateRoot`.
- `internal/config/skilllock.go` *(new)* — parse the upstream `.skill-lock.json` (v3) + resolve its path. No omni-authoring of this file.
- `internal/app/agents_skills.go` *(new)* — `SkillInstaller` interface, `npxInstaller`, `RestoreSkills` + `ImportSkills` orchestration, result structs.
- `internal/app/agents_skills_text.go` *(new)* — summary text helpers for CLI/TUI.
- `internal/cli/agents.go` *(new)* — `agents` command group → `skills` → `restore`/`import`/`list`.
- `internal/cli/root.go` — register `newAgentsCmd(state)`.
- `integration_tests/testdata/scripts/agents-skills-*.txtar` *(new)* — dry-run fixtures (via **txtar-writer** agent).

---

## Task 1: Config types for the skills manifest

**Files:**
- Modify: `internal/config/config.go` (add types near `RootConfig`, ~line 390)
- Test: `internal/config/agents_test.go` (create)

- [ ] **Step 1: Write the failing test**

```go
package config_test

import (
	"encoding/json"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestRootConfigAgentsSkillsRoundTrip(t *testing.T) {
	in := `{"version":1,"agents":{"skills":[{"name":"frontend-design","source":"vercel-labs/agent-skills","ref":"main","agents":["claude-code"]}]}}`
	var cfg config.RootConfig
	if err := json.Unmarshal([]byte(in), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(cfg.Agents.Skills) != 1 {
		t.Fatalf("want 1 skill, got %d", len(cfg.Agents.Skills))
	}
	s := cfg.Agents.Skills[0]
	if s.Name != "frontend-design" || s.Source != "vercel-labs/agent-skills" || s.Ref != "main" {
		t.Fatalf("unexpected skill: %+v", s)
	}
	if len(s.Agents) != 1 || s.Agents[0] != "claude-code" {
		t.Fatalf("unexpected agents: %+v", s.Agents)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestRootConfigAgentsSkillsRoundTrip -v`
Expected: FAIL — `cfg.Agents` undefined.

- [ ] **Step 3: Add the types and field**

In `internal/config/config.go`, add the field to `RootConfig` after `Groups`:

```go
	Agents       AgentsConfig        `json:"agents,omitempty"`
```

Then add the type definitions just below the `RootConfig` struct:

```go
// AgentsConfig holds omni-managed AI-agent resources. Skills are restored by
// driving the upstream `skills` CLI; this is omni's own declarative manifest,
// not the CLI's runtime lockfile.
type AgentsConfig struct {
	Skills []ManifestSkill `json:"skills,omitempty"`
}

// ManifestSkill is one declared agent skill. Only fields omni can author and
// replay are stored; runtime fields (folder hash, timestamps) live in the
// upstream lockfile and are never written here.
type ManifestSkill struct {
	Name      string   `json:"name"`
	Source    string   `json:"source"`
	Ref       string   `json:"ref,omitempty"`
	SkillPath string   `json:"skill_path,omitempty"`
	Agents    []string `json:"agents,omitempty"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run TestRootConfigAgentsSkillsRoundTrip -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/agents_test.go
git commit -m "feat(config): add agents.skills manifest types"
```

---

## Task 2: Validate the manifest in ValidateRoot

**Files:**
- Modify: `internal/config/config.go` (`ValidateRoot`, after the `cfg.Tools` loop ~line 552)
- Test: `internal/config/agents_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/config/agents_test.go`:

```go
func TestValidateRootSkillRequiresNameAndSource(t *testing.T) {
	cfg := &config.RootConfig{
		Version: 1,
		Agents: config.AgentsConfig{Skills: []config.ManifestSkill{
			{Name: "", Source: ""},
			{Name: "ok", Source: "owner/repo"},
		}},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{})
	if len(errs) != 2 {
		t.Fatalf("want 2 errors (empty name, empty source), got %d: %v", len(errs), errs)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestValidateRootSkillRequiresNameAndSource -v`
Expected: FAIL — 0 errors returned.

- [ ] **Step 3: Add validation**

In `internal/config/config.go`, inside `ValidateRoot`, after the `for name, spec := range cfg.Tools` loop closes, add:

```go
	for i, skill := range cfg.Agents.Skills {
		path := fmt.Sprintf("$.agents.skills[%d]", i)
		if strings.TrimSpace(skill.Name) == "" {
			errs = append(errs, ValidationError{Path: path + ".name", Message: "skill name is required"})
		}
		if strings.TrimSpace(skill.Source) == "" {
			errs = append(errs, ValidationError{Path: path + ".source", Message: "skill source is required"})
		}
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run TestValidateRootSkillRequiresNameAndSource -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/agents_test.go
git commit -m "feat(config): validate agents.skills name and source"
```

---

## Task 3: Parse the upstream skill lockfile

**Files:**
- Create: `internal/config/skilllock.go`
- Test: `internal/config/skilllock_test.go`

- [ ] **Step 1: Write the failing test**

```go
package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestParseSkillLock(t *testing.T) {
	data := `{"version":3,"skills":{"frontend-design":{"source":"vercel-labs/agent-skills","sourceType":"github","sourceUrl":"https://github.com/vercel-labs/agent-skills","ref":"main","skillPath":"skills/frontend-design","skillFolderHash":"abc123","installedAt":"2026-06-01T00:00:00Z","updatedAt":"2026-06-01T00:00:00Z"}}}`
	lock, err := config.ParseSkillLock([]byte(data))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	e, ok := lock.Skills["frontend-design"]
	if !ok {
		t.Fatal("missing frontend-design entry")
	}
	if e.Source != "vercel-labs/agent-skills" || e.Ref != "main" || e.SkillFolderHash != "abc123" {
		t.Fatalf("unexpected entry: %+v", e)
	}
}

func TestSkillLockPathPrefersXDG(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/tmp/xdgstate")
	if got := config.SkillLockPath("/home/u"); got != filepath.Join("/tmp/xdgstate", "skills", ".skill-lock.json") {
		t.Fatalf("xdg path wrong: %s", got)
	}
	os.Unsetenv("XDG_STATE_HOME")
	if got := config.SkillLockPath("/home/u"); got != filepath.Join("/home/u", ".agents", ".skill-lock.json") {
		t.Fatalf("home path wrong: %s", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run 'TestParseSkillLock|TestSkillLockPath' -v`
Expected: FAIL — undefined `config.ParseSkillLock` / `config.SkillLockPath`.

- [ ] **Step 3: Write the implementation**

Create `internal/config/skilllock.go`:

```go
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// SkillLockEntry mirrors a single entry in the upstream skills CLI lockfile
// (vercel-labs/skills, schema v3). Omni reads this for import and drift checks
// and never writes it.
type SkillLockEntry struct {
	Source          string `json:"source"`
	SourceType      string `json:"sourceType"`
	SourceURL       string `json:"sourceUrl"`
	Ref             string `json:"ref,omitempty"`
	SkillPath       string `json:"skillPath,omitempty"`
	SkillFolderHash string `json:"skillFolderHash"`
	InstalledAt     string `json:"installedAt"`
	UpdatedAt       string `json:"updatedAt"`
	PluginName      string `json:"pluginName,omitempty"`
}

// SkillLockFile mirrors the upstream lockfile structure.
type SkillLockFile struct {
	Version           int                       `json:"version"`
	Skills            map[string]SkillLockEntry `json:"skills"`
	LastSelectedAgents []string                 `json:"lastSelectedAgents,omitempty"`
}

// ParseSkillLock decodes the upstream lockfile bytes.
func ParseSkillLock(data []byte) (*SkillLockFile, error) {
	var lock SkillLockFile
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("parsing skill lock: %w", err)
	}
	if lock.Skills == nil {
		lock.Skills = map[string]SkillLockEntry{}
	}
	return &lock, nil
}

// SkillLockPath returns the global lockfile path: $XDG_STATE_HOME/skills/.skill-lock.json
// when set, else <home>/.agents/.skill-lock.json.
func SkillLockPath(home string) string {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "skills", ".skill-lock.json")
	}
	return filepath.Join(home, ".agents", ".skill-lock.json")
}

// LoadSkillLock reads and parses the lockfile at path. A missing file yields an
// empty lockfile and no error.
func LoadSkillLock(path string) (*SkillLockFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &SkillLockFile{Version: 3, Skills: map[string]SkillLockEntry{}}, nil
		}
		return nil, fmt.Errorf("reading skill lock: %w", err)
	}
	return ParseSkillLock(data)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run 'TestParseSkillLock|TestSkillLockPath' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/skilllock.go internal/config/skilllock_test.go
git commit -m "feat(config): parse upstream skills lockfile (read-only)"
```

---

## Task 4: SkillInstaller interface + npx command construction

**Files:**
- Create: `internal/app/agents_skills.go`
- Test: `internal/app/agents_skills_test.go`

- [ ] **Step 1: Write the failing test**

```go
package app

import (
	"reflect"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestSkillAddArgs(t *testing.T) {
	skill := config.ManifestSkill{Name: "frontend-design", Source: "vercel-labs/agent-skills", Ref: "main"}
	got := skillAddArgs(skill, []string{"claude-code", "codex"})
	want := []string{"skills", "add", "vercel-labs/agent-skills#main", "-s", "frontend-design", "-g", "-a", "claude-code", "codex", "-y"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestSkillAddArgsNoRef(t *testing.T) {
	skill := config.ManifestSkill{Name: "x", Source: "owner/repo"}
	got := skillAddArgs(skill, []string{"claude-code"})
	want := []string{"skills", "add", "owner/repo", "-s", "x", "-g", "-a", "claude-code", "-y"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestSkillRunnerFromManager(t *testing.T) {
	if got := skillRunner("bun"); got != "bunx" {
		t.Fatalf("bun -> %s, want bunx", got)
	}
	for _, m := range []string{"npm", "pnpm", ""} {
		if got := skillRunner(m); got != "npx" {
			t.Fatalf("%q -> %s, want npx", m, got)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run 'TestSkillAddArgs|TestSkillRunner' -v`
Expected: FAIL — undefined `skillAddArgs` / `skillRunner`.

- [ ] **Step 3: Write the implementation**

Create `internal/app/agents_skills.go`:

```go
package app

import (
	"context"
	"fmt"

	"github.com/lkshrk/omni/internal/config"
)

// SkillInstaller materializes one manifest skill onto the host for the given
// agents. The npx/bunx wrapper is the only implementation today; a Go-native
// installer can replace it behind this interface later.
type SkillInstaller interface {
	Install(ctx context.Context, skill config.ManifestSkill, agents []string) error
}

// skillRunner picks the JS package runner from the node manager setting.
func skillRunner(nodeManager string) string {
	if nodeManager == "bun" {
		return "bunx"
	}
	return "npx"
}

// skillSource reconstructs the `add` source argument: "owner/repo" or
// "owner/repo#ref".
func skillSource(skill config.ManifestSkill) string {
	if skill.Ref != "" {
		return skill.Source + "#" + skill.Ref
	}
	return skill.Source
}

// skillAddArgs builds the argument vector for `<runner> skills add ...`.
func skillAddArgs(skill config.ManifestSkill, agents []string) []string {
	args := []string{"skills", "add", skillSource(skill), "-s", skill.Name, "-g"}
	if len(agents) > 0 {
		args = append(args, "-a")
		args = append(args, agents...)
	}
	return append(args, "-y")
}

// npxInstaller runs the upstream skills CLI via npx/bunx.
type npxInstaller struct {
	runner string
	exec   func(ctx context.Context, name string, args ...string) (string, string, error)
}

func (n npxInstaller) Install(ctx context.Context, skill config.ManifestSkill, agents []string) error {
	args := skillAddArgs(skill, agents)
	_, stderr, err := n.exec(ctx, n.runner, args...)
	if err != nil {
		return fmt.Errorf("skills add %s: %w: %s", skill.Name, err, stderr)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/ -run 'TestSkillAddArgs|TestSkillRunner' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/agents_skills.go internal/app/agents_skills_test.go
git commit -m "feat(app): skill installer interface and npx/bunx command builder"
```

---

## Task 5: RestoreSkills orchestration

**Files:**
- Modify: `internal/app/agents_skills.go`
- Test: `internal/app/agents_skills_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestRestoreSkillsAggregatesResults(t *testing.T) {
	skills := []config.ManifestSkill{
		{Name: "good", Source: "o/r", Agents: []string{"claude-code"}},
		{Name: "bad", Source: "o/r2", Agents: []string{"claude-code"}},
	}
	inst := stubInstaller{fail: map[string]bool{"bad": true}}
	res := restoreSkills(context.Background(), skills, inst)
	if len(res.Installed) != 1 || res.Installed[0] != "good" {
		t.Fatalf("installed = %v", res.Installed)
	}
	if len(res.Failed) != 1 || res.Failed[0].Name != "bad" {
		t.Fatalf("failed = %v", res.Failed)
	}
}

type stubInstaller struct{ fail map[string]bool }

func (s stubInstaller) Install(_ context.Context, skill config.ManifestSkill, _ []string) error {
	if s.fail[skill.Name] {
		return fmt.Errorf("boom")
	}
	return nil
}
```

Add imports `context` and `fmt` to the test file if not present.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run TestRestoreSkillsAggregates -v`
Expected: FAIL — undefined `restoreSkills` / result types.

- [ ] **Step 3: Write the implementation**

Append to `internal/app/agents_skills.go`:

```go
// SkillFailure records one skill that failed to install.
type SkillFailure struct {
	Name    string
	Message string
}

// RestoreSkillsResult summarises a restore run.
type RestoreSkillsResult struct {
	Installed []string
	Failed    []SkillFailure
}

// restoreSkills installs every skill, never aborting on a single failure.
func restoreSkills(ctx context.Context, skills []config.ManifestSkill, inst SkillInstaller) RestoreSkillsResult {
	var res RestoreSkillsResult
	for _, skill := range skills {
		if err := inst.Install(ctx, skill, skill.Agents); err != nil {
			res.Failed = append(res.Failed, SkillFailure{Name: skill.Name, Message: err.Error()})
			continue
		}
		res.Installed = append(res.Installed, skill.Name)
	}
	return res
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/ -run TestRestoreSkillsAggregates -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/agents_skills.go internal/app/agents_skills_test.go
git commit -m "feat(app): restore skills orchestration with partial-failure aggregation"
```

---

## Task 6: App.RestoreSkills public method (runner wiring + dry-run)

**Files:**
- Modify: `internal/app/agents_skills.go`
- Test: `internal/app/agents_skills_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestRestoreSkillsOptionsDryRun(t *testing.T) {
	skills := []config.ManifestSkill{{Name: "a", Source: "o/r", Ref: "main", Agents: []string{"claude-code"}}}
	lines := dryRunLines("npx", skills)
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %d", len(lines))
	}
	want := "npx skills add o/r#main -s a -g -a claude-code -y"
	if lines[0] != want {
		t.Fatalf("line = %q want %q", lines[0], want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run TestRestoreSkillsOptionsDryRun -v`
Expected: FAIL — undefined `dryRunLines`.

- [ ] **Step 3: Write the implementation**

Append to `internal/app/agents_skills.go`:

Add `strings` and the provider package to the import block:

```go
import (
	"strings"

	"github.com/lkshrk/omni/internal/provider"
)
```

```go
// RestoreSkillsOptions controls a restore run.
type RestoreSkillsOptions struct {
	DryRun bool
}

// dryRunLines renders the commands restore would run, one per skill.
func dryRunLines(runner string, skills []config.ManifestSkill) []string {
	lines := make([]string, 0, len(skills))
	for _, skill := range skills {
		args := skillAddArgs(skill, skill.Agents)
		lines = append(lines, runner+" "+strings.Join(args, " "))
	}
	return lines
}

// nodeManager reads the declared node ecosystem manager ("bun"/"npm"/...).
// Empty when unset → skillRunner falls back to npx.
func nodeManager(cfg *config.RootConfig) string {
	return cfg.Settings.Ecosystems[provider.EcosystemNode].Manager
}

// RestoreSkills restores the manifest skill set onto this host.
func (a *App) RestoreSkills(ctx context.Context, opts RestoreSkillsOptions) (RestoreSkillsResult, []string, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return RestoreSkillsResult{}, nil, err
	}
	runner := skillRunner(nodeManager(cfg))
	skills := cfg.Agents.Skills
	if opts.DryRun {
		return RestoreSkillsResult{}, dryRunLines(runner, skills), nil
	}
	inst := npxInstaller{runner: runner, exec: a.fallbackExecutor().Run}
	return restoreSkills(ctx, skills, inst), nil, nil
}
```

> Verified against source: `EcosystemSettings.Manager` exists
> (`internal/config/config.go:234`); `provider.EcosystemNode == "node"`
> (`internal/provider/catalog.go:7`); `a.fallbackExecutor()` is the nil-safe
> executor accessor returning `a.fallbackExec` or `a.newExecutor()`
> (`internal/app/fallback.go:630`) — use it, NOT `a.fallbackExec` directly.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/ -run TestRestoreSkillsOptionsDryRun -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/agents_skills.go internal/app/agents_skills_test.go
git commit -m "feat(app): App.RestoreSkills with runner resolution and dry-run"
```

---

## Task 7: ImportSkills — upsert lockfile entries into the manifest

**Files:**
- Modify: `internal/app/agents_skills.go`
- Test: `internal/app/agents_skills_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestImportSkillsUpsert(t *testing.T) {
	existing := []config.ManifestSkill{
		{Name: "keep", Source: "o/keep", Ref: "main"},
		{Name: "changed", Source: "o/changed", Ref: "old"},
	}
	lock := &config.SkillLockFile{Skills: map[string]config.SkillLockEntry{
		"keep":    {Source: "o/keep", Ref: "main"},
		"changed": {Source: "o/changed", Ref: "new"},
		"added":   {Source: "o/added", Ref: "main", SkillPath: "skills/added"},
	}}
	merged, diff := importSkills(existing, lock)
	if len(merged) != 3 {
		t.Fatalf("want 3 merged, got %d", len(merged))
	}
	if len(diff.Added) != 1 || diff.Added[0] != "added" {
		t.Fatalf("added = %v", diff.Added)
	}
	if len(diff.Updated) != 1 || diff.Updated[0] != "changed" {
		t.Fatalf("updated = %v", diff.Updated)
	}
	if len(diff.Unchanged) != 1 || diff.Unchanged[0] != "keep" {
		t.Fatalf("unchanged = %v", diff.Unchanged)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run TestImportSkillsUpsert -v`
Expected: FAIL — undefined `importSkills`.

- [ ] **Step 3: Write the implementation**

Append to `internal/app/agents_skills.go`:

```go
import "sort"  // add to existing import block

// ImportDiff reports what an import changed.
type ImportDiff struct {
	Added     []string
	Updated   []string
	Unchanged []string
}

// importSkills upserts lockfile entries into the manifest, dropping runtime
// fields. Existing entries change only when source/ref/skillPath differ.
func importSkills(existing []config.ManifestSkill, lock *config.SkillLockFile) ([]config.ManifestSkill, ImportDiff) {
	byName := make(map[string]config.ManifestSkill, len(existing))
	order := make([]string, 0, len(existing))
	for _, s := range existing {
		byName[s.Name] = s
		order = append(order, s.Name)
	}

	var diff ImportDiff
	names := make([]string, 0, len(lock.Skills))
	for name := range lock.Skills {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		e := lock.Skills[name]
		next := config.ManifestSkill{Name: name, Source: e.Source, Ref: e.Ref, SkillPath: e.SkillPath}
		prev, ok := byName[name]
		switch {
		case !ok:
			next.Agents = nil
			byName[name] = next
			order = append(order, name)
			diff.Added = append(diff.Added, name)
		case prev.Source != next.Source || prev.Ref != next.Ref || prev.SkillPath != next.SkillPath:
			next.Agents = prev.Agents // preserve hand-declared targeting
			byName[name] = next
			diff.Updated = append(diff.Updated, name)
		default:
			diff.Unchanged = append(diff.Unchanged, name)
		}
	}

	merged := make([]config.ManifestSkill, 0, len(order))
	for _, name := range order {
		merged = append(merged, byName[name])
	}
	return merged, diff
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/ -run TestImportSkillsUpsert -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/agents_skills.go internal/app/agents_skills_test.go
git commit -m "feat(app): import skills from lockfile into manifest (upsert)"
```

---

## Task 8: App.ImportSkills public method (read lock, write manifest)

**Files:**
- Modify: `internal/app/agents_skills.go`
- Test: `internal/app/agents_skills_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestImportSkillsDiffOnly(t *testing.T) {
	// importSkills already covers merge logic; here assert dry-run produces a
	// diff without requiring a manifest write.
	existing := []config.ManifestSkill{}
	lock := &config.SkillLockFile{Skills: map[string]config.SkillLockEntry{
		"x": {Source: "o/x", Ref: "main"},
	}}
	_, diff := importSkills(existing, lock)
	if len(diff.Added) != 1 {
		t.Fatalf("added = %v", diff.Added)
	}
}
```

- [ ] **Step 2: Run test to verify it fails (or compile-checks the public method)**

Run: `go test ./internal/app/ -run TestImportSkillsDiffOnly -v`
Expected: PASS for the helper; this task's real deliverable is the public method below — verify it compiles via `go build ./...`.

- [ ] **Step 3: Write the implementation**

Append to `internal/app/agents_skills.go`:

```go
import "os"  // add to existing import block

// ImportSkillsOptions controls an import run.
type ImportSkillsOptions struct {
	DryRun bool
}

// ImportSkills ingests CLI/UI-added skills from the live lockfile into the
// manifest. With DryRun it computes the diff but does not write.
func (a *App) ImportSkills(ctx context.Context, opts ImportSkillsOptions) (ImportDiff, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return ImportDiff{}, fmt.Errorf("resolving home dir: %w", err)
	}
	lock, err := config.LoadSkillLock(config.SkillLockPath(home))
	if err != nil {
		return ImportDiff{}, err
	}

	var diff ImportDiff
	if opts.DryRun {
		cfg, err := a.loadConfig()
		if err != nil {
			return ImportDiff{}, err
		}
		_, diff = importSkills(cfg.Agents.Skills, lock)
		return diff, nil
	}

	err = a.withConfig(func(cfg *config.RootConfig) error {
		merged, d := importSkills(cfg.Agents.Skills, lock)
		cfg.Agents.Skills = merged
		diff = d
		return nil
	})
	return diff, err
}
```

- [ ] **Step 4: Verify build + tests**

Run: `go build ./... && go test ./internal/app/ -run TestImportSkills -v`
Expected: build OK, tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/agents_skills.go internal/app/agents_skills_test.go
git commit -m "feat(app): App.ImportSkills reads lockfile and updates manifest"
```

---

## Task 9: Summary text helpers

**Files:**
- Create: `internal/app/agents_skills_text.go`
- Test: `internal/app/agents_skills_text_test.go`

- [ ] **Step 1: Write the failing test**

```go
package app

import (
	"strings"
	"testing"
)

func TestRestoreSkillsSummaryText(t *testing.T) {
	res := RestoreSkillsResult{Installed: []string{"a", "b"}, Failed: []SkillFailure{{Name: "c", Message: "boom"}}}
	out := RestoreSkillsSummaryText(res)
	if !strings.Contains(out, "2 installed") || !strings.Contains(out, "1 failed") {
		t.Fatalf("summary = %q", out)
	}
}

func TestImportDiffSummaryText(t *testing.T) {
	out := ImportDiffSummaryText(ImportDiff{Added: []string{"x"}, Updated: []string{"y"}, Unchanged: []string{"z"}})
	if !strings.Contains(out, "1 added") || !strings.Contains(out, "1 updated") {
		t.Fatalf("summary = %q", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run 'TestRestoreSkillsSummary|TestImportDiffSummary' -v`
Expected: FAIL — undefined helpers.

- [ ] **Step 3: Write the implementation**

Create `internal/app/agents_skills_text.go`:

```go
package app

import "fmt"

// RestoreSkillsSummaryText renders a one-line restore summary.
func RestoreSkillsSummaryText(res RestoreSkillsResult) string {
	return fmt.Sprintf("%d installed, %d failed", len(res.Installed), len(res.Failed))
}

// ImportDiffSummaryText renders a one-line import summary.
func ImportDiffSummaryText(diff ImportDiff) string {
	return fmt.Sprintf("%d added, %d updated, %d unchanged", len(diff.Added), len(diff.Updated), len(diff.Unchanged))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/ -run 'TestRestoreSkillsSummary|TestImportDiffSummary' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/agents_skills_text.go internal/app/agents_skills_text_test.go
git commit -m "feat(app): summary text for skills restore and import"
```

---

## Task 10: CLI `agents` command group

**Files:**
- Create: `internal/cli/agents.go`
- Modify: `internal/cli/root.go` (add `newAgentsCmd(state)` to `root.AddCommand(...)`, ~line 123)

- [ ] **Step 1: Write the command file**

Create `internal/cli/agents.go`:

```go
package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
)

func newAgentsCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "Manage AI-agent resources (skills)",
	}
	cmd.AddCommand(newAgentsSkillsCmd(state))
	return cmd
}

func newAgentsSkillsCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Restore and import agent skills",
	}
	cmd.AddCommand(
		newAgentsRestoreSkillsCmd(state),
		newAgentsImportSkillsCmd(state),
	)
	return cmd
}

func newAgentsRestoreSkillsCmd(state *rootState) *cobra.Command {
	var opts app.RestoreSkillsOptions
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Install the manifest skill set onto this host",
		RunE: func(cmd *cobra.Command, _ []string) error {
			res, lines, err := state.app.RestoreSkills(cmd.Context(), opts)
			if err != nil {
				return err
			}
			if opts.DryRun {
				for _, l := range lines {
					fmt.Fprintln(cmdOut(cmd), l)
				}
				return nil
			}
			fmt.Fprintln(cmdOut(cmd), app.RestoreSkillsSummaryText(res))
			for _, f := range res.Failed {
				fmt.Fprintf(cmdOut(cmd), "  ! %s: %s\n", f.Name, f.Message)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "print the skills add commands without running them")
	return cmd
}

func newAgentsImportSkillsCmd(state *rootState) *cobra.Command {
	var opts app.ImportSkillsOptions
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import CLI/UI-added skills from the lockfile into the manifest",
		RunE: func(cmd *cobra.Command, _ []string) error {
			diff, err := state.app.ImportSkills(cmd.Context(), opts)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmdOut(cmd), app.ImportDiffSummaryText(diff))
			for _, n := range diff.Added {
				fmt.Fprintf(cmdOut(cmd), "  + %s\n", n)
			}
			for _, n := range diff.Updated {
				fmt.Fprintf(cmdOut(cmd), "  ~ %s\n", n)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "show the manifest diff without writing")
	return cmd
}
```

- [ ] **Step 2: Register in root**

In `internal/cli/root.go`, add to the `root.AddCommand(...)` block (~line 123):

```go
		newAgentsCmd(state),
```

- [ ] **Step 3: Exempt `agents` from host gating**

Agent skills are independent of tool-host assignment (like `dots`). In
`internal/cli/root.go`, add to the `hostExempt` map (~line 143):

```go
	"agents":     true, // skills restore/import are independent of tool hosts
```

The ancestor walk in `requireActiveHost` (root.go:174-183) then exempts the whole
`agents` subtree. Verified: without this, `omni agents skills restore` errors
when no active host is configured.

- [ ] **Step 4: Extend the hostExempt membership test**

In `internal/cli/cmd_test.go`, `TestHostExempt_ContainsExpectedCommands`
(~line 1751), add `"agents"` to the `expected` slice:

```go
	expected := []string{"bootstrap", "doctor", "init", "hosts", "dots", "ui", "version", "settings", "help", "completion", "agents"}
```

- [ ] **Step 5: Verify build + host-exempt test**

Run: `go build ./... && go test ./internal/cli/ -run TestHostExempt -v`
Expected: build OK, test PASS.

- [ ] **Step 6: Manual smoke (dry-run, no network)**

Run:
```bash
printf '{"version":1,"agents":{"skills":[{"name":"x","source":"o/r","ref":"main","agents":["claude-code"]}]}}' > /tmp/omni-skills.json
go run ./cmd/omni --config /tmp/omni-skills.json agents skills restore --dry-run
```
Expected stdout: `npx skills add o/r#main -s x -g -a claude-code -y`

- [ ] **Step 7: Commit**

```bash
git add internal/cli/agents.go internal/cli/root.go internal/cli/cmd_test.go
git commit -m "feat(cli): omni agents skills restore/import commands"
```

---

## Task 11: Integration tests (txtar dry-run fixtures)

**Files:**
- Create: `integration_tests/testdata/scripts/agents-skills-restore-dryrun.txtar`
- Create: `integration_tests/testdata/scripts/agents-skills-import-dryrun.txtar`

> **Delegate fixture authoring to the `txtar-writer` agent** (project rule —
> never hand-write txtar fixtures inline). Brief below.

- [ ] **Step 1: Dispatch the txtar-writer agent**

Brief: "Write two txtar fixtures for `integration_tests/testdata/scripts/`.

Fixture 1 `agents-skills-restore-dryrun.txtar`: a `settings.json` declaring one
`agents.skills` entry `{name:'x', source:'o/r', ref:'main', agents:['claude-code']}`;
run `omni --config settings.json agents skills restore --dry-run`; assert stdout
matches `npx skills add o/r#main -s x -g -a claude-code -y`. The `agents` command
is host-exempt (Task 10 Step 3), so no `hosts ensure` is needed.

Fixture 2 `agents-skills-import-dryrun.txtar`: provide a `.agents/.skill-lock.json`
file in the work dir with one skill entry, set `HOME` (or `XDG_STATE_HOME`) so
`config.SkillLockPath` resolves to it, run `omni --config settings.json agents
skills import --dry-run`, assert stdout contains `1 added`. Confirm how the
harness sets HOME/XDG before finalizing; mirror existing fixtures."

- [ ] **Step 2: Run the integration tests**

Run: `go test ./integration_tests/ -run TestCLI -v`
Expected: the two new scripts PASS.

- [ ] **Step 3: Commit**

```bash
git add integration_tests/testdata/scripts/agents-skills-restore-dryrun.txtar integration_tests/testdata/scripts/agents-skills-import-dryrun.txtar
git commit -m "test(agents): txtar dry-run coverage for skills restore and import"
```

---

## Task 12: Post-restore drift check

Compare each skill's `skillFolderHash` before vs after restore and surface
changes as non-fatal warnings (the spec's health check). `skills add` rewrites
the lockfile, so post-restore hashes reflect what was actually fetched; a change
vs the pre-restore value means the locked ref moved.

**Files:**
- Modify: `internal/app/agents_skills.go`
- Test: `internal/app/agents_skills_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestSkillDriftDiff(t *testing.T) {
	before := map[string]string{"a": "h1", "b": "h2", "gone": "h9"}
	after := map[string]string{"a": "h1", "b": "h2new", "c": "h3"}
	drift := skillDrift(before, after)
	// changed: b (h2 -> h2new); new: c (had no prior hash). "a" unchanged, "gone" ignored.
	if len(drift) != 2 {
		t.Fatalf("want 2 drift entries, got %d: %v", len(drift), drift)
	}
	got := map[string]bool{}
	for _, d := range drift {
		got[d] = true
	}
	if !got["b"] || !got["c"] {
		t.Fatalf("drift = %v, want b and c", drift)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run TestSkillDriftDiff -v`
Expected: FAIL — undefined `skillDrift`.

- [ ] **Step 3: Write the implementation**

Append to `internal/app/agents_skills.go`:

```go
// skillDrift returns the names whose folder hash changed (or newly appeared)
// between two lockfile snapshots. Names absent from `after` are ignored.
func skillDrift(before, after map[string]string) []string {
	var changed []string
	for name, h := range after {
		if before[name] != h {
			changed = append(changed, name)
		}
	}
	sort.Strings(changed)
	return changed
}

// lockHashes reads the live lockfile and returns name -> skillFolderHash.
// A missing lockfile yields an empty map (no error).
func lockHashes(home string) (map[string]string, error) {
	lock, err := config.LoadSkillLock(config.SkillLockPath(home))
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(lock.Skills))
	for name, e := range lock.Skills {
		out[name] = e.SkillFolderHash
	}
	return out, nil
}
```

Then add a `Drift []string` field to `RestoreSkillsResult` and populate it in
`RestoreSkills` (non-dry-run path) by snapshotting hashes before and after the
install loop:

```go
// in RestoreSkillsResult:
//     Drift []string

// in RestoreSkills, replace the non-dry-run return with:
	home, err := os.UserHomeDir()
	if err != nil {
		return RestoreSkillsResult{}, nil, fmt.Errorf("resolving home dir: %w", err)
	}
	before, err := lockHashes(home)
	if err != nil {
		return RestoreSkillsResult{}, nil, err
	}
	inst := npxInstaller{runner: runner, exec: a.fallbackExecutor().Run}
	res := restoreSkills(ctx, skills, inst)
	after, err := lockHashes(home)
	if err != nil {
		return RestoreSkillsResult{}, nil, err
	}
	res.Drift = skillDrift(before, after)
	return res, nil, nil
```

Ensure `os` and `sort` are in the import block (added in Tasks 7–8).

- [ ] **Step 4: Run tests**

Run: `go test ./internal/app/ -run 'TestSkillDrift|TestRestoreSkills' -v`
Expected: PASS.

- [ ] **Step 5: Surface drift in the CLI**

In `internal/cli/agents.go`, in `newAgentsRestoreSkillsCmd`'s RunE, after the
failures loop, add:

```go
			for _, d := range res.Drift {
				fmt.Fprintf(cmdOut(cmd), "  ~ drift: %s changed since lock\n", d)
			}
```

- [ ] **Step 6: Commit**

```bash
git add internal/app/agents_skills.go internal/app/agents_skills_test.go internal/cli/agents.go
git commit -m "feat(app): post-restore skill drift warnings"
```

---

## Task 13: TUI Skills tab

Add a `Skills` main tab that lists the manifest and runs restore/import with two
keys. Mirrors the existing tab wiring (Dots/Groups). TUI tests are authored by
the **tui-tester** agent (project rule) in the final step.

**Files:**
- Modify: `internal/app/agents_skills.go` (add `ListSkills` accessor)
- Modify: `internal/tui/model.go` (enum + state fields)
- Modify: `internal/tui/view_header.go` (`mainTabs`)
- Modify: `internal/tui/view.go` (render dispatch + window title)
- Modify: `internal/tui/update_keys.go` (key handling + tab-cycle allowlists)
- Modify: `internal/tui/update.go` (message routing + allowlist)
- Create: `internal/tui/view_skills.go`
- Create: `internal/tui/commands_agents.go`
- Modify: `internal/tui/messages.go` (two result messages)

- [ ] **Step 1: App accessor for the manifest**

Append to `internal/app/agents_skills.go`:

```go
// ListSkills returns the declared manifest skills.
func (a *App) ListSkills() ([]config.ManifestSkill, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	return cfg.Agents.Skills, nil
}
```

- [ ] **Step 2: Add the view mode**

In `internal/tui/model.go`, add to the `viewMode` const block (after `viewDots`):

```go
	viewSkills                          // agent skills management tab
```

- [ ] **Step 3: Add model state**

In `internal/tui/model.go`, in the `Model` struct (near the other per-tab state),
add:

```go
	// skills tab
	skillsManifest []config.ManifestSkill
	skillsLoaded   bool
	skillsRunning  bool
	skillsResult   *app.RestoreSkillsResult
	skillsImport   *app.ImportDiff
	skillsErr      error
```

Confirm `config` is already imported in `model.go` (it is — `ManifestSkill` is a
`config` type).

- [ ] **Step 4: Register the tab**

In `internal/tui/view_header.go`, add to `mainTabs()` (after the Dots entry):

```go
		{mode: viewSkills, label: "Skills"},
```

- [ ] **Step 5: Messages**

In `internal/tui/messages.go`, add:

```go
type skillsRestoredMsg struct {
	res app.RestoreSkillsResult
	err error
}

type skillsImportedMsg struct {
	diff app.ImportDiff
	err  error
}
```

- [ ] **Step 6: Commands**

Create `internal/tui/commands_agents.go`. Copy the import block style from
`internal/tui/commands_admin.go` (same `tea` and `app` import paths):

```go
package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
)

func (m *Model) doRestoreSkills() tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		res, _, err := a.RestoreSkills(ctx, app.RestoreSkillsOptions{})
		return skillsRestoredMsg{res: res, err: err}
	}
}

func (m *Model) doImportSkills() tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		diff, err := a.ImportSkills(ctx, app.ImportSkillsOptions{})
		return skillsImportedMsg{diff: diff, err: err}
	}
}

func (m *Model) loadSkillsManifest() {
	skills, err := m.app.ListSkills()
	m.skillsManifest = skills
	m.skillsErr = err
	m.skillsLoaded = true
}
```

> Verify the `tea` import path against `commands_admin.go` before writing — if it
> differs, match that file exactly.

- [ ] **Step 7: Render dispatch + view**

Create `internal/tui/view_skills.go`:

```go
package tui

import (
	"fmt"
	"strings"
)

func (m Model) viewSkillsBody() string {
	var b strings.Builder
	b.WriteString("Agent Skills\n\n")
	if m.skillsErr != nil {
		fmt.Fprintf(&b, "error: %v\n", m.skillsErr)
		return b.String()
	}
	if len(m.skillsManifest) == 0 {
		b.WriteString("No skills declared. Run import to capture installed skills.\n")
	}
	for _, s := range m.skillsManifest {
		ref := ""
		if s.Ref != "" {
			ref = "#" + s.Ref
		}
		fmt.Fprintf(&b, "  • %-28s %s%s\n", s.Name, s.Source, ref)
	}
	if m.skillsRunning {
		b.WriteString("\nworking…\n")
	}
	if m.skillsResult != nil {
		fmt.Fprintf(&b, "\n%s\n", RestoreSkillsSummaryLine(*m.skillsResult))
	}
	if m.skillsImport != nil {
		fmt.Fprintf(&b, "\n%s\n", ImportDiffSummaryLine(*m.skillsImport))
	}
	b.WriteString("\n[r] restore   [i] import\n")
	return b.String()
}
```

Add thin re-exports in `internal/tui/view_skills.go` that call the app text
helpers (keeps the view import-light):

```go
func RestoreSkillsSummaryLine(res app.RestoreSkillsResult) string {
	return app.RestoreSkillsSummaryText(res)
}

func ImportDiffSummaryLine(diff app.ImportDiff) string {
	return app.ImportDiffSummaryText(diff)
}
```

Add `"github.com/lkshrk/omni/internal/app"` to the `view_skills.go` imports for
those two helpers.

In `internal/tui/view.go`, add to the render dispatch switch (alongside
`case viewDots:`):

```go
	case viewSkills:
		body = m.viewSkillsBody()
```

> Match the exact assignment pattern of the neighbouring `case viewDots:` arm
> (variable name / wrapper call may differ — copy its shape).

Also add to `windowTitle()`:

```go
	case viewSkills:
		return "omni — skills"
```

- [ ] **Step 8: Key handling**

In `internal/tui/update_keys.go`, add a `case viewSkills:` arm to the per-mode
key switch (near `case viewDots:`):

```go
	case viewSkills:
		switch msg.String() {
		case "r":
			m.skillsRunning = true
			m.skillsResult = nil
			return m, m.doRestoreSkills()
		case "i":
			m.skillsRunning = true
			m.skillsImport = nil
			return m, m.doImportSkills()
		}
```

Add `viewSkills` to the tab-cycle allowlists in `update_keys.go` (the
`case viewStatus, viewList, viewDots, viewGroups, viewSettings:` at ~line 216 and
the negative guard at ~line 230) and in `update.go` (~line 339). Also load the
manifest on switch: in the tab-switch handler (mirror the `target == viewDots`
lazy-load at update_keys.go:203), add:

```go
	if target == viewSkills && !m.skillsLoaded {
		m.loadSkillsManifest()
	}
```

- [ ] **Step 9: Message handling**

In `internal/tui/update.go`, add cases to the top-level message switch (near
`case toolsLoadedMsg:`):

```go
	case skillsRestoredMsg:
		m.skillsRunning = false
		if msg.err != nil {
			m.skillsErr = msg.err
		} else {
			r := msg.res
			m.skillsResult = &r
			m.loadSkillsManifest()
		}
		return m, nil

	case skillsImportedMsg:
		m.skillsRunning = false
		if msg.err != nil {
			m.skillsErr = msg.err
		} else {
			d := msg.diff
			m.skillsImport = &d
			m.loadSkillsManifest()
		}
		return m, nil
```

> Match the exact `return` shape of neighbouring arms — some return
> `m, cmd`, some `m, nil`. Copy the local convention.

- [ ] **Step 10: Build + manual smoke**

Run: `go build ./... && go run ./cmd/omni --config /tmp/omni-skills.json ui`
(reuse the `/tmp/omni-skills.json` from Task 10). Tab to **Skills**, confirm the
declared skill lists and the `[r] restore   [i] import` footer renders.

- [ ] **Step 11: Tests via tui-tester, then commit**

Dispatch the **tui-tester** agent: "Cover the new `viewSkills` tab in
`internal/tui`: tab appears in `mainTabs()`; switching to it lazy-loads the
manifest via `loadSkillsManifest`; `r` dispatches `doRestoreSkills` and a
`skillsRestoredMsg` populates `skillsResult` + clears `skillsRunning`; `i`
dispatches import and `skillsImportedMsg` populates `skillsImport`; the body
renders skill rows and the `[r] restore   [i] import` footer. Use the project's
established TUI test helpers (`drive(...)`, `baseModel(...)`)."

Then:
```bash
go test ./internal/tui/ -run Skills -v
git add internal/app/agents_skills.go internal/tui/
git commit -m "feat(tui): agent skills tab with restore and import"
```

---

## Task 14: Full suite + docs touch-up

**Files:**
- Modify: `README.md` (add a short agents/skills usage note near existing command docs)

- [ ] **Step 1: Run the whole suite**

Run: `make test`
Expected: all unit + integration tests PASS; coverage ≥80% on changed packages.

- [ ] **Step 2: Add README usage note**

Add a concise section documenting:
```
omni agents skills import          # capture CLI/UI-added skills into the manifest
omni agents skills restore         # install the manifest skill set on this host
omni agents skills restore --dry-run
```

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: document omni agents skills restore/import"
```

---

## Self-Review Notes

- **Spec coverage:** manifest types (T1–2), lockfile parse + path (T3), installer
  boundary/npx+bunx (T4), restore aggregation (T5–6), dry-run (T6), import upsert
  (T7–8), summary text (T9), CLI surface + host-exempt (T10), integration tests
  (T11), drift check (T12), TUI Skills tab (T13), suite + docs (T14).
- **Seams verified against source (no longer assumptions):**
  - Node manager: `cfg.Settings.Ecosystems[provider.EcosystemNode].Manager`
    (`config.go:234`, `catalog.go:7`). — Task 6.
  - Executor: `a.fallbackExecutor()` nil-safe accessor (`fallback.go:630`),
    not raw `a.fallbackExec`. — Task 6.
  - Host gating: `hostExempt` map (`root.go:143`) + ancestor walk
    (`root.go:174`); `agents` added + membership test updated. — Task 10.
- **TUI:** a dedicated **Skills** main tab (T13) — `viewSkills` mode, `mainTabs`
  entry, `view_skills.go`, `commands_agents.go`, message routing, `r`/`i` keys.
  Wiring mirrors the verified Dots/Groups tab pattern; tests via **tui-tester**.
  Implementer must copy the exact `tea` import path and neighbouring `case`-arm
  shapes from `commands_admin.go` / `view.go` / `update.go` (flagged inline).
- **Still open (low-risk, decide in execution):** `--copy` vs symlink — default
  to CLI default (symlink) for v1; `SkillPath` is stored for import fidelity and
  the future Go installer but unused by the npx `add` path (`-s <name>` selects);
  empty manifest/lockfile → treat as no-op success.
- **Out of scope (per spec):** removing skills on restore, omni writing the
  lockfile, exact-hash pinning, per-host skill sets.
