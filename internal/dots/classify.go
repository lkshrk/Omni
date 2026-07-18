package dots

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// State is the entry-level state for a dots entry. It is the source of truth
// for CLI/TUI actions; DotHealth is only derived for current renderers.
type State string

// State values enumerate the possible lifecycle states of a dots entry.
const (
	StateSynced            State = "synced"
	StateMissing           State = "missing"
	StateBroken            State = "broken"
	StateConflict          State = "conflict"
	StateModified          State = "modified"
	StateLocalOnly         State = "local-only"
	StateRepoOnly          State = "repo-only"
	StateNoSource          State = "no-source"
	StateUntrackedLinked   State = "untracked-linked"
	StateUntrackedConflict State = "untracked-conflict"
	StateIgnored           State = "ignored"
	StateInactive          State = "inactive"
	StateDisabled          State = "disabled"
	StateAmbiguous         State = "ambiguous"
)

// Action is a durable action that can be exposed consistently by CLI/TUI.
type Action string

// Action values enumerate the actions a dots entry may expose.
const (
	ActionSync     Action = "sync"
	ActionUseRepo  Action = "use-repo"
	ActionUseLocal Action = "use-local"
	ActionRemove   Action = "remove"
	ActionIgnore   Action = "ignore"
	ActionUnignore Action = "unignore"
	ActionActivate Action = "activate"
	ActionEnable   Action = "enable"
)

// LocalKind classifies the on-disk shape of a dots entry's target path.
type LocalKind int

// LocalKind values describe how the local target relates to the repo source.
const (
	LocalMissing LocalKind = iota
	LocalExpectedLink
	LocalWrongLink
	LocalBrokenLink
	LocalContent
	LocalModified
	LocalAllIgnored
)

// LocalState is the inspected local state of a dots entry's target path.
type LocalState struct {
	Kind LocalKind
}

// ClassifyEntry derives the entry-level State and available Actions for a
// resolved dots entry by inspecting its repo source and local target.
func ClassifyEntry(e ResolvedEntry) (State, []Action) {
	if e.Ignored {
		return StateIgnored, []Action{ActionUnignore, ActionRemove}
	}

	sourceExists := PathExists(e.SourcePath)
	local := InspectDotLocal(e)

	if sourceExists {
		switch local.Kind {
		case LocalMissing:
			return StateMissing, syncableDotActions()
		case LocalExpectedLink:
			return StateSynced, trackedHealthyDotActions()
		case LocalBrokenLink:
			return StateBroken, syncableDotActions()
		case LocalModified:
			return StateModified, syncableDotActions()
		case LocalAllIgnored:
			return StateIgnored, []Action{ActionUnignore, ActionRemove}
		default:
			return StateConflict, conflictDotActions()
		}
	}

	switch local.Kind {
	case LocalContent, LocalWrongLink:
		return StateLocalOnly, syncableDotActions()
	default:
		return StateNoSource, noSourceDotActions()
	}
}

// InspectDotLocal reports the on-disk shape of a resolved entry's target path.
func InspectDotLocal(e ResolvedEntry) LocalState {
	info, err := os.Lstat(e.TargetPath)
	if os.IsNotExist(err) {
		return LocalState{Kind: LocalMissing}
	}
	if err != nil {
		return LocalState{Kind: LocalContent}
	}
	if info.Mode()&os.ModeSymlink == 0 {
		sourceInfo, sourceErr := os.Lstat(e.SourcePath)
		if info.IsDir() {
			if sourceErr == nil && sourceInfo.IsDir() && sourceInfo.Mode()&os.ModeSymlink == 0 {
				return LocalState{Kind: InspectManagedDotDirectory(e)}
			}
		}
		if sourceErr == nil && LocalFileIsNewer(sourceInfo, info) {
			return LocalState{Kind: LocalModified}
		}
		return LocalState{Kind: LocalContent}
	}

	target, err := os.Readlink(e.TargetPath)
	if err != nil {
		return LocalState{Kind: LocalBrokenLink}
	}
	absTarget := target
	if !filepath.IsAbs(absTarget) {
		absTarget = filepath.Clean(filepath.Join(filepath.Dir(e.TargetPath), absTarget))
	}
	if sameCleanPath(absTarget, e.SourcePath) {
		return LocalState{Kind: LocalExpectedLink}
	}
	if PathExists(absTarget) {
		return LocalState{Kind: LocalWrongLink}
	}
	return LocalState{Kind: LocalBrokenLink}
}

// InspectManagedDotDirectory classifies a directory-shaped entry whose repo
// source and local target are both real directories, by walking the source
// tree and comparing each managed path against the local target.
func InspectManagedDotDirectory(e ResolvedEntry) LocalKind {
	kind := LocalExpectedLink
	rootMatches := SameResolvedPath(e.TargetPath, e.SourcePath)
	sawNonIgnoredPath := false
	sawIgnoredPath := false
	sawLinkedManagedPath := false
	ignores := CombinedIgnores(e.Ignore)
	walkErr := filepath.WalkDir(e.SourcePath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			kind = LocalContent
			return err
		}
		rel, relErr := filepath.Rel(e.SourcePath, path)
		if relErr != nil {
			kind = LocalContent
			return relErr
		}
		if rel == "." {
			return nil
		}
		if ShouldIgnoreDotPath(e.SourcePath, rel, d.Name(), ignores) {
			sawIgnoredPath = true
			if d.IsDir() {
				if IgnoredDotDirHasIncludedDescendant(e.SourcePath, rel, ignores) {
					return nil
				}
				return filepath.SkipDir
			}
			return nil
		}
		sawNonIgnoredPath = true

		srcInfo, infoErr := os.Lstat(path)
		if infoErr != nil {
			kind = LocalContent
			return infoErr
		}
		targetPath := filepath.Join(e.TargetPath, rel)
		targetInfo, targetErr := os.Lstat(targetPath)
		if os.IsNotExist(targetErr) {
			if kind == LocalExpectedLink {
				kind = LocalMissing
			}
			if srcInfo.IsDir() && srcInfo.Mode()&os.ModeSymlink == 0 {
				return filepath.SkipDir
			}
			return nil
		}
		if targetErr != nil {
			kind = LocalContent
			return targetErr
		}
		if SameResolvedPath(targetPath, path) {
			sawLinkedManagedPath = true
			return nil
		}
		if srcInfo.IsDir() && srcInfo.Mode()&os.ModeSymlink == 0 {
			if targetInfo.IsDir() && targetInfo.Mode()&os.ModeSymlink == 0 {
				return nil
			}
		}
		if targetInfo.Mode()&os.ModeSymlink == 0 {
			if LocalFileIsNewer(srcInfo, targetInfo) {
				if kind == LocalExpectedLink || kind == LocalMissing || kind == LocalModified {
					kind = LocalModified
				}
				return nil
			}
			kind = LocalContent
			if srcInfo.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target, readErr := os.Readlink(targetPath)
		if readErr != nil {
			if kind != LocalContent {
				kind = LocalBrokenLink
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
		if PathExists(absTarget) {
			kind = LocalWrongLink
			return filepath.SkipAll
		}
		if kind != LocalContent && kind != LocalWrongLink {
			kind = LocalBrokenLink
		}
		return nil
	})
	if walkErr != nil && kind == LocalExpectedLink {
		return LocalContent
	}
	if kind == LocalExpectedLink || kind == LocalMissing || kind == LocalModified {
		hasLocalAdditions, additionErr := WalkLocalOnlyDotFiles(e, nil)
		if additionErr != nil {
			return LocalContent
		}
		if hasLocalAdditions {
			kind = LocalModified
		}
	}
	if !rootMatches && kind != LocalModified && (!sawNonIgnoredPath || !sawLinkedManagedPath) {
		if !sawNonIgnoredPath && sawIgnoredPath {
			return LocalAllIgnored
		}
		return LocalContent
	}
	return kind
}

// LocalFileIsNewer reports whether the local target regular file has a newer
// modification time than the repo source regular file.
func LocalFileIsNewer(sourceInfo, targetInfo os.FileInfo) bool {
	return sourceInfo.Mode().IsRegular() &&
		targetInfo.Mode().IsRegular() &&
		targetInfo.ModTime().After(sourceInfo.ModTime())
}

// PathExists reports whether a path exists (following no symlinks).
func PathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func sameCleanPath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

// SameResolvedPath reports whether two paths refer to the same file, comparing
// cleaned paths first and falling back to fully resolved symlink targets.
func SameResolvedPath(a, b string) bool {
	if sameCleanPath(a, b) {
		return true
	}
	resolvedA, errA := filepath.EvalSymlinks(a)
	resolvedB, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && sameCleanPath(resolvedA, resolvedB)
}

func syncableDotActions() []Action {
	return []Action{ActionSync, ActionRemove, ActionIgnore}
}

func trackedHealthyDotActions() []Action {
	return []Action{ActionRemove, ActionIgnore}
}

func conflictDotActions() []Action {
	return []Action{ActionUseRepo, ActionUseLocal, ActionRemove, ActionIgnore}
}

func noSourceDotActions() []Action {
	return []Action{ActionRemove, ActionIgnore}
}

// IsFoldedDotDirectory reports whether a directory entry's target is itself a
// symlink that resolves to the repo source directory (a stow-folded directory).
func IsFoldedDotDirectory(entry ResolvedEntry) bool {
	sourceInfo, err := os.Lstat(entry.SourcePath)
	if err != nil || !sourceInfo.IsDir() || sourceInfo.Mode()&os.ModeSymlink != 0 {
		return false
	}
	targetInfo, err := os.Lstat(entry.TargetPath)
	if err != nil || targetInfo.Mode()&os.ModeSymlink == 0 {
		return false
	}
	return SameResolvedPath(entry.TargetPath, entry.SourcePath)
}

// SelfHealDotEntryLinkShape rewrites existing target symlinks that already
// resolve to the correct repo file but aren't shaped the way GNU Stow itself
// would write them (e.g. absolute instead of relative, or a differently
// computed relative path). Stow refuses to touch such links ("not owned by
// stow"), aborting the whole package's restow even though the link's target
// is already correct. Only links resolving to the entry's own source are
// touched; anything else is left for the normal conflict/broken-link flow.
func SelfHealDotEntryLinkShape(entry ResolvedEntry) error {
	srcInfo, err := os.Lstat(entry.SourcePath)
	if err != nil {
		return nil
	}
	if srcInfo.IsDir() && srcInfo.Mode()&os.ModeSymlink == 0 {
		return filepath.WalkDir(entry.SourcePath, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, relErr := filepath.Rel(entry.SourcePath, path)
			if relErr != nil || rel == "." {
				return relErr
			}
			if ShouldIgnoreDotPath(entry.SourcePath, rel, d.Name(), CombinedIgnores(entry.Ignore)) {
				if d.IsDir() {
					if IgnoredDotDirHasIncludedDescendant(entry.SourcePath, rel, CombinedIgnores(entry.Ignore)) {
						return nil
					}
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			return healDotLinkShapeAt(filepath.Join(entry.TargetPath, rel), path)
		})
	}
	return healDotLinkShapeAt(entry.TargetPath, entry.SourcePath)
}

// healDotLinkShapeAt rewrites the symlink at linkPath to stow-relative shape
// when it already resolves to sourcePath. Non-symlinks and links resolving
// to a different file are left untouched.
func healDotLinkShapeAt(linkPath, sourcePath string) error {
	info, err := os.Lstat(linkPath)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return nil
	}
	target, err := os.Readlink(linkPath)
	if err != nil {
		return nil
	}
	absTarget := target
	if !filepath.IsAbs(absTarget) {
		absTarget = filepath.Clean(filepath.Join(filepath.Dir(linkPath), absTarget))
	}
	if !sameCleanPath(absTarget, sourcePath) {
		return nil
	}
	wantRel, err := StowRelativeSymlinkTarget(linkPath, sourcePath)
	if err != nil {
		return fmt.Errorf("compute stow-relative target for %q: %w", linkPath, err)
	}
	if target == wantRel {
		return nil
	}
	return WriteStowShapedSymlink(linkPath, sourcePath)
}

// ConflictIsManagedStowLink reports whether an entry currently in a conflict
// state is actually a stow-managed link (or tree of links) pointing within the
// stow root, meaning it can be safely repaired by a restow.
func ConflictIsManagedStowLink(entry ResolvedEntry, stowPath string) bool {
	local := InspectDotLocal(entry).Kind
	// Accept LocalContent for directories: InspectManagedDotDirectory may
	// classify a fully wrong-linked directory as content when no paths point
	// to the current source. The walk below still correctly identifies stow
	// links, so we let it make the final determination.
	if local != LocalWrongLink && local != LocalContent {
		return false
	}
	targetInfo, err := os.Lstat(entry.TargetPath)
	if err != nil {
		return false
	}
	if targetInfo.Mode()&os.ModeSymlink != 0 {
		return symlinkTargetWithinStowRoot(entry.TargetPath, stowPath)
	}
	sourceInfo, err := os.Lstat(entry.SourcePath)
	if err != nil || !sourceInfo.IsDir() || sourceInfo.Mode()&os.ModeSymlink != 0 || !targetInfo.IsDir() {
		return false
	}
	managedWrongLink := false
	walkErr := filepath.WalkDir(entry.SourcePath, func(sourcePath string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(entry.SourcePath, sourcePath)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		if ShouldIgnoreDotPath(entry.SourcePath, rel, d.Name(), CombinedIgnores(entry.Ignore)) {
			if d.IsDir() {
				if IgnoredDotDirHasIncludedDescendant(entry.SourcePath, rel, CombinedIgnores(entry.Ignore)) {
					return nil
				}
				return filepath.SkipDir
			}
			return nil
		}
		sourceInfo, infoErr := os.Lstat(sourcePath)
		if infoErr != nil {
			return infoErr
		}
		targetPath := filepath.Join(entry.TargetPath, rel)
		targetInfo, targetErr := os.Lstat(targetPath)
		if os.IsNotExist(targetErr) {
			if sourceInfo.IsDir() && sourceInfo.Mode()&os.ModeSymlink == 0 {
				return filepath.SkipDir
			}
			return nil
		}
		if targetErr != nil {
			return targetErr
		}
		if SameResolvedPath(targetPath, sourcePath) {
			return nil
		}
		if sourceInfo.IsDir() && sourceInfo.Mode()&os.ModeSymlink == 0 && targetInfo.IsDir() && targetInfo.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		if targetInfo.Mode()&os.ModeSymlink != 0 && symlinkTargetWithinStowRoot(targetPath, stowPath) {
			managedWrongLink = true
			return nil
		}
		return fmt.Errorf("unmanaged conflict at %s", targetPath)
	})
	return walkErr == nil && managedWrongLink
}

func symlinkTargetWithinStowRoot(path, stowPath string) bool {
	target, err := os.Readlink(path)
	if err != nil {
		return false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Clean(filepath.Join(filepath.Dir(path), target))
	}
	return pathWithinDir(target, stowPath)
}

func pathWithinDir(path, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// LstatEntryOp inspects the local target after a stow operation and returns the
// Op describing the resulting link state (or a conflict when it is not linked).
func LstatEntryOp(entry ResolvedEntry, dryRun bool) Op {
	local := InspectDotLocal(entry)
	switch local.Kind {
	case LocalExpectedLink:
		if dryRun {
			return Op{Kind: OpSkip, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}
		}
		return Op{Kind: OpLink, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}
	case LocalMissing:
		if dryRun {
			return Op{Kind: OpDryLink, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}
		}
		return Op{Kind: OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath,
			Err: fmt.Errorf("path not linked after stow")}
	case LocalBrokenLink:
		return Op{Kind: OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath,
			Err: fmt.Errorf("managed link is broken")}
	case LocalAllIgnored:
		return Op{Kind: OpSkip, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}
	default:
		return Op{Kind: OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath,
			Err: fmt.Errorf("real file at %q; use omni dots add --adopt to migrate", entry.TargetPath)}
	}
}

// WalkLocalOnlyDotFiles walks the local target tree looking for managed files
// that exist locally but not in the repo source. When addOne is non-nil it is
// invoked for each such file; it returns whether any were found.
func WalkLocalOnlyDotFiles(entry ResolvedEntry, addOne func(sourcePath, targetPath string) error) (bool, error) {
	sourceInfo, sourceErr := os.Lstat(entry.SourcePath)
	if sourceErr != nil {
		return false, sourceErr
	}
	targetInfo, targetErr := os.Lstat(entry.TargetPath)
	if targetErr != nil {
		return false, targetErr
	}
	if !sourceInfo.IsDir() || sourceInfo.Mode()&os.ModeSymlink != 0 || !targetInfo.IsDir() || targetInfo.Mode()&os.ModeSymlink != 0 {
		return false, nil
	}
	ignores := CombinedIgnores(entry.Ignore)
	found := false
	err := filepath.WalkDir(entry.TargetPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(entry.TargetPath, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		if ShouldIgnoreDotPath(entry.SourcePath, rel, d.Name(), ignores) {
			if d.IsDir() {
				if IgnoredDotDirHasIncludedDescendant(entry.SourcePath, rel, ignores) {
					return nil
				}
				return filepath.SkipDir
			}
			return nil
		}
		targetInfo, infoErr := os.Lstat(path)
		if infoErr != nil {
			return infoErr
		}
		sourcePath := filepath.Join(entry.SourcePath, rel)
		sourceInfo, sourceErr := os.Lstat(sourcePath)
		if sourceErr == nil {
			if SameResolvedPath(path, sourcePath) {
				if targetInfo.IsDir() && targetInfo.Mode()&os.ModeSymlink == 0 {
					return filepath.SkipDir
				}
				return nil
			}
			if sourceInfo.IsDir() && sourceInfo.Mode()&os.ModeSymlink == 0 && targetInfo.IsDir() && targetInfo.Mode()&os.ModeSymlink == 0 {
				return nil
			}
			return nil
		}
		if !os.IsNotExist(sourceErr) {
			return sourceErr
		}
		if targetInfo.IsDir() && targetInfo.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		if !IsManagedDotFile(targetInfo.Mode()) {
			return nil
		}
		found = true
		if addOne == nil {
			return nil
		}
		return addOne(sourcePath, path)
	})
	if err != nil {
		return false, err
	}
	return found, nil
}

// CombinedIgnores returns the default ignore patterns combined with the given
// per-entry ignore patterns.
func CombinedIgnores(ignores []string) []string {
	return append(DefaultIgnores(), ignores...)
}

// ShouldIgnoreDotPath reports whether a path (relative to root) should be
// ignored, matching both the raw relative path and a root-prefixed variant.
func ShouldIgnoreDotPath(root, relPath, basename string, ignores []string) bool {
	rooted := filepath.ToSlash(filepath.Join(filepath.Base(root), relPath))
	return ShouldIgnoreAnyPath([]string{relPath, rooted}, basename, ignores)
}

// IgnoredDotDirHasIncludedDescendant reports whether an ignored directory
// contains a descendant re-included by a negated ignore pattern.
func IgnoredDotDirHasIncludedDescendant(root, relPath string, ignores []string) bool {
	if HasIncludedDescendant(relPath, ignores) {
		return true
	}
	rooted := filepath.ToSlash(filepath.Join(filepath.Base(root), relPath))
	return HasIncludedDescendant(rooted, ignores)
}

// IsManagedDotFile reports whether a file mode is one dots manages (a regular
// file or a symlink).
func IsManagedDotFile(mode os.FileMode) bool {
	return mode.IsRegular() || mode&os.ModeSymlink != 0
}
