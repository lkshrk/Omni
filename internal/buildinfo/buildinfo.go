// Package buildinfo resolves the version from -ldflags -X, else Go's embedded module metadata.
package buildinfo

import (
	"fmt"
	"runtime/debug"
	"sync"
)

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

func Short() string {
	return resolve().version
}

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
