package app

import (
	"testing"
)

func TestNormalizeFallbackVersion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{"v2.93.0", "2.93.0"},
		{"2.93.0", "2.93.0"},
		{"v0.1.0", "0.1.0"},
		{"V2.0.0", "V2.0.0"}, // uppercase V is not stripped — only lowercase v
		{"  v1.2.3  ", "1.2.3"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := normalizeFallbackVersion(tt.input); got != tt.want {
				t.Errorf("normalizeFallbackVersion(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFallbackVersionNewer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		candidate string
		current   string
		wantNewer bool
		wantOK    bool
	}{
		// Semver: newer major
		{"2.0.0", "1.9.9", true, true},
		// Semver: newer minor
		{"1.10.0", "1.9.0", true, true},
		// Semver: newer patch
		{"1.0.1", "1.0.0", true, true},
		// Semver: same
		{"1.0.0", "1.0.0", false, true},
		// v-prefix stripped before comparison
		{"v2.93.0", "v2.92.0", true, true},
		{"v2.93.0", "2.92.0", true, true},
		// Older
		{"1.0.0", "2.0.0", false, true},
		// Empty inputs — not comparable
		{"", "1.0.0", false, false},
		{"1.0.0", "", false, false},
		{"", "", false, false},
		// Single-component numeric
		{"15", "14", true, true},
		// Different component counts compare with zero-padding.
		{"1.2.3", "1.2", true, true},
		{"1.2", "1.2.0", false, true},
		// Non-semver strings are not guessed lexicographically.
		{"release-10", "release-9", false, false},
		// Non-parseable — no digits or no dots
		{"nightly", "stable", false, false},
	}
	for _, tt := range tests {
		name := tt.candidate + "_vs_" + tt.current
		t.Run(name, func(t *testing.T) {
			gotNewer, gotOK := fallbackVersionNewer(tt.candidate, tt.current)
			if gotNewer != tt.wantNewer || gotOK != tt.wantOK {
				t.Errorf("fallbackVersionNewer(%q, %q) = (%v, %v), want (%v, %v)",
					tt.candidate, tt.current, gotNewer, gotOK, tt.wantNewer, tt.wantOK)
			}
		})
	}
}
