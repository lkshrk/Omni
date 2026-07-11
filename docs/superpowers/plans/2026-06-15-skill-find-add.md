# Skill Find + Add (P2) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add single agent-skill packages from omni — by typed source (`owner/repo`, github URL) or by discovery via the upstream `skills find` command — registering them in the package manifest and installing them, all from the CLI and the TUI Agents tab.

**Architecture:** A pure source-normalizer turns user input (github URL / `owner/repo` / `owner/repo@skill`) into a canonical `owner/repo`. A pure parser turns `skills find` stdout into structured results. App methods `FindSkillPackages` (runs `<runner> skills find <query>`, parses) and `AddSkillPackage` (registers an ungrouped `SkillPackage` if new, then runs `skills add <source>[#ref] -g -a <agents> -y`) build on the existing P1 package model. A new `omni agents add` CLI command and a TUI add-flow (input → optional find list → pick → add) drive them. New packages land ungrouped (restore everywhere); grouping is done later via the existing `g` picker.

**Agents-tab visual model (NEW this round):** the Agents tab is restyled to match the Tools page — a two-row pill filter bar above the list, reusing `renderPillBarFit` (see `internal/tui/view_list.go renderFilterBar`). Filter 1 = resource **type** with chips `skills` / `mcp` / `plugin`; only `skills` is populated this round — `mcp` and `plugin` render as visible-but-empty chips (selecting them shows a "coming soon / nothing tracked" body). Filter 2 = **agent** with chips `[all] claude-code codex …` (installed agents); it is **view-only** — it filters which package rows are shown by which agents currently have the package installed, and does NOT change add/restore targeting (those still use the per-host `agents_use`). This requires per-package agent presence on the row (`SkillPackageRow.Agents`).

**Tech Stack:** Go, Cobra CLI, Bubbletea/lipgloss TUI, testscript/txtar.

**Builds on P1 (already merged to main):** `config.SkillPackage{Source,Ref,Agents}`, `config.AgentsConfig.Packages`, `App.withConfig`, `skillRunner(nodeManager(cfg))`, `App.fallbackExecutor().Run(ctx, name, args...) (stdout, stderr string, err error)`, `effectiveSkillAgents(use []string, pkg config.SkillPackage)`, `a.effectiveSettings(cfg).AgentsUse`, `requireAgentsEnabled`, `skillPackageAddArgs(pkg, agents)`, `App.SkillPackageRows()`. Read `internal/app/agents_skills.go`, `internal/app/agents_packages.go`, `internal/cli/agents.go`.

**`skills find` behavior (verified, vercel-labs/skills ~v1.5.x):** command is `find` (NOT `search`). `<runner> skills find <query>` prints, per result, a line `owner/repo@skill-name  <N installs>` followed by a line `└ https://skills.sh/<slug>`. No `--json`. The install identifier is `owner/repo@skill-name`; the package identity we track is the `owner/repo` portion.

---

### Task 1: Source normalization

**Files:**
- Create: `internal/app/skill_source.go`
- Test: `internal/app/skill_source_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/app/skill_source_test.go
package app

import "testing"

func TestNormalizeSkillSource(t *testing.T) {
	cases := map[string]struct{ source, ref string }{
		"owner/repo":                                  {"owner/repo", ""},
		"owner/repo#main":                             {"owner/repo", "main"},
		"owner/repo@some-skill":                       {"owner/repo", ""},
		"owner/repo@some-skill#v2":                    {"owner/repo", "v2"},
		"https://github.com/owner/repo":               {"owner/repo", ""},
		"https://github.com/owner/repo.git":           {"owner/repo", ""},
		"https://github.com/owner/repo/tree/main":     {"owner/repo", "main"},
		"git@github.com:owner/repo.git":               {"owner/repo", ""},
		"  owner/repo  ":                              {"owner/repo", ""},
	}
	for in, want := range cases {
		gotSource, gotRef, err := normalizeSkillSource(in)
		if err != nil {
			t.Fatalf("%q: unexpected error %v", in, err)
		}
		if gotSource != want.source || gotRef != want.ref {
			t.Errorf("%q -> (%q,%q), want (%q,%q)", in, gotSource, gotRef, want.source, want.ref)
		}
	}
	for _, bad := range []string{"", "   ", "notaurl", "owner", "/repo", "owner/"} {
		if _, _, err := normalizeSkillSource(bad); err == nil {
			t.Errorf("%q: expected error, got nil", bad)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run TestNormalizeSkillSource`
Expected: FAIL — `normalizeSkillSource` undefined.

- [ ] **Step 3: Implement**

```go
// internal/app/skill_source.go
package app

import (
	"fmt"
	"regexp"
	"strings"
)

var ownerRepoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// normalizeSkillSource turns user input (owner/repo, owner/repo@skill,
// owner/repo#ref, or a github URL/scp form) into a canonical owner/repo source
// and an optional ref. The @skill component is stripped — packages are tracked
// at the repo level.
func normalizeSkillSource(in string) (source, ref string, err error) {
	s := strings.TrimSpace(in)
	if s == "" {
		return "", "", fmt.Errorf("empty skill source")
	}

	// git@github.com:owner/repo(.git)
	if strings.HasPrefix(s, "git@github.com:") {
		s = strings.TrimPrefix(s, "git@github.com:")
	}
	// https://github.com/owner/repo[/tree/<ref>][.git]
	for _, p := range []string{"https://github.com/", "http://github.com/", "github.com/"} {
		if strings.HasPrefix(s, p) {
			s = strings.TrimPrefix(s, p)
			break
		}
	}
	s = strings.TrimSuffix(s, ".git")

	// /tree/<ref> suffix sets the ref.
	if i := strings.Index(s, "/tree/"); i >= 0 {
		ref = strings.Trim(s[i+len("/tree/"):], "/")
		s = s[:i]
	}
	// #ref suffix.
	if i := strings.Index(s, "#"); i >= 0 {
		if ref == "" {
			ref = s[i+1:]
		}
		s = s[:i]
	}
	// @skill suffix — strip (repo-level identity).
	if i := strings.Index(s, "@"); i >= 0 {
		s = s[:i]
	}
	s = strings.Trim(s, "/")

	if !ownerRepoRe.MatchString(s) {
		return "", "", fmt.Errorf("cannot parse skill source %q (want owner/repo or a github URL)", in)
	}
	return s, ref, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/ -run TestNormalizeSkillSource`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/skill_source.go internal/app/skill_source_test.go
git commit -m "feat(app): normalize skill source input to owner/repo + ref"
```

---

### Task 2: `skills find` output parser

**Files:**
- Create: `internal/app/skill_find.go`
- Test: `internal/app/skill_find_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/app/skill_find_test.go
package app

import "testing"

func TestParseFindOutput(t *testing.T) {
	out := "" +
		"vercel-labs/agent-skills@find-skills  1.2k installs\n" +
		"└ https://skills.sh/find-skills\n" +
		"acme/tools@deploy  340 installs\n" +
		"└ https://skills.sh/deploy\n"
	got := parseFindOutput(out)
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2: %+v", len(got), got)
	}
	if got[0].Source != "vercel-labs/agent-skills" || got[0].Skill != "find-skills" {
		t.Errorf("result[0] = %+v", got[0])
	}
	if got[0].Installs != "1.2k installs" {
		t.Errorf("installs[0] = %q", got[0].Installs)
	}
	if got[1].Source != "acme/tools" || got[1].Skill != "deploy" {
		t.Errorf("result[1] = %+v", got[1])
	}
}

func TestParseFindOutputEmpty(t *testing.T) {
	if got := parseFindOutput("No skills found.\n"); len(got) != 0 {
		t.Errorf("want 0 results, got %+v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run TestParseFindOutput`
Expected: FAIL — `parseFindOutput` / `FindResult` undefined.

- [ ] **Step 3: Implement**

```go
// internal/app/skill_find.go
package app

import (
	"regexp"
	"strings"
)

// FindResult is one entry from `skills find`. Source is owner/repo (the package
// identity); Skill is the individual skill name within it; Installs is the raw
// install-count label for display.
type FindResult struct {
	Source   string
	Skill    string
	Installs string
}

// findLineRe matches `owner/repo@skill  <installs...>`.
var findLineRe = regexp.MustCompile(`^([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)@([A-Za-z0-9_.-]+)\s+(.*)$`)

// parseFindOutput parses ANSI-stripped `skills find` stdout. Result lines look
// like `owner/repo@skill  N installs`; the following `└ <url>` lines and any
// non-matching lines are ignored.
func parseFindOutput(out string) []FindResult {
	var results []FindResult
	for _, line := range strings.Split(out, "\n") {
		m := findLineRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		results = append(results, FindResult{Source: m[1], Skill: m[2], Installs: strings.TrimSpace(m[3])})
	}
	return results
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/ -run TestParseFindOutput`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/skill_find.go internal/app/skill_find_test.go
git commit -m "feat(app): parse skills find output into structured results"
```

---

### Task 3: FindSkillPackages app method

**Files:**
- Modify: `internal/app/skill_find.go`
- Test: `internal/app/skill_find_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestFindSkillPackagesRunsRunnerAndParses(t *testing.T) {
	var gotName string
	var gotArgs []string
	a := &App{}
	a.execFn = func(_ context.Context, name string, args ...string) (string, string, error) {
		gotName, gotArgs = name, args
		return "owner/repo@skill  5 installs\n└ https://skills.sh/skill\n", "", nil
	}
	res, err := a.findSkillPackages(context.Background(), "npx", "query words")
	if err != nil {
		t.Fatal(err)
	}
	if gotName != "npx" {
		t.Errorf("runner = %q", gotName)
	}
	want := []string{"skills", "find", "query", "words"}
	if len(gotArgs) != len(want) {
		t.Fatalf("args = %v want %v", gotArgs, want)
	}
	for i := range want {
		if gotArgs[i] != want[i] {
			t.Fatalf("args = %v want %v", gotArgs, want)
		}
	}
	if len(res) != 1 || res[0].Source != "owner/repo" {
		t.Fatalf("results = %+v", res)
	}
}
```

> This test injects a fake exec via a seam `a.execFn`. INSPECT how the app already seams its executor (`a.fallbackExecutor().Run`). If there is no settable `execFn` field, adapt the test to whatever seam exists (e.g. construct via the existing test App constructor and stub the executor the way other app tests do), and make `findSkillPackages` take a runner-exec function as a parameter so it is unit-testable without a real binary. Mirror how `npxInstaller` takes an `exec` func in `agents_skills.go`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run TestFindSkillPackages`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

Add a lower-case `findSkillPackages` that takes the runner + an exec func (testable), and an exported `FindSkillPackages` that wires the real runner + executor and loads config for the node manager:

```go
func findSkillPackages(ctx context.Context, exec func(context.Context, string, ...string) (string, string, error), runner, query string) ([]FindResult, error) {
	args := append([]string{"skills", "find"}, strings.Fields(query)...)
	stdout, stderr, err := exec(ctx, runner, args...)
	if err != nil {
		return nil, fmt.Errorf("skills find: %w: %s", err, stderr)
	}
	return parseFindOutput(stripANSISkills(stdout)), nil
}

// FindSkillPackages runs `skills find <query>` and returns parsed results.
func (a *App) FindSkillPackages(ctx context.Context, query string) ([]FindResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	if err := a.requireAgentsEnabled(cfg); err != nil {
		return nil, err
	}
	runner := skillRunner(nodeManager(cfg))
	return findSkillPackages(ctx, a.fallbackExecutor().Run, runner, query)
}
```

Add `stripANSISkills` (a small ANSI escape stripper) in this file unless the app package already has one — grep for an existing ANSI stripper and reuse it; if found, call that instead and drop `stripANSISkills`. Minimal stripper:

```go
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func stripANSISkills(s string) string { return ansiRe.ReplaceAllString(s, "") }
```

Adjust the test from Step 1 to call `findSkillPackages(ctx, fakeExec, "npx", "query words")` directly (the lower-case, exec-injected form) so it needs no App wiring. Update imports (`context`, `fmt`, `regexp`, `strings`).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/ -run 'TestFindSkillPackages|TestParseFindOutput'`
Expected: PASS. Run `go build ./...`.

- [ ] **Step 5: Commit**

```bash
git add internal/app/skill_find.go internal/app/skill_find_test.go
git commit -m "feat(app): FindSkillPackages runs skills find via the node runner"
```

---

### Task 4: AddSkillPackage app method

**Files:**
- Create: `internal/app/agents_add.go`
- Test: `internal/app/agents_add_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/app/agents_add_test.go
package app

import (
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestUpsertPackage(t *testing.T) {
	existing := []config.SkillPackage{{Source: "a/b", Ref: "main"}}
	// new source appends
	got, added := upsertPackage(existing, "c/d", "v1")
	if !added || len(got) != 2 || got[1].Source != "c/d" || got[1].Ref != "v1" {
		t.Fatalf("add new: got=%+v added=%v", got, added)
	}
	// existing source updates ref, does not duplicate
	got2, added2 := upsertPackage(existing, "a/b", "next")
	if added2 || len(got2) != 1 || got2[0].Ref != "next" {
		t.Fatalf("update existing: got=%+v added=%v", got2, added2)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run TestUpsertPackage`
Expected: FAIL — `upsertPackage` undefined.

- [ ] **Step 3: Implement**

```go
// internal/app/agents_add.go
package app

import (
	"context"
	"fmt"

	"github.com/lkshrk/omni/internal/config"
)

// upsertPackage adds source to the manifest (ungrouped) or updates its ref when
// already present. Returns the new slice and whether a new package was added.
func upsertPackage(pkgs []config.SkillPackage, source, ref string) ([]config.SkillPackage, bool) {
	for i := range pkgs {
		if pkgs[i].Source == source {
			pkgs[i].Ref = ref
			return pkgs, false
		}
	}
	return append(pkgs, config.SkillPackage{Source: source, Ref: ref}), true
}

// AddSkillPackage registers a package in the manifest (ungrouped) and installs
// it via the skills CLI. input may be owner/repo, owner/repo@skill, owner/repo#ref,
// or a github URL; the @skill component is stripped (repo-level identity).
func (a *App) AddSkillPackage(ctx context.Context, input string) (config.SkillPackage, error) {
	source, ref, err := normalizeSkillSource(input)
	if err != nil {
		return config.SkillPackage{}, err
	}
	cfg, err := a.loadConfig()
	if err != nil {
		return config.SkillPackage{}, err
	}
	if err := a.requireAgentsEnabled(cfg); err != nil {
		return config.SkillPackage{}, err
	}
	pkg := config.SkillPackage{Source: source, Ref: ref}
	agents := effectiveSkillAgents(a.effectiveSettings(cfg).AgentsUse, pkg)
	runner := skillRunner(nodeManager(cfg))

	args := skillPackageAddArgs(pkg, agents)
	if _, stderr, err := a.fallbackExecutor().Run(ctx, runner, args...); err != nil {
		return config.SkillPackage{}, fmt.Errorf("skills add %s: %w: %s", source, err, stderr)
	}

	if err := a.withConfig(func(c *config.RootConfig) error {
		merged, _ := upsertPackage(c.Agents.Packages, source, ref)
		c.Agents.Packages = merged
		return nil
	}); err != nil {
		return config.SkillPackage{}, err
	}
	return pkg, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/ -run TestUpsertPackage`
Expected: PASS. Run `go build ./...`.

- [ ] **Step 5: Commit**

```bash
git add internal/app/agents_add.go internal/app/agents_add_test.go
git commit -m "feat(app): AddSkillPackage registers + installs a single package"
```

---

### Task 4b: Per-package agent presence (for the agent filter)

**Files:**
- Modify: `internal/app/agents_skills_rows.go` (`SkillPackageRow`, `SkillPackageRows`)
- Modify: `internal/app/agents_catalog.go` (re-add a skills-dir helper if absent)
- Test: `internal/app/agents_packages_test.go` (append)

- [ ] **Step 1: Write the failing test**

```go
func TestPackageAgentsPresence(t *testing.T) {
	home := t.TempDir()
	// codex has the skill installed; claude-code does not.
	if err := os.MkdirAll(filepath.Join(home, ".codex", "skills", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	lock := &config.SkillLockFile{Skills: map[string]config.SkillLockEntry{
		"demo": {Source: "o/r"},
	}}
	got := packageAgents(home, lock, "o/r")
	if len(got) != 1 || got[0] != "codex" {
		t.Fatalf("packageAgents = %v, want [codex]", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run TestPackageAgentsPresence`
Expected: FAIL — `packageAgents` undefined.

- [ ] **Step 3: Implement**

In `internal/app/agents_catalog.go`, ensure a skills-dir helper exists (re-add if P1 removed it):

```go
func agentSkillsDir(home string, a AgentInfo) string {
	return filepath.Join(agentConfigPath(home, a), a.skillsSub)
}
```

In `internal/app/agents_skills_rows.go`, add `Agents []string` to `SkillPackageRow`, and a helper that lists which installed agents have any of a package's skills on disk:

```go
// packageAgents returns the installed agents whose global skills dir contains a
// skill belonging to source (matched via the lockfile's name->source mapping).
func packageAgents(home string, lock *config.SkillLockFile, source string) []string {
	var names []string
	for name, e := range lock.Skills {
		if e.Source == source {
			names = append(names, name)
		}
	}
	var out []string
	for _, a := range InstalledAgents(home) {
		dir := agentSkillsDir(home, a)
		for _, name := range names {
			if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
				out = append(out, a.ID)
				break
			}
		}
	}
	return out
}
```

Populate it in `SkillPackageRows`: after computing `installed, updated`, set `Agents: packageAgents(home, lock, p.Source)` on the row. Add `"path/filepath"` to the imports if not present.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/ -run TestPackageAgentsPresence`
Expected: PASS. Run `go build ./...`.

- [ ] **Step 5: Commit**

```bash
git add internal/app/agents_skills_rows.go internal/app/agents_catalog.go internal/app/agents_packages_test.go
git commit -m "feat(app): per-package agent presence for the agents filter"
```

---

### Task 5: CLI `omni agents add` and `omni agents find`

**Files:**
- Modify: `internal/cli/agents.go`
- Test: `integration_tests/testdata/scripts/agents-add.txtar` (Task 8 covers the txtar; this task is the wiring + a build check)

- [ ] **Step 1: Implement the commands**

Add to `internal/cli/agents.go`, registering them on the `agents` command (sibling of `skills`):

```go
func newAgentsCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "Manage AI-agent resources (skills)",
	}
	cmd.AddCommand(
		newAgentsSkillsCmd(state),
		newAgentsAddCmd(state),
		newAgentsFindCmd(state),
	)
	return cmd
}

func newAgentsAddCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "add <source>",
		Short: "Add and install a skill package (owner/repo or github URL)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pkg, err := state.app.AddSkillPackage(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "added %s\n", pkg.Source)
			return nil
		},
	}
}

func newAgentsFindCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "find <query>",
		Short: "Search skills.sh for skill packages",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			results, err := state.app.FindSkillPackages(cmd.Context(), strings.Join(args, " "))
			if err != nil {
				return err
			}
			for _, r := range results {
				fmt.Fprintf(cmdOut(cmd), "%s  (%s)  %s\n", r.Source, r.Skill, r.Installs)
			}
			return nil
		},
	}
}
```

Add `"strings"` to the imports.

- [ ] **Step 2: Build + existing CLI tests**

Run: `go build ./... && go test ./internal/cli/`
Expected: PASS. If `internal/cli/action_catalog_test.go` or `cmd_test.go` enumerates subcommands, update those expectations (they are non-integration unit tests; adjust to include `add`/`find`).

- [ ] **Step 3: Commit**

```bash
git add internal/cli/agents.go internal/cli/action_catalog_test.go internal/cli/cmd_test.go
git commit -m "feat(cli): omni agents add/find for single skill packages"
```

---

### Task 6: TUI add + find flow on the Agents tab

**Files:**
- Modify: `internal/tui/view_skills.go`, `internal/tui/model.go`, `internal/tui/commands_agents.go`, `internal/tui/messages.go`, `internal/tui/update_keys.go` (and a new `internal/tui/update_skill_add.go` if the add-flow key handling is sizable)
- Test: delegated to the **tui-tester** agent (Task 7) — do NOT inline TUI tests

> Larger integration task; dispatch with model=opus. Mirror existing TUI patterns: the Tools filter bar (`renderFilterBar` + `renderPillBarFit` + `toolFilterHitZones` in `view_list.go`), the dots search input (`renderDotsSearchControl`/`m.settingsInput`), and the group membership picker list in `update_group_picker.go`.

- [ ] **Step 0: Tools-style dual-filter bar**

Restyle `viewSkillsBody` to render a two-row pill filter bar above the package table, matching the Tools page. Add model state: `skillTypeIdx int` (0=skills,1=mcp,2=plugin) and `skillAgentIdx int` (0=all, then one per installed agent). Render with `renderPillBarFit(p, []string{"skills","mcp","plugin"}, m.skillTypeIdx, available)` for the type row and `renderPillBarFit(p, append([]string{...installed agent IDs}), m.skillAgentIdx, available)` for the agent row (the helper prepends "all"). Wire left/right (or tab) keys to move the active filter, mirroring `m.providerTabIdx`/`m.groupTabIdx` handling for tools.
- **Type filter:** only `skills` populated. When `skillTypeIdx` is `mcp` or `plugin`, render a muted body line ("No MCP servers tracked yet." / "No plugins tracked yet.") instead of the skills table, and skip the skills sections.
- **Agent filter (view-only):** when `skillAgentIdx > 0`, filter `m.skillsRows` to rows whose `Agents` contains the selected agent ID before grouping/rendering. `all` shows everything. This does NOT affect add/restore.
- Keep the status grouping (Installed/Not Installed) and the group badge from P1 within the filtered set.

- [ ] **Step 1: Model state + entry keybind**

Add to `internal/tui/model.go`: an add-flow state — `skillAddActive bool`, `skillAddInput textinput.Model` (or reuse `m.settingsInput`), `skillFindResults []app.FindResult`, `skillFindCursor int`, `skillAddRunning bool`. In `update_keys.go` (the viewSkills handler near the `g`/`r`/`i`/`u` keys), add an `a` case that opens the add flow: set `skillAddActive = true`, focus the input. Gate on `m.agentsEnabled`.

- [ ] **Step 2: Input → submit**

While `skillAddActive`: typing edits the input; `enter` submits. On submit, decide: if the input parses as a source (contains `/` and matches `owner/repo`/github URL — call a tiny TUI-side check or just try add), dispatch `doAddSkillPackage(input)`; otherwise treat it as a find query and dispatch `doFindSkills(input)`. `esc` cancels the add flow.

- [ ] **Step 3: Find results list → pick**

When `doFindSkills` returns `skillsFoundMsg{results, err}`, render a selectable list (mirror the group-picker rows: `pickerChoiceRow` or the dots list rows) of `Source (Skill)  Installs`. up/down move `skillFindCursor`; `enter` picks → dispatch `doAddSkillPackage(results[cursor].Source)` (the package source, @skill already excluded by parsing). `esc` returns to input / closes.

- [ ] **Step 4: Add → refresh**

`doAddSkillPackage(source)` calls `a.AddSkillPackage(ctx, source)` and returns `skillAddedMsg{pkg, err}`. Its handler clears `skillAddRunning`/`skillAddActive`, surfaces errors via the existing `m.skillsErr` path, and reloads rows (dispatch `loadSkillsManifestCmd()` so the new package appears). Add the commands in `commands_agents.go` and message types in `messages.go`, mirroring `doRestoreSkills`/`skillsRestoredMsg`.

- [ ] **Step 5: View + footer**

In `view_skills.go`, render the add input (when `skillAddActive` and no results yet) and the find-results list (when results present), above the table. Footer already shows `[a] add` per the spec — ensure the footer includes `[a] add` alongside `[g] group [r] restore [i] import [u] update`. Use `screenEdgeInset()`/existing styles for consistency.

- [ ] **Step 6: Build + vet**

Run: `go build ./... && go vet ./internal/tui/`
Expected: PASS/clean. (TUI tests come in Task 7.)

- [ ] **Step 7: Commit**

```bash
git add internal/tui/
git commit -m "feat(tui): add skill packages by source or skills find discovery"
```

---

### Task 7: TUI add/find tests (tui-tester agent)

**Files:**
- Create: `internal/tui/skill_add_test.go`

- [ ] **Step 1: Dispatch tui-tester**

Brief the **tui-tester** agent to cover: `a` opens the add flow (`skillAddActive == true`); submitting a source string dispatches the add command and on `skillAddedMsg` the flow closes + error surfaces on failure; submitting a non-source query dispatches find and `skillsFoundMsg` populates `skillFindResults`; cursor moves over results and `enter` selects the package source; `esc` cancels; the footer shows `[a] add`. ALSO cover the dual-filter bar: the type pill bar renders `skills`/`mcp`/`plugin`; selecting `mcp`/`plugin` shows the empty body (no skills table); the agent pill bar renders `all` + installed agent IDs; selecting an agent filters the table to rows whose `Agents` includes that ID (build `skillsRows` with mixed `Agents` and assert filtered render). Use existing helpers (`baseModel`, `drive`, key helpers, `stripANSIEscapeSequences`). Run `go test ./internal/tui/ -run 'TestSkillAdd|TestSkillFilter' -v`.

- [ ] **Step 2: Verify + commit**

Run: `go test ./internal/tui/`
Expected: PASS.

```bash
git add internal/tui/skill_add_test.go
git commit -m "test(tui): cover skill add + find flow"
```

---

### Task 8: CLI integration smoke (txtar-writer agent)

**Files:**
- Create: `integration_tests/testdata/scripts/agents-add.txtar`

- [ ] **Step 1: Dispatch txtar-writer**

Brief the **txtar-writer** agent to add a fixture for `omni agents add` using a FAKE `skills`/node runner registered in the testscript main (mirror how the existing `agents-skills-restore-dryrun.txtar` / the fake-exec fixtures stub external commands — read `integration_tests/cli_test.go` `RunMain` for available fakes). Assert: `omni agents add owner/repo` invokes `skills add owner/repo -g ... -y` (against the fake) and that `owner/repo` is then present in the written `settings.json` `agents.packages` (e.g. via a config-dump command or by re-reading the file). If the runner cannot be faked in txtar, assert the source-normalization + manifest write path via whatever seam the harness supports, and note the limitation. Read `internal/cli` for the exact `agents add` command/flags.

- [ ] **Step 2: Verify + commit**

Run: `go test -tags integration ./integration_tests/ -run TestCLI/agents-add -v`
Expected: PASS.

```bash
git add integration_tests/testdata/scripts/agents-add.txtar
git commit -m "test(integration): omni agents add smoke"
```

---

## Self-Review

**Spec coverage** (against the spec's "Add / find flow" section):
- Typed source (owner/repo, github URL, #ref, @skill stripped) → Task 1 + Task 4. ✓
- `skills find` discovery (parse `owner/repo@skill  N installs`) → Task 2 + Task 3. ✓
- Whole-package install (`skills add <source> -g -a … -y`, no `-s`) → Task 4 (reuses `skillPackageAddArgs`). ✓
- New package ungrouped by default → Task 4 (`upsertPackage` appends without groups). ✓
- TUI `a` add flow (input → find list → pick → add) + footer `[a] add` → Task 6. ✓
- Tools-style dual-filter bar (type chips skills/mcp/plugin, mcp+plugin stubbed; agent chips view-only) → Task 6 Step 0. ✓
- Per-package agent presence (powers the agent filter) → Task 4b. ✓
- CLI parity (`agents add`/`find`) → Task 5. ✓
- Tests: app unit (Tasks 1-4b), CLI (Task 5), TUI incl. filters (Task 7), integration (Task 8). ✓
- Group assignment is NOT part of add (done later via `g`) — matches spec; no task needed. ✓
- mcp/plugin are filter stubs only this round (chips render, bodies empty); full mcp/plugin management is a future plan. ✓

**Placeholder scan:** none — every code step has concrete code; the two delegated tasks (7, 8) name the agent and exact assertions.

**Type consistency:** `FindResult{Source,Skill,Installs}`, `normalizeSkillSource(in) (source, ref, err)`, `parseFindOutput(out) []FindResult`, `findSkillPackages(ctx, exec, runner, query)`, `FindSkillPackages(ctx, query)`, `upsertPackage(pkgs, source, ref) ([]config.SkillPackage, bool)`, `AddSkillPackage(ctx, input) (config.SkillPackage, error)`. Reused P1 symbols (`skillPackageAddArgs`, `effectiveSkillAgents`, `skillRunner`, `nodeManager`, `SkillPackageRows`) match their merged signatures.

**Open risk:** Task 3's exec seam and Task 8's fake-runner depend on the existing executor/testscript seams — both tasks instruct the implementer to inspect and adapt to the real seam rather than assume. Task 6's source-vs-query disambiguation in the TUI should reuse the same `normalizeSkillSource` (try-parse) rather than a second heuristic.
