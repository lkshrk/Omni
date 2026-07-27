package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/config"
)

var errSkillsUnconfigured = errors.New("skills service is not configured")

func (s *Skills) packagesRoot() string {
	return filepath.Join(s.dataDir, "packages")
}

// PruneStoreDebris — Settles the staging and backup entries an interrupted install, adopt or remove left behind, restoring a backup whose destination never came back.
func (s *Skills) PruneStoreDebris(dryRun bool) ([]string, error) {
	if s == nil || s.registry == nil {
		return nil, errSkillsUnconfigured
	}
	lock, err := s.lockStoreForWrite(dryRun)
	if err != nil {
		return nil, err
	}
	defer lock.release()
	root := s.packagesRoot()
	entries, err := readDirIfExists(root)
	if err != nil {
		return nil, fmt.Errorf("reading skills package root %s: %w", root, err)
	}
	var found []storeDebris
	for _, entry := range entries {
		if dest, ok := packageRootDebrisName(entry.Name()); ok {
			found = append(found, classifyStoreDebris(root, entry.Name(), dest))
		}
	}
	for _, dir := range s.allTargetSkillDirs() {
		entries, err := readDirIfExists(dir)
		if err != nil {
			return nil, fmt.Errorf("reading target skill directory %s: %w", dir, err)
		}
		for _, entry := range entries {
			if dest, ok := targetDebrisName(entry.Name()); ok {
				found = append(found, classifyStoreDebris(dir, entry.Name(), dest))
			}
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].path < found[j].path })
	if dryRun {
		lines := make([]string, 0, len(found))
		for _, item := range found {
			lines = append(lines, item.line(true))
		}
		return lines, nil
	}
	return applyStoreDebris(found)
}

// A restore path means the debris is currently the only copy of its content; a retain reason means omni could not tell and left it alone.
type storeDebris struct {
	path    string
	restore string
	retain  string
}

// Debris is not uniformly removable — a backup whose destination is gone is restored instead — so the verb varies per item while sharing doctor's removed/would-remove vocabulary.
func (d storeDebris) line(dryRun bool) string {
	const noun = "leftover skill store artifact"
	switch {
	case d.retain != "":
		return fmt.Sprintf("kept %s %s: %s", noun, d.path, d.retain)
	case d.restore != "":
		if dryRun {
			return fmt.Sprintf("would restore %s %s to %s", noun, d.path, d.restore)
		}
		return fmt.Sprintf("restored %s %s to %s", noun, d.path, d.restore)
	default:
		if dryRun {
			return fmt.Sprintf("would remove %s %s", noun, d.path)
		}
		return fmt.Sprintf("removed %s %s", noun, d.path)
	}
}

func classifyStoreDebris(dir, name, dest string) storeDebris {
	path := filepath.Join(dir, name)
	if dest == "" {
		return storeDebris{path: path}
	}
	destPath := filepath.Join(dir, dest)
	_, err := os.Lstat(destPath)
	switch {
	case err == nil:
		return storeDebris{path: path}
	case os.IsNotExist(err):
		return storeDebris{path: path, restore: destPath}
	default:
		return storeDebris{path: path, retain: fmt.Sprintf("inspecting %s: %v", destPath, err)}
	}
}

func applyStoreDebris(items []storeDebris) ([]string, error) {
	lines := make([]string, 0, len(items))
	var errs []error
	for _, item := range items {
		switch {
		// Retained debris is reported and deliberately left on disk.
		case item.retain != "":
		case item.restore != "":
			if err := os.Rename(item.path, item.restore); err != nil {
				errs = append(errs, fmt.Errorf("restoring %s to %s: %w", item.path, item.restore, err))
				continue
			}
		default:
			if err := os.RemoveAll(item.path); err != nil && !os.IsNotExist(err) {
				errs = append(errs, fmt.Errorf("removing %s: %w", item.path, err))
				continue
			}
		}
		lines = append(lines, item.line(false))
	}
	return lines, errors.Join(errs...)
}

// A live package directory is bare 64-hex, so none of these debris shapes can name one; an empty destination is never restorable.
func packageRootDebrisName(name string) (string, bool) {
	if base := strings.TrimSuffix(name, ".previous"); base != name {
		return base, packageDirName(base)
	}
	// A trashed package is a half-deleted tree the user already asked to drop, so it is never restored.
	if base := strings.TrimSuffix(name, ".removing"); base != name {
		return "", packageDirName(base)
	}
	if strings.HasPrefix(name, ".install-") || strings.HasPrefix(name, ".adopt-") {
		return "", true
	}
	return "", false
}

// Matches only the shapes installPackageLink and stageDirBackup generate, so a user directory that merely spells ".omni-adopt-" is left alone.
func targetDebrisName(name string) (string, bool) {
	if base := strings.TrimSuffix(name, ".omni-tmp"); base != name {
		return "", validSkillName(base)
	}
	if !strings.HasPrefix(name, ".") {
		return "", false
	}
	for _, marker := range []string{".omni-legacy-", ".omni-adopt-"} {
		skill, random, ok := strings.Cut(name[1:], marker)
		if !ok || !validSkillName(skill) || !numericSuffix(random) {
			continue
		}
		return skill, true
	}
	return "", false
}

// os.MkdirTemp appends a decimal random number, which no skill name can look like.
func numericSuffix(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

// PruneDanglingLinks — Ownership uses the same test install applies, so an entry another tool owns is never touched.
func (s *Skills) PruneDanglingLinks(dryRun bool) ([]string, error) {
	if s == nil || s.registry == nil {
		return nil, errSkillsUnconfigured
	}
	lock, err := s.lockStoreForWrite(dryRun)
	if err != nil {
		return nil, err
	}
	defer lock.release()
	root := s.packagesRoot()
	var found []string
	for _, dir := range s.allTargetSkillDirs() {
		entries, err := readDirIfExists(dir)
		if err != nil {
			return nil, fmt.Errorf("reading target skill directory %s: %w", dir, err)
		}
		for _, entry := range entries {
			path := filepath.Join(dir, entry.Name())
			if !ownedLink(path, root) {
				continue
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				continue
			}
			found = append(found, path)
		}
	}
	sort.Strings(found)
	if dryRun {
		return found, nil
	}
	return removePaths(found, os.Remove)
}

// PruneOrphanedPackages — Raw manifest locators are normalized here so the compared identity matches the one install would compute.
func (s *Skills) PruneOrphanedPackages(sources []string, dryRun bool) ([]string, error) {
	if s == nil || s.registry == nil {
		return nil, errSkillsUnconfigured
	}
	lock, err := s.lockStoreForWrite(dryRun)
	if err != nil {
		return nil, err
	}
	defer lock.release()
	declared := make(map[string]bool, len(sources))
	for _, source := range sources {
		if parsed, err := ParseSkillSource(source); err == nil {
			source = parsed.Source
		}
		declared[s.packageDir(source)] = true
	}
	root := s.packagesRoot()
	entries, err := readDirIfExists(root)
	if err != nil {
		return nil, fmt.Errorf("reading skills package root %s: %w", root, err)
	}
	var found []string
	for _, entry := range entries {
		if !entry.IsDir() || !packageDirName(entry.Name()) {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		if declared[dir] {
			continue
		}
		referenced, err := s.packageReferenced(dir)
		if err != nil {
			return nil, err
		}
		if !referenced {
			found = append(found, dir)
		}
	}
	sort.Strings(found)
	if dryRun {
		return found, nil
	}
	return removePaths(found, os.RemoveAll)
}

// A directory that did not arrive through packageDir is left alone.
func packageDirName(name string) bool {
	if len(name) != 64 {
		return false
	}
	for _, char := range name {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

// RebuildMetadata — The record only caches what the installed tree already says, so rebuilding it changes no host state.
func (s *Skills) RebuildMetadata(ctx context.Context, pkgs []config.SkillPackage, dryRun bool) ([]string, error) {
	if s == nil || s.registry == nil {
		return nil, errSkillsUnconfigured
	}
	lock, err := s.lockStoreForWrite(dryRun)
	if err != nil {
		return nil, err
	}
	defer lock.release()
	var rebuilt []string
	var errs []error
	seen := make(map[string]bool, len(pkgs))
	for _, pkg := range pkgs {
		pkg, err := normalizeSkillPackage(pkg)
		if err != nil {
			continue
		}
		packageDir := s.packageDir(pkg.Source)
		if seen[packageDir] {
			continue
		}
		seen[packageDir] = true
		names, err := skillDirNames(packageDir)
		if err != nil || len(names) == 0 {
			continue
		}
		if s.state == nil {
			// Installed store content proves a cache DB once existed, so a nil state must be reported rather than skipped.
			errs = append(errs, fmt.Errorf(
				"local state unavailable; cannot check install metadata for %s", pkg.Source))
			continue
		}
		if s.metadataReadable(ctx, packageDir) {
			continue
		}
		if dryRun {
			rebuilt = append(rebuilt, pkg.Source)
			continue
		}
		if err := s.storeMetadata(ctx, pkg, packageDir, names, "", "", skillProbeNone); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", pkg.Source, err))
			continue
		}
		rebuilt = append(rebuilt, pkg.Source)
	}
	sort.Strings(rebuilt)
	return rebuilt, errors.Join(errs...)
}

func (s *Skills) metadataReadable(ctx context.Context, packageDir string) bool {
	raw, err := s.state.GetState(ctx, metadataStateKey(packageDir))
	if err != nil {
		return false
	}
	var metadata skillMetadata
	return json.Unmarshal([]byte(raw), &metadata) == nil
}

// ForeignSkillEntries — Nothing is mutated, because a foreign entry belongs to whoever installed it.
func (s *Skills) ForeignSkillEntries(pkgs []config.SkillPackage) (map[string][]string, error) {
	if s == nil || s.registry == nil {
		return nil, errSkillsUnconfigured
	}
	accounted := s.accountedSkillNames(pkgs)
	root := s.packagesRoot()
	out := make(map[string][]string)
	for _, target := range s.registry.Installed(s.home) {
		var found []string
		for _, dir := range target.skillDirs(s.home) {
			entries, err := readDirIfExists(dir)
			if err != nil {
				return nil, fmt.Errorf("reading target skill directory %s: %w", dir, err)
			}
			for _, entry := range entries {
				name := entry.Name()
				_, debris := targetDebrisName(name)
				if accounted[name] || debris || strings.HasPrefix(name, ".") {
					continue
				}
				if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
					continue
				}
				path := filepath.Join(dir, name)
				if ownedLink(path, root) {
					continue
				}
				found = append(found, path)
			}
		}
		if len(found) > 0 {
			sort.Strings(found)
			out[target.ID] = uniqueStrings(found)
		}
	}
	return out, nil
}

// Excluding names another surface already speaks for keeps the foreign scan to entries nothing else mentions.
func (s *Skills) accountedSkillNames(pkgs []config.SkillPackage) map[string]bool {
	names := make(map[string]bool)
	for _, pkg := range pkgs {
		pkg, err := normalizeSkillPackage(pkg)
		if err != nil {
			continue
		}
		for _, name := range pkg.Skills {
			names[name] = true
		}
		installed, err := skillDirNames(s.packageDir(pkg.Source))
		if err != nil {
			continue
		}
		for _, name := range installed {
			names[name] = true
		}
	}
	if lock, err := config.LoadSkillLock(config.SkillLockPath(s.home)); err == nil {
		for name := range lock.Skills {
			names[name] = true
		}
	}
	return names
}

func (s *Skills) allTargetSkillDirs() []string {
	return uniqueTargetSkillDirs(s.registry.All(), s.home)
}

func removePaths(paths []string, remove func(string) error) ([]string, error) {
	removed := make([]string, 0, len(paths))
	var errs []error
	for _, path := range paths {
		if err := remove(path); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("removing %s: %w", path, err))
			continue
		}
		removed = append(removed, path)
	}
	return removed, errors.Join(errs...)
}
