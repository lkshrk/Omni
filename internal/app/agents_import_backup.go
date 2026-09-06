package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lkshrk/omni/internal/apm"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/testguard"
)

const (
	importBackupPrefix   = "agents-import-backup"
	importBackupManifest = "backup.json"
	importBackupPinned   = "pinned"
)

// Absence is state: a file missing at backup time must be missing again after a restore.
type importBackupEntry struct {
	Path   string `json:"path"`
	Copy   string `json:"copy,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Mode   uint32 `json:"mode,omitempty"`
	Absent bool   `json:"absent,omitempty"`
}

type importBackup struct {
	Host      string              `json:"host"`
	CreatedAt time.Time           `json:"created_at"`
	Entries   []importBackupEntry `json:"entries"`
}

// importBoundary is every path an import writes: the template, the live manifest, and the sync marker.
func (a *App) importBoundary() ([]string, error) {
	template, err := agentsAdoptTemplatePath()
	if err != nil {
		return nil, err
	}
	paths := []string{template}
	if dir, err := apm.GlobalWorkspaceDir(); err == nil {
		paths = append(paths, filepath.Join(dir, "apm.yml"))
	}
	if a.StateDir != "" {
		paths = append(paths, filepath.Join(a.StateDir, templateStateName))
	}
	return paths, nil
}

func importBackupRoot() (string, error) {
	base, err := config.DefaultStateDir()
	if err != nil {
		return "", err
	}
	return base, nil
}

// The APM workspace and the omni config dir are rebuilt by other operations, so a recovery point
// living there can be cleaned by the very thing it exists to undo. Omni's state dir is not guarded:
// it is where backups belong, even though the sync marker also lives there.
func assertBackupDisjoint(dir string, guarded []string) error {
	resolved, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	for _, g := range guarded {
		if g == "" {
			continue
		}
		abs, err := filepath.Abs(g)
		if err != nil {
			return err
		}
		if resolved == abs || strings.HasPrefix(resolved, abs+string(filepath.Separator)) || strings.HasPrefix(abs, resolved+string(filepath.Separator)) {
			return fmt.Errorf("import backup %s overlaps managed path %s", resolved, abs)
		}
	}
	return nil
}

// guardedRoots are the trees a backup must not live in.
func guardedRoots() []string {
	var roots []string
	if dir, err := apm.GlobalWorkspaceDir(); err == nil {
		roots = append(roots, dir)
	}
	if dir, err := config.DefaultConfigDir(); err == nil {
		roots = append(roots, dir)
	}
	return roots
}

func importFileDigest(path string) (string, os.FileMode, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", 0, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", 0, fmt.Errorf("%s is a symlink; import backs up regular files only", path)
	}
	if !info.Mode().IsRegular() {
		return "", 0, fmt.Errorf("%s is not a regular file", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", 0, err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), info.Mode().Perm(), nil
}

func (a *App) CreateImportBackup(host string) (string, error) {
	boundary, err := a.importBoundary()
	if err != nil {
		return "", err
	}
	root, err := importBackupRoot()
	if err != nil {
		return "", err
	}
	base := filepath.Join(root, fmt.Sprintf("%s-%s-%s", importBackupPrefix, host, time.Now().UTC().Format("20060102-150405")))
	if err := testguard.RequireTempPath("import backup", base); err != nil {
		return "", err
	}
	if err := assertBackupDisjoint(base, guardedRoots()); err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	dir, err := mkdirUnique(base)
	if err != nil {
		return "", fmt.Errorf("create import backup: %w", err)
	}
	if err := assertPrivateDir(dir); err != nil {
		return "", err
	}

	backup := importBackup{Host: host, CreatedAt: time.Now().UTC()}
	for i, path := range boundary {
		entry := importBackupEntry{Path: path}
		sum, mode, err := importFileDigest(path)
		switch {
		case err == nil:
			entry.SHA256, entry.Mode = sum, uint32(mode)
			entry.Copy = fmt.Sprintf("%02d-%s", i, filepath.Base(path))
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				return "", readErr
			}
			if writeErr := os.WriteFile(filepath.Join(dir, entry.Copy), raw, mode); writeErr != nil {
				return "", writeErr
			}
		case os.IsNotExist(err):
			entry.Absent = true
		default:
			return "", err
		}
		backup.Entries = append(backup.Entries, entry)
	}
	if err := writeImportManifest(dir, backup); err != nil {
		return "", err
	}
	if err := pinFirstBackup(root, host, dir); err != nil {
		return "", err
	}
	return dir, nil
}

// Timestamps are second-granular, so two runs in one second must not collide.
func mkdirUnique(base string) (string, error) {
	for i := 0; ; i++ {
		candidate := base
		if i > 0 {
			candidate = fmt.Sprintf("%s.%d", base, i)
		}
		err := os.Mkdir(candidate, 0o700)
		if err == nil {
			return candidate, nil
		}
		if !os.IsExist(err) {
			return "", err
		}
		if i > 999 {
			return "", fmt.Errorf("exhausted unique names for %s", base)
		}
	}
}

func writeImportManifest(dir string, backup importBackup) error {
	raw, err := json.MarshalIndent(backup, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, importBackupManifest), append(raw, '\n'), 0o600)
}

// The first backup for a host is the only pre-import state that exists; rolling pruning must never reach it.
func pinFirstBackup(root, host, dir string) error {
	existing, err := listImportBackups(root, host)
	if err != nil {
		return err
	}
	for _, e := range existing {
		if e != dir {
			if _, err := os.Lstat(filepath.Join(e, importBackupPinned)); err == nil {
				return nil
			}
		}
	}
	return os.WriteFile(filepath.Join(dir, importBackupPinned), nil, 0o600)
}

func listImportBackups(root, host string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	prefix := fmt.Sprintf("%s-%s-", importBackupPrefix, host)
	var out []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
			out = append(out, filepath.Join(root, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

func assertPrivateDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("import backup root %s is a symlink", dir)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		return fmt.Errorf("import backup root %s has mode %04o, want 0700", dir, perm)
	}
	return nil
}

func readImportBackup(dir string) (importBackup, error) {
	var backup importBackup
	raw, err := os.ReadFile(filepath.Join(dir, importBackupManifest))
	if err != nil {
		return backup, fmt.Errorf("read import backup manifest: %w", err)
	}
	if err := json.Unmarshal(raw, &backup); err != nil {
		return backup, fmt.Errorf("parse import backup manifest: %w", err)
	}
	if len(backup.Entries) == 0 {
		return backup, fmt.Errorf("import backup %s lists no entries", dir)
	}
	return backup, nil
}

// VerifyImportBackup rehashes every copy: one that cannot be proven intact is not a recovery point.
func VerifyImportBackup(dir string) error {
	backup, err := readImportBackup(dir)
	if err != nil {
		return err
	}
	for _, entry := range backup.Entries {
		if entry.Absent {
			continue
		}
		sum, _, err := importFileDigest(filepath.Join(dir, entry.Copy))
		if err != nil {
			return fmt.Errorf("verify %s: %w", entry.Path, err)
		}
		if sum != entry.SHA256 {
			return fmt.Errorf("verify %s: content hash changed since backup", entry.Path)
		}
	}
	return assertPrivateDir(dir)
}

func RestoreImportBackup(dir string) error {
	if err := VerifyImportBackup(dir); err != nil {
		return fmt.Errorf("refusing to restore an unverified backup: %w", err)
	}
	backup, err := readImportBackup(dir)
	if err != nil {
		return err
	}
	for _, entry := range backup.Entries {
		if entry.Absent {
			if err := os.Remove(entry.Path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("restore %s: %w", entry.Path, err)
			}
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Copy))
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(entry.Path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(entry.Path, raw, os.FileMode(entry.Mode)); err != nil {
			return fmt.Errorf("restore %s: %w", entry.Path, err)
		}
	}
	return nil
}
