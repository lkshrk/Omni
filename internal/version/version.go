// Package version compares numeric releases and refuses to order provider-specific labels.
package version

import (
	"regexp"
	"strconv"
	"strings"
)

func Normalize(value string) string {
	return strings.TrimPrefix(strings.TrimSpace(value), "v")
}

// Match only dotted numeric cores that Newer can order; the word boundary keeps embedded versions from outranking the real banner version.
var versionToken = regexp.MustCompile(`\bv?(\d+(?:\.\d+){1,3})`)

// Extract returns the most version-shaped token, using position only to break ties.
func Extract(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	matches := versionToken.FindAllStringSubmatch(line, -1)
	if matches == nil {
		return ""
	}
	best, bestScore := "", 0
	for i, match := range matches {
		score := tokenScore(match[0], match[1])
		if i == 0 || score > bestScore {
			best, bestScore = match[1], score
		}
	}
	return best
}

func tokenScore(token, core string) int {
	score := 0
	if strings.HasPrefix(token, "v") {
		score += 2
	}
	parts := strings.Split(core, ".")
	if len(parts) >= 3 {
		score++
	}
	// A four-digit-or-longer leading component is a year or a datestamp more often than a major, but
	// the penalty stays at 1 so a calendar version still ties a bare build number and wins on position.
	if len(parts[0]) >= 4 {
		score--
	}
	return score
}

// Newer — comparable is false unless both sides are 1-4 numeric parts; missing trailing parts are zero.
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
