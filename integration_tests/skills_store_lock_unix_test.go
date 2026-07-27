//go:build integration && !windows

package integration_test

import (
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// Takes the same advisory lock omni takes, so a second process must queue behind the test.
func holdStoreLock(t *testing.T, path string) func() {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	for {
		err = unix.Flock(int(file.Fd()), unix.LOCK_EX)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		break
	}
	if err != nil {
		_ = file.Close()
		t.Fatalf("locking %s: %v", path, err)
	}
	return func() {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
	}
}
