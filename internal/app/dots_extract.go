package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/dots"
)

type DotsExtractOptions struct {
	// Empty derives one from the parent name and the subpath.
	Name string
	// Empty assigns the current host group; the group is created if missing.
	Group string
}

// DotsExtract — Composes ignore-and-eject on the parent with adopt into the target group, reusing those tested paths.
func (a *App) DotsExtract(ctx context.Context, parentName, subpath string, opts DotsExtractOptions) (ops []dots.Op, retErr error) {
	parentName = strings.TrimSpace(parentName)
	if parentName == "" {
		return nil, fmt.Errorf("dots extract: parent entry name is required")
	}
	subrel, err := cleanExtractSubpath(subpath)
	if err != nil {
		return nil, fmt.Errorf("dots extract: %w", err)
	}

	rootCfg, err := a.loadConfig()
	if err != nil {
		return nil, fmt.Errorf("dots extract: load config: %w", err)
	}
	if err := a.requireDotsEnabled(rootCfg); err != nil {
		return nil, err
	}
	if _, ok := findDotEntryInConfig(rootCfg, parentName); !ok {
		return nil, fmt.Errorf("dots extract: parent entry %q is not configured", parentName)
	}
	originalGroups := make(map[string]struct{}, len(rootCfg.Groups))
	for _, group := range rootCfg.Groups {
		originalGroups[group.BaseName()] = struct{}{}
	}

	repoPath, err := resolveRepoPath(a.dotsRepoPath())
	if err != nil {
		return nil, err
	}
	stowPath, err := ensureDotsContentPath(repoPath)
	if err != nil {
		return nil, fmt.Errorf("dots extract: content dir: %w", err)
	}
	parent, err := a.resolvedDotEntry(parentName, stowPath)
	if err != nil {
		return nil, fmt.Errorf("dots extract: %w", err)
	}
	if info, statErr := os.Lstat(parent.SourcePath); statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("dots extract: %q is not a directory entry", parentName)
	}

	childTarget := filepath.Join(parent.TargetPath, subrel)
	if rel, relErr := filepath.Rel(parent.TargetPath, childTarget); relErr != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("dots extract: %q is not under entry %q", subpath, parentName)
	}
	info, err := os.Stat(childTarget)
	if err != nil {
		return nil, fmt.Errorf("dots extract: %q does not exist", childTarget)
	}

	childName := strings.TrimSpace(opts.Name)
	if childName == "" {
		childName = deriveExtractName(parentName, subrel)
	}
	if err := dots.ValidateEntryName(childName); err != nil {
		return nil, fmt.Errorf("dots extract: %w", err)
	}
	if _, exists := findDotEntryInConfig(rootCfg, childName); exists {
		return nil, fmt.Errorf("dots extract: entry %q already exists", childName)
	}
	if opts.Group != "" && strings.TrimSpace(opts.Group) == "" {
		return nil, fmt.Errorf("dots extract: group name is required")
	}
	if hostGroup := findGroupInConfig(rootCfg, currentMachineGroupName()); hostGroup != nil && !hostGroup.IsHost() {
		return nil, fmt.Errorf("dots extract: group %q already exists and is not a host group", currentMachineGroupName())
	}
	childPackagePath, err := stowPackagePath(stowPath, childName, childTarget)
	if err != nil {
		return nil, fmt.Errorf("dots extract: %w", err)
	}
	if _, statErr := os.Lstat(childPackagePath); statErr == nil {
		return nil, fmt.Errorf("dots extract: %q is already tracked (repo path: %s)", childName, childPackagePath)
	} else if !os.IsNotExist(statErr) {
		return nil, fmt.Errorf("dots extract: stat child package: %w", statErr)
	}
	childPackageRoot := filepath.Join(stowPath, childName)
	_, childPackageRootErr := os.Lstat(childPackageRoot)
	childPackageRootExisted := childPackageRootErr == nil
	if childPackageRootErr != nil && !os.IsNotExist(childPackageRootErr) {
		return nil, fmt.Errorf("dots extract: stat child package root: %w", childPackageRootErr)
	}

	parentSub := filepath.Join(parent.SourcePath, subrel)
	if !dots.PathExists(parentSub) {
		return nil, fmt.Errorf("dots extract: %q is not tracked under %q", subrel, parentName)
	}

	// Anchored to the entry root so a top-level name does not match same-named dirs deeper in the tree.
	pattern := "/" + filepath.ToSlash(subrel)
	if info.IsDir() {
		pattern += "/"
	}
	if err := dots.ValidateIgnorePattern(pattern); err != nil {
		return nil, fmt.Errorf("dots extract: %w", err)
	}
	if err := a.requireStow(ctx); err != nil {
		return nil, err
	}

	// Preserve the repo state without touching the user's branch before moving files around.
	if g := newGitForRepo(repoPath, a.newExecutor()); g.IsRepo() {
		if err := g.BackupAll(ctx, "dots: pre-extract "+childName); err != nil {
			return nil, fmt.Errorf("dots extract: backup repo: %w", err)
		}
	}

	// With --no-folding the subtree is a real dir of symlinks, so materialise it before re-adopting.
	exec := a.newExecutor()
	backupPath, err := dots.BackupLocalPathWithExecutor(ctx, exec, childTarget)
	if err != nil {
		return nil, fmt.Errorf("dots extract: backup %q: %w", childTarget, err)
	}
	targetMutated := false
	stagingRoot := ""
	stagedSource := ""
	sourceStaged := false
	defer func() {
		if retErr == nil || !targetMutated {
			return
		}
		rollbackCtx := context.WithoutCancel(ctx)
		var rollbackErrs []string
		sourceRestored := !sourceStaged
		if sourceStaged {
			if err := os.MkdirAll(filepath.Dir(parentSub), 0o755); err != nil {
				rollbackErrs = append(rollbackErrs, fmt.Sprintf("create parent source directory: %v", err))
			} else if err := os.Rename(stagedSource, parentSub); err != nil {
				rollbackErrs = append(rollbackErrs, fmt.Sprintf("restore parent source: %v", err))
			} else {
				sourceRestored = true
			}
		}
		if err := a.removeDotsExtractChildConfig(childName, originalGroups); err != nil {
			rollbackErrs = append(rollbackErrs, fmt.Sprintf("remove child config: %v", err))
		}
		childCleanupPath := childPackagePath
		if !childPackageRootExisted {
			childCleanupPath = childPackageRoot
		}
		if err := rollbackDotTargetFromBackup(rollbackCtx, exec, childTarget, childCleanupPath, backupPath); err != nil {
			rollbackErrs = append(rollbackErrs, fmt.Sprintf("restore child extraction: %v", err))
		}
		if stagingRoot != "" && sourceRestored {
			if err := os.RemoveAll(stagingRoot); err != nil && !os.IsNotExist(err) {
				rollbackErrs = append(rollbackErrs, fmt.Sprintf("remove source staging: %v", err))
			}
		}
		if len(rollbackErrs) > 0 {
			retErr = fmt.Errorf("%w (rollback failed: %s)", retErr, strings.Join(rollbackErrs, "; "))
		}
	}()
	targetMutated = true
	if err := os.RemoveAll(childTarget); err != nil {
		return nil, fmt.Errorf("dots extract: clear %q: %w", childTarget, err)
	}
	if err := dots.CopyDotPath(parentSub, childTarget, dots.CombinedIgnores(nil)); err != nil {
		return nil, fmt.Errorf("dots extract: materialise %q: %w", childTarget, err)
	}
	stagingRoot, err = os.MkdirTemp(stowPath, ".omni-extract-*")
	if err != nil {
		return nil, fmt.Errorf("dots extract: stage subtree from %q package: %w", parentName, err)
	}
	stagedSource = filepath.Join(stagingRoot, filepath.Base(parentSub))
	if err := os.Rename(parentSub, stagedSource); err != nil {
		return nil, fmt.Errorf("dots extract: stage subtree from %q package: %w", parentName, err)
	}
	sourceStaged = true

	ops, err = a.DotsAdd(ctx, childTarget, DotsAddOptions{Name: childName, Group: opts.Group, Adopt: true})
	if err != nil {
		return nil, fmt.Errorf("dots extract: adopt %q as %q: %w", childTarget, childName, err)
	}
	if err := a.DotsAddIgnorePatternContext(ctx, parentName, pattern); err != nil {
		return nil, fmt.Errorf("dots extract: ignore subtree in %q: %w", parentName, err)
	}
	if err := os.RemoveAll(stagingRoot); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "warning: omni: remove dots extract staging %q: %v\n", stagingRoot, err)
	}
	return ops, nil
}

func (a *App) removeDotsExtractChildConfig(name string, originalGroups map[string]struct{}) error {
	return a.withConfig(func(rootCfg *config.RootConfig) error {
		changed := false
		groups := rootCfg.Groups[:0]
		for _, group := range rootCfg.Groups {
			before := len(group.Dots)
			group.Dots = slices.DeleteFunc(group.Dots, func(entry config.DotEntry) bool {
				return entry.Name == name
			})
			changed = changed || len(group.Dots) != before
			_, existed := originalGroups[group.BaseName()]
			newEmpty := !existed && group.Description == "" && len(group.Dots) == 0 && len(group.Tools) == 0 && len(group.Taps) == 0
			if !newEmpty {
				groups = append(groups, group)
			} else {
				changed = true
			}
		}
		rootCfg.Groups = groups
		if !changed {
			return errSkipSave
		}
		return nil
	})
}

// DotsExtractThenAddHostVariant — Returns the new entry's variant info plus the combined ops from both operations.
func (a *App) DotsExtractThenAddHostVariant(ctx context.Context, parentName, subpath string, opts DotsAddVariantOptions) (DotVariantInfo, []dots.Op, error) {
	childName := DotExtractName(parentName, subpath)
	ops, err := a.DotsExtract(ctx, parentName, subpath, DotsExtractOptions{})
	if err != nil {
		return DotVariantInfo{}, ops, err
	}
	info, variantOps, err := a.DotsAddHostVariant(ctx, childName, opts)
	return info, append(ops, variantOps...), err
}

func (a *App) DotsExtractWithState(ctx context.Context, parentName, subpath string, opts DotsExtractOptions) (*DotsOperationStateResult, error) {
	return a.dotsOperationStateAfter(ctx, func(ctx context.Context) ([]dots.Op, error) {
		return a.DotsExtract(ctx, parentName, subpath, opts)
	})
}

// DotExtractName — Lets callers preview the new entry's name before committing.
func DotExtractName(parentName, subpath string) string {
	subrel, err := cleanExtractSubpath(subpath)
	if err != nil {
		return sanitizeEntryName(parentName + "-extracted")
	}
	return deriveExtractName(parentName, subrel)
}

func cleanExtractSubpath(subpath string) (string, error) {
	s := strings.TrimSpace(subpath)
	if s == "" {
		return "", fmt.Errorf("subpath is required")
	}
	if filepath.IsAbs(s) {
		return "", fmt.Errorf("subpath %q must be relative to the entry", subpath)
	}
	s = filepath.Clean(filepath.FromSlash(s))
	if s == "." || s == ".." || strings.HasPrefix(s, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("subpath %q escapes the entry", subpath)
	}
	return s, nil
}

func deriveExtractName(parentName, subrel string) string {
	return sanitizeEntryName(parentName + "-" + filepath.Base(subrel))
}

func sanitizeEntryName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "extracted"
	}
	return out
}
