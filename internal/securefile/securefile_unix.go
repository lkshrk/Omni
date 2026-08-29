//go:build !windows

package securefile

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

var securefileSwapTestHook func() error

func secureReadSource(path string, limit int64) (data []byte, retErr error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer func() { retErr = errors.Join(retErr, f.Close()) }()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return nil, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return nil, errors.New("copy source is not regular")
	}
	return io.ReadAll(io.LimitReader(f, limit))
}

func secureMkdirAll(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	root, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	parts := strings.Split(strings.TrimPrefix(abs, string(filepath.Separator)), string(filepath.Separator))
	child, walkErr := walkDirFD(root, parts, true)
	closeRoot := unix.Close(root)
	if walkErr != nil {
		return errors.Join(walkErr, closeRoot)
	}
	chmodErr := unix.Fchmod(child, 0o700)
	return errors.Join(chmodErr, unix.Close(child), closeRoot)
}

func secureMkdirRoot(root, relative string) (retErr error) {
	fd, err := openRootFD(root)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, unix.Close(fd)) }()
	child, err := walkDirFD(fd, strings.Split(relative, string(filepath.Separator)), true)
	if child >= 0 {
		retErr = errors.Join(retErr, unix.Close(child))
	}
	return errors.Join(err, retErr)
}

func secureWriteRoot(root, relative string, data []byte) (retErr error) {
	fd, err := openRootFD(root)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, unix.Close(fd)) }()
	parts := strings.Split(relative, string(filepath.Separator))
	parentParts, leaf := parts[:len(parts)-1], parts[len(parts)-1]
	parent := fd
	if len(parentParts) > 0 {
		parent, err = walkDirFD(fd, parentParts, true)
		if err != nil {
			return err
		}
		defer func() { retErr = errors.Join(retErr, unix.Close(parent)) }()
	}
	nonce := make([]byte, 8)
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	tmp := ".secure-" + hex.EncodeToString(nonce)
	tmpfd, err := unix.Openat(parent, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(tmpfd), tmp)
	closed := false
	defer func() {
		if !closed {
			retErr = errors.Join(retErr, f.Close())
		}
		if err := unix.Unlinkat(parent, tmp, 0); err != nil && !errors.Is(err, unix.ENOENT) {
			retErr = errors.Join(retErr, err)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		closed = true
		return err
	}
	closed = true
	if securefileSwapTestHook != nil {
		if err = securefileSwapTestHook(); err != nil {
			return err
		}
	}
	if err = unix.Renameat(parent, tmp, parent, leaf); err != nil {
		return err
	}
	return unix.Fsync(parent)
}

func secureVerifyRoot(root, relative string) (retErr error) {
	fd, err := openRootFD(root)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, unix.Close(fd)) }()
	parts := strings.Split(relative, string(filepath.Separator))
	parent := fd
	if len(parts) > 1 {
		parent, err = walkDirFD(fd, parts[:len(parts)-1], false)
		if err != nil {
			return err
		}
		defer func() { retErr = errors.Join(retErr, unix.Close(parent)) }()
	}
	var st unix.Stat_t
	if err := unix.Fstatat(parent, parts[len(parts)-1], &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0o777 != 0o600 {
		return fmt.Errorf("private file has unsafe type/mode")
	}
	return nil
}

func secureRemoveRoot(root, relative string) (retErr error) {
	fd, err := openRootFD(root)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, unix.Close(fd)) }()
	parts := strings.Split(relative, string(filepath.Separator))
	parent := fd
	if len(parts) > 1 {
		parent, err = walkDirFD(fd, parts[:len(parts)-1], false)
		if err != nil {
			return err
		}
		defer func() { retErr = errors.Join(retErr, unix.Close(parent)) }()
	}
	return removeAt(parent, parts[len(parts)-1])
}

func openRootFD(root string) (int, error) {
	return unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
}
func walkDirFD(root int, parts []string, create bool) (int, error) {
	current, err := unix.Dup(root)
	if err != nil {
		return -1, err
	}
	for _, part := range parts {
		if create {
			if err := unix.Mkdirat(current, part, 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
				return -1, errors.Join(err, unix.Close(current))
			}
		}
		next, err := unix.Openat(current, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		closeErr := unix.Close(current)
		if err != nil {
			return -1, errors.Join(err, closeErr)
		}
		if closeErr != nil {
			return -1, closeErr
		}
		current = next
	}
	return current, nil
}
func removeAt(parent int, name string) error {
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), name)
	entries, readErr := file.ReadDir(-1)
	if readErr != nil {
		return errors.Join(readErr, file.Close())
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.Join(errors.New("refuse symlink during secure cleanup"), file.Close())
		}
		if entry.IsDir() {
			if err := removeAt(fd, entry.Name()); err != nil {
				return errors.Join(err, file.Close())
			}
		} else if err := unix.Unlinkat(fd, entry.Name(), 0); err != nil {
			return errors.Join(err, file.Close())
		}
	}
	if err := file.Close(); err != nil {
		return err
	}
	return unix.Unlinkat(parent, name, unix.AT_REMOVEDIR)
}

func secureVerifyDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		return fmt.Errorf("private directory %q has unsafe mode %v", path, info.Mode())
	}
	return nil
}
