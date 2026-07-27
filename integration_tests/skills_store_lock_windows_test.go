//go:build integration && windows

package integration_test

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

// Takes the same advisory lock omni takes, so a second process must queue behind the test.
func holdStoreLock(t *testing.T, path string) func() {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	handle := windows.Handle(file.Fd())
	if err := windows.LockFileEx(
		handle, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, new(windows.Overlapped)); err != nil {
		_ = file.Close()
		t.Fatalf("locking %s: %v", path, err)
	}
	return func() {
		_ = windows.UnlockFileEx(handle, 0, 1, 0, new(windows.Overlapped))
		_ = file.Close()
	}
}
