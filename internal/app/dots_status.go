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
		mgr, err := dots.NewEngine(stowPath, candidates)
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
		mgr, err := dots.NewEngine(stowPath, ignoredCandidates)
		if err != nil {
			return result, fmt.Errorf("dots discover: resolve ignored candidates: %w", err)
		}
		ignored := entryHealth(mgr, nil, nil)
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

// collectIgnoredFromChildren gathers the entries that belong in the synthesized
// Ignored-section tree: every ignored node, plus any tracked (re-included) leaf
// that lives inside an ignored subtree. The tracked leaves are carried with
// their real state so they render active instead of being dropped or muted.
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
		if child.Ignored {
			add(child)
		}
	}
	var walk func(nodes []DotChild, underIgnored bool)
	walk = func(nodes []DotChild, underIgnored bool) {
		for _, child := range nodes {
			switch {
			case child.Ignored:
				add(child)
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

// DotChildDisplayState resolves the state a child row should present: an ignored
// child reads as ignored, an explicit child state wins, otherwise it inherits
// the parent entry's state. This is the single authority both the app and the
// TUI consult so the two layers cannot drift.
func DotChildDisplayState(child DotChild, parentState dots.State) dots.State {
	if child.Ignored {
		return dots.StateIgnored
	}
	if child.State != "" {
		return child.State
	}
	return parentState
}

// DotChildIsTrackedLeaf reports whether a child is a genuinely tracked leaf
// (a managed file/dir with a real, non-ignored state) rather than an ignored
// entry or a synthesized structural container.
func DotChildIsTrackedLeaf(child DotChild) bool {
	return !child.Ignored && child.State != "" && child.State != dots.StateIgnored && len(child.Children) == 0
}

// DotChildIsSynthesizedContainer reports whether a child row is a structural
// directory synthesized for the Ignored-section tree (no real state of its own)
// rather than an explicitly ignored entry or a tracked, re-included leaf.
func DotChildIsSynthesizedContainer(child DotChild, parentState dots.State) bool {
	return !child.Ignored && DotChildDisplayState(child, parentState) == dots.StateIgnored
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
			n, ok := cur.children[part]
			if !ok {
				n = &node{children: make(map[string]*node)}
				cur.children[part] = n
				cur.order = append(cur.order, part)
			}
			if i == len(parts)-1 {
				// Leaf — use the original child data, unless this path was
				// already promoted to an intermediate container by a sibling.
				if len(n.children) == 0 {
					n.child = child
					n.child.Depth = i + 1
				}
			} else {
				// Intermediate directory — always a synthesized container (not
				// explicitly ignored), overwriting any earlier leaf assignment
				// for this path so an ignored dir with re-included children
				// renders as structure rather than a single ignored row.
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
			// Tracked (re-included) leaves keep their real counts so their
			// synced/managed ratio renders truthfully; everything else reports
			// the ignored-leaf count.
			if !DotChildIsTrackedLeaf(ch) {
				n := countIgnoredTree(ch)
				ch.FileCount = n
				ch.Counts = DotFileCounts{Ignored: n}
			}
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
		if DotChildIsTrackedLeaf(child) {
			return 0
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

func entryHealth(m *dots.Engine, groupMap map[string]string, variantMap map[string]bool) []DotStatus {
	statuses := make([]DotStatus, 0, len(m.Entries))
	for _, e := range m.Entries {
		state, actions := dots.ClassifyEntry(e)
		contentRoot := dotStatusContentRoot(e)
		ignoreRoot := dotIgnoreRoot(e.SourcePath, e.TargetPath, contentRoot)
		ignores := dots.CombinedIgnores(e.Ignore)
		children := directDotChildren(e.SourcePath, e.TargetPath, contentRoot, ignoreRoot, ignores, state)
		counts := dotChildrenFileCounts(children)
		if !dotStatusIsDir(e, contentRoot) {
			counts = dotFileCountsUnion(e.SourcePath, e.TargetPath, contentRoot, ignoreRoot, ignores, state)
		}
		fileCount := counts.Managed()
		if state == dots.StateIgnored {
			counts = DotFileCounts{}
			fileCount = 0
			children = nil
		}
		statuses = append(statuses, DotStatus{
			Name:       e.Name,
			Package:    e.Package,
			Variant:    variantMap[e.Name],
			SourcePath: e.SourcePath,
			TargetPath: e.TargetPath,
			ConfigPath: configPathForTarget(e.TargetPath),
			Health:     healthForDotState(state),
			State:      state,
			Actions:    actions,
			Group:      groupMap[e.Name],
			FileCount:  fileCount,
			Counts:     counts,
			IsDir:      dotStatusIsDir(e, contentRoot),
			Children:   children,
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

func dotFileCountsUnion(entrySourceRoot, targetRoot, contentRoot, ignoreRoot string, ignores []string, parentState dots.State) DotFileCounts {
	roots := dotExistingRoots(entrySourceRoot, targetRoot, contentRoot)
	if len(roots) == 0 {
		return DotFileCounts{}
	}
	tracked := false
	ignored := false
	for _, root := range roots {
		info, err := os.Lstat(root)
		if err != nil || info.IsDir() || !dots.IsManagedDotFile(info.Mode()) {
			continue
		}
		if dots.ShouldIgnoreDotPath(ignoreRoot, ".", filepath.Base(root), ignores) {
			ignored = true
		} else {
			tracked = true
		}
	}
	if tracked {
		if dotFileCountSynced(parentState) {
			return DotFileCounts{Synced: 1}
		}
		return DotFileCounts{OutOfSync: 1}
	}
	if ignored {
		return DotFileCounts{Ignored: 1}
	}
	return DotFileCounts{}
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

func dotStatusIsDir(e dots.ResolvedEntry, contentRoot string) bool {
	for _, path := range []string{contentRoot, e.SourcePath, e.TargetPath} {
		if path != "" && dotPathIsDir(path) {
			return true
		}
	}
	return false
}

func directDotChildren(entrySourceRoot, targetRoot, contentRoot, ignoreRoot string, ignores []string, parentState dots.State) []DotChild {
	roots := dotChildRoots(entrySourceRoot, targetRoot, contentRoot)
	if len(roots) == 0 {
		return nil
	}
	inheritedIgnores := make([]string, 0, len(ignores)+1)
	inheritedIgnores = append(inheritedIgnores, "*")
	inheritedIgnores = append(inheritedIgnores, ignores...)
	return directDotChildrenAt(entrySourceRoot, targetRoot, roots, ignoreRoot, "", ignores, inheritedIgnores, false, parentState, 1)
}

type dotChildCandidate struct {
	name  string
	rel   string
	isDir bool
}

func directDotChildrenAt(entrySourceRoot, targetRoot string, roots []string, ignoreRoot, relRoot string, ignores, inheritedIgnores []string, ancestorIgnored bool, parentState dots.State, depth int) []DotChild {
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
			if infoErr != nil || (!entry.IsDir() && !dots.IsManagedDotFile(info.Mode())) {
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
		ignored := dots.ShouldIgnoreDotPath(ignoreRoot, candidate.rel, candidate.name, ignores)
		if ancestorIgnored {
			ignored = dots.ShouldIgnoreDotPath(ignoreRoot, candidate.rel, candidate.name, inheritedIgnores)
		}
		state := dotChildState(entrySourceRoot, targetRoot, candidate.rel, ignored, ignores, parentState)
		child := DotChild{
			Name:    candidate.name,
			RelPath: candidate.rel,
			Path:    filepath.Join(targetRoot, candidate.rel),
			State:   state,
			IsDir:   candidate.isDir,
			Depth:   depth,
			Ignored: ignored,
		}
		if candidate.isDir {
			child.Children = directDotChildrenAt(entrySourceRoot, targetRoot, roots, ignoreRoot, candidate.rel, ignores, inheritedIgnores, ignored, parentState, depth+1)
			child.Counts = dotChildrenFileCounts(child.Children)
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
