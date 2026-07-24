package app

import (
	"context"
	"fmt"
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
	pf, err := a.dotService().preflight()
	if err != nil {
		return nil, err
	}
	defer func() {
		a.recordDotsHistoryResult(ctx, "resolve "+string(strategy), name, pf.repoPath, ops, err, false)
		a.refreshDotsStateAfterSuccess(ctx, &err, false)
	}()
	stowPath, err := ensureDotsContentPath(pf.repoPath)
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
	if err := a.requireSafeTestDotsMutation(pf.repoPath, []config.DotEntry{{Name: entry.Name, Path: entry.TargetPath}}); err != nil {
		return nil, err
	}
	state, _ := dots.ClassifyEntry(entry)
	// Modified entries resolve the same two ways a conflict does: use-local
	// adopts the diverged local content into the repo, use-repo discards it
	// and restores the repo version.
	if state != dots.StateConflict && state != dots.StateModified {
		return nil, fmt.Errorf("dots resolve %q: state %q does not require conflict resolution", name, state)
	}
	switch strategy {
	case DotResolveUseRepo:
		return dots.ResolveUseRepo(ctx, a.newExecutor(), stowPath, entry)
	case DotResolveUseLocal:
		return dots.ResolveUseLocal(ctx, a.newExecutor(), pf.repoPath, stowPath, entry)
	default:
		return nil, fmt.Errorf("dots resolve %q: unknown strategy %q", name, strategy)
	}
}

// DotsResolveConflictPath resolves one selected path inside a dots entry
// without restowing or replacing its siblings.
func (a *App) DotsResolveConflictPath(ctx context.Context, name, relPath string, strategy DotsResolveStrategy) (ops []dots.Op, err error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("dots resolve path: entry name is required")
	}
	relPath, err = cleanDotRelativePath(relPath)
	if err != nil {
		return nil, fmt.Errorf("dots resolve %q: %w", name, err)
	}
	pf, err := a.dotService().preflight()
	if err != nil {
		return nil, err
	}
	qualifiedName := name + "/" + filepath.ToSlash(relPath)
	defer func() {
		a.recordDotsHistoryResult(ctx, "resolve "+string(strategy), qualifiedName, pf.repoPath, ops, err, false)
		a.refreshDotsStateAfterSuccess(ctx, &err, false)
	}()
	stowPath, err := ensureDotsContentPath(pf.repoPath)
	if err != nil {
		return nil, fmt.Errorf("dots resolve %q: content dir: %w", qualifiedName, err)
	}
	parent, err := a.resolvedDotEntry(name, stowPath)
	if err != nil {
		return nil, err
	}
	entry := dots.ResolvedEntry{
		Name:       qualifiedName,
		Package:    parent.Package,
		SourcePath: filepath.Join(parent.SourcePath, relPath),
		TargetPath: filepath.Join(parent.TargetPath, relPath),
	}
	if err := a.requireSafeTestDotsMutation(pf.repoPath, []config.DotEntry{{Name: entry.Name, Path: entry.TargetPath}}); err != nil {
		return nil, err
	}
	state := classifyDotPathState(entry.SourcePath, entry.TargetPath, nil, dots.StateConflict)
	switch strategy {
	case DotResolveUseRepo:
		if state != dots.StateConflict && state != dots.StateModified && state != dots.StateMissing && state != dots.StateRepoOnly && state != dots.StateBroken {
			return nil, fmt.Errorf("dots resolve %q: state %q cannot use repo", qualifiedName, state)
		}
		return resolveDotUseRepoWith(ctx, a.newExecutor(), entry, func() error {
			return linkResolvedDotPath(entry)
		})
	case DotResolveUseLocal:
		if state != dots.StateConflict && state != dots.StateModified && state != dots.StateLocalOnly {
			return nil, fmt.Errorf("dots resolve %q: state %q cannot use local", qualifiedName, state)
		}
		return resolveDotUseLocalWith(ctx, a.newExecutor(), pf.repoPath, filepath.Join(stowPath, parent.Package), entry, func() error {
			return linkResolvedDotPath(entry)
		})
	default:
		return nil, fmt.Errorf("dots resolve %q: unknown strategy %q", qualifiedName, strategy)
	}
}

func cleanDotRelativePath(relPath string) (string, error) {
	relPath = filepath.Clean(filepath.FromSlash(strings.TrimSpace(relPath)))
	if relPath == "." || filepath.IsAbs(relPath) || relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid relative path %q", relPath)
	}
	return relPath, nil
}

func linkResolvedDotPath(entry dots.ResolvedEntry) error {
	if err := dots.ValidateHomeTargetPath(entry.TargetPath); err != nil {
		return fmt.Errorf("refusing to relink %q: %w", entry.TargetPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(entry.TargetPath), 0o755); err != nil {
		return err
	}
	return dots.WriteStowShapedSymlink(entry.TargetPath, entry.SourcePath)
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
	mgr, err := dots.NewEngine(stowPath, entries)
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

func resolveDotUseRepoWith(ctx context.Context, exec executor.Executor, entry dots.ResolvedEntry, relink func() error) ([]dots.Op, error) {
	return dots.ResolveUseRepoWith(ctx, exec, entry, relink)
}

func resolveDotUseLocalWith(ctx context.Context, exec executor.Executor, repoPath, packageRoot string, entry dots.ResolvedEntry, relink func() error) ([]dots.Op, error) {
	return dots.ResolveUseLocalWith(ctx, exec, repoPath, packageRoot, entry, relink)
}

// ─── stow availability ────────────────────────────────────────────────────────
