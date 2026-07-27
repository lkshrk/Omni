package app

import "github.com/lkshrk/omni/internal/version"

// Strips a leading "v" so "v2.93.0" and "2.93.0" compare equal.
func normalizeFallbackVersion(v string) string {
	return version.Normalize(v)
}

// False when either version is empty or unparseable, so callers can fall back to other signals.
func fallbackVersionNewer(candidate, current string) (newer bool, ok bool) {
	return version.Newer(candidate, current)
}
