package app

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

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/dots"
	"github.com/lkshrk/omni/internal/executor"
)

func (a *App) requireStow(ctx context.Context) error {
	if a.DotsStowInstalled(ctx) {
		return nil
	}
	return fmt.Errorf(
		"stow is required for dotfile mutate operations (sync, add, resolve, delete) " +
			"but was not found on PATH\n" +
			"  macOS:  brew install stow\n" +
			"  Debian: apt install stow\n" +
			"  Arch:   pacman -S stow\n" +
			"  Fedora: dnf install stow",
	)
}

// DotsStowInstalled reports whether GNU Stow is currently reachable on PATH.
func (a *App) DotsStowInstalled(ctx context.Context) bool {
	return dots.CheckInstalled(ctx, executor.New())
}

// InstallDotsStow installs GNU Stow through the system provider family.
// Callers own user consent before invoking this; this method may run the host
// package manager and can require privileges depending on the concrete manager.
func (a *App) InstallDotsStow(ctx context.Context) error {
	if a.DotsStowInstalled(ctx) {
		return nil
	}
	settings, _ := a.LoadSettings()
	providerName, err := a.resolveProvider(ctx, SystemInstallPriority(settings))
	if err != nil {
		return fmt.Errorf("install stow: %w", err)
	}
	if err := a.Install(ctx, "stow", providerName); err != nil {
		return fmt.Errorf("install stow: %w", err)
	}
	if !a.DotsStowInstalled(ctx) {
		return fmt.Errorf("install stow: completed but stow is still not available on PATH")
	}
	return nil
}

// ─── private helpers ──────────────────────────────────────────────────────────

// dotsRepoPath returns the configured dots repo path for the current host,
// applying host-specific settings overrides via EffectiveSettings.
func (a *App) dotsRepoPath() string {
	cfg, err := a.loadConfig()
	if err != nil {
		return ""
	}
	return a.effectiveSettings(cfg).DotsRepo
}

// newGitForRepo creates a Git instance for repoPath.
func newGitForRepo(repoPath string, exec executor.Executor) *dots.Git {
	return dots.NewGit(repoPath, exec)
}

func (a *App) buildDotsManager() (*dots.Manager, map[string]string, map[string]bool, error) {
	repoPath, err := resolveRepoPath(a.dotsRepoPath())
	if err != nil {
		return nil, nil, nil, err
	}
	if err := a.requireSafeTestDotsMutation(repoPath, nil); err != nil {
		return nil, nil, nil, err
	}
	stowPath := dotsContentPath(repoPath)
	rootCfg, err := a.loadConfig()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("dots: load groups: %w", err)
	}
	groups := rootCfg.Groups
	if effective, _, ok := effectiveHostGroups(rootCfg, groups, currentMachineGroupName()); ok {
		groups = effective
	}
	entries := collectDots(rootCfg, groups)
	variantMap := activeDotVariantMap(entries, currentMachineGroupName())
	entries = resolveDotEntryPackagesForCurrentHost(entries)
	if err := a.requireSafeTestDotsMutation(repoPath, entries); err != nil {
		return nil, nil, nil, err
	}
	groupMap := collectDotsGroupMap(groups)
	mgr, err := dots.New(stowPath, entries)
	return mgr, groupMap, variantMap, err
}

// resolveRepoPath validates that a non-empty repo path is configured, expands
// ~ and environment variables, and enforces that the result is an absolute path.
func resolveRepoPath(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("dots_repo is not configured; set it via 'omni ui' (Dots tab) or settings.dots_repo in settings.json")
	}
	expanded, err := dots.ExpandPath(raw)
	if err != nil {
		return "", fmt.Errorf("dots_repo: expand path: %w", err)
	}
	abs := filepath.Clean(expanded)
	if !filepath.IsAbs(abs) {
		return "", fmt.Errorf("dots_repo must be an absolute path, got %q", raw)
	}
	return abs, nil
}

func dotsContentPath(repoPath string) string {
	return filepath.Join(repoPath, dotsContentDirName)
}

func existingDotsContentPath(repoPath string) (string, error) {
	path := dotsContentPath(repoPath)
	if err := validateDotsContentDir(path); err != nil {
		return "", err
	}
	return path, nil
}

func ensureDotsContentPath(repoPath string) (string, error) {
	path := dotsContentPath(repoPath)
	if err := validateDotsContentDir(path); err == nil {
		return path, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", err
	}
	if err := validateDotsContentDir(path); err != nil {
		return "", err
	}
	return path, nil
}

func validateDotsContentDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%q is a symlink; dots content dir must be a real directory", path)
	}
	if !info.IsDir() {
		return fmt.Errorf("%q is not a directory", path)
	}
	return nil
}

// stowPackagePath returns where the source file/dir belongs inside the stow
// package tree. It mirrors the home directory structure:
//
//	~/.config/nvim  →  <repo>/dotfiles/nvim/.config/nvim
//	~/.zshrc        →  <repo>/dotfiles/zsh/.zshrc
func stowPackagePath(stowPath, pkgName, absPath string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	rel, err := filepath.Rel(home, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is not under home directory", absPath)
	}
	return filepath.Join(stowPath, pkgName, rel), nil
}

func normalizeDotsVariantHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return currentMachineGroupName()
	}
	return machineGroupName(host)
}

func defaultDotVariantPackage(name, host string) string {
	return name + "@" + host
}

type dotVariantSourceResult struct {
	CleanupPath string
	Created     bool
}

func ensureDotVariantSource(stowPath string, entry config.DotEntry, pkgName string) (dotVariantSourceResult, error) {
	targetPath, err := dots.ExpandPath(entry.Path)
	if err != nil {
		return dotVariantSourceResult{}, fmt.Errorf("expand target path: %w", err)
	}
	targetPath, err = filepath.Abs(targetPath)
	if err != nil {
		return dotVariantSourceResult{}, fmt.Errorf("target path: %w", err)
	}
	targetPath = filepath.Clean(targetPath)
	dst, err := stowPackagePath(stowPath, pkgName, targetPath)
	if err != nil {
		return dotVariantSourceResult{}, err
	}
	if _, err := os.Lstat(dst); err == nil {
		return dotVariantSourceResult{}, nil
	} else if !os.IsNotExist(err) {
		return dotVariantSourceResult{}, fmt.Errorf("stat package source: %w", err)
	}

	pkgRoot := filepath.Join(stowPath, pkgName)
	pkgRootExisted := true
	if info, err := os.Lstat(pkgRoot); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return dotVariantSourceResult{}, fmt.Errorf("package root %q is not a real directory", pkgRoot)
		}
	} else if os.IsNotExist(err) {
		pkgRootExisted = false
	} else {
		return dotVariantSourceResult{}, fmt.Errorf("stat package root: %w", err)
	}

	src, err := localDotCopySource(targetPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return dotVariantSourceResult{}, fmt.Errorf("local target %q: %w", targetPath, err)
		}
		src, err = stowPackagePath(stowPath, entry.EffectivePackage(), targetPath)
		if err != nil {
			return dotVariantSourceResult{}, err
		}
		if _, err := os.Lstat(src); err != nil {
			return dotVariantSourceResult{}, fmt.Errorf("default package source %q: %w", src, err)
		}
	}
	cleanupPath := dst
	if !pkgRootExisted {
		cleanupPath = pkgRoot
	}
	if err := copyDotPath(src, dst, combinedDotIgnores(entry.Ignore)); err != nil {
		if removeErr := os.RemoveAll(cleanupPath); removeErr != nil {
			return dotVariantSourceResult{}, fmt.Errorf("seed package source: %w (cleanup failed: %v)", err, removeErr)
		}
		return dotVariantSourceResult{}, fmt.Errorf("seed package source: %w", err)
	}
	return dotVariantSourceResult{CleanupPath: cleanupPath, Created: true}, nil
}

// normalisePath converts a path under $HOME to ~/... form for persisted config.
// Falls back to the cleaned path when it is not under HOME.
func normalisePath(path string) string {
	if path == "" {
		return ""
	}
	expanded, err := dots.ExpandPath(path)
	if err != nil {
		return path
	}
	cleaned := filepath.Clean(expanded)
	if !filepath.IsAbs(cleaned) {
		return filepath.ToSlash(cleaned)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return cleaned
	}
	home = filepath.Clean(home)
	rel, err := filepath.Rel(home, cleaned)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return cleaned
	}
	if rel == "." {
		return "~"
	}
	return "~/" + filepath.ToSlash(rel)
}
func syncResolvedDotEntry(ctx context.Context, repoPath, stowPath string, entry dots.ResolvedEntry, opts dots.SyncOptions, failUnsyncable bool) ([]dots.Op, error) {
	// Pre-stow: remove repo source files matching ignore patterns so stow never
	// attempts to link them. Handles legacy entries where files were committed
	// before their ignore pattern (or DotsEjectIgnoredPaths) existed.
	if !opts.DryRun {
		purgeIgnoredRepoSources(entry)
	}
	state, _ := classifyDotEntry(entry)
	switch state {
	case DotStateSynced:
		if isFoldedDotDirectory(entry) {
			if opts.DryRun {
				return []dots.Op{{Kind: dots.OpDryRepair, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
			}
			if err := dots.Restow(ctx, executor.New(), stowPath, []string{entry.Package}, false); err != nil {
				return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}}, err
			}
			return []dots.Op{lstatEntryOp(entry, false)}, nil
		}
		return []dots.Op{{Kind: dots.OpSkip, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
	case DotStateMissing:
		if opts.DryRun {
			return []dots.Op{{Kind: dots.OpDryLink, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
		}
		if err := dots.Restow(ctx, executor.New(), stowPath, []string{entry.Package}, false); err != nil {
			return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}}, err
		}
		return []dots.Op{lstatEntryOp(entry, false)}, nil
	case DotStateBroken:
		if opts.DryRun {
			return []dots.Op{{Kind: dots.OpDryRepair, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
		}
		prep, err := prepareDotTargetForRestow(entry)
		if err != nil {
			return nil, err
		}
		if err := dots.Restow(ctx, executor.New(), stowPath, []string{entry.Package}, false); err != nil {
			if prep.backupPath != "" {
				if restoreErr := restoreDotTargetAfterFailedRestow(entry, prep); restoreErr != nil {
					return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}},
						fmt.Errorf("%w (restore failed: %v)", err, restoreErr)
				}
			}
			return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}}, err
		}
		return []dots.Op{{Kind: dots.OpRepair, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
	case DotStateLocalOnly:
		if opts.DryRun {
			return []dots.Op{{Kind: dots.OpDryAdopt, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
		}
		op, err := syncLocalOnlyDotEntry(ctx, stowPath, entry)
		if err != nil {
			return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}}, err
		}
		return []dots.Op{op}, nil
	case DotStateModified:
		if opts.DryRun {
			return []dots.Op{{Kind: dots.OpDryAdopt, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
		}
		ops, err := syncModifiedDotEntry(ctx, repoPath, stowPath, entry)
		if err != nil {
			return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}}, err
		}
		return ops, nil
	case DotStateConflict, DotStateUntrackedConflict, DotStateAmbiguous:
		if state == DotStateConflict && dotConflictIsManagedStowLink(entry, stowPath) {
			if opts.DryRun {
				return []dots.Op{{Kind: dots.OpDryRepair, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
			}
			prep, err := prepareDotTargetForRestow(entry)
			if err != nil {
				return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}}, err
			}
			if err := dots.Restow(ctx, executor.New(), stowPath, []string{entry.Package}, false); err != nil {
				if prep.backupPath != "" {
					if restoreErr := restoreDotTargetAfterFailedRestow(entry, prep); restoreErr != nil {
						return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}},
							fmt.Errorf("%w (restore failed: %v)", err, restoreErr)
					}
				}
				return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}}, err
			}
			return []dots.Op{{Kind: dots.OpRepair, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
		}
		if state == DotStateConflict {
			// Per-entry on_conflict wins; otherwise fall back to a sync-wide
			// ConflictStrategy (e.g. `dots sync --use-repo`).
			strategy := entry.OnConflict
			if strategy == "" {
				strategy = opts.ConflictStrategy
			}
			switch strategy {
			case "use_repo":
				if opts.DryRun {
					return []dots.Op{{Kind: dots.OpDryRepair, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
				}
				return resolveDotUseRepo(ctx, stowPath, entry)
			case "use_local":
				if opts.DryRun {
					return []dots.Op{{Kind: dots.OpDryAdopt, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
				}
				return resolveDotUseLocal(ctx, repoPath, stowPath, entry)
			}
		}
		err := fmt.Errorf("requires choosing use repo version or use local version")
		return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}}, err
	default:
		err := fmt.Errorf("state %q is not syncable; remove or ignore this entry", state)
		if failUnsyncable {
			return []dots.Op{{Kind: dots.OpSkip, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}}, err
		}
		return []dots.Op{{Kind: dots.OpSkip, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}}, nil
	}
}

func syncLocalOnlyDotEntry(ctx context.Context, stowPath string, entry dots.ResolvedEntry) (dots.Op, error) {
	copySource, err := localDotCopySource(entry.TargetPath)
	if err != nil {
		return dots.Op{}, err
	}
	if err := copyDotPath(copySource, entry.SourcePath, combinedDotIgnores(entry.Ignore)); err != nil {
		if removeErr := os.RemoveAll(entry.SourcePath); removeErr != nil {
			return dots.Op{}, fmt.Errorf("copy local into repo: %w (remove created source failed: %v)", err, removeErr)
		}
		return dots.Op{}, fmt.Errorf("copy local into repo: %w", err)
	}
	prep, err := prepareDotTargetForRestow(entry)
	if err != nil {
		if cleanupErr := os.RemoveAll(entry.SourcePath); cleanupErr != nil {
			return dots.Op{}, fmt.Errorf("prepare local target: %w (remove created source failed: %v)", err, cleanupErr)
		}
		return dots.Op{}, fmt.Errorf("prepare local target: %w", err)
	}
	if err := dots.Restow(ctx, executor.New(), stowPath, []string{entry.Package}, false); err != nil {
		if removeErr := os.RemoveAll(entry.SourcePath); removeErr != nil {
			return dots.Op{}, fmt.Errorf("%w (remove created source failed: %v)", err, removeErr)
		}
		if prep.backupPath != "" {
			if restoreErr := restoreDotTargetAfterFailedRestow(entry, prep); restoreErr != nil {
				return dots.Op{}, fmt.Errorf("%w (restore failed: %v)", err, restoreErr)
			}
		}
		return dots.Op{}, err
	}
	return dots.Op{Kind: dots.OpAdopt, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}, nil
}

func syncModifiedDotEntry(ctx context.Context, repoPath, stowPath string, entry dots.ResolvedEntry) (ops []dots.Op, retErr error) {
	if pathExists(entry.SourcePath) {
		gt := newGitForRepo(repoPath, executor.New())
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
	prep, err := prepareDotTargetForRestow(entry)
	if err != nil {
		return nil, fmt.Errorf("prepare local target: %w", err)
	}
	if err := dots.Restow(ctx, executor.New(), stowPath, []string{entry.Package}, false); err != nil {
		if prep.backupPath != "" {
			if restoreErr := restoreDotTargetAfterFailedRestow(entry, prep); restoreErr != nil {
				return nil, fmt.Errorf("%w (restore failed: %v)", err, restoreErr)
			}
		}
		return nil, err
	}
	committedSource = true
	if err := replacement.commit(); err != nil {
		return nil, fmt.Errorf("cleanup source backup: %w", err)
	}
	return []dots.Op{{Kind: dots.OpAdopt, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
}

func dotConflictIsManagedStowLink(entry dots.ResolvedEntry, stowPath string) bool {
	local := inspectDotLocal(entry).kind
	// Accept dotLocalContent for directories: inspectManagedDotDirectory may
	// classify a fully wrong-linked directory as content when no paths point
	// to the current source. The walk below still correctly identifies stow
	// links, so we let it make the final determination.
	if local != dotLocalWrongLink && local != dotLocalContent {
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
		if shouldIgnoreDotPath(entry.SourcePath, rel, d.Name(), combinedDotIgnores(entry.Ignore)) {
			if d.IsDir() {
				if ignoredDotDirHasIncludedDescendant(entry.SourcePath, rel, combinedDotIgnores(entry.Ignore)) {
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
		if sameResolvedPath(targetPath, sourcePath) {
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

func localDotCopySource(targetPath string) (string, error) {
	if _, err := os.Lstat(targetPath); err != nil {
		return "", err
	}
	return targetPath, nil
}

func backupAndRemoveLocalTarget(targetPath string) (string, error) {
	return dots.BackupAndRemoveLocalPath(targetPath)
}

func backupLocalTarget(targetPath string) (string, error) {
	backupPath, backupErr := dots.BackupLocalPath(targetPath)
	if backupErr != nil && !os.IsNotExist(backupErr) {
		return "", fmt.Errorf("backup %q: %w", targetPath, backupErr)
	}
	return backupPath, nil
}

type preparedDotTarget struct {
	backupPath          string
	preservedDirectory  bool
	removedManagedPaths bool
}

func prepareDotTargetForRestow(entry dots.ResolvedEntry) (preparedDotTarget, error) {
	if shouldPreserveDirectoryDotTarget(entry) {
		prep := preparedDotTarget{preservedDirectory: true}
		backupPath, err := backupLocalTarget(entry.TargetPath)
		if err != nil {
			return prep, err
		}
		prep.backupPath = backupPath
		prep.removedManagedPaths = true
		if err := removeManagedDotTargetPaths(entry, backupPath); err != nil {
			if backupPath != "" {
				if restoreErr := restorePreparedDirectoryTargetAfterFailedRestow(entry, prep); restoreErr != nil {
					return prep, fmt.Errorf("%w (restore failed: %v)", err, restoreErr)
				}
			}
			return prep, err
		}
		return prep, nil
	}
	backupPath, err := backupAndRemoveLocalTarget(entry.TargetPath)
	if err != nil {
		return preparedDotTarget{}, err
	}
	return preparedDotTarget{backupPath: backupPath}, nil
}

func shouldPreserveDirectoryDotTarget(entry dots.ResolvedEntry) bool {
	sourceInfo, err := os.Lstat(entry.SourcePath)
	if err != nil || !sourceInfo.IsDir() || sourceInfo.Mode()&os.ModeSymlink != 0 {
		return false
	}
	targetInfo, err := os.Lstat(entry.TargetPath)
	return err == nil && targetInfo.IsDir() && targetInfo.Mode()&os.ModeSymlink == 0
}

func isFoldedDotDirectory(entry dots.ResolvedEntry) bool {
	sourceInfo, err := os.Lstat(entry.SourcePath)
	if err != nil || !sourceInfo.IsDir() || sourceInfo.Mode()&os.ModeSymlink != 0 {
		return false
	}
	targetInfo, err := os.Lstat(entry.TargetPath)
	if err != nil || targetInfo.Mode()&os.ModeSymlink == 0 {
		return false
	}
	return sameResolvedPath(entry.TargetPath, entry.SourcePath)
}

func removeManagedDotTargetPaths(entry dots.ResolvedEntry, backupPath string) error {
	ignores := combinedDotIgnores(entry.Ignore)
	if err := removeManagedDotTargetFiles(entry, ignores, backupPath); err != nil {
		return err
	}
	if err := removeDotTargetDirectoryConflicts(entry, ignores, backupPath); err != nil {
		return err
	}
	return removeEmptyUnmanagedDotTargetDirs(entry, ignores)
}

func removeManagedDotTargetFiles(entry dots.ResolvedEntry, ignores []string, backupPath string) error {
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
		if shouldIgnoreDotPath(entry.TargetPath, rel, d.Name(), ignores) {
			if d.IsDir() {
				if ignoredDotDirHasIncludedDescendant(entry.TargetPath, rel, ignores) {
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
		if sameResolvedPath(path, sourcePath) {
			return nil
		}
		return dots.RemoveLocalPathAfterBackup(path, backupPath)
	})
}

func removeDotTargetDirectoryConflicts(entry dots.ResolvedEntry, ignores []string, backupPath string) error {
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
		if shouldIgnoreDotPath(entry.SourcePath, rel, d.Name(), ignores) {
			if d.IsDir() {
				if ignoredDotDirHasIncludedDescendant(entry.SourcePath, rel, ignores) {
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
			return dots.RemoveLocalPathAfterBackup(targetPath, backupPath)
		}
		if targetInfo.IsDir() && targetInfo.Mode()&os.ModeSymlink == 0 {
			if err := dots.RemoveLocalPathAfterBackup(targetPath, backupPath); err != nil {
				return fmt.Errorf("replace directory %q with managed file: %w", targetPath, err)
			}
		}
		return nil
	})
}

func removeEmptyUnmanagedDotTargetDirs(entry dots.ResolvedEntry, ignores []string) error {
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
		if shouldIgnoreDotPath(entry.TargetPath, rel, d.Name(), ignores) {
			if ignoredDotDirHasIncludedDescendant(entry.TargetPath, rel, ignores) {
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

func restoreDotTargetAfterFailedRestow(entry dots.ResolvedEntry, prep preparedDotTarget) error {
	if prep.preservedDirectory {
		return restorePreparedDirectoryTargetAfterFailedRestow(entry, prep)
	}
	return restoreDotBackupAfterFailedStow(prep.backupPath, entry.TargetPath)
}

func restorePreparedDirectoryTargetAfterFailedRestow(entry dots.ResolvedEntry, prep preparedDotTarget) error {
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
		if err := dots.RemoveLocalPathAfterBackup(targetItem, prep.backupPath); err != nil {
			return err
		}
		return restoreBackupFile(path, targetItem)
	})
}

func combinedDotIgnores(ignores []string) []string {
	return append(dots.DefaultIgnores(), ignores...)
}

func copyDotPath(src, dst string, ignores []string) error {
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
			if rel != "." && shouldIgnoreDotPath(logicalRoot, pathLogicalRel, d.Name(), ignores) {
				if d.IsDir() {
					if ignoredDotDirHasIncludedDescendant(logicalRoot, pathLogicalRel, ignores) {
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
	return copyDotFile(src, dst, info.Mode().Perm())
}

func joinDotLogicalRel(parent, rel string) string {
	if parent == "" || parent == "." {
		return rel
	}
	return filepath.Join(parent, rel)
}

func copyDotFile(src, dst string, mode os.FileMode) error {
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

func shouldIgnoreDotPath(root, relPath, basename string, ignores []string) bool {
	rooted := filepath.ToSlash(filepath.Join(filepath.Base(root), relPath))
	return dots.ShouldIgnoreAnyPath([]string{relPath, rooted}, basename, ignores)
}

func ignoredDotDirHasIncludedDescendant(root, relPath string, ignores []string) bool {
	if dots.HasIncludedDescendant(relPath, ignores) {
		return true
	}
	rooted := filepath.ToSlash(filepath.Join(filepath.Base(root), relPath))
	return dots.HasIncludedDescendant(rooted, ignores)
}

// purgeIgnoredRepoSources removes files from the repo source tree that match
// the entry's ignore patterns. This prevents stow from attempting to link
// ignored paths. It handles the case where files were committed before their
// ignore pattern existed or before DotsEjectIgnoredPaths was available.
// Returns the number of top-level paths removed.
func purgeIgnoredRepoSources(entry dots.ResolvedEntry) int {
	if len(entry.Ignore) == 0 {
		return 0
	}
	srcInfo, err := os.Lstat(entry.SourcePath)
	if err != nil || !srcInfo.IsDir() {
		return 0
	}
	var purged int
	_ = filepath.WalkDir(entry.SourcePath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(entry.SourcePath, path)
		if relErr != nil || rel == "." {
			return relErr
		}
		if !dots.ShouldIgnorePath(rel, d.Name(), entry.Ignore) {
			if d.IsDir() && !dots.HasIncludedDescendant(rel, entry.Ignore) {
				return filepath.SkipDir
			}
			return nil
		}
		_ = os.RemoveAll(path)
		purged++
		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	return purged
}

// lstatOp returns an Op that describes the current link state at dst.
func lstatOp(entryName, dst, repoPath string, dryRun bool) dots.Op {
	info, err := os.Lstat(dst)
	if os.IsNotExist(err) {
		if dryRun {
			return dots.Op{Kind: dots.OpDryLink, Entry: entryName, Dst: dst}
		}
		return dots.Op{Kind: dots.OpConflict, Entry: entryName, Dst: dst,
			Err: fmt.Errorf("path not linked after stow")}
	}
	if err != nil {
		return dots.Op{Kind: dots.OpConflict, Entry: entryName, Dst: dst, Err: err}
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, _ := os.Readlink(dst)
		// Stow may create relative symlinks; resolve to absolute for comparison.
		absTarget := target
		if !filepath.IsAbs(absTarget) {
			absTarget = filepath.Clean(filepath.Join(filepath.Dir(dst), absTarget))
		}
		if pathWithinDir(absTarget, repoPath) {
			if dryRun {
				return dots.Op{Kind: dots.OpSkip, Entry: entryName, Dst: dst}
			}
			return dots.Op{Kind: dots.OpLink, Entry: entryName, Src: absTarget, Dst: dst}
		}
		return dots.Op{Kind: dots.OpConflict, Entry: entryName, Dst: dst,
			Err: fmt.Errorf("symlink points elsewhere: %s", target)}
	}
	return dots.Op{Kind: dots.OpConflict, Entry: entryName, Dst: dst,
		Err: fmt.Errorf("real file at %q; use omni dots add --adopt to migrate", dst)}
}

func lstatEntryOp(entry dots.ResolvedEntry, dryRun bool) dots.Op {
	local := inspectDotLocal(entry)
	switch local.kind {
	case dotLocalExpectedLink:
		if dryRun {
			return dots.Op{Kind: dots.OpSkip, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}
		}
		return dots.Op{Kind: dots.OpLink, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}
	case dotLocalMissing:
		if dryRun {
			return dots.Op{Kind: dots.OpDryLink, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}
		}
		return dots.Op{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath,
			Err: fmt.Errorf("path not linked after stow")}
	case dotLocalBrokenLink:
		return dots.Op{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath,
			Err: fmt.Errorf("managed link is broken")}
	default:
		return dots.Op{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath,
			Err: fmt.Errorf("real file at %q; use omni dots add --adopt to migrate", entry.TargetPath)}
	}
}

func pathWithinDir(path, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// collectDots gathers all DotEntry values from a slice of groups.
func collectDots(cfg *config.RootConfig, groups []*config.GroupConfig) []config.DotEntry {
	var entries []config.DotEntry
	seen := make(map[string]struct{})
	ignored := make(map[string]struct{}, len(cfg.Ignore.Dots))
	for _, name := range cfg.Ignore.Dots {
		ignored[name] = struct{}{}
	}
	for _, g := range groups {
		for _, entry := range g.Dots {
			if _, ok := seen[entry.Name]; ok {
				continue
			}
			seen[entry.Name] = struct{}{}
			if _, ok := ignored[entry.Name]; ok {
				entry.Ignored = true
			}
			entries = append(entries, entry)
		}
	}
	return entries
}

func resolveDotEntryPackagesForCurrentHost(entries []config.DotEntry) []config.DotEntry {
	if len(entries) == 0 {
		return entries
	}
	host := currentMachineGroupName()
	out := make([]config.DotEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, resolveDotEntryPackageForHost(entry, host))
	}
	return out
}

func activeDotVariantMap(entries []config.DotEntry, host string) map[string]bool {
	variants := make(map[string]bool)
	for _, entry := range entries {
		if _, ok := entry.Hosts[host]; ok {
			variants[entry.Name] = true
		}
	}
	return variants
}

func resolveDotEntryPackageForHost(entry config.DotEntry, host string) config.DotEntry {
	entry.Package = entry.PackageForHost(host)
	return entry
}

func dotEntryPackages(entry config.DotEntry) []string {
	seen := map[string]bool{}
	add := func(pkg string, out *[]string) {
		pkg = strings.TrimSpace(pkg)
		if pkg == "" || seen[pkg] {
			return
		}
		seen[pkg] = true
		*out = append(*out, pkg)
	}
	var packages []string
	add(entry.EffectivePackage(), &packages)
	for _, variant := range entry.Hosts {
		add(variant.Package, &packages)
	}
	sort.Strings(packages)
	return packages
}

func dotEntriesForAllPackages(entry config.DotEntry) []config.DotEntry {
	packages := dotEntryPackages(entry)
	if len(packages) == 0 {
		return nil
	}
	entries := make([]config.DotEntry, 0, len(packages))
	for _, pkgName := range packages {
		resolved := entry
		resolved.Package = pkgName
		resolved.Hosts = nil
		entries = append(entries, resolved)
	}
	return entries
}

func filterActiveDotEntries(entries []config.DotEntry) []config.DotEntry {
	filtered := entries[:0]
	for _, entry := range entries {
		if entry.Ignored {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

// collectDotsGroupMap returns a map from dots entry name → group base name.
func collectDotsGroupMap(groups []*config.GroupConfig) map[string]string {
	groupSets := make(map[string][]string)
	for _, g := range groups {
		for _, d := range g.Dots {
			groupSets[d.Name] = appendUniqueStringValue(groupSets[d.Name], g.BaseName())
		}
	}
	m := make(map[string]string, len(groupSets))
	for name, groups := range groupSets {
		sort.Strings(groups)
		m[name] = compactDotGroupLabel(groups)
	}
	return m
}

func appendUniqueStringValue(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func compactDotGroupLabel(groups []string) string {
	switch len(groups) {
	case 0:
		return ""
	case 1:
		return groups[0]
	case 2:
		return groups[0] + "," + groups[1]
	default:
		return groups[0] + "," + groups[1] + ",+" + fmt.Sprintf("%d", len(groups)-2)
	}
}

func findDotEntryInConfig(cfg *config.RootConfig, name string) (config.DotEntry, bool) {
	for _, group := range cfg.Groups {
		for _, entry := range group.Dots {
			if entry.Name == name {
				return entry, true
			}
		}
	}
	return config.DotEntry{}, false
}

func findDotEntryPtrInConfig(cfg *config.RootConfig, name string) (*config.DotEntry, bool) {
	for _, group := range cfg.Groups {
		if group == nil {
			continue
		}
		for i := range group.Dots {
			if group.Dots[i].Name == name {
				return &group.Dots[i], true
			}
		}
	}
	return nil, false
}

func dotPackageOwner(cfg *config.RootConfig, pkgName string) (string, bool) {
	pkgName = strings.ToLower(strings.TrimSpace(pkgName))
	if pkgName == "" {
		return "", false
	}
	for _, group := range cfg.Groups {
		if group == nil {
			continue
		}
		for _, entry := range group.Dots {
			for _, pkg := range dotEntryPackages(entry) {
				if strings.ToLower(pkg) == pkgName {
					return entry.Name, true
				}
			}
		}
	}
	return "", false
}

func dotPackageReferencedInConfig(cfg *config.RootConfig, pkgName string) bool {
	_, ok := dotPackageOwner(cfg, pkgName)
	return ok
}

// expandAndStat expands environment variables and ~, then confirms the path exists.
func expandAndStat(path string) (string, error) {
	expanded, err := dots.ExpandPath(path)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(abs); err != nil {
		return "", fmt.Errorf("path %q: %w", abs, err)
	}
	return abs, nil
}

// inferName derives a human-readable entry name from an absolute path.
// Strips a leading dot for hidden files (e.g. ".zshrc" → "zshrc").
func inferName(abs string) string {
	base := filepath.Base(abs)
	return strings.TrimPrefix(base, ".")
}

// entryHealth computes a DotStatus for each ResolvedEntry in the manager
// by lstat-ing the expected symlink locations. groupMap maps entry name →
// group base name and is used to populate the Group field; may be nil.
