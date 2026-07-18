# Config Optimize: `omni doctor --fix`

**Date:** 2026-07-17
**Status:** Approved

## Problem

`omni doctor` reports two classes of config hygiene warnings with no auto-fix:

1. **Merge-notice duplicates** (`config.MergeNotices`, surfaced in the doctor
   config check): a group's dot entry (or other list entry) is defined in both
   the parent `settings.json` and an `$include` fragment. The fragment's
   definition silently wins (`includeMergeNotices`,
   `internal/config/config_merge.go`), so the parent copy is dead weight the
   user must remove by hand. A typical extracted config produces dozens of
   these warnings at once.
2. **Ignore-pattern issues** (`dots.ignore` check,
   `internal/app/dots_audit.go`): redundant, contradictory, or dead ignore
   globs on dot entries. A fixer already exists
   (`App.DotsFixIgnorePatterns`) but is only reachable through the dashboard
   reconcile flow.

## Goal

`omni doctor --fix` applies safe, provably behavior-preserving fixes for
fixable checks, in the CLI and via a fix action in the TUI doctor tab.

## Non-goals

- No semantic changes to the effective (merged) config — ever.
- No reordering or reformatting of files beyond the minimal edits.
- No new standalone `optimize` command; doctor owns report + fix.

## Design

### 1. Include-chain dedupe (`internal/config`)

New entry point:

```go
// OptimizeIncludeChain removes entries from earlier files in the $include
// chain that are overridden by identically-named entries in later files.
// Returns a report of removals per file; with dryRun no file is written.
func OptimizeIncludeChain(mainPath string, dryRun bool) (*OptimizeReport, error)
```

- Operates on the raw JSON chain via `loadIncludeChain`
  (`internal/config/routed_write.go`), the same machinery routed writes use —
  preserves foreign keys and touches only files that change.
- Winner rule mirrors merge semantics exactly: later file in merge order wins.
  For each group present in two files, remove from the **earlier** file any
  entry whose name/identity also appears in the later file. Covered group
  keys: `dots`, `tools`, `skills`, `mcpServers`, `plugins`, `marketplaces`,
  `taps`.
- Applies between parent and fragment **and** between two fragments (same
  later-wins rule `loadIncludes` applies).
- A group object left with no keys besides its name after dedupe is removed
  from the earlier file entirely.
- `OptimizeReport` lists, per file: group, key, removed entry names — feeds
  both `--dry-run` output and the post-fix summary.

### 2. Ignore-pattern cleanup (existing)

Reuse `App.DotsFixIgnorePatterns` (`internal/app/dots_audit.go`) unchanged;
additionally dedupe + sort each entry's `ignore` list while cleaning.

### 3. Safety: equivalence check

Before writing anything, `OptimizeIncludeChain`:

1. Loads the current effective config (full merge).
2. Applies planned edits to in-memory copies of the chain files.
3. Re-merges from the edited copies and compares (normalized JSON of the
   merged `RootConfig`) against step 1.
4. Any difference → abort with an error, write nothing.

Dry-run performs the same check and reports planned removals only.

### 4. CLI: `omni doctor --fix [--dry-run]`

Flow in `internal/cli/doctor.go`:

1. Run doctor as today.
2. If `--fix`: apply fixers for fixable findings —
   merge-notice dedupe (`OptimizeIncludeChain`) and `dots.ignore`
   cleanup (`DotsFixIgnorePatterns`).
3. Rerun doctor; print per-file fix summary
   (e.g. `removed 27 duplicate dot entries from settings.json`) followed by
   the fresh doctor report.
4. Exit code semantics unchanged: nonzero only on remaining failures.
5. `--fix --dry-run`: print planned changes, write nothing, skip rerun.

### 5. TUI: fix action on doctor tab

- When the current doctor result has fixable findings (merge notices or
  `dots.ignore` warn), the action line reads
  `enter rerun doctor · f fix issues`.
- `f` dispatches a `tea.Cmd` running the same fix path (pattern:
  `startDoctorRun` / `commands_admin.go`), then auto-reruns doctor.
- Respects the shared status-stream guard (claim only when free), matching
  the concurrency-hardening pattern from the recent stream-race fix.

## Error handling

- Unreadable/unparseable chain file: abort whole fix, report file, change
  nothing (consistent with `loadIncludeChain`'s missing-fragment behavior).
- Equivalence check failure: abort with explicit message naming the check.
- Partial applicability (e.g. ignore fix succeeds, dedupe aborts): report
  each fixer's outcome separately; fixers are independent.

## Testing

- **Unit (config):** dedupe across parent+fragment, fragment+fragment,
  empty-group removal, foreign keys untouched, formatting-stable writes,
  report contents. Golden before/after JSON fixtures.
- **Property-style:** for every fixture, merged config before == after.
- **Unit (app/cli):** `--fix` flow ordering (fix → rerun), `--dry-run`
  writes nothing, exit codes.
- **TUI:** `f` binding visible only with fixable findings; fix command
  triggers doctor rerun; stream guard respected.
