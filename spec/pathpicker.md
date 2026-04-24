# Path Picker Replacement Plan

## Summary
Replace the current `bubbles/filepicker` popup with a focused internal path picker built from official Charm primitives.

The target UX is an editable path input with autocomplete-style suggestions, fuzzy/path filtering, tab completion, parent navigation, visible matches, and the existing omni two-step confirmation flow.

## Decision
Do not add `github.com/pgavlin/picky` initially.

`picky` is the closest library match, but it is young, untagged, and weakly adopted. Omni prefers popular, actively maintained dependencies, especially in the TUI core. Build a narrow internal component on `charm.land/bubbles/v2/textinput` and ordinary filesystem reads instead.

Keep `picky` as a reference point for behavior only:
- Fuzzy subsequence filtering.
- Path-based input.
- Tab completion by longest common prefix.
- Directory selection with an explicit key.
- Testability with isolated filesystems.

## Current Integration Points
- `internal/tui/model.go`
  - Replace `dotsFilePicker filepicker.Model` with internal path picker state.
  - Keep existing flags: `showFilePicker`, `filePickerTitle`, `filePickerAllowFiles`, `filePickerPending`, `filePickerForDotAdd`.
- `internal/tui/commands.go`
  - Replace `openFilePicker` setup logic.
  - Preserve starting path behavior: existing dir opens directly, nonexistent path starts at parent, empty starts at home.
- `internal/tui/update_file_picker.go`
  - Route key and non-key picker messages through the new component.
  - Preserve Enter-to-stage, Enter-to-accept, Esc-to-clear/close, and timeout behavior.
- `internal/tui/view.go`
  - Keep shared popup frame and footer hint rendering.
  - Render new picker body inside existing popup shell.
- `internal/tui/view_hints.go`
  - Update browse hints for text input, completion, parent navigation, and row selection.

## Component Shape
Add `internal/tui/path_picker.go` in package `tui`.

Proposed model:
```go
type pathPickerModel struct {
	input textinput.Model
	cwd string
	entries []pathPickerEntry
	filtered []pathPickerEntry
	cursor int
	width int
	height int
	allowFiles bool
	showHidden bool
	err error
}

type pathPickerEntry struct {
	name string
	path string
	isDir bool
}
```

Keep it package-private until it proves reusable outside current TUI flows.

## Behavior
1. Opening
   - Resolve `~` and relative paths where appropriate.
   - If current path is an existing directory, set input and `cwd` to that directory.
   - If current path is a file or nonexistent path, use its parent as `cwd` and prefill the full desired path in the input when useful.
   - Default to home.

2. Text input
   - Typing updates the input value and recomputes matches.
   - Absolute paths and `~/` paths should work.
   - Relative input is interpreted relative to `cwd`.
   - Pasting a path should update suggestions and selectable rows.

3. Suggestions and filtering
   - Match entries under the resolved parent directory.
   - Rank exact prefix before fuzzy subsequence matches.
   - Directories appear before files, then alphabetical ascending.
   - Respect `allowFiles`; disabled files should not be selectable when false.

4. Completion
   - `tab` completes to the longest common prefix for matches.
   - If there is exactly one directory match and the completed path ends at that directory, descend or append a path separator.
   - Do not steal `tab` from global tab navigation while the picker is open; picker owns it.

5. Navigation
   - Up/down moves within matches.
   - `h`, `backspace`, or `left` moves to parent when the input is not actively deleting text at a meaningful cursor position.
   - `l` or `right` descends into the highlighted directory.
   - Enter stages the highlighted valid path, or the typed path when it exists and is allowed.

6. Confirmation
   - Preserve `filePickerPending`.
   - First Enter stages.
   - Second Enter accepts and calls existing `acceptPendingFilePickerPath`.
   - Esc clears pending if present, otherwise closes picker without saving.

## Tests
Update or add focused tests in `internal/tui`:
- Opening settings/setup/dots path picker still works.
- Empty current path starts at home or a test-controlled home.
- Existing directory starts in that directory.
- Nonexistent path starts at parent.
- Typing filters visible rows.
- Tab completes common prefix.
- Enter stages typed existing path.
- Enter stages highlighted directory.
- Esc clears pending, then closes.
- Popup height remains bounded.
- Footer hints do not wrap and stay in shared popup shell.

Run:
```sh
rtk go test ./internal/tui
rtk go test ./...
```

Use `make test-integration` only if app-level dots/settings behavior changes beyond TUI routing.

## Rollout Steps
1. Introduce `pathPickerModel` and tests for filtering/completion in isolation.
2. Swap model/import fields from `filepicker.Model` to `pathPickerModel`.
3. Port `openFilePicker`, update routing, and render logic.
4. Update hints and affected view/flow tests.
5. Run unit tests and fix regressions.
6. Commit as `feat(tui): replace file picker with path input`.

## Non-Goals
- No generic file manager.
- No file operations such as create/delete/rename/copy.
- No multi-select.
- No dependency on untagged path picker libraries unless internal implementation proves too costly.
