//go:build !windows

// Package flock wraps the platform's advisory whole-file locks.
package flock

import (
	"errors"
	"os"
	"syscall"
)

// Lock blocks until the exclusive lock is held.
func Lock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX) }

// TryLock reports whether the exclusive lock was taken without waiting.
func TryLock(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, syscall.EWOULDBLOCK):
		return false, nil
	default:
		return false, err
	}
}

func Unlock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
