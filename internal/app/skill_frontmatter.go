package app

import (
	"os"
	"path/filepath"
	"strings"
)

// skillDescription reads the description field from a skill's SKILL.md YAML
// frontmatter. Returns "" on any read or parse failure.
func skillDescription(home, skillName string) string {
	path := filepath.Join(home, ".agents", "skills", skillName, "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return parseFrontmatterDescription(string(data))
}

// parseFrontmatterDescription extracts the "description:" value from YAML
// frontmatter delimited by "---" lines at the start of content. Supports two
// shapes: a block scalar ("description: |", followed by indented lines) and
// a single-line quoted or bare scalar.
func parseFrontmatterDescription(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}

	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return ""
	}
	frontmatter := lines[1:end]

	for i, line := range lines[:end] {
		trimmed := strings.TrimLeft(line, " \t")
		if !strings.HasPrefix(trimmed, "description:") {
			continue
		}
		keyIndent := len(line) - len(trimmed)
		value := strings.TrimSpace(strings.TrimPrefix(trimmed, "description:"))

		if value == "|" || strings.HasPrefix(value, "|") {
			return parseBlockScalar(frontmatter, i-1, keyIndent)
		}
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			return value[1 : len(value)-1]
		}
		if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
			return value[1 : len(value)-1]
		}
		return value
	}
	return ""
}

// parseBlockScalar collects the indented lines following a "description: |"
// key (at frontmatterIdx within the frontmatter slice, indented keyIndent
// spaces), joining them with spaces until a line at or below keyIndent ends
// the block.
func parseBlockScalar(frontmatter []string, keyLineIdx, keyIndent int) string {
	var parts []string
	for i := keyLineIdx + 1; i < len(frontmatter); i++ {
		line := frontmatter[i]
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if indent <= keyIndent {
			break
		}
		parts = append(parts, strings.TrimSpace(line))
	}
	return strings.Join(parts, " ")
}
