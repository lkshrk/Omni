package tui

import "testing"

func TestCursorMove(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		cur   int
		delta int
		count int
		wrap  bool
		want  int
	}{
		{"wrap down past end", 2, 1, 3, true, 0},
		{"wrap up past start", 0, -1, 3, true, 2},
		{"wrap large negative delta", 0, -7, 3, true, 2},
		{"wrap large positive delta", 0, 7, 3, true, 1},
		{"clamp down past end stays at last", 2, 1, 3, false, 2},
		{"clamp up past start stays at 0", 0, -1, 3, false, 0},
		{"clamp mid-range moves normally", 1, 1, 3, false, 2},
		{"wrap mid-range moves normally", 1, 1, 3, true, 2},
		{"count zero returns 0 wrap", 0, 1, 0, true, 0},
		{"count zero returns 0 clamp", 0, 1, 0, false, 0},
		{"count one wrap stays at 0", 0, 1, 1, true, 0},
		{"count one clamp stays at 0", 0, -1, 1, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cursorMove(tt.cur, tt.delta, tt.count, tt.wrap)
			if got != tt.want {
				t.Errorf("cursorMove(%d, %d, %d, %v) = %d, want %d", tt.cur, tt.delta, tt.count, tt.wrap, got, tt.want)
			}
		})
	}
}

func TestClampIndex(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		cur   int
		count int
		want  int
	}{
		{"negative clamps to 0", -3, 5, 0},
		{"over clamps to last", 9, 5, 4},
		{"in range unchanged", 2, 5, 2},
		{"count zero returns 0", 3, 0, 0},
		{"count one clamps to 0", 5, 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clampIndex(tt.cur, tt.count)
			if got != tt.want {
				t.Errorf("clampIndex(%d, %d) = %d, want %d", tt.cur, tt.count, got, tt.want)
			}
		})
	}
}
