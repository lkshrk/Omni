package app

import (
	"os"
	"path/filepath"

	"github.com/lkshrk/omni/internal/dots"
)

// DotState is the app-layer state for a dots entry. It is the source of truth
// for CLI/TUI actions; DotHealth is only derived for current renderers.
type DotState string

const (
	DotStateSynced            DotState = "synced"
	DotStateMissing           DotState = "missing"
	DotStateBroken            DotState = "broken"
	DotStateConflict          DotState = "conflict"
	DotStateLocalOnly         DotState = "local-only"
	DotStateRepoOnly          DotState = "repo-only"
	DotStateNoSource          DotState = "no-source"
	DotStateUntrackedLinked   DotState = "untracked-linked"
	DotStateUntrackedConflict DotState = "untracked-conflict"
	DotStateIgnored           DotState = "ignored"
	DotStateInactive          DotState = "inactive"
	DotStateDisabled          DotState = "disabled"
	DotStateAmbiguous         DotState = "ambiguous"
)

// DotAction is a durable action that can be exposed consistently by CLI/TUI.
type DotAction string

const (
	DotActionSync     DotAction = "sync"
	DotActionUseRepo  DotAction = "use-repo"
	DotActionUseLocal DotAction = "use-local"
	DotActionRemove   DotAction = "remove"
	DotActionIgnore   DotAction = "ignore"
	DotActionUnignore DotAction = "unignore"
	DotActionActivate DotAction = "activate"
	DotActionEnable   DotAction = "enable"
)

type dotLocalKind int

const (
	dotLocalMissing dotLocalKind = iota
	dotLocalExpectedLink
	dotLocalWrongLink
	dotLocalBrokenLink
	dotLocalContent
)

type dotLocalState struct {
	kind dotLocalKind
}

func classifyDotEntry(e dots.ResolvedEntry) (DotState, []DotAction) {
	if e.Ignored {
		return DotStateIgnored, []DotAction{DotActionUnignore, DotActionRemove}
	}

	sourceExists := pathExists(e.SourcePath)
	local := inspectDotLocal(e)

	if sourceExists {
		switch local.kind {
		case dotLocalMissing:
			return DotStateMissing, syncableDotActions()
		case dotLocalExpectedLink:
			return DotStateSynced, trackedHealthyDotActions()
		case dotLocalBrokenLink:
			return DotStateBroken, syncableDotActions()
		default:
			return DotStateConflict, conflictDotActions()
		}
	}

	switch local.kind {
	case dotLocalContent, dotLocalWrongLink:
		return DotStateLocalOnly, syncableDotActions()
	default:
		return DotStateNoSource, noSourceDotActions()
	}
}

func inspectDotLocal(e dots.ResolvedEntry) dotLocalState {
	info, err := os.Lstat(e.TargetPath)
	if os.IsNotExist(err) {
		return dotLocalState{kind: dotLocalMissing}
	}
	if err != nil {
		return dotLocalState{kind: dotLocalContent}
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return dotLocalState{kind: dotLocalContent}
	}

	target, err := os.Readlink(e.TargetPath)
	if err != nil {
		return dotLocalState{kind: dotLocalBrokenLink}
	}
	absTarget := target
	if !filepath.IsAbs(absTarget) {
		absTarget = filepath.Clean(filepath.Join(filepath.Dir(e.TargetPath), absTarget))
	}
	if sameCleanPath(absTarget, e.SourcePath) {
		return dotLocalState{kind: dotLocalExpectedLink}
	}
	if pathExists(absTarget) {
		return dotLocalState{kind: dotLocalWrongLink}
	}
	return dotLocalState{kind: dotLocalBrokenLink}
}

func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func sameCleanPath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

func syncableDotActions() []DotAction {
	return []DotAction{DotActionSync, DotActionRemove, DotActionIgnore}
}

func trackedHealthyDotActions() []DotAction {
	return []DotAction{DotActionRemove, DotActionIgnore}
}

func conflictDotActions() []DotAction {
	return []DotAction{DotActionUseRepo, DotActionUseLocal, DotActionRemove, DotActionIgnore}
}

func noSourceDotActions() []DotAction {
	return []DotAction{DotActionRemove, DotActionIgnore}
}

func healthForDotState(state DotState) DotHealth {
	switch state {
	case DotStateSynced:
		return HealthOK
	case DotStateConflict, DotStateUntrackedConflict:
		return HealthConflict
	case DotStateNoSource:
		return HealthNoSource
	case DotStateMissing, DotStateBroken, DotStateLocalOnly, DotStateRepoOnly, DotStateUntrackedLinked:
		return HealthMissing
	default:
		return DotHealth(state)
	}
}
