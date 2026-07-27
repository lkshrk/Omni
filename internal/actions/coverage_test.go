package actions

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoverageMatrixListsEveryAction(t *testing.T) {
	rows := readCoverageMatrix(t)
	all := All()
	for _, action := range all {
		row, ok := rows[action.ID]
		if !ok {
			t.Fatalf("docs/test-matrix.md is missing action %s", action.ID)
		}
		for _, field := range []struct {
			name  string
			value string
		}{
			{name: "app/shared", value: row.app},
			{name: "CLI unit", value: row.cliUnit},
			{name: "CLI integration", value: row.cliIntegration},
			{name: "TUI flow/render", value: row.tui},
		} {
			if !validCoverageStatus(field.value) {
				t.Fatalf("%s has invalid %s status %q", action.ID, field.name, field.value)
			}
		}
		if row.hasGapStatus() && strings.TrimSpace(row.gap) == "-" {
			t.Fatalf("%s has a gap/partial status but no next fixture note", action.ID)
		}
	}
	assertCoverageProseCount(t, len(all))
	if len(rows) != len(all) {
		known := map[ID]bool{}
		for _, action := range all {
			known[action.ID] = true
		}
		for id := range rows {
			if !known[id] {
				t.Fatalf("docs/test-matrix.md lists unknown action %s", id)
			}
		}
	}
}

type coverageRow struct {
	app            string
	cliUnit        string
	cliIntegration string
	tui            string
	gap            string
}

func (r coverageRow) hasGapStatus() bool {
	for _, status := range []string{r.app, r.cliUnit, r.cliIntegration, r.tui} {
		if status == "partial" || status == "gap" {
			return true
		}
	}
	return false
}

// Assert the prose count separately because it has drifted despite rows being pinned to All().
func assertCoverageProseCount(t *testing.T, want int) {
	t.Helper()
	path := filepath.Join("..", "..", "docs", "test-matrix.md")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	claim := fmt.Sprintf("the %d user-visible actions", want)
	if !strings.Contains(string(body), claim) {
		t.Fatalf("docs/test-matrix.md does not state %q; update the prose count to match All()", claim)
	}
}

func readCoverageMatrix(t *testing.T) map[ID]coverageRow {
	t.Helper()
	path := filepath.Join("..", "..", "docs", "test-matrix.md")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	rows := map[ID]coverageRow{}
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		cells := splitMarkdownRow(line)
		if len(cells) != 6 {
			t.Fatalf("coverage row should have 6 cells, got %d: %s", len(cells), line)
		}
		id := ID(strings.Trim(cells[0], "`"))
		if id == "" {
			t.Fatalf("coverage row has empty action ID: %s", line)
		}
		if _, exists := rows[id]; exists {
			t.Fatalf("duplicate coverage row for %s", id)
		}
		rows[id] = coverageRow{
			app:            coverageStatus(cells[1]),
			cliUnit:        coverageStatus(cells[2]),
			cliIntegration: coverageStatus(cells[3]),
			tui:            coverageStatus(cells[4]),
			gap:            strings.TrimSpace(cells[5]),
		}
	}
	if len(rows) == 0 {
		t.Fatal("docs/test-matrix.md has no action rows")
	}
	return rows
}

func splitMarkdownRow(line string) []string {
	trimmed := strings.Trim(line, "|")
	parts := strings.Split(trimmed, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func coverageStatus(cell string) string {
	fields := strings.Fields(strings.TrimSpace(cell))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func validCoverageStatus(status string) bool {
	switch status {
	case "yes", "partial", "gap", "n/a":
		return true
	default:
		return false
	}
}
