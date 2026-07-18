package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/dots"
	"github.com/lkshrk/omni/internal/executor"
)

// DotsAdd moves the file/dir at path into the dots repo and links it back via
// stow. A backup is made under ~/dotfiles.bkp before any mutation, then the
// local original is moved to trash. path must exist on disk.
func (a *App) DotsAdd(ctx context.Context, path string, opts DotsAddOptions) (ops []dots.Op, err error) {
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
	historyEntry := ""
	defer func() {
		a.recordDotsHistoryResult(ctx, "add", historyEntry, repoPath, ops, err, false)
		a.refreshDotsStateAfterSuccess(ctx, &err, false)
	}()

	abs, err := expandAndStat(path)
	if err != nil {
		return nil, fmt.Errorf("dots add: %w", err)
	}

	name := opts.Name
	if name == "" {
		name = inferName(abs)
	}
	historyEntry = name
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

	backupPath, err := dots.BackupLocalPathWithExecutor(ctx, a.newExecutor(), abs)
	if err != nil {
		return nil, fmt.Errorf("dots add: backup: %w", err)
	}

	// Copy filtered content into the repo package tree, move the local original
	// to trash, then stow links it back. The backup remains a full safety copy.
	if err := os.MkdirAll(filepath.Dir(pkgDst), 0o755); err != nil {
		return nil, fmt.Errorf("dots add: create package dir: %w", err)
	}
	if err := dots.CopyDotPath(abs, pkgDst, dots.CombinedIgnores(entry.Ignore)); err != nil {
		if cleanupErr := cleanupPackage(); cleanupErr != nil {
			return nil, fmt.Errorf("dots add: copy to repo: %w (cleanup failed: %v)", err, cleanupErr)
		}
		return nil, fmt.Errorf("dots add: copy to repo: %w", err)
	}
	if err := dots.RemoveLocalPathAfterBackupWithExecutor(ctx, a.newExecutor(), abs, backupPath); err != nil {
		if cleanupErr := cleanupPackage(); cleanupErr != nil {
			return nil, fmt.Errorf("dots add: remove local target: %w (cleanup failed: %v)", err, cleanupErr)
		}
		return nil, fmt.Errorf("dots add: remove local target: %w", err)
	}
	if err := dots.Restow(ctx, a.newExecutor(), stowPath, []string{pkgName}, false); err != nil {
		if rollbackErr := rollbackDotsAdd(ctx, a.newExecutor(), abs, pkgDst, backupPath); rollbackErr != nil {
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
		if rollbackErr := rollbackDotsAdd(ctx, a.newExecutor(), abs, pkgDst, backupPath); rollbackErr != nil {
			return nil, fmt.Errorf("dots add: save config: %w (rollback failed: %v)", err, rollbackErr)
		}
		return nil, fmt.Errorf("dots add: save config: %w", err)
	}

	if g := newGitForRepo(repoPath, a.newExecutor()); g.IsRepo() {
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

	return []dots.Op{dots.LstatEntryOp(dots.ResolvedEntry{
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

func (a *App) DotsHasActiveHostVariant(name string) (bool, error) {
	variants, err := a.DotsListVariants(name)
	if err != nil {
		return false, err
	}
	for _, variant := range variants {
		if variant.Active && !variant.Default {
			return true, nil
		}
	}
	return false, nil
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
		if gt := newGitForRepo(repoPath, a.newExecutor()); gt.IsRepo() {
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
		if gt := newGitForRepo(repoPath, a.newExecutor()); gt.IsRepo() {
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
func rollbackDotsAdd(ctx context.Context, exec executor.Executor, targetPath, packagePath, backupPath string) error {
	var errs []string
	if err := dots.RemoveLocalPathAfterBackupWithExecutor(ctx, exec, targetPath, backupPath); err != nil {
		errs = append(errs, fmt.Sprintf("remove target link: %v", err))
	}
	if backupPath != "" {
		if err := dots.RestoreBackupPath(backupPath, targetPath); err != nil {
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
