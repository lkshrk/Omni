// Package version compares simple numeric release versions without guessing
// an ordering for provider-specific labels.
package version

import (
	"strconv"
	"strings"
)

// Normalize trims whitespace and the conventional lowercase v prefix.
func Normalize(value string) string {
	return strings.TrimPrefix(strings.TrimSpace(value), "v")
}

// Newer reports whether candidate is strictly newer than current. Versions
// must contain one to four dot-separated numeric components. Missing trailing
// components compare as zero, so 1.2 and 1.2.0 are equal.
func Newer(candidate, current string) (newer bool, comparable bool) {
	candidate = Normalize(candidate)
	current = Normalize(current)
	if candidate == "" || current == "" {
		return false, false
	}
	if candidate == current {
		return false, true
	}
	candidateParts, ok := numericParts(candidate)
	if !ok {
		return false, false
	}
	currentParts, ok := numericParts(current)
	if !ok {
		return false, false
	}
	count := max(len(candidateParts), len(currentParts))
	for i := 0; i < count; i++ {
		var candidatePart, currentPart uint64
		if i < len(candidateParts) {
			candidatePart = candidateParts[i]
		}
		if i < len(currentParts) {
			currentPart = currentParts[i]
		}
		if candidatePart > currentPart {
			return true, true
		}
		if candidatePart < currentPart {
			return false, true
		}
	}
	return false, true
}

func numericParts(value string) ([]uint64, bool) {
	parts := strings.Split(value, ".")
	if len(parts) == 0 || len(parts) > 4 {
		return nil, false
	}
	values := make([]uint64, len(parts))
	for i, part := range parts {
		if part == "" {
			return nil, false
		}
		value, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return nil, false
		}
		values[i] = value
	}
	return values, true
}
