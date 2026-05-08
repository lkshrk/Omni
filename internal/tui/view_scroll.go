package tui

import "strings"

// applyScrollWindow clips a fully-rendered content string to the visible
// viewport window. cursorLine is the 0-based line index of the cursor row
// (first line of the selected item) within the full content. avail is the
// number of terminal lines available for content.
//
// The selected row is kept inside a comfort band: once it would move closer
// than roughly 1/5 of the viewport to the bottom edge, the window scrolls.
// When the cursor is near the top the view starts at line 0.
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

	start, end := scrollWindowBounds(len(lines), cursorLine, avail)
	return strings.Join(lines[start:end], "\n") + "\n"
}

func scrollWindowBounds(totalLines, cursorLine, avail int) (int, int) {
	if avail < 1 {
		avail = 1
	}
	if totalLines <= 0 {
		return 0, 0
	}
	margin := scrollComfortMargin(avail)
	anchorRow := max(avail-margin-1, 0)
	start := cursorLine - anchorRow
	if start < 0 {
		start = 0
	}
	end := start + avail
	if end > totalLines {
		end = totalLines
		start = max(0, end-avail)
	}
	return start, end
}

func scrollComfortMargin(avail int) int {
	if avail <= 1 {
		return 0
	}
	return max(avail/5, 1)
}
