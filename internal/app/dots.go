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
	"github.com/lkshrk/omni/internal/provider"
)

// DotsAddOptions controls the behaviour of DotsAdd.
type DotsAddOptions struct {
	// Name overrides the name inferred from the path.
	Name string
	// Group is the config group name to write the entry to.
	// Defaults to the current host group when empty.
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

type DotVariantInfo struct {
	Name    string `json:"name"`
	Host    string `json:"host,omitempty"`
	Package string `json:"package"`
	Default bool   `json:"default,omitempty"`
	Active  bool   `json:"active,omitempty"`
}

type DotsAddVariantOptions struct {
	// Host is the short hostname for the variant. Defaults to the current host.
	Host string
	// Package is the stow package directory for this variant. Defaults to
	// "<name>@<host>".
	Package string
	// Sync immediately syncs the entry when Host is the current host.
	Sync bool
}

type DotsRemoveVariantOptions struct {
	// Host is the short hostname for the variant. Defaults to the current host.
	Host string
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
	Name       string        `json:"name"`
	Package    string        `json:"package,omitempty"`
	Variant    bool          `json:"variant,omitempty"`
	SourcePath string        `json:"source_path"`
	TargetPath string        `json:"target_path"`
	ConfigPath string        `json:"path,omitempty"`
	Health     DotHealth     `json:"health"`
	State      DotState      `json:"state"`
	Actions    []DotAction   `json:"actions,omitempty"`
	Group      string        `json:"group,omitempty"` // config group name (for example "work" or the current host group)
	FileCount  int           `json:"file_count,omitempty"`
	Counts     DotFileCounts `json:"counts,omitempty"`
	IsDir      bool          `json:"is_dir,omitempty"`
	Children   []DotChild    `json:"children,omitempty"`

	ignoredChildren []DotChild
}

type DotChild struct {
	Name      string        `json:"name"`
	RelPath   string        `json:"rel_path"`
	Path      string        `json:"path"`
	State     DotState      `json:"state,omitempty"`
	IsDir     bool          `json:"is_dir"`
	Depth     int           `json:"depth,omitempty"`
	Ignored   bool          `json:"ignored,omitempty"`
	FileCount int           `json:"file_count,omitempty"`
	Counts    DotFileCounts `json:"counts,omitempty"`
	Children  []DotChild    `json:"children,omitempty"`
}

type DotFileCounts struct {
	Synced    int `json:"synced,omitempty"`
	OutOfSync int `json:"out_of_sync,omitempty"`
	Ignored   int `json:"ignored,omitempty"`
}

func (c DotFileCounts) Managed() int {
	return c.Synced + c.OutOfSync
}

func (c DotFileCounts) Total() int {
	return c.Managed() + c.Ignored
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

const (
	dotsContentDirName  = "dotfiles"
	dotChildrenMaxDepth = 4
)

// ─── public API ───────────────────────────────────────────────────────────────

// DotsConfigured reports whether dots_repo is set in settings.json.
func (a *App) DotsConfigured() bool {
	return a.dotsRepoPath() != ""
}

func (a *App) requireDotsEnabled(rootCfg *config.RootConfig) error {
	if config.BoolVal(a.effectiveSettings(rootCfg).DotsDisabled) {
		return fmt.Errorf("dots are disabled for this host")
	}
	return nil
}

// DotsSync creates or repairs all symlinks for all dots entries across active
// groups. When the current host has assigned groups, only the host group and
// assigned reusable groups are synced. Falls back to all groups when no active
// host is configured.
// All entries are managed via GNU Stow.
func (a *App) DotsSync(opts dots.SyncOptions) ([]dots.Op, error) {
	return a.DotsSyncContext(context.Background(), opts)
}

// DotsSyncContext creates or repairs all symlinks like DotsSync, honoring ctx
// for provider/stow commands.
func (a *App) DotsSyncContext(ctx context.Context, opts dots.SyncOptions) ([]dots.Op, error) {
	rootCfg, err := a.loadConfig()
	if err != nil {
		return nil, fmt.Errorf("dots sync: load config: %w", err)
	}
	if err := a.requireDotsEnabled(rootCfg); err != nil {
		return nil, err
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
			return nil, fmt.Errorf("dots sync: content dir: %w", err)
		}
	}
	groups := rootCfg.Groups
	if effective, _, ok := effectiveHostGroups(rootCfg, groups, currentMachineGroupName()); ok {
		groups = effective
	}
	entries := collectDots(rootCfg, groups)
	entries = resolveDotEntryPackagesForCurrentHost(entries)
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
	orderedEntries := orderResolvedDotEntries(mgr.Entries, opts.EntryOrder)
	var ops []dots.Op
	var failures []dotSyncFailure
	total := len(orderedEntries)
	for i, entry := range orderedEntries {
		if opts.Progress != nil {
			opts.Progress(dots.SyncProgressEvent{Entry: entry.Name, Index: i + 1, Total: total})
		}
		entryOps, syncErr := syncResolvedDotEntry(ctx, repoPath, stowPath, entry, opts, false)
		if opts.Progress != nil {
			opts.Progress(dots.SyncProgressEvent{Entry: entry.Name, Index: i + 1, Total: total, Done: true, Err: syncErr, Ops: entryOps})
		}
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

func orderResolvedDotEntries(entries []dots.ResolvedEntry, order []string) []dots.ResolvedEntry {
	if len(entries) == 0 || len(order) == 0 {
		return entries
	}
	rank := make(map[string]int, len(order))
	for i, name := range order {
		if name == "" {
			continue
		}
		if _, exists := rank[name]; !exists {
			rank[name] = i
		}
	}
	if len(rank) == 0 {
		return entries
	}
	ordered := append([]dots.ResolvedEntry(nil), entries...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left, leftOK := rank[ordered[i].Name]
		right, rightOK := rank[ordered[j].Name]
		switch {
		case leftOK && rightOK:
			return left < right
		case leftOK:
			return true
		case rightOK:
			return false
		default:
			return false
		}
	})
	return ordered
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
	rootCfg, err := a.loadConfig()
	if err != nil {
		return nil, fmt.Errorf("dots sync: load config: %w", err)
	}
	if err := a.requireDotsEnabled(rootCfg); err != nil {
		return nil, err
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
	groups := rootCfg.Groups
	if effective, _, ok := effectiveHostGroups(rootCfg, groups, currentMachineGroupName()); ok {
		groups = effective
	}
	entries := collectDots(rootCfg, groups)
	entries = resolveDotEntryPackagesForCurrentHost(entries)
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
		ops, syncErr := syncResolvedDotEntry(ctx, repoPath, stowPath, entry, opts, true)
		if syncErr != nil {
			return ops, fmt.Errorf("dots sync %q: %w", name, syncErr)
		}
		return ops, nil
	}
	return nil, fmt.Errorf("dots entry %q not found", name)
}

// DotsAdd moves the file/dir at path into the dots repo and links it back via
// stow. A backup is made under ~/dotfiles.bkp before any mutation, then the
// local original is moved to trash. path must exist on disk.
func (a *App) DotsAdd(ctx context.Context, path string, opts DotsAddOptions) ([]dots.Op, error) {
	rootCfg, err := a.loadConfig()
	if err != nil {
		return nil, fmt.Errorf("dots add: load config: %w", err)
	}
	if err := a.requireDotsEnabled(rootCfg); err != nil {
		return nil, err
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
	pkgName := entry.EffectivePackage()
	if opts.Group == "" {
		opts.Group = currentMachineGroupName()
	}
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
	pkgDst, err := stowPackagePath(stowPath, pkgName, abs)
	if err != nil {
		return nil, fmt.Errorf("dots add: %w", err)
	}
	if _, statErr := os.Lstat(pkgDst); statErr == nil {
		return nil, fmt.Errorf("dots add: %q is already tracked (repo path: %s)", name, pkgDst)
	}
	pkgRoot := filepath.Join(stowPath, pkgName)
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

	// Copy filtered content into the repo package tree, move the local original
	// to trash, then stow links it back. The backup remains a full safety copy.
	if err := os.MkdirAll(filepath.Dir(pkgDst), 0o755); err != nil {
		return nil, fmt.Errorf("dots add: create package dir: %w", err)
	}
	if err := copyDotPath(abs, pkgDst, combinedDotIgnores(entry.Ignore)); err != nil {
		if cleanupErr := cleanupPackage(); cleanupErr != nil {
			return nil, fmt.Errorf("dots add: copy to repo: %w (cleanup failed: %v)", err, cleanupErr)
		}
		return nil, fmt.Errorf("dots add: copy to repo: %w", err)
	}
	if err := dots.RemoveLocalPathAfterBackup(abs, backupPath); err != nil {
		if cleanupErr := cleanupPackage(); cleanupErr != nil {
			return nil, fmt.Errorf("dots add: remove local target: %w (cleanup failed: %v)", err, cleanupErr)
		}
		return nil, fmt.Errorf("dots add: remove local target: %w", err)
	}
	if err := dots.Restow(ctx, executor.New(), stowPath, []string{pkgName}, false); err != nil {
		if rollbackErr := rollbackDotsAdd(abs, pkgDst, backupPath); rollbackErr != nil {
			return nil, fmt.Errorf("dots add: stow: %w (rollback failed: %v)", err, rollbackErr)
		}
		return nil, fmt.Errorf("dots add: stow: %w", err)
	}

	// Record entry in config using normalised ~-form path.
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		if _, err := ensureHostGroupInConfig(cfg, currentMachineGroupName()); err != nil {
			return err
		}
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

	return []dots.Op{lstatEntryOp(dots.ResolvedEntry{
		Name:       name,
		Package:    pkgName,
		SourcePath: pkgDst,
		TargetPath: abs,
		Ignore:     entry.Ignore,
	}, false)}, nil
}

func (a *App) DotsListVariants(name string) ([]DotVariantInfo, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("dots variants: entry name is required")
	}
	rootCfg, err := a.loadConfig()
	if err != nil {
		return nil, fmt.Errorf("dots variants: load config: %w", err)
	}
	if err := a.requireDotsEnabled(rootCfg); err != nil {
		return nil, err
	}
	entry, ok := findDotEntryInConfig(rootCfg, name)
	if !ok {
		return nil, fmt.Errorf("dots entry %q not found", name)
	}
	activePackage := entry.PackageForHost(currentMachineGroupName())
	variants := []DotVariantInfo{{
		Name:    entry.Name,
		Package: entry.EffectivePackage(),
		Default: true,
		Active:  entry.EffectivePackage() == activePackage,
	}}
	hosts := make([]string, 0, len(entry.Hosts))
	for host := range entry.Hosts {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	for _, host := range hosts {
		pkgName := entry.PackageForHost(host)
		variants = append(variants, DotVariantInfo{
			Name:    entry.Name,
			Host:    host,
			Package: pkgName,
			Active:  host == currentMachineGroupName() && pkgName == activePackage,
		})
	}
	return variants, nil
}

func (a *App) DotsAddHostVariant(ctx context.Context, name string, opts DotsAddVariantOptions) (DotVariantInfo, []dots.Op, error) {
	if err := dots.ValidateEntryName(name); err != nil {
		return DotVariantInfo{}, nil, fmt.Errorf("dots variant add: %w", err)
	}
	host := normalizeDotsVariantHost(opts.Host)
	if host == "" {
		return DotVariantInfo{}, nil, fmt.Errorf("dots variant add: host is required")
	}
	pkgName := strings.TrimSpace(opts.Package)
	if pkgName == "" {
		pkgName = defaultDotVariantPackage(name, host)
	}
	if err := dots.ValidateEntryName(pkgName); err != nil {
		return DotVariantInfo{}, nil, fmt.Errorf("dots variant add: package: %w", err)
	}
	rootCfg, err := a.loadConfig()
	if err != nil {
		return DotVariantInfo{}, nil, fmt.Errorf("dots variant add: load config: %w", err)
	}
	if err := a.requireDotsEnabled(rootCfg); err != nil {
		return DotVariantInfo{}, nil, err
	}
	gitCfg := rootCfg.Settings.DotsGit
	entry, ok := findDotEntryInConfig(rootCfg, name)
	if !ok {
		return DotVariantInfo{}, nil, fmt.Errorf("dots entry %q not found", name)
	}
	if existing, ok := entry.Hosts[host]; ok {
		if existing.Package == pkgName {
			return DotVariantInfo{Name: name, Host: host, Package: pkgName, Active: host == currentMachineGroupName()}, nil, nil
		}
		return DotVariantInfo{}, nil, fmt.Errorf("dots variant add: %q already has host variant for %q", name, host)
	}
	if owner, exists := dotPackageOwner(rootCfg, pkgName); exists && owner != name {
		return DotVariantInfo{}, nil, fmt.Errorf("dots variant add: package %q is already used by dotfile %q", pkgName, owner)
	}

	repoPath, err := resolveRepoPath(a.effectiveSettings(rootCfg).DotsRepo)
	if err != nil {
		return DotVariantInfo{}, nil, err
	}
	if err := a.requireSafeTestDotsMutation(repoPath, []config.DotEntry{entry}); err != nil {
		return DotVariantInfo{}, nil, err
	}
	stowPath, err := ensureDotsContentPath(repoPath)
	if err != nil {
		return DotVariantInfo{}, nil, fmt.Errorf("dots variant add: content dir: %w", err)
	}
	source, err := ensureDotVariantSource(stowPath, entry, pkgName)
	if err != nil {
		return DotVariantInfo{}, nil, fmt.Errorf("dots variant add: %w", err)
	}

	var changed bool
	saveErr := a.withConfig(func(cfg *config.RootConfig) error {
		if err := a.requireDotsEnabled(cfg); err != nil {
			return err
		}
		dot, ok := findDotEntryPtrInConfig(cfg, name)
		if !ok {
			return fmt.Errorf("dots entry %q not found", name)
		}
		if owner, exists := dotPackageOwner(cfg, pkgName); exists && owner != name {
			return fmt.Errorf("dots variant add: package %q is already used by dotfile %q", pkgName, owner)
		}
		if dot.Hosts == nil {
			dot.Hosts = make(map[string]config.DotVariant)
		}
		if _, ok := dot.Hosts[host]; ok {
			return fmt.Errorf("dots variant add: %q already has host variant for %q", name, host)
		}
		dot.Hosts[host] = config.DotVariant{Package: pkgName}
		changed = true
		return nil
	})
	if saveErr != nil {
		if source.Created {
			_ = os.RemoveAll(source.CleanupPath)
		}
		return DotVariantInfo{}, nil, saveErr
	}

	info := DotVariantInfo{Name: name, Host: host, Package: pkgName, Active: host == currentMachineGroupName()}
	var ops []dots.Op
	if changed && opts.Sync && host == currentMachineGroupName() {
		syncOps, syncErr := a.DotsSyncEntry(ctx, name, dots.SyncOptions{})
		ops = syncOps
		if syncErr != nil {
			return info, ops, fmt.Errorf("dots variant add: sync %q: %w", name, syncErr)
		}
	}
	if source.Created {
		if gt := newGitForRepo(repoPath, executor.New()); gt.IsRepo() {
			msg := fmt.Sprintf("dots: add %s variant for %s", name, host)
			if gitCfg.AutoPush {
				if err := gt.Push(ctx, msg); err != nil {
					return info, ops, fmt.Errorf("dots variant add: auto-push: %w", err)
				}
			} else if gitCfg.AutoCommit {
				if err := gt.CommitAll(ctx, msg); err != nil {
					return info, ops, fmt.Errorf("dots variant add: auto-commit: %w", err)
				}
			}
		}
	}
	return info, ops, nil
}

func (a *App) DotsRemoveHostVariant(ctx context.Context, name string, opts DotsRemoveVariantOptions) (DotVariantInfo, error) {
	if err := dots.ValidateEntryName(name); err != nil {
		return DotVariantInfo{}, fmt.Errorf("dots variant remove: %w", err)
	}
	host := normalizeDotsVariantHost(opts.Host)
	if host == "" {
		return DotVariantInfo{}, fmt.Errorf("dots variant remove: host is required")
	}
	var (
		removed       DotVariantInfo
		repoPath      string
		stowPath      string
		gitCfg        config.DotsGitConfig
		removePackage bool
	)
	err := a.withConfig(func(cfg *config.RootConfig) error {
		if err := a.requireDotsEnabled(cfg); err != nil {
			return err
		}
		var err error
		repoPath, err = resolveRepoPath(a.effectiveSettings(cfg).DotsRepo)
		if err != nil {
			return err
		}
		stowPath, err = existingDotsContentPath(repoPath)
		if err != nil {
			return fmt.Errorf("dots variant remove: content dir: %w", err)
		}
		gitCfg = cfg.Settings.DotsGit
		dot, ok := findDotEntryPtrInConfig(cfg, name)
		if !ok {
			return fmt.Errorf("dots entry %q not found", name)
		}
		if err := a.requireSafeTestDotsMutation(repoPath, []config.DotEntry{*dot}); err != nil {
			return err
		}
		variant, ok := dot.Hosts[host]
		if !ok {
			return fmt.Errorf("dots variant remove: %q has no variant for host %q", name, host)
		}
		removed = DotVariantInfo{Name: name, Host: host, Package: variant.Package}
		delete(dot.Hosts, host)
		if len(dot.Hosts) == 0 {
			dot.Hosts = nil
		}
		removePackage = !dotPackageReferencedInConfig(cfg, variant.Package)
		return nil
	})
	if err != nil {
		return removed, err
	}

	if host == currentMachineGroupName() {
		if _, syncErr := a.DotsSyncEntry(ctx, name, dots.SyncOptions{}); syncErr != nil {
			_, resolveErr := a.DotsResolveConflict(ctx, name, DotResolveUseRepo)
			if resolveErr != nil {
				return removed, fmt.Errorf("dots variant remove: sync %q: %w", name, errors.Join(syncErr, resolveErr))
			}
		}
	}

	if removePackage {
		pkgRoot := filepath.Join(stowPath, removed.Package)
		if rmErr := os.RemoveAll(pkgRoot); rmErr != nil {
			return removed, fmt.Errorf("dots variant remove: remove repo package %q: %w", removed.Package, rmErr)
		}
		if gt := newGitForRepo(repoPath, executor.New()); gt.IsRepo() {
			msg := fmt.Sprintf("dots: remove %s variant for %s", name, host)
			if gitCfg.AutoPush {
				if err := gt.Push(ctx, msg); err != nil {
					return removed, fmt.Errorf("dots variant remove: auto-push: %w", err)
				}
			} else if gitCfg.AutoCommit {
				if err := gt.CommitAll(ctx, msg); err != nil {
					return removed, fmt.Errorf("dots variant remove: auto-commit: %w", err)
				}
			}
		}
	}
	return removed, nil
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
		if err := a.requireDotsEnabled(rootCfg); err != nil {
			return err
		}
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
	unlinkDots := dotEntriesForAllPackages(*deleteDot)
	mgr, resolveErr := dots.New(stowPath, unlinkDots)
	if resolveErr != nil {
		return fmt.Errorf("dots delete %q: resolve entry: %w", name, resolveErr)
	}
	unlinkOpts := dots.UnlinkOptions{KeepExistingLocal: true, RemoveLocal: !opts.KeepLocal}
	if _, unlinkErr := mgr.UnlinkAll(unlinkOpts); unlinkErr != nil {
		return fmt.Errorf("dots delete %q: %w", name, unlinkErr)
	}
	for _, pkgName := range dotEntryPackages(*deleteDot) {
		if rmErr := os.RemoveAll(filepath.Join(stowPath, pkgName)); rmErr != nil {
			return fmt.Errorf("dots delete %q: remove repo package %q: %w", name, pkgName, rmErr)
		}
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

// MoveDotToGroup makes groupName the dotfile entry's only owning group.
func (a *App) MoveDotToGroup(name, groupName string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("dots entry name is required")
	}
	groupName = compatibilityGroupName(groupName)
	return a.withConfig(func(cfg *config.RootConfig) error {
		if err := a.requireDotsEnabled(cfg); err != nil {
			return err
		}
		template, ok := findDotEntryInConfig(cfg, name)
		if !ok {
			return fmt.Errorf("dots entry %q not found", name)
		}
		group := ensureGroupInConfig(cfg, groupName)
		changed := false
		for _, existing := range cfg.Groups {
			if existing == nil || existing.BaseName() == groupName {
				continue
			}
			if filterDotMemberships(existing, name) {
				changed = true
			}
		}
		for _, entry := range group.Dots {
			if entry.Name == name {
				if !changed {
					return errSkipSave
				}
				return nil
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
	groupName = compatibilityGroupName(groupName)
	return a.withConfig(func(cfg *config.RootConfig) error {
		if err := a.requireDotsEnabled(cfg); err != nil {
			return err
		}
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

func filterDotMemberships(group *config.GroupConfig, name string) bool {
	filtered := group.Dots[:0]
	changed := false
	for _, dot := range group.Dots {
		if dot.Name == name {
			changed = true
			continue
		}
		filtered = append(filtered, dot)
	}
	group.Dots = filtered
	return changed
}

// DotsList returns the symlink health for every dots entry across active groups.
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
		seen := make(map[string]bool)
		for _, child := range append(status.Children, status.ignoredChildren...) {
			if !child.Ignored {
				continue
			}
			rel := filepath.ToSlash(child.RelPath)
			if seen[rel] {
				continue
			}
			seen[rel] = true
			ignored = append(ignored, DotStatus{
				Name:       status.Name + "/" + rel,
				TargetPath: child.Path,
				ConfigPath: child.Path,
				Health:     healthForDotState(DotStateIgnored),
				State:      DotStateIgnored,
				Group:      status.Group,
				Counts:     child.Counts,
				IsDir:      child.IsDir,
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

// DotsPull runs git pull in the dots repo, then re-syncs all symlinks.
func (a *App) DotsPull(ctx context.Context) ([]dots.Op, error) {
	rootCfg, err := a.loadConfig()
	if err != nil {
		return nil, fmt.Errorf("dots pull: load config: %w", err)
	}
	if err := a.requireDotsEnabled(rootCfg); err != nil {
		return nil, err
	}
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
	rootCfg, err := a.loadConfig()
	if err != nil {
		return fmt.Errorf("dots push: load config: %w", err)
	}
	if err := a.requireDotsEnabled(rootCfg); err != nil {
		return err
	}
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

// DotsCommit stages and commits all changes in the dots repo without pushing.
// When message is empty the commit message is auto-generated from the git
// status of the repo (e.g. "dots: update nvim, zshrc").
func (a *App) DotsCommit(ctx context.Context, message string) error {
	rootCfg, err := a.loadConfig()
	if err != nil {
		return fmt.Errorf("dots commit: load config: %w", err)
	}
	if err := a.requireDotsEnabled(rootCfg); err != nil {
		return err
	}
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
			return fmt.Errorf("dots commit: %w", err)
		}
		message = dots.CommitMessageFromStatus(gitStatus)
	}
	return g.CommitAll(ctx, message)
}

// DisableDotsOptions controls the behaviour of DotsDisable.
type DisableDotsOptions struct {
	// ConflictOverwrite, when true, moves any real (non-managed) files at target
	// paths to trash and replaces them with the repo version. When false those
	// files are left in place and an OpUnlinkConflict is recorded.
	ConflictOverwrite bool
	// KeepExistingLocal, when true, leaves real non-managed local files in place
	// instead of recording unlink conflicts.
	KeepExistingLocal bool
	// RemoveLocal removes local real targets via trash, or unlinks local
	// symlinks, instead of materializing repo copies.
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
	m, _, _, err := a.buildDotsManager()
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
// Use-repo backs up the local target, moves it to trash, and restows the repo
// version.
// Use-local commits the current repo state first when the repo source exists,
// copies local content into the repo, then replaces the local target with the
// managed link.
func (a *App) DotsResolveConflict(ctx context.Context, name string, strategy DotsResolveStrategy) ([]dots.Op, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("dots resolve: entry name is required")
	}
	rootCfg, err := a.loadConfig()
	if err != nil {
		return nil, fmt.Errorf("dots resolve: load config: %w", err)
	}
	if err := a.requireDotsEnabled(rootCfg); err != nil {
		return nil, err
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
	if effective, _, ok := effectiveHostGroups(rootCfg, groups, currentMachineGroupName()); ok {
		groups = effective
	}
	entries := collectDots(rootCfg, groups)
	entries = resolveDotEntryPackagesForCurrentHost(entries)
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
	prep, err := prepareDotTargetForRestow(entry)
	if err != nil {
		return nil, err
	}
	if err := dots.Restow(ctx, executor.New(), stowPath, []string{entry.Package}, false); err != nil {
		wrapped := fmt.Errorf("dots resolve %q: use repo version relink: %w", entry.Name, err)
		if prep.backupPath != "" {
			if restoreErr := restoreDotTargetAfterFailedRestow(entry, prep); restoreErr != nil {
				return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: wrapped}},
					fmt.Errorf("%w (restore failed: %v)", wrapped, restoreErr)
			}
		}
		return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: wrapped}}, wrapped
	}
	return []dots.Op{{Kind: dots.OpRepair, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath}}, nil
}

func resolveDotUseLocal(ctx context.Context, repoPath, stowPath string, entry dots.ResolvedEntry) (ops []dots.Op, retErr error) {
	copySource, err := localDotCopySource(entry.TargetPath)
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
	replacement, err := replaceDotSourceFromLocal(copySource, entry.SourcePath, filepath.Join(stowPath, entry.Package), entry.Ignore)
	if err != nil {
		return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}},
			fmt.Errorf("dots resolve %q: replace repo source: %w", entry.Name, err)
	}
	prep, err := prepareDotTargetForRestow(entry)
	if err != nil {
		if rollbackErr := replacement.rollback(); rollbackErr != nil {
			return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}},
				fmt.Errorf("dots resolve %q: prepare local target: %w (rollback failed: %v)", entry.Name, err, rollbackErr)
		}
		return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: err}},
			fmt.Errorf("dots resolve %q: prepare local target: %w", entry.Name, err)
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
	if err := dots.Restow(ctx, executor.New(), stowPath, []string{entry.Package}, false); err != nil {
		wrapped := fmt.Errorf("dots resolve %q: use local version relink after copying local content: %w", entry.Name, err)
		if prep.backupPath != "" {
			if restoreErr := restoreDotTargetAfterFailedRestow(entry, prep); restoreErr != nil {
				return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: wrapped}},
					fmt.Errorf("%w (restore failed: %v)", wrapped, restoreErr)
			}
		}
		return []dots.Op{{Kind: dots.OpConflict, Entry: entry.Name, Src: entry.SourcePath, Dst: entry.TargetPath, Err: wrapped}}, wrapped
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
	if err := dots.RemoveLocalPathAfterBackup(targetPath, backupPath); err != nil {
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

type newerDotSourceFileReplacement struct {
	sourcePath string
	backupPath string
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

func replaceModifiedDotSourceFilesFromLocal(entry dots.ResolvedEntry, packageRoot string) (*dotSourceReplacement, error) {
	stagingParent := oldSourceStagingParent(entry.SourcePath, packageRoot)
	backupRoot, err := os.MkdirTemp(stagingParent, ".omni-newer-*")
	if err != nil {
		return nil, err
	}
	replacements := make([]newerDotSourceFileReplacement, 0)
	var addedSourcePaths []string
	rollback := func() error {
		var errs []string
		for i := len(addedSourcePaths) - 1; i >= 0; i-- {
			if err := os.RemoveAll(addedSourcePaths[i]); err != nil && !os.IsNotExist(err) {
				errs = append(errs, fmt.Sprintf("remove added source %q: %v", addedSourcePaths[i], err))
			}
		}
		for i := len(replacements) - 1; i >= 0; i-- {
			replacement := replacements[i]
			if err := copyRegularDotFileReplace(replacement.backupPath, replacement.sourcePath); err != nil {
				errs = append(errs, fmt.Sprintf("restore %q: %v", replacement.sourcePath, err))
			}
		}
		if err := os.RemoveAll(backupRoot); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Sprintf("remove newer source staging: %v", err))
		}
		if len(errs) > 0 {
			return fmt.Errorf("%s", strings.Join(errs, "; "))
		}
		return nil
	}
	addOne := func(sourcePath, targetPath string) error {
		if err := copyDotPath(targetPath, sourcePath, combinedDotIgnores(entry.Ignore)); err != nil {
			return fmt.Errorf("copy local addition %q into repo source %q: %w", targetPath, sourcePath, err)
		}
		addedSourcePaths = append(addedSourcePaths, sourcePath)
		return nil
	}
	replaceOne := func(sourcePath, targetPath string) error {
		rel, relErr := filepath.Rel(entry.SourcePath, sourcePath)
		if relErr != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
			rel = filepath.Base(sourcePath)
		}
		backupPath := filepath.Join(backupRoot, rel)
		if err := copyRegularDotFileReplace(sourcePath, backupPath); err != nil {
			return fmt.Errorf("backup repo source %q: %w", sourcePath, err)
		}
		replacements = append(replacements, newerDotSourceFileReplacement{
			sourcePath: sourcePath,
			backupPath: backupPath,
		})
		if err := copyRegularDotFileReplace(targetPath, sourcePath); err != nil {
			return fmt.Errorf("copy newer local %q into repo source %q: %w", targetPath, sourcePath, err)
		}
		return nil
	}
	replaced, err := walkNewerLocalDotFiles(entry, replaceOne)
	if err != nil {
		if rollbackErr := rollback(); rollbackErr != nil {
			return nil, fmt.Errorf("%w (rollback failed: %v)", err, rollbackErr)
		}
		return nil, err
	}
	added, err := walkLocalOnlyDotFiles(entry, addOne)
	if err != nil {
		if rollbackErr := rollback(); rollbackErr != nil {
			return nil, fmt.Errorf("%w (rollback failed: %v)", err, rollbackErr)
		}
		return nil, err
	}
	if !replaced && !added {
		if err := os.RemoveAll(backupRoot); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("remove newer source staging: %w", err)
		}
		return nil, fmt.Errorf("no local changes found for %q", entry.Name)
	}
	return &dotSourceReplacement{
		commit: func() error {
			return os.RemoveAll(backupRoot)
		},
		rollback: rollback,
	}, nil
}

func walkNewerLocalDotFiles(entry dots.ResolvedEntry, replaceOne func(sourcePath, targetPath string) error) (bool, error) {
	sourceInfo, sourceErr := os.Lstat(entry.SourcePath)
	if sourceErr != nil {
		return false, sourceErr
	}
	targetInfo, targetErr := os.Lstat(entry.TargetPath)
	if targetErr != nil {
		return false, targetErr
	}
	if sourceInfo.Mode().IsRegular() {
		if !localFileIsNewer(sourceInfo, targetInfo) {
			return false, nil
		}
		if replaceOne == nil {
			return true, nil
		}
		return true, replaceOne(entry.SourcePath, entry.TargetPath)
	}
	if !sourceInfo.IsDir() || sourceInfo.Mode()&os.ModeSymlink != 0 || !targetInfo.IsDir() || targetInfo.Mode()&os.ModeSymlink != 0 {
		return false, nil
	}
	ignores := combinedDotIgnores(entry.Ignore)
	found := false
	err := filepath.WalkDir(entry.SourcePath, func(path string, d os.DirEntry, err error) error {
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
			if sourceInfo.IsDir() && sourceInfo.Mode()&os.ModeSymlink == 0 {
				return filepath.SkipDir
			}
			return nil
		}
		if targetErr != nil {
			return targetErr
		}
		if sameResolvedPath(targetPath, path) {
			return nil
		}
		if sourceInfo.IsDir() && sourceInfo.Mode()&os.ModeSymlink == 0 {
			if targetInfo.IsDir() && targetInfo.Mode()&os.ModeSymlink == 0 {
				return nil
			}
			return fmt.Errorf("local target %q conflicts with repo directory %q", targetPath, path)
		}
		if targetInfo.Mode()&os.ModeSymlink != 0 {
			target, readErr := os.Readlink(targetPath)
			if readErr != nil {
				return fmt.Errorf("read local link %q: %w", targetPath, readErr)
			}
			if !filepath.IsAbs(target) {
				target = filepath.Clean(filepath.Join(filepath.Dir(targetPath), target))
			}
			if pathExists(target) {
				return fmt.Errorf("local target %q links outside managed source", targetPath)
			}
			return nil
		}
		if localFileIsNewer(sourceInfo, targetInfo) {
			found = true
			if replaceOne == nil {
				return nil
			}
			return replaceOne(path, targetPath)
		}
		return fmt.Errorf("local target %q is not newer than repo source %q", targetPath, path)
	})
	if err != nil {
		return false, err
	}
	return found, nil
}

func walkLocalOnlyDotFiles(entry dots.ResolvedEntry, addOne func(sourcePath, targetPath string) error) (bool, error) {
	sourceInfo, sourceErr := os.Lstat(entry.SourcePath)
	if sourceErr != nil {
		return false, sourceErr
	}
	targetInfo, targetErr := os.Lstat(entry.TargetPath)
	if targetErr != nil {
		return false, targetErr
	}
	if !sourceInfo.IsDir() || sourceInfo.Mode()&os.ModeSymlink != 0 || !targetInfo.IsDir() || targetInfo.Mode()&os.ModeSymlink != 0 {
		return false, nil
	}
	ignores := combinedDotIgnores(entry.Ignore)
	found := false
	err := filepath.WalkDir(entry.TargetPath, func(path string, d os.DirEntry, err error) error {
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
		if shouldIgnoreDotPath(entry.SourcePath, rel, d.Name(), ignores) {
			if d.IsDir() {
				if ignoredDotDirHasIncludedDescendant(entry.SourcePath, rel, ignores) {
					return nil
				}
				return filepath.SkipDir
			}
			return nil
		}
		targetInfo, infoErr := os.Lstat(path)
		if infoErr != nil {
			return infoErr
		}
		sourcePath := filepath.Join(entry.SourcePath, rel)
		sourceInfo, sourceErr := os.Lstat(sourcePath)
		if sourceErr == nil {
			if sameResolvedPath(path, sourcePath) {
				if targetInfo.IsDir() && targetInfo.Mode()&os.ModeSymlink == 0 {
					return filepath.SkipDir
				}
				return nil
			}
			if sourceInfo.IsDir() && sourceInfo.Mode()&os.ModeSymlink == 0 && targetInfo.IsDir() && targetInfo.Mode()&os.ModeSymlink == 0 {
				return nil
			}
			return nil
		}
		if !os.IsNotExist(sourceErr) {
			return sourceErr
		}
		if targetInfo.IsDir() && targetInfo.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		if !isManagedDotFile(targetInfo.Mode()) {
			return nil
		}
		found = true
		if addOne == nil {
			return nil
		}
		return addOne(sourcePath, path)
	})
	if err != nil {
		return false, err
	}
	return found, nil
}

func copyRegularDotFileReplace(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%q is not a regular file", src)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		cleanup()
		return err
	}
	if _, err := io.Copy(tmp, in); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Chtimes(tmpPath, info.ModTime(), info.ModTime()); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
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
	if err := dots.RemoveLocalPathAfterBackup(originalPath, backupPath); err != nil {
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
			if err := gt.CommitAll(ctx, "dots: pre-sync "+entry.Name); err != nil {
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
	if inspectDotLocal(entry).kind != dotLocalWrongLink {
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
			return fmt.Errorf("resolve dot copy symlink %q: %w", src, err)
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
// variantMap marks entries that are using a host-specific package.
func entryHealth(m *dots.Manager, groupMap map[string]string, variantMap map[string]bool) []DotStatus {
	statuses := make([]DotStatus, 0, len(m.Entries))
	for _, e := range m.Entries {
		state, actions := classifyDotEntry(e)
		contentRoot := dotStatusContentRoot(e)
		ignoreRoot := dotIgnoreRoot(e.SourcePath, e.TargetPath, contentRoot)
		counts := dotFileCountsUnion(e.SourcePath, e.TargetPath, contentRoot, "", ignoreRoot, e.Ignore, state)
		fileCount := counts.Managed()
		children := directDotChildren(e.SourcePath, e.TargetPath, contentRoot, ignoreRoot, e.Ignore, state)
		ignoredChildren := ignoredDotChildren(e.SourcePath, e.TargetPath, contentRoot, ignoreRoot, e.Ignore, state)
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
	_ = filepath.WalkDir(path, func(path string, d os.DirEntry, err error) error {
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
	})
}

func collectIgnoredDotFileCountRels(root, path string, ignored map[string]bool) {
	_ = filepath.WalkDir(path, func(path string, d os.DirEntry, err error) error {
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
	})
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
	default:
		return DotStateConflict
	}
}

func isManagedDotFile(mode os.FileMode) bool {
	return mode.IsRegular() || mode&os.ModeSymlink != 0
}

// DotsAddIgnorePattern appends a per-entry glob pattern to the named dots entry
// in config. The pattern is validated before saving.
// Adding a pattern that is already present is a no-op.
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
