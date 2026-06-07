package app

import (
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
