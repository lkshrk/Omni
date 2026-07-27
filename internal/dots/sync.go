package dots

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/lkshrk/omni/internal/executor"
)

// Sync — Mutates the filesystem, so the Engine must have been built WithExecutor.
func (e *Engine) Sync(ctx context.Context, opts SyncOptions) (ops []Op, err error) {
	if e.exec == nil {
		return nil, fmt.Errorf("dots sync: executor is required")
	}
	stowPath := e.RepoPath
	repoPath := filepath.Dir(stowPath)
	orderedEntries := orderResolvedDotEntries(e.Entries, opts.EntryOrder)
	var failures []dotSyncFailure
	total := len(orderedEntries)
	for i, entry := range orderedEntries {
		if opts.Progress != nil {
			opts.Progress(SyncProgressEvent{Entry: entry.Name, Index: i + 1, Total: total})
		}
		entryOps, syncErr := syncResolvedDotEntry(ctx, e.exec, repoPath, stowPath, entry, opts, false)
		if opts.Progress != nil {
			opts.Progress(SyncProgressEvent{Entry: entry.Name, Index: i + 1, Total: total, Done: true, Err: syncErr, Ops: entryOps})
		}
		ops = append(ops, entryOps...)
		if syncErr != nil {
			failures = append(failures, dotSyncFailure{entry: entry.Name, err: syncErr})
		}
	}
	if len(failures) > 0 {
		return ops, dotSyncFailures(failures)
	}
	return ops, nil
}

// SyncEntry — Acts only on a single safe classifier action; choice-based conflicts are reported, not resolved.
func (e *Engine) SyncEntry(ctx context.Context, name string, opts SyncOptions) ([]Op, error) {
	if e.exec == nil {
		return nil, fmt.Errorf("dots sync: executor is required")
	}
	stowPath := e.RepoPath
	repoPath := filepath.Dir(stowPath)
	for _, entry := range e.Entries {
		if entry.Name != name {
			continue
		}
		if opts.Progress != nil {
			opts.Progress(SyncProgressEvent{Entry: entry.Name, Index: 1, Total: 1})
		}
		ops, syncErr := syncResolvedDotEntry(ctx, e.exec, repoPath, stowPath, entry, opts, true)
		if opts.Progress != nil {
			opts.Progress(SyncProgressEvent{Entry: entry.Name, Index: 1, Total: 1, Done: true, Err: syncErr, Ops: ops})
		}
		if syncErr != nil {
			return ops, fmt.Errorf("dots sync %q: %w", name, syncErr)
		}
		return ops, nil
	}
	return nil, fmt.Errorf("dots entry %q not found", name)
}

func orderResolvedDotEntries(entries []ResolvedEntry, order []string) []ResolvedEntry {
	if len(entries) == 0 || len(order) == 0 {
		return entries
	}
	rank := make(map[string]int, len(order))
	for i, name := range order {
		if name == "" {
			continue
		}
		if _, exists := rank[name]; !exists {
			rank[name] = i
		}
	}
	if len(rank) == 0 {
		return entries
	}
	ordered := append([]ResolvedEntry(nil), entries...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left, leftOK := rank[ordered[i].Name]
		right, rightOK := rank[ordered[j].Name]
		switch {
		case leftOK && rightOK:
			return left < right
		case leftOK:
			return true
		case rightOK:
			return false
		default:
			return false
		}
	})
	return ordered
}

type dotSyncFailure struct {
	entry string
	err   error
}

type dotSyncFailures []dotSyncFailure

func (f dotSyncFailures) Error() string {
	parts := make([]string, 0, len(f))
	for _, failure := range f {
		parts = append(parts, fmt.Sprintf("%s: %v", failure.entry, failure.err))
	}
	return "dots sync: " + strings.Join(parts, "; ")
}

func syncResolvedDotEntry(ctx context.Context, exec executor.Executor, repoPath, stowPath string, entry ResolvedEntry, opts SyncOptions, failUnsyncable bool) ([]Op, error) {
	// Drop ignored repo sources first so stow never links legacy files committed before their pattern existed.
	if !opts.DryRun {
		purgeIgnoredRepoSources(ctx, exec, repoPath, entry)
		if err := SelfHealDotEntryLinkShape(entry); err != nil {
			fmt.Fprintf(os.Stderr, "warning: omni: self-heal symlink shape for %s: %v\n", entry.Name, err)
		}
	}
	state, _ := ClassifyEntry(entry)
	switch state {
	case StateSynced:
		if IsFoldedDotDirectory(entry) {
			if opts.DryRun {
				return []Op{{Kind: OpDryRepair, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
			}
			if err := Restow(ctx, exec, stowPath, []string{entry.Package}, false); err != nil {
				return []Op{{Kind: OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}}, err
			}
			return []Op{LstatEntryOp(entry, false)}, nil
		}
		return []Op{{Kind: OpSkip, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
	case StateMissing:
		if opts.DryRun {
			return []Op{{Kind: OpDryLink, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
		}
		if err := Restow(ctx, exec, stowPath, []string{entry.Package}, false); err != nil {
			return []Op{{Kind: OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}}, err
		}
		return []Op{LstatEntryOp(entry, false)}, nil
	case StateBroken:
		if opts.DryRun {
			return []Op{{Kind: OpDryRepair, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
		}
		prep, err := PrepareDotTargetForRestow(ctx, exec, entry)
		if err != nil {
			return nil, err
		}
		if err := Restow(ctx, exec, stowPath, []string{entry.Package}, false); err != nil {
			if prep.backupPath != "" {
				if restoreErr := RestoreDotTargetAfterFailedRestow(ctx, exec, entry, prep); restoreErr != nil {
					return []Op{{Kind: OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}},
						fmt.Errorf("%w (restore failed: %v)", err, restoreErr)
				}
			}
			return []Op{{Kind: OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}}, err
		}
		return []Op{{Kind: OpRepair, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
	case StateLocalOnly:
		if opts.DryRun {
			return []Op{{Kind: OpDryAdopt, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
		}
		op, err := syncLocalOnlyDotEntry(ctx, exec, stowPath, entry)
		if err != nil {
			return []Op{{Kind: OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}}, err
		}
		return []Op{op}, nil
	case StateModified:
		if opts.DryRun {
			return []Op{{Kind: OpDryAdopt, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
		}
		ops, err := syncModifiedDotEntry(ctx, exec, repoPath, stowPath, entry)
		if err != nil {
			return []Op{{Kind: OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}}, err
		}
		return ops, nil
	case StateConflict, StateUntrackedConflict, StateAmbiguous:
		if state == StateConflict && ConflictIsManagedStowLink(entry, stowPath) {
			if opts.DryRun {
				return []Op{{Kind: OpDryRepair, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
			}
			prep, err := PrepareDotTargetForRestow(ctx, exec, entry)
			if err != nil {
				return []Op{{Kind: OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}}, err
			}
			if err := Restow(ctx, exec, stowPath, []string{entry.Package}, false); err != nil {
				if prep.backupPath != "" {
					if restoreErr := RestoreDotTargetAfterFailedRestow(ctx, exec, entry, prep); restoreErr != nil {
						return []Op{{Kind: OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}},
							fmt.Errorf("%w (restore failed: %v)", err, restoreErr)
					}
				}
				return []Op{{Kind: OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}}, err
			}
			return []Op{{Kind: OpRepair, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
		}
		if state == StateConflict {
			// Per-entry on_conflict wins over the sync-wide ConflictStrategy.
			strategy := entry.OnConflict
			if strategy == "" {
				strategy = opts.ConflictStrategy
			}
			switch strategy {
			case "use_repo":
				if opts.DryRun {
					return []Op{{Kind: OpDryRepair, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
				}
				return ResolveUseRepo(ctx, exec, stowPath, entry)
			case "use_local":
				if opts.DryRun {
					return []Op{{Kind: OpDryAdopt, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
				}
				return ResolveUseLocal(ctx, exec, repoPath, stowPath, entry)
			}
		}
		err := fmt.Errorf("requires choosing use repo version or use local version")
		return []Op{{Kind: OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}}, err
	default:
		err := fmt.Errorf("state %q is not syncable; remove or ignore this entry", state)
		if failUnsyncable {
			return []Op{{Kind: OpSkip, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}}, err
		}
		return []Op{{Kind: OpSkip, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}}, nil
	}
}

func syncLocalOnlyDotEntry(ctx context.Context, exec executor.Executor, stowPath string, entry ResolvedEntry) (Op, error) {
	copySource, err := LocalDotCopySource(entry.TargetPath)
	if err != nil {
		return Op{}, err
	}
	if err := CopyDotPath(copySource, entry.SourcePath, CombinedIgnores(entry.Ignore)); err != nil {
		if removeErr := os.RemoveAll(entry.SourcePath); removeErr != nil {
			return Op{}, fmt.Errorf("copy local into repo: %w (remove created source failed: %v)", err, removeErr)
		}
		return Op{}, fmt.Errorf("copy local into repo: %w", err)
	}
	prep, err := PrepareDotTargetForRestow(ctx, exec, entry)
	if err != nil {
		if cleanupErr := os.RemoveAll(entry.SourcePath); cleanupErr != nil {
			return Op{}, fmt.Errorf("prepare local target: %w (remove created source failed: %v)", err, cleanupErr)
		}
		return Op{}, fmt.Errorf("prepare local target: %w", err)
	}
	if err := Restow(ctx, exec, stowPath, []string{entry.Package}, false); err != nil {
		if removeErr := os.RemoveAll(entry.SourcePath); removeErr != nil {
			return Op{}, fmt.Errorf("%w (remove created source failed: %v)", err, removeErr)
		}
		if prep.backupPath != "" {
			if restoreErr := RestoreDotTargetAfterFailedRestow(ctx, exec, entry, prep); restoreErr != nil {
				return Op{}, fmt.Errorf("%w (restore failed: %v)", err, restoreErr)
			}
		}
		return Op{}, err
	}
	return Op{Kind: OpAdopt, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}, nil
}

func syncModifiedDotEntry(ctx context.Context, exec executor.Executor, repoPath, stowPath string, entry ResolvedEntry) (ops []Op, retErr error) {
	if PathExists(entry.SourcePath) {
		gt := NewGit(repoPath, exec)
		if gt.IsRepo() {
			if err := gt.SnapshotAll(ctx, "dots: pre-sync "+entry.Name); err != nil {
				return nil, fmt.Errorf("pre-commit repo state: %w", err)
			}
		}
	}
	replacement, err := replaceModifiedDotSourceFilesFromLocal(entry, filepath.Join(stowPath, entry.Package))
	if err != nil {
		return nil, err
	}
	committedSource := false
	defer func() {
		if committedSource || retErr == nil {
			return
		}
		if rollbackErr := replacement.rollback(); rollbackErr != nil {
			retErr = fmt.Errorf("%w (rollback failed: %v)", retErr, rollbackErr)
		}
	}()
	prep, err := PrepareDotTargetForRestow(ctx, exec, entry)
	if err != nil {
		return nil, fmt.Errorf("prepare local target: %w", err)
	}
	if err := Restow(ctx, exec, stowPath, []string{entry.Package}, false); err != nil {
		if prep.backupPath != "" {
			if restoreErr := RestoreDotTargetAfterFailedRestow(ctx, exec, entry, prep); restoreErr != nil {
				return nil, fmt.Errorf("%w (restore failed: %v)", err, restoreErr)
			}
		}
		return nil, err
	}
	committedSource = true
	if err := replacement.commit(); err != nil {
		return nil, fmt.Errorf("cleanup source backup: %w", err)
	}
	return []Op{{Kind: OpAdopt, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
}

func LocalDotCopySource(targetPath string) (string, error) {
	if _, err := os.Lstat(targetPath); err != nil {
		return "", err
	}
	return targetPath, nil
}

func backupAndRemoveLocalTarget(ctx context.Context, exec executor.Executor, targetPath string) (string, error) {
	return BackupAndRemoveLocalPathWithExecutor(ctx, exec, targetPath)
}

func backupLocalTarget(ctx context.Context, exec executor.Executor, targetPath string, ignores []string) (string, error) {
	backupPath, backupErr := BackupLocalPathFilteredWithExecutor(ctx, exec, targetPath, ignores)
	if backupErr != nil && !os.IsNotExist(backupErr) {
		return "", fmt.Errorf("backup %q: %w", targetPath, backupErr)
	}
	return backupPath, nil
}

type PreparedDotTarget struct {
	backupPath          string
	preservedDirectory  bool
	removedManagedPaths bool
}

// PrepareDotTargetForAdoption preserves a top-level symlink backup before using the shared restow transaction.
func PrepareDotTargetForAdoption(ctx context.Context, exec executor.Executor, entry ResolvedEntry) (PreparedDotTarget, error) {
	info, err := os.Lstat(entry.TargetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return PrepareDotTargetForRestow(ctx, exec, entry)
		}
		return PreparedDotTarget{}, err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return PrepareDotTargetForRestow(ctx, exec, entry)
	}
	backupPath, err := BackupLocalPathWithExecutor(ctx, exec, entry.TargetPath)
	if err != nil {
		return PreparedDotTarget{}, err
	}
	if err := RemoveLocalPathAfterBackupWithExecutor(ctx, exec, entry.TargetPath, backupPath); err != nil {
		return PreparedDotTarget{backupPath: backupPath}, err
	}
	return PreparedDotTarget{backupPath: backupPath}, nil
}

func PrepareDotTargetForRestow(ctx context.Context, exec executor.Executor, entry ResolvedEntry) (PreparedDotTarget, error) {
	if shouldPreserveDirectoryDotTarget(entry) {
		prep := PreparedDotTarget{preservedDirectory: true}
		backupPath, err := backupLocalTarget(ctx, exec, entry.TargetPath, CombinedIgnores(entry.Ignore))
		if err != nil {
			return prep, err
		}
		prep.backupPath = backupPath
		prep.removedManagedPaths = true
		if err := removeManagedDotTargetPaths(ctx, exec, entry, backupPath); err != nil {
			if backupPath != "" {
				if restoreErr := restorePreparedDirectoryTargetAfterFailedRestow(ctx, exec, entry, prep); restoreErr != nil {
					return prep, fmt.Errorf("%w (restore failed: %v)", err, restoreErr)
				}
			}
			return prep, err
		}
		return prep, nil
	}
	backupPath, err := backupAndRemoveLocalTarget(ctx, exec, entry.TargetPath)
	if err != nil {
		return PreparedDotTarget{}, err
	}
	return PreparedDotTarget{backupPath: backupPath}, nil
}

func shouldPreserveDirectoryDotTarget(entry ResolvedEntry) bool {
	sourceInfo, err := os.Lstat(entry.SourcePath)
	if err != nil || !sourceInfo.IsDir() || sourceInfo.Mode()&os.ModeSymlink != 0 {
		return false
	}
	targetInfo, err := os.Lstat(entry.TargetPath)
	return err == nil && targetInfo.IsDir() && targetInfo.Mode()&os.ModeSymlink == 0
}

func removeManagedDotTargetPaths(ctx context.Context, exec executor.Executor, entry ResolvedEntry, backupPath string) error {
	ignores := CombinedIgnores(entry.Ignore)
	if err := removeManagedDotTargetFiles(ctx, exec, entry, ignores, backupPath); err != nil {
		return err
	}
	if err := removeDotTargetDirectoryConflicts(ctx, exec, entry, ignores, backupPath); err != nil {
		return err
	}
	return removeEmptyUnmanagedDotTargetDirs(entry, ignores)
}

func removeManagedDotTargetFiles(ctx context.Context, exec executor.Executor, entry ResolvedEntry, ignores []string, backupPath string) error {
	return filepath.WalkDir(entry.TargetPath, func(path string, d os.DirEntry, err error) error {
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
		if ShouldIgnoreDotPath(entry.TargetPath, rel, d.Name(), ignores) {
			if d.IsDir() {
				if IgnoredDotDirHasIncludedDescendant(entry.TargetPath, rel, ignores) {
					return nil
				}
				return filepath.SkipDir
			}
			return nil
		}
		info, infoErr := os.Lstat(path)
		if infoErr != nil {
			return infoErr
		}
		if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		sourcePath := filepath.Join(entry.SourcePath, rel)
		if SameResolvedPath(path, sourcePath) {
			return nil
		}
		return RemoveLocalPathAfterBackupWithExecutor(ctx, exec, path, backupPath)
	})
}

func removeDotTargetDirectoryConflicts(ctx context.Context, exec executor.Executor, entry ResolvedEntry, ignores []string, backupPath string) error {
	return filepath.WalkDir(entry.SourcePath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(entry.SourcePath, path)
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
		sourceInfo, infoErr := os.Lstat(path)
		if infoErr != nil {
			return infoErr
		}
		targetPath := filepath.Join(entry.TargetPath, rel)
		targetInfo, targetErr := os.Lstat(targetPath)
		if os.IsNotExist(targetErr) {
			return nil
		}
		if targetErr != nil {
			return targetErr
		}
		if sourceInfo.IsDir() && sourceInfo.Mode()&os.ModeSymlink == 0 {
			if targetInfo.IsDir() && targetInfo.Mode()&os.ModeSymlink == 0 {
				return nil
			}
			return RemoveLocalPathAfterBackupWithExecutor(ctx, exec, targetPath, backupPath)
		}
		if targetInfo.IsDir() && targetInfo.Mode()&os.ModeSymlink == 0 {
			if err := RemoveLocalPathAfterBackupWithExecutor(ctx, exec, targetPath, backupPath); err != nil {
				return fmt.Errorf("replace directory %q with managed file: %w", targetPath, err)
			}
		}
		return nil
	})
}

func removeEmptyUnmanagedDotTargetDirs(entry ResolvedEntry, ignores []string) error {
	var dirs []string
	if err := filepath.WalkDir(entry.TargetPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == entry.TargetPath || !d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(entry.TargetPath, path)
		if relErr != nil {
			return relErr
		}
		if ShouldIgnoreDotPath(entry.TargetPath, rel, d.Name(), ignores) {
			if IgnoredDotDirHasIncludedDescendant(entry.TargetPath, rel, ignores) {
				return nil
			}
			return filepath.SkipDir
		}
		if sourceInfo, sourceErr := os.Lstat(filepath.Join(entry.SourcePath, rel)); sourceErr == nil && sourceInfo.IsDir() && sourceInfo.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		dirs = append(dirs, path)
		return nil
	}); err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		err := os.Remove(dirs[i])
		if err == nil || os.IsNotExist(err) {
			continue
		}
		if errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST) {
			continue
		}
		return err
	}
	return nil
}

func RestoreDotTargetAfterFailedRestow(ctx context.Context, exec executor.Executor, entry ResolvedEntry, prep PreparedDotTarget) error {
	if prep.preservedDirectory {
		return restorePreparedDirectoryTargetAfterFailedRestow(ctx, exec, entry, prep)
	}
	return restoreDotBackupAfterFailedStow(ctx, exec, prep.backupPath, entry.TargetPath)
}

func restorePreparedDirectoryTargetAfterFailedRestow(ctx context.Context, exec executor.Executor, entry ResolvedEntry, prep PreparedDotTarget) error {
	if prep.backupPath == "" || !prep.removedManagedPaths {
		return nil
	}
	return filepath.WalkDir(prep.backupPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(prep.backupPath, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return os.MkdirAll(entry.TargetPath, 0o755)
		}
		targetItem := filepath.Join(entry.TargetPath, rel)
		info, infoErr := os.Lstat(path)
		if infoErr != nil {
			return infoErr
		}
		if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			return os.MkdirAll(targetItem, info.Mode().Perm())
		}
		if err := RemoveLocalPathAfterBackupWithExecutor(ctx, exec, targetItem, prep.backupPath); err != nil {
			return err
		}
		return restoreBackupFile(path, targetItem)
	})
}

func purgeIgnoredRepoSources(ctx context.Context, exec executor.Executor, repoPath string, entry ResolvedEntry) int {
	if len(entry.Ignore) == 0 {
		return 0
	}
	srcInfo, err := os.Lstat(entry.SourcePath)
	if err != nil || !srcInfo.IsDir() {
		return 0
	}
	var candidates []string
	if walkErr := filepath.WalkDir(entry.SourcePath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(entry.SourcePath, path)
		if relErr != nil || rel == "." {
			return relErr
		}
		if !ShouldIgnorePath(rel, d.Name(), entry.Ignore) {
			if d.IsDir() && !HasIncludedDescendant(rel, entry.Ignore) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() && HasIncludedDescendant(rel, entry.Ignore) {
			return nil
		}
		candidates = append(candidates, path)
		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	}); walkErr != nil {
		fmt.Fprintf(os.Stderr, "warning: omni: purging dot files in %s: %v\n", entry.SourcePath, walkErr)
	}
	if len(candidates) == 0 {
		return 0
	}
	// Snapshot first so purged files stay recoverable from git history; the trash move is the second layer.
	gt := NewGit(repoPath, exec)
	if gt.IsRepo() {
		if err := gt.SnapshotAll(ctx, "dots: pre-purge "+entry.Name); err != nil {
			fmt.Fprintf(os.Stderr, "warning: omni: pre-purge snapshot for %s: %v\n", entry.Name, err)
		}
	}
	var purged int
	for _, path := range candidates {
		if err := TrashLocalPath(ctx, exec, path); err != nil {
			fmt.Fprintf(os.Stderr, "warning: omni: purge ignored repo source %q: %v\n", path, err)
			continue
		}
		purged++
	}
	return purged
}

// ResolveUseRepo — Discards diverged local content and relinks to the repo version, restoring the prior target on failure.
func ResolveUseRepo(ctx context.Context, exec executor.Executor, stowPath string, entry ResolvedEntry) ([]Op, error) {
	return ResolveUseRepoWith(ctx, exec, entry, func() error {
		return Restow(ctx, exec, stowPath, []string{entry.Package}, false)
	})
}

// ResolveUseRepoWith — The caller-supplied relink step lets path-scoped resolution relink one symlink instead of the whole package.
func ResolveUseRepoWith(ctx context.Context, exec executor.Executor, entry ResolvedEntry, relink func() error) ([]Op, error) {
	prep, err := PrepareDotTargetForRestow(ctx, exec, entry)
	if err != nil {
		return nil, err
	}
	if err := relink(); err != nil {
		wrapped := fmt.Errorf("dots resolve %q: use repo version relink: %w", entry.Name, err)
		if prep.backupPath != "" {
			if restoreErr := RestoreDotTargetAfterFailedRestow(ctx, exec, entry, prep); restoreErr != nil {
				return []Op{{Kind: OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: wrapped}},
					fmt.Errorf("%w (restore failed: %v)", wrapped, restoreErr)
			}
		}
		return []Op{{Kind: OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: wrapped}}, wrapped
	}
	return []Op{{Kind: OpRepair, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
}

// ResolveUseLocal — Adopts diverged local content into the repo; on failure rolls the source back and restores the target.
func ResolveUseLocal(ctx context.Context, exec executor.Executor, repoPath, stowPath string, entry ResolvedEntry) (ops []Op, retErr error) {
	return ResolveUseLocalWith(ctx, exec, repoPath, filepath.Join(stowPath, entry.Package), entry, func() error {
		return Restow(ctx, exec, stowPath, []string{entry.Package}, false)
	})
}

// ResolveUseLocalWith — The caller-supplied relink step lets path-scoped resolution relink one symlink instead of the whole package.
func ResolveUseLocalWith(ctx context.Context, exec executor.Executor, repoPath, packageRoot string, entry ResolvedEntry, relink func() error) (ops []Op, retErr error) {
	copySource, err := LocalDotCopySource(entry.TargetPath)
	if err != nil {
		return nil, err
	}
	if PathExists(entry.SourcePath) {
		gt := NewGit(repoPath, exec)
		if gt.IsRepo() {
			if err := gt.SnapshotAll(ctx, "dots: pre-resolve "+entry.Name); err != nil {
				return nil, fmt.Errorf("dots resolve %q: pre-commit repo state: %w", entry.Name, err)
			}
		}
	}
	replacement, err := replaceDotSourceFromLocal(copySource, entry.SourcePath, packageRoot, entry.Ignore)
	if err != nil {
		return []Op{{Kind: OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}},
			fmt.Errorf("dots resolve %q: replace repo source: %w", entry.Name, err)
	}
	prep, err := PrepareDotTargetForRestow(ctx, exec, entry)
	if err != nil {
		if rollbackErr := replacement.rollback(); rollbackErr != nil {
			return []Op{{Kind: OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}},
				fmt.Errorf("dots resolve %q: prepare local target: %w (rollback failed: %v)", entry.Name, err, rollbackErr)
		}
		return []Op{{Kind: OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}},
			fmt.Errorf("dots resolve %q: prepare local target: %w", entry.Name, err)
	}
	committedSource := false
	defer func() {
		if committedSource || retErr == nil {
			return
		}
		if rollbackErr := replacement.rollback(); rollbackErr != nil {
			retErr = fmt.Errorf("%w (rollback failed: %v)", retErr, rollbackErr)
			if len(ops) > 0 && ops[0].Err != nil {
				ops[0].Err = retErr
			}
		}
	}()
	if err := relink(); err != nil {
		wrapped := fmt.Errorf("dots resolve %q: use local version relink after copying local content: %w", entry.Name, err)
		if prep.backupPath != "" {
			if restoreErr := RestoreDotTargetAfterFailedRestow(ctx, exec, entry, prep); restoreErr != nil {
				return []Op{{Kind: OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: wrapped}},
					fmt.Errorf("%w (restore failed: %v)", wrapped, restoreErr)
			}
		}
		return []Op{{Kind: OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: wrapped}}, wrapped
	}
	committedSource = true
	if err := replacement.commit(); err != nil {
		return []Op{{Kind: OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}},
			fmt.Errorf("dots resolve %q: cleanup old source: %w", entry.Name, err)
	}
	return []Op{{Kind: OpAdopt, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
}

type dotSourceReplacement struct {
	commit   func() error
	rollback func() error
}

type newerDotSourceFileReplacement struct {
	sourcePath string
	backupPath string
}

func replaceDotSourceFromLocal(copySource, sourcePath, packageRoot string, ignores []string) (*dotSourceReplacement, error) {
	parent := filepath.Dir(sourcePath)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, err
	}
	base := filepath.Base(sourcePath)
	tmp, err := os.CreateTemp(parent, "."+base+".tmp-*")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return nil, err
	}
	if err := os.Remove(tmpPath); err != nil {
		return nil, err
	}
	if err := CopyDotPath(copySource, tmpPath, CombinedIgnores(ignores)); err != nil {
		_ = os.RemoveAll(tmpPath)
		return nil, err
	}

	oldPath := ""
	oldStagingRoot := ""
	if _, err := os.Lstat(sourcePath); err == nil {
		oldParent := oldSourceStagingParent(sourcePath, packageRoot)
		oldStagingRoot, err = os.MkdirTemp(oldParent, ".omni-old-*")
		if err != nil {
			_ = os.RemoveAll(tmpPath)
			return nil, err
		}
		oldPath = filepath.Join(oldStagingRoot, base)
		if err := os.Rename(sourcePath, oldPath); err != nil {
			_ = os.RemoveAll(oldStagingRoot)
			_ = os.RemoveAll(tmpPath)
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		_ = os.RemoveAll(tmpPath)
		return nil, err
	}

	if err := os.Rename(tmpPath, sourcePath); err != nil {
		if oldPath != "" {
			if restoreErr := os.Rename(oldPath, sourcePath); restoreErr != nil {
				err = fmt.Errorf("%w (restore old source failed: %v)", err, restoreErr)
			} else {
				_ = os.RemoveAll(oldStagingRoot)
			}
		}
		_ = os.RemoveAll(tmpPath)
		return nil, err
	}

	return &dotSourceReplacement{
		commit: func() error {
			if oldPath == "" {
				return nil
			}
			return os.RemoveAll(oldStagingRoot)
		},
		rollback: func() error {
			var errs []string
			if err := os.RemoveAll(sourcePath); err != nil && !os.IsNotExist(err) {
				errs = append(errs, fmt.Sprintf("remove replacement: %v", err))
			}
			if oldPath != "" {
				if err := os.Rename(oldPath, sourcePath); err != nil {
					errs = append(errs, fmt.Sprintf("restore old source: %v", err))
				} else if err := os.RemoveAll(oldStagingRoot); err != nil && !os.IsNotExist(err) {
					errs = append(errs, fmt.Sprintf("remove old source staging: %v", err))
				}
			}
			if len(errs) > 0 {
				return fmt.Errorf("%s", strings.Join(errs, "; "))
			}
			return nil
		},
	}, nil
}

func replaceModifiedDotSourceFilesFromLocal(entry ResolvedEntry, packageRoot string) (*dotSourceReplacement, error) {
	stagingParent := oldSourceStagingParent(entry.SourcePath, packageRoot)
	backupRoot, err := os.MkdirTemp(stagingParent, ".omni-newer-*")
	if err != nil {
		return nil, err
	}
	replacements := make([]newerDotSourceFileReplacement, 0)
	var addedSourcePaths []string
	rollback := func() error {
		var errs []string
		for i := len(addedSourcePaths) - 1; i >= 0; i-- {
			if err := os.RemoveAll(addedSourcePaths[i]); err != nil && !os.IsNotExist(err) {
				errs = append(errs, fmt.Sprintf("remove added source %q: %v", addedSourcePaths[i], err))
			}
		}
		for i := len(replacements) - 1; i >= 0; i-- {
			replacement := replacements[i]
			if err := copyRegularDotFileReplace(replacement.backupPath, replacement.sourcePath); err != nil {
				errs = append(errs, fmt.Sprintf("restore %q: %v", replacement.sourcePath, err))
			}
		}
		if err := os.RemoveAll(backupRoot); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Sprintf("remove newer source staging: %v", err))
		}
		if len(errs) > 0 {
			return fmt.Errorf("%s", strings.Join(errs, "; "))
		}
		return nil
	}
	addOne := func(sourcePath, targetPath string) error {
		if err := CopyDotPath(targetPath, sourcePath, CombinedIgnores(entry.Ignore)); err != nil {
			return fmt.Errorf("copy local addition %q into repo source %q: %w", targetPath, sourcePath, err)
		}
		addedSourcePaths = append(addedSourcePaths, sourcePath)
		return nil
	}
	replaceOne := func(sourcePath, targetPath string) error {
		rel, relErr := filepath.Rel(entry.SourcePath, sourcePath)
		if relErr != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
			rel = filepath.Base(sourcePath)
		}
		backupPath := filepath.Join(backupRoot, rel)
		if err := copyRegularDotFileReplace(sourcePath, backupPath); err != nil {
			return fmt.Errorf("backup repo source %q: %w", sourcePath, err)
		}
		replacements = append(replacements, newerDotSourceFileReplacement{
			sourcePath: sourcePath,
			backupPath: backupPath,
		})
		if err := copyRegularDotFileReplace(targetPath, sourcePath); err != nil {
			return fmt.Errorf("copy newer local %q into repo source %q: %w", targetPath, sourcePath, err)
		}
		return nil
	}
	replaced, err := walkNewerLocalDotFiles(entry, replaceOne)
	if err != nil {
		if rollbackErr := rollback(); rollbackErr != nil {
			return nil, fmt.Errorf("%w (rollback failed: %v)", err, rollbackErr)
		}
		return nil, err
	}
	added, err := WalkLocalOnlyDotFiles(entry, addOne)
	if err != nil {
		if rollbackErr := rollback(); rollbackErr != nil {
			return nil, fmt.Errorf("%w (rollback failed: %v)", err, rollbackErr)
		}
		return nil, err
	}
	if !replaced && !added {
		if err := os.RemoveAll(backupRoot); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("remove newer source staging: %w", err)
		}
		return nil, fmt.Errorf("no local changes found for %q", entry.Name)
	}
	return &dotSourceReplacement{
		commit: func() error {
			return os.RemoveAll(backupRoot)
		},
		rollback: rollback,
	}, nil
}

func walkNewerLocalDotFiles(entry ResolvedEntry, replaceOne func(sourcePath, targetPath string) error) (bool, error) {
	sourceInfo, sourceErr := os.Lstat(entry.SourcePath)
	if sourceErr != nil {
		return false, sourceErr
	}
	targetInfo, targetErr := os.Lstat(entry.TargetPath)
	if targetErr != nil {
		return false, targetErr
	}
	if sourceInfo.Mode().IsRegular() {
		if !LocalFileIsNewer(sourceInfo, targetInfo) {
			return false, nil
		}
		if replaceOne == nil {
			return true, nil
		}
		return true, replaceOne(entry.SourcePath, entry.TargetPath)
	}
	if !sourceInfo.IsDir() || sourceInfo.Mode()&os.ModeSymlink != 0 || !targetInfo.IsDir() || targetInfo.Mode()&os.ModeSymlink != 0 {
		return false, nil
	}
	ignores := CombinedIgnores(entry.Ignore)
	found := false
	err := filepath.WalkDir(entry.SourcePath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(entry.SourcePath, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		if ShouldIgnoreDotPath(entry.SourcePath, rel, d.Name(), ignores) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		sourceInfo, infoErr := os.Lstat(path)
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
		if SameResolvedPath(targetPath, path) {
			return nil
		}
		if sourceInfo.IsDir() && sourceInfo.Mode()&os.ModeSymlink == 0 {
			if targetInfo.IsDir() && targetInfo.Mode()&os.ModeSymlink == 0 {
				return nil
			}
			return fmt.Errorf("local target %q conflicts with repo directory %q", targetPath, path)
		}
		if targetInfo.Mode()&os.ModeSymlink != 0 {
			target, readErr := os.Readlink(targetPath)
			if readErr != nil {
				return fmt.Errorf("read local link %q: %w", targetPath, readErr)
			}
			if !filepath.IsAbs(target) {
				target = filepath.Clean(filepath.Join(filepath.Dir(targetPath), target))
			}
			if PathExists(target) {
				return fmt.Errorf("local target %q links outside managed source", targetPath)
			}
			return nil
		}
		if LocalFileIsNewer(sourceInfo, targetInfo) {
			found = true
			if replaceOne == nil {
				return nil
			}
			return replaceOne(path, targetPath)
		}
		return fmt.Errorf("local target %q is not newer than repo source %q", targetPath, path)
	})
	if err != nil {
		return false, err
	}
	return found, nil
}

func copyRegularDotFileReplace(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%q is not a regular file", src)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		cleanup()
		return err
	}
	if _, err := io.Copy(tmp, in); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Chtimes(tmpPath, info.ModTime(), info.ModTime()); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func oldSourceStagingParent(sourcePath, packageRoot string) string {
	if packageRoot == "" {
		return filepath.Dir(sourcePath)
	}
	rel, err := filepath.Rel(packageRoot, sourcePath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return filepath.Dir(sourcePath)
	}
	return filepath.Dir(packageRoot)
}

func restoreDotBackupAfterFailedStow(ctx context.Context, exec executor.Executor, backupPath, originalPath string) error {
	if err := RemoveLocalPathAfterBackupWithExecutor(ctx, exec, originalPath, backupPath); err != nil {
		return fmt.Errorf("remove partial target: %w", err)
	}
	return RestoreBackupPath(backupPath, originalPath)
}

func RestoreBackupPath(backupPath, originalPath string) error {
	info, err := os.Lstat(backupPath)
	if err != nil {
		return err
	}
	if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		return filepath.WalkDir(backupPath, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, relErr := filepath.Rel(backupPath, path)
			if relErr != nil {
				return relErr
			}
			target := filepath.Join(originalPath, rel)
			if d.IsDir() {
				return os.MkdirAll(target, 0o755)
			}
			return restoreBackupFile(path, target)
		})
	}
	return restoreBackupFile(backupPath, originalPath)
}

func restoreBackupFile(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.Symlink(target, dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// CopyDotPath — Follows symlinks into their targets and guards against symlink cycles.
func CopyDotPath(src, dst string, ignores []string) error {
	return copyDotPathSeen(src, dst, ignores, src, ".", make(map[string]struct{}))
}

func copyDotPathSeen(src, dst string, ignores []string, logicalRoot, logicalRel string, seenDirs map[string]struct{}) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(src)
		if err != nil {
			// Broken symlink — target doesn't exist. Skip silently.
			return nil
		}
		return copyDotPathSeen(resolved, dst, ignores, logicalRoot, logicalRel, seenDirs)
	}
	if info.IsDir() {
		if resolved, err := filepath.EvalSymlinks(src); err == nil {
			resolved = filepath.Clean(resolved)
			if _, ok := seenDirs[resolved]; ok {
				return fmt.Errorf("dot copy source %q resolves into a symlink cycle at %q", src, resolved)
			}
			seenDirs[resolved] = struct{}{}
			defer delete(seenDirs, resolved)
		}
		return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, relErr := filepath.Rel(src, path)
			if relErr != nil {
				return relErr
			}
			pathLogicalRel := logicalRel
			if rel != "." {
				pathLogicalRel = joinDotLogicalRel(logicalRel, rel)
			}
			if rel != "." && ShouldIgnoreDotPath(logicalRoot, pathLogicalRel, d.Name(), ignores) {
				if d.IsDir() {
					if IgnoredDotDirHasIncludedDescendant(logicalRoot, pathLogicalRel, ignores) {
						return nil
					}
					return filepath.SkipDir
				}
				return nil
			}
			target := filepath.Join(dst, rel)
			entryInfo, infoErr := os.Lstat(path)
			if infoErr != nil {
				return infoErr
			}
			if entryInfo.IsDir() && entryInfo.Mode()&os.ModeSymlink == 0 {
				return os.MkdirAll(target, entryInfo.Mode().Perm())
			}
			return copyDotPathSeen(path, target, ignores, logicalRoot, pathLogicalRel, seenDirs)
		})
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	return CopyDotFile(src, dst, info.Mode().Perm())
}

func joinDotLogicalRel(parent, rel string) string {
	if parent == "" || parent == "." {
		return rel
	}
	return filepath.Join(parent, rel)
}

// CopyDotFile — Fails if dst already exists.
func CopyDotFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		if closeErr := out.Close(); closeErr != nil {
			return fmt.Errorf("%w (close failed: %v)", err, closeErr)
		}
		return err
	}
	return out.Close()
}
