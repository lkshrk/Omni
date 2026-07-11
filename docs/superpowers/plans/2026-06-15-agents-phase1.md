# Agents Phase 1 — Harden & Merge

> REQUIRED SUB-SKILL: superpowers:subagent-driven-development.

**Goal:** Add the per-row skill list under the highlighted package, backfill tests for the recent live-testing fixes, then merge `feat/skill-find-add` into main.

**Branch:** `feat/skill-find-add` (current). All commits stack here.

---

### Task 1: Per-package skill names from the lockfile (app)

**Files:**
- Modify: `internal/app/agents_skills_rows.go` (`SkillPackageRow`, `SkillPackageRows`, add `packageSkills`)
- Test: `internal/app/agents_packages_test.go` (append)

- [ ] **Step 1: failing test**

```go
func TestPackageSkills(t *testing.T) {
	lock := &config.SkillLockFile{Skills: map[string]config.SkillLockEntry{
		"beta":  {Source: "o/r"},
		"alpha": {Source: "o/r"},
		"other": {Source: "x/y"},
	}}
	got := packageSkills(lock, "o/r")
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("packageSkills = %v, want [alpha beta] (sorted)", got)
	}
	if len(packageSkills(lock, "absent/pkg")) != 0 {
		t.Error("absent package should have no skills")
	}
}
```

- [ ] **Step 2:** `go test ./internal/app/ -run TestPackageSkills` → FAIL (undefined).

- [ ] **Step 3: implement**

Add `Skills []string` to `SkillPackageRow` (after `Agents`). Add:

```go
// packageSkills returns the lockfile skill names belonging to source, sorted.
func packageSkills(lock *config.SkillLockFile, source string) []string {
	var names []string
	for name, e := range lock.Skills {
		if e.Source == source {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
```

In `SkillPackageRows`, set `Skills: packageSkills(lock, p.Source)` on each row. Add `"sort"` import.

- [ ] **Step 4:** `go test ./internal/app/ -run TestPackageSkills` → PASS; `go build ./...`.

- [ ] **Step 5: commit**

```bash
git add internal/app/agents_skills_rows.go internal/app/agents_packages_test.go
git commit -m "feat(agents): expose per-package skill names from the lockfile"
```

---

### Task 2: Render the skill list under the highlighted row (TUI)

**Files:**
- Modify: `internal/tui/view_skills.go` (`makeLocalRow` details)
- Test: delegated to tui-tester (Task 3)

- [ ] **Step 1:** In `makeLocalRow`, when `selected` and `r.Installed` and `len(r.Skills) > 0`, prepend a skill-list detail line above the existing `renderContextHints` line. Mirror the Tools description style (`p.styleHelp`, `listHintPrefixWithGap(listWideIconGapWidth)` indent). Format: the skill names joined `, ` (truncate to width). Result: selected row shows `<skills>` then the `g group • r restore …` hint beneath it.

Concretely, change the `details` build:
```go
var details []string
if selected {
	if r.Installed && len(r.Skills) > 0 {
		skills := strings.Join(r.Skills, ", ")
		details = append(details, hintPrefix+p.styleHelp.Render(fitCellText(skills, max(contentW-lipgloss.Width(hintPrefix), 1))))
	}
	details = append(details, renderContextHints(m, hintCtxSkillsRow, hintPrefix))
}
```
(Use the existing `hintPrefix`/`contentW` in scope. If `lipgloss`/`fitCellText` widths need tweaking, match the Tools description line in `view_list.go`.)

- [ ] **Step 2:** `go build ./... && go vet ./internal/tui/` → green. Manually eyeball via a throwaway render if helpful (delete after).

- [ ] **Step 3: commit**

```bash
git add internal/tui/view_skills.go
git commit -m "feat(tui): list a package's skills under the highlighted Agents row"
```

---

### Task 3: Backfill tests for recent fixes (tui-tester)

**Files:**
- Modify/create: `internal/tui/` test files

- [ ] Dispatch **tui-tester** to cover (these landed without dedicated tests):
  1. **cursorHidden**: a fresh `viewSkills` model with `cursorHidden=true` renders NO selected-row marker (`>` absent / no row highlighted); after a nav key reveals it.
  2. **search placement**: with `skillsSearchActive=true`, `viewSkillsBody` renders the `/` search line ABOVE the `skills mcp plugin` pill bar (assert line order).
  3. **search spinner**: submitting a find query sets `m.searching=true`; `skillsFoundMsg` clears it (assert flag transitions).
  4. **intent-based agents**: `SkillPackageRow.Agents` comes from configured targets — at the app level, a package with `Agents:["codex"]` yields a row `Agents` of `["codex"]` regardless of filesystem (unit test on `SkillPackageRows` via a test App, or assert `effectiveSkillAgents` wiring).
  5. **per-row skill list** (Task 2): selected installed row with `Skills` shows the joined skill names in its details, above the hint line.
- [ ] `go test ./internal/tui/` green. Commit.

---

### Task 4: Review, rebase, merge

- [ ] Final `code-reviewer` pass over `main..HEAD` of `feat/skill-find-add`.
- [ ] Address blocking findings.
- [ ] `go test ./...` + integration `agents-*` fixtures green.
- [ ] Rebase `feat/skill-find-add` onto latest `main` (FF if main unchanged), then fast-forward merge into `main`. Do NOT push without explicit user OK.

---

## Self-Review
- Per-row skill list → Task 1 (data) + Task 2 (render). ✓
- Tests for recent fixes → Task 3. ✓
- Merge → Task 4. ✓
- `packageSkills` (lockfile by source, sorted) is the only new app symbol; `SkillPackageRow.Skills` the only new field. Consistent.
