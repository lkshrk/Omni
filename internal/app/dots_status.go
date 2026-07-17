package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/dots"
)

func (a *App) DotsList() ([]DotStatus, error) {
	m, groupMap, variantMap, err := a.buildDotsManager()
	if err != nil {
		return nil, err
	}
	return entryHealth(m, groupMap, variantMap), nil
}

func (a *App) QueryDots(opts DotsQueryOptions) ([]DotStatus, error) {
	statuses, err := a.DotsList()
	if err != nil {
		return nil, err
	}
	return filterDotStatuses(statuses, opts)
}

// DotsStatus returns DotsList combined with the git status of the dots repo.
func (a *App) DotsStatus(ctx context.Context) (*DotsStatusResult, error) {
	mgr, groupMap, variantMap, err := a.buildDotsManager()
	if err != nil {
		return nil, err
	}
	statuses := entryHealth(mgr, groupMap, variantMap)
	a.attachLastSyncErrors(ctx, statuses)
	memberships, membershipErr := a.DotMembershipMap(ctx)
	var gitStatus string
	repoPath, repoErr := resolveRepoPath(a.dotsRepoPath())
	if repoErr != nil {
		return nil, repoErr
	}
	g := newGitForRepo(repoPath, a.newExecutor())
	if g.IsRepo() {
		gitStatus, err = g.Status(ctx)
		if err != nil {
			return &DotsStatusResult{Entries: statuses, DotMemberships: memberships}, fmt.Errorf("dots status: git status: %w", err)
		}
	}
	result := &DotsStatusResult{Entries: statuses, GitStatus: gitStatus, DotMemberships: memberships}
	return result, membershipErr
}

// DiscoverDotsStatus returns current tracked status plus transient untracked
// discovery candidates. It does not mutate config, the repo, or local files.
func (a *App) DiscoverDotsStatus(ctx context.Context) (*DotsStatusResult, error) {
	result, statusErr := a.DotsStatus(ctx)
	if result == nil {
		result = &DotsStatusResult{}
	}
	result.Entries = append(result.Entries, ignoredChildDotStatuses(result.Entries)...)
	rootCfg, cfgErr := a.loadConfig()
	if cfgErr != nil {
		return result, fmt.Errorf("dots discover: load config: %w", cfgErr)
	}
	rawRepo := a.effectiveSettings(rootCfg).DotsRepo
	repoPath, err := resolveRepoPath(rawRepo)
	if err != nil {
		return result, err
	}
	if err := a.requireSafeTestDotsMutation(repoPath, nil); err != nil {
		return result, err
	}
	allCandidates, discoverErr := discoverDotsEntriesIncludingIgnored(repoPath)
	if discoverErr != nil {
		return result, discoverErr
	}
	var candidates []config.DotEntry
	var ignoredCandidates []config.DotEntry
	for _, candidate := range untrackedDotCandidates(rootCfg, allCandidates) {
		if candidate.Ignored {
			ignoredCandidates = append(ignoredCandidates, candidate)
			continue
		}
		candidates = append(candidates, candidate)
	}
	if len(candidates) == 0 && len(ignoredCandidates) == 0 {
		return result, statusErr
	}
	stowPath := dotsContentPath(repoPath)
	if len(candidates) > 0 {
		mgr, err := dots.New(stowPath, candidates)
		if err != nil {
			return result, fmt.Errorf("dots discover: resolve candidates: %w", err)
		}
		discovered := entryHealth(mgr, nil, nil)
		for i := range discovered {
			discovered[i].State = discoveredDotState(discovered[i])
			discovered[i].Actions = discoveredDotActions(discovered[i].State)
			discovered[i].Health = healthForDotState(discovered[i].State)
		}
		result.Entries = append(result.Entries, discovered...)
		result.Entries = append(result.Entries, ignoredChildDotStatuses(discovered)...)
	}
	if len(ignoredCandidates) > 0 {
		mgr, err := dots.New(stowPath, ignoredCandidates)
		if err != nil {
			return result, fmt.Errorf("dots discover: resolve ignored candidates: %w", err)
		}
		ignored := entryHealth(mgr, nil, nil)
		for i := range ignored {
			ignored[i].Actions = []DotAction{DotActionUnignore}
			ignored[i].State = DotStateIgnored
			ignored[i].Health = healthForDotState(DotStateIgnored)
		}
		result.Entries = append(result.Entries, ignored...)
	}
	result.DiscoveredCount = countTransientDotCandidates(result.Entries)
	return result, statusErr
}

func countTransientDotCandidates(statuses []DotStatus) int {
	count := 0
	for _, status := range statuses {
		if status.Group != "" || status.State == DotStateIgnored || status.State == DotStateInactive || status.State == DotStateDisabled {
			continue
		}
		count++
	}
	return count
}

func ignoredChildDotStatuses(statuses []DotStatus) []DotStatus {
	var ignored []DotStatus
	for _, status := range statuses {
		flat := collectIgnoredFromChildren(status.Children, status.ignoredChildren)
		if len(flat) == 0 {
			continue
		}
		tree := buildIgnoredChildTree(flat, status.TargetPath)
		var totalIgnored int
		for _, child := range tree {
			totalIgnored += countIgnoredTree(child)
		}
		ignored = append(ignored, DotStatus{
			Name:       status.Name,
			SourcePath: status.SourcePath,
			TargetPath: status.TargetPath,
			ConfigPath: status.ConfigPath,
			Health:     healthForDotState(DotStateIgnored),
			State:      DotStateIgnored,
			Actions:    []DotAction{DotActionIgnore, DotActionRemove},
			Group:      status.Group,
			Counts:     DotFileCounts{Ignored: totalIgnored},
			IsDir:      true,
			Children:   tree,
		})
	}
	return ignored
}

// collectIgnoredFromChildren deduplicates ignored children from both the
// visible tree and the separate ignored-children list.
func collectIgnoredFromChildren(children, ignoredChildren []DotChild) []DotChild {
	seen := make(map[string]bool)
	var result []DotChild
	for _, child := range append(children, ignoredChildren...) {
		if !child.Ignored {
			continue
		}
		rel := filepath.ToSlash(child.RelPath)
		if seen[rel] {
			continue
		}
		seen[rel] = true
		result = append(result, child)
	}
	return result
}

// buildIgnoredChildTree reconstructs a tree of DotChild from a flat list of
// ignored entries, grouping files under their parent directories.
func buildIgnoredChildTree(flat []DotChild, targetRoot string) []DotChild {
	type node struct {
		child    DotChild
		children map[string]*node
		order    []string
	}

	root := &node{children: make(map[string]*node)}

	for _, child := range flat {
		rel := filepath.ToSlash(child.RelPath)
		parts := strings.Split(rel, "/")

		cur := root
		for i, part := range parts {
			if cur.children == nil {
				cur.children = make(map[string]*node)
			}
			n, ok := cur.children[part]
			if !ok {
				n = &node{children: make(map[string]*node)}
				if i == len(parts)-1 {
					// Leaf — use the original child data.
					n.child = child
					n.child.Depth = i + 1
				} else {
					// Intermediate directory — synthesize as tree
					// container (not explicitly ignored).
					dirRel := strings.Join(parts[:i+1], "/")
					n.child = DotChild{
						Name:    part,
						RelPath: dirRel,
						Path:    filepath.Join(targetRoot, filepath.FromSlash(dirRel)),
						State:   DotStateIgnored,
						IsDir:   true,
						Depth:   i + 1,
					}
				}
				cur.children[part] = n
				cur.order = append(cur.order, part)
			}
			cur = n
		}
	}

	var buildChildren func(n *node) []DotChild
	buildChildren = func(n *node) []DotChild {
		if len(n.children) == 0 {
			return nil
		}
		result := make([]DotChild, 0, len(n.children))
		for _, key := range n.order {
			child := n.children[key]
			ch := child.child
			ch.Children = buildChildren(child)
			if len(ch.Children) > 0 {
				ch.IsDir = true
			}
			n := countIgnoredTree(ch)
			ch.FileCount = n
			ch.Counts = DotFileCounts{Ignored: n}
			result = append(result, ch)
		}
		// Sort: dirs first, then alphabetically.
		sort.SliceStable(result, func(i, j int) bool {
			if result[i].IsDir != result[j].IsDir {
				return result[i].IsDir
			}
			return result[i].Name < result[j].Name
		})
		return result
	}

	return buildChildren(root)
}

// countIgnoredTree counts all leaves (files and leaf directories) in the tree.
// maxDepth guards against malformed trees with cycles.
const countIgnoredTreeMaxDepth = 64

func countIgnoredTree(child DotChild) int {
	return countIgnoredTreeDepth(child, 0)
}

func countIgnoredTreeDepth(child DotChild, depth int) int {
	if depth > countIgnoredTreeMaxDepth {
		return 0
	}
	if len(child.Children) == 0 {
		return 1
	}
	count := 0
	for _, ch := range child.Children {
		count += countIgnoredTreeDepth(ch, depth+1)
	}
	return count
}

func (a *App) QueryDotsStatus(ctx context.Context, opts DotsQueryOptions) (*DotsStatusResult, error) {
	result, err := a.DotsStatus(ctx)
	if err != nil && result == nil {
		return nil, err
	}
	if result != nil {
		_, cacheErr := a.cacheDotsStatusResult(ctx, result, time.Now().UTC())
		err = errors.Join(err, cacheErr)
	}
	filtered, filterErr := filterDotStatuses(result.Entries, opts)
	if filterErr != nil {
		return nil, filterErr
	}
	result.Entries = filtered
	return result, err
}

func filterDotStatuses(statuses []DotStatus, opts DotsQueryOptions) ([]DotStatus, error) {
	state, err := normalizeDotState(opts.State)
	if err != nil {
		return nil, err
	}
	out := make([]DotStatus, 0, len(statuses))
	for _, status := range statuses {
		if opts.Name != "" && !strings.EqualFold(status.Name, opts.Name) {
			continue
		}
		if state != "" && status.State != state {
			continue
		}
		out = append(out, status)
	}
	return out, nil
}

func normalizeDotState(raw string) (DotState, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return "", nil
	case "ok", "linked", "healthy", "synced":
		return DotStateSynced, nil
	case "missing", "unlinked":
		return DotStateMissing, nil
	case "conflict":
		return DotStateConflict, nil
	case "modified":
		return DotStateModified, nil
	case "broken":
		return DotStateBroken, nil
	case "no-source", "nosource", "source-missing":
		return DotStateNoSource, nil
	case "local-only", "localonly":
		return DotStateLocalOnly, nil
	case "repo-only", "repoonly":
		return DotStateRepoOnly, nil
	case "untracked-linked", "untrackedlinked":
		return DotStateUntrackedLinked, nil
	case "untracked-conflict", "untrackedconflict":
		return DotStateUntrackedConflict, nil
	case "ignored":
		return DotStateIgnored, nil
	case "inactive":
		return DotStateInactive, nil
	case "disabled":
		return DotStateDisabled, nil
	case "ambiguous":
		return DotStateAmbiguous, nil
	default:
		return "", fmt.Errorf("unknown dots state %q", raw)
	}
}

func entryHealth(m *dots.Manager, groupMap map[string]string, variantMap map[string]bool) []DotStatus {
	statuses := make([]DotStatus, 0, len(m.Entries))
	for _, e := range m.Entries {
		state, actions := classifyDotEntry(e)
		contentRoot := dotStatusContentRoot(e)
		ignoreRoot := dotIgnoreRoot(e.SourcePath, e.TargetPath, contentRoot)
		ignores := combinedDotIgnores(e.Ignore)
		counts := dotFileCountsUnion(e.SourcePath, e.TargetPath, contentRoot, "", ignoreRoot, ignores, state)
		fileCount := counts.Managed()
		children := directDotChildren(e.SourcePath, e.TargetPath, contentRoot, ignoreRoot, ignores, state)
		ignoredChildren := ignoredDotChildren(e.SourcePath, e.TargetPath, contentRoot, ignoreRoot, ignores, state)
		if state == DotStateIgnored {
			counts = DotFileCounts{}
			fileCount = 0
			children = nil
			ignoredChildren = nil
		}
		statuses = append(statuses, DotStatus{
			Name:            e.Name,
			Package:         e.Package,
			Variant:         variantMap[e.Name],
			SourcePath:      e.SourcePath,
			TargetPath:      e.TargetPath,
			ConfigPath:      configPathForTarget(e.TargetPath),
			Health:          healthForDotState(state),
			State:           state,
			Actions:         actions,
			Group:           groupMap[e.Name],
			FileCount:       fileCount,
			Counts:          counts,
			IsDir:           dotStatusIsDir(e, contentRoot),
			Children:        children,
			ignoredChildren: ignoredChildren,
		})
	}
	return statuses
}

func discoveredDotState(status DotStatus) DotState {
	sourceExists := pathExists(status.SourcePath)
	targetExists := pathExists(status.TargetPath)
	switch {
	case sourceExists && targetExists:
		return DotStateUntrackedConflict
	case sourceExists:
		return DotStateRepoOnly
	case targetExists:
		return DotStateLocalOnly
	default:
		return DotStateNoSource
	}
}

func discoveredDotActions(state DotState) []DotAction {
	switch state {
	case DotStateUntrackedConflict:
		return []DotAction{DotActionUseRepo, DotActionUseLocal, DotActionIgnore}
	case DotStateLocalOnly, DotStateRepoOnly, DotStateUntrackedLinked:
		return []DotAction{DotActionSync, DotActionIgnore}
	case DotStateIgnored:
		return []DotAction{DotActionUnignore}
	default:
		return []DotAction{DotActionIgnore}
	}
}

func configPathForTarget(target string) string {
	return normalisePath(target)
}

func dotStatusContentRoot(e dots.ResolvedEntry) string {
	if pathExists(e.SourcePath) {
		return e.SourcePath
	}
	local := inspectDotLocal(e)
	if local.kind == dotLocalWrongLink {
		if source, err := localDotCopySource(e.TargetPath); err == nil {
			return source
		}
	}
	if local.kind == dotLocalContent {
		return e.TargetPath
	}
	return e.SourcePath
}

func dotIgnoreRoot(sourceRoot, targetRoot, contentRoot string) string {
	for _, root := range []string{sourceRoot, contentRoot, targetRoot} {
		if root != "" && pathExists(root) {
			return root
		}
	}
	if sourceRoot != "" {
		return sourceRoot
	}
	if contentRoot != "" {
		return contentRoot
	}
	return targetRoot
}

func dotFileCountsUnion(entrySourceRoot, targetRoot, contentRoot, relRoot, ignoreRoot string, ignores []string, parentState DotState) DotFileCounts {
	roots := dotExistingRoots(entrySourceRoot, targetRoot, contentRoot)
	if len(roots) == 0 {
		return DotFileCounts{}
	}
	tracked := make(map[string]bool)
	ignored := make(map[string]bool)
	for _, root := range roots {
		collectDotFileCountRelsFromRoot(root, relRoot, ignoreRoot, ignores, tracked, ignored)
	}
	var counts DotFileCounts
	for rel := range tracked {
		if dotFileCountSynced(dotChildState(entrySourceRoot, targetRoot, filepath.FromSlash(rel), false, ignores, parentState)) {
			counts.Synced++
		} else {
			counts.OutOfSync++
		}
	}
	for rel := range ignored {
		if !tracked[rel] {
			counts.Ignored++
		}
	}
	return counts
}

func collectDotFileCountRelsFromRoot(root, relRoot, ignoreRoot string, ignores []string, tracked, ignored map[string]bool) {
	path := root
	if relRoot != "" {
		path = filepath.Join(root, relRoot)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return
	}
	if !info.IsDir() {
		rel := relRoot
		if rel == "" {
			rel = "."
		}
		if !isManagedDotFile(info.Mode()) {
			return
		}
		if shouldIgnoreDotPath(ignoreRoot, rel, filepath.Base(path), ignores) {
			ignored[filepath.ToSlash(rel)] = true
			return
		}
		tracked[filepath.ToSlash(rel)] = true
		return
	}
	if walkErr := filepath.WalkDir(path, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		walkRel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		if walkRel == "." {
			return nil
		}
		if shouldIgnoreDotPath(ignoreRoot, walkRel, d.Name(), ignores) {
			if d.IsDir() {
				if ignoredDotDirHasIncludedDescendant(ignoreRoot, walkRel, ignores) {
					return nil
				}
				collectIgnoredDotFileCountRels(root, path, ignored)
				return filepath.SkipDir
			}
			info, infoErr := d.Info()
			if infoErr == nil && isManagedDotFile(info.Mode()) {
				ignored[filepath.ToSlash(walkRel)] = true
			}
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		if !d.IsDir() && isManagedDotFile(info.Mode()) {
			tracked[filepath.ToSlash(walkRel)] = true
		}
		return nil
	}); walkErr != nil {
		fmt.Fprintf(os.Stderr, "warning: omni: scanning dot files in %s: %v\n", path, walkErr)
	}
}

func collectIgnoredDotFileCountRels(root, path string, ignored map[string]bool) {
	if walkErr := filepath.WalkDir(path, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil || !isManagedDotFile(info.Mode()) {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr == nil {
			ignored[filepath.ToSlash(rel)] = true
		}
		return nil
	}); walkErr != nil {
		fmt.Fprintf(os.Stderr, "warning: omni: scanning ignored dot files in %s: %v\n", path, walkErr)
	}
}

func dotFileCountSynced(state DotState) bool {
	return state == DotStateSynced
}

func dotExistingRoots(entrySourceRoot, targetRoot, contentRoot string) []string {
	seen := make(map[string]bool)
	roots := make([]string, 0, 3)
	for _, root := range []string{entrySourceRoot, targetRoot, contentRoot} {
		if root == "" || !pathExists(root) {
			continue
		}
		rootPath := root
		clean := filepath.Clean(root)
		if resolved, err := filepath.EvalSymlinks(root); err == nil {
			rootPath = resolved
			clean = filepath.Clean(resolved)
		}
		if seen[clean] {
			continue
		}
		seen[clean] = true
		roots = append(roots, rootPath)
	}
	return roots
}

func dotChildRoots(entrySourceRoot, targetRoot, contentRoot string) []string {
	roots := dotExistingRoots(entrySourceRoot, targetRoot, contentRoot)
	out := roots[:0]
	for _, root := range roots {
		if dotPathIsDir(root) {
			out = append(out, root)
		}
	}
	return out
}

func dotPathIsDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func dotStatusIsDir(e dots.ResolvedEntry, contentRoot string) bool {
	for _, path := range []string{contentRoot, e.SourcePath, e.TargetPath} {
		if path != "" && dotPathIsDir(path) {
			return true
		}
	}
	return false
}

func directDotChildren(entrySourceRoot, targetRoot, contentRoot, ignoreRoot string, ignores []string, parentState DotState) []DotChild {
	roots := dotChildRoots(entrySourceRoot, targetRoot, contentRoot)
	if len(roots) == 0 {
		return nil
	}
	return directDotChildrenAt(entrySourceRoot, targetRoot, roots, ignoreRoot, "", ignores, parentState, 1)
}

type dotChildCandidate struct {
	name  string
	rel   string
	isDir bool
}

func directDotChildrenAt(entrySourceRoot, targetRoot string, roots []string, ignoreRoot, relRoot string, ignores []string, parentState DotState, depth int) []DotChild {
	if depth > dotChildrenMaxDepth {
		return nil
	}
	candidates := make(map[string]dotChildCandidate)
	for _, root := range roots {
		dir := root
		if relRoot != "" {
			dir = filepath.Join(root, relRoot)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			rel := entry.Name()
			if relRoot != "" {
				rel = filepath.Join(relRoot, entry.Name())
			}
			info, infoErr := entry.Info()
			if infoErr != nil || (!entry.IsDir() && !isManagedDotFile(info.Mode())) {
				continue
			}
			candidate := candidates[rel]
			candidate.name = entry.Name()
			candidate.rel = rel
			candidate.isDir = candidate.isDir || entry.IsDir()
			candidates[rel] = candidate
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	ordered := make([]dotChildCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		ordered = append(ordered, candidate)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].isDir != ordered[j].isDir {
			return ordered[i].isDir
		}
		return ordered[i].name < ordered[j].name
	})
	children := make([]DotChild, 0, len(ordered))
	for _, candidate := range ordered {
		ignored := shouldIgnoreDotPath(ignoreRoot, candidate.rel, candidate.name, ignores)
		counts := dotFileCountsUnion(entrySourceRoot, targetRoot, "", candidate.rel, ignoreRoot, ignores, parentState)
		child := DotChild{
			Name:    candidate.name,
			RelPath: candidate.rel,
			Path:    filepath.Join(targetRoot, candidate.rel),
			State:   dotChildState(entrySourceRoot, targetRoot, candidate.rel, ignored, ignores, parentState),
			IsDir:   candidate.isDir,
			Depth:   depth,
			Ignored: ignored,
			Counts:  counts,
		}
		if ignored {
			child.FileCount = 0
		} else if candidate.isDir {
			child.FileCount = counts.Managed()
		} else {
			child.FileCount = 1
		}
		if candidate.isDir && (!ignored || ignoredDotDirHasIncludedDescendant(ignoreRoot, candidate.rel, ignores)) {
			child.Children = directDotChildrenAt(entrySourceRoot, targetRoot, roots, ignoreRoot, candidate.rel, ignores, parentState, depth+1)
		}
		children = append(children, child)
	}
	return children
}

func ignoredDotChildren(entrySourceRoot, targetRoot, contentRoot, ignoreRoot string, ignores []string, parentState DotState) []DotChild {
	roots := dotChildRoots(entrySourceRoot, targetRoot, contentRoot)
	if len(roots) == 0 {
		return nil
	}
	var children []DotChild
	collectIgnoredDotChildrenAt(entrySourceRoot, targetRoot, roots, ignoreRoot, "", ignores, parentState, 1, &children)
	return children
}

func collectIgnoredDotChildrenAt(entrySourceRoot, targetRoot string, roots []string, ignoreRoot, relRoot string, ignores []string, parentState DotState, depth int, out *[]DotChild) {
	candidates := make(map[string]dotChildCandidate)
	for _, root := range roots {
		dir := root
		if relRoot != "" {
			dir = filepath.Join(root, relRoot)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			rel := entry.Name()
			if relRoot != "" {
				rel = filepath.Join(relRoot, entry.Name())
			}
			info, infoErr := entry.Info()
			if infoErr != nil || (!entry.IsDir() && !isManagedDotFile(info.Mode())) {
				continue
			}
			candidate := candidates[rel]
			candidate.name = entry.Name()
			candidate.rel = rel
			candidate.isDir = candidate.isDir || entry.IsDir()
			candidates[rel] = candidate
		}
	}
	if len(candidates) == 0 {
		return
	}
	ordered := make([]dotChildCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		ordered = append(ordered, candidate)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].isDir != ordered[j].isDir {
			return ordered[i].isDir
		}
		return ordered[i].name < ordered[j].name
	})
	for _, candidate := range ordered {
		ignored := shouldIgnoreDotPath(ignoreRoot, candidate.rel, candidate.name, ignores)
		if ignored {
			*out = append(*out, DotChild{
				Name:    candidate.name,
				RelPath: candidate.rel,
				Path:    filepath.Join(targetRoot, candidate.rel),
				State:   DotStateIgnored,
				IsDir:   candidate.isDir,
				Depth:   depth,
				Ignored: true,
			})
		}
		if candidate.isDir && (!ignored || ignoredDotDirHasIncludedDescendant(ignoreRoot, candidate.rel, ignores)) {
			collectIgnoredDotChildrenAt(entrySourceRoot, targetRoot, roots, ignoreRoot, candidate.rel, ignores, parentState, depth+1, out)
		}
	}
}

func dotChildState(entrySourceRoot, targetRoot, rel string, ignored bool, ignores []string, parentState DotState) DotState {
	if ignored {
		return DotStateIgnored
	}
	if entrySourceRoot == "" || !pathExists(entrySourceRoot) {
		return parentState
	}
	return classifyDotPathState(filepath.Join(entrySourceRoot, rel), filepath.Join(targetRoot, rel), ignores, parentState)
}

func classifyDotPathState(sourcePath, targetPath string, ignores []string, parentState DotState) DotState {
	sourceInfo, sourceErr := os.Lstat(sourcePath)
	sourceExists := sourceErr == nil
	targetInfo, targetErr := os.Lstat(targetPath)
	targetExists := targetErr == nil

	switch {
	case !sourceExists && !targetExists:
		return DotStateNoSource
	case !sourceExists:
		return DotStateLocalOnly
	case !targetExists:
		if parentState == DotStateRepoOnly {
			return DotStateRepoOnly
		}
		return DotStateMissing
	}

	if sameResolvedPath(targetPath, sourcePath) {
		return DotStateSynced
	}
	if sourceInfo.IsDir() && targetInfo.IsDir() && sourceInfo.Mode()&os.ModeSymlink == 0 && targetInfo.Mode()&os.ModeSymlink == 0 {
		return dotLocalKindState(inspectManagedDotDirectory(dots.ResolvedEntry{
			SourcePath: sourcePath,
			TargetPath: targetPath,
			Ignore:     ignores,
		}), parentState)
	}
	if targetInfo.Mode()&os.ModeSymlink == 0 && localFileIsNewer(sourceInfo, targetInfo) {
		return DotStateModified
	}
	if targetInfo.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(targetPath)
		if err != nil {
			return DotStateBroken
		}
		if !filepath.IsAbs(target) {
			target = filepath.Clean(filepath.Join(filepath.Dir(targetPath), target))
		}
		if pathExists(target) {
			return DotStateConflict
		}
		return DotStateBroken
	}
	return DotStateConflict
}

func dotLocalKindState(kind dotLocalKind, parentState DotState) DotState {
	switch kind {
	case dotLocalExpectedLink:
		return DotStateSynced
	case dotLocalMissing:
		if parentState == DotStateRepoOnly {
			return DotStateRepoOnly
		}
		return DotStateMissing
	case dotLocalBrokenLink:
		return DotStateBroken
	case dotLocalModified:
		return DotStateModified
	case dotLocalAllIgnored:
		return DotStateIgnored
	default:
		return DotStateConflict
	}
}

func isManagedDotFile(mode os.FileMode) bool {
	return mode.IsRegular() || mode&os.ModeSymlink != 0
}

// attachLastSyncErrors annotates out-of-sync statuses with the most recent
// recorded failure for their entry so UIs can explain why an entry is broken
// instead of only showing its state. The newest history verdict per entry
// wins: a successful newer operation clears an older failure.
func (a *App) attachLastSyncErrors(ctx context.Context, statuses []DotStatus) {
	if len(statuses) == 0 {
		return
	}
	history, err := a.RecentDotsHistory(ctx, dotsHistoryLimit)
	if err != nil || len(history) == 0 {
		return
	}
	verdict := make(map[string]string)
	note := func(entry, errText string) {
		if entry == "" {
			return
		}
		if _, seen := verdict[entry]; !seen {
			verdict[entry] = errText
		}
	}
	for _, record := range history {
		for _, op := range record.Ops {
			note(op.Entry, op.Error)
		}
		note(record.Entry, record.Error)
	}
	for i := range statuses {
		if !DotStatusNeedsAttention(statuses[i]) {
			continue
		}
		statuses[i].LastError = verdict[statuses[i].Name]
	}
}
