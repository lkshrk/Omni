package dots

import (
	"fmt"
	"path/filepath"
)

// AuditSeverity classifies an audit finding.
type AuditSeverity string

const (
	AuditWarn AuditSeverity = "warn"
)

// AuditFinding describes one simplification opportunity in an ignore list.
type AuditFinding struct {
	Severity AuditSeverity `json:"severity"`
	Type     string        `json:"type"`
	Message  string        `json:"message"`
	// Indices of the patterns involved (0-based into the input slice).
	Indices []int `json:"indices"`
	// Patterns contains the raw pattern strings involved.
	Patterns []string `json:"patterns"`
}

// AuditIgnoreList analyses patterns for internal redundancy, contradictions,
// and dead entries. It does NOT compare against DefaultIgnores — only the
// caller-supplied slice is examined.
func AuditIgnoreList(patterns []string) []AuditFinding {
	if len(patterns) == 0 {
		return nil
	}

	parsed := make([]ignorePattern, len(patterns))
	for i, raw := range patterns {
		p, err := parseIgnorePattern(raw)
		if err != nil {
			continue // invalid patterns are a validation concern, not audit
		}
		parsed[i] = p
	}

	var findings []AuditFinding
	findings = append(findings, auditDuplicates(patterns)...)
	findings = append(findings, auditContradictions(patterns, parsed)...)
	findings = append(findings, auditRedundant(patterns, parsed)...)
	findings = append(findings, auditShadowedIncludes(patterns, parsed)...)
	return findings
}

// auditDuplicates finds identical patterns appearing more than once.
func auditDuplicates(patterns []string) []AuditFinding {
	seen := make(map[string]int, len(patterns))
	var findings []AuditFinding
	for i, p := range patterns {
		if prev, ok := seen[p]; ok {
			findings = append(findings, AuditFinding{
				Severity: AuditWarn,
				Type:     "duplicate",
				Message:  fmt.Sprintf("pattern %q appears at index %d and %d — remove the duplicate", p, prev, i),
				Indices:  []int{i, prev},
				Patterns: []string{p},
			})
		} else {
			seen[p] = i
		}
	}
	return findings
}

// auditContradictions detects include patterns that are later overridden by a
// matching ignore (or vice versa), making the earlier pattern dead.
// It simulates the full pattern list to determine if a pattern's effect is
// nullified by a later pattern of the opposite polarity.
func auditContradictions(patterns []string, parsed []ignorePattern) []AuditFinding {
	var findings []AuditFinding
	for i := range parsed {
		a := parsed[i]
		if a.glob == "" {
			continue
		}
		// Simulate the state after applying patterns[0..i] for target a.glob.
		stateAtI := simulateIgnored(a.glob, patterns[:i+1], parsed[:i+1])
		// Now simulate with the full list.
		stateAtEnd := simulateIgnored(a.glob, patterns, parsed)
		// If the state is the same with or without pattern i, it's not
		// necessarily a contradiction — check if a later pattern with opposite
		// polarity overrides it.
		if stateAtI == stateAtEnd {
			continue // pattern i's effect persists to the end
		}
		// Pattern i's effect was reversed. Find the first later pattern that
		// reverses it.
		for j := i + 1; j < len(parsed); j++ {
			b := parsed[j]
			if b.glob == "" || b.include == a.include {
				continue
			}
			if !globsOverlap(a.glob, b.glob) {
				continue
			}
			findings = append(findings, AuditFinding{
				Severity: AuditWarn,
				Type:     "contradiction",
				Message: fmt.Sprintf(
					"pattern %q at index %d is overridden by %q at index %d — remove %q",
					patterns[i], i, patterns[j], j, patterns[i]),
				Indices:  []int{i, j},
				Patterns: []string{patterns[i], patterns[j]},
			})
			break
		}
	}
	return findings
}

// auditRedundant detects non-include patterns that are already covered by a
// broader non-include pattern earlier in the list, with no un-ignore in between
// that would reverse the effect.
func auditRedundant(patterns []string, parsed []ignorePattern) []AuditFinding {
	var findings []AuditFinding
	for j := 1; j < len(parsed); j++ {
		b := parsed[j]
		if b.glob == "" || b.include {
			continue // only check non-include (ignore) patterns
		}
		// Simulate the list up to j-1 to see if b's target is already ignored.
		alreadyIgnored := simulateIgnored(b.glob, patterns[:j], parsed[:j])
		if alreadyIgnored {
			findings = append(findings, AuditFinding{
				Severity: AuditWarn,
				Type:     "redundant",
				Message:  fmt.Sprintf("pattern %q at index %d is already covered by earlier patterns — remove it", patterns[j], j),
				Indices:  []int{j},
				Patterns: []string{patterns[j]},
			})
		}
	}
	return findings
}

// auditShadowedIncludes detects include (!) patterns that have no effect
// because nothing before them ignores the target.
func auditShadowedIncludes(patterns []string, parsed []ignorePattern) []AuditFinding {
	var findings []AuditFinding
	for j := 0; j < len(parsed); j++ {
		b := parsed[j]
		if b.glob == "" || !b.include {
			continue // only check include patterns
		}
		if j == 0 {
			// Nothing before it — always shadowed.
			findings = append(findings, AuditFinding{
				Severity: AuditWarn,
				Type:     "shadowed",
				Message:  fmt.Sprintf("pattern %q at index %d has no effect — nothing before it ignores this path", patterns[j], j),
				Indices:  []int{j},
				Patterns: []string{patterns[j]},
			})
			continue
		}
		// Check if the target would be ignored by patterns[:j].
		ignored := simulateIgnored(b.glob, patterns[:j], parsed[:j])
		if !ignored {
			findings = append(findings, AuditFinding{
				Severity: AuditWarn,
				Type:     "shadowed",
				Message:  fmt.Sprintf("pattern %q at index %d has no effect — nothing before it ignores this path", patterns[j], j),
				Indices:  []int{j},
				Patterns: []string{patterns[j]},
			})
		}
	}
	return findings
}

// simulateIgnored checks whether target would be ignored by the given pattern
// subset. For basename-scoped patterns it matches against target directly; for
// path-scoped patterns it uses the glob as a stand-in path.
func simulateIgnored(target string, rawPatterns []string, parsed []ignorePattern) bool {
	ignored := false
	for k, p := range parsed {
		if p.glob == "" {
			continue
		}
		_ = rawPatterns[k] // bounds check hint
		if p.pathScoped {
			// For path-scoped, match target as a relative path.
			matched, _ := matchIgnorePattern(p, target)
			if matched {
				ignored = !p.include
			}
		} else {
			matched, _ := filepath.Match(p.glob, target)
			if matched {
				ignored = !p.include
			}
		}
	}
	return ignored
}

// globsOverlap reports whether two globs can match the same filename.
// It checks if either matches the other's literal form, or if both are
// identical. This is a conservative heuristic — it may miss some overlaps
// but won't produce false positives.
func globsOverlap(a, b string) bool {
	if a == b {
		return true
	}
	// Check if glob a matches literal b or vice versa.
	if matched, _ := filepath.Match(a, b); matched {
		return true
	}
	if matched, _ := filepath.Match(b, a); matched {
		return true
	}
	return false
}
