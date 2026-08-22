//go:build !windows

package app

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

func captureFragmentIdentity(path string) (fragmentIdentity, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return fragmentIdentity{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fragmentIdentity{}, errors.New("symlink config fragment refused")
	}
	if !info.Mode().IsRegular() {
		return fragmentIdentity{}, errors.New("config fragment is not a regular file")
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fragmentIdentity{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fragmentIdentity{}, errors.New("config fragment ownership unavailable")
	}
	return fragmentIdentity{CanonicalPath: canonical, Kind: "file", Mode: uint32(info.Mode().Perm()), UID: stat.Uid, GID: stat.Gid}, nil
}
