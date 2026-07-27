package dots

import (
	"fmt"
	"path/filepath"
)

type AuditSeverity string

const (
	AuditWarn AuditSeverity = "warn"
)

type AuditFinding struct {
	Severity AuditSeverity `json:"severity"`
	Type     string        `json:"type"`
	Message  string        `json:"message"`
	// Indices of the patterns involved (0-based into the input slice).
	Indices  []int    `json:"indices"`
	Patterns []string `json:"patterns"`
}

// AuditIgnoreList — Examines only the caller-supplied slice; DefaultIgnores are never consulted.
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

// A pattern contradicts when a later pattern of opposite polarity nullifies its effect.
func auditContradictions(patterns []string, parsed []ignorePattern) []AuditFinding {
	var findings []AuditFinding
	for i := range parsed {
		a := parsed[i]
		if a.glob == "" {
			continue
		}
		stateAtI := simulateIgnored(a.glob, patterns[:i+1], parsed[:i+1])
		stateAtEnd := simulateIgnored(a.glob, patterns, parsed)
		if stateAtI == stateAtEnd {
			continue // pattern i's effect persists to the end
		}
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

// Redundant means covered by a broader earlier ignore with no un-ignore in between.
func auditRedundant(patterns []string, parsed []ignorePattern) []AuditFinding {
	var findings []AuditFinding
	for j := 1; j < len(parsed); j++ {
		b := parsed[j]
		if b.glob == "" || b.include {
			continue
		}
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

// An include is shadowed when nothing before it ignores the target.
func auditShadowedIncludes(patterns []string, parsed []ignorePattern) []AuditFinding {
	var findings []AuditFinding
	for j := 0; j < len(parsed); j++ {
		b := parsed[j]
		if b.glob == "" || !b.include {
			continue
		}
		if j == 0 {
			findings = append(findings, AuditFinding{
				Severity: AuditWarn,
				Type:     "shadowed",
				Message:  fmt.Sprintf("pattern %q at index %d has no effect — nothing before it ignores this path", patterns[j], j),
				Indices:  []int{j},
				Patterns: []string{patterns[j]},
			})
			continue
		}
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

// Basename-scoped patterns match target directly; path-scoped ones use the glob as a stand-in path.
func simulateIgnored(target string, rawPatterns []string, parsed []ignorePattern) bool {
	ignored := false
	for k, p := range parsed {
		if p.glob == "" {
			continue
		}
		_ = rawPatterns[k] // bounds check hint
		if p.pathScoped {
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

// Conservative: may miss overlaps, never reports a false positive.
func globsOverlap(a, b string) bool {
	if a == b {
		return true
	}
	if matched, _ := filepath.Match(a, b); matched {
		return true
	}
	if matched, _ := filepath.Match(b, a); matched {
		return true
	}
	return false
}
