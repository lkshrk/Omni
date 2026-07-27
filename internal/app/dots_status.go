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
	return entryHealth(m, groupMap, variantMap, 0, nil), nil
}

type dotsChildDepthKey struct{}
type dotsExpandedChildrenKey struct{}

// WithShallowDotsChildren — Callers that do not opt in retain the full tree.
func WithShallowDotsChildren(ctx context.Context) context.Context {
	return context.WithValue(ctx, dotsChildDepthKey{}, 1)
}

// WithExpandedDotsChildren — Preserves already-expanded TUI branches while a shallow snapshot is rebuilt.
func WithExpandedDotsChildren(ctx context.Context, paths map[string][]string) context.Context {
	if len(paths) == 0 {
		return ctx
	}
	expanded := make(map[string]map[string]bool, len(paths))
	for name, relPaths := range paths {
		for _, relPath := range relPaths {
			rel := filepath.ToSlash(filepath.Clean(relPath))
			if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
				continue
			}
			if expanded[name] == nil {
				expanded[name] = make(map[string]bool)
			}
			expanded[name][rel] = true
		}
	}
	return context.WithValue(ctx, dotsExpandedChildrenKey{}, expanded)
}

func dotsChildDepth(ctx context.Context) int {
	if ctx == nil {
		return 0
	}
	depth, _ := ctx.Value(dotsChildDepthKey{}).(int)
	return depth
}

func dotsExpandedChildren(ctx context.Context) map[string]map[string]bool {
	if ctx == nil {
		return nil
	}
	expanded, _ := ctx.Value(dotsExpandedChildrenKey{}).(map[string]map[string]bool)
	return expanded
}

func (a *App) QueryDots(opts DotsQueryOptions) ([]DotStatus, error) {
	statuses, err := a.DotsList()
	if err != nil {
		return nil, err
	}
	return filterDotStatuses(statuses, opts)
}

func (a *App) DotsStatus(ctx context.Context) (*DotsStatusResult, error) {
	mgr, groupMap, variantMap, err := a.buildDotsManager()
	if err != nil {
		return nil, err
	}
	childDepth := dotsChildDepth(ctx)
	expandedChildren := dotsExpandedChildren(ctx)
	statuses := entryHealth(mgr, groupMap, variantMap, childDepth, expandedChildren)
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

// DiscoverDotsStatus — Does not mutate config, the repo, or local files.
func (a *App) DiscoverDotsStatus(ctx context.Context) (*DotsStatusResult, error) {
	childDepth := dotsChildDepth(ctx)
	expandedChildren := dotsExpandedChildren(ctx)
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
	allCandidates, discoverErr := a.discoverDotsEntriesIncludingIgnored(repoPath)
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
		mgr, err := dots.NewEngine(stowPath, candidates)
		if err != nil {
			return result, fmt.Errorf("dots discover: resolve candidates: %w", err)
		}
		discovered := entryHealth(mgr, nil, nil, childDepth, expandedChildren)
		for i := range discovered {
			discovered[i].State = discoveredDotState(discovered[i])
			discovered[i].Actions = discoveredDotActions(discovered[i].State)
			discovered[i].Health = healthForDotState(discovered[i].State)
		}
		result.Entries = append(result.Entries, discovered...)
		result.Entries = append(result.Entries, ignoredChildDotStatuses(discovered)...)
	}
	if len(ignoredCandidates) > 0 {
		mgr, err := dots.NewEngine(stowPath, ignoredCandidates)
		if err != nil {
			return result, fmt.Errorf("dots discover: resolve ignored candidates: %w", err)
		}
		ignored := entryHealth(mgr, nil, nil, childDepth, expandedChildren)
		for i := range ignored {
			ignored[i].Actions = []dots.Action{dots.ActionUnignore}
			ignored[i].State = dots.StateIgnored
			ignored[i].Health = healthForDotState(dots.StateIgnored)
		}
		result.Entries = append(result.Entries, ignored...)
	}
	result.DiscoveredCount = countTransientDotCandidates(result.Entries)
	return result, statusErr
}

func countTransientDotCandidates(statuses []DotStatus) int {
	count := 0
	for _, status := range statuses {
		if status.Group != "" || status.State == dots.StateIgnored || status.State == dots.StateInactive || status.State == dots.StateDisabled {
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
			Health:     healthForDotState(dots.StateIgnored),
			State:      dots.StateIgnored,
			Actions:    []dots.Action{dots.ActionIgnore, dots.ActionRemove},
			Group:      status.Group,
			Counts:     DotFileCounts{Ignored: totalIgnored},
			IsDir:      true,
			Children:   tree,
		})
	}
	return ignored
}

// Tracked leaves carry their real state so they render active instead of being dropped or muted.
func collectIgnoredFromChildren(children, ignoredChildren []DotChild) []DotChild {
	seen := make(map[string]bool)
	var result []DotChild
	add := func(child DotChild) {
		rel := filepath.ToSlash(child.RelPath)
		if rel == "" || seen[rel] {
			return
		}
		seen[rel] = true
		result = append(result, child)
	}
	for _, child := range ignoredChildren {
		// The compact ignored scan also carries tracked leaves re-included beneath an ignored directory.
		add(child)
	}
	var walk func(nodes []DotChild, underIgnored bool)
	walk = func(nodes []DotChild, underIgnored bool) {
		for _, child := range nodes {
			switch {
			case child.Ignored:
				add(child)
				walk(child.Children, true)
			case child.matchedIgnored:
				walk(child.Children, true)
			case underIgnored && len(child.Children) == 0:
				add(child)
			default:
				walk(child.Children, underIgnored)
			}
		}
	}
	walk(children, false)
	return result
}

// DotChildDisplayState — The single authority both the app and the TUI consult, so the two layers cannot drift.
func DotChildDisplayState(child DotChild, parentState dots.State) dots.State {
	if child.Ignored {
		return dots.StateIgnored
	}
	if child.State != "" {
		return child.State
	}
	return parentState
}

func DotChildIsTrackedLeaf(child DotChild) bool {
	return !child.Ignored && child.State != "" && child.State != dots.StateIgnored && len(child.Children) == 0
}

func DotChildIsSynthesizedContainer(child DotChild, parentState dots.State) bool {
	return !child.Ignored && DotChildDisplayState(child, parentState) == dots.StateIgnored
}

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
			n, ok := cur.children[part]
			if !ok {
				n = &node{children: make(map[string]*node)}
				cur.children[part] = n
				cur.order = append(cur.order, part)
			}
			if i == len(parts)-1 {
				// Unless this path was already promoted to an intermediate container by a sibling.
				if len(n.children) == 0 {
					n.child = child
					n.child.Depth = i + 1
				}
			} else {
				// Always synthesized, so an ignored dir with re-included children renders as structure rather than one ignored row.
				dirRel := strings.Join(parts[:i+1], "/")
				n.child = DotChild{
					Name:    part,
					RelPath: dirRel,
					Path:    filepath.Join(targetRoot, filepath.FromSlash(dirRel)),
					State:   dots.StateIgnored,
					IsDir:   true,
					Depth:   i + 1,
				}
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
			// Tracked leaves keep real counts so their synced-to-managed ratio renders truthfully.
			if !DotChildIsTrackedLeaf(ch) {
				n := countIgnoredTree(ch)
				ch.FileCount = n
				ch.Counts = DotFileCounts{Ignored: n}
			}
			result = append(result, ch)
		}
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
		if DotChildIsTrackedLeaf(child) {
			return 0
		}
		if child.Counts.Ignored > 0 {
			return child.Counts.Ignored
		}
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

// DotsChildChildren — Used by the TUI's on-demand expansion path.
func (a *App) DotsChildChildren(ctx context.Context, name, relPath string, ancestorIgnored bool) ([]DotChild, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	rel := filepath.Clean(strings.TrimSpace(relPath))
	if rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("dots child path %q must stay within its entry", relPath)
	}
	mgr, _, _, err := a.buildDotsManager()
	if err != nil {
		return nil, err
	}
	found := false
	for _, entry := range mgr.Entries {
		if entry.Name == name {
			found = true
			break
		}
	}
	if !found {
		candidates, err := a.DiscoverUntrackedDotsEntries()
		if err != nil {
			return nil, err
		}
		for _, candidate := range candidates {
			if candidate.Name != name {
				continue
			}
			transient, err := dots.NewEngine(mgr.RepoPath, []config.DotEntry{candidate})
			if err != nil {
				return nil, err
			}
			mgr.Entries = append(mgr.Entries, transient.Entries...)
			break
		}
	}
	for _, entry := range mgr.Entries {
		if entry.Name != name {
			continue
		}
		state, _ := dots.ClassifyEntry(entry)
		contentRoot := dotStatusContentRoot(entry)
		ignoreRoot := dotIgnoreRoot(entry.SourcePath, entry.TargetPath, contentRoot)
		im := compileDotIgnores(entry.Ignore)
		inheritedIM := compileInheritedDotIgnores(im)
		var prefix string
		for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
			prefix = filepath.Join(prefix, part)
			matcher := im
			if ancestorIgnored {
				matcher = inheritedIM
			}
			ancestorIgnored = ancestorIgnored || matcher.IgnoredDotPath(ignoreRoot, prefix, part)
		}
		roots := dotChildRoots(entry.SourcePath, entry.TargetPath, contentRoot)
		for _, root := range roots {
			dir := filepath.Join(root, rel)
			if _, err := os.Stat(dir); err != nil {
				continue
			}
			resolvedDir, err := filepath.EvalSymlinks(dir)
			if err != nil {
				return nil, fmt.Errorf("resolve dots child path %q: %w", relPath, err)
			}
			relToRoot, err := filepath.Rel(root, resolvedDir)
			if err != nil || relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) {
				return nil, fmt.Errorf("dots child path %q resolves outside its entry", relPath)
			}
		}
		depth := strings.Count(filepath.ToSlash(rel), "/") + 2
		children := directDotChildrenAt(entry.SourcePath, entry.TargetPath, roots, ignoreRoot, rel, im, inheritedIM, ancestorIgnored, state, depth, depth, nil)
		if children == nil {
			children = []DotChild{}
		}
		return children, nil
	}
	return nil, fmt.Errorf("dots entry %q not found", name)
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

func normalizeDotState(raw string) (dots.State, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return "", nil
	case "ok", "linked", "healthy", "synced":
		return dots.StateSynced, nil
	case "missing", "unlinked":
		return dots.StateMissing, nil
	case "conflict":
		return dots.StateConflict, nil
	case "modified":
		return dots.StateModified, nil
	case "broken":
		return dots.StateBroken, nil
	case "no-source", "nosource", "source-missing":
		return dots.StateNoSource, nil
	case "local-only", "localonly":
		return dots.StateLocalOnly, nil
	case "repo-only", "repoonly":
		return dots.StateRepoOnly, nil
	case "untracked-linked", "untrackedlinked":
		return dots.StateUntrackedLinked, nil
	case "untracked-conflict", "untrackedconflict":
		return dots.StateUntrackedConflict, nil
	case "ignored":
		return dots.StateIgnored, nil
	case "inactive":
		return dots.StateInactive, nil
	case "disabled":
		return dots.StateDisabled, nil
	case "ambiguous":
		return dots.StateAmbiguous, nil
	default:
		return "", fmt.Errorf("unknown dots state %q", raw)
	}
}

func entryHealth(m *dots.Engine, groupMap map[string]string, variantMap map[string]bool, maxChildDepth int, expandedChildren map[string]map[string]bool) []DotStatus {
	statuses := make([]DotStatus, 0, len(m.Entries))
	for _, e := range m.Entries {
		state, actions := dots.ClassifyEntry(e)
		contentRoot := dotStatusContentRoot(e)
		// Ignored entries render without children, and targets like ~/.cargo hold tens of thousands of files.
		var children, ignoredChildren []DotChild
		var counts DotFileCounts
		if state != dots.StateIgnored {
			ignoreRoot := dotIgnoreRoot(e.SourcePath, e.TargetPath, contentRoot)
			im := compileDotIgnores(e.Ignore)
			children = directDotChildren(e.SourcePath, e.TargetPath, contentRoot, ignoreRoot, im, state, maxChildDepth, expandedChildren[e.Name])
			counts = dotChildrenFileCounts(children)
			if !dotStatusIsDir(e, contentRoot) {
				counts = dotFileCountsUnion(e.SourcePath, e.TargetPath, contentRoot, "", ignoreRoot, im, state)
			}
			if maxChildDepth > 0 {
				ignoredChildren = ignoredDotChildren(e.SourcePath, e.TargetPath, contentRoot, ignoreRoot, im, state)
			}
		}
		fileCount := counts.Managed()
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

func discoveredDotState(status DotStatus) dots.State {
	sourceExists := dots.PathExists(status.SourcePath)
	targetExists := dots.PathExists(status.TargetPath)
	switch {
	case sourceExists && targetExists:
		return dots.StateUntrackedConflict
	case sourceExists:
		return dots.StateRepoOnly
	case targetExists:
		return dots.StateLocalOnly
	default:
		return dots.StateNoSource
	}
}

func discoveredDotActions(state dots.State) []dots.Action {
	switch state {
	case dots.StateUntrackedConflict:
		return []dots.Action{dots.ActionUseRepo, dots.ActionUseLocal, dots.ActionIgnore}
	case dots.StateLocalOnly:
		return []dots.Action{dots.ActionSync, dots.ActionRemove, dots.ActionIgnore}
	case dots.StateRepoOnly, dots.StateUntrackedLinked:
		return []dots.Action{dots.ActionSync, dots.ActionIgnore}
	case dots.StateIgnored:
		return []dots.Action{dots.ActionUnignore}
	default:
		return []dots.Action{dots.ActionIgnore}
	}
}

func configPathForTarget(target string) string {
	return normalisePath(target)
}

func dotStatusContentRoot(e dots.ResolvedEntry) string {
	if dots.PathExists(e.SourcePath) {
		return e.SourcePath
	}
	local := dots.InspectDotLocal(e)
	if local.Kind == dots.LocalWrongLink {
		if source, err := dots.LocalDotCopySource(e.TargetPath); err == nil {
			return source
		}
	}
	if local.Kind == dots.LocalContent {
		return e.TargetPath
	}
	return e.SourcePath
}

func dotIgnoreRoot(sourceRoot, targetRoot, contentRoot string) string {
	for _, root := range []string{sourceRoot, contentRoot, targetRoot} {
		if root != "" && dots.PathExists(root) {
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

func dotFileCountsUnion(entrySourceRoot, targetRoot, contentRoot, relRoot, ignoreRoot string, im *dots.IgnoreMatcher, parentState dots.State) DotFileCounts {
	roots := dotExistingRoots(entrySourceRoot, targetRoot, contentRoot)
	if len(roots) == 0 {
		return DotFileCounts{}
	}
	tracked := make(map[string]bool)
	ignored := make(map[string]bool)
	for _, root := range roots {
		collectDotFileCountRelsFromRoot(root, relRoot, ignoreRoot, im, tracked, ignored)
	}
	var counts DotFileCounts
	for rel := range tracked {
		if dotFileCountSynced(dotChildState(entrySourceRoot, targetRoot, filepath.FromSlash(rel), false, im.Raw(), parentState)) {
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

func collectDotFileCountRelsFromRoot(root, relRoot, ignoreRoot string, im *dots.IgnoreMatcher, tracked, ignored map[string]bool) {
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
		if !dots.IsManagedDotFile(info.Mode()) {
			return
		}
		if im.IgnoredDotPath(ignoreRoot, rel, filepath.Base(path)) {
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
		if relErr != nil || walkRel == "." {
			return nil
		}
		if im.IgnoredDotPath(ignoreRoot, walkRel, d.Name()) {
			if d.IsDir() {
				if im.DotDirHasIncludedDescendant(ignoreRoot, walkRel) {
					return nil
				}
				collectIgnoredDotFileCountRels(root, path, ignored)
				return filepath.SkipDir
			}
			if dotDirEntryManaged(d) {
				ignored[filepath.ToSlash(walkRel)] = true
			}
			return nil
		}
		if !d.IsDir() && dotDirEntryManaged(d) {
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
		if !dotDirEntryManaged(d) {
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

func dotFileCountSynced(state dots.State) bool {
	return state == dots.StateSynced
}

func dotExistingRoots(entrySourceRoot, targetRoot, contentRoot string) []string {
	seen := make(map[string]bool)
	roots := make([]string, 0, 3)
	for _, root := range []string{entrySourceRoot, targetRoot, contentRoot} {
		if root == "" || !dots.PathExists(root) {
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

// Uses the dirent's d_type when available to avoid a per-file lstat on large walks.
func dotDirEntryManaged(d os.DirEntry) bool {
	switch d.Type() & os.ModeType {
	case 0, os.ModeSymlink:
		return true
	case os.ModeDir:
		return false
	}
	info, err := d.Info()
	return err == nil && dots.IsManagedDotFile(info.Mode())
}

func dotStatusIsDir(e dots.ResolvedEntry, contentRoot string) bool {
	for _, path := range []string{contentRoot, e.SourcePath, e.TargetPath} {
		if path != "" && dotPathIsDir(path) {
			return true
		}
	}
	return false
}

// Invalid per-entry patterns yield an empty matcher, preserving the historical ignore-nothing behavior.
func compileDotIgnores(perEntry []string) *dots.IgnoreMatcher {
	im, err := dots.CompileIgnores(dots.CombinedIgnores(perEntry))
	if err != nil {
		im, _ = dots.CompileIgnores(nil)
	}
	return im
}

// Descendants of an ignored directory default to ignored unless re-included.
func compileInheritedDotIgnores(im *dots.IgnoreMatcher) *dots.IgnoreMatcher {
	raw := im.Raw()
	inherited := make([]string, 0, len(raw)+1)
	inherited = append(inherited, "*")
	inherited = append(inherited, raw...)
	inheritedIM, err := dots.CompileIgnores(inherited)
	if err != nil {
		return im
	}
	return inheritedIM
}

func directDotChildren(entrySourceRoot, targetRoot, contentRoot, ignoreRoot string, im *dots.IgnoreMatcher, parentState dots.State, maxDepth int, expanded map[string]bool) []DotChild {
	roots := dotChildRoots(entrySourceRoot, targetRoot, contentRoot)
	if len(roots) == 0 {
		return nil
	}
	return directDotChildrenAt(entrySourceRoot, targetRoot, roots, ignoreRoot, "", im, compileInheritedDotIgnores(im), false, parentState, 1, maxDepth, expanded)
}

type dotChildCandidate struct {
	name  string
	rel   string
	isDir bool
}

func directDotChildrenAt(entrySourceRoot, targetRoot string, roots []string, ignoreRoot, relRoot string, im, inheritedIM *dots.IgnoreMatcher, ancestorIgnored bool, parentState dots.State, depth, maxDepth int, expanded map[string]bool) []DotChild {
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
			if !entry.IsDir() && !dotDirEntryManaged(entry) {
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
		matcher := im
		if ancestorIgnored {
			matcher = inheritedIM
		}
		matchedIgnored := matcher.IgnoredDotPath(ignoreRoot, candidate.rel, candidate.name)
		ignored := matchedIgnored && !(candidate.isDir && matcher.DotDirHasIncludedDescendant(ignoreRoot, candidate.rel))
		state := dotChildState(entrySourceRoot, targetRoot, candidate.rel, ignored, im.Raw(), parentState)
		child := DotChild{
			Name:           candidate.name,
			RelPath:        candidate.rel,
			Path:           filepath.Join(targetRoot, candidate.rel),
			State:          state,
			IsDir:          candidate.isDir,
			Depth:          depth,
			Ignored:        ignored,
			matchedIgnored: matchedIgnored,
		}
		if candidate.isDir {
			if maxDepth == 0 || depth < maxDepth || expanded[filepath.ToSlash(candidate.rel)] {
				child.Children = directDotChildrenAt(entrySourceRoot, targetRoot, roots, ignoreRoot, candidate.rel, im, inheritedIM, ancestorIgnored || matchedIgnored, parentState, depth+1, maxDepth, expanded)
				if child.Children == nil {
					child.Children = []DotChild{}
				}
				child.Counts = dotChildrenFileCounts(child.Children)
			} else {
				child.Counts = dotFileCountsUnion(entrySourceRoot, targetRoot, "", candidate.rel, ignoreRoot, im, parentState)
			}
		} else if ignored {
			child.Counts.Ignored = 1
		} else if dotFileCountSynced(state) {
			child.Counts.Synced = 1
		} else {
			child.Counts.OutOfSync = 1
		}
		if ignored {
			child.FileCount = 0
		} else if candidate.isDir {
			child.FileCount = child.Counts.Managed()
		} else {
			child.FileCount = 1
		}
		children = append(children, child)
	}
	return children
}

func ignoredDotChildren(entrySourceRoot, targetRoot, contentRoot, ignoreRoot string, im *dots.IgnoreMatcher, parentState dots.State) []DotChild {
	roots := dotChildRoots(entrySourceRoot, targetRoot, contentRoot)
	if len(roots) == 0 {
		return nil
	}
	var children []DotChild
	collectIgnoredDotChildrenAt(entrySourceRoot, targetRoot, roots, ignoreRoot, "", im, compileInheritedDotIgnores(im), false, parentState, 1, &children)
	return children
}

func collectIgnoredDotChildrenAt(entrySourceRoot, targetRoot string, roots []string, ignoreRoot, relRoot string, im, inheritedIM *dots.IgnoreMatcher, ancestorIgnored bool, parentState dots.State, depth int, out *[]DotChild) {
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
			if !entry.IsDir() && !dotDirEntryManaged(entry) {
				continue
			}
			candidate := candidates[rel]
			candidate.name = entry.Name()
			candidate.rel = rel
			candidate.isDir = candidate.isDir || entry.IsDir()
			candidates[rel] = candidate
		}
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
		ignored := im.IgnoredDotPath(ignoreRoot, candidate.rel, candidate.name)
		if ancestorIgnored {
			ignored = inheritedIM.IgnoredDotPath(ignoreRoot, candidate.rel, candidate.name)
		}
		if ignored {
			child := DotChild{
				Name:    candidate.name,
				RelPath: candidate.rel,
				Path:    filepath.Join(targetRoot, candidate.rel),
				State:   dots.StateIgnored,
				IsDir:   candidate.isDir,
				Depth:   depth,
				Ignored: true,
			}
			if candidate.isDir {
				child.Counts = dotFileCountsUnion(entrySourceRoot, targetRoot, "", candidate.rel, ignoreRoot, im, parentState)
				child.FileCount = child.Counts.Ignored
			}
			*out = append(*out, child)
		} else if ancestorIgnored && !candidate.isDir {
			state := dotChildState(entrySourceRoot, targetRoot, candidate.rel, false, im.Raw(), parentState)
			child := DotChild{
				Name:      candidate.name,
				RelPath:   candidate.rel,
				Path:      filepath.Join(targetRoot, candidate.rel),
				State:     state,
				Depth:     depth,
				FileCount: 1,
			}
			if dotFileCountSynced(state) {
				child.Counts.Synced = 1
			} else {
				child.Counts.OutOfSync = 1
			}
			*out = append(*out, child)
		}
		if candidate.isDir && (!ignored || im.DotDirHasIncludedDescendant(ignoreRoot, candidate.rel)) {
			collectIgnoredDotChildrenAt(entrySourceRoot, targetRoot, roots, ignoreRoot, candidate.rel, im, inheritedIM, ancestorIgnored || ignored, parentState, depth+1, out)
		}
	}
}

func dotChildrenFileCounts(children []DotChild) (counts DotFileCounts) {
	for _, child := range children {
		counts.Synced += child.Counts.Synced
		counts.OutOfSync += child.Counts.OutOfSync
		counts.Ignored += child.Counts.Ignored
	}
	return counts
}

func dotChildState(entrySourceRoot, targetRoot, rel string, ignored bool, ignores []string, parentState dots.State) dots.State {
	if ignored {
		return dots.StateIgnored
	}
	if entrySourceRoot == "" || !dots.PathExists(entrySourceRoot) {
		return parentState
	}
	return classifyDotPathState(filepath.Join(entrySourceRoot, rel), filepath.Join(targetRoot, rel), ignores, parentState)
}

func classifyDotPathState(sourcePath, targetPath string, ignores []string, parentState dots.State) dots.State {
	sourceInfo, sourceErr := os.Lstat(sourcePath)
	sourceExists := sourceErr == nil
	targetInfo, targetErr := os.Lstat(targetPath)
	targetExists := targetErr == nil

	switch {
	case !sourceExists && !targetExists:
		return dots.StateNoSource
	case !sourceExists:
		return dots.StateLocalOnly
	case !targetExists:
		if parentState == dots.StateRepoOnly {
			return dots.StateRepoOnly
		}
		return dots.StateMissing
	}

	if dots.SameResolvedPath(targetPath, sourcePath) {
		return dots.StateSynced
	}
	if sourceInfo.IsDir() && targetInfo.IsDir() && sourceInfo.Mode()&os.ModeSymlink == 0 && targetInfo.Mode()&os.ModeSymlink == 0 {
		return dotLocalKindState(dots.InspectManagedDotDirectory(dots.ResolvedEntry{
			SourcePath: sourcePath,
			TargetPath: targetPath,
			Ignore:     ignores,
		}), parentState)
	}
	if targetInfo.Mode()&os.ModeSymlink == 0 && dots.LocalFileIsNewer(sourceInfo, targetInfo) {
		return dots.StateModified
	}
	if targetInfo.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(targetPath)
		if err != nil {
			return dots.StateBroken
		}
		if !filepath.IsAbs(target) {
			target = filepath.Clean(filepath.Join(filepath.Dir(targetPath), target))
		}
		if dots.PathExists(target) {
			return dots.StateConflict
		}
		return dots.StateBroken
	}
	return dots.StateConflict
}

func dotLocalKindState(kind dots.LocalKind, parentState dots.State) dots.State {
	switch kind {
	case dots.LocalExpectedLink:
		return dots.StateSynced
	case dots.LocalMissing:
		if parentState == dots.StateRepoOnly {
			return dots.StateRepoOnly
		}
		return dots.StateMissing
	case dots.LocalBrokenLink:
		return dots.StateBroken
	case dots.LocalModified:
		return dots.StateModified
	case dots.LocalAllIgnored:
		return dots.StateIgnored
	default:
		return dots.StateConflict
	}
}

// The newest history verdict per entry wins: a successful newer operation clears an older failure.
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
