package apm

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type OutdatedRow struct {
	Package string
	Current string
	Latest  string
	Source  string
}

type OutdatedResult struct {
	Rows    []OutdatedRow
	Unknown int
}

var ansiEscape = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
var outdatedSummary = regexp.MustCompile(`(?m)\b([0-9]+) outdated dependenc(?:y|ies) found\b`)
var plainStatusField = regexp.MustCompile(`(?:^|\s)(up-to-date|outdated|unknown)(?:\s|$)`)

// Outdated checks the pinned APM global lockfile without changing it.
func (c *Client) Outdated(ctx context.Context) (OutdatedResult, error) {
	result, err := c.runEnv(ctx, []string{"NO_COLOR=1", "COLUMNS=240", "TERM=dumb"}, "outdated", "-g", "--parallel-checks", "4")
	if err != nil {
		return OutdatedResult{}, err
	}
	parsed, reported := parseOutdated(result.Stdout + "\n" + result.Stderr)
	if reported >= 0 && reported != len(parsed.Rows) {
		return OutdatedResult{}, fmt.Errorf("parse apm outdated output: summary reported %d updates, parsed %d", reported, len(parsed.Rows))
	}
	return parsed, nil
}

func parseOutdated(output string) (OutdatedResult, int) {
	var result OutdatedResult
	reported := -1
	clean := ansiEscape.ReplaceAllString(output, "")
	if match := outdatedSummary.FindStringSubmatch(clean); len(match) == 2 {
		reported, _ = strconv.Atoi(match[1])
	}
	var plainHeader []int
	for _, raw := range strings.Split(clean, "\n") {
		if strings.Contains(raw, "│") {
			var fields []string
			for _, field := range strings.Split(raw, "│") {
				if field = strings.TrimSpace(field); field != "" {
					fields = append(fields, field)
				}
			}
			if len(fields) == 5 {
				appendOutdatedRow(&result, fields[0], fields[1], fields[2], fields[3], fields[4])
			}
			continue
		}
		if strings.HasPrefix(raw, "Package") {
			plainHeader = []int{strings.Index(raw, "Package"), strings.Index(raw, "Current"), strings.Index(raw, "Latest"), strings.Index(raw, "Status"), strings.Index(raw, "Source")}
			continue
		}
		if len(plainHeader) != 5 || len(raw) <= plainHeader[2] {
			continue
		}
		packageID := sliceColumn(raw, plainHeader[0], plainHeader[1])
		current := sliceColumn(raw, plainHeader[1], plainHeader[2])
		tail := raw[plainHeader[2]:]
		status, at := plainStatus(tail, max(plainHeader[3]-plainHeader[2], 0))
		if at < 0 {
			continue
		}
		appendOutdatedRow(&result, packageID, current, strings.TrimSpace(tail[:at]), status, strings.TrimSpace(tail[at+len(status):]))
	}
	return result, reported
}

func sliceColumn(line string, start, end int) string {
	if start < 0 || start >= len(line) {
		return ""
	}
	return strings.TrimSpace(line[start:min(end, len(line))])
}

func plainStatus(tail string, minimum int) (string, int) {
	if minimum >= len(tail) {
		return "", -1
	}
	match := plainStatusField.FindStringSubmatchIndex(tail[minimum:])
	if len(match) >= 4 {
		return tail[minimum+match[2] : minimum+match[3]], minimum + match[2]
	}
	return "", -1
}

func appendOutdatedRow(result *OutdatedResult, packageID, current, latest, status, source string) {
	switch status {
	case "unknown":
		result.Unknown++
	case "outdated":
		result.Rows = append(result.Rows, OutdatedRow{Package: packageID, Current: current, Latest: latest, Source: source})
	}
}
