package dots

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Stow writes relative symlinks, so resolve against the directory containing the link.
func resolveSymlinkTarget(linkPath, target string) string {
	if filepath.IsAbs(target) {
		return filepath.Clean(target)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(linkPath), target))
}

// StowRelativeSymlinkTarget — The exact relative text GNU Stow would write: targetPath relative to linkPath's own directory.
func StowRelativeSymlinkTarget(linkPath, targetPath string) (string, error) {
	return filepath.Rel(filepath.Dir(linkPath), targetPath)
}

// WriteStowShapedSymlink — Shaped as stow writes them so stow -R accepts them; the caller must confirm the link resolves to targetPath.
func WriteStowShapedSymlink(linkPath, targetPath string) error {
	if err := ValidateHomeTargetPath(linkPath); err != nil {
		return fmt.Errorf("refusing to write symlink %q: %w", linkPath, err)
	}
	rel, err := StowRelativeSymlinkTarget(linkPath, targetPath)
	if err != nil {
		return fmt.Errorf("compute stow-relative target for %q: %w", linkPath, err)
	}
	tmp := linkPath + ".omc-relink-tmp"
	if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clear stale relink temp %q: %w", tmp, err)
	}
	if err := os.Symlink(rel, tmp); err != nil {
		return fmt.Errorf("create stow-shaped symlink %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, linkPath); err != nil {
		if removeErr := os.Remove(tmp); removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("relink %q: %w (cleanup failed: %v)", linkPath, err, removeErr)
		}
		return fmt.Errorf("relink %q: %w", linkPath, err)
	}
	return nil
}

// Two layouts: TargetPath itself a stow directory symlink, or a real directory of per-file symlinks.
func unlinkEntry(e ResolvedEntry, opts UnlinkOptions) ([]Op, error) {
	if opts.RemoveLocal {
		if _, err := backupAndRemoveUnlinkedTarget(e); err != nil {
			return nil, fmt.Errorf("remove local target %q: %w", e.TargetPath, err)
		}
		return []Op{{Kind: OpUnlink, Entry: e.Name, Src: e.SourcePath, Dst: e.TargetPath}}, nil
	}

	srcInfo, err := os.Lstat(e.SourcePath)
	if err != nil {
		return nil, nil
	}

	if tgtInfo, tgtErr := os.Lstat(e.TargetPath); tgtErr == nil && tgtInfo.Mode()&os.ModeSymlink != 0 {
		existing, readErr := os.Readlink(e.TargetPath)
		if readErr == nil && resolveSymlinkTarget(e.TargetPath, existing) == filepath.Clean(e.SourcePath) {
			if _, err := backupAndRemoveManagedLink(e.TargetPath); err != nil {
				return nil, fmt.Errorf("remove stow symlink %q: %w", e.TargetPath, err)
			}
			if cpErr := copyDir(e.SourcePath, e.TargetPath, e.Ignore); cpErr != nil {
				return nil, fmt.Errorf("copy %q → %q: %w", e.SourcePath, e.TargetPath, cpErr)
			}
			return []Op{{Kind: OpUnlink, Entry: e.Name, Src: e.SourcePath, Dst: e.TargetPath}}, nil
		}
		return []Op{{Kind: OpUnlinkSkip, Entry: e.Name, Src: e.SourcePath, Dst: e.TargetPath}}, nil
	}

	if !srcInfo.IsDir() {
		op, err := unlinkFile(e.Name, "", e.SourcePath, e.TargetPath, opts)
		if err != nil {
			return nil, err
		}
		return []Op{op}, nil
	}

	var ops []Op
	im := CompileIgnoresLenient(e.Ignore)
	walkErr := filepath.WalkDir(e.SourcePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(e.SourcePath, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		if im.Ignored(rel, d.Name()) {
			if d.IsDir() {
				if im.HasIncludedDescendant(rel) {
					return nil
				}
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		dst := filepath.Join(e.TargetPath, rel)
		op, opErr := unlinkFile(e.Name, rel, path, dst, opts)
		if opErr != nil {
			return opErr
		}
		ops = append(ops, op)
		return nil
	})
	return ops, walkErr
}

func backupAndRemoveUnlinkedTarget(e ResolvedEntry) (string, error) {
	info, err := os.Lstat(e.TargetPath)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("stat local target %q: %w", e.TargetPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		if target, readErr := os.Readlink(e.TargetPath); readErr == nil && resolveSymlinkTarget(e.TargetPath, target) == filepath.Clean(e.SourcePath) {
			backupPath, backupErr := BackupLocalPathFrom(e.TargetPath, e.SourcePath)
			if os.IsNotExist(backupErr) {
				backupPath, backupErr = BackupLocalPath(e.TargetPath)
			}
			if backupErr != nil && !os.IsNotExist(backupErr) {
				return "", fmt.Errorf("backup %q: %w", e.TargetPath, backupErr)
			}
			if removeErr := RemoveLocalPathAfterBackup(e.TargetPath, backupPath); removeErr != nil {
				return backupPath, removeErr
			}
			return backupPath, nil
		}
		return "", RemoveLocalPathAfterBackup(e.TargetPath, "")
	}
	sourceInfo, sourceErr := os.Lstat(e.SourcePath)
	if sourceErr == nil && sourceInfo.IsDir() && sourceInfo.Mode()&os.ModeSymlink == 0 && info.IsDir() {
		hasLinks, walkErr := targetDirectoryHasManagedLinks(e)
		if walkErr != nil {
			// Fail closed: a walk error means we cannot confirm the directory is safe to clobber.
			return "", fmt.Errorf("scan managed links in %q: %w", e.TargetPath, walkErr)
		}
		if hasLinks {
			backupPath, backupErr := BackupLocalPathFrom(e.TargetPath, e.SourcePath)
			if backupErr != nil && !os.IsNotExist(backupErr) {
				return "", fmt.Errorf("backup %q: %w", e.TargetPath, backupErr)
			}
			if removeErr := RemoveLocalPathAfterBackup(e.TargetPath, backupPath); removeErr != nil {
				return backupPath, removeErr
			}
			return backupPath, nil
		}
	}
	return BackupAndRemoveLocalPath(e.TargetPath)
}

func targetDirectoryHasManagedLinks(e ResolvedEntry) (bool, error) {
	found := false
	im := CompileIgnoresLenient(e.Ignore)
	walkErr := filepath.WalkDir(e.SourcePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if found {
			return filepath.SkipAll
		}
		rel, relErr := filepath.Rel(e.SourcePath, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		if im.Ignored(rel, d.Name()) {
			if d.IsDir() {
				if im.HasIncludedDescendant(rel) {
					return nil
				}
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		targetPath := filepath.Join(e.TargetPath, rel)
		info, infoErr := os.Lstat(targetPath)
		if infoErr != nil || info.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		target, readErr := os.Readlink(targetPath)
		if readErr == nil && resolveSymlinkTarget(targetPath, target) == filepath.Clean(path) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	if walkErr != nil {
		return false, walkErr
	}
	return found, nil
}

func backupAndRemoveManagedLink(path string) (string, error) {
	backupPath, err := backupManagedLinkContent(path)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("backup %q: %w", path, err)
	}
	if err := RemoveLocalPathAfterBackup(path, backupPath); err != nil {
		return backupPath, err
	}
	return backupPath, nil
}

func backupManagedLinkContent(path string) (string, error) {
	target, err := os.Readlink(path)
	if err != nil {
		return BackupLocalPath(path)
	}
	source := resolveSymlinkTarget(path, target)
	backupPath, err := BackupLocalPathFrom(path, source)
	if os.IsNotExist(err) {
		return BackupLocalPath(path)
	}
	return backupPath, err
}

// Managed symlinks become real copies of src; non-managed real files follow opts.ConflictOverwrite.
func unlinkFile(entryName, rel, src, dst string, opts UnlinkOptions) (Op, error) {
	fileLabel := rel
	if fileLabel == "" {
		fileLabel = filepath.Base(src)
	}

	info, err := os.Lstat(dst)
	if os.IsNotExist(err) {
		return Op{Kind: OpUnlinkSkip, Entry: entryName, File: fileLabel, Src: src, Dst: dst}, nil
	}
	if err != nil {
		return Op{}, fmt.Errorf("lstat %q: %w", dst, err)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		existing, readErr := os.Readlink(dst)
		if readErr != nil || resolveSymlinkTarget(dst, existing) != filepath.Clean(src) {
			return Op{Kind: OpUnlinkSkip, Entry: entryName, File: fileLabel, Src: src, Dst: dst}, nil
		}
		if _, err := backupAndRemoveManagedLink(dst); err != nil {
			return Op{}, fmt.Errorf("remove symlink %q: %w", dst, err)
		}
		if cpErr := copyFile(src, dst); cpErr != nil {
			return Op{}, fmt.Errorf("copy %q → %q: %w", src, dst, cpErr)
		}
		return Op{Kind: OpUnlink, Entry: entryName, File: fileLabel, Src: src, Dst: dst}, nil
	}

	if opts.KeepExistingLocal {
		return Op{Kind: OpUnlinkSkip, Entry: entryName, File: fileLabel, Src: src, Dst: dst}, nil
	}
	if opts.ConflictOverwrite {
		tmpPath, stageErr := stagedCopyFile(src, dst)
		if stageErr != nil {
			return Op{}, fmt.Errorf("stage overwrite %q: %w", dst, stageErr)
		}
		backupPath, backupErr := BackupLocalPath(dst)
		if backupErr != nil {
			_ = os.Remove(tmpPath)
			return Op{}, fmt.Errorf("backup %q: %w", dst, backupErr)
		}
		if removeErr := RemoveLocalPathAfterBackup(dst, backupPath); removeErr != nil {
			_ = os.Remove(tmpPath)
			return Op{}, fmt.Errorf("remove existing %q: %w", dst, removeErr)
		}
		if renameErr := os.Rename(tmpPath, dst); renameErr != nil {
			_ = os.Remove(tmpPath)
			return Op{}, fmt.Errorf("install staged overwrite %q: %w", dst, renameErr)
		}
		return Op{Kind: OpUnlink, Entry: entryName, File: fileLabel, Src: src, Dst: dst}, nil
	}
	return Op{
		Kind:  OpUnlinkConflict,
		Entry: entryName,
		File:  fileLabel,
		Src:   src,
		Dst:   dst,
		Err:   fmt.Errorf("real file at %q; use --overwrite to replace it", dst),
	}, nil
}

func copyFile(src, dst string) error {
	srcInfo, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if srcInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("source %q is a symlink; refusing to copy linked external content", src)
	}
	if !srcInfo.Mode().IsRegular() {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func stagedCopyFile(src, dst string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".tmp-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := os.Remove(tmpPath); err != nil {
		return "", err
	}
	if err := copyFile(src, tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	return tmpPath, nil
}

func copyDir(src, dst string, ignores []string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		if rel != "." && ShouldIgnorePath(rel, d.Name(), ignores) {
			if d.IsDir() {
				if HasIncludedDescendant(rel, ignores) {
					return nil
				}
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}
