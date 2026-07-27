package dots

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// State — Source of truth for CLI/TUI actions; DotHealth is only derived for current renderers.
type State string

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

type Action string

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

type LocalKind int

const (
	LocalMissing LocalKind = iota
	LocalExpectedLink
	LocalWrongLink
	LocalBrokenLink
	LocalContent
	LocalModified
	LocalAllIgnored
)

type LocalState struct {
	Kind LocalKind
}

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

// InspectManagedDotDirectory — Both repo source and local target must already be real directories.
func InspectManagedDotDirectory(e ResolvedEntry) LocalKind {
	kind := LocalExpectedLink
	rootMatches := SameResolvedPath(e.TargetPath, e.SourcePath)
	sawNonIgnoredPath := false
	sawIgnoredPath := false
	sawLinkedManagedPath := false
	im := CompileIgnoresLenient(CombinedIgnores(e.Ignore))
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
		if im.IgnoredDotPath(e.SourcePath, rel, d.Name()) {
			sawIgnoredPath = true
			if d.IsDir() {
				if im.DotDirHasIncludedDescendant(e.SourcePath, rel) {
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

// SameResolvedPath — Compares cleaned paths first, then falls back to fully resolved symlink targets.
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

// IsFoldedDotDirectory — A stow-folded directory: the target is itself a symlink to the repo source directory.
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

// SelfHealDotEntryLinkShape — Stow aborts a whole package on links it did not shape, so reshape correct-but-foreign links to its own form.
func SelfHealDotEntryLinkShape(entry ResolvedEntry) error {
	srcInfo, err := os.Lstat(entry.SourcePath)
	if err != nil {
		return nil
	}
	if srcInfo.IsDir() && srcInfo.Mode()&os.ModeSymlink == 0 {
		im := CompileIgnoresLenient(CombinedIgnores(entry.Ignore))
		return filepath.WalkDir(entry.SourcePath, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, relErr := filepath.Rel(entry.SourcePath, path)
			if relErr != nil || rel == "." {
				return relErr
			}
			if im.IgnoredDotPath(entry.SourcePath, rel, d.Name()) {
				if d.IsDir() {
					if im.DotDirHasIncludedDescendant(entry.SourcePath, rel) {
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

// Non-symlinks and links resolving elsewhere are left untouched.
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

// ConflictIsManagedStowLink — A conflict that is really a stow link into the stow root can be repaired by a restow.
func ConflictIsManagedStowLink(entry ResolvedEntry, stowPath string) bool {
	local := InspectDotLocal(entry).Kind
	// LocalContent is accepted for directories because the walk below makes the final call.
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
	im := CompileIgnoresLenient(CombinedIgnores(entry.Ignore))
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
		if im.IgnoredDotPath(entry.SourcePath, rel, d.Name()) {
			if d.IsDir() {
				if im.DotDirHasIncludedDescendant(entry.SourcePath, rel) {
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
	im := CompileIgnoresLenient(CombinedIgnores(entry.Ignore))
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
		if im.IgnoredDotPath(entry.SourcePath, rel, d.Name()) {
			if d.IsDir() {
				if im.DotDirHasIncludedDescendant(entry.SourcePath, rel) {
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

func CombinedIgnores(ignores []string) []string {
	return append(DefaultIgnores(), ignores...)
}

// ShouldIgnoreDotPath — Matches both the raw relative path and a root-prefixed variant.
func ShouldIgnoreDotPath(root, relPath, basename string, ignores []string) bool {
	matcher, err := CompileIgnores(ignores)
	if err != nil {
		return false
	}
	return matcher.IgnoredDotPath(root, relPath, basename)
}

// IgnoredDotPath — Matches both the raw relative path and a root-prefixed variant.
func (m *IgnoreMatcher) IgnoredDotPath(root, relPath, basename string) bool {
	// Plain concat, not filepath.Join: the matcher cleans candidates and Join's extra Clean shows up on large walks.
	rooted := filepath.Base(root) + "/" + relPath
	matched, _ := m.MatchAnyPath([]string{relPath, rooted}, basename)
	return matched
}

func (m *IgnoreMatcher) DotDirHasIncludedDescendant(root, relPath string) bool {
	if !m.hasIncluded {
		return false
	}
	if m.HasIncludedDescendant(relPath) {
		return true
	}
	rooted := filepath.ToSlash(filepath.Join(filepath.Base(root), relPath))
	return m.HasIncludedDescendant(rooted)
}

func IgnoredDotDirHasIncludedDescendant(root, relPath string, ignores []string) bool {
	matcher, err := CompileIgnores(ignores)
	if err != nil {
		return false
	}
	return matcher.DotDirHasIncludedDescendant(root, relPath)
}

// IsManagedDotFile — dots manages regular files and symlinks only.
func IsManagedDotFile(mode os.FileMode) bool {
	return mode.IsRegular() || mode&os.ModeSymlink != 0
}
