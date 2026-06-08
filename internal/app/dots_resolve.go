package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/dots"
	"github.com/lkshrk/omni/internal/executor"
)

func (a *App) DotsResolveConflict(ctx context.Context, name string, strategy DotsResolveStrategy) (ops []dots.Op, err error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("dots resolve: entry name is required")
	}
	rootCfg, err := a.loadConfig()
	if err != nil {
		return nil, fmt.Errorf("dots resolve: load config: %w", err)
	}
	if err := a.requireDotsEnabled(rootCfg); err != nil {
		return nil, err
	}
	repoPath, err := resolveRepoPath(a.dotsRepoPath())
	if err != nil {
		return nil, err
	}
	if err := a.requireSafeTestDotsMutation(repoPath, nil); err != nil {
		return nil, err
	}
	defer func() {
		a.recordDotsHistoryResult(ctx, "resolve "+string(strategy), name, repoPath, ops, err, false)
		a.refreshDotsStateAfterSuccess(ctx, &err, false)
	}()
	stowPath, err := ensureDotsContentPath(repoPath)
	if err != nil {
		return nil, fmt.Errorf("dots resolve %q: content dir: %w", name, err)
	}
	if err := a.requireStow(ctx); err != nil {
		return nil, err
	}
	entry, err := a.resolvedDotEntry(name, stowPath)
	if err != nil {
		return nil, err
	}
	if err := a.requireSafeTestDotsMutation(repoPath, []config.DotEntry{{Name: entry.Name, Path: entry.TargetPath}}); err != nil {
		return nil, err
	}
	state, _ := classifyDotEntry(entry)
	if state != DotStateConflict {
		return nil, fmt.Errorf("dots resolve %q: state %q does not require conflict resolution", name, state)
	}
	switch strategy {
	case DotResolveUseRepo:
		return resolveDotUseRepo(ctx, stowPath, entry)
	case DotResolveUseLocal:
		return resolveDotUseLocal(ctx, repoPath, stowPath, entry)
	default:
		return nil, fmt.Errorf("dots resolve %q: unknown strategy %q", name, strategy)
	}
}

func (a *App) resolvedDotEntry(name, stowPath string) (dots.ResolvedEntry, error) {
	rootCfg, err := a.loadConfig()
	if err != nil {
		return dots.ResolvedEntry{}, fmt.Errorf("dots resolve: load config: %w", err)
	}
	groups := rootCfg.Groups
	if effective, _, ok := effectiveHostGroups(rootCfg, groups, currentMachineGroupName()); ok {
		groups = effective
	}
	entries := collectDots(rootCfg, groups)
	entries = resolveDotEntryPackagesForCurrentHost(entries)
	mgr, err := dots.New(stowPath, entries)
	if err != nil {
		return dots.ResolvedEntry{}, fmt.Errorf("dots resolve: resolve entries: %w", err)
	}
	for _, entry := range mgr.Entries {
		if entry.Name == name {
			return entry, nil
		}
	}
	return dots.ResolvedEntry{}, fmt.Errorf("dots entry %q not found", name)
}

func resolveDotUseRepo(ctx context.Context, stowPath string, entry dots.ResolvedEntry) ([]dots.Op, error) {
	prep, err := prepareDotTargetForRestow(entry)
	if err != nil {
		return nil, err
	}
	if err := dots.Restow(ctx, executor.New(), stowPath, []string{entry.Package}, false); err != nil {
		wrapped := fmt.Errorf("dots resolve %q: use repo version relink: %w", entry.Name, err)
		if prep.backupPath != "" {
			if restoreErr := restoreDotTargetAfterFailedRestow(entry, prep); restoreErr != nil {
				return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: wrapped}},
					fmt.Errorf("%w (restore failed: %v)", wrapped, restoreErr)
			}
		}
		return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: wrapped}}, wrapped
	}
	return []dots.Op{{Kind: dots.OpRepair, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
}

func resolveDotUseLocal(ctx context.Context, repoPath, stowPath string, entry dots.ResolvedEntry) (ops []dots.Op, retErr error) {
	copySource, err := localDotCopySource(entry.TargetPath)
	if err != nil {
		return nil, err
	}
	if pathExists(entry.SourcePath) {
		gt := newGitForRepo(repoPath, executor.New())
		if gt.IsRepo() {
			if err := gt.SnapshotAll(ctx, "dots: pre-resolve "+entry.Name); err != nil {
				return nil, fmt.Errorf("dots resolve %q: pre-commit repo state: %w", entry.Name, err)
			}
		}
	}
	replacement, err := replaceDotSourceFromLocal(copySource, entry.SourcePath, filepath.Join(stowPath, entry.Package), entry.Ignore)
	if err != nil {
		return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}},
			fmt.Errorf("dots resolve %q: replace repo source: %w", entry.Name, err)
	}
	prep, err := prepareDotTargetForRestow(entry)
	if err != nil {
		if rollbackErr := replacement.rollback(); rollbackErr != nil {
			return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}},
				fmt.Errorf("dots resolve %q: prepare local target: %w (rollback failed: %v)", entry.Name, err, rollbackErr)
		}
		return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}},
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
	if err := dots.Restow(ctx, executor.New(), stowPath, []string{entry.Package}, false); err != nil {
		wrapped := fmt.Errorf("dots resolve %q: use local version relink after copying local content: %w", entry.Name, err)
		if prep.backupPath != "" {
			if restoreErr := restoreDotTargetAfterFailedRestow(entry, prep); restoreErr != nil {
				return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: wrapped}},
					fmt.Errorf("%w (restore failed: %v)", wrapped, restoreErr)
			}
		}
		return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: wrapped}}, wrapped
	}
	committedSource = true
	if err := replacement.commit(); err != nil {
		return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}},
			fmt.Errorf("dots resolve %q: cleanup old source: %w", entry.Name, err)
	}
	return []dots.Op{{Kind: dots.OpAdopt, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
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
	if err := copyDotPath(copySource, tmpPath, combinedDotIgnores(ignores)); err != nil {
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

func replaceModifiedDotSourceFilesFromLocal(entry dots.ResolvedEntry, packageRoot string) (*dotSourceReplacement, error) {
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
		if err := copyDotPath(targetPath, sourcePath, combinedDotIgnores(entry.Ignore)); err != nil {
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
	added, err := walkLocalOnlyDotFiles(entry, addOne)
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

func walkNewerLocalDotFiles(entry dots.ResolvedEntry, replaceOne func(sourcePath, targetPath string) error) (bool, error) {
	sourceInfo, sourceErr := os.Lstat(entry.SourcePath)
	if sourceErr != nil {
		return false, sourceErr
	}
	targetInfo, targetErr := os.Lstat(entry.TargetPath)
	if targetErr != nil {
		return false, targetErr
	}
	if sourceInfo.Mode().IsRegular() {
		if !localFileIsNewer(sourceInfo, targetInfo) {
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
	ignores := combinedDotIgnores(entry.Ignore)
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
		if shouldIgnoreDotPath(entry.SourcePath, rel, d.Name(), ignores) {
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
		if sameResolvedPath(targetPath, path) {
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
			if pathExists(target) {
				return fmt.Errorf("local target %q links outside managed source", targetPath)
			}
			return nil
		}
		if localFileIsNewer(sourceInfo, targetInfo) {
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

func walkLocalOnlyDotFiles(entry dots.ResolvedEntry, addOne func(sourcePath, targetPath string) error) (bool, error) {
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
	ignores := combinedDotIgnores(entry.Ignore)
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
		if shouldIgnoreDotPath(entry.SourcePath, rel, d.Name(), ignores) {
			if d.IsDir() {
				if ignoredDotDirHasIncludedDescendant(entry.SourcePath, rel, ignores) {
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
			if sameResolvedPath(path, sourcePath) {
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
		if !isManagedDotFile(targetInfo.Mode()) {
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

func restoreDotBackupAfterFailedStow(backupPath, originalPath string) error {
	if err := dots.RemoveLocalPathAfterBackup(originalPath, backupPath); err != nil {
		return fmt.Errorf("remove partial target: %w", err)
	}
	return restoreBackupPath(backupPath, originalPath)
}

func restoreBackupPath(backupPath, originalPath string) error {
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

// ─── stow availability ────────────────────────────────────────────────────────
