package cli

import "github.com/lkshrk/omni/internal/buildinfo"

// Version — Injected in internal/buildinfo via -ldflags; that package holds the go-install fallback.
func Version() string {
	return buildinfo.Full()
}
