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

func (a *App) DotsAddIgnorePattern(name, pattern string) (err error) {
	return a.DotsAddIgnorePatternContext(context.Background(), name, pattern)
}

func (a *App) DotsAddIgnorePatternContext(ctx context.Context, name, pattern string) (err error) {
	defer func() {
		a.refreshDotsStateAfterSuccess(ctx, &err, false)
	}()
	if err := dots.ValidateIgnorePattern(pattern); err != nil {
		return err
	}
	return a.withConfig(func(rootCfg *config.RootConfig) error {
		if err := a.requireDotsEnabled(rootCfg); err != nil {
			return err
		}
		for _, g := range rootCfg.Groups {
			for i, d := range g.Dots {
				if d.Name != name {
					continue
				}
				for _, existing := range d.Ignore {
					if existing == pattern {
						return errSkipSave
					}
				}
				g.Dots[i].Ignore = append(g.Dots[i].Ignore, pattern)
				return nil
			}
		}
		return fmt.Errorf("dots entry %q not found", name)
	})
}

// DotsEjectIgnoredPaths replaces managed symlinks that now match the given
// ignore pattern with real file copies and removes the corresponding source
// files from the repo. Call after DotsAddIgnorePattern to clean up previously
// synced paths that are now ignored. Returns the number of ejected paths.
func (a *App) DotsEjectIgnoredPaths(name, pattern string) (ejected int, err error) {
	return a.DotsEjectIgnoredPathsContext(context.Background(), name, pattern)
}

func (a *App) DotsEjectIgnoredPathsContext(ctx context.Context, name, pattern string) (ejected int, err error) {
	defer func() {
		a.refreshDotsStateAfterSuccess(ctx, &err, false)
	}()
	repoPath, err := resolveRepoPath(a.dotsRepoPath())
	if err != nil {
		return 0, err
	}
	stowPath := dotsContentPath(repoPath)
	entry, err := a.resolvedDotEntry(name, stowPath)
	if err != nil {
		return 0, err
	}
	srcInfo, err := os.Lstat(entry.SourcePath)
	if err != nil || !srcInfo.IsDir() {
		return 0, nil
	}

	patterns := []string{pattern}
	patternIM := dots.CompileIgnoresLenient(patterns)
	// Build ignore list WITHOUT the new pattern so dots.CopyDotPath doesn't skip
	// the very files we're ejecting.
	var copyIgnores []string
	for _, ig := range entry.Ignore {
		if ig != pattern {
			copyIgnores = append(copyIgnores, ig)
		}
	}
	walkErr := filepath.WalkDir(entry.TargetPath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(entry.TargetPath, path)
		if relErr != nil || rel == "." {
			return relErr
		}
		if !patternIM.Ignored(rel, d.Name()) {
			return nil
		}
		info, infoErr := os.Lstat(path)
		if infoErr != nil {
			if os.IsNotExist(infoErr) {
				return nil
			}
			return fmt.Errorf("stat %q: %w", path, infoErr)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		sourcePath := filepath.Join(entry.SourcePath, rel)
		if !dots.SameResolvedPath(path, sourcePath) {
			return nil
		}
		if err := dots.ValidateHomeTargetPath(path); err != nil {
			return fmt.Errorf("refusing to eject managed symlink %q: %w", path, err)
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove managed symlink %q: %w", path, err)
		}
		sInfo, sErr := os.Lstat(sourcePath)
		if sErr != nil {
			ejected++
			return nil
		}
		if sInfo.IsDir() {
			if cpErr := dots.CopyDotPath(sourcePath, path, copyIgnores); cpErr != nil {
				if restoreErr := dots.WriteStowShapedSymlink(path, sourcePath); restoreErr != nil {
					return fmt.Errorf("copy dir %q → %q: %w (restore symlink failed: %v)", sourcePath, path, cpErr, restoreErr)
				}
				return fmt.Errorf("copy dir %q → %q: %w", sourcePath, path, cpErr)
			}
		} else {
			if cpErr := dots.CopyDotFile(sourcePath, path, sInfo.Mode().Perm()); cpErr != nil {
				if restoreErr := dots.WriteStowShapedSymlink(path, sourcePath); restoreErr != nil {
					return fmt.Errorf("copy %q → %q: %w (restore symlink failed: %v)", sourcePath, path, cpErr, restoreErr)
				}
				return fmt.Errorf("copy %q → %q: %w", sourcePath, path, cpErr)
			}
		}
		if removeErr := os.RemoveAll(sourcePath); removeErr != nil {
			return fmt.Errorf("remove repo source %q: %w", sourcePath, removeErr)
		}
		ejected++
		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	if walkErr != nil {
		return ejected, walkErr
	}

	// Second pass: purge repo source files matching the pattern that have no
	// corresponding symlink at the target (e.g. target already has real files).
	purgeErr := filepath.WalkDir(entry.SourcePath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(entry.SourcePath, path)
		if relErr != nil || rel == "." {
			return relErr
		}
		if !patternIM.Ignored(rel, d.Name()) {
			return nil
		}
		if removeErr := os.RemoveAll(path); removeErr != nil {
			return fmt.Errorf("remove repo source %q: %w", path, removeErr)
		}
		ejected++
		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	return ejected, purgeErr
}

// DotsSetEntryIgnored toggles whole-entry dotfile ignore state. When ignoring
// an untracked discovery candidate, the ignored entry is persisted to this
// machine group so discovery does not keep suggesting it.
func (a *App) DotsSetEntryIgnored(name, path string, ignored bool) (err error) {
	return a.DotsSetEntryIgnoredContext(context.Background(), name, path, ignored)
}

func (a *App) DotsSetEntryIgnoredContext(ctx context.Context, name, path string, ignored bool) (err error) {
	defer func() {
		a.refreshDotsStateAfterSuccess(ctx, &err, false)
	}()
	var (
		stowPath       string
		removedEntries []deletedDotEntry
		trackedEntry   *config.DotEntry
	)
	err = a.withConfig(func(rootCfg *config.RootConfig) error {
		if err := a.requireDotsEnabled(rootCfg); err != nil {
			return err
		}
		if ignored {
			matchedIgnored := false
			for _, g := range rootCfg.Groups {
				for i := 0; i < len(g.Dots); i++ {
					d := g.Dots[i]
					if !dotEntryMatchesNameOrPath(d, name, path) {
						continue
					}
					if d.Ignored {
						matchedIgnored = true
						continue
					}
					if trackedEntry == nil {
						copyDot := d
						trackedEntry = &copyDot
					}
					removedEntries = append(removedEntries, deletedDotEntry{group: g.Name, dot: d})
					g.Dots = append(g.Dots[:i], g.Dots[i+1:]...)
					i--
				}
			}
			if trackedEntry != nil {
				if strings.TrimSpace(path) == "" {
					path = trackedEntry.Path
				}
				repoPath, err := resolveRepoPath(a.effectiveSettings(rootCfg).DotsRepo)
				if err != nil {
					return err
				}
				if err := a.requireSafeTestDotsMutation(repoPath, []config.DotEntry{*trackedEntry}); err != nil {
					return err
				}
				stowPath, err = existingDotsContentPath(repoPath)
				if err != nil {
					return fmt.Errorf("dots ignore %q: content dir: %w", name, err)
				}
			}
			group, err := ensureDestinationGroupInConfig(rootCfg, "")
			if err != nil {
				return err
			}
			for _, d := range group.Dots {
				if d.Ignored && dotEntryMatchesNameOrPath(d, name, path) {
					return nil
				}
			}
			if strings.TrimSpace(path) == "" {
				if matchedIgnored {
					return nil
				}
				return fmt.Errorf("dots ignore entry %q: path is required", name)
			}
			group.Dots = append(group.Dots, config.DotEntry{Name: name, Path: normalisePath(path), Ignored: true})
			return nil
		}
		for _, g := range rootCfg.Groups {
			for i, d := range g.Dots {
				if !dotEntryMatchesNameOrPath(d, name, path) {
					continue
				}
				g.Dots[i].Ignored = ignored
				return nil
			}
		}
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("dots ignore entry %q: path is required", name)
		}
		group, err := ensureDestinationGroupInConfig(rootCfg, "")
		if err != nil {
			return err
		}
		group.Dots = append(group.Dots, config.DotEntry{Name: name, Path: normalisePath(path), Ignored: ignored})
		return nil
	})
	if err != nil {
		return err
	}
	if trackedEntry == nil {
		return nil
	}
	if err := a.removeDeletedDotFiles(ctx, name, stowPath, trackedEntry, DotsDeleteOptions{KeepLocal: true}); err != nil {
		if restoreErr := a.restoreDeletedDotConfig(removedEntries); restoreErr != nil {
			return fmt.Errorf("%w (restore config failed: %v)", err, restoreErr)
		}
		return err
	}
	return nil
}

func dotEntryMatchesNameOrPath(entry config.DotEntry, name, path string) bool {
	return entry.Name == name || (path != "" && entry.Path == path)
}

// DotsRemoveIgnorePattern removes a per-entry ignore glob from the named dots
// entry in config. Removing a pattern that is not present is a no-op.
func (a *App) DotsRemoveIgnorePattern(name, pattern string) (err error) {
	return a.DotsRemoveIgnorePatternContext(context.Background(), name, pattern)
}

func (a *App) DotsRemoveIgnorePatternContext(ctx context.Context, name, pattern string) (err error) {
	defer func() {
		a.refreshDotsStateAfterSuccess(ctx, &err, false)
	}()
	return a.withConfig(func(rootCfg *config.RootConfig) error {
		if err := a.requireDotsEnabled(rootCfg); err != nil {
			return err
		}
		for _, g := range rootCfg.Groups {
			for i, d := range g.Dots {
				if d.Name != name {
					continue
				}
				for j, existing := range d.Ignore {
					if existing != pattern {
						continue
					}
					g.Dots[i].Ignore = append(d.Ignore[:j], d.Ignore[j+1:]...)
					return nil
				}
				return errSkipSave
			}
		}
		return fmt.Errorf("dots entry %q not found", name)
	})
}

// DotsIncludeIgnoredPath ensures a concrete child path is no longer ignored.
func (a *App) DotsIncludeIgnoredPath(name, relPath string) (err error) {
	return a.DotsIncludeIgnoredPathContext(context.Background(), name, relPath)
}

func (a *App) DotsIncludeIgnoredPathContext(ctx context.Context, name, relPath string) (err error) {
	rel, includePattern, err := dotIncludePattern(relPath)
	if err != nil {
		return err
	}
	includeDir := a.includedDotPathIsDir(name, rel)
	defer func() {
		a.refreshDotsStateAfterSuccess(ctx, &err, false)
	}()
	return a.withConfig(func(rootCfg *config.RootConfig) error {
		if err := a.requireDotsEnabled(rootCfg); err != nil {
			return err
		}
		for _, g := range rootCfg.Groups {
			for i, d := range g.Dots {
				if d.Name != name {
					continue
				}
				targetPath, expandErr := dots.ExpandPath(d.Path)
				if expandErr != nil {
					return expandErr
				}
				if includeDir {
					includePattern += "/"
				}
				original := append([]string(nil), d.Ignore...)
				d.Ignore = removeExactDotIgnorePath(d.Ignore, rel)
				ignored, ignoreErr := dotPathOrAncestorIgnored(targetPath, rel, append(dots.DefaultIgnores(), d.Ignore...))
				if ignoreErr != nil {
					return ignoreErr
				}
				if ignored {
					d.Ignore = removeExactString(d.Ignore, strings.TrimSuffix(includePattern, "/"))
					d.Ignore = removeExactString(d.Ignore, includePattern)
					d.Ignore = append(d.Ignore, includePattern)
				}
				if slices.Equal(original, d.Ignore) {
					return errSkipSave
				}
				g.Dots[i].Ignore = d.Ignore
				return nil
			}
		}
		return fmt.Errorf("dots entry %q not found", name)
	})
}

func (a *App) includedDotPathIsDir(name, rel string) bool {
	repoPath, err := resolveRepoPath(a.dotsRepoPath())
	if err != nil {
		return false
	}
	entry, err := a.resolvedDotEntry(name, dotsContentPath(repoPath))
	if err != nil {
		return false
	}
	for _, root := range []string{entry.TargetPath, entry.SourcePath} {
		if info, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); statErr == nil {
			return info.IsDir()
		}
	}
	return false
}

func dotPathOrAncestorIgnored(root, rel string, patterns []string) (bool, error) {
	matcher, err := dots.CompileIgnores(patterns)
	if err != nil {
		return false, err
	}
	for path := rel; path != "."; path = filepath.Dir(path) {
		rooted := filepath.ToSlash(filepath.Join(filepath.Base(root), path))
		ignored, err := matcher.MatchAnyPath([]string{path, rooted}, filepath.Base(path))
		if err != nil || ignored {
			return ignored, err
		}
	}
	return false, nil
}

func dotIncludePattern(relPath string) (string, string, error) {
	rel, err := cleanDotRelativePath(relPath)
	if err != nil {
		return "", "", err
	}
	rel = filepath.ToSlash(rel)
	includePattern := "!/" + rel
	if err := dots.ValidateIgnorePattern(includePattern); err != nil {
		return "", "", err
	}
	return rel, includePattern, nil
}

func removeExactDotIgnorePath(patterns []string, rel string) []string {
	out := patterns[:0]
	for _, pattern := range patterns {
		if dotIgnorePatternPath(pattern) == rel {
			continue
		}
		out = append(out, pattern)
	}
	return out
}

func removeExactString(values []string, target string) []string {
	out := values[:0]
	for _, value := range values {
		if value == target {
			continue
		}
		out = append(out, value)
	}
	return out
}

func dotIgnorePatternPath(pattern string) string {
	if strings.HasPrefix(pattern, "!") {
		return ""
	}
	pattern = strings.TrimPrefix(pattern, "/")
	pattern = filepath.ToSlash(filepath.Clean(pattern))
	if pattern == "." {
		return ""
	}
	return pattern
}
