//go:build integration && windows

package integration_test

import "os"

func killTUIProcessTree(p *os.Process) {
	if p == nil {
		return
	}
	// Windows has no process groups to signal here; the job object owns the tree.
	_ = p.Kill()
}
