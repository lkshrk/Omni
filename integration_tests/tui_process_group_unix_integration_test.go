//go:build integration && !windows

package integration_test

import (
	"os"
	"syscall"
)

// The pty starts the TUI with Setsid, so it leads its own process group and everything it spawns joins
// that group. Signalling the process alone orphans those children, which then run on into later tests.
func killTUIProcessTree(p *os.Process) {
	if p == nil {
		return
	}
	if err := syscall.Kill(-p.Pid, syscall.SIGKILL); err != nil {
		// The group is already gone, so the process is too; nothing is left to reap.
		_ = p.Kill()
	}
}
