package app

import "github.com/lkshrk/omni/internal/version"

// normalizeFallbackVersion strips a leading "v" and trims whitespace so that
// "v2.93.0" and "2.93.0" compare as equal. The result is suitable for
// semver-aware string comparison via compareFallbackVersions.
func normalizeFallbackVersion(v string) string {
	return version.Normalize(v)
}

// fallbackVersionNewer reports whether candidate is strictly newer than current
// using semver-aware comparison. Both inputs are normalized before comparison.
// Returns false (not newer) when either version is empty or cannot be parsed,
// so callers can fall back to other signals (e.g. published_at).
func fallbackVersionNewer(candidate, current string) (newer bool, ok bool) {
	return version.Newer(candidate, current)
}
