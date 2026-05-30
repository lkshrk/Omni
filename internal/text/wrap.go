// Package text provides shared text-processing utilities.
package text

import (
	"fmt"
	"strings"
)

// WrapText splits s into lines of at most width runes, breaking at word
// boundaries. Width is measured in runes so multibyte characters count as one
// visual column. Returns nil for empty or whitespace-only input.
func WrapText(s string, width int) []string {
	if s == "" || width <= 0 {
		return nil
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}
	var out []string
	line := words[0]
	lineLen := len([]rune(line))
	for _, w := range words[1:] {
		wLen := len([]rune(w))
		if lineLen+1+wLen <= width {
			line += " " + w
			lineLen += 1 + wLen
		} else {
			out = append(out, strings.TrimRight(line, " "))
			line = w
			lineLen = wLen
		}
	}
	return append(out, strings.TrimRight(line, " "))
}

func PluralCount(count int, singular, plural string) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", singular)
	}
	return fmt.Sprintf("%d %s", count, plural)
}
