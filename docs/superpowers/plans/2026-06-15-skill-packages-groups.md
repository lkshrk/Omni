# Skill Packages + Groups (P1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make agent-skill *packages* (source repos) first-class group-targeted resources: a flat package manifest, group-side membership, ungrouped-restores-everywhere resolution, and an Agents-tab table with a group badge + group editing.

**Architecture:** Replace the per-skill `config.ManifestSkill`/`AgentsConfig.Skills` flat list with a package type `SkillPackage{Source,Ref,Agents}` stored in `AgentsConfig.Packages`. Group membership lives in `GroupConfig.Skills []string` (source refs), like `ToolEntry` in `GroupConfig.Tools`. Restore resolves: all ungrouped packages PLUS packages referenced by an active host group. A one-time config migration moves legacy flat skills into `Packages` (ungrouped). The Agents tab gains a group-badge column and a `g` group-picker keybind that writes membership across `cfg.Groups[*].Skills`.

**Tech Stack:** Go, SQLite, Cobra CLI, Bubbletea/lipgloss TUI, testify-free table tests, `rogpeppe/go-internal/testscript` txtar.

**Scope:** P1 only. P2 (TUI add-by-source + `skills find` discovery) is a separate plan.

**Reference functions to mirror (read the source):**
- Tool group membership: `internal/app/app_membership.go` `setToolGroupsInConfig` (680), `createSelectedGroupsInConfig` (627), `SetToolGroups`/`SetToolGroupsWithState`.
- Active host groups: `internal/app/app.go` `activeHostGroupNames` (704), `effectiveHostGroups` (727), `currentMachineGroupName` (653).
- Tool resolution: `internal/app/app_resolver.go` `resolveTools`.
- Current skills code (to be replaced): `internal/app/agents_skills.go`, `internal/app/agents_skills_rows.go`, `internal/tui/view_skills.go`, `internal/config/config.go` `AgentsConfig`/`ManifestSkill` + `ValidateRoot` skills block (586-594).
- Per-host agents-use (keep): `internal/app/agents_enable.go`, `Settings.AgentsUse`, `effectiveSkillAgents` in `agents_skills.go`.

---

### Task 1: SkillPackage config type + GroupConfig.Skills

**Files:**
- Modify: `internal/config/config.go` (AgentsConfig ~395, ManifestSkill ~402, GroupConfig ~347)
- Modify: `internal/config/loader.go` `cloneSettings`/group clone path
- Test: `internal/config/skill_package_test.go` (create)

- [ ] **Step 1: Write the failing test**

```go
// internal/config/skill_package_test.go
package config_test

import (
	"encoding/json"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestSkillPackageJSONRoundTrip(t *testing.T) {
	root := &config.RootConfig{
		Agents: config.AgentsConfig{
			Packages: []config.SkillPackage{
				{Source: "vercel-labs/agent-skills", Ref: "main", Agents: []string{"codex"}},
			},
		},
		Groups: []*config.GroupConfig{
			{Name: "work", Skills: []string{"vercel-labs/agent-skills"}},
		},
	}
	b, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	var got config.RootConfig
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Agents.Packages) != 1 || got.Agents.Packages[0].Source != "vercel-labs/agent-skills" {
		t.Fatalf("packages = %+v", got.Agents.Packages)
	}
	if len(got.Groups[0].Skills) != 1 || got.Groups[0].Skills[0] != "vercel-labs/agent-skills" {
		t.Fatalf("group skills = %+v", got.Groups[0].Skills)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestSkillPackageJSONRoundTrip`
Expected: FAIL — `Packages`/`Skills` undefined (compile error).

- [ ] **Step 3: Implement**

In `internal/config/config.go`, replace the `AgentsConfig` and `ManifestSkill` definitions:

```go
// AgentsConfig holds omni-managed AI-agent resources. Packages are restored by
// driving the upstream `skills` CLI; this is omni's own declarative manifest.
type AgentsConfig struct {
	Packages []SkillPackage `json:"packages,omitempty"`
	// Skills is the legacy per-skill manifest, retained only so a one-time
	// migration can fold it into Packages. Never written back.
	Skills []ManifestSkill `json:"skills,omitempty"`
}

// SkillPackage is a source repo of agent skills, tracked as one unit. The
// repo (Source) is the identity; we do not split it into individual skills.
type SkillPackage struct {
	Source string   `json:"source"`
	Ref    string   `json:"ref,omitempty"`
	Agents []string `json:"agents,omitempty"`
}

// ManifestSkill is the legacy per-skill entry, kept for migration only.
type ManifestSkill struct {
	Name      string   `json:"name"`
	Source    string   `json:"source"`
	Ref       string   `json:"ref,omitempty"`
	SkillPath string   `json:"skill_path,omitempty"`
	Agents    []string `json:"agents,omitempty"`
}
```

In `GroupConfig` (struct around line 347) add a field after `Dots`:

```go
	Skills []string `json:"skills,omitempty"`
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run TestSkillPackageJSONRoundTrip`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/skill_package_test.go
git commit -m "feat(config): add SkillPackage manifest + group skill refs"
```

---

### Task 2: ValidateRoot for packages + group skill refs

**Files:**
- Modify: `internal/config/config.go` `ValidateRoot` (skills block 586-594 + group loop)
- Test: `internal/config/skill_package_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestValidateRootSkillPackages(t *testing.T) {
	root := &config.RootConfig{
		Agents: config.AgentsConfig{Packages: []config.SkillPackage{{Source: ""}}},
		Groups: []*config.GroupConfig{{Name: "g", Skills: []string{"unknown/pkg"}}},
	}
	errs := config.ValidateRoot(root)
	var sawSource, sawRef bool
	for _, e := range errs {
		if e.Path == "$.agents.packages[0].source" {
			sawSource = true
		}
		if e.Path == `$.groups[0].skills[0]` {
			sawRef = true
		}
	}
	if !sawSource {
		t.Error("expected empty package source error")
	}
	if !sawRef {
		t.Error("expected unknown group skill ref error")
	}
}
```

- [ ] **Step 2: Run test**

Run: `go test ./internal/config/ -run TestValidateRootSkillPackages`
Expected: FAIL.

- [ ] **Step 3: Implement**

Replace the skills validation block (lines 586-594) with package validation, and collect package sources for ref-checking:

```go
	pkgSources := make(map[string]struct{}, len(cfg.Agents.Packages))
	for i, pkg := range cfg.Agents.Packages {
		path := fmt.Sprintf("$.agents.packages[%d]", i)
		if strings.TrimSpace(pkg.Source) == "" {
			errs = append(errs, ValidationError{Path: path + ".source", Message: "skill package source is required"})
			continue
		}
		pkgSources[pkg.Source] = struct{}{}
	}
```

Inside the existing `for gi, g := range cfg.Groups` loop (after the tool/dot membership checks), add group skill-ref validation:

```go
		for si, src := range g.Skills {
			if _, ok := pkgSources[src]; !ok {
				errs = append(errs, ValidationError{
					Path:    fmt.Sprintf("$.groups[%d].skills[%d]", gi, si),
					Message: fmt.Sprintf("group skill ref %q has no matching package", src),
				})
			}
		}
```

- [ ] **Step 4: Run test**

Run: `go test ./internal/config/ -run TestValidateRootSkillPackages`
Expected: PASS. Also run `go build ./...` (the old `cfg.Agents.Skills` validation is gone; ensure nothing else references it for validation).

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/skill_package_test.go
git commit -m "feat(config): validate skill packages and group skill refs"
```

---

### Task 3: Legacy skills → packages migration

**Files:**
- Modify: `internal/config/loader.go` (the normalize/migrate path that runs on load — find where other migrations live, e.g. a `migrate*`/`normalize*` function called from the loader)
- Test: `internal/config/skill_package_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestMigrateLegacySkillsToPackages(t *testing.T) {
	root := &config.RootConfig{
		Agents: config.AgentsConfig{
			Skills: []config.ManifestSkill{
				{Name: "a", Source: "o/r", Ref: "main", Agents: []string{"codex"}},
				{Name: "b", Source: "o/r"},                 // same source -> dedup
				{Name: "c", Source: "x/y", Ref: "v2"},
			},
		},
	}
	config.MigrateSkillPackages(root)
	if root.Agents.Skills != nil {
		t.Errorf("legacy Skills should be cleared, got %+v", root.Agents.Skills)
	}
	if len(root.Agents.Packages) != 2 {
		t.Fatalf("packages = %+v, want 2 deduped by source", root.Agents.Packages)
	}
	if root.Agents.Packages[0].Source != "o/r" || root.Agents.Packages[0].Ref != "main" {
		t.Errorf("first package = %+v", root.Agents.Packages[0])
	}
	// idempotent
	config.MigrateSkillPackages(root)
	if len(root.Agents.Packages) != 2 {
		t.Errorf("second migrate changed packages: %+v", root.Agents.Packages)
	}
}
```

- [ ] **Step 2: Run test**

Run: `go test ./internal/config/ -run TestMigrateLegacySkillsToPackages`
Expected: FAIL — `MigrateSkillPackages` undefined.

- [ ] **Step 3: Implement**

Add to `internal/config/config.go` (exported so the loader and tests can call it):

```go
// MigrateSkillPackages folds the legacy per-skill agents.skills manifest into
// package-level agents.packages (deduped by source, ungrouped) and clears the
// legacy field. Idempotent: a no-op once agents.skills is empty.
func MigrateSkillPackages(cfg *RootConfig) {
	if len(cfg.Agents.Skills) == 0 {
		return
	}
	seen := make(map[string]struct{}, len(cfg.Agents.Packages))
	for _, p := range cfg.Agents.Packages {
		seen[p.Source] = struct{}{}
	}
	for _, s := range cfg.Agents.Skills {
		if s.Source == "" {
			continue
		}
		if _, ok := seen[s.Source]; ok {
			continue
		}
		seen[s.Source] = struct{}{}
		cfg.Agents.Packages = append(cfg.Agents.Packages, SkillPackage{Source: s.Source, Ref: s.Ref, Agents: s.Agents})
	}
	cfg.Agents.Skills = nil
}
```

Call it from the loader's post-parse normalize path. Find where the loader returns the parsed `*RootConfig` (grep `func` in `internal/config/loader.go` for the main load function and any existing `normalize`/`migrate` call) and add `MigrateSkillPackages(cfg)` alongside the other normalizations so it runs on every load.

- [ ] **Step 4: Run test**

Run: `go test ./internal/config/ -run TestMigrateLegacySkillsToPackages`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/loader.go internal/config/skill_package_test.go
git commit -m "feat(config): migrate legacy flat skills into ungrouped packages"
```

---

### Task 4: Persist packages + group skill refs through withConfig

**Files:**
- Modify: `internal/app/app.go` `rootConfigPatchDoc` (the `topLevelKeys` patch struct — it already has `Agents config.AgentsConfig`; confirm `Groups` is included)
- Test: `internal/app/agents_packages_test.go` (create)

- [ ] **Step 1: Write the failing test**

```go
// internal/app/agents_packages_test.go
package app

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestWithConfigPersistsPackagesAndGroupRefs(t *testing.T) {
	a := newTestApp(t) // see existing app test helpers for the in-tmp-config constructor
	err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Agents.Packages = []config.SkillPackage{{Source: "o/r", Ref: "main"}}
		cfg.Groups = append(cfg.Groups, &config.GroupConfig{Name: "g", Skills: []string{"o/r"}})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.Packages) != 1 || cfg.Agents.Packages[0].Source != "o/r" {
		t.Fatalf("packages not persisted: %+v", cfg.Agents.Packages)
	}
	var found bool
	for _, g := range cfg.Groups {
		if g.Name == "g" && len(g.Skills) == 1 && g.Skills[0] == "o/r" {
			found = true
		}
	}
	if !found {
		t.Fatalf("group skill ref not persisted: %+v", cfg.Groups)
	}
	_ = context.Background()
}
```

> Inspect existing app tests for the real constructor (e.g. how `TestWithConfigPersistsAgentsSkills` builds its App). Reuse that helper instead of `newTestApp` if the name differs.

- [ ] **Step 2: Run test**

Run: `go test ./internal/app/ -run TestWithConfigPersistsPackagesAndGroupRefs`
Expected: FAIL if the patch-doc drops `Packages` or group `Skills`; PASS if already covered (the patch struct embeds full `AgentsConfig` and `Groups`). If it passes immediately, keep the test as a regression guard and skip Step 3.

- [ ] **Step 3: Implement (only if failing)**

In `internal/app/app.go`, ensure `rootConfigPatchDoc` includes `Agents config.AgentsConfig json:"agents,omitempty"` (already added previously) and `Groups []*config.GroupConfig json:"groups,omitempty"`, and that the marshal copies both from `cfg`.

- [ ] **Step 4: Run test**

Run: `go test ./internal/app/ -run TestWithConfigPersistsPackagesAndGroupRefs`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/app.go internal/app/agents_packages_test.go
git commit -m "test(app): guard package + group-skill-ref persistence"
```

---

### Task 5: Resolve packages for the active host (ungrouped + active groups)

**Files:**
- Create: `internal/app/agents_packages.go`
- Test: `internal/app/agents_packages_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestResolveSkillPackages(t *testing.T) {
	cfg := &config.RootConfig{
		Agents: config.AgentsConfig{Packages: []config.SkillPackage{
			{Source: "glob/al"},   // ungrouped -> everywhere
			{Source: "work/only"}, // in group "work"
			{Source: "home/only"}, // in group "home"
		}},
		Groups: []*config.GroupConfig{
			{Name: "box"},                                // host's own group
			{Name: "work", Skills: []string{"work/only"}},
			{Name: "home", Skills: []string{"home/only"}},
		},
		Hosts: map[string][]string{"box": {"work"}}, // host "box" activates "work"
	}
	got := resolveSkillPackages(cfg, "box")
	srcs := make([]string, len(got))
	for i, r := range got {
		srcs[i] = r.Source
	}
	// ungrouped (glob/al) + active-group (work/only); NOT home/only.
	want := map[string]bool{"glob/al": true, "work/only": true}
	if len(srcs) != 2 {
		t.Fatalf("resolved = %v, want glob/al + work/only", srcs)
	}
	for _, s := range srcs {
		if !want[s] {
			t.Errorf("unexpected resolved package %q", s)
		}
	}
}
```

- [ ] **Step 2: Run test**

Run: `go test ./internal/app/ -run TestResolveSkillPackages`
Expected: FAIL — `resolveSkillPackages` undefined.

- [ ] **Step 3: Implement**

```go
// internal/app/agents_packages.go
package app

import (
	"sort"

	"github.com/lkshrk/omni/internal/config"
)

// resolvedPackage is a package selected for the active host with its active
// group memberships (for display badges).
type resolvedPackage struct {
	config.SkillPackage
	Groups []string
}

// resolveSkillPackages returns the packages to restore on hostname: every
// ungrouped package (referenced by no group) plus every package referenced by
// a group active on the host. Deduped by source, first-seen order preserved.
func resolveSkillPackages(cfg *config.RootConfig, hostname string) []resolvedPackage {
	groupedSources := make(map[string]struct{})
	for _, g := range cfg.Groups {
		if g == nil {
			continue
		}
		for _, src := range g.Skills {
			groupedSources[src] = struct{}{}
		}
	}

	activeNames, _ := activeHostGroupNames(cfg, hostname)
	activeSet := make(map[string]struct{}, len(activeNames))
	for _, n := range activeNames {
		activeSet[n] = struct{}{}
	}
	activeRefs := make(map[string][]string) // source -> active group names
	for _, g := range cfg.Groups {
		if g == nil {
			continue
		}
		if _, ok := activeSet[g.BaseName()]; !ok {
			continue
		}
		for _, src := range g.Skills {
			activeRefs[src] = append(activeRefs[src], g.BaseName())
		}
	}

	out := make([]resolvedPackage, 0, len(cfg.Agents.Packages))
	for _, p := range cfg.Agents.Packages {
		_, grouped := groupedSources[p.Source]
		groups, active := activeRefs[p.Source]
		if grouped && !active {
			continue // grouped but no active group references it
		}
		sort.Strings(groups)
		out = append(out, resolvedPackage{SkillPackage: p, Groups: groups})
	}
	return out
}
```

- [ ] **Step 4: Run test**

Run: `go test ./internal/app/ -run TestResolveSkillPackages`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/agents_packages.go internal/app/agents_packages_test.go
git commit -m "feat(app): resolve skill packages (ungrouped everywhere + active groups)"
```

---

### Task 6: Package-level install args + restore

**Files:**
- Modify: `internal/app/agents_skills.go` (`skillAddArgs`, `restoreSkills`, `dryRunLines`, `RestoreSkills`, `SkillInstaller`, `npxInstaller`)
- Modify: `internal/app/agents_skills_test.go` (existing tests reference per-skill API)
- Test: `internal/app/agents_packages_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestSkillPackageAddArgs(t *testing.T) {
	pkg := config.SkillPackage{Source: "o/r", Ref: "main"}
	got := skillPackageAddArgs(pkg, []string{"claude-code", "codex"})
	want := []string{"skills", "add", "o/r#main", "-g", "-a", "claude-code", "codex", "-y"}
	if len(got) != len(want) {
		t.Fatalf("args = %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %v want %v", got, want)
		}
	}
}
```

- [ ] **Step 2: Run test**

Run: `go test ./internal/app/ -run TestSkillPackageAddArgs`
Expected: FAIL — `skillPackageAddArgs` undefined.

- [ ] **Step 3: Implement**

In `internal/app/agents_skills.go`, replace the per-skill installer/args with package-level. The `SkillInstaller` interface, `npxInstaller`, `skillAddArgs`, `restoreSkills`, `dryRunLines` change from `config.ManifestSkill` to `config.SkillPackage`. Note: NO `-s` flag (whole package).

```go
func skillPackageSource(pkg config.SkillPackage) string {
	if pkg.Ref != "" {
		return pkg.Source + "#" + pkg.Ref
	}
	return pkg.Source
}

// skillPackageAddArgs builds `<runner> skills add <source>[#ref] -g [-a agents...] -y`.
func skillPackageAddArgs(pkg config.SkillPackage, agents []string) []string {
	args := []string{"skills", "add", skillPackageSource(pkg), "-g"}
	if len(agents) > 0 {
		args = append(args, "-a")
		args = append(args, agents...)
	}
	return append(args, "-y")
}
```

Change `SkillInstaller.Install` to take `config.SkillPackage`; update `npxInstaller.Install` to call `skillPackageAddArgs`. Update `effectiveSkillAgents` to take `config.SkillPackage` (it reads `.Agents`; rename param). Rewrite `restoreSkills` and `dryRunLines` to iterate `[]resolvedPackage` (use `.SkillPackage`), keying results by `pkg.Source`. Update `RestoreSkills`:

```go
func (a *App) RestoreSkills(ctx context.Context, opts RestoreSkillsOptions) (RestoreSkillsResult, []string, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return RestoreSkillsResult{}, nil, err
	}
	if err := a.requireAgentsEnabled(cfg); err != nil {
		return RestoreSkillsResult{}, nil, err
	}
	runner := skillRunner(nodeManager(cfg))
	pkgs := resolveSkillPackages(cfg, currentMachineGroupName())
	use := a.effectiveSettings(cfg).AgentsUse
	if opts.DryRun {
		return RestoreSkillsResult{}, dryRunLines(runner, pkgs, use), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return RestoreSkillsResult{}, nil, fmt.Errorf("resolving home dir: %w", err)
	}
	before, err := lockHashes(home)
	if err != nil {
		return RestoreSkillsResult{}, nil, err
	}
	inst := npxInstaller{runner: runner, exec: a.fallbackExecutor().Run}
	res := restoreSkills(ctx, pkgs, use, inst)
	after, err := lockHashes(home)
	if err != nil {
		return RestoreSkillsResult{}, nil, err
	}
	res.Drift = skillDrift(before, after)
	return res, nil, nil
}
```

Where `restoreSkills`/`dryRunLines` now take `[]resolvedPackage`:

```go
func restoreSkills(ctx context.Context, pkgs []resolvedPackage, use []string, inst SkillInstaller) RestoreSkillsResult {
	var res RestoreSkillsResult
	for _, p := range pkgs {
		agents := effectiveSkillAgents(use, p.SkillPackage)
		if err := inst.Install(ctx, p.SkillPackage, agents); err != nil {
			res.Failed = append(res.Failed, SkillFailure{Name: p.Source, Message: err.Error()})
			continue
		}
		res.Installed = append(res.Installed, p.Source)
	}
	return res
}

func dryRunLines(runner string, pkgs []resolvedPackage, use []string) []string {
	lines := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		args := skillPackageAddArgs(p.SkillPackage, effectiveSkillAgents(use, p.SkillPackage))
		lines = append(lines, runner+" "+strings.Join(args, " "))
	}
	return lines
}
```

Update `effectiveSkillAgents` signature to `func effectiveSkillAgents(use []string, pkg config.SkillPackage) []string` (body unchanged except it reads `pkg.Agents`). Update existing tests in `agents_skills_test.go` (`TestRestoreSkillsAggregatesResults`, `TestRestoreSkillsOptionsDryRun`, `stubInstaller`, and the `effectiveSkillAgents` test in `agents_catalog_test.go`) to use `config.SkillPackage`/`resolvedPackage`.

- [ ] **Step 4: Run tests**

Run: `go build ./... && go test ./internal/app/`
Expected: PASS (fix any remaining `ManifestSkill` references surfaced by the build).

- [ ] **Step 5: Commit**

```bash
git add internal/app/agents_skills.go internal/app/agents_skills_test.go internal/app/agents_catalog_test.go internal/app/agents_packages_test.go
git commit -m "feat(app): package-level skill restore via resolved packages"
```

---

### Task 7: Package-level installed/updated detection

**Files:**
- Modify: `internal/app/agents_skills_rows.go` (replace `SkillRow`/`SkillRows`/`agentsWithSkill` with package-level)
- Test: `internal/app/agents_packages_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestSkillLockSourceIndex(t *testing.T) {
	lock := &config.SkillLockFile{Skills: map[string]config.SkillLockEntry{
		"foo": {Source: "o/r", UpdatedAt: "2026-06-10T00:00:00Z"},
		"bar": {Source: "o/r", UpdatedAt: "2026-06-12T00:00:00Z"},
		"baz": {Source: "x/y", UpdatedAt: "2026-05-01T00:00:00Z"},
	}}
	installed, updated := packageLockStatus(lock, "o/r")
	if !installed {
		t.Fatal("o/r should be installed")
	}
	if updated != "2026-06-12" {
		t.Errorf("updated = %q, want latest 2026-06-12", updated)
	}
	if in, _ := packageLockStatus(lock, "absent/pkg"); in {
		t.Error("absent package should not be installed")
	}
}
```

- [ ] **Step 2: Run test**

Run: `go test ./internal/app/ -run TestSkillLockSourceIndex`
Expected: FAIL — `packageLockStatus` undefined.

- [ ] **Step 3: Implement**

Rewrite `internal/app/agents_skills_rows.go`:

```go
package app

import (
	"os"

	"github.com/lkshrk/omni/internal/config"
)

// SkillPackageRow is a display row for the agents packages table.
type SkillPackageRow struct {
	Source    string
	Name      string   // repo segment of Source
	Ref       string
	Groups    []string // active group memberships (badge)
	Updated   string   // YYYY-MM-DD, latest across the package's lockfile skills
	Installed bool
}

// packageLockStatus reports whether any lockfile entry belongs to source and
// the latest updated date (YYYY-MM-DD) across those entries.
func packageLockStatus(lock *config.SkillLockFile, source string) (bool, string) {
	installed := false
	latest := ""
	for _, e := range lock.Skills {
		if e.Source != source {
			continue
		}
		installed = true
		d := skillUpdatedDate(e.UpdatedAt)
		if d > latest {
			latest = d
		}
	}
	return installed, latest
}

func packageDisplayName(source string) string {
	for i := len(source) - 1; i >= 0; i-- {
		if source[i] == '/' {
			return source[i+1:]
		}
	}
	return source
}

// SkillPackageRows builds the agents table: packages resolved for this host,
// joined with the lockfile for install status.
func (a *App) SkillPackageRows() ([]SkillPackageRow, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	lock, err := config.LoadSkillLock(config.SkillLockPath(home))
	if err != nil {
		return nil, err
	}
	resolved := resolveSkillPackages(cfg, currentMachineGroupName())
	rows := make([]SkillPackageRow, 0, len(resolved))
	for _, p := range resolved {
		installed, updated := packageLockStatus(lock, p.Source)
		rows = append(rows, SkillPackageRow{
			Source:    p.Source,
			Name:      packageDisplayName(p.Source),
			Ref:       p.Ref,
			Groups:    p.Groups,
			Updated:   updated,
			Installed: installed,
		})
	}
	return rows, nil
}
```

Keep `skillUpdatedDate` (move it here if it lived in the old file). Delete the old `SkillRow`, `SkillRows`, `agentsWithSkill` (and confirm `supportedAgents`/`agentSkillsPath` in `agents_catalog.go` are still used by `AgentPickerRows`; they are).

- [ ] **Step 4: Run tests**

Run: `go build ./... && go test ./internal/app/ -run TestSkillLockSourceIndex`
Expected: PASS. Build will flag the TUI's use of the old `SkillRows`/`SkillRow` — that is fixed in Task 10; for now the app package must compile (it will; the TUI is a separate package handled next).

- [ ] **Step 5: Commit**

```bash
git add internal/app/agents_skills_rows.go internal/app/agents_packages_test.go
git commit -m "feat(app): package-level installed/updated detection"
```

---

### Task 8: Package import (collapse lockfile by source)

**Files:**
- Modify: `internal/app/agents_skills.go` (`importSkills`, `ImportSkills`, `ImportDiff`)
- Test: `internal/app/agents_packages_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestImportPackages(t *testing.T) {
	existing := []config.SkillPackage{{Source: "o/r", Ref: "main"}}
	lock := &config.SkillLockFile{Skills: map[string]config.SkillLockEntry{
		"a": {Source: "o/r", Ref: "main"},   // unchanged
		"b": {Source: "o/r", Ref: "main"},   // same package
		"c": {Source: "new/pkg", Ref: "v1"}, // new
	}}
	merged, diff := importPackages(existing, lock)
	if len(merged) != 2 {
		t.Fatalf("merged = %+v, want 2 packages", merged)
	}
	if len(diff.Added) != 1 || diff.Added[0] != "new/pkg" {
		t.Errorf("added = %v, want [new/pkg]", diff.Added)
	}
	if len(diff.Unchanged) != 1 || diff.Unchanged[0] != "o/r" {
		t.Errorf("unchanged = %v, want [o/r]", diff.Unchanged)
	}
}
```

- [ ] **Step 2: Run test**

Run: `go test ./internal/app/ -run TestImportPackages`
Expected: FAIL — `importPackages` undefined.

- [ ] **Step 3: Implement**

Replace `importSkills` with `importPackages` in `internal/app/agents_skills.go`:

```go
// importPackages folds lockfile entries into the flat package manifest, deduped
// by source. New sources are appended ungrouped; existing sources update Ref
// when the lockfile differs.
func importPackages(existing []config.SkillPackage, lock *config.SkillLockFile) ([]config.SkillPackage, ImportDiff) {
	bySource := make(map[string]config.SkillPackage, len(existing))
	order := make([]string, 0, len(existing))
	for _, p := range existing {
		bySource[p.Source] = p
		order = append(order, p.Source)
	}

	// Collapse lockfile -> source -> ref (first seen ref wins).
	lockBySource := make(map[string]string)
	sources := make([]string, 0)
	for _, e := range lock.Skills {
		if e.Source == "" {
			continue
		}
		if _, ok := lockBySource[e.Source]; !ok {
			lockBySource[e.Source] = e.Ref
			sources = append(sources, e.Source)
		}
	}
	sort.Strings(sources)

	var diff ImportDiff
	for _, src := range sources {
		ref := lockBySource[src]
		prev, ok := bySource[src]
		switch {
		case !ok:
			bySource[src] = config.SkillPackage{Source: src, Ref: ref}
			order = append(order, src)
			diff.Added = append(diff.Added, src)
		case prev.Ref != ref:
			prev.Ref = ref
			bySource[src] = prev
			diff.Updated = append(diff.Updated, src)
		default:
			diff.Unchanged = append(diff.Unchanged, src)
		}
	}

	merged := make([]config.SkillPackage, 0, len(order))
	for _, src := range order {
		merged = append(merged, bySource[src])
	}
	return merged, diff
}
```

Update `ImportSkills` to read/write `cfg.Agents.Packages` via `importPackages` (replace the `cfg.Agents.Skills` reads). `ImportDiff` stays the same shape (now holds sources, not names).

- [ ] **Step 4: Run tests**

Run: `go build ./... && go test ./internal/app/ -run TestImportPackages`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/agents_skills.go internal/app/agents_packages_test.go
git commit -m "feat(app): package-level skill import (collapse lockfile by source)"
```

---

### Task 9: SetSkillGroups membership API

**Files:**
- Create: `internal/app/agents_membership.go`
- Test: `internal/app/agents_packages_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestSetSkillGroupsInConfig(t *testing.T) {
	cfg := &config.RootConfig{
		Agents: config.AgentsConfig{Packages: []config.SkillPackage{{Source: "o/r"}}},
		Groups: []*config.GroupConfig{
			{Name: "work", Skills: []string{"o/r"}},
			{Name: "home"},
		},
	}
	setSkillGroupsInConfig(cfg, "o/r", map[string]struct{}{"home": {}})
	work := findGroupInConfig(cfg, "work")
	home := findGroupInConfig(cfg, "home")
	if len(work.Skills) != 0 {
		t.Errorf("work should no longer reference o/r: %v", work.Skills)
	}
	if len(home.Skills) != 1 || home.Skills[0] != "o/r" {
		t.Errorf("home should reference o/r: %v", home.Skills)
	}
}
```

- [ ] **Step 2: Run test**

Run: `go test ./internal/app/ -run TestSetSkillGroupsInConfig`
Expected: FAIL — `setSkillGroupsInConfig` undefined.

- [ ] **Step 3: Implement**

Mirror `setToolGroupsInConfig` (`app_membership.go:680`):

```go
// internal/app/agents_membership.go
package app

import (
	"context"
	"slices"

	"github.com/lkshrk/omni/internal/config"
)

func setSkillGroupsInConfig(cfg *config.RootConfig, source string, groups map[string]struct{}) {
	for _, group := range cfg.Groups {
		if group == nil {
			continue
		}
		if _, keep := groups[group.BaseName()]; !keep {
			group.Skills = slices.DeleteFunc(group.Skills, func(s string) bool { return s == source })
			continue
		}
		if !slices.Contains(group.Skills, source) {
			group.Skills = append(group.Skills, source)
		}
	}
}

// SetSkillGroups persists group membership for a package source. createdGroups
// names brand-new groups to create first; activeHost auto-assigns new groups to
// the current host (mirrors SetToolGroups).
func (a *App) SetSkillGroups(source string, groups, createdGroups []string, activeHost string) error {
	return a.withConfig(func(cfg *config.RootConfig) error {
		selected := make(map[string]struct{}, len(groups))
		for _, g := range groups {
			selected[g] = struct{}{}
		}
		if err := createSelectedGroupsInConfig(cfg, createdGroups, selected); err != nil {
			return err
		}
		if err := ensureMembershipGroupsOnHostInConfig(cfg, activeHost, selected); err != nil {
			return err
		}
		setSkillGroupsInConfig(cfg, source, selected)
		return nil
	})
}

// SetSkillGroupsWithState wraps SetSkillGroups and returns refreshed rows.
func (a *App) SetSkillGroupsWithState(ctx context.Context, source string, groups, createdGroups []string, activeHost string) ([]SkillPackageRow, error) {
	if err := a.SetSkillGroups(source, groups, createdGroups, activeHost); err != nil {
		return nil, err
	}
	return a.SkillPackageRows()
}
```

> Confirm `findGroupInConfig`, `createSelectedGroupsInConfig`, `ensureMembershipGroupsOnHostInConfig` exist in `app_membership.go` (they do) and are in package `app`.

- [ ] **Step 4: Run tests**

Run: `go build ./... && go test ./internal/app/ -run 'TestSetSkillGroups'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/agents_membership.go internal/app/agents_packages_test.go
git commit -m "feat(app): SetSkillGroups package-group membership API"
```

---

### Task 10: TUI Agents table (package rows + group badge) and group picker

**Files:**
- Modify: `internal/tui/view_skills.go` (`viewSkillsBody`, `renderSkillsGrouped`)
- Modify: `internal/tui/model.go` (`skillsRows` type → `[]app.SkillPackageRow`)
- Modify: `internal/tui/commands_agents.go` (`loadSkillsManifestCmd` → `SkillPackageRows`)
- Modify: `internal/tui/update.go` / `update_list.go` (the `viewSkills` key handling: add `g` group picker entry; reuse `viewGroupMembership` with a new `pickerMembershipKind`)
- Modify: `internal/tui/update_group_picker.go` (`saveGroupMembershipPicker`: dispatch skill membership save when kind is skill)
- Test: delegated to the **tui-tester** agent (do NOT inline TUI tests — project rule)

> This task is larger; dispatch the implementer with the note that `internal/tui` test files must be written by the tui-tester agent afterward, and that the constant/field renames will ripple through `skills_test.go`.

- [ ] **Step 1: Update model + command (compile-first)**

In `internal/tui/model.go`, change `skillsRows []app.SkillRow` to `skillsRows []app.SkillPackageRow`. In `internal/tui/commands_agents.go`, change `loadSkillsManifestCmd` to call `a.SkillPackageRows()` and carry `[]app.SkillPackageRow` in its result message (rename the message field type accordingly).

- [ ] **Step 2: Rewrite renderSkillsGrouped for packages + group badge**

In `internal/tui/view_skills.go`, replace the body of `renderSkillsGrouped` to render `app.SkillPackageRow`. Keep the existing split-row layout and status grouping; add a group-badge column between `ref` and `status`. Use the row's `Name` for the left cell and `Source` for the source column.

```go
func (m Model) renderSkillsGrouped() string {
	p := m.palette
	contentW := rowAvailableWidth(m.width)
	iconGap := strings.Repeat(" ", toolIconNameGapWidth)

	skillStatusLabel := func(r app.SkillPackageRow) string {
		if r.Installed {
			return "installed"
		}
		return "missing"
	}
	groupBadge := func(r app.SkillPackageRow) string {
		if len(r.Groups) == 0 {
			return ""
		}
		return "[" + strings.Join(r.Groups, ",") + "]"
	}

	statusW, srcW, refW, grpW, updW := 0, 0, 0, 0, 0
	for _, r := range m.skillsRows {
		statusW = max(statusW, lipgloss.Width(skillStatusLabel(r)))
		srcW = max(srcW, lipgloss.Width(r.Source))
		refW = max(refW, lipgloss.Width(r.Ref))
		grpW = max(grpW, lipgloss.Width(groupBadge(r)))
		updW = max(updW, lipgloss.Width(r.Updated))
	}

	renderRow := func(r app.SkillPackageRow) string {
		var icon string
		var statusStyle lipgloss.Style
		if r.Installed {
			icon = p.styleInstalled.Render(iconInstalled)
			statusStyle = p.styleInstalled
		} else {
			icon = p.styleMissing.Render(iconMissing)
			statusStyle = p.styleMissing
		}
		left := []rowCell{leftCell(icon+iconGap+p.styleNormal.Render(r.Name), 0)}
		right := []rowCell{
			leftCell(p.styleHelp.Render(fitCellText(r.Source, srcW)), srcW),
			leftCell(p.styleHelp.Render(fitCellText(r.Ref, refW)), refW),
			leftCell(p.styleProvider.Render(fitCellText(groupBadge(r), grpW)), grpW),
			leftCell(statusStyle.Render(fitCellText(skillStatusLabel(r), statusW)), statusW),
			leftCell(p.styleVersionMuted.Render(fitCellText(r.Updated, updW)), updW),
		}
		return inactiveRowPrefix() + renderSplitRow(left, right, contentW, listColumnGap, listColumnGap)
	}

	installed := make([]app.SkillPackageRow, 0)
	missing := make([]app.SkillPackageRow, 0)
	for _, r := range m.skillsRows {
		if r.Installed {
			installed = append(installed, r)
		} else {
			missing = append(missing, r)
		}
	}
	sort.Slice(installed, func(i, j int) bool { return installed[i].Source < installed[j].Source })
	sort.Slice(missing, func(i, j int) bool { return missing[i].Source < missing[j].Source })

	var buf strings.Builder
	write := func(s string) { buf.WriteString(s) }
	sections := newListSectionWriter(p, m.width, write)
	if len(installed) > 0 {
		sections.Header("Installed")
		for _, r := range installed {
			write(renderRow(r) + "\n")
		}
	}
	if len(missing) > 0 {
		sections.Header("Not Installed")
		for _, r := range missing {
			write(renderRow(r) + "\n")
		}
	}
	return buf.String()
}
```

Update the footer line in `viewSkillsBody` to `"[g] group   [r] restore   [i] import   [u] update"`.

- [ ] **Step 3: Wire the `g` group picker**

In the `viewSkills` key handling (find where `r`/`i`/`u` are handled — `update.go` or `update_list.go`), add a `g` case that, when the cursor is on a package row, opens the membership picker for that package. Add a `pickerMembershipKind` value `pickerMembershipSkill` and store the package source in `pickerMembershipName`. Seed `pickerOriginalGroups` from the row's `Groups`. In `update_group_picker.go` `saveGroupMembershipPicker`, when kind is `pickerMembershipSkill`, dispatch a new command `doSetSkillGroupMemberships(source, original, next, created, host)` that calls `a.SetSkillGroupsWithState` and returns a message updating `m.skillsRows`.

> NOTE: the Agents tab currently has no per-row cursor (the table is non-interactive). Adding `g` requires a row cursor for the skills view. If that is out of scope for a first cut, instead expose grouping via the existing group-tab "edit group skills" surface; choose the smaller change and `log` the decision in the PR. The implementer should pick the approach that matches how dots rows gained their cursor and confirm with the controller if ambiguous.

- [ ] **Step 4: Build + existing suite**

Run: `go build ./... && go test ./internal/tui/`
Expected: build PASS; some `skills_test.go` assertions referencing `app.SkillRow` will fail to compile — those are updated by the tui-tester agent in Task 11. For THIS task, ensure non-test code compiles and `go vet ./internal/tui/` is clean.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/view_skills.go internal/tui/model.go internal/tui/commands_agents.go internal/tui/update.go internal/tui/update_group_picker.go
git commit -m "feat(tui): package-level agents table with group badge + group picker"
```

---

### Task 11: Update + add TUI tests (tui-tester agent)

**Files:**
- Modify: `internal/tui/skills_test.go` (migrate `app.SkillRow` → `app.SkillPackageRow`)
- Create: `internal/tui/skill_packages_test.go`

- [ ] **Step 1: Dispatch tui-tester**

Brief the **tui-tester** agent to: (a) update every `skills_test.go` test that constructs `app.SkillRow` to use `app.SkillPackageRow{Source,Name,Ref,Groups,Updated,Installed}`; (b) add tests covering: package rows render by `Name`+`Source`, group badge `[work,home]` appears for a row with `Groups`, status grouping still splits Installed/Not Installed, footer shows `[g] group`, and the `g` keybind opens the membership picker seeded with the row's groups (if the row cursor was added in Task 10). Run `go test ./internal/tui/ -run 'TestSkills|TestSkillPackages' -v`.

- [ ] **Step 2: Verify**

Run: `go test ./internal/tui/`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/tui/skills_test.go internal/tui/skill_packages_test.go
git commit -m "test(tui): package-level agents table + group picker coverage"
```

---

### Task 12: CLI integration smoke (txtar-writer agent)

**Files:**
- Create: `integration_tests/testdata/scripts/agents_packages.txtar`

- [ ] **Step 1: Dispatch txtar-writer**

Brief the **txtar-writer** agent to add a txtar fixture exercising the package model end-to-end against a fake `skills` runner: a config with `agents.packages` (one ungrouped, one in a non-active group), assert `omni agents skills restore --dry-run` (or the equivalent existing command) emits `skills add <source>` only for the ungrouped + active-group packages, and that a legacy `agents.skills` config migrates to `agents.packages` on load (e.g. visible via a config-dump command). Read `internal/cli` for the exact agents subcommands and flags first.

- [ ] **Step 2: Verify**

Run: `make test`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add integration_tests/testdata/scripts/agents_packages.txtar
git commit -m "test(integration): package restore + legacy migration smoke"
```

---

## Self-Review

**Spec coverage:**
- Data model (SkillPackage, Packages, GroupConfig.Skills) → Task 1. ✓
- Validation → Task 2. ✓
- Migration (ungrouped) → Task 3. ✓
- Persistence → Task 4. ✓
- Resolution (ungrouped everywhere + active groups) → Task 5. ✓
- Restore (package add, no `-s`, agent resolution) → Task 6. ✓
- Installed/updated by source → Task 7. ✓
- Import (collapse by source, ungrouped) → Task 8. ✓
- Group editing (SetSkillGroups) → Task 9 + Task 10 wiring. ✓
- Table + group badge → Task 10. ✓
- Tests (config/app inline, TUI via tui-tester, integration via txtar-writer) → Tasks 1-12. ✓
- `skills update` unchanged → not modified (correct; no task needed).
- P2 (find/add flow) → explicitly deferred to a separate plan. ✓

**Type consistency:** `SkillPackage{Source,Ref,Agents}`, `resolvedPackage{SkillPackage,Groups}`, `SkillPackageRow{Source,Name,Ref,Groups,Updated,Installed}`, `effectiveSkillAgents(use []string, pkg config.SkillPackage)`, `skillPackageAddArgs(pkg, agents)`, `resolveSkillPackages(cfg, hostname)`, `packageLockStatus(lock, source)`, `importPackages(existing, lock)`, `setSkillGroupsInConfig(cfg, source, groups)`, `SetSkillGroups(source, groups, createdGroups, activeHost)`. Names consistent across tasks.

**Open risk (flagged in Task 10):** the Agents tab may need a row cursor to support `g`. If adding one is heavy, fall back to editing skill membership from the groups surface and log the scope cut. Resolve during implementation.
