//go:build windows

package executor

import "os/exec"

// Windows has no process groups to place the command in; the default cancel behaviour stands.
func isolateProcessGroup(*exec.Cmd) {}
