package dots

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// resolveSymlinkTarget returns the absolute, clean path that a symlink at
// linkPath points to. Stow creates relative symlinks, so we resolve them
// against the directory containing the link.
func resolveSymlinkTarget(linkPath, target string) string {
	if filepath.IsAbs(target) {
		return filepath.Clean(target)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(linkPath), target))
}

// ─── unlink helpers ───────────────────────────────────────────────────────────

// unlinkEntry removes managed symlinks for entry e and copies the repo files
// to the target, restoring a clean local state.
//
// It handles two layouts:
//  1. Stow directory-level: TargetPath is itself a symlink → SourcePath.
//     The symlink is removed and the source tree is copied to TargetPath.
//  2. File-level (legacy): TargetPath is a real directory; individual file
//     symlinks inside it are checked and replaced with real copies.
func unlinkEntry(e ResolvedEntry, opts UnlinkOptions) ([]Op, error) {
	if opts.RemoveLocal {
		if err := os.RemoveAll(e.TargetPath); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("remove local target %q: %w", e.TargetPath, err)
		}
		return []Op{{Kind: OpUnlink, Entry: e.Name, Src: e.SourcePath, Dst: e.TargetPath}}, nil
	}

	// If the source doesn't exist in the repo, there's nothing to unlink.
	srcInfo, err := os.Lstat(e.SourcePath)
	if err != nil {
		return nil, nil
	}

	// Check if TargetPath itself is a stow-managed directory symlink.
	if tgtInfo, tgtErr := os.Lstat(e.TargetPath); tgtErr == nil && tgtInfo.Mode()&os.ModeSymlink != 0 {
		existing, readErr := os.Readlink(e.TargetPath)
		if readErr == nil && resolveSymlinkTarget(e.TargetPath, existing) == filepath.Clean(e.SourcePath) {
			// Our stow-managed directory link → remove and copy source tree.
			if _, backupErr := BackupLocalPath(e.TargetPath); backupErr != nil {
				return nil, fmt.Errorf("backup stow symlink %q: %w", e.TargetPath, backupErr)
			}
			if rmErr := os.Remove(e.TargetPath); rmErr != nil {
				return nil, fmt.Errorf("remove stow symlink %q: %w", e.TargetPath, rmErr)
			}
			if cpErr := copyDir(e.SourcePath, e.TargetPath, e.Ignore); cpErr != nil {
				return nil, fmt.Errorf("copy %q → %q: %w", e.SourcePath, e.TargetPath, cpErr)
			}
			return []Op{{Kind: OpUnlink, Entry: e.Name, Src: e.SourcePath, Dst: e.TargetPath}}, nil
		}
		// Symlink points elsewhere — not ours; nothing to do.
		return []Op{{Kind: OpUnlinkSkip, Entry: e.Name, Src: e.SourcePath, Dst: e.TargetPath}}, nil
	}

	// TargetPath is not a directory symlink. Fall back to file-level walk.
	if !srcInfo.IsDir() {
		op, err := unlinkFile(e.Name, "", e.SourcePath, e.TargetPath, opts)
		if err != nil {
			return nil, err
		}
		return []Op{op}, nil
	}

	var ops []Op
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
		if ShouldIgnorePath(rel, d.Name(), e.Ignore) {
			if d.IsDir() {
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

// unlinkFile handles one src→dst pair for the unlink operation.
// Managed symlinks are replaced by a real file copy from src.
// Non-managed real files are handled according to opts.ConflictOverwrite.
func unlinkFile(entryName, rel, src, dst string, opts UnlinkOptions) (Op, error) {
	fileLabel := rel
	if fileLabel == "" {
		fileLabel = filepath.Base(src)
	}

	info, err := os.Lstat(dst)
	if os.IsNotExist(err) {
		// Target never existed — nothing to unlink.
		return Op{Kind: OpUnlinkSkip, Entry: entryName, File: fileLabel, Src: src, Dst: dst}, nil
	}
	if err != nil {
		return Op{}, fmt.Errorf("lstat %q: %w", dst, err)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		existing, readErr := os.Readlink(dst)
		if readErr != nil || resolveSymlinkTarget(dst, existing) != filepath.Clean(src) {
			// Wrong symlink — not ours; skip.
			return Op{Kind: OpUnlinkSkip, Entry: entryName, File: fileLabel, Src: src, Dst: dst}, nil
		}
		// Our managed symlink — replace with a real copy.
		if _, backupErr := BackupLocalPath(dst); backupErr != nil {
			return Op{}, fmt.Errorf("backup symlink %q: %w", dst, backupErr)
		}
		if rmErr := os.Remove(dst); rmErr != nil {
			return Op{}, fmt.Errorf("remove symlink %q: %w", dst, rmErr)
		}
		if cpErr := copyFile(src, dst); cpErr != nil {
			return Op{}, fmt.Errorf("copy %q → %q: %w", src, dst, cpErr)
		}
		return Op{Kind: OpUnlink, Entry: entryName, File: fileLabel, Src: src, Dst: dst}, nil
	}

	// Real file/dir at dst — conflict.
	if opts.KeepExistingLocal {
		return Op{Kind: OpUnlinkSkip, Entry: entryName, File: fileLabel, Src: src, Dst: dst}, nil
	}
	if opts.ConflictOverwrite {
		if _, backupErr := BackupLocalPath(dst); backupErr != nil {
			return Op{}, fmt.Errorf("backup %q: %w", dst, backupErr)
		}
		if cpErr := copyFile(src, dst); cpErr != nil {
			return Op{}, fmt.Errorf("overwrite %q: %w", dst, cpErr)
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

// copyFile copies src to dst, preserving the source file mode.
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

// copyDir recursively copies the directory tree at src to dst.
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
