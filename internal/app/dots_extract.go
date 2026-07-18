package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lkshrk/omni/internal/dots"
)

// DotsExtractOptions configures DotsExtract.
type DotsExtractOptions struct {
	// Name is the logical name for the new child entry. Empty derives one from
	// the parent name and the subpath.
	Name string
	// Group is the group the new child entry is assigned to. Empty assigns it to
	// the current host group (matching DotsAdd). The group is created if missing.
	Group string
}

// DotsExtract splits subpath out of the tracked directory entry parentName into
// its own dot entry, letting the subtree live in a different group than its
// parent. It works by composing two existing operations:
//
//  1. Add an ignore for the subtree to the parent and eject it — the parent
//     stops managing those paths and their symlinks become real files again.
//  2. Adopt the now-real files as a brand-new entry in the target group.
//
// This reuses the battle-tested ignore/eject/adopt paths rather than hand-rolling
// the symlink choreography, so the parent and child end up cleanly stowed.
func (a *App) DotsExtract(ctx context.Context, parentName, subpath string, opts DotsExtractOptions) ([]dots.Op, error) {
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

	parentSub := filepath.Join(parent.SourcePath, subrel)
	if !dots.PathExists(parentSub) {
		return nil, fmt.Errorf("dots extract: %q is not tracked under %q", subrel, parentName)
	}

	// Anchor the ignore to the entry root so a top-level subdir name does not
	// also match same-named directories deeper in the tree. Directories get a
	// trailing slash so the whole subtree is matched.
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

	// Safety snapshot before moving files around the repo.
	if g := newGitForRepo(repoPath, a.newExecutor()); g.IsRepo() {
		if err := g.SnapshotAll(ctx, "dots: pre-extract "+childName); err != nil {
			return nil, fmt.Errorf("dots extract: snapshot repo: %w", err)
		}
	}

	// With --no-folding the parent links each file individually, so the home
	// subtree is a real directory of symlinks (not a single folded symlink) and
	// can't be ejected by pattern. Instead materialise it from the parent package
	// as real files in place, then drop it from the parent package; DotsAdd then
	// re-adopts those real files into a new package + entry.
	if _, err := dots.BackupLocalPathWithExecutor(ctx, a.newExecutor(), childTarget); err != nil {
		return nil, fmt.Errorf("dots extract: backup %q: %w", childTarget, err)
	}
	if err := os.RemoveAll(childTarget); err != nil {
		return nil, fmt.Errorf("dots extract: clear %q: %w", childTarget, err)
	}
	if err := dots.CopyDotPath(parentSub, childTarget, dots.CombinedIgnores(nil)); err != nil {
		return nil, fmt.Errorf("dots extract: materialise %q: %w", childTarget, err)
	}
	if err := os.RemoveAll(parentSub); err != nil {
		return nil, fmt.Errorf("dots extract: drop subtree from %q package: %w", parentName, err)
	}

	if err := a.DotsAddIgnorePatternContext(ctx, parentName, pattern); err != nil {
		return nil, fmt.Errorf("dots extract: ignore subtree in %q: %w", parentName, err)
	}

	ops, err := a.DotsAdd(ctx, childTarget, DotsAddOptions{Name: childName, Group: opts.Group, Adopt: true})
	if err != nil {
		return nil, fmt.Errorf("dots extract: adopt %q as %q: %w", childTarget, childName, err)
	}
	return ops, nil
}

// DotsExtractThenAddHostVariant splits subpath out of parentName into its own
// dot entry and then adds a host-specific variant for that new entry, letting a
// child sub-path row become a host variant in a single step. It returns the new
// entry's variant info plus the combined ops from both operations.
func (a *App) DotsExtractThenAddHostVariant(ctx context.Context, parentName, subpath string, opts DotsAddVariantOptions) (DotVariantInfo, []dots.Op, error) {
	childName := DotExtractName(parentName, subpath)
	ops, err := a.DotsExtract(ctx, parentName, subpath, DotsExtractOptions{})
	if err != nil {
		return DotVariantInfo{}, ops, err
	}
	info, variantOps, err := a.DotsAddHostVariant(ctx, childName, opts)
	return info, append(ops, variantOps...), err
}

// DotsExtractWithState runs DotsExtract and returns the refreshed dots state for
// TUI callers.
func (a *App) DotsExtractWithState(ctx context.Context, parentName, subpath string, opts DotsExtractOptions) (*DotsOperationStateResult, error) {
	return a.dotsOperationStateAfter(ctx, func() ([]dots.Op, error) {
		return a.DotsExtract(ctx, parentName, subpath, opts)
	})
}

// DotExtractName returns the default child entry name DotsExtract would assign
// for a subpath under parentName, so callers (e.g. the TUI group picker) can
// preview the new entry's name before committing.
func DotExtractName(parentName, subpath string) string {
	subrel, err := cleanExtractSubpath(subpath)
	if err != nil {
		return sanitizeEntryName(parentName + "-extracted")
	}
	return deriveExtractName(parentName, subrel)
}

// cleanExtractSubpath normalises a subpath that must be relative to and contained
// within the parent entry.
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

// deriveExtractName builds a default child entry name from the parent name and
// the last component of the subpath, sanitised to a single valid name token.
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
