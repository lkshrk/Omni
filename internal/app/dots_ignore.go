package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/dots"
)

func (a *App) DotsAddIgnorePattern(name, pattern string) error {
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
func (a *App) DotsEjectIgnoredPaths(name, pattern string) (int, error) {
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
	// Build ignore list WITHOUT the new pattern so copyDotPath doesn't skip
	// the very files we're ejecting.
	var copyIgnores []string
	for _, ig := range entry.Ignore {
		if ig != pattern {
			copyIgnores = append(copyIgnores, ig)
		}
	}
	var ejected int
	walkErr := filepath.WalkDir(entry.TargetPath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(entry.TargetPath, path)
		if relErr != nil || rel == "." {
			return relErr
		}
		if !dots.ShouldIgnorePath(rel, d.Name(), patterns) {
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
		if !sameResolvedPath(path, sourcePath) {
			return nil
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
			if cpErr := copyDotPath(sourcePath, path, copyIgnores); cpErr != nil {
				// Restore symlink on copy failure to avoid torn state.
				_ = os.Symlink(sourcePath, path) // best-effort restore
				return fmt.Errorf("copy dir %q → %q: %w", sourcePath, path, cpErr)
			}
		} else {
			if cpErr := copyDotFile(sourcePath, path, sInfo.Mode().Perm()); cpErr != nil {
				_ = os.Symlink(sourcePath, path) // best-effort restore
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
		if !dots.ShouldIgnorePath(rel, d.Name(), patterns) {
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
func (a *App) DotsSetEntryIgnored(name, path string, ignored bool) error {
	return a.withConfig(func(rootCfg *config.RootConfig) error {
		if err := a.requireDotsEnabled(rootCfg); err != nil {
			return err
		}
		for _, g := range rootCfg.Groups {
			for i, d := range g.Dots {
				if d.Name != name && (path == "" || d.Path != path) {
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
}

// DotsRemoveIgnorePattern removes a per-entry ignore glob from the named dots
// entry in config. Removing a pattern that is not present is a no-op.
func (a *App) DotsRemoveIgnorePattern(name, pattern string) error {
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
