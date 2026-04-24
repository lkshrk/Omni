package dots_test

import (
	"testing"

	"github.com/lkshrk/omni/internal/dots"
)

func TestCommitMessageFromStatus(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   string
	}{
		{
			name:   "empty status",
			status: "",
			want:   "dots: update",
		},
		{
			name:   "whitespace only",
			status: "   \n  ",
			want:   "dots: update",
		},
		{
			name:   "single file",
			status: "M  zshrc",
			want:   "dots: update zshrc",
		},
		{
			name:   "leading dot stripped",
			status: "M  .zshrc",
			want:   "dots: update zshrc",
		},
		{
			name:   "nested path uses first segment",
			status: "M  nvim/init.lua",
			want:   "dots: update nvim",
		},
		{
			name:   "two unique top-level names",
			status: " M nvim/init.lua\n M zshrc",
			want:   "dots: update nvim, zshrc",
		},
		{
			name:   "three unique names",
			status: " M nvim/init.lua\n M zshrc\n M gitconfig",
			want:   "dots: update nvim, zshrc, gitconfig",
		},
		{
			name:   "four names truncates with +N more",
			status: " M nvim/init.lua\n M zshrc\n M gitconfig\n M tmux/tmux.conf",
			want:   "dots: update nvim, zshrc, gitconfig + 1 more",
		},
		{
			name:   "six names shows +3 more",
			status: " M a\n M b\n M c\n M d\n M e\n M f",
			want:   "dots: update a, b, c + 3 more",
		},
		{
			name:   "duplicate paths deduplicated",
			status: "M  nvim/init.lua\nA  nvim/lua/plugins.lua",
			want:   "dots: update nvim",
		},
		{
			name:   "rename arrow uses new path",
			status: "R  old/file -> nvim/new.lua",
			want:   "dots: update nvim",
		},
		{
			name:   "short lines ignored",
			status: "?\n\nM  zsh",
			want:   "dots: update zsh",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := dots.CommitMessageFromStatus(tc.status)
			if got != tc.want {
				t.Errorf("CommitMessageFromStatus(%q) = %q, want %q", tc.status, got, tc.want)
			}
		})
	}
}
