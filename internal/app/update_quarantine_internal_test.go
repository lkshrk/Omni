package app

import (
	"strings"
	"testing"
	"time"
)

func TestParseQuarantineDuration(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    time.Duration
		wantErr bool
	}{
		{name: "empty disables", raw: "", want: 0},
		{name: "zero disables", raw: "0", want: 0},
		{name: "zero days disables", raw: "0d", want: 0},
		{name: "days", raw: "2d", want: 48 * time.Hour},
		{name: "go duration", raw: "36h", want: 36 * time.Hour},
		{name: "trim and lowercase", raw: " 1D ", want: 24 * time.Hour},
		{name: "negative days rejected", raw: "-1d", wantErr: true},
		{name: "negative duration rejected", raw: "-1h", wantErr: true},
		{name: "invalid duration rejected", raw: "soon", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseQuarantineDuration(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseQuarantineDuration(%q) err = nil, want error", tt.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseQuarantineDuration(%q): %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("parseQuarantineDuration(%q) = %s, want %s", tt.raw, got, tt.want)
			}
		})
	}
}

func TestQuarantineBlockedErrorMessages(t *testing.T) {
	blockedUntil := time.Date(2026, 6, 7, 10, 30, 0, 0, time.UTC)
	tests := []struct {
		name     string
		decision updateQuarantineDecision
		want     string
	}{
		{
			name:     "missing metadata",
			decision: updateQuarantineDecision{Reason: UpdateBlockMetadataMissing},
			want:     "package-manager update metadata unavailable",
		},
		{
			name:     "quarantined with deadline",
			decision: updateQuarantineDecision{Reason: UpdateBlockQuarantined, BlockedUntil: &blockedUntil},
			want:     "update quarantined until 2026-06-07T10:30:00Z",
		},
		{
			name:     "quarantined without deadline",
			decision: updateQuarantineDecision{Reason: UpdateBlockQuarantined},
			want:     "update quarantined; use --force",
		},
		{
			name:     "generic block",
			decision: updateQuarantineDecision{Reason: "custom"},
			want:     "update blocked by quarantine",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := quarantineBlockedError("ripgrep", tt.decision)
			if err == nil {
				t.Fatal("quarantineBlockedError returned nil")
			}
			if got := err.Error(); !strings.Contains(got, tt.want) {
				t.Fatalf("error = %q, want substring %q", got, tt.want)
			}
		})
	}
}
