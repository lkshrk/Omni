package app

import "testing"

func TestCleanExtractSubpath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "lua/plugins", want: "lua/plugins"},
		{in: " lua/plugins ", want: "lua/plugins"},
		{in: "lua/./plugins", want: "lua/plugins"},
		{in: "init.lua", want: "init.lua"},
		{in: "", wantErr: true},
		{in: ".", wantErr: true},
		{in: "..", wantErr: true},
		{in: "../etc", wantErr: true},
		{in: "/abs/path", wantErr: true},
	}
	for _, tt := range tests {
		got, err := cleanExtractSubpath(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("cleanExtractSubpath(%q) = %q, want error", tt.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("cleanExtractSubpath(%q): unexpected error %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("cleanExtractSubpath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestDeriveExtractName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		parent string
		subrel string
		want   string
	}{
		{parent: "nvim", subrel: "lua/plugins", want: "nvim-plugins"},
		{parent: "nvim", subrel: "init.lua", want: "nvim-init.lua"},
		{parent: "cfg", subrel: "weird name/x y", want: "cfg-x-y"},
	}
	for _, tt := range tests {
		if got := deriveExtractName(tt.parent, tt.subrel); got != tt.want {
			t.Errorf("deriveExtractName(%q, %q) = %q, want %q", tt.parent, tt.subrel, got, tt.want)
		}
	}
}

func TestSanitizeEntryName(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, want string }{
		{in: "ok-name_1.2", want: "ok-name_1.2"},
		{in: "with/slash", want: "with-slash"},
		{in: "spaces here", want: "spaces-here"},
		{in: "--trim--", want: "trim"},
		{in: "///", want: "extracted"},
	}
	for _, tt := range tests {
		if got := sanitizeEntryName(tt.in); got != tt.want {
			t.Errorf("sanitizeEntryName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
