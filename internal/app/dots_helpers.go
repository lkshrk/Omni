package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/dots"
	"github.com/lkshrk/omni/internal/executor"
)

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

func (a *App) DotsStowInstalled(ctx context.Context) bool {
	return dots.CheckInstalled(ctx, a.newExecutor())
}

// InstallDotsStow — Callers own user consent: this may run the host package manager and can require privileges.
func (a *App) InstallDotsStow(ctx context.Context) error {
	if a.DotsStowInstalled(ctx) {
		return nil
	}
	settings, _ := a.LoadSettings()
	providerName, err := a.resolveProvider(ctx, SystemInstallPriority(settings))
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

// Applies host-specific settings overrides via EffectiveSettings.
func (a *App) dotsRepoPath() string {
	cfg, err := a.loadConfig()
	if err != nil {
		return ""
	}
	return a.effectiveSettings(cfg).DotsRepo
}

func newGitForRepo(repoPath string, exec executor.Executor) *dots.Git {
	return dots.NewGit(repoPath, exec)
}

func (a *App) buildDotsManager() (*dots.Engine, map[string]string, map[string]bool, error) {
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
	mgr, err := dots.NewEngine(stowPath, entries)
	return mgr, groupMap, variantMap, err
}

// Expands ~ and env vars and requires the result to be absolute.
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

// Mirrors the home directory structure: ~/.config/nvim becomes <repo>/dotfiles/nvim/.config/nvim.
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

	src, err := dots.LocalDotCopySource(targetPath)
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
	if err := dots.CopyDotPath(src, dst, dots.CombinedIgnores(entry.Ignore)); err != nil {
		if removeErr := os.RemoveAll(cleanupPath); removeErr != nil {
			return dotVariantSourceResult{}, fmt.Errorf("seed package source: %w (cleanup failed: %v)", err, removeErr)
		}
		return dotVariantSourceResult{}, fmt.Errorf("seed package source: %w", err)
	}
	return dotVariantSourceResult{CleanupPath: cleanupPath, Created: true}, nil
}

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

// Strips a leading dot for hidden files, so ".zshrc" becomes "zshrc".
func inferName(abs string) string {
	base := filepath.Base(abs)
	return strings.TrimPrefix(base, ".")
}

// groupMap maps entry name to group base name and may be nil.
