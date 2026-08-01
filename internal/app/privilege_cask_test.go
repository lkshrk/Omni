package app

import (
	"testing"

	"github.com/lkshrk/omni/internal/provider"
)

func TestBrewCaskMayPromptForPassword(t *testing.T) {
	t.Parallel()
	cases := []struct {
		reason string
		want   bool
	}{
		{"brew cask parsec uses a pkg installer", true},
		{"brew cask parsec uses pkgutil uninstall", true},
		{"brew cask battle-net runs an installer that may need sudo", true},
		{"brew cask stats unloads a launchctl service", true},
		{"brew cask obs deletes system files during uninstall", true},
		{"brew formula ripgrep needs sudo", false},
		{"", false},
	}
	for _, tc := range cases {
		got := brewCaskMayPromptForPassword(provider.PrivilegePlan{Requirement: provider.PrivilegeMaybe, Reason: tc.reason})
		if got != tc.want {
			t.Errorf("brewCaskMayPromptForPassword(%q) = %v, want %v", tc.reason, got, tc.want)
		}
	}
}
