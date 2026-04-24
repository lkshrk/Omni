package tui

import "strings"

// applyScrollWindow clips a fully-rendered content string to the visible
// viewport window. cursorLine is the 0-based line index of the cursor row
// (first line of the selected item) within the full content. avail is the
// number of terminal lines available for content.
//
// The algorithm mirrors renderList: the cursor row is anchored to the bottom
// of the viewport, scrolling up when the cursor approaches the end of the
// content. When the cursor is near the top the view starts at line 0.
func applyScrollWindow(content string, cursorLine, avail int) string {
	if avail < 1 {
		avail = 1
	}

	lines := strings.Split(content, "\n")
	// strings.Split on "a\nb\n" yields ["a","b",""] — drop the trailing empty
	// entry so our line count is accurate.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	if len(lines) == 0 {
		return ""
	}

	// Anchor the cursor to the bottom of the visible area.
	start := cursorLine - avail + 1
	if start < 0 {
		start = 0
	}
	end := start + avail
	if end > len(lines) {
		end = len(lines)
		start = max(0, end-avail)
	}

	return strings.Join(lines[start:end], "\n") + "\n"
}
