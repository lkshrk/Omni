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
	DotStateModified          DotState = "modified"
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
	dotLocalModified
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
		case dotLocalModified:
			return DotStateModified, syncableDotActions()
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
		sourceInfo, sourceErr := os.Lstat(e.SourcePath)
		if info.IsDir() {
			if sourceErr == nil && sourceInfo.IsDir() && sourceInfo.Mode()&os.ModeSymlink == 0 {
				return dotLocalState{kind: inspectManagedDotDirectory(e)}
			}
		}
		if sourceErr == nil && localFileIsNewer(sourceInfo, info) {
			return dotLocalState{kind: dotLocalModified}
		}
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

func inspectManagedDotDirectory(e dots.ResolvedEntry) dotLocalKind {
	kind := dotLocalExpectedLink
	rootMatches := sameResolvedPath(e.TargetPath, e.SourcePath)
	sawNonIgnoredPath := false
	sawLinkedManagedPath := false
	walkErr := filepath.WalkDir(e.SourcePath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			kind = dotLocalContent
			return err
		}
		rel, relErr := filepath.Rel(e.SourcePath, path)
		if relErr != nil {
			kind = dotLocalContent
			return relErr
		}
		if rel == "." {
			return nil
		}
		if shouldIgnoreDotPath(e.SourcePath, rel, d.Name(), combinedDotIgnores(e.Ignore)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		sawNonIgnoredPath = true

		srcInfo, infoErr := os.Lstat(path)
		if infoErr != nil {
			kind = dotLocalContent
			return infoErr
		}
		targetPath := filepath.Join(e.TargetPath, rel)
		targetInfo, targetErr := os.Lstat(targetPath)
		if os.IsNotExist(targetErr) {
			if kind == dotLocalExpectedLink {
				kind = dotLocalMissing
			}
			if srcInfo.IsDir() && srcInfo.Mode()&os.ModeSymlink == 0 {
				return filepath.SkipDir
			}
			return nil
		}
		if targetErr != nil {
			kind = dotLocalContent
			return targetErr
		}
		if sameResolvedPath(targetPath, path) {
			sawLinkedManagedPath = true
			return nil
		}
		if srcInfo.IsDir() && srcInfo.Mode()&os.ModeSymlink == 0 {
			if targetInfo.IsDir() && targetInfo.Mode()&os.ModeSymlink == 0 {
				return nil
			}
		}
		if targetInfo.Mode()&os.ModeSymlink == 0 {
			if localFileIsNewer(srcInfo, targetInfo) {
				if kind == dotLocalExpectedLink || kind == dotLocalMissing || kind == dotLocalModified {
					kind = dotLocalModified
				}
				return nil
			}
			kind = dotLocalContent
			if srcInfo.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target, readErr := os.Readlink(targetPath)
		if readErr != nil {
			if kind != dotLocalContent {
				kind = dotLocalBrokenLink
			}
			return nil
		}
		absTarget := target
		if !filepath.IsAbs(absTarget) {
			absTarget = filepath.Clean(filepath.Join(filepath.Dir(targetPath), absTarget))
		}
		if sameCleanPath(absTarget, path) {
			sawLinkedManagedPath = true
			return nil
		}
		if pathExists(absTarget) {
			kind = dotLocalWrongLink
			return filepath.SkipAll
		}
		if kind != dotLocalContent && kind != dotLocalWrongLink {
			kind = dotLocalBrokenLink
		}
		return nil
	})
	if walkErr != nil && kind == dotLocalExpectedLink {
		return dotLocalContent
	}
	if kind == dotLocalExpectedLink || kind == dotLocalMissing || kind == dotLocalModified {
		hasLocalAdditions, additionErr := walkLocalOnlyDotFiles(e, nil)
		if additionErr != nil {
			return dotLocalContent
		}
		if hasLocalAdditions {
			kind = dotLocalModified
		}
	}
	if !rootMatches && kind != dotLocalModified && (!sawNonIgnoredPath || !sawLinkedManagedPath) {
		return dotLocalContent
	}
	return kind
}

func localFileIsNewer(sourceInfo, targetInfo os.FileInfo) bool {
	return sourceInfo.Mode().IsRegular() &&
		targetInfo.Mode().IsRegular() &&
		targetInfo.ModTime().After(sourceInfo.ModTime())
}

func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func sameCleanPath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

func sameResolvedPath(a, b string) bool {
	if sameCleanPath(a, b) {
		return true
	}
	resolvedA, errA := filepath.EvalSymlinks(a)
	resolvedB, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && sameCleanPath(resolvedA, resolvedB)
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
	case DotStateMissing, DotStateBroken, DotStateModified, DotStateLocalOnly, DotStateRepoOnly, DotStateUntrackedLinked:
		return HealthMissing
	default:
		return DotHealth(state)
	}
}
