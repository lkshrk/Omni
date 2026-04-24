package dots

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const BackupDirName = "dotfiles.bkp"

// BackupLocalPath copies path into ~/dotfiles.bkp before callers mutate the
// live target. The backup path mirrors the target's home-relative path.
func BackupLocalPath(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	dst, err := backupDestination(path)
	if err != nil {
		return "", err
	}
	if err := backupCopyPath(path, dst, info); err != nil {
		return "", err
	}
	return dst, nil
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
