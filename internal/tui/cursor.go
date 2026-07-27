package tui

// wrap true wraps modulo count in both directions (the tools/dots list j/k behavior); wrap false clamps to [0, count-1] (picker/popup behavior). count <= 0 returns 0.
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
