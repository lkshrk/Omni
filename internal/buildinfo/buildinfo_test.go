package buildinfo

import (
	"strings"
	"testing"
)

func TestShortNonEmpty(t *testing.T) {
	if Short() == "" {
		t.Fatal("Short() must never be empty")
	}
}

func TestFullContainsShort(t *testing.T) {
	if !strings.HasPrefix(Full(), Short()) {
		t.Fatalf("Full() = %q must start with Short() = %q", Full(), Short())
	}
}

func TestFullFormatsByAvailableFields(t *testing.T) {
	_ = resolve() // record the real closure's coverage before swapping resolve
	orig := resolve
	t.Cleanup(func() { resolve = orig })

	tests := []struct {
		name string
		in   info
		want string
	}{
		{"commit and date", info{version: "v1.2.3", commit: "abc1234", date: "2026-07-17"}, "v1.2.3 (abc1234, 2026-07-17)"},
		{"commit only", info{version: "v1.2.3", commit: "abc1234"}, "v1.2.3 (abc1234)"},
		{"date without commit falls back to version", info{version: "v1.2.3", date: "2026-07-17"}, "v1.2.3"},
		{"version only", info{version: "dev"}, "dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolve = func() info { return tt.in }
			if got := Full(); got != tt.want {
				t.Fatalf("Full() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestShortReturnsResolvedVersionIgnoringCommitAndDate(t *testing.T) {
	_ = resolve()
	orig := resolve
	t.Cleanup(func() { resolve = orig })

	resolve = func() info { return info{version: "v9.9.9", commit: "deadbee", date: "2026-01-01"} }
	if got := Short(); got != "v9.9.9" {
		t.Fatalf("Short() = %q, want %q", got, "v9.9.9")
	}
}
