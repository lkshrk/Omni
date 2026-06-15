package app

import (
	"strings"
)

// normalizeFallbackVersion strips a leading "v" and trims whitespace so that
// "v2.93.0" and "2.93.0" compare as equal. The result is suitable for
// semver-aware string comparison via compareFallbackVersions.
func normalizeFallbackVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	return v
}

// fallbackVersionNewer reports whether candidate is strictly newer than current
// using semver-aware comparison. Both inputs are normalized before comparison.
// Returns false (not newer) when either version is empty or cannot be parsed,
// so callers can fall back to other signals (e.g. published_at).
func fallbackVersionNewer(candidate, current string) (newer bool, ok bool) {
	c := normalizeFallbackVersion(candidate)
	cur := normalizeFallbackVersion(current)
	if c == "" || cur == "" {
		return false, false
	}
	// Fast path: identical normalized strings.
	if c == cur {
		return false, true
	}
	cParts := parseSemverParts(c)
	curParts := parseSemverParts(cur)
	if cParts == nil || curParts == nil {
		// One or both are non-semver — fall back to lexicographic comparison
		// only when both look like a version string (digits present).
		if !looksLikeVersion(c) || !looksLikeVersion(cur) {
			return false, false
		}
		return c > cur, true
	}
	for i := range cParts {
		if cParts[i] > curParts[i] {
			return true, true
		}
		if cParts[i] < curParts[i] {
			return false, true
		}
	}
	return false, true
}

// parseSemverParts splits "MAJOR.MINOR.PATCH" into [3]int.
// Returns nil when the string does not match a strict semver triple.
func parseSemverParts(v string) []int {
	// Accept up to 4 numeric components; anything with letters (pre-release) is
	// not parsed so callers can fall back.
	parts := strings.Split(v, ".")
	if len(parts) < 2 || len(parts) > 4 {
		return nil
	}
	nums := make([]int, len(parts))
	for i, p := range parts {
		n, ok := parseUint(p)
		if !ok {
			return nil
		}
		nums[i] = n
	}
	return nums
}

// parseUint parses a non-negative integer from a string without allocating.
func parseUint(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// looksLikeVersion reports whether s contains at least one digit, indicating
// it could be a version string even if not strictly semver.
func looksLikeVersion(s string) bool {
	for _, c := range s {
		if c >= '0' && c <= '9' {
			return true
		}
	}
	return false
}

// parseFallbackVersionOutput extracts the first version-looking token from
// the output of a version command (e.g. "gh version 2.93.0 (2026-05-27)").
// Returns the raw token (not normalized) so callers can pass it to normalizeFallbackVersion.
// Returns "" when no version token is found.
func parseFallbackVersionOutput(output string) string {
	// Walk word by word; the first token that looks like a version wins.
	for _, word := range strings.Fields(output) {
		w := strings.TrimPrefix(word, "v")
		if looksLikeVersion(w) && strings.Contains(w, ".") {
			return strings.TrimPrefix(word, "v")
		}
	}
	return ""
}
