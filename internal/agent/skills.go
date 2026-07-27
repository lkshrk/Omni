package agent

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lkshrk/omni/internal/config"
)

const skillAcquireTimeout = 10 * time.Minute

// NotInstalledError — Callers composing an uninstall with another step must tell this apart from a real failure.
type NotInstalledError struct {
	Source string
}

func (e *NotInstalledError) Error() string {
	return fmt.Sprintf("skill package %q is not installed", e.Source)
}

type LocalState interface {
	GetState(context.Context, string) (string, error)
	SetState(context.Context, string, string) error
}

type Skills struct {
	registry   *Registry
	home       string
	dataDir    string
	sourceBase string
	client     *http.Client
	state      LocalState
}

type SkillInventory struct {
	Skills    []string
	Installed bool
	Updated   string
	PerTarget map[string]bool
	// Targets whose entry another tool took over; a target is never both installed and drifted.
	PerTargetDrifted map[string]bool
	// Targets whose entry points at another package in Omni's managed store.
	PerTargetShadowed map[string]bool
	Description       string
	// Read from local state so a render never probes; nil means unknown, and CheckOutdated refreshes it.
	Outdated          *bool
	OutdatedCheckedAt time.Time
	// Inventory skips unknown target IDs so one bad manifest entry cannot blank a read-only view; mutating calls still reject them.
	UnknownTargets []string
}

func NewSkills(registry *Registry, home, dataDir string, client *http.Client, state LocalState, sourceBase string) *Skills {
	if client == nil {
		client = defaultSkillsClient()
	}
	return &Skills{registry: registry, home: home, dataDir: dataDir, sourceBase: sourceBase, client: client, state: state}
}

func DefaultSkillsDataDir(home string) string {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "omni", "skills")
	}
	return filepath.Join(home, ".local", "share", "omni", "skills")
}

// Install — Stages and validates before atomically replacing shared content and target links; warnings are advisory even on success.
func (s *Skills) Install(ctx context.Context, pkg config.SkillPackage, targetIDs []string) ([]string, error) {
	return s.install(ctx, pkg, targetIDs, false, false)
}

// Refresh — Reacquires even when a valid canonical installation could have been reused.
func (s *Skills) Refresh(ctx context.Context, pkg config.SkillPackage, targetIDs []string) ([]string, error) {
	return s.install(ctx, pkg, targetIDs, true, false)
}

// Sync — Converge flows must not withhold a package from healthy targets over one contested entry, so Sync degrades to drift where Install refuses.
func (s *Skills) Sync(ctx context.Context, pkg config.SkillPackage, targetIDs []string) ([]string, error) {
	return s.install(ctx, pkg, targetIDs, false, true)
}

// Upgrade reacquires a package like Refresh, with Sync's drift tolerance.
func (s *Skills) Upgrade(ctx context.Context, pkg config.SkillPackage, targetIDs []string) ([]string, error) {
	return s.install(ctx, pkg, targetIDs, true, true)
}

// Adopt — Only the explicit import flow adopts; install, restore and update never mutate legacy-managed directories.
func (s *Skills) Adopt(ctx context.Context, pkg config.SkillPackage, targetIDs []string) (bool, []string, error) {
	if s == nil || s.registry == nil {
		return false, nil, fmt.Errorf("skills service is not configured")
	}
	pkg, err := normalizeSkillPackage(pkg)
	if err != nil {
		return false, nil, err
	}
	targets, err := s.targets(targetIDs)
	if err != nil {
		return false, nil, err
	}
	if len(targets) == 0 {
		return false, nil, fmt.Errorf("no installed agent targets selected")
	}
	lock, err := s.lockStore()
	if err != nil {
		return false, nil, err
	}
	defer lock.release()
	return s.adoptLegacyInstalled(ctx, pkg, targets)
}

func (s *Skills) install(ctx context.Context, pkg config.SkillPackage, targetIDs []string, force, tolerateDrift bool) ([]string, error) {
	if s == nil || s.registry == nil {
		return nil, fmt.Errorf("skills service is not configured")
	}
	pkg, err := normalizeSkillPackage(pkg)
	if err != nil {
		return nil, err
	}
	targets, err := s.targets(targetIDs)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no installed agent targets selected")
	}
	// Held across acquisition too: the staging tree lives under the packages root, where a concurrent store fix would prune it as debris.
	lock, err := s.lockStore()
	if err != nil {
		return nil, err
	}
	defer lock.release()
	// A nil id list is detection-derived: an agent whose CLI is momentarily off $PATH must not read as a deselection.
	return s.installLocked(ctx, pkg, targets, force, tolerateDrift, nil, targetIDs != nil)
}

func (s *Skills) installLocked(
	ctx context.Context,
	pkg config.SkillPackage,
	targets []Target,
	force, tolerateDrift bool,
	displace *displaceScope,
	prune bool,
) ([]string, error) {
	warnings := s.legacyInstallWarnings(pkg)
	if !force {
		reused, reuseWarnings, err := s.reuseInstalled(ctx, pkg, targets, tolerateDrift, displace, prune)
		warnings = append(warnings, reuseWarnings...)
		if err != nil {
			return warnings, err
		}
		if reused {
			return warnings, nil
		}
	}

	root := s.packagesRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return warnings, fmt.Errorf("creating skills data dir: %w", err)
	}
	stage, err := os.MkdirTemp(root, ".install-")
	if err != nil {
		return warnings, fmt.Errorf("creating skills staging dir: %w", err)
	}
	// Best-effort cleanup of the staging tree once the install settled.
	defer func() { _ = os.RemoveAll(stage) }()

	acquired := filepath.Join(stage, "source")
	// Probed before acquiring: recording the newer identity afterwards would hide a source that moved mid-install.
	sourceProbe, sourceProbeKind := s.probeSource(ctx, pkg)
	if err := s.acquireBounded(ctx, pkg, acquired); err != nil {
		return warnings, err
	}
	sourceFingerprint := fingerprintSource(ctx, acquired)
	discovered, discoverWarnings, err := discoverSkills(acquired)
	warnings = append(warnings, discoverWarnings...)
	if err != nil {
		return warnings, err
	}
	selected, err := selectSkills(discovered, pkg.Skills)
	if err != nil {
		return warnings, err
	}

	candidate := filepath.Join(stage, "package")
	for name, src := range selected {
		if err := copyTree(src, filepath.Join(candidate, "skills", name), src); err != nil {
			return warnings, fmt.Errorf("staging skill %s: %w", name, err)
		}
	}
	if err := writePackageIntent(candidate, pkg); err != nil {
		return warnings, err
	}

	packageDir := s.packageDir(pkg.Source)
	oldNames, err := skillDirNames(packageDir)
	if err != nil && !os.IsNotExist(err) {
		return warnings, fmt.Errorf("reading installed package %s: %w", packageDir, err)
	}
	foreign, err := s.validateLinkChanges(packageDir, candidate, targets, mapKeys(selected), displace, tolerateDrift)
	if err != nil {
		return warnings, err
	}

	backup := packageDir + ".previous"
	// Best-effort: a leftover backup from an interrupted install is not fatal.
	_ = os.RemoveAll(backup)
	hadOld := false
	if _, err := os.Stat(packageDir); err == nil {
		if err := os.Rename(packageDir, backup); err != nil {
			return warnings, fmt.Errorf("backing up installed package: %w", err)
		}
		hadOld = true
	} else if !os.IsNotExist(err) {
		return warnings, fmt.Errorf("inspecting installed package %s: %w", packageDir, err)
	}
	if err := os.Rename(candidate, packageDir); err != nil {
		if hadOld {
			if renameErr := os.Rename(backup, packageDir); renameErr != nil {
				warnings = append(warnings, fmt.Sprintf(
					"rollback could not restore previous package from %s: %v", backup, renameErr))
			}
		}
		return warnings, fmt.Errorf("installing staged package: %w", err)
	}

	snapshots, linkWarnings, err := s.applyLinkChanges(packageDir, targets, oldNames, mapKeys(selected), displace, foreign, prune)
	warnings = append(warnings, linkWarnings...)
	if err != nil {
		warnings = append(warnings, restoreLinks(snapshots)...)
		if removeErr := os.RemoveAll(packageDir); removeErr != nil {
			warnings = append(warnings, fmt.Sprintf("rollback left %s in place: %v", packageDir, removeErr))
		}
		if hadOld {
			if renameErr := os.Rename(backup, packageDir); renameErr != nil {
				warnings = append(warnings, fmt.Sprintf(
					"rollback could not restore previous package from %s: %v", backup, renameErr))
			}
		}
		return warnings, err
	}
	warnings = append(warnings, removeLinkBackups(snapshots, "unmanaged copy")...)
	if err := os.RemoveAll(backup); err != nil {
		warnings = append(warnings, fmt.Sprintf("superseded package copy remains at %s: %v", backup, err))
	}
	if err := s.storeMetadata(ctx, pkg, packageDir, mapKeys(selected), sourceFingerprint, sourceProbe, sourceProbeKind); err != nil {
		warnings = append(warnings, fmt.Sprintf("skill package %s: %v", pkg.Source, err))
	}
	return warnings, nil
}

func (s *Skills) reuseInstalled(
	ctx context.Context,
	pkg config.SkillPackage,
	targets []Target,
	tolerateDrift bool,
	displace *displaceScope,
	prune bool,
) (bool, []string, error) {
	packageDir := s.packageDir(pkg.Source)
	if !s.installedMetadataMatches(ctx, pkg, packageDir) {
		return false, nil, nil
	}
	names, err := skillDirNames(packageDir)
	if os.IsNotExist(err) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, fmt.Errorf("reading installed package %s: %w", packageDir, err)
	}
	if len(names) == 0 {
		return false, nil, nil
	}
	discovered := make(map[string]string, len(names))
	for _, name := range names {
		dir := filepath.Join(packageDir, "skills", name)
		manifestName, err := validateSkillDir(dir)
		if err != nil || manifestName != name {
			return false, nil, nil
		}
		discovered[name] = dir
	}
	selected, err := selectSkills(discovered, pkg.Skills)
	if err != nil {
		return false, nil, nil
	}
	selectedNames := mapKeys(selected)
	if len(selectedNames) != len(names) {
		return false, nil, nil
	}
	foreign, err := s.validateLinkChanges(packageDir, packageDir, targets, selectedNames, displace, tolerateDrift)
	if err != nil {
		return false, nil, err
	}
	snapshots, warnings, err := s.applyLinkChanges(packageDir, targets, names, selectedNames, displace, foreign, prune)
	if err != nil {
		warnings = append(warnings, restoreLinks(snapshots)...)
		return false, warnings, err
	}
	warnings = append(warnings, removeLinkBackups(snapshots, "unmanaged copy")...)
	if err := s.storeMetadata(ctx, pkg, packageDir, selectedNames, "", "", skillProbeNone); err != nil {
		warnings = append(warnings, fmt.Sprintf("skill package %s: %v", pkg.Source, err))
	}
	return true, warnings, nil
}

// SourceProbe is the source identity recorded at install time and SourceProbeKind the value space it came from; Outdated holds the last probe against it, nil meaning unknown.
type skillMetadata struct {
	Source            string    `json:"source"`
	Ref               string    `json:"ref,omitempty"`
	Skills            []string  `json:"skills"`
	Selectors         []string  `json:"selectors,omitempty"`
	AllSkills         bool      `json:"all_skills"`
	SourceFingerprint string    `json:"source_fingerprint"`
	ContentHash       string    `json:"content_hash"`
	HashVersion       string    `json:"hash_version,omitempty"`
	CheckedAt         time.Time `json:"checked_at"`
	SourceProbe       string    `json:"source_probe,omitempty"`
	SourceProbeKind   string    `json:"source_probe_kind,omitempty"`
	Outdated          *bool     `json:"outdated,omitempty"`
	OutdatedCheckedAt time.Time `json:"outdated_checked_at,omitempty"`
}

func metadataStateKey(packageDir string) string {
	return "agent.skills." + filepath.Base(packageDir)
}

func (s *Skills) installedMetadataMatches(ctx context.Context, pkg config.SkillPackage, packageDir string) bool {
	if s.state == nil {
		return installedIntentMatches(packageDir, pkg)
	}
	raw, err := s.state.GetState(ctx, metadataStateKey(packageDir))
	if err != nil {
		return installedIntentMatches(packageDir, pkg)
	}
	var metadata skillMetadata
	if json.Unmarshal([]byte(raw), &metadata) != nil {
		return installedIntentMatches(packageDir, pkg)
	}
	if metadata.Source != pkg.Source || metadata.Ref != pkg.Ref ||
		metadata.AllSkills != (len(pkg.Skills) == 0) ||
		(!metadata.AllSkills && !sameStringSet(metadata.Selectors, pkg.Skills)) {
		return false
	}
	// A hash written by an older encoding says nothing about the tree, so it reads as absent rather than as a mismatch.
	if metadata.ContentHash == "" || metadata.HashVersion != hashTreeVersion {
		return pkg.Ref == ""
	}
	contentHash, err := hashTree(packageDir)
	return err == nil && contentHash == metadata.ContentHash
}

type packageIntent struct {
	Source    string   `json:"source"`
	Ref       string   `json:"ref,omitempty"`
	Selectors []string `json:"selectors,omitempty"`
	AllSkills bool     `json:"all_skills"`
}

func writePackageIntent(packageDir string, pkg config.SkillPackage) error {
	data, err := json.Marshal(packageIntent{
		Source:    pkg.Source,
		Ref:       pkg.Ref,
		Selectors: pkg.Skills,
		AllSkills: len(pkg.Skills) == 0,
	})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(packageDir, ".omni-package.json"), data, 0o644); err != nil {
		return fmt.Errorf("writing package intent: %w", err)
	}
	return nil
}

func installedIntentMatches(packageDir string, pkg config.SkillPackage) bool {
	data, err := os.ReadFile(filepath.Join(packageDir, ".omni-package.json"))
	if err != nil {
		return pkg.Ref == "" && len(pkg.Skills) > 0
	}
	var intent packageIntent
	if json.Unmarshal(data, &intent) != nil {
		return false
	}
	return intent.Source == pkg.Source &&
		intent.Ref == pkg.Ref &&
		intent.AllSkills == (len(pkg.Skills) == 0) &&
		(intent.AllSkills || sameStringSet(intent.Selectors, pkg.Skills))
}

// Only lockfile-attributed directories that still hold a valid manifest qualify.
func (s *Skills) legacySkillDirs(pkg config.SkillPackage) map[string]string {
	lock, err := config.LoadSkillLock(config.SkillLockPath(s.home))
	if err != nil {
		return nil
	}
	legacy := make(map[string]string)
	for name, entry := range lock.Skills {
		entryPkg, err := normalizeSkillPackage(config.SkillPackage{Source: entry.Source, Ref: entry.Ref})
		if err != nil || entryPkg.Source != pkg.Source || entryPkg.Ref != pkg.Ref {
			continue
		}
		if !validSkillName(name) {
			continue
		}
		dir := filepath.Join(s.home, ".agents", "skills", name)
		manifestName, err := validateSkillDir(dir)
		if err != nil || manifestName != name {
			continue
		}
		legacy[name] = dir
	}
	return legacy
}

func (s *Skills) legacyInstallWarnings(pkg config.SkillPackage) []string {
	if _, err := os.Lstat(s.packageDir(pkg.Source)); !os.IsNotExist(err) {
		return nil
	}
	if len(s.legacySkillDirs(pkg)) == 0 {
		return nil
	}
	return []string{fmt.Sprintf(
		"legacy CLI-managed install of %s remains in %s; run \"omni agents skills import\" to adopt it",
		pkg.Source, filepath.Join(s.home, ".agents", "skills"),
	)}
}

func (s *Skills) adoptLegacyInstalled(
	ctx context.Context,
	pkg config.SkillPackage,
	targets []Target,
) (bool, []string, error) {
	packageDir := s.packageDir(pkg.Source)
	if _, err := os.Lstat(packageDir); err == nil {
		return false, nil, nil
	} else if !os.IsNotExist(err) {
		return false, nil, fmt.Errorf("inspecting installed package %s: %w", packageDir, err)
	}
	legacy := s.legacySkillDirs(pkg)
	if len(legacy) == 0 {
		return false, nil, nil
	}
	names := pkg.Skills
	if len(names) == 0 {
		names = mapKeys(legacy)
	} else {
		names = append([]string(nil), names...)
		sort.Strings(names)
	}
	for _, name := range names {
		if legacy[name] == "" {
			return false, nil, nil
		}
	}
	if err := s.validateLegacyTargets(targets, names, legacy); err != nil {
		return false, nil, err
	}

	root := s.packagesRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return false, nil, fmt.Errorf("creating skills data dir: %w", err)
	}
	stage, err := os.MkdirTemp(root, ".adopt-")
	if err != nil {
		return false, nil, fmt.Errorf("creating skills staging dir: %w", err)
	}
	// Best-effort cleanup of the staging tree once the adoption settled.
	defer func() { _ = os.RemoveAll(stage) }()
	candidate := filepath.Join(stage, "package")
	for _, name := range names {
		if err := copyTree(legacy[name], filepath.Join(candidate, "skills", name), legacy[name]); err != nil {
			return false, nil, fmt.Errorf("staging legacy skill %s: %w", name, err)
		}
	}
	if err := writePackageIntent(candidate, pkg); err != nil {
		return false, nil, err
	}
	if err := os.Rename(candidate, packageDir); err != nil {
		return false, nil, fmt.Errorf("installing adopted package: %w", err)
	}
	snapshots, err := s.replaceLegacyTargets(packageDir, targets, names)
	var warnings []string
	if err != nil {
		warnings = append(warnings, restoreLinks(snapshots)...)
		if removeErr := os.RemoveAll(packageDir); removeErr != nil {
			warnings = append(warnings, fmt.Sprintf("rollback left %s in place: %v", packageDir, removeErr))
		}
		return false, warnings, err
	}
	warnings = append(warnings, removeLinkBackups(snapshots, "legacy copy")...)
	if err := s.storeMetadata(ctx, pkg, packageDir, names, "", "", skillProbeNone); err != nil {
		warnings = append(warnings, fmt.Sprintf("skill package %s: %v", pkg.Source, err))
	}
	return true, warnings, nil
}

func (s *Skills) validateLegacyTargets(targets []Target, names []string, legacy map[string]string) error {
	for _, dir := range uniqueTargetSkillDirs(targets, s.home) {
		for _, name := range names {
			path := filepath.Join(dir, name)
			info, err := os.Lstat(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				resolved, err := filepath.EvalSymlinks(path)
				legacyResolved, legacyErr := filepath.EvalSymlinks(legacy[name])
				if err != nil || legacyErr != nil || resolved != legacyResolved {
					return fmt.Errorf("skill %q "+SkillEntryConflict+" directory %s", name, dir)
				}
				continue
			}
			if !info.IsDir() {
				return fmt.Errorf("skill %q "+SkillEntryConflict+" directory %s", name, dir)
			}
			same, err := sameTree(path, legacy[name])
			if err != nil || !same {
				return fmt.Errorf("skill %q "+SkillEntryConflict+" directory %s", name, dir)
			}
		}
	}
	return nil
}

func (s *Skills) replaceLegacyTargets(
	packageDir string,
	targets []Target,
	names []string,
) ([]linkSnapshot, error) {
	var snapshots []linkSnapshot
	for _, dir := range uniqueTargetSkillDirs(targets, s.home) {
		for _, name := range names {
			path := filepath.Join(dir, name)
			info, err := os.Lstat(path)
			switch {
			case os.IsNotExist(err):
				snapshots = append(snapshots, linkSnapshot{path: path})
			case err != nil:
				return snapshots, fmt.Errorf("inspecting %s: %w", path, err)
			case info.Mode()&os.ModeSymlink != 0:
				target, err := os.Readlink(path)
				if err != nil {
					return snapshots, fmt.Errorf("reading skill link %s: %w", path, err)
				}
				snapshots = append(snapshots, linkSnapshot{path: path, target: target})
			case info.IsDir():
				backup, err := stageDirBackup(path, "."+name+".omni-legacy-")
				if err != nil {
					return snapshots, err
				}
				snapshots = append(snapshots, linkSnapshot{path: path, backup: backup})
			default:
				return snapshots, fmt.Errorf("skill %q already exists in %s", name, dir)
			}
			if err := installPackageLink(path, packageDir, name); err != nil {
				return snapshots, err
			}
		}
	}
	return snapshots, nil
}

func uniqueTargetSkillDirs(targets []Target, home string) []string {
	seen := make(map[string]struct{})
	var dirs []string
	for _, target := range targets {
		for _, dir := range target.skillDirs(home) {
			if _, ok := seen[dir]; ok {
				continue
			}
			seen[dir] = struct{}{}
			dirs = append(dirs, dir)
		}
	}
	sort.Strings(dirs)
	return dirs
}

// The backup must outlive the whole operation, since restoreLinks rewinds by renaming it back; only a caller that saw success may delete it.
func stageDirBackup(path, prefix string) (string, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("preparing target skill directory %s: %w", dir, err)
	}
	backup, err := os.MkdirTemp(dir, prefix)
	if err != nil {
		return "", fmt.Errorf("staging backup in %s: %w", dir, err)
	}
	if err := os.Remove(backup); err != nil {
		return "", fmt.Errorf("staging backup in %s: %w", dir, err)
	}
	if err := os.Rename(path, backup); err != nil {
		return "", fmt.Errorf("backing up %s: %w", path, err)
	}
	return backup, nil
}

// Failures degrade to warnings because the operation itself already succeeded.
func removeLinkBackups(snapshots []linkSnapshot, what string) []string {
	var warnings []string
	for _, snapshot := range snapshots {
		if snapshot.backup == "" {
			continue
		}
		if err := os.RemoveAll(snapshot.backup); err != nil {
			warnings = append(warnings, fmt.Sprintf("replaced %s remains at %s: %v", what, snapshot.backup, err))
		}
	}
	return warnings
}

func installPackageLink(path, packageDir, name string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := path + ".omni-tmp"
	// Best-effort: a leftover staging link from an interrupted run is not fatal.
	_ = os.Remove(tmp)
	relative, err := filepath.Rel(dir, filepath.Join(packageDir, "skills", name))
	if err != nil {
		return err
	}
	if err := os.Symlink(relative, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		// Best-effort cleanup of the staging link; the swap already failed.
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	values := make(map[string]struct{}, len(a))
	for _, value := range a {
		values[value] = struct{}{}
	}
	for _, value := range b {
		if _, ok := values[value]; !ok {
			return false
		}
	}
	return true
}

// Remove — Shared content goes only once no registered target references it, and unmanaged real directories are warned about rather than deleted.
func (s *Skills) Remove(_ context.Context, source string, targetIDs []string) ([]string, error) {
	targets, err := s.targets(targetIDs)
	if err != nil {
		return nil, err
	}
	lock, err := s.lockStore()
	if err != nil {
		return nil, err
	}
	defer lock.release()
	packageDir := s.packageDir(source)
	if _, err := os.Stat(packageDir); err != nil {
		if os.IsNotExist(err) {
			return nil, &NotInstalledError{Source: source}
		}
		return nil, fmt.Errorf("inspecting installed package %s: %w", packageDir, err)
	}
	names, err := skillDirNames(packageDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading installed package %s: %w", packageDir, err)
	}
	var warnings []string
	var snapshots []linkSnapshot
	for _, target := range targets {
		for _, dir := range target.skillDirs(s.home) {
			for _, name := range names {
				path := filepath.Join(dir, name)
				if info, err := os.Lstat(path); err == nil && info.IsDir() {
					warnings = append(warnings, fmt.Sprintf(
						"unmanaged copy of skill %q remains at %s for target %s", name, path, target.ID))
				}
			}
			entries, err := readDirIfExists(dir)
			if err != nil {
				warnings = append(warnings, restoreLinks(snapshots)...)
				return warnings, fmt.Errorf("reading target skill directory %s: %w", dir, err)
			}
			for _, entry := range entries {
				path := filepath.Join(dir, entry.Name())
				if ownedLink(path, packageDir) {
					link, err := os.Readlink(path)
					if err != nil {
						warnings = append(warnings, restoreLinks(snapshots)...)
						return warnings, fmt.Errorf("reading skill link %s: %w", path, err)
					}
					snapshots = append(snapshots, linkSnapshot{path: path, target: link})
					if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
						warnings = append(warnings, restoreLinks(snapshots)...)
						return warnings, fmt.Errorf("removing skill link %s: %w", path, err)
					}
				}
			}
		}
	}
	referenced, err := s.packageReferenced(packageDir)
	if err != nil {
		warnings = append(warnings, restoreLinks(snapshots)...)
		return warnings, err
	}
	if !referenced {
		trash := packageDir + ".removing"
		// Best-effort: a leftover trash dir from an interrupted remove is not fatal.
		_ = os.RemoveAll(trash)
		if err := os.Rename(packageDir, trash); err != nil {
			warnings = append(warnings, restoreLinks(snapshots)...)
			return warnings, fmt.Errorf("removing package %s: %w", packageDir, err)
		}
		if err := os.RemoveAll(trash); err != nil {
			if renameErr := os.Rename(trash, packageDir); renameErr != nil {
				warnings = append(warnings, fmt.Sprintf(
					"partially removed package remains at %s: %v", trash, renameErr))
			}
			warnings = append(warnings, restoreLinks(snapshots)...)
			return warnings, fmt.Errorf("removing package %s: %w", packageDir, err)
		}
	}
	return warnings, nil
}

func (s *Skills) Inventory(ctx context.Context, source string, targetIDs []string) (SkillInventory, error) {
	parsed, err := ParseSkillSource(source)
	if err != nil {
		return SkillInventory{}, err
	}
	source = parsed.Source
	names, err := skillDirNames(s.packageDir(source))
	if os.IsNotExist(err) {
		names = nil
	} else if err != nil {
		return SkillInventory{}, err
	}
	targets, unknown := s.knownTargets(targetIDs)
	out := SkillInventory{
		Skills:            names,
		Installed:         len(names) > 0 && len(targets) > 0,
		PerTarget:         make(map[string]bool, len(targets)),
		PerTargetDrifted:  make(map[string]bool, len(targets)),
		PerTargetShadowed: make(map[string]bool, len(targets)),
		UnknownTargets:    unknown,
	}
	canonicalValid := len(names) > 0
	for _, name := range names {
		manifestName, err := validateSkillDir(filepath.Join(s.packageDir(source), "skills", name))
		if err != nil || manifestName != name {
			canonicalValid = false
			break
		}
	}
	out.Installed = out.Installed && canonicalValid
	if len(names) == 1 {
		if data, err := os.ReadFile(filepath.Join(s.packageDir(source), "skills", names[0], "SKILL.md")); err == nil {
			_, out.Description = skillFrontmatter(data)
		}
	}
	for _, target := range targets {
		state := targetSkillMissing
		if canonicalValid {
			state = s.targetPackageState(target, source, names)
		}
		out.PerTarget[target.ID] = state == targetSkillInstalled
		out.PerTargetDrifted[target.ID] = state == targetSkillDrifted
		out.PerTargetShadowed[target.ID] = state == targetSkillShadowed
		if state != targetSkillInstalled {
			out.Installed = false
		}
	}
	if metadata, ok := s.loadMetadata(ctx, s.packageDir(source)); ok {
		if !metadata.CheckedAt.IsZero() {
			out.Updated = metadata.CheckedAt.Format("2006-01-02")
		}
		out.Outdated = metadata.Outdated
		out.OutdatedCheckedAt = metadata.OutdatedCheckedAt
	}
	return out, nil
}

// SkillEntryConflict — Raised only by the strict flows (Install, Refresh, Adopt, ResolveDrift); Sync and Upgrade skip the entry instead.
const SkillEntryConflict = "already exists for target"

// SkillDriftSelection — Everything outside the selection keeps the leave-foreign-content-alone rule that install, restore and update follow.
type SkillDriftSelection struct {
	Targets []string
	Skills  []string
}

// A nil scope displaces nothing, which is what every non-resolve caller wants.
type displaceScope struct {
	dirs   map[string]bool
	skills map[string]bool
}

// Scoped by directory, not target: two targets can share a skill dir, and its single entry cannot be resolved for one and left foreign for the other.
func (d *displaceScope) allows(dir, name string) bool {
	return d != nil && d.dirs[dir] && d.skills[name]
}

// ResolveDrift — Takes over the selection's foreign entries, staging displaced content aside until the install succeeds so a failure can rename it back.
func (s *Skills) ResolveDrift(
	ctx context.Context,
	pkg config.SkillPackage,
	targetIDs []string,
	selection SkillDriftSelection,
) ([]string, error) {
	if s == nil || s.registry == nil {
		return nil, fmt.Errorf("skills service is not configured")
	}
	pkg, err := normalizeSkillPackage(pkg)
	if err != nil {
		return nil, err
	}
	targets, err := s.targets(targetIDs)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no installed agent targets selected")
	}
	selected, err := s.targets(selection.Targets)
	if err != nil {
		return nil, err
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("no drifted agent targets selected")
	}
	names := selection.Skills
	if len(names) == 0 {
		// Only what the caller previewed as drifted may be displaced; anything else collides into the leave-in-place warning.
		names, err = s.driftedEntryNames(pkg.Source, selection.Targets)
		if err != nil {
			return nil, err
		}
	}
	scope := &displaceScope{
		dirs:   make(map[string]bool),
		skills: make(map[string]bool, len(names)),
	}
	for _, dir := range uniqueTargetSkillDirs(selected, s.home) {
		scope.dirs[dir] = true
	}
	for _, name := range names {
		scope.skills[name] = true
	}
	lock, err := s.lockStore()
	if err != nil {
		return nil, err
	}
	defer lock.release()
	return s.installLocked(ctx, pkg, targets, false, false, scope, targetIDs != nil)
}

func (s *Skills) driftedEntryNames(source string, targetIDs []string) ([]string, error) {
	entries, err := s.DriftedEntries(source, targetIDs, nil)
	if err != nil {
		return nil, err
	}
	names := make(map[string]bool)
	for _, paths := range entries {
		for _, path := range paths {
			names[filepath.Base(path)] = true
		}
	}
	return mapKeys(names), nil
}

// DriftedEntries — A package with no canonical content has no names to compare against, so its drift shows up only as an install collision.
func (s *Skills) DriftedEntries(source string, targetIDs, skills []string) (map[string][]string, error) {
	parsed, err := ParseSkillSource(source)
	if err != nil {
		return nil, err
	}
	source = parsed.Source
	packageDir := s.packageDir(source)
	names, err := skillDirNames(packageDir)
	if os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	wanted := make(map[string]bool, len(skills))
	for _, name := range skills {
		wanted[name] = true
	}
	targets, err := s.targets(targetIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]string, len(targets))
	for _, target := range targets {
		for _, dir := range target.skillDirs(s.home) {
			for _, name := range names {
				if len(wanted) > 0 && !wanted[name] {
					continue
				}
				path := filepath.Join(dir, name)
				if ownedLink(path, packageDir) {
					continue
				}
				info, err := os.Lstat(path)
				if err != nil {
					continue
				}
				if info.Mode()&os.ModeSymlink == 0 && info.IsDir() {
					if same, err := sameTree(path, filepath.Join(packageDir, "skills", name)); err == nil && same {
						continue
					}
				}
				out[target.ID] = append(out[target.ID], path)
			}
		}
	}
	for id := range out {
		sort.Strings(out[id])
	}
	return out, nil
}

// Installed covers a managed link or a byte-identical copy the next restore converges; drifted means another tool owns the entry.
type targetSkillState int

const (
	targetSkillMissing targetSkillState = iota
	targetSkillInstalled
	targetSkillDrifted
	targetSkillShadowed
)

// Installed only when every skill is; foreign content is drift, while another managed package shadows.
func (s *Skills) targetPackageState(target Target, source string, names []string) targetSkillState {
	if len(names) == 0 {
		return targetSkillMissing
	}
	packageDir := s.packageDir(source)
	installed, drifted, shadowed := true, false, false
	for _, name := range names {
		switch s.targetSkillEntryState(target, packageDir, name) {
		case targetSkillInstalled:
		case targetSkillDrifted:
			installed, drifted = false, true
		case targetSkillShadowed:
			installed, shadowed = false, true
		default:
			installed = false
		}
	}
	switch {
	case installed:
		return targetSkillInstalled
	case drifted:
		return targetSkillDrifted
	case shadowed:
		return targetSkillShadowed
	default:
		return targetSkillMissing
	}
}

// Inspects every skill dir rather than stopping at the first good one: restore writes all of them, so one foreign entry blocks it.
func (s *Skills) targetSkillEntryState(target Target, packageDir, name string) targetSkillState {
	state := targetSkillMissing
	for _, dir := range target.skillDirs(s.home) {
		path := filepath.Join(dir, name)
		if ownedLink(path, packageDir) {
			state = targetSkillInstalled
			continue
		}
		info, err := os.Lstat(path)
		if err != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 && ownedLink(path, s.packagesRoot()) {
			return targetSkillShadowed
		}
		if info.Mode()&os.ModeSymlink == 0 && info.IsDir() {
			if same, err := sameTree(path, filepath.Join(packageDir, "skills", name)); err == nil && same {
				state = targetSkillInstalled
				continue
			}
		}
		return targetSkillDrifted
	}
	return state
}

func (s *Skills) packageDir(source string) string {
	resolved, err := s.resolveSource(source)
	if err != nil {
		// an unparseable source can never install; the raw string still yields a stable dir
		resolved = source
	}
	sum := sha256.Sum256([]byte(resolved))
	return filepath.Join(s.packagesRoot(), hex.EncodeToString(sum[:]))
}

// Unrecognised ids are reported rather than fatal, and a nil id list means every installed target.
func (s *Skills) knownTargets(ids []string) ([]Target, []string) {
	if ids == nil {
		return s.registry.Installed(s.home), nil
	}
	out := make([]Target, 0, len(ids))
	var unknown []string
	for _, id := range ids {
		target, ok := s.registry.ByID(id)
		if !ok {
			unknown = append(unknown, id)
			continue
		}
		out = append(out, target)
	}
	return out, unknown
}

func (s *Skills) targets(ids []string) ([]Target, error) {
	out, unknown := s.knownTargets(ids)
	if len(unknown) > 0 {
		return nil, fmt.Errorf("unknown agent target %q", unknown[0])
	}
	return out, nil
}

func (t Target) skillDirs(home string) []string {
	dirs := []string{filepath.Join(t.configPath(home), "skills")}
	for _, dir := range t.extraSkillsDirs {
		dirs = append(dirs, filepath.Join(home, dir))
	}
	return uniqueStrings(dirs)
}

// Package identity derives from this, so acquire and packageDir must share it.
func (s *Skills) resolveSource(raw string) (string, error) {
	if strings.HasPrefix(raw, ".") && s.sourceBase != "" {
		raw = filepath.Join(s.sourceBase, filepath.FromSlash(raw))
	}
	if strings.HasPrefix(raw, "file://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", err
		}
		raw = u.Path
	}
	if raw == "~" {
		raw = s.home
	} else if strings.HasPrefix(raw, "~/") {
		raw = filepath.Join(s.home, filepath.FromSlash(strings.TrimPrefix(raw, "~/")))
	}
	return raw, nil
}

// The store flock is held across acquisition, so an unbounded fetch or clone would wedge skills management for every other process.
func (s *Skills) acquireBounded(ctx context.Context, pkg config.SkillPackage, dst string) error {
	ctx, cancel := context.WithTimeout(ctx, skillAcquireTimeout)
	defer cancel()
	return s.acquire(ctx, pkg, dst)
}

func (s *Skills) acquire(ctx context.Context, pkg config.SkillPackage, dst string) error {
	raw, err := s.resolveSource(pkg.Source)
	if err != nil {
		return err
	}
	if info, err := os.Stat(raw); err == nil && info.IsDir() {
		if pkg.Ref != "" && dirExists(filepath.Join(raw, ".git")) {
			return cloneGit(ctx, raw, pkg.Ref, dst)
		}
		if pkg.Ref != "" {
			return fmt.Errorf("local non-Git source %q cannot use ref %q", pkg.Source, pkg.Ref)
		}
		return copyTree(raw, dst, raw)
	}
	if strings.HasPrefix(pkg.Source, ".") || strings.HasPrefix(pkg.Source, "/") ||
		strings.HasPrefix(pkg.Source, "~") || strings.HasPrefix(pkg.Source, "file://") {
		return fmt.Errorf("local skill source %q is not an accessible directory", pkg.Source)
	}
	return s.acquireRemote(ctx, pkg, dst)
}

func cloneGit(ctx context.Context, remote, ref, dst string) error {
	args := []string{"clone"}
	if ref == "" {
		args = append(args, "--depth", "1")
	} else {
		args = append(args, "--no-checkout")
	}
	args = append(args, "--", remote, dst)
	if out, err := exec.CommandContext(ctx, "git", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("git clone %s: %w: %s", remote, err, strings.TrimSpace(string(out)))
	}
	if ref != "" {
		if !validGitRef(ref) {
			return fmt.Errorf("invalid Git ref %q", ref)
		}
		if out, err := exec.CommandContext(ctx, "git", "-C", dst, "fetch", "--depth", "1", "origin", "--", ref).CombinedOutput(); err != nil {
			return fmt.Errorf("git fetch %s#%s: %w: %s", remote, ref, err, strings.TrimSpace(string(out)))
		}
		if out, err := exec.CommandContext(ctx, "git", "-C", dst, "checkout", "--detach", "FETCH_HEAD").CombinedOutput(); err != nil {
			return fmt.Errorf("git checkout %s#%s: %w: %s", remote, ref, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// Matches case-insensitively: on a case-insensitive filesystem two spellings differing only in case are one directory entry.
func discoveredName(out map[string]string, name string) (string, bool) {
	if _, ok := out[name]; ok {
		return name, true
	}
	for existing := range out {
		if strings.EqualFold(existing, name) {
			return existing, true
		}
	}
	return "", false
}

// Overlapping containers can offer the same directory twice; only two different directories claiming one name conflict.
func addDiscovered(out map[string]string, name, dir string) error {
	existing, ok := discoveredName(out, name)
	if !ok {
		out[name] = dir
		return nil
	}
	if out[existing] == dir {
		return nil
	}
	if existing != name {
		return fmt.Errorf("duplicate discovered skill %q and %q", existing, name)
	}
	return fmt.Errorf("duplicate discovered skill %q", name)
}

// Names the directory relative to the source: its absolute path is inside a staging dir gone by the time a caller reads the warning.
func invalidSkillWarning(root, dir string, err error) string {
	name := "the source root"
	if rel, relErr := filepath.Rel(root, dir); relErr == nil && rel != "." {
		name = filepath.ToSlash(rel)
	}
	return fmt.Sprintf("skipping skill directory %s: %v", name, err)
}

// Directories with an unusable manifest are skipped and reported as warnings.
func discoverSkills(root string) (map[string]string, []string, error) {
	out := make(map[string]string)
	var warnings []string
	containers := []string{
		"skills",
		"skills/.curated",
		"skills/.experimental",
		"skills/.system",
		".agents/skills",
		".claude/skills",
		".codex/skills",
		"agent/skills",
		"data/skills",
	}
	for _, target := range supportedTargets {
		containers = append(containers, filepath.Join(target.configDir, "skills"))
		containers = append(containers, target.extraSkillsDirs...)
	}
	containers = uniqueStrings(containers)
	if fileExists(filepath.Join(root, "SKILL.md")) {
		name, err := validateSkillDir(root)
		if err != nil {
			warnings = append(warnings, invalidSkillWarning(root, root, err))
		} else {
			out[name] = root
		}
	}
	for _, rel := range containers {
		base := filepath.Join(root, rel)
		containerWarnings, err := discoverSkillsUnder(root, base, 2, out)
		warnings = append(warnings, containerWarnings...)
		if err != nil {
			return nil, warnings, err
		}
	}
	manifestWarnings, err := discoverManifestSkills(root, out)
	warnings = append(warnings, manifestWarnings...)
	if err != nil {
		return nil, warnings, err
	}
	if len(out) > 0 {
		return out, warnings, nil
	}
	directories := 0
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			directories++
			if directories > 10_000 {
				return fmt.Errorf("skill discovery directory limit exceeded")
			}
			if path != root && (entry.Name() == ".git" || entry.Name() == "node_modules") {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			if strings.Count(filepath.ToSlash(rel), "/") >= 8 {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() != "SKILL.md" {
			return nil
		}
		dir := filepath.Dir(path)
		name, err := validateSkillDir(dir)
		if err != nil {
			warnings = append(warnings, invalidSkillWarning(root, dir, err))
			return nil
		}
		return addDiscovered(out, name, dir)
	})
	if err != nil {
		return nil, warnings, err
	}
	if len(out) == 0 {
		return nil, warnings, fmt.Errorf("source contains no valid SKILL.md directories")
	}
	return out, warnings, nil
}

func discoverSkillsUnder(root, base string, maxDepth int, out map[string]string) ([]string, error) {
	if !dirExists(base) {
		return nil, nil
	}
	var warnings []string
	err := filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		depth := 0
		if rel != "." {
			depth = strings.Count(filepath.ToSlash(rel), "/") + 1
		}
		if depth > maxDepth {
			return filepath.SkipDir
		}
		if !fileExists(filepath.Join(path, "SKILL.md")) {
			return nil
		}
		name, err := validateSkillDir(path)
		if err != nil {
			warnings = append(warnings, invalidSkillWarning(root, path, err))
			return filepath.SkipDir
		}
		if err := addDiscovered(out, name, path); err != nil {
			return err
		}
		return filepath.SkipDir
	})
	return warnings, err
}

func discoverManifestSkills(root string, out map[string]string) ([]string, error) {
	var warnings []string
	for _, rel := range []string{
		".claude-plugin/plugin.json",
		".claude-plugin/marketplace.json",
		".plugin/plugin.json",
	} {
		path := filepath.Join(root, rel)
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return warnings, fmt.Errorf("reading plugin manifest %s: %w", path, err)
		}
		var manifest any
		if err := json.Unmarshal(data, &manifest); err != nil {
			return warnings, fmt.Errorf("malformed plugin manifest %s: %w", path, err)
		}
		var declared []string
		collectSkillPaths(manifest, &declared)
		for _, declaredPath := range declared {
			candidate := filepath.Clean(filepath.Join(root, filepath.FromSlash(declaredPath)))
			if !pathWithin(root, candidate) {
				return warnings, fmt.Errorf("plugin skill path %q escapes repository", declaredPath)
			}
			if fileExists(filepath.Join(candidate, "SKILL.md")) {
				name, err := validateSkillDir(candidate)
				if err != nil {
					warnings = append(warnings, invalidSkillWarning(root, candidate, err))
					continue
				}
				if _, exists := discoveredName(out, name); !exists {
					out[name] = candidate
				}
				continue
			}
			entries, err := os.ReadDir(candidate)
			if err != nil && !os.IsNotExist(err) {
				return warnings, fmt.Errorf("reading plugin skill path %s: %w", candidate, err)
			}
			for _, entry := range entries {
				dir := filepath.Join(candidate, entry.Name())
				if !entry.IsDir() || !fileExists(filepath.Join(dir, "SKILL.md")) {
					continue
				}
				name, err := validateSkillDir(dir)
				if err != nil {
					warnings = append(warnings, invalidSkillWarning(root, dir, err))
					continue
				}
				if _, exists := discoveredName(out, name); !exists {
					out[name] = dir
				}
			}
		}
	}
	return warnings, nil
}

func collectSkillPaths(value any, out *[]string) {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if key == "skills" {
				if values, ok := child.([]any); ok {
					for _, item := range values {
						if path, ok := item.(string); ok {
							*out = append(*out, path)
						}
					}
				}
			}
			collectSkillPaths(child, out)
		}
	case []any:
		for _, child := range value {
			collectSkillPaths(child, out)
		}
	}
}

func validateSkillDir(dir string) (string, error) {
	path := filepath.Join(dir, "SKILL.md")
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("SKILL.md is not a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	name, description := skillFrontmatter(data)
	if !validSkillName(name) || strings.TrimSpace(description) == "" {
		return "", fmt.Errorf("malformed SKILL.md frontmatter")
	}
	return name, nil
}

func skillFrontmatter(data []byte) (name, description string) {
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", ""
	}
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "---" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch strings.TrimSpace(key) {
		case "name":
			name = value
		case "description":
			description = value
		}
	}
	return name, description
}

func selectSkills(all map[string]string, names []string) (map[string]string, error) {
	if len(names) == 0 {
		return all, nil
	}
	out := make(map[string]string, len(names))
	for _, name := range names {
		path, ok := all[name]
		if !ok {
			return nil, fmt.Errorf("requested skill %q was not found", name)
		}
		out[name] = path
	}
	return out, nil
}

// Excluded from a checkout-root copy: carrying repository history into the package would destabilize its content hash.
var vcsMetadataDirs = map[string]bool{".git": true, ".hg": true, ".svn": true}

func copyTree(src, dst, root string) error {
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if vcsMetadataDirs[rel] {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if filepath.IsAbs(link) {
				return fmt.Errorf("absolute symlink %s is not allowed", path)
			}
			resolved := filepath.Clean(filepath.Join(filepath.Dir(path), link))
			if !pathWithin(root, resolved) {
				return fmt.Errorf("symlink %s escapes the skill directory", path)
			}
			realRoot, rootErr := filepath.EvalSymlinks(root)
			realTarget, targetErr := filepath.EvalSymlinks(path)
			// A dangling link resolves to nothing, so it cannot reach outside; the lexical check above is the whole verdict for it.
			if !os.IsNotExist(targetErr) {
				if rootErr != nil || targetErr != nil || !pathWithin(realRoot, realTarget) {
					return fmt.Errorf("symlink %s escapes the skill directory", path)
				}
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		if entry.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported file type %s", path)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			_ = in.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		inErr := in.Close()
		outErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if inErr != nil {
			return inErr
		}
		return outErr
	})
}

// A drift-tolerant install leaves these exactly as they are; a strict one never sees them because validateLinkChanges refuses first.
type foreignEntries map[string]bool

func (s *Skills) validateLinkChanges(
	packageDir, candidate string,
	targets []Target,
	newNames []string,
	displace *displaceScope,
	tolerateDrift bool,
) (foreignEntries, error) {
	var foreign foreignEntries
	for _, target := range targets {
		for _, dir := range target.skillDirs(s.home) {
			for _, name := range newNames {
				path := filepath.Join(dir, name)
				info, err := os.Lstat(path)
				if os.IsNotExist(err) || ownedLink(path, packageDir) {
					continue
				} else if err != nil {
					return nil, err
				}
				if displace.allows(dir, name) {
					continue
				}
				if info.Mode()&os.ModeSymlink == 0 {
					same, err := sameTree(path, filepath.Join(candidate, "skills", name))
					if err == nil && same {
						continue
					}
				}
				if !tolerateDrift {
					return nil, fmt.Errorf("skill %q "+SkillEntryConflict+" %s", name, target.ID)
				}
				if foreign == nil {
					foreign = make(foreignEntries)
				}
				foreign[path] = true
			}
		}
	}
	return foreign, nil
}

type linkSnapshot struct {
	path   string
	target string
	backup string
}

func (s *Skills) applyLinkChanges(
	packageDir string,
	targets []Target,
	oldNames, newNames []string,
	displace *displaceScope,
	foreign foreignEntries,
	prune bool,
) ([]linkSnapshot, []string, error) {
	newSet := make(map[string]bool, len(newNames))
	for _, name := range newNames {
		newSet[name] = true
	}
	desiredDirs := make(map[string]bool)
	for _, target := range targets {
		for _, dir := range target.skillDirs(s.home) {
			desiredDirs[dir] = true
		}
	}
	dirOwners := make(map[string]string)
	for _, target := range s.registry.All() {
		for _, dir := range target.skillDirs(s.home) {
			if _, ok := dirOwners[dir]; !ok {
				dirOwners[dir] = target.ID
			}
		}
	}
	dirs := make([]string, 0, len(dirOwners))
	for dir := range dirOwners {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)

	var snapshots []linkSnapshot
	var warnings []string
	for _, dir := range dirs {
		if !desiredDirs[dir] {
			if !prune {
				continue
			}
			entries, err := readDirIfExists(dir)
			if err != nil {
				return snapshots, warnings, fmt.Errorf("reading target skill directory %s: %w", dir, err)
			}
			for _, entry := range entries {
				path := filepath.Join(dir, entry.Name())
				if !ownedLink(path, packageDir) {
					continue
				}
				link, err := os.Readlink(path)
				if err != nil {
					return snapshots, warnings, fmt.Errorf("reading skill link %s: %w", path, err)
				}
				snapshots = append(snapshots, linkSnapshot{path: path, target: link})
				if err := os.Remove(path); err != nil {
					return snapshots, warnings, fmt.Errorf("removing skill link %s: %w", path, err)
				}
				warnings = append(warnings, fmt.Sprintf(
					"unlinked %s: target %s is no longer selected", path, dirOwners[dir]))
			}
			continue
		}
		for _, name := range oldNames {
			if newSet[name] {
				continue
			}
			path := filepath.Join(dir, name)
			if ownedLink(path, packageDir) {
				link, err := os.Readlink(path)
				if err != nil {
					return snapshots, warnings, fmt.Errorf("reading skill link %s: %w", path, err)
				}
				snapshots = append(snapshots, linkSnapshot{path: path, target: link})
				if err := os.Remove(path); err != nil {
					return snapshots, warnings, fmt.Errorf("removing skill link %s: %w", path, err)
				}
			}
		}
		for _, name := range newNames {
			path := filepath.Join(dir, name)
			if foreign[path] {
				continue
			}
			if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink == 0 {
				identical := false
				if info.IsDir() {
					same, sameErr := sameTree(path, filepath.Join(packageDir, "skills", name))
					identical = sameErr == nil && same
				}
				if !identical && !displace.allows(dir, name) {
					warnings = append(warnings, fmt.Sprintf(
						"existing unmanaged directory at %s; leaving in place", path))
					continue
				}
				backup, err := stageDirBackup(path, "."+name+".omni-adopt-")
				if err != nil {
					return snapshots, warnings, err
				}
				snapshots = append(snapshots, linkSnapshot{path: path, backup: backup})
				if err := installPackageLink(path, packageDir, name); err != nil {
					return snapshots, warnings, err
				}
				if identical {
					warnings = append(warnings, fmt.Sprintf("adopted identical unmanaged copy at %s", path))
				} else {
					warnings = append(warnings, fmt.Sprintf("replaced foreign copy at %s", path))
				}
				continue
			}
			if linkPointsAt(path, filepath.Join(packageDir, "skills", name)) {
				continue
			}
			old, err := os.Readlink(path)
			if err != nil && !os.IsNotExist(err) {
				return snapshots, warnings, fmt.Errorf("reading skill link %s: %w", path, err)
			}
			snapshots = append(snapshots, linkSnapshot{path: path, target: old})
			if err := installPackageLink(path, packageDir, name); err != nil {
				return snapshots, warnings, err
			}
		}
	}
	return snapshots, warnings, nil
}

// The caller is already returning the original failure, so rewind problems become warnings.
func restoreLinks(snapshots []linkSnapshot) []string {
	var failed []string
	for i := len(snapshots) - 1; i >= 0; i-- {
		snapshot := snapshots[i]
		if err := os.Remove(snapshot.path); err != nil && !os.IsNotExist(err) {
			failed = append(failed, fmt.Sprintf("rollback left %s in place: %v", snapshot.path, err))
			continue
		}
		switch {
		case snapshot.backup != "":
			if err := os.Rename(snapshot.backup, snapshot.path); err != nil {
				failed = append(failed, fmt.Sprintf(
					"rollback could not restore %s from %s: %v", snapshot.path, snapshot.backup, err))
			}
		case snapshot.target != "":
			if err := os.MkdirAll(filepath.Dir(snapshot.path), 0o755); err != nil {
				failed = append(failed, fmt.Sprintf("rollback could not restore %s: %v", snapshot.path, err))
				continue
			}
			if err := os.Symlink(snapshot.target, snapshot.path); err != nil {
				failed = append(failed, fmt.Sprintf("rollback could not restore %s: %v", snapshot.path, err))
			}
		}
	}
	return failed
}

func linkPointsAt(path, target string) bool {
	link, err := os.Readlink(path)
	if err != nil {
		return false
	}
	if !filepath.IsAbs(link) {
		link = filepath.Join(filepath.Dir(path), link)
	}
	return filepath.Clean(link) == filepath.Clean(target)
}

func ownedLink(path, packageDir string) bool {
	link, err := os.Readlink(path)
	if err != nil {
		return false
	}
	if !filepath.IsAbs(link) {
		link = filepath.Join(filepath.Dir(path), link)
	}
	return pathWithin(packageDir, filepath.Clean(link))
}

func (s *Skills) packageReferenced(packageDir string) (bool, error) {
	for _, target := range s.registry.All() {
		for _, dir := range target.skillDirs(s.home) {
			entries, err := readDirIfExists(dir)
			if err != nil {
				return false, fmt.Errorf("reading target skill directory %s: %w", dir, err)
			}
			for _, entry := range entries {
				if ownedLink(filepath.Join(dir, entry.Name()), packageDir) {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func readDirIfExists(path string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	return entries, err
}

func sameTree(a, b string) (bool, error) {
	ha, err := hashTree(a)
	if err != nil {
		return false, err
	}
	hb, err := hashTree(b)
	return ha == hb, err
}

// Bumping this invalidates every stored ContentHash, which installedMetadataMatches must read as "unknown" rather than as a corrupt tree.
const hashTreeVersion = "omni-skill-tree-v2"

// Length-prefixed records with a type tag: sameTree gates a delete, so a file must never hash like the symlink or the mode-differing tree it replaces.
func hashTree(root string) (string, error) {
	hash := sha256.New()
	writeHashField(hash, []byte(hashTreeVersion))
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel != "." {
			paths = append(paths, rel)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	for _, rel := range paths {
		path := filepath.Join(root, rel)
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		writeHashField(hash, []byte(filepath.ToSlash(rel)))
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return "", err
			}
			writeHashField(hash, []byte("link"))
			writeHashField(hash, []byte(link))
		case info.IsDir():
			writeHashField(hash, []byte("dir"))
		case info.Mode().IsRegular():
			sum, err := hashFile(path)
			if err != nil {
				return "", err
			}
			writeHashField(hash, []byte("file"))
			writeHashField(hash, []byte(executableMode(info.Mode())))
			writeHashField(hash, sum)
		default:
			return "", fmt.Errorf("unsupported file type %s", path)
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func writeHashField(hash io.Writer, data []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(data)))
	// A hash.Hash never fails a write, and every caller here passes one.
	_, _ = hash.Write(length[:])
	_, _ = hash.Write(data)
}

func executableMode(mode os.FileMode) string {
	if mode.Perm()&0o111 != 0 {
		return "exec"
	}
	return "plain"
}

func hashFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil {
		return nil, copyErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return hash.Sum(nil), nil
}

// An empty sourceFingerprint means content was reused rather than reacquired, so the previous source identity and verdict carry forward.
func (s *Skills) storeMetadata(
	ctx context.Context,
	pkg config.SkillPackage,
	packageDir string,
	names []string,
	sourceFingerprint string,
	sourceProbe string,
	sourceProbeKind skillProbeKind,
) error {
	if s.state == nil {
		return nil
	}
	contentHash, err := hashTree(packageDir)
	if err != nil {
		return fmt.Errorf("hashing installed package: %w", err)
	}
	key := metadataStateKey(packageDir)
	metadata := skillMetadata{
		Source:            pkg.Source,
		Ref:               pkg.Ref,
		Skills:            names,
		Selectors:         pkg.Skills,
		AllSkills:         len(pkg.Skills) == 0,
		SourceFingerprint: sourceFingerprint,
		ContentHash:       contentHash,
		HashVersion:       hashTreeVersion,
		CheckedAt:         time.Now().UTC(),
		SourceProbe:       sourceProbe,
		SourceProbeKind:   probeKindName(sourceProbeKind),
	}
	if sourceFingerprint == "" {
		previous, _ := s.loadMetadata(ctx, packageDir)
		metadata.SourceFingerprint = previous.SourceFingerprint
		metadata.SourceProbe = previous.SourceProbe
		metadata.SourceProbeKind = previous.SourceProbeKind
		metadata.Outdated = previous.Outdated
		metadata.OutdatedCheckedAt = previous.OutdatedCheckedAt
	} else if sourceProbe != "" {
		current := false
		metadata.Outdated = &current
		metadata.OutdatedCheckedAt = time.Now().UTC()
	}
	if metadata.SourceFingerprint == "" {
		metadata.SourceFingerprint = contentHash
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encoding install metadata: %w", err)
	}
	if err := s.state.SetState(ctx, key, string(data)); err != nil {
		return fmt.Errorf("writing install metadata: %w", err)
	}
	return nil
}

func fingerprintSource(ctx context.Context, root string) string {
	if dirExists(filepath.Join(root, ".git")) {
		out, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "HEAD").Output()
		if err == nil {
			return strings.TrimSpace(string(out))
		}
	}
	hash, _ := hashTree(root)
	return hash
}

func (s *Skills) acquireRemote(ctx context.Context, pkg config.SkillPackage, dst string) error {
	source := pkg.Source
	if strings.HasSuffix(strings.ToLower(source), "/skill.md") {
		return fmt.Errorf("direct SKILL.md URLs are not supported; use a well-known index")
	}
	if !strings.Contains(source, "://") && !strings.Contains(source, ":") {
		parts := strings.Split(source, "/")
		if len(parts) < 2 {
			return fmt.Errorf("invalid GitHub source %q", source)
		}
		remote := "https://github.com/" + parts[0] + "/" + parts[1] + ".git"
		if len(parts) == 2 {
			return cloneGit(ctx, remote, pkg.Ref, dst)
		}
		repo := dst + ".repo"
		// Best-effort cleanup of the intermediate clone.
		defer func() { _ = os.RemoveAll(repo) }()
		if err := cloneGit(ctx, remote, pkg.Ref, repo); err != nil {
			return err
		}
		subpath := filepath.Join(append([]string{repo}, parts[2:]...)...)
		if !dirExists(subpath) {
			return fmt.Errorf("repository subpath %q not found", strings.Join(parts[2:], "/"))
		}
		return copyTree(subpath, dst, subpath)
	}
	u, err := url.Parse(source)
	if err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		catalog, wellKnownErr := s.acquireWellKnown(ctx, pkg, dst)
		if wellKnownErr == nil {
			return nil
		}
		if catalog {
			return wellKnownErr
		}
		if err := os.RemoveAll(dst); err != nil {
			return fmt.Errorf("clearing skills index staging dir: %w", err)
		}
		if err := acquireGit(ctx, pkg, source, dst); err != nil {
			return fmt.Errorf("%w (skills index probe: %v)", err, wellKnownErr)
		}
		return nil
	}
	return acquireGit(ctx, pkg, source, dst)
}

func acquireGit(ctx context.Context, pkg config.SkillPackage, source, dst string) error {
	remote, subpath := splitGitRemoteSubpath(source)
	if subpath == "" {
		return cloneGit(ctx, remote, pkg.Ref, dst)
	}
	repo := dst + ".repo"
	// Best-effort cleanup of the intermediate clone.
	defer func() { _ = os.RemoveAll(repo) }()
	if err := cloneGit(ctx, remote, pkg.Ref, repo); err != nil {
		return err
	}
	path := filepath.Join(repo, filepath.FromSlash(subpath))
	if !pathWithin(repo, path) || !dirExists(path) {
		return fmt.Errorf("repository subpath %q not found", subpath)
	}
	return copyTree(path, dst, path)
}

func splitGitRemoteSubpath(source string) (remote, subpath string) {
	lower := strings.ToLower(source)
	marker := strings.Index(lower, ".git/")
	if marker < 0 {
		return source, ""
	}
	end := marker + len(".git")
	return source[:end], strings.TrimPrefix(filepath.ToSlash(source[end:]), "/")
}

func skillDirNames(packageDir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(packageDir, "skills"))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func mapKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := values[:0]
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}
