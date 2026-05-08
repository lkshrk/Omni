package tui

import (
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
)

// ProgramOptions returns Bubble Tea options for the interactive TUI.
// omni is only launched as an interactive program, so use the terminal
// environment profile directly instead of relying on stdout probing. That keeps
// colors available in PTY wrappers and recorders that proxy stdout.
func ProgramOptions() []tea.ProgramOption {
	return []tea.ProgramOption{
		tea.WithColorProfile(colorprofile.Env(os.Environ())),
	}
}
