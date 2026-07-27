package tui

import "strings"

// The selected row is kept inside a comfort band: the window scrolls once the cursor would move within roughly 1/5 of the viewport of the bottom edge.
func applyScrollWindow(content string, cursorLine, avail int) string {
	if avail < 1 {
		avail = 1
	}

	lines := strings.Split(content, "\n")
	// strings.Split on "a\nb\n" yields ["a","b",""] — drop the trailing empty entry so the line count is accurate.
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
