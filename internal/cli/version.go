package cli

import "github.com/lkshrk/omni/internal/buildinfo"

// Version reports the binary's full version string (version, commit, date).
// Injection happens in internal/buildinfo via -ldflags; see that package for
// the go-install fallback.
func Version() string {
	return buildinfo.Full()
}
