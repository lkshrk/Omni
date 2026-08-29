package app

import (
	"fmt"
	"regexp"
	"strings"
)

// A user-managed symlink shadowing a package file is intended precedence, but APM reports each one
// as a multi-line containment rejection.
var (
	shadowRejectPattern = regexp.MustCompile(`^\[!\] \[[^\]]+\] Rejected \S+ target path:`)
	apmLogMarker        = regexp.MustCompile(`^\[[^\]]\] `)
)

const shadowContainmentPhrase = "outside the allowed base directory"

// IsAPMNoticeLine reports APM's own marked output lines, the only summary a raw apm run offers.
func IsAPMNoticeLine(line string) bool {
	return apmLogMarker.MatchString(strings.TrimSpace(line))
}

// Returns the output with the rejection blocks removed and how many files they reported shadowed.
func collapseShadowWarnings(output string) (string, int) {
	lines := strings.Split(output, "\n")
	kept := make([]string, 0, len(lines))
	shadowed := 0
	for i := 0; i < len(lines); i++ {
		if !shadowRejectPattern.MatchString(strings.TrimSpace(lines[i])) {
			kept = append(kept, lines[i])
			continue
		}
		shadowed++
		i = shadowWarningEnd(lines, i)
	}
	if shadowed == 0 {
		return output, 0
	}
	body := strings.TrimRight(strings.Join(kept, "\n"), "\n")
	if body != "" {
		body += "\n"
	}
	return body, shadowed
}

func shadowedFilesNote(shadowed int) string {
	return fmt.Sprintf("note: %d package file(s) shadowed by user-managed files", shadowed)
}

func shadowWarningEnd(lines []string, start int) int {
	for i := start; i < len(lines); i++ {
		if strings.Contains(lines[i], shadowContainmentPhrase) {
			return i
		}
		if i > start && apmLogMarker.MatchString(strings.TrimSpace(lines[i])) {
			return i - 1
		}
	}
	return len(lines) - 1
}
