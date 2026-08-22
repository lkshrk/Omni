//go:build windows

package config

import (
	"golang.org/x/sys/windows"
	"os"
)

func lockFile(f *os.File) error {
	var o windows.Overlapped
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &o)
}
func unlockFile(f *os.File) error {
	var o windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &o)
}
