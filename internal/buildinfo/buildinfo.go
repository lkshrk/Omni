// Package buildinfo carries the binary's version identity, shared by the CLI
// (--version, help) and the TUI header. Release values are injected via
// -ldflags -X; builds without injection (go install, plain go build) fall
// back to the module metadata Go embeds in every binary.
package buildinfo

import (
	"fmt"
	"runtime/debug"
	"sync"
)

// Injected at build time via
// -ldflags "-X github.com/lkshrk/omni/internal/buildinfo.Version=<tag> ...".
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

type info struct {
	version string
	commit  string
	date    string
}

// resolve merges the ldflags values with debug.ReadBuildInfo fallbacks so
// go-install and untagged builds still report the module version and VCS
// revision instead of a bare "dev".
var resolve = sync.OnceValue(func() info {
	out := info{version: Version, commit: Commit, date: Date}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return out
	}
	if out.version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		out.version = bi.Main.Version
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if out.commit == "" && len(s.Value) >= 7 {
				out.commit = s.Value[:7]
			}
		case "vcs.time":
			if out.date == "" {
				out.date = s.Value
			}
		}
	}
	return out
})

// Short returns just the version, e.g. "v0.9.10" or "dev".
func Short() string {
	return resolve().version
}

// Full returns the version with commit and date when known, e.g.
// "v0.9.10 (8cd2d04, 2026-07-17)".
func Full() string {
	r := resolve()
	switch {
	case r.commit != "" && r.date != "":
		return fmt.Sprintf("%s (%s, %s)", r.version, r.commit, r.date)
	case r.commit != "":
		return fmt.Sprintf("%s (%s)", r.version, r.commit)
	default:
		return r.version
	}
}
