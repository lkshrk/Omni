//go:build !windows

package agent

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func lockFile(file *os.File) error {
	for {
		err := unix.Flock(int(file.Fd()), unix.LOCK_EX)
		// A blocking flock is interruptible, and the runtime's async preemption signal alone trips it.
		if errors.Is(err, unix.EINTR) {
			continue
		}
		return err
	}
}

func unlockFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
