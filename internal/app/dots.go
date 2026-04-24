package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/dots"
	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

// DotsAddOptions controls the behaviour of DotsAdd.
type DotsAddOptions struct {
	// Name overrides the name inferred from the path.
	Name string
	// Group is the base name of the group file to write the entry to.
	// Defaults to "base" when empty.
	Group string
	// Adopt moves the existing file or directory into the dots repo before linking.
	Adopt bool
	// Ignore is a list of per-entry ignore patterns.
	Ignore []string
}

// DotsDeleteOptions controls the behaviour of DotsDeleteWithOptions.
type DotsDeleteOptions struct {
	// KeepLocal preserves a real local copy before deleting the repo package.
	KeepLocal bool
}

// DotHealth summarises the symlink health of a dots entry.
type DotHealth string

const (
	HealthOK       DotHealth = "ok"        // all symlinks correct
	HealthMissing  DotHealth = "missing"   // some/all links not yet created
	HealthConflict DotHealth = "conflict"  // real files at one or more target paths
	HealthNoSource DotHealth = "no-source" // source does not exist in repo
)

// DotStatus describes the current state of a single dots entry.
type DotStatus struct {
	Name       string      `json:"name"`
	SourcePath string      `json:"source_path"`
	TargetPath string      `json:"target_path"`
	ConfigPath string      `json:"path,omitempty"`
	Health     DotHealth   `json:"health"`
	State      DotState    `json:"state"`
	Actions    []DotAction `json:"actions,omitempty"`
	Group      string      `json:"group,omitempty"` // base name of the group file (e.g. "base", "work")
	FileCount  int         `json:"file_count,omitempty"`
	Children   []DotChild  `json:"children,omitempty"`
}

type DotChild struct {
	Name      string `json:"name"`
	RelPath   string `json:"rel_path"`
	Path      string `json:"path"`
	IsDir     bool   `json:"is_dir"`
	Depth     int    `json:"depth,omitempty"`
	Ignored   bool   `json:"ignored,omitempty"`
	FileCount int    `json:"file_count,omitempty"`
}

// DotsStatusResult bundles symlink health with the git status string.
type DotsStatusResult struct {
	Entries         []DotStatus `json:"entries"`
	GitStatus       string      `json:"git_status"` // output of "git status --short" in the dots repo
	DiscoveredCount int         `json:"discovered_count,omitempty"`
}

type DotsQueryOptions struct {
	Name  string
	State string
}

type DotsResolveStrategy string

const (
	DotResolveUseRepo  DotsResolveStrategy = "use-repo"
	DotResolveUseLocal DotsResolveStrategy = "use-local"
)

const dotChildrenMaxDepth = 4

const dotsContentDirName = "dotfiles"

// ─── public API ───────────────────────────────────────────────────────────────

// DotsConfigured reports whether dots_repo is set in settings.json.
func (a *App) DotsConfigured() bool {
	return a.dotsRepoPath() != ""
}

// DotsSync creates or repairs all symlinks for all dots entries across active
// group files. When the current hostname maps to a profile, only that profile's
// groups are synced. Falls back to all groups when no profile is configured.
// All entries are managed via GNU Stow.
func (a *App) DotsSync(opts dots.SyncOptions) ([]dots.Op, error) {
	return a.DotsSyncContext(context.Background(), opts)
}

// DotsSyncContext creates or repairs all symlinks like DotsSync, honoring ctx
// for provider/stow commands.
func (a *App) DotsSyncContext(ctx context.Context, opts dots.SyncOptions) ([]dots.Op, error) {
	repoPath, err := resolveRepoPath(a.dotsRepoPath())
	if err != nil {
		return nil, err
	}
	if err := a.requireSafeTestDotsMutation(repoPath, nil); err != nil {
		return nil, err
	}
	stowPath := dotsContentPath(repoPath)
	if !opts.DryRun {
		var err error
		stowPath, err = ensureDotsContentPath(repoPath)
		if err != nil {
			return nil, fmt.Errorf("dots sync: content dir: %w", err)
		}
	}
	rootCfg, err := a.loadConfig()
	if err != nil {
		return nil, fmt.Errorf("dots sync: load config: %w", err)
	}
	groups := rootCfg.Groups
	if profileName, ok := rootCfg.ActiveProfile(currentHostname()); ok {
		if effective, _, e := effectiveProfileGroups(rootCfg, groups, profileName); e == nil {
			groups = effective
		}
	}
	entries := collectDots(groups)
	entries = filterActiveDotEntries(entries)
	if len(entries) == 0 {
		return nil, nil
	}
	if err := a.requireSafeTestDotsMutation(repoPath, entries); err != nil {
		return nil, err
	}

	if err := a.requireStow(ctx); err != nil {
		return nil, err
	}
	mgr, err := dots.New(stowPath, entries)
	if err != nil {
		return nil, fmt.Errorf("dots sync: resolve entries: %w", err)
	}
	var ops []dots.Op
	var failures []dotSyncFailure
	for _, entry := range mgr.Entries {
		entryOps, syncErr := syncResolvedDotEntry(ctx, stowPath, entry, opts, false)
		ops = append(ops, entryOps...)
		if syncErr != nil {
			failures = append(failures, dotSyncFailure{entry: entry.Name, err: syncErr})
		}
	}
	if len(failures) > 0 {
		return ops, dotSyncFailures(failures)
	}
	return ops, nil
}

type dotSyncFailure struct {
	entry string
	err   error
}

type dotSyncFailures []dotSyncFailure

func (f dotSyncFailures) Error() string {
	parts := make([]string, 0, len(f))
	for _, failure := range f {
		parts = append(parts, fmt.Sprintf("%s: %v", failure.entry, failure.err))
	}
	return "dots sync: " + strings.Join(parts, "; ")
}

// DotsSyncEntry syncs one configured dots entry when the classifier reports a
// single safe action. Choice-based conflicts are reported but not resolved.
func (a *App) DotsSyncEntry(ctx context.Context, name string, opts dots.SyncOptions) ([]dots.Op, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("dots sync: entry name is required")
	}
	repoPath, err := resolveRepoPath(a.dotsRepoPath())
	if err != nil {
		return nil, err
	}
	if err := a.requireSafeTestDotsMutation(repoPath, nil); err != nil {
		return nil, err
	}
	stowPath := dotsContentPath(repoPath)
	if !opts.DryRun {
		var err error
		stowPath, err = ensureDotsContentPath(repoPath)
		if err != nil {
			return nil, fmt.Errorf("dots sync %q: content dir: %w", name, err)
		}
	}
	rootCfg, err := a.loadConfig()
	if err != nil {
		return nil, fmt.Errorf("dots sync: load config: %w", err)
	}
	groups := rootCfg.Groups
	if profileName, ok := rootCfg.ActiveProfile(currentHostname()); ok {
		if effective, _, e := effectiveProfileGroups(rootCfg, groups, profileName); e == nil {
			groups = effective
		}
	}
	entries := collectDots(groups)
	entries = filterActiveDotEntries(entries)
	if err := a.requireSafeTestDotsMutation(repoPath, entries); err != nil {
		return nil, err
	}
	mgr, err := dots.New(stowPath, entries)
	if err != nil {
		return nil, fmt.Errorf("dots sync: resolve entries: %w", err)
	}
	for _, entry := range mgr.Entries {
		if entry.Name != name {
			continue
		}
		if err := a.requireStow(ctx); err != nil {
			return nil, err
		}
		ops, syncErr := syncResolvedDotEntry(ctx, stowPath, entry, opts, true)
		if syncErr != nil {
			return ops, fmt.Errorf("dots sync %q: %w", name, syncErr)
		}
		return ops, nil
	}
	return nil, fmt.Errorf("dots entry %q not found", name)
}

// DotsAdd moves the file/dir at path into the dots repo and links it back via
// stow. A backup is made under ~/dotfiles.bkp before any mutation. path must
// exist on disk.
func (a *App) DotsAdd(ctx context.Context, path string, opts DotsAddOptions) ([]dots.Op, error) {
	rootCfg, err := a.loadConfig()
	if err != nil {
		return nil, fmt.Errorf("dots add: load config: %w", err)
	}
	rawRepo := a.effectiveSettings(rootCfg).DotsRepo
	gitCfg := rootCfg.Settings.DotsGit

	repoPath, err := resolveRepoPath(rawRepo)
	if err != nil {
		return nil, err
	}
	if err := a.requireSafeTestDotsMutation(repoPath, nil); err != nil {
		return nil, err
	}

	abs, err := expandAndStat(path)
	if err != nil {
		return nil, fmt.Errorf("dots add: %w", err)
	}

	name := opts.Name
	if name == "" {
		name = inferName(abs)
	}
	if err := dots.ValidateEntryName(name); err != nil {
		return nil, fmt.Errorf("dots add: %w", err)
	}
	entry := dotEntryWithDefaults(config.DotEntry{Name: name, Path: normalisePath(abs), Ignore: opts.Ignore})
	if err := a.requireSafeTestDotsMutation(repoPath, []config.DotEntry{entry}); err != nil {
		return nil, err
	}
	if _, exists := findDotEntryInConfig(rootCfg, name); exists {
		return nil, fmt.Errorf("dots add: %q is already configured", name)
	}
	stowPath, err := ensureDotsContentPath(repoPath)
	if err != nil {
		return nil, fmt.Errorf("dots add: content dir: %w", err)
	}
	if err := a.requireStow(ctx); err != nil {
		return nil, err
	}

	// Where this entry lives in the stow package tree.
	pkgDst, err := stowPackagePath(stowPath, name, abs)
	if err != nil {
		return nil, fmt.Errorf("dots add: %w", err)
	}
	if _, statErr := os.Lstat(pkgDst); statErr == nil {
		return nil, fmt.Errorf("dots add: %q is already tracked (repo path: %s)", name, pkgDst)
	}
	pkgRoot := filepath.Join(stowPath, name)
	pkgRootExisted := true
	if _, statErr := os.Lstat(pkgRoot); os.IsNotExist(statErr) {
		pkgRootExisted = false
	} else if statErr != nil {
		return nil, fmt.Errorf("dots add: stat package root: %w", statErr)
	}
	cleanupPackage := func() error {
		if !pkgRootExisted {
			return os.RemoveAll(pkgRoot)
		}
		return os.RemoveAll(pkgDst)
	}
	if !opts.Adopt {
		return nil, fmt.Errorf("dots add: %q exists locally; pass --adopt to move it into dots management", path)
	}

	backupPath, err := dots.BackupLocalPath(abs)
	if err != nil {
		return nil, fmt.Errorf("dots add: backup: %w", err)
	}

	// Copy filtered content into the repo package tree, remove the local
	// original, then stow links it back. The backup remains a full safety copy.
	if err := os.MkdirAll(filepath.Dir(pkgDst), 0o755); err != nil {
		return nil, fmt.Errorf("dots add: create package dir: %w", err)
	}
	if err := copyDotPath(abs, pkgDst, combinedDotIgnores(entry.Ignore)); err != nil {
		if cleanupErr := cleanupPackage(); cleanupErr != nil {
			return nil, fmt.Errorf("dots add: copy to repo: %w (cleanup failed: %v)", err, cleanupErr)
		}
		return nil, fmt.Errorf("dots add: copy to repo: %w", err)
	}
	if err := os.RemoveAll(abs); err != nil {
		if cleanupErr := cleanupPackage(); cleanupErr != nil {
			return nil, fmt.Errorf("dots add: remove local target: %w (cleanup failed: %v)", err, cleanupErr)
		}
		return nil, fmt.Errorf("dots add: remove local target: %w", err)
	}
	if err := dots.Restow(ctx, executor.New(), stowPath, []string{name}, false); err != nil {
		if rollbackErr := rollbackDotsAdd(abs, pkgDst, backupPath); rollbackErr != nil {
			return nil, fmt.Errorf("dots add: stow: %w (rollback failed: %v)", err, rollbackErr)
		}
		return nil, fmt.Errorf("dots add: stow: %w", err)
	}

	// Record entry in config using normalised ~-form path.
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		gc := ensureGroupInConfig(cfg, opts.Group)
		gc.Dots = append(gc.Dots, entry)
		return nil
	}); err != nil {
		if rollbackErr := rollbackDotsAdd(abs, pkgDst, backupPath); rollbackErr != nil {
			return nil, fmt.Errorf("dots add: save config: %w (rollback failed: %v)", err, rollbackErr)
		}
		return nil, fmt.Errorf("dots add: save config: %w", err)
	}

	if g := newGitForRepo(repoPath, executor.New()); g.IsRepo() {
		msg := "dots: add " + name
		if gitCfg.AutoPush {
			if err := g.Push(ctx, msg); err != nil {
				return nil, fmt.Errorf("dots add: auto-push: %w", err)
			}
		} else if gitCfg.AutoCommit {
			if err := g.CommitAll(ctx, msg); err != nil {
				return nil, fmt.Errorf("dots add: auto-commit: %w", err)
			}
		}
	}

	return []dots.Op{lstatOp(name, abs, stowPath, false)}, nil
}

// DotsDelete deletes the dots entry named name from all group files. Managed
// symlinks are replaced with regular local files copied back from the repo.
func (a *App) DotsDelete(ctx context.Context, name string) error {
	return a.DotsDeleteWithOptions(ctx, name, DotsDeleteOptions{KeepLocal: true})
}

// DotsDeleteWithOptions deletes the dots entry named name from all group files
// and always removes its package from the dots repo. When KeepLocal is true,
// managed local symlinks are first replaced with real local copies.
func (a *App) DotsDeleteWithOptions(ctx context.Context, name string, opts DotsDeleteOptions) error {
	if err := dots.ValidateEntryName(name); err != nil {
		return fmt.Errorf("dots delete: %w", err)
	}
	var (
		repoPath       string
		stowPath       string
		gitCfg         config.DotsGitConfig
		found          bool
		deleteDot      *config.DotEntry
		deletedEntries []deletedDotEntry
	)
	err := a.withConfig(func(rootCfg *config.RootConfig) error {
		rawRepo := a.effectiveSettings(rootCfg).DotsRepo
		gitCfg = rootCfg.Settings.DotsGit

		var err error
		repoPath, err = resolveRepoPath(rawRepo)
		if err != nil {
			return err
		}
		if err := a.requireSafeTestDotsMutation(repoPath, nil); err != nil {
			return err
		}
		stowPath = dotsContentPath(repoPath)

		for _, g := range rootCfg.Groups {
			for i := 0; i < len(g.Dots); i++ {
				d := g.Dots[i]
				if d.Name != name {
					continue
				}
				found = true
				if deleteDot == nil {
					copyDot := d
					deleteDot = &copyDot
				}
				deletedEntries = append(deletedEntries, deletedDotEntry{group: g.Name, dot: d})
				g.Dots = append(g.Dots[:i], g.Dots[i+1:]...)
				i--
			}
		}
		if !found {
			return errSkipSave
		}
		if err := a.requireSafeTestDotsMutation(repoPath, []config.DotEntry{*deleteDot}); err != nil {
			return err
		}
		stowPath, err = existingDotsContentPath(repoPath)
		if err != nil {
			return fmt.Errorf("dots delete %q: content dir: %w", name, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("dots entry %q not found", name)
	}

	if err := a.removeDeletedDotFiles(ctx, name, stowPath, deleteDot, opts); err != nil {
		if restoreErr := a.restoreDeletedDotConfig(deletedEntries); restoreErr != nil {
			return fmt.Errorf("%w (restore config failed: %v)", err, restoreErr)
		}
		return err
	}

	if gt := newGitForRepo(repoPath, executor.New()); gt.IsRepo() {
		msg := "dots: delete " + name
		if gitCfg.AutoPush {
			if err := gt.Push(ctx, msg); err != nil {
				return fmt.Errorf("dots delete: auto-push: %w", err)
			}
		} else if gitCfg.AutoCommit {
			if err := gt.CommitAll(ctx, msg); err != nil {
				return fmt.Errorf("dots delete: auto-commit: %w", err)
			}
		}
	}
	return nil
}

type deletedDotEntry struct {
	group string
	dot   config.DotEntry
}

func (a *App) removeDeletedDotFiles(ctx context.Context, name, stowPath string, deleteDot *config.DotEntry, opts DotsDeleteOptions) error {
	mgr, resolveErr := dots.New(stowPath, []config.DotEntry{*deleteDot})
	if resolveErr != nil {
		return fmt.Errorf("dots delete %q: resolve entry: %w", name, resolveErr)
	}
	unlinkOpts := dots.UnlinkOptions{KeepExistingLocal: true, RemoveLocal: !opts.KeepLocal}
	if _, unlinkErr := mgr.UnlinkAll(unlinkOpts); unlinkErr != nil {
		return fmt.Errorf("dots delete %q: %w", name, unlinkErr)
	}
	if rmErr := os.RemoveAll(filepath.Join(stowPath, deleteDot.Name)); rmErr != nil {
		return fmt.Errorf("dots delete %q: remove repo package: %w", name, rmErr)
	}
	return nil
}

func (a *App) restoreDeletedDotConfig(entries []deletedDotEntry) error {
	return a.withConfig(func(cfg *config.RootConfig) error {
		for _, entry := range entries {
			group := ensureGroupInConfig(cfg, entry.group)
			exists := false
			for _, d := range group.Dots {
				if d.Name == entry.dot.Name {
					exists = true
					break
				}
			}
			if !exists {
				group.Dots = append(group.Dots, entry.dot)
			}
		}
		return nil
	})
}

func (a *App) DotMembershipMap(_ context.Context) (map[string][]string, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	memberships := make(map[string][]string)
	for _, group := range cfg.Groups {
		for _, entry := range group.Dots {
			if entry.Name == "" {
				continue
			}
			memberships[entry.Name] = append(memberships[entry.Name], group.BaseName())
		}
	}
	for name := range memberships {
		sort.Strings(memberships[name])
	}
	return memberships, nil
}

func (a *App) AddDotToGroup(name, groupName string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("dots entry name is required")
	}
	return a.withConfig(func(cfg *config.RootConfig) error {
		template, ok := findDotEntryInConfig(cfg, name)
		if !ok {
			return fmt.Errorf("dots entry %q not found", name)
		}
		group := ensureGroupInConfig(cfg, groupName)
		for _, entry := range group.Dots {
			if entry.Name == name {
				return errSkipSave
			}
		}
		group.Dots = append(group.Dots, template)
		return nil
	})
}

func (a *App) RemoveDotFromGroup(name, groupName string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("dots entry name is required")
	}
	return a.withConfig(func(cfg *config.RootConfig) error {
		group := findGroupInConfig(cfg, groupName)
		if group == nil {
			return fmt.Errorf("group %q not found", groupName)
		}
		for i, entry := range group.Dots {
			if entry.Name != name {
				continue
			}
			group.Dots = append(group.Dots[:i], group.Dots[i+1:]...)
			return nil
		}
		return errSkipSave
	})
}

// DotsList returns the symlink health for every dots entry across active groups.
func (a *App) DotsList() ([]DotStatus, error) {
	m, groupMap, err := a.buildDotsManager()
	if err != nil {
		return nil, err
	}
	return entryHealth(m, groupMap), nil
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
	mgr, groupMap, err := a.buildDotsManager()
	if err != nil {
		return nil, err
	}
	statuses := entryHealth(mgr, groupMap)
	var gitStatus string
	repoPath, repoErr := resolveRepoPath(a.dotsRepoPath())
	if repoErr != nil {
		return nil, repoErr
	}
	g := newGitForRepo(repoPath, executor.New())
	if g.IsRepo() {
		gitStatus, err = g.Status(ctx)
		if err != nil {
			return &DotsStatusResult{Entries: statuses}, fmt.Errorf("dots status: git status: %w", err)
		}
	}
	return &DotsStatusResult{Entries: statuses, GitStatus: gitStatus}, nil
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
		discovered := entryHealth(mgr, nil)
		for i := range discovered {
			discovered[i].State = discoveredDotState(discovered[i])
			discovered[i].Actions = discoveredDotActions(discovered[i].State)
			discovered[i].Health = healthForDotState(discovered[i].State)
		}
		result.Entries = append(result.Entries, discovered...)
	}
	if len(ignoredCandidates) > 0 {
		mgr, err := dots.New(stowPath, ignoredCandidates)
		if err != nil {
			return result, fmt.Errorf("dots discover: resolve ignored candidates: %w", err)
		}
		ignored := entryHealth(mgr, nil)
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
		for _, child := range status.Children {
			if !child.Ignored {
				continue
			}
			ignored = append(ignored, DotStatus{
				Name:       status.Name + "/" + filepath.ToSlash(child.RelPath),
				TargetPath: child.Path,
				ConfigPath: child.Path,
				Health:     healthForDotState(DotStateIgnored),
				State:      DotStateIgnored,
				Group:      status.Group,
			})
		}
	}
	return ignored
}

func (a *App) QueryDotsStatus(ctx context.Context, opts DotsQueryOptions) (*DotsStatusResult, error) {
	result, err := a.DotsStatus(ctx)
	if err != nil && result == nil {
		return nil, err
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

// DotsPull runs git pull in the dots repo, then re-syncs all symlinks.
func (a *App) DotsPull(ctx context.Context) ([]dots.Op, error) {
	repoPath, err := resolveRepoPath(a.dotsRepoPath())
	if err != nil {
		return nil, err
	}
	if err := a.requireSafeTestDotsMutation(repoPath, nil); err != nil {
		return nil, err
	}
	g := newGitForRepo(repoPath, executor.New())
	if err := g.Pull(ctx); err != nil {
		return nil, fmt.Errorf("dots pull: %w", err)
	}
	return a.DotsSyncContext(ctx, dots.SyncOptions{})
}

// DotsPush stages all changes in the dots repo, commits, and pushes.
// When message is empty the commit message is auto-generated from the git
// status of the repo (e.g. "dots: update nvim, zshrc").
func (a *App) DotsPush(ctx context.Context, message string) error {
	repoPath, err := resolveRepoPath(a.dotsRepoPath())
	if err != nil {
		return err
	}
	if err := a.requireSafeTestDotsMutation(repoPath, nil); err != nil {
		return err
	}
	g := newGitForRepo(repoPath, executor.New())
	if message == "" {
		gitStatus, err := g.Status(ctx)
		if err != nil {
			return fmt.Errorf("dots push: %w", err)
		}
		message = dots.CommitMessageFromStatus(gitStatus)
	}
	return g.Push(ctx, message)
}

// DisableDotsOptions controls the behaviour of DotsDisable.
type DisableDotsOptions struct {
	// ConflictOverwrite, when true, overwrites any real (non-managed) files at
	// target paths with the repo version. When false those files are left in
	// place and an OpUnlinkConflict is recorded.
	ConflictOverwrite bool
	// KeepExistingLocal, when true, leaves real non-managed local files in place
	// instead of recording unlink conflicts.
	KeepExistingLocal bool
	// RemoveLocal removes local targets instead of materializing repo copies.
	RemoveLocal bool
}

// DotsDisable removes all managed symlinks and replaces them with real file
// copies from the repo. This leaves the user in a clean local state after
// disabling dots management. It does NOT remove dots entries from config —
// call SaveSettings to clear DotsRepo if the user also wants to stop tracking.
func (a *App) DotsDisable(opts DisableDotsOptions) ([]dots.Op, error) {
	if err := a.requireSafeTestHomeForDots(); err != nil {
		return nil, err
	}
	m, _, err := a.buildDotsManager()
	if err != nil {
		return nil, fmt.Errorf("dots disable: %w", err)
	}
	return m.UnlinkAll(dots.UnlinkOptions{
		ConflictOverwrite: opts.ConflictOverwrite,
		KeepExistingLocal: opts.KeepExistingLocal,
		RemoveLocal:       opts.RemoveLocal,
	})
}

// DisableDotsForHost disables dots on this machine. When a repo is configured,
// managed symlinks are first replaced with real local copies.
func (a *App) DisableDotsForHost(ctx context.Context, opts DisableDotsOptions) ([]dots.Op, error) {
	var ops []dots.Op
	var disableErr error
	if a.DotsConfigured() {
		ops, disableErr = a.DotsDisable(opts)
	}
	if err := a.SaveDotsDisabled(ctx, true); err != nil {
		if disableErr != nil {
			return ops, fmt.Errorf("%v; save dots disabled flag: %w", disableErr, err)
		}
		return ops, fmt.Errorf("save dots disabled flag: %w", err)
	}
	return ops, disableErr
}

// EnableDotsForHost enables dots on this machine. If dots_repo is configured,
// it immediately runs a sync so managed symlinks are restored.
func (a *App) EnableDotsForHost(ctx context.Context) ([]dots.Op, error) {
	if err := a.SaveDotsDisabled(ctx, false); err != nil {
		return nil, fmt.Errorf("save dots enabled flag: %w", err)
	}
	if !a.DotsConfigured() {
		return nil, nil
	}
	ops, err := a.DotsSyncContext(ctx, dots.SyncOptions{})
	if err != nil {
		return ops, fmt.Errorf("dots sync: %w", err)
	}
	return ops, nil
}

// DotsResolveConflict resolves a choice-based conflict for one tracked entry.
// Use-repo backs up the local target, removes it, and restows the repo version.
// Use-local commits the current repo state first when the repo source exists,
// copies local content into the repo, then replaces the local target with the
// managed link.
func (a *App) DotsResolveConflict(ctx context.Context, name string, strategy DotsResolveStrategy) ([]dots.Op, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("dots resolve: entry name is required")
	}
	repoPath, err := resolveRepoPath(a.dotsRepoPath())
	if err != nil {
		return nil, err
	}
	if err := a.requireSafeTestDotsMutation(repoPath, nil); err != nil {
		return nil, err
	}
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
	if profileName, ok := rootCfg.ActiveProfile(currentHostname()); ok {
		if effective, _, e := effectiveProfileGroups(rootCfg, groups, profileName); e == nil {
			groups = effective
		}
	}
	entries := collectDots(groups)
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
	if err := refuseIgnoredDotSource(entry); err != nil {
		return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}}, err
	}
	backupPath, err := backupAndRemoveLocalTarget(entry.TargetPath)
	if err != nil {
		return nil, err
	}
	if err := dots.Restow(ctx, executor.New(), stowPath, []string{entry.Name}, false); err != nil {
		if backupPath != "" {
			if restoreErr := restoreDotBackupAfterFailedStow(backupPath, entry.TargetPath); restoreErr != nil {
				return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}},
					fmt.Errorf("%w (restore failed: %v)", err, restoreErr)
			}
		}
		return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}}, err
	}
	return []dots.Op{{Kind: dots.OpRepair, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
}

func resolveDotUseLocal(ctx context.Context, repoPath, stowPath string, entry dots.ResolvedEntry) (ops []dots.Op, retErr error) {
	copySource, err := localDotCopySource(entry.TargetPath)
	if err != nil {
		return nil, err
	}
	backupPath, err := backupLocalTarget(entry.TargetPath)
	if err != nil {
		return nil, err
	}
	if pathExists(entry.SourcePath) {
		gt := newGitForRepo(repoPath, executor.New())
		if gt.IsRepo() {
			if err := gt.CommitAll(ctx, "dots: pre-resolve "+entry.Name); err != nil {
				return nil, fmt.Errorf("dots resolve %q: pre-commit repo state: %w", entry.Name, err)
			}
		}
	}
	replacement, err := replaceDotSourceFromLocal(copySource, entry.SourcePath, filepath.Join(stowPath, entry.Name), entry.Ignore)
	if err != nil {
		return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}},
			fmt.Errorf("dots resolve %q: replace repo source: %w", entry.Name, err)
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
	if err := refuseIgnoredDotSource(entry); err != nil {
		return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}}, err
	}
	if removeErr := os.RemoveAll(entry.TargetPath); removeErr != nil && !os.IsNotExist(removeErr) {
		return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: removeErr}},
			fmt.Errorf("dots resolve %q: remove local target: %w", entry.Name, removeErr)
	}
	if err := dots.Restow(ctx, executor.New(), stowPath, []string{entry.Name}, false); err != nil {
		if backupPath != "" {
			if restoreErr := restoreDotBackupAfterFailedStow(backupPath, entry.TargetPath); restoreErr != nil {
				return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}},
					fmt.Errorf("%w (restore failed: %v)", err, restoreErr)
			}
		}
		return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}}, err
	}
	committedSource = true
	if err := replacement.commit(); err != nil {
		return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}},
			fmt.Errorf("dots resolve %q: cleanup old source: %w", entry.Name, err)
	}
	return []dots.Op{{Kind: dots.OpAdopt, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
}

func rollbackDotsAdd(targetPath, packagePath, backupPath string) error {
	var errs []string
	if err := os.RemoveAll(targetPath); err != nil && !os.IsNotExist(err) {
		errs = append(errs, fmt.Sprintf("remove target link: %v", err))
	}
	if backupPath != "" {
		if err := restoreBackupPath(backupPath, targetPath); err != nil {
			errs = append(errs, fmt.Sprintf("restore backup: %v", err))
		}
	}
	if err := os.RemoveAll(packagePath); err != nil && !os.IsNotExist(err) {
		errs = append(errs, fmt.Sprintf("remove repo package: %v", err))
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

type dotSourceReplacement struct {
	commit   func() error
	rollback func() error
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
	if err := os.RemoveAll(originalPath); err != nil && !os.IsNotExist(err) {
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

// requireStow returns a descriptive error if the stow binary is not on PATH.
// Call this at the start of any App method that delegates to stow.
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

// InstallDotsStow installs GNU Stow through the system ecosystem provider.
// Callers own user consent before invoking this; this method may run the host
// package manager and can require privileges depending on the concrete manager.
func (a *App) InstallDotsStow(ctx context.Context) error {
	if a.DotsStowInstalled(ctx) {
		return nil
	}
	settings, _ := a.LoadSettings()
	priority := settings.EcosystemPriority(provider.EcosystemSystem)
	if len(priority) == 0 {
		priority = provider.BuiltinSystemProviderPriorityNames()
	}
	providerName, err := a.resolveProvider(ctx, priority)
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

func (a *App) buildDotsManager() (*dots.Manager, map[string]string, error) {
	repoPath, err := resolveRepoPath(a.dotsRepoPath())
	if err != nil {
		return nil, nil, err
	}
	if err := a.requireSafeTestDotsMutation(repoPath, nil); err != nil {
		return nil, nil, err
	}
	stowPath := dotsContentPath(repoPath)
	rootCfg, err := a.loadConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("dots: load groups: %w", err)
	}
	groups := rootCfg.Groups
	// Apply profile filtering so only this machine's groups are shown —
	// same logic as DotsSync to prevent other-profile entries appearing as HealthNoSource.
	if profileName, ok := rootCfg.ActiveProfile(currentHostname()); ok {
		if effective, _, e := effectiveProfileGroups(rootCfg, groups, profileName); e == nil {
			groups = effective
		}
	}
	entries := collectDots(groups)
	if err := a.requireSafeTestDotsMutation(repoPath, entries); err != nil {
		return nil, nil, err
	}
	groupMap := collectDotsGroupMap(groups)
	mgr, err := dots.New(stowPath, entries)
	return mgr, groupMap, err
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

func syncResolvedDotEntry(ctx context.Context, stowPath string, entry dots.ResolvedEntry, opts dots.SyncOptions, failUnsyncable bool) ([]dots.Op, error) {
	state, _ := classifyDotEntry(entry)
	switch state {
	case DotStateSynced:
		if err := refuseIgnoredDotSource(entry); err != nil {
			return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}}, err
		}
		return []dots.Op{{Kind: dots.OpSkip, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
	case DotStateMissing:
		if opts.DryRun {
			return []dots.Op{{Kind: dots.OpDryLink, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
		}
		if err := refuseIgnoredDotSource(entry); err != nil {
			return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}}, err
		}
		if err := dots.Restow(ctx, executor.New(), stowPath, []string{entry.Name}, false); err != nil {
			return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}}, err
		}
		return []dots.Op{lstatOp(entry.Name, entry.TargetPath, stowPath, false)}, nil
	case DotStateBroken:
		if opts.DryRun {
			return []dots.Op{{Kind: dots.OpDryRepair, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
		}
		if err := refuseIgnoredDotSource(entry); err != nil {
			return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}}, err
		}
		backupPath, err := backupAndRemoveLocalTarget(entry.TargetPath)
		if err != nil {
			return nil, err
		}
		if err := dots.Restow(ctx, executor.New(), stowPath, []string{entry.Name}, false); err != nil {
			if backupPath != "" {
				if restoreErr := restoreDotBackupAfterFailedStow(backupPath, entry.TargetPath); restoreErr != nil {
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
	case DotStateConflict, DotStateUntrackedConflict, DotStateAmbiguous:
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
	backupPath, err := backupLocalTarget(entry.TargetPath)
	if err != nil {
		return dots.Op{}, err
	}
	if err := copyDotPath(copySource, entry.SourcePath, combinedDotIgnores(entry.Ignore)); err != nil {
		if removeErr := os.RemoveAll(entry.SourcePath); removeErr != nil {
			return dots.Op{}, fmt.Errorf("copy local into repo: %w (remove created source failed: %v)", err, removeErr)
		}
		return dots.Op{}, fmt.Errorf("copy local into repo: %w", err)
	}
	if removeErr := os.RemoveAll(entry.TargetPath); removeErr != nil && !os.IsNotExist(removeErr) {
		if cleanupErr := os.RemoveAll(entry.SourcePath); cleanupErr != nil {
			return dots.Op{}, fmt.Errorf("remove local target: %w (remove created source failed: %v)", removeErr, cleanupErr)
		}
		return dots.Op{}, fmt.Errorf("remove local target: %w", removeErr)
	}
	if err := dots.Restow(ctx, executor.New(), stowPath, []string{entry.Name}, false); err != nil {
		if removeErr := os.RemoveAll(entry.SourcePath); removeErr != nil {
			return dots.Op{}, fmt.Errorf("%w (remove created source failed: %v)", err, removeErr)
		}
		if backupPath != "" {
			if restoreErr := restoreDotBackupAfterFailedStow(backupPath, entry.TargetPath); restoreErr != nil {
				return dots.Op{}, fmt.Errorf("%w (restore failed: %v)", err, restoreErr)
			}
		}
		return dots.Op{}, err
	}
	return dots.Op{Kind: dots.OpAdopt, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}, nil
}

func localDotCopySource(targetPath string) (string, error) {
	info, err := os.Lstat(targetPath)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("local target %q is a symlink; refusing to adopt linked external content automatically", targetPath)
	}
	return targetPath, nil
}

func backupAndRemoveLocalTarget(targetPath string) (string, error) {
	backupPath, backupErr := dots.BackupLocalPath(targetPath)
	if backupErr != nil && !os.IsNotExist(backupErr) {
		return "", fmt.Errorf("backup %q: %w", targetPath, backupErr)
	}
	if removeErr := os.RemoveAll(targetPath); removeErr != nil && !os.IsNotExist(removeErr) {
		return backupPath, fmt.Errorf("remove %q: %w", targetPath, removeErr)
	}
	return backupPath, nil
}

func backupLocalTarget(targetPath string) (string, error) {
	backupPath, backupErr := dots.BackupLocalPath(targetPath)
	if backupErr != nil && !os.IsNotExist(backupErr) {
		return "", fmt.Errorf("backup %q: %w", targetPath, backupErr)
	}
	return backupPath, nil
}

func combinedDotIgnores(ignores []string) []string {
	return append(dots.DefaultIgnores(), ignores...)
}

func refuseIgnoredDotSource(entry dots.ResolvedEntry) error {
	rel, ok, err := firstIgnoredDotSourcePath(entry.SourcePath, entry.Ignore)
	if err != nil {
		return fmt.Errorf("check ignored repo source: %w", err)
	}
	if !ok {
		return nil
	}
	return fmt.Errorf("repo source contains ignored path %q; refusing to stow ignored content", rel)
}

func firstIgnoredDotSourcePath(root string, ignores []string) (string, bool, error) {
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	allIgnores := combinedDotIgnores(ignores)
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		if shouldIgnoreDotPath(filepath.Dir(root), filepath.Base(root), filepath.Base(root), allIgnores) {
			return filepath.Base(root), true, nil
		}
		return "", false, nil
	}
	var first string
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		if shouldIgnoreDotPath(root, rel, d.Name(), allIgnores) {
			first = filepath.ToSlash(rel)
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		return nil
	})
	if walkErr != nil {
		return "", false, walkErr
	}
	return first, first != "", nil
}

func copyDotPath(src, dst string, ignores []string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("dot copy source %q is a symlink; refusing to adopt linked external content automatically", src)
	}
	if info.IsDir() {
		return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, relErr := filepath.Rel(src, path)
			if relErr != nil {
				return relErr
			}
			if rel != "." && shouldIgnoreDotPath(src, rel, d.Name(), ignores) {
				if d.IsDir() {
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
			return copyDotPath(path, target, ignores)
		})
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	return copyDotFile(src, dst, info.Mode().Perm())
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
	if dots.ShouldIgnorePath(relPath, basename, ignores) {
		return true
	}
	rooted := filepath.ToSlash(filepath.Join(filepath.Base(root), relPath))
	return dots.ShouldIgnorePath(rooted, basename, ignores)
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

func pathWithinDir(path, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// collectDots gathers all DotEntry values from a slice of groups.
func collectDots(groups []*config.GroupConfig) []config.DotEntry {
	var entries []config.DotEntry
	seen := make(map[string]struct{})
	for _, g := range groups {
		for _, entry := range g.Dots {
			if _, ok := seen[entry.Name]; ok {
				continue
			}
			seen[entry.Name] = struct{}{}
			entries = append(entries, entry)
		}
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
func entryHealth(m *dots.Manager, groupMap map[string]string) []DotStatus {
	statuses := make([]DotStatus, 0, len(m.Entries))
	for _, e := range m.Entries {
		state, actions := classifyDotEntry(e)
		contentRoot := dotStatusContentRoot(e)
		fileCount := countManagedDotFiles(contentRoot, e.Ignore)
		children := directDotChildren(contentRoot, e.TargetPath, e.Ignore)
		if state == DotStateIgnored {
			fileCount = 0
			children = nil
		}
		statuses = append(statuses, DotStatus{
			Name:       e.Name,
			SourcePath: e.SourcePath,
			TargetPath: e.TargetPath,
			ConfigPath: configPathForTarget(e.TargetPath),
			Health:     healthForDotState(state),
			State:      state,
			Actions:    actions,
			Group:      groupMap[e.Name],
			FileCount:  fileCount,
			Children:   children,
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

func countManagedDotFiles(root string, ignores []string) int {
	if root == "" {
		return 0
	}
	info, err := os.Lstat(root)
	if err != nil {
		return 0
	}
	if !info.IsDir() {
		if shouldIgnoreDotPath(root, ".", filepath.Base(root), ignores) {
			return 0
		}
		if !isManagedDotFile(info.Mode()) {
			return 0
		}
		return 1
	}
	count := 0
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		if rel != "." && shouldIgnoreDotPath(root, rel, d.Name(), ignores) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		if !d.IsDir() && isManagedDotFile(info.Mode()) {
			count++
		}
		return nil
	})
	return count
}

func directDotChildren(sourceRoot, targetRoot string, ignores []string) []DotChild {
	if sourceRoot == "" {
		return nil
	}
	info, err := os.Lstat(sourceRoot)
	if err != nil || !info.IsDir() {
		return nil
	}
	return directDotChildrenAt(sourceRoot, targetRoot, "", ignores, 1)
}

func directDotChildrenAt(sourceRoot, targetRoot, relRoot string, ignores []string, depth int) []DotChild {
	if depth > dotChildrenMaxDepth {
		return nil
	}
	dir := sourceRoot
	if relRoot != "" {
		dir = filepath.Join(sourceRoot, relRoot)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return entries[i].Name() < entries[j].Name()
	})
	children := make([]DotChild, 0, len(entries))
	for _, entry := range entries {
		rel := entry.Name()
		if relRoot != "" {
			rel = filepath.Join(relRoot, entry.Name())
		}
		sourcePath := filepath.Join(sourceRoot, rel)
		info, infoErr := entry.Info()
		if infoErr != nil || (!entry.IsDir() && !isManagedDotFile(info.Mode())) {
			continue
		}
		ignored := shouldIgnoreDotPath(sourceRoot, rel, entry.Name(), ignores)
		child := DotChild{
			Name:    entry.Name(),
			RelPath: rel,
			Path:    filepath.Join(targetRoot, rel),
			IsDir:   entry.IsDir(),
			Depth:   depth,
			Ignored: ignored,
		}
		if ignored {
			child.FileCount = 0
		} else if entry.IsDir() {
			child.FileCount = countManagedDotFiles(sourcePath, ignores)
		} else {
			child.FileCount = 1
		}
		children = append(children, child)
		if entry.IsDir() && !ignored {
			children = append(children, directDotChildrenAt(sourceRoot, targetRoot, rel, ignores, depth+1)...)
		}
	}
	return children
}

func isManagedDotFile(mode os.FileMode) bool {
	return mode.IsRegular() || mode&os.ModeSymlink != 0
}

// DotsAddIgnorePattern appends a per-entry glob pattern to the named dots entry
// in config. The pattern is validated (filepath.Match) before saving.
// Adding a pattern that is already present is a no-op.
func (a *App) DotsAddIgnorePattern(name, pattern string) error {
	if _, err := filepath.Match(pattern, "test"); err != nil {
		return fmt.Errorf("invalid glob pattern %q: %w", pattern, err)
	}
	return a.withConfig(func(rootCfg *config.RootConfig) error {
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

// DotsSetEntryIgnored toggles whole-entry dotfile ignore state. When ignoring
// an untracked discovery candidate, the ignored entry is persisted to this
// machine group so discovery does not keep suggesting it.
func (a *App) DotsSetEntryIgnored(name, path string, ignored bool) error {
	return a.withConfig(func(rootCfg *config.RootConfig) error {
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
		group := ensureGroupInConfig(rootCfg, shortHostname(currentHostname()))
		group.Dots = append(group.Dots, config.DotEntry{Name: name, Path: normalisePath(path), Ignored: ignored})
		return nil
	})
}

// DotsRemoveIgnorePattern removes a per-entry ignore glob from the named dots
// entry in config. Removing a pattern that is not present is a no-op.
func (a *App) DotsRemoveIgnorePattern(name, pattern string) error {
	return a.withConfig(func(rootCfg *config.RootConfig) error {
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

// computeEntryHealth determines health by inspecting the stow-managed target path.
// TargetPath is the original location (e.g. ~/.config/nvim); stow creates a
// directory-level symlink there pointing to SourcePath in the repo.
func computeEntryHealth(e dots.ResolvedEntry) DotHealth {
	state, _ := classifyDotEntry(e)
	return healthForDotState(state)
}
