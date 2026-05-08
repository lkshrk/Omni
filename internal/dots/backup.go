package dots

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const BackupDirName = "dotfiles.bkp"

// BackupLocalPath copies path into ~/dotfiles.bkp before callers mutate the
// live target. The backup path mirrors the target's home-relative path.
func BackupLocalPath(path string) (string, error) {
	return BackupLocalPathFrom(path, path)
}

// BackupLocalPathFrom copies source into the backup destination derived from
// path. Use this when path is a managed symlink but the durable safety copy
// should contain the linked repo content before that repo content is removed.
func BackupLocalPathFrom(path, source string) (string, error) {
	info, err := os.Lstat(source)
	if err != nil {
		return "", err
	}
	dst, err := backupDestination(path)
	if err != nil {
		return "", err
	}
	if err := backupCopyPath(source, dst, info); err != nil {
		return "", err
	}
	return dst, nil
}

// BackupAndRemoveLocalPath creates a backup, then moves path to the user's
// trash. Symlinks are only unlinked because their targets are not local data
// owned by the link path. If path is already absent it is a no-op, but if a
// real target exists without a readable backup, removal is refused.
func BackupAndRemoveLocalPath(path string) (string, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("stat local target %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", RemoveLocalPathAfterBackup(path, "")
	}

	backupPath, err := BackupLocalPath(path)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("backup %q: %w", path, err)
	}
	if err := RemoveLocalPathAfterBackup(path, backupPath); err != nil {
		return backupPath, err
	}
	return backupPath, nil
}

// RemoveLocalPathAfterBackup moves path to trash only after proving backupPath
// exists. Symlinks are unlinked without a backup because unlinking the link does
// not delete the target data. This is the guard for flows that must copy local
// content elsewhere before replacement, such as "use local" conflict resolution.
func RemoveLocalPathAfterBackup(path, backupPath string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat local target %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		if backupPath != "" {
			if _, err := os.Lstat(backupPath); err != nil {
				return fmt.Errorf("refusing to unlink %q without readable backup %q: %w", path, backupPath, err)
			}
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("unlink %q: %w", path, err)
		}
		return nil
	}
	if backupPath == "" {
		return fmt.Errorf("refusing to remove %q without backup", path)
	}
	if _, err := os.Lstat(backupPath); err != nil {
		return fmt.Errorf("refusing to remove %q without readable backup %q: %w", path, backupPath, err)
	}
	if _, err := moveLocalPathToTrash(path, info); err != nil {
		return fmt.Errorf("trash %q: %w", path, err)
	}
	return nil
}

func moveLocalPathToTrash(path string, info os.FileInfo) (string, error) {
	dst, err := trashDestination(path)
	if err != nil {
		return "", err
	}
	if err := os.Rename(path, dst); err == nil {
		return dst, nil
	}
	if err := backupCopyPath(path, dst, info); err != nil {
		return "", err
	}
	if err := os.RemoveAll(path); err != nil && !os.IsNotExist(err) {
		_ = os.RemoveAll(dst)
		return "", err
	}
	return dst, nil
}

func trashDestination(path string) (string, error) {
	root, err := trashRoot()
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	rootAbs = filepath.Clean(rootAbs)
	if abs == rootAbs || strings.HasPrefix(abs, rootAbs+string(filepath.Separator)) {
		return "", fmt.Errorf("refusing to trash %q inside %s", abs, rootAbs)
	}
	if err := os.MkdirAll(rootAbs, 0o700); err != nil {
		return "", fmt.Errorf("create trash dir: %w", err)
	}
	return uniqueBackupDestination(filepath.Join(rootAbs, filepath.Base(abs))), nil
}

func trashRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, ".Trash"), nil
	case "windows":
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			return filepath.Join(localAppData, "Trash", "files"), nil
		}
		return filepath.Join(home, ".Trash"), nil
	default:
		if xdgDataHome := os.Getenv("XDG_DATA_HOME"); xdgDataHome != "" {
			return filepath.Join(xdgDataHome, "Trash", "files"), nil
		}
		return filepath.Join(home, ".local", "share", "Trash", "files"), nil
	}
}

func backupDestination(path string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	root := filepath.Join(home, BackupDirName)
	if abs == root || strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return "", fmt.Errorf("refusing to back up %q inside %s", abs, root)
	}

	rel, err := filepath.Rel(home, abs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		rel = filepath.Base(abs)
	}
	return uniqueBackupDestination(filepath.Join(root, rel)), nil
}

func uniqueBackupDestination(path string) string {
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return path
	}
	for i := 1; ; i++ {
		next := fmt.Sprintf("%s.%d", path, i)
		if _, err := os.Lstat(next); os.IsNotExist(err) {
			return next
		}
	}
}

func backupCopyPath(src, dst string, info os.FileInfo) error {
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
	if info.IsDir() {
		return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, relErr := filepath.Rel(src, path)
			if relErr != nil {
				return relErr
			}
			target := filepath.Join(dst, rel)
			entryInfo, infoErr := os.Lstat(path)
			if infoErr != nil {
				return infoErr
			}
			if entryInfo.IsDir() && entryInfo.Mode()&os.ModeSymlink == 0 {
				return os.MkdirAll(target, entryInfo.Mode().Perm())
			}
			return backupCopyPath(path, target, entryInfo)
		})
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	return backupCopyFile(src, dst, info.Mode())
}

func backupCopyFile(src, dst string, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
