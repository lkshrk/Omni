//go:build !windows

package executor

import (
	"os/exec"
	"syscall"
)

// Each command leads its own process group so cancelling it stops whatever it started too: killing the
// direct child leaves its own children running, and they outlive omni as orphans.
func isolateProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			// The group is gone already, so fall back to the process the command started.
			return cmd.Process.Kill()
		}
		return nil
	}
}
