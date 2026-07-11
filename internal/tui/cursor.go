package tui

// cursorMove returns cur shifted by delta over a list of count items. When
// count <= 0 it returns 0. When wrap is true the result wraps modulo count
// in both directions (matching bubbletea's existing keyboard j/k behavior
// on the tools and dots lists); when wrap is false it clamps to [0, count-1]
// (matching picker/popup behavior, which never wraps).
func cursorMove(cur, delta, count int, wrap bool) int {
	if count <= 0 {
		return 0
	}
	if wrap {
		next := (cur + delta) % count
		if next < 0 {
			next += count
		}
		return next
	}
	return clampIndex(cur+delta, count)
}
